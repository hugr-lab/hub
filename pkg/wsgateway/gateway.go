package wsgateway

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

// ConversationInfo holds the routing info for a conversation.
type ConversationInfo struct {
	ID              string
	UserID          string
	Mode            string // "llm", "tools", "agent"
	AgentInstanceID string
	AgentName       string // display name of the agent instance
	Model           string
}

// LLMMessage is a single message in conversation history (OpenAI-compatible).
type LLMMessage struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCalls  any    `json:"tool_calls,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// StreamCallback sends intermediate messages back to the client.
type StreamCallback func(msg ChatMessage)

// ConversationLookup resolves conversation info by ID.
type ConversationLookup func(ctx context.Context, conversationID string) (*ConversationInfo, error)

// LLMHandler handles pure LLM chat (no tools) for Quick Chat mode.
type LLMHandler func(ctx context.Context, model string, messages []LLMMessage) (string, *UsageInfo, error)

// AgentHandler sends messages to an agent container with full conversation history.
type AgentHandler func(ctx context.Context, instanceID, conversationID, userID string, messages []LLMMessage) (string, error)

// AgentStreamHandler sends messages to an agent and streams intermediate results.
type AgentStreamHandler func(ctx context.Context, instanceID, conversationID, userID string, messages []LLMMessage, stream StreamCallback) (string, error)

// MessagePersister saves messages to DB.
type MessagePersister func(ctx context.Context, conversationID, role, content string)

// FullMessagePersister saves messages with tool_calls, tool_call_id, and channel metadata to DB.
type FullMessagePersister func(ctx context.Context, conversationID, role, content string, toolCalls any, toolCallID string, channel string, tokenCount int, modelUsed string)

// TitleGenerator generates a short title from the first user message.
type TitleGenerator func(ctx context.Context, userMessage string) string

// TitleUpdater updates conversation title in DB.
type TitleUpdater func(ctx context.Context, conversationID, title string)

// Gateway is a WebSocket relay between browser clients and backend.
type Gateway struct {
	lookup      ConversationLookup
	llm         LLMHandler
	agent       AgentHandler
	agentStream AgentStreamHandler
	persist     MessagePersister
	persistFull FullMessagePersister
	genTitle    TitleGenerator
	setTitle    TitleUpdater
	logger      *slog.Logger

	mu    sync.Mutex
	conns map[string]*websocket.Conn // conversationID → active connection
}

// Config holds all handlers for the gateway.
type Config struct {
	Lookup      ConversationLookup
	LLM         LLMHandler
	Agent       AgentHandler
	AgentStream AgentStreamHandler
	Persist     MessagePersister
	PersistFull FullMessagePersister
	GenTitle    TitleGenerator
	SetTitle    TitleUpdater
	Logger      *slog.Logger
}

func New(cfg Config) *Gateway {
	return &Gateway{
		lookup:      cfg.Lookup,
		llm:         cfg.LLM,
		agent:       cfg.Agent,
		agentStream: cfg.AgentStream,
		persist:     cfg.Persist,
		persistFull: cfg.PersistFull,
		genTitle:    cfg.GenTitle,
		setTitle:    cfg.SetTitle,
		logger:   cfg.Logger,
		conns:    make(map[string]*websocket.Conn),
	}
}

// Handler returns an http.Handler for /ws/{conversation_id}.
func (g *Gateway) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/ws/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			http.Error(w, "conversation_id required in path", http.StatusBadRequest)
			return
		}
		conversationID := parts[0]

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

		// Default ReadLimit on nhooyr.io/websocket is 32 KiB — way too small
		// for chat history that includes a long pasted document or file.
		// 16 MiB lets the client paste large specs / source files without
		// silent truncation.
		conn.SetReadLimit(16 * 1024 * 1024)

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

// ChatMessage is the wire format for WebSocket messages (channel protocol).
type ChatMessage struct {
	// Legacy fields — kept for backward compat during migration window
	Type       string       `json:"type"`                    // "message", "token", "thinking", "tool_call", "tool_result", "response", "error", "status", "title_update", "summary", "cancel", "summarize"
	Content    string       `json:"content,omitempty"`       // text content
	Messages   []LLMMessage `json:"messages,omitempty"`      // full history (client → server) — will be dropped after migration
	ToolCalls  any          `json:"tool_calls,omitempty"`    // tool calls from LLM
	ToolCallID string       `json:"tool_call_id,omitempty"`  // for tool_result
	AgentName  string       `json:"agent_name,omitempty"`    // agent display name (for personalization)
	Usage      *UsageInfo   `json:"usage,omitempty"`         // token usage metadata (on response)
	SummaryOf  []string     `json:"summary_of,omitempty"`    // message IDs summarized (on summary)

	// Channel protocol fields (Spec F) — coexist with Type during migration
	Channel    string `json:"channel,omitempty"`     // final|status|analysis|tool_call|tool_result|plan|sub_agent|dive|hitl|hud|error
	Payload    any    `json:"payload,omitempty"`     // channel-specific structured data
	TokenCount int    `json:"token_count,omitempty"` // estimated tokens for this frame
	ModelUsed  string `json:"model_used,omitempty"`  // which model produced this
}

// UsageInfo contains token usage metadata for a response.
type UsageInfo struct {
	TokensIn  int    `json:"tokens_in"`
	TokensOut int    `json:"tokens_out"`
	Model     string `json:"model"`
}

func (g *Gateway) readLoop(ctx context.Context, conn *websocket.Conn, userID, conversationID string) {
	var cancelCurrent context.CancelFunc
	var doneCurrent chan struct{} // signals current handler finished

	// cancelAndWait cancels the in-flight handler and waits for it to finish
	// so we don't have concurrent writes to the WebSocket.
	cancelAndWait := func() {
		if cancelCurrent != nil {
			cancelCurrent()
			cancelCurrent = nil
		}
		if doneCurrent != nil {
			<-doneCurrent
			doneCurrent = nil
		}
	}

	defer cancelAndWait()

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
			cancelAndWait()
			msgCtx, cancel := context.WithCancel(ctx)
			cancelCurrent = cancel
			done := make(chan struct{})
			doneCurrent = done
			go func() {
				defer close(done)
				g.handleMessage(msgCtx, conn, userID, conversationID, msg)
			}()
		case "cancel":
			cancelAndWait()
			g.writeJSON(ctx, conn, ChatMessage{Type: "status", Content: "cancelled"})
		case "summarize":
			// Summarize handled in future phase — placeholder
			g.writeJSON(ctx, conn, ChatMessage{Type: "status", Content: "summarization not yet implemented"})
		default:
			g.writeJSON(ctx, conn, ChatMessage{Type: "error", Content: fmt.Sprintf("unknown type: %s", msg.Type)})
		}
	}
}

// TODO(spec-e): the client currently re-sends the full chat history (msg.Messages)
// on every turn. The agent runtime is supposed to be stateful and own its
// conversation history across sessions, browsers and reloads. The current
// "client owns history" model breaks if the user opens the chat from a
// different browser or if the agent has to run autonomously between user
// turns (cron, sub-agents). Spec E re-architects this so the WebSocket only
// carries the new user message + (optionally) a small "since N" cursor, and
// the agent loop reads its history from hub.db.agent_messages directly.
// Until then, the in-memory client history is treated as authoritative.
func (g *Gateway) handleMessage(ctx context.Context, conn *websocket.Conn, userID, conversationID string, msg ChatMessage) {
	g.logger.Info("chat message", "user", userID, "conversation", conversationID, "history_len", len(msg.Messages))

	// Persist user message (goroutine — independent of request lifecycle, 30s timeout)
	if g.persist != nil && msg.Content != "" {
		go func() {
			pCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			g.persist(pCtx, conversationID, "user", msg.Content)
		}()
	}

	conv, err := g.lookup(ctx, conversationID)
	if err != nil {
		g.writeJSON(ctx, conn, ChatMessage{Type: "error", Content: fmt.Sprintf("conversation not found: %v", err)})
		return
	}

	// Verify conversation belongs to this user
	if conv.UserID != userID {
		g.writeJSON(ctx, conn, ChatMessage{Type: "error", Content: "forbidden: conversation belongs to another user"})
		return
	}

	g.writeJSON(ctx, conn, ChatMessage{Type: "status", Content: "thinking"})

	stream := func(m ChatMessage) {
		g.writeJSON(ctx, conn, m)
		// Persist intermediate tool messages. Each persist runs in its own
		// goroutine with a 30s timeout — without the timeout a wedged DB
		// would leak goroutines on every tool call.
		// Channel metadata (channel, token_count, model_used) is passed through
		// so the DB row captures the channel protocol fields.
		if g.persistFull != nil {
			ch := withChannel(m) // resolve channel for persistence
			switch m.Type {
			case "tool_call":
				go func(content string, toolCalls any, channel string, tokenCount int, modelUsed string) {
					pCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
					defer cancel()
					g.persistFull(pCtx, conversationID, "assistant", content, toolCalls, "", channel, tokenCount, modelUsed)
				}(m.Content, m.ToolCalls, ch.Channel, ch.TokenCount, ch.ModelUsed)
			case "tool_result":
				go func(content, toolCallID, channel string, tokenCount int) {
					pCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
					defer cancel()
					g.persistFull(pCtx, conversationID, "tool", content, nil, toolCallID, channel, tokenCount, "")
				}(m.Content, m.ToolCallID, ch.Channel, ch.TokenCount)
			}
		}
	}

	var response string
	var usage *UsageInfo

	switch conv.Mode {
	case "llm":
		// Quick Chat — direct LLM completion, no agent
		if g.llm != nil {
			response, usage, err = g.llm(ctx, conv.Model, msg.Messages)
		} else {
			err = fmt.Errorf("LLM mode not configured")
		}

	default:
		// All other modes (agent, tools, legacy) route to the agent.
		// For mode=tools (legacy), fall through to agent with personal agent fallback.
		agentID := conv.AgentInstanceID
		if agentID == "" {
			// Legacy conversation or no agent linked — use personal agent
			agentID = "agent-personal-" + userID
		}
		if g.agentStream != nil {
			response, err = g.agentStream(ctx, agentID, conversationID, userID, msg.Messages, stream)
		} else if g.agent != nil {
			response, err = g.agent(ctx, agentID, conversationID, userID, msg.Messages)
		} else {
			err = fmt.Errorf("no agent handler configured")
		}
	}

	if err != nil {
		if ctx.Err() != nil {
			return // cancelled
		}
		errMsg := err.Error()
		if isContextLengthError(errMsg) {
			errMsg = "Conversation too long — context limit exceeded. Start a new chat or delete older messages."
		}
		g.writeJSON(ctx, conn, ChatMessage{Type: "error", Content: errMsg})
		return
	}

	respMsg := ChatMessage{Type: "response", Content: response, AgentName: conv.AgentName}
	if usage != nil {
		respMsg.Usage = usage
	}

	// Persist assistant response with channel metadata (goroutine — 30s timeout)
	if g.persistFull != nil {
		ch := withChannel(respMsg)
		go func(content, channel, modelUsed string, tokenCount int) {
			pCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			g.persistFull(pCtx, conversationID, "assistant", content, nil, "", channel, tokenCount, modelUsed)
		}(response, ch.Channel, ch.ModelUsed, ch.TokenCount)
	} else if g.persist != nil {
		go func() {
			pCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			g.persist(pCtx, conversationID, "assistant", response)
		}()
	}

	g.writeJSON(ctx, conn, respMsg)

	// Auto-generate title after first user message (synchronous — handleMessage is already in goroutine)
	if g.genTitle != nil && g.setTitle != nil && msg.Content != "" {
		userMsgCount := 0
		for _, m := range msg.Messages {
			if m.Role == "user" {
				userMsgCount++
			}
		}
		if userMsgCount <= 1 {
			title := g.genTitle(context.WithoutCancel(ctx), msg.Content)
			if title != "" {
				go func(t string) {
					pCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
					defer cancel()
					g.setTitle(pCtx, conversationID, t)
				}(title)
				g.writeJSON(ctx, conn, ChatMessage{Type: "title_update", Content: title})
			}
		}
	}
}

// withChannel sets the Channel field from the legacy Type field so both
// coexist on the wire during the migration window. Also estimates TokenCount
// from content length when not already set.
func withChannel(msg ChatMessage) ChatMessage {
	if msg.Channel == "" {
		switch msg.Type {
		case "token":
			msg.Channel = "final"
		case "response":
			msg.Channel = "final"
		case "thinking":
			msg.Channel = "analysis"
		case "tool_call":
			msg.Channel = "tool_call"
		case "tool_result":
			msg.Channel = "tool_result"
		case "status", "title_update":
			msg.Channel = "status"
		case "error":
			msg.Channel = "error"
		case "summary":
			msg.Channel = "final"
		default:
			msg.Channel = "status"
		}
	}
	if msg.TokenCount == 0 && msg.Content != "" {
		msg.TokenCount = len(msg.Content) / 4 // rough char/4 estimate
	}
	if msg.ModelUsed == "" && msg.Usage != nil {
		msg.ModelUsed = msg.Usage.Model
	}
	return msg
}

func (g *Gateway) writeJSON(ctx context.Context, conn *websocket.Conn, msg ChatMessage) {
	msg = withChannel(msg) // dual-emit: populate Channel from Type for all outgoing frames
	data, _ := json.Marshal(msg)
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		g.logger.Warn("websocket write error", "error", err)
	}
}

// isContextLengthError checks if an error is about LLM context/token limits.
func isContextLengthError(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "context length") ||
		strings.Contains(lower, "token limit") ||
		strings.Contains(lower, "too many tokens") ||
		strings.Contains(lower, "maximum context") ||
		strings.Contains(lower, "context window") ||
		strings.Contains(lower, "input too long") ||
		strings.Contains(lower, "prompt is too long")
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
