package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/hugr-lab/hub/pkg/llmrouter"

	"github.com/coder/websocket"
)

// RunLocal starts the agent in local mode — listens on localhost for
// WebSocket connections from ChatWebSocketHandler (routed by hub-chat).
func (a *Agent) RunLocal(ctx context.Context, listenAddr string) error {
	if err := a.Connect(ctx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}

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

// workerInput is a message sent to the conversation worker goroutine.
type workerInput struct {
	kind    string // "message" or "cancel"
	content string
}

// handleLocalWS handles /ws/{conversation_id} WebSocket connections.
// Creates a conversation worker goroutine that processes messages sequentially
// via an input channel. The WS read loop only dispatches to the channel.
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

	session := a.sessions.Get(conversationID)
	ctx := r.Context()

	// Input channel — user messages and cancel signals flow here.
	// Buffered(1) so WS read loop doesn't block on fast sequential sends.
	inbox := make(chan workerInput, 1)

	// Start conversation worker goroutine — owns all processing for this conversation.
	// Single goroutine ensures sequential message processing and clean state management.
	// Future: HITL responses, sub-agent results will also arrive via inbox.
	go a.conversationWorker(ctx, conn, conversationID, session, inbox)

	// WS read loop — just dispatches to inbox, no processing here
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
			select {
			case inbox <- workerInput{kind: "message", content: msg.Content}:
			case <-ctx.Done():
				return
			}
		case "cancel":
			select {
			case inbox <- workerInput{kind: "cancel"}:
			case <-ctx.Done():
				return
			}
		}
	}
}

// conversationWorker is the main loop for a conversation. Reads from inbox,
// processes messages sequentially. Cancellation of in-flight work is handled
// via context: a new message or cancel signal cancels the current processing.
//
// Architecture:
//
//	Browser WS → handleLocalWS (read loop) → inbox (chan)
//	                                              │
//	                                              ▼
//	                                    conversationWorker (goroutine)
//	                                    ┌────────────────────────────┐
//	                                    │ for input := range inbox   │
//	                                    │   cancel previous if any   │
//	                                    │   processMessage(ctx, ...) │
//	                                    └────────────────────────────┘
//	                                              │
//	                                              ▼
//	                                    WebSocket write (output)
func (a *Agent) conversationWorker(ctx context.Context, conn *websocket.Conn, conversationID string, session *ConversationSession, inbox <-chan workerInput) {
	var (
		cancelCurrent context.CancelFunc
		currentDone   <-chan struct{} // closed when current processing finishes
	)

	defer func() {
		if cancelCurrent != nil {
			cancelCurrent()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return

		case input, ok := <-inbox:
			if !ok {
				return
			}

			switch input.kind {
			case "cancel":
				if cancelCurrent != nil {
					cancelCurrent()
					cancelCurrent = nil
				}
				writeWS(ctx, conn, wsMessage{Type: "status", Content: "cancelled", Channel: "status"})

			case "message":
				// Cancel previous in-flight processing
				if cancelCurrent != nil {
					cancelCurrent()
					// Wait for previous goroutine to finish (ensures session state is consistent)
					if currentDone != nil {
						<-currentDone
					}
				}

				msgCtx, cancel := context.WithCancel(ctx)
				cancelCurrent = cancel
				done := make(chan struct{})
				currentDone = done

				// Process in goroutine so we can receive cancel signals concurrently
				go func() {
					defer close(done)
					a.processUserMessage(msgCtx, conn, conversationID, input.content, session)
				}()
			}

		case <-currentDone:
			// Current processing finished — clean up
			if cancelCurrent != nil {
				cancelCurrent()
				cancelCurrent = nil
			}
			currentDone = nil
		}
	}
}

// processUserMessage handles a single user message within a conversation.
// Called from conversationWorker — never spawned directly as a goroutine.
//
// Flow:
//  1. Append user message to session, get snapshot of full agent↔LLM history
//  2. Build per-turn system prompt via skill router
//  3. Run agentic loop (LLM calls, tool execution, streaming to user)
//  4. Store updated history back to session
//  5. Persist to DB + auto-generate title
func (a *Agent) processUserMessage(ctx context.Context, conn *websocket.Conn, conversationID, content string, session *ConversationSession) {
	writeWS(ctx, conn, wsMessage{Type: "status", Content: "thinking", Channel: "status"})

	// 1. Append user message, get snapshot of full agent↔LLM history
	snapshot := session.AppendUser(content)
	isFirstMessage := len(snapshot) == 1

	// Persist user message to DB (async, non-blocking — independent of agent loop)
	go a.persistMessage(context.WithoutCancel(ctx), conversationID, "user", content)

	// 2. Build per-turn system prompt (skill routing based on message content)
	learnedContext := a.learner.RetrieveContext(ctx, content)
	systemPrompt := a.buildSystemPrompt(content, learnedContext)

	// 3. Prepend system prompt — not stored in session, rebuilt per-turn
	llmHistory := make([]llmrouter.Message, 0, 1+len(snapshot))
	llmHistory = append(llmHistory, llmrouter.Message{Role: "system", Content: systemPrompt})
	llmHistory = append(llmHistory, snapshot...)

	// Stream callback — sends intermediate results to user via WebSocket
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

	// 4. Run agentic loop — returns full updated history with all intermediates
	response, updatedHistory, err := a.runAgenticLoopWithStream(ctx, llmHistory, stream)
	if err != nil {
		if ctx.Err() != nil {
			return // cancelled
		}
		writeWS(ctx, conn, wsMessage{Type: "error", Content: err.Error(), Channel: "error"})
		return
	}

	// 5. Store full history back to session (strips system prompt)
	session.SetMessages(updatedHistory)

	writeWS(ctx, conn, wsMessage{
		Type:    "response",
		Content: response,
		Channel: "final",
	})

	// Persist assistant response to DB (async, survives cancellation)
	go a.persistMessage(context.WithoutCancel(ctx), conversationID, "assistant", response)

	// Auto-generate title on first message (async)
	if isFirstMessage {
		go a.generateTitle(context.WithoutCancel(ctx), conn, conversationID, content)
	}
}

// persistMessage saves a message to DB via hub-service MCP tool.
func (a *Agent) persistMessage(ctx context.Context, conversationID, role, content string) {
	_, err := a.CallHubTool(ctx, "conversation-persist-message", map[string]any{
		"conversation_id": conversationID,
		"role":            role,
		"content":         content,
	})
	if err != nil {
		a.logger.Warn("failed to persist message", "conversation", conversationID, "role", role, "error", err)
	}
}

// generateTitle generates and sets a conversation title from the first user message.
func (a *Agent) generateTitle(ctx context.Context, conn *websocket.Conn, conversationID, userMessage string) {
	result, err := a.CallHubTool(ctx, "llm-complete", map[string]any{
		"messages": []map[string]string{
			{"role": "system", "content": "Generate a very short title (3-6 words, no quotes) for a chat that starts with this message. Reply with ONLY the title, nothing else."},
			{"role": "user", "content": userMessage},
		},
		"intent": "classification",
	})
	if err != nil {
		a.logger.Warn("title generation failed", "error", err)
		return
	}

	var resp struct {
		Content string `json:"content"`
	}
	if json.Unmarshal([]byte(result), &resp) != nil || resp.Content == "" {
		return
	}

	title := strings.TrimSpace(resp.Content)
	runes := []rune(title)
	if len(runes) > 60 {
		title = string(runes[:60])
	}

	if _, err := a.CallHubTool(ctx, "conversation-set-title", map[string]any{
		"conversation_id": conversationID,
		"title":           title,
	}); err != nil {
		a.logger.Warn("failed to set title", "error", err)
		return
	}

	writeWS(ctx, conn, wsMessage{Type: "title_update", Content: title})
}

func writeWS(ctx context.Context, conn *websocket.Conn, msg wsMessage) {
	data, _ := json.Marshal(msg)
	conn.Write(ctx, websocket.MessageText, data)
}
