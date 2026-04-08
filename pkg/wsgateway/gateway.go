package wsgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/hugr-lab/hub/pkg/auth"

	"nhooyr.io/websocket"
)

// ConversationInfo holds the routing info for a conversation.
type ConversationInfo struct {
	ID              string
	UserID          string
	Mode            string // "llm", "tools", "agent"
	AgentInstanceID string
	Model           string
}

// ConversationLookup resolves conversation info by ID.
type ConversationLookup func(ctx context.Context, conversationID string) (*ConversationInfo, error)

// LLMHandler handles pure LLM chat (no tools).
type LLMHandler func(ctx context.Context, model string, conversationID string, message string) (string, error)

// ToolsHandler handles LLM + Hugr tools chat.
type ToolsHandler func(ctx context.Context, userID string, conversationID string, message string) (string, error)

// AgentHandler sends a message to an agent container. Returns response.
type AgentHandler func(ctx context.Context, instanceID, conversationID, userID, message string) (string, error)

// MessagePersister saves messages to DB.
type MessagePersister func(ctx context.Context, conversationID, role, content string)

// Gateway is a WebSocket relay between browser clients and backend (LLM/tools/agent).
type Gateway struct {
	lookup  ConversationLookup
	llm     LLMHandler
	tools   ToolsHandler
	agent   AgentHandler
	persist MessagePersister
	logger  *slog.Logger

	mu    sync.Mutex
	conns map[string]*websocket.Conn // conversationID → active connection
}

// Config holds all handlers for the gateway.
type Config struct {
	Lookup  ConversationLookup
	LLM     LLMHandler
	Tools   ToolsHandler
	Agent   AgentHandler
	Persist MessagePersister
	Logger  *slog.Logger
}

func New(cfg Config) *Gateway {
	return &Gateway{
		lookup:  cfg.Lookup,
		llm:     cfg.LLM,
		tools:   cfg.Tools,
		agent:   cfg.Agent,
		persist: cfg.Persist,
		logger:  cfg.Logger,
		conns:   make(map[string]*websocket.Conn),
	}
}

// Handler returns an http.Handler for /ws/{conversation_id}.
func (g *Gateway) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract conversation_id from path: /ws/{conversation_id}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/ws/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			http.Error(w, "conversation_id required in path", http.StatusBadRequest)
			return
		}
		conversationID := parts[0]

		// Get user from auth context
		authUser, ok := auth.UserFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: []string{"*"},
		})
		if err != nil {
			g.logger.Error("websocket accept failed", "error", err)
			return
		}
		defer conn.CloseNow()

		// Track connection by conversation
		var prev *websocket.Conn
		g.mu.Lock()
		if old, ok := g.conns[conversationID]; ok {
			prev = old
		}
		g.conns[conversationID] = conn
		g.mu.Unlock()
		if prev != nil {
			prev.Close(websocket.StatusGoingAway, "replaced by new connection")
		}

		defer func() {
			g.mu.Lock()
			if g.conns[conversationID] == conn {
				delete(g.conns, conversationID)
			}
			g.mu.Unlock()
		}()

		g.logger.Info("websocket connected", "user", authUser.ID, "conversation", conversationID)

		ctx := auth.ContextWithUser(r.Context(), authUser)
		g.readLoop(ctx, conn, authUser.ID, conversationID)
	})
}

// ChatMessage is the wire format for WebSocket messages.
type ChatMessage struct {
	Type    string `json:"type"`    // "message", "response", "error", "status"
	Content string `json:"content"` // message text or status info
}

func (g *Gateway) readLoop(ctx context.Context, conn *websocket.Conn, userID, conversationID string) {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				g.logger.Info("websocket closed", "conversation", conversationID)
			} else {
				g.logger.Warn("websocket read error", "conversation", conversationID, "error", err)
			}
			return
		}

		var msg ChatMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			g.writeJSON(ctx, conn, ChatMessage{Type: "error", Content: "invalid message format"})
			continue
		}

		switch msg.Type {
		case "message":
			g.handleMessage(ctx, conn, userID, conversationID, msg.Content)
		default:
			g.writeJSON(ctx, conn, ChatMessage{Type: "error", Content: fmt.Sprintf("unknown type: %s", msg.Type)})
		}
	}
}

func (g *Gateway) handleMessage(ctx context.Context, conn *websocket.Conn, userID, conversationID, content string) {
	g.logger.Info("chat message", "user", userID, "conversation", conversationID, "length", len(content))

	// Persist user message
	if g.persist != nil {
		g.persist(ctx, conversationID, "user", content)
	}

	// Lookup conversation to determine mode
	conv, err := g.lookup(ctx, conversationID)
	if err != nil {
		g.writeJSON(ctx, conn, ChatMessage{Type: "error", Content: fmt.Sprintf("conversation not found: %v", err)})
		return
	}

	g.writeJSON(ctx, conn, ChatMessage{Type: "status", Content: "thinking"})

	var response string

	switch conv.Mode {
	case "llm":
		if g.llm != nil {
			response, err = g.llm(ctx, conv.Model, conversationID, content)
		} else {
			err = fmt.Errorf("LLM mode not configured")
		}

	case "tools":
		if g.tools != nil {
			response, err = g.tools(ctx, userID, conversationID, content)
		} else {
			err = fmt.Errorf("tools mode not configured")
		}

	case "agent":
		if g.agent != nil {
			response, err = g.agent(ctx, conv.AgentInstanceID, conversationID, userID, content)
		}
		// Fallback to tools if agent handler fails or not connected
		if g.agent == nil || err != nil {
			if err != nil {
				g.logger.Warn("agent unavailable, falling back to tools", "instance", conv.AgentInstanceID, "error", err)
				g.writeJSON(ctx, conn, ChatMessage{Type: "status", Content: "agent offline, using tools mode"})
			}
			if g.tools != nil {
				response, err = g.tools(ctx, userID, conversationID, content)
			} else {
				err = fmt.Errorf("no handler available")
			}
		}

	default:
		err = fmt.Errorf("unknown mode: %s", conv.Mode)
	}

	if err != nil {
		g.writeJSON(ctx, conn, ChatMessage{Type: "error", Content: err.Error()})
		return
	}

	// Persist assistant response
	if g.persist != nil {
		g.persist(ctx, conversationID, "assistant", response)
	}

	g.writeJSON(ctx, conn, ChatMessage{Type: "response", Content: response})
}

func (g *Gateway) writeJSON(ctx context.Context, conn *websocket.Conn, msg ChatMessage) {
	data, _ := json.Marshal(msg)
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		g.logger.Warn("websocket write error", "error", err)
	}
}

// SendToConversation sends a server-initiated message to a connected conversation.
func (g *Gateway) SendToConversation(ctx context.Context, conversationID string, msg ChatMessage) error {
	g.mu.Lock()
	conn, ok := g.conns[conversationID]
	g.mu.Unlock()

	if !ok {
		return fmt.Errorf("conversation %s not connected", conversationID)
	}

	g.writeJSON(ctx, conn, msg)
	return nil
}
