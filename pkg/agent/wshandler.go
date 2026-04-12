package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"nhooyr.io/websocket"
)

// RunLocal starts the agent in local mode — HTTP server with WebSocket
// endpoints at /ws/{conversation_id} and /health.
func (a *Agent) RunLocal(ctx context.Context, listenAddr string) error {
	if err := a.Connect(ctx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	a.skills.Load()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/", a.handleLocalWS)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	srv := &http.Server{Addr: listenAddr, Handler: mux}
	a.logger.Info("local agent listening", "addr", listenAddr)

	go func() {
		<-ctx.Done()
		srv.Close()
	}()

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// wsMessage mirrors the ChatMessage wire format used by hub-service gateway.
type wsMessage struct {
	Type       string `json:"type"`
	Content    string `json:"content,omitempty"`
	Channel    string `json:"channel,omitempty"`
	ToolCalls  any    `json:"tool_calls,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// handleLocalWS handles /ws/{conversation_id} WebSocket connections.
func (a *Agent) handleLocalWS(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/ws/"), "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "conversation_id required", http.StatusBadRequest)
		return
	}
	conversationID := parts[0]

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // local only — agent-bridge handles auth
	})
	if err != nil {
		a.logger.Warn("ws accept failed", "error", err)
		return
	}
	defer conn.CloseNow()

	a.logger.Info("local ws connected", "conversation", conversationID)

	ctx := r.Context()
	var cancelMu sync.Mutex
	var cancelFn context.CancelFunc

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			break
		}

		var msg wsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "message":
			cancelMu.Lock()
			if cancelFn != nil {
				cancelFn()
			}
			msgCtx, cancel := context.WithCancel(ctx)
			cancelFn = cancel
			cancelMu.Unlock()

			go a.handleLocalMessage(msgCtx, conn, conversationID, msg)

		case "cancel":
			cancelMu.Lock()
			if cancelFn != nil {
				cancelFn()
				cancelFn = nil
			}
			cancelMu.Unlock()
			writeWS(ctx, conn, wsMessage{Type: "status", Content: "cancelled", Channel: "status"})
		}
	}
}

func (a *Agent) handleLocalMessage(ctx context.Context, conn *websocket.Conn, conversationID string, msg wsMessage) {
	writeWS(ctx, conn, wsMessage{Type: "status", Content: "thinking", Channel: "status"})

	// Build history as historyMessage (the type HandleMessagesWithStream expects)
	messages := []historyMessage{
		{Role: "user", Content: msg.Content},
	}

	stream := AgentStreamCallback(func(msgType, content string, toolCalls any, toolCallID string) {
		out := wsMessage{Type: msgType, Content: content, ToolCallID: toolCallID}
		if toolCalls != nil {
			out.ToolCalls = toolCalls
		}
		switch msgType {
		case "token":
			out.Channel = "final"
		case "thinking":
			out.Channel = "analysis"
		case "tool_call":
			out.Channel = "tool_call"
		case "tool_result":
			out.Channel = "tool_result"
		case "status":
			out.Channel = "status"
		case "error":
			out.Channel = "error"
		}
		writeWS(ctx, conn, out)
	})

	response, err := a.HandleMessagesWithStream(ctx, messages, stream)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		writeWS(ctx, conn, wsMessage{Type: "error", Content: err.Error(), Channel: "error"})
		return
	}

	writeWS(ctx, conn, wsMessage{
		Type:    "response",
		Content: response,
		Channel: "final",
	})
}

func writeWS(ctx context.Context, conn *websocket.Conn, msg wsMessage) {
	data, _ := json.Marshal(msg)
	conn.Write(ctx, websocket.MessageText, data)
}
