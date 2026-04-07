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

// AgentSender sends a message to a user's agent and returns the response.
type AgentSender func(ctx context.Context, userID, message string) (string, error)

// Gateway is a WebSocket relay between browser clients and agents.
type Gateway struct {
	send   AgentSender
	logger *slog.Logger

	mu    sync.Mutex
	conns map[string]*websocket.Conn // userID → active connection
}

func New(send AgentSender, logger *slog.Logger) *Gateway {
	return &Gateway{
		send:   send,
		logger: logger,
		conns:  make(map[string]*websocket.Conn),
	}
}

// Handler returns an http.Handler for /ws/{user_id}.
func (g *Gateway) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract user_id from path: /ws/{user_id}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/ws/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			http.Error(w, "user_id required in path", http.StatusBadRequest)
			return
		}
		userID := parts[0]

		// Verify auth context matches path user
		if authUser, ok := auth.UserFromContext(r.Context()); ok {
			if authUser.AuthType == "jwt" && authUser.ID != userID {
				http.Error(w, "forbidden: user mismatch", http.StatusForbidden)
				return
			}
		}

		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: []string{"*"},
		})
		if err != nil {
			g.logger.Error("websocket accept failed", "error", err)
			return
		}
		defer conn.CloseNow()

		g.mu.Lock()
		// Close previous connection for this user if any
		if prev, ok := g.conns[userID]; ok {
			prev.Close(websocket.StatusGoingAway, "replaced by new connection")
		}
		g.conns[userID] = conn
		g.mu.Unlock()

		defer func() {
			g.mu.Lock()
			if g.conns[userID] == conn {
				delete(g.conns, userID)
			}
			g.mu.Unlock()
		}()

		g.logger.Info("websocket connected", "user_id", userID)

		ctx := auth.ContextWithUser(r.Context(), auth.UserInfo{ID: userID})
		g.readLoop(ctx, conn, userID)
	})
}

// ChatMessage is the wire format for WebSocket messages.
type ChatMessage struct {
	Type    string `json:"type"`    // "message", "response", "error", "status"
	Content string `json:"content"` // message text or status info
}

func (g *Gateway) readLoop(ctx context.Context, conn *websocket.Conn, userID string) {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				g.logger.Info("websocket closed", "user_id", userID)
			} else {
				g.logger.Warn("websocket read error", "user_id", userID, "error", err)
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
			g.handleMessage(ctx, conn, userID, msg.Content)
		default:
			g.writeJSON(ctx, conn, ChatMessage{Type: "error", Content: fmt.Sprintf("unknown type: %s", msg.Type)})
		}
	}
}

func (g *Gateway) handleMessage(ctx context.Context, conn *websocket.Conn, userID, content string) {
	g.logger.Info("chat message", "user_id", userID, "length", len(content))

	// Send status
	g.writeJSON(ctx, conn, ChatMessage{Type: "status", Content: "thinking"})

	// Forward to agent
	response, err := g.send(ctx, userID, content)
	if err != nil {
		g.writeJSON(ctx, conn, ChatMessage{Type: "error", Content: err.Error()})
		return
	}

	g.writeJSON(ctx, conn, ChatMessage{Type: "response", Content: response})
}

func (g *Gateway) writeJSON(ctx context.Context, conn *websocket.Conn, msg ChatMessage) {
	data, _ := json.Marshal(msg)
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		g.logger.Warn("websocket write error", "error", err)
	}
}

// SendToUser sends a message to a connected user (for server-initiated messages).
func (g *Gateway) SendToUser(ctx context.Context, userID string, msg ChatMessage) error {
	g.mu.Lock()
	conn, ok := g.conns[userID]
	g.mu.Unlock()

	if !ok {
		return fmt.Errorf("user %s not connected", userID)
	}

	g.writeJSON(ctx, conn, msg)
	return nil
}
