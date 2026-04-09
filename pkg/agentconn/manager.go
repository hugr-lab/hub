// Package agentconn manages WebSocket connections from agent containers to Hub Service.
// Each agent container connects on startup; Hub Service routes messages to the correct agent.
package agentconn

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hugr-lab/hub/pkg/auth"

	"nhooyr.io/websocket"
)

// AgentMessage is the wire format between Hub Service and agent containers.
type AgentMessage struct {
	RequestID      string        `json:"request_id"`
	ConversationID string        `json:"conversation_id,omitempty"`
	UserID         string        `json:"user_id,omitempty"`
	Type           string        `json:"type"` // "request", "response", "status", "error", "ping", "pong"
	Content        string        `json:"content,omitempty"`
	Messages       []ChatMessage `json:"messages,omitempty"` // full conversation history for request type
}

// ChatMessage is a single message in conversation history (matches wsgateway.LLMMessage).
type ChatMessage struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCalls  any    `json:"tool_calls,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// agentConn tracks a single agent WebSocket connection.
type agentConn struct {
	instanceID string
	userID     string
	conn       *websocket.Conn
	connected  time.Time
	pending    map[string]chan AgentMessage // request_id → response channel
	mu         sync.Mutex
}

// Manager handles agent container WebSocket connections.
type Manager struct {
	mu     sync.RWMutex
	agents map[string]*agentConn // instance_id → connection
	logger *slog.Logger

	heartbeatInterval time.Duration
	requestTimeout    time.Duration
}

// NewManager creates a new agent connection manager.
func NewManager(logger *slog.Logger) *Manager {
	return &Manager{
		agents:            make(map[string]*agentConn),
		logger:            logger,
		heartbeatInterval: 30 * time.Second,
		requestTimeout:    60 * time.Second,
	}
}

// Handler returns an HTTP handler for /agent/ws/{instance_id}.
func (m *Manager) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/agent/ws/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			http.Error(w, "instance_id required", http.StatusBadRequest)
			return
		}
		instanceID := parts[0]

		authUser, ok := auth.UserFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: []string{"*"},
		})
		if err != nil {
			m.logger.Error("agent websocket accept failed", "error", err)
			return
		}

		ac := &agentConn{
			instanceID: instanceID,
			userID:     authUser.ID,
			conn:       conn,
			connected:  time.Now(),
			pending:    make(map[string]chan AgentMessage),
		}

		// Replace existing connection if any
		m.mu.Lock()
		if old, ok := m.agents[instanceID]; ok {
			old.conn.Close(websocket.StatusGoingAway, "replaced")
		}
		m.agents[instanceID] = ac
		m.mu.Unlock()

		m.logger.Info("agent connected", "instance", instanceID, "user", authUser.ID)

		defer func() {
			m.mu.Lock()
			if m.agents[instanceID] == ac {
				delete(m.agents, instanceID)
			}
			m.mu.Unlock()
			conn.CloseNow()
			m.logger.Info("agent disconnected", "instance", instanceID)
		}()

		m.readLoop(r.Context(), ac)
	})
}

// readLoop reads messages from the agent container.
func (m *Manager) readLoop(ctx context.Context, ac *agentConn) {
	for {
		_, data, err := ac.conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
				m.logger.Warn("agent read error", "instance", ac.instanceID, "error", err)
			}
			return
		}

		var msg AgentMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			m.logger.Warn("agent invalid message", "instance", ac.instanceID, "error", err)
			continue
		}

		switch msg.Type {
		case "pong":
			// heartbeat response — nothing to do
		case "response", "status", "error":
			// Route to pending request
			ac.mu.Lock()
			ch, ok := ac.pending[msg.RequestID]
			ac.mu.Unlock()
			if ok {
				select {
				case ch <- msg:
				default:
				}
			}
		default:
			m.logger.Warn("unknown agent message type", "type", msg.Type, "instance", ac.instanceID)
		}
	}
}

// SendMessage sends a message to an agent and waits for a response.
// messages contains the full conversation history.
func (m *Manager) SendMessage(ctx context.Context, instanceID, conversationID, userID string, messages []ChatMessage) (string, error) {
	m.mu.RLock()
	ac, ok := m.agents[instanceID]
	m.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("agent %s not connected", instanceID)
	}

	requestID := fmt.Sprintf("req-%d", time.Now().UnixNano())
	ch := make(chan AgentMessage, 10) // buffered for status + response

	ac.mu.Lock()
	ac.pending[requestID] = ch
	ac.mu.Unlock()

	defer func() {
		ac.mu.Lock()
		delete(ac.pending, requestID)
		ac.mu.Unlock()
	}()

	// Extract last user message as content
	var content string
	if len(messages) > 0 {
		content = messages[len(messages)-1].Content
	}

	// Send request to agent with full history
	msg := AgentMessage{
		RequestID:      requestID,
		ConversationID: conversationID,
		UserID:         userID,
		Type:           "request",
		Content:        content,
		Messages:       messages,
	}
	data, _ := json.Marshal(msg)
	if err := ac.conn.Write(ctx, websocket.MessageText, data); err != nil {
		return "", fmt.Errorf("send to agent: %w", err)
	}

	// Wait for response
	timeout := time.After(m.requestTimeout)
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timeout:
			return "", fmt.Errorf("agent %s did not respond within %v", instanceID, m.requestTimeout)
		case resp := <-ch:
			switch resp.Type {
			case "response":
				return resp.Content, nil
			case "error":
				return "", fmt.Errorf("agent error: %s", resp.Content)
			case "status":
				// Intermediate status — continue waiting
				continue
			}
		}
	}
}

// SendMessageStream sends a message and streams intermediate results via callback.
func (m *Manager) SendMessageStream(ctx context.Context, instanceID, conversationID, userID string, messages []ChatMessage, onStatus func(string)) (string, error) {
	m.mu.RLock()
	ac, ok := m.agents[instanceID]
	m.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("agent %s not connected", instanceID)
	}

	requestID := fmt.Sprintf("req-%d", time.Now().UnixNano())
	ch := make(chan AgentMessage, 10)

	ac.mu.Lock()
	ac.pending[requestID] = ch
	ac.mu.Unlock()

	defer func() {
		ac.mu.Lock()
		delete(ac.pending, requestID)
		ac.mu.Unlock()
	}()

	var content string
	if len(messages) > 0 {
		content = messages[len(messages)-1].Content
	}

	msg := AgentMessage{
		RequestID:      requestID,
		ConversationID: conversationID,
		UserID:         userID,
		Type:           "request",
		Content:        content,
		Messages:       messages,
	}
	data, _ := json.Marshal(msg)
	if err := ac.conn.Write(ctx, websocket.MessageText, data); err != nil {
		return "", fmt.Errorf("send to agent: %w", err)
	}

	timeout := time.After(m.requestTimeout)
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timeout:
			return "", fmt.Errorf("agent %s did not respond within %v", instanceID, m.requestTimeout)
		case resp := <-ch:
			switch resp.Type {
			case "response":
				return resp.Content, nil
			case "error":
				return "", fmt.Errorf("agent error: %s", resp.Content)
			case "status":
				if onStatus != nil {
					onStatus(resp.Content)
				}
			}
		}
	}
}

// IsConnected checks if an agent instance has an active WebSocket connection.
func (m *Manager) IsConnected(instanceID string) bool {
	m.mu.RLock()
	_, ok := m.agents[instanceID]
	m.mu.RUnlock()
	return ok
}

// ConnectedInstances returns the list of currently connected agent instance IDs.
func (m *Manager) ConnectedInstances() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.agents))
	for id := range m.agents {
		ids = append(ids, id)
	}
	return ids
}

// StartHeartbeat begins periodic ping checks for all connected agents.
func (m *Manager) StartHeartbeat(ctx context.Context) {
	ticker := time.NewTicker(m.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.pingAll(ctx)
		}
	}
}

func (m *Manager) pingAll(ctx context.Context) {
	m.mu.RLock()
	agents := make([]*agentConn, 0, len(m.agents))
	for _, ac := range m.agents {
		agents = append(agents, ac)
	}
	m.mu.RUnlock()

	for _, ac := range agents {
		msg := AgentMessage{Type: "ping"}
		data, _ := json.Marshal(msg)
		writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := ac.conn.Write(writeCtx, websocket.MessageText, data); err != nil {
			m.logger.Warn("heartbeat failed, disconnecting", "instance", ac.instanceID, "error", err)
			ac.conn.Close(websocket.StatusGoingAway, "heartbeat failed")
		}
		cancel()
	}
}
