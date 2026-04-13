package wsgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hugr-lab/hub/pkg/auth"

	"github.com/coder/websocket"
)

// testGateway creates a gateway with configurable handlers for testing.
func testGateway(t *testing.T, opts ...func(*Config)) (*Gateway, *httptest.Server) {
	t.Helper()

	cfg := Config{
		Lookup: func(ctx context.Context, id string) (*ConversationInfo, error) {
			return &ConversationInfo{ID: id, UserID: "test-user", Mode: "agent", AgentInstanceID: "agent-personal-test-user"}, nil
		},
		AgentStream: func(ctx context.Context, instanceID, conversationID, userID string, msgs []LLMMessage, stream StreamCallback) (string, error) {
			return "agent response", nil
		},
		Logger: slog.Default(),
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	gw := New(cfg)

	// Wrap with auth injection (test user)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.ContextWithUser(r.Context(), auth.UserInfo{ID: "test-user", Role: "user", AuthType: "jwt"})
		gw.Handler().ServeHTTP(w, r.WithContext(ctx))
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return gw, srv
}

func wsConnect(t *testing.T, srv *httptest.Server, convID string) *websocket.Conn {
	t.Helper()
	url := strings.Replace(srv.URL, "http://", "ws://", 1) + "/ws/" + convID
	conn, _, err := websocket.Dial(context.Background(), url, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

func wsSend(t *testing.T, conn *websocket.Conn, msg ChatMessage) {
	t.Helper()
	data, _ := json.Marshal(msg)
	if err := conn.Write(context.Background(), websocket.MessageText, data); err != nil {
		t.Fatalf("ws write: %v", err)
	}
}

func wsRead(t *testing.T, conn *websocket.Conn, timeout time.Duration) ChatMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	var msg ChatMessage
	json.Unmarshal(data, &msg)
	return msg
}

func wsReadAll(t *testing.T, conn *websocket.Conn, timeout time.Duration) []ChatMessage {
	t.Helper()
	var msgs []ChatMessage
	for {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			break
		}
		var msg ChatMessage
		json.Unmarshal(data, &msg)
		msgs = append(msgs, msg)
	}
	return msgs
}

func TestGateway_ToolsMode(t *testing.T) {
	_, srv := testGateway(t, func(cfg *Config) {
		cfg.AgentStream = func(ctx context.Context, instanceID, conversationID, userID string, msgs []LLMMessage, stream StreamCallback) (string, error) {
			if userID != "test-user" {
				return "", fmt.Errorf("unexpected user: %s", userID)
			}
			stream(ChatMessage{Type: "status", Content: "thinking"})
			return "hello from tools", nil
		}
	})

	conn := wsConnect(t, srv, "conv-1")

	wsSend(t, conn, ChatMessage{
		Type:    "message",
		Content: "test",
		Messages: []LLMMessage{
			{Role: "user", Content: "test"},
		},
	})

	// Read messages: status(thinking) + status(thinking from gateway) + response
	var got []ChatMessage
	for i := 0; i < 10; i++ {
		msg := wsRead(t, conn, 2*time.Second)
		got = append(got, msg)
		if msg.Type == "response" || msg.Type == "error" {
			break
		}
	}

	// Should have response at the end
	last := got[len(got)-1]
	if last.Type != "response" {
		t.Fatalf("expected response, got %s: %s", last.Type, last.Content)
	}
	if last.Content != "hello from tools" {
		t.Fatalf("unexpected content: %s", last.Content)
	}
}

func TestGateway_LLMMode(t *testing.T) {
	_, srv := testGateway(t, func(cfg *Config) {
		cfg.Lookup = func(ctx context.Context, id string) (*ConversationInfo, error) {
			return &ConversationInfo{ID: id, UserID: "test-user", Mode: "llm", Model: "test-model"}, nil
		}
		cfg.LLM = func(ctx context.Context, model string, msgs []LLMMessage) (string, *UsageInfo, error) {
			if model != "test-model" {
				return "", nil, fmt.Errorf("unexpected model: %s", model)
			}
			return "llm response", nil, nil
		}
	})

	conn := wsConnect(t, srv, "conv-llm")
	wsSend(t, conn, ChatMessage{
		Type:    "message",
		Content: "hello",
		Messages: []LLMMessage{
			{Role: "user", Content: "hello"},
		},
	})

	var got []ChatMessage
	for i := 0; i < 10; i++ {
		msg := wsRead(t, conn, 2*time.Second)
		got = append(got, msg)
		if msg.Type == "response" || msg.Type == "error" {
			break
		}
	}

	last := got[len(got)-1]
	if last.Type != "response" || last.Content != "llm response" {
		t.Fatalf("unexpected: type=%s content=%s", last.Type, last.Content)
	}
}

func TestGateway_AgentFallback(t *testing.T) {
	_, srv := testGateway(t, func(cfg *Config) {
		cfg.Lookup = func(ctx context.Context, id string) (*ConversationInfo, error) {
			return &ConversationInfo{ID: id, UserID: "test-user", Mode: "agent", AgentInstanceID: "inst-1"}, nil
		}
		cfg.Agent = func(ctx context.Context, instanceID, convID, userID string, msgs []LLMMessage) (string, error) {
			return "", fmt.Errorf("agent offline")
		}
		cfg.AgentStream = func(ctx context.Context, instanceID, conversationID, userID string, msgs []LLMMessage, stream StreamCallback) (string, error) {
			return "fallback to tools", nil
		}
	})

	conn := wsConnect(t, srv, "conv-agent")
	wsSend(t, conn, ChatMessage{
		Type:    "message",
		Content: "test",
		Messages: []LLMMessage{
			{Role: "user", Content: "test"},
		},
	})

	var got []ChatMessage
	for i := 0; i < 10; i++ {
		msg := wsRead(t, conn, 2*time.Second)
		got = append(got, msg)
		if msg.Type == "response" || msg.Type == "error" {
			break
		}
	}

	last := got[len(got)-1]
	if last.Type != "response" || last.Content != "fallback to tools" {
		t.Fatalf("expected tools fallback, got type=%s content=%s", last.Type, last.Content)
	}
}

func TestGateway_Cancel(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	_, srv := testGateway(t, func(cfg *Config) {
		cfg.AgentStream = func(ctx context.Context, instanceID, conversationID, userID string, msgs []LLMMessage, stream StreamCallback) (string, error) {
			close(started)
			<-ctx.Done()
			close(cancelled)
			return "", ctx.Err()
		}
	})

	conn := wsConnect(t, srv, "conv-cancel")
	wsSend(t, conn, ChatMessage{
		Type:    "message",
		Content: "long running",
		Messages: []LLMMessage{
			{Role: "user", Content: "long running"},
		},
	})

	// Wait for handler to start
	<-started
	time.Sleep(50 * time.Millisecond)

	// Send cancel
	wsSend(t, conn, ChatMessage{Type: "cancel"})

	// Read messages — expect "cancelled" status
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	gotCancelled := false
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			break
		}
		var msg ChatMessage
		json.Unmarshal(data, &msg)
		if msg.Type == "status" && msg.Content == "cancelled" {
			gotCancelled = true
			break
		}
	}

	// Also verify the handler was actually cancelled
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("handler was not cancelled")
	}

	if !gotCancelled {
		t.Fatal("did not receive 'cancelled' status message")
	}
}

func TestGateway_Persist(t *testing.T) {
	var mu sync.Mutex
	var persisted []struct{ role, content string }

	_, srv := testGateway(t, func(cfg *Config) {
		cfg.Persist = func(ctx context.Context, convID, role, content string) {
			mu.Lock()
			persisted = append(persisted, struct{ role, content string }{role, content})
			mu.Unlock()
		}
		cfg.AgentStream = func(ctx context.Context, instanceID, conversationID, userID string, msgs []LLMMessage, stream StreamCallback) (string, error) {
			return "persisted response", nil
		}
	})

	conn := wsConnect(t, srv, "conv-persist")
	wsSend(t, conn, ChatMessage{
		Type:    "message",
		Content: "save me",
		Messages: []LLMMessage{
			{Role: "user", Content: "save me"},
		},
	})

	// Wait for response
	for i := 0; i < 10; i++ {
		msg := wsRead(t, conn, 2*time.Second)
		if msg.Type == "response" {
			break
		}
	}

	// Give goroutines time to persist
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(persisted) < 2 {
		t.Fatalf("expected at least 2 persisted messages (user+assistant), got %d", len(persisted))
	}

	var foundUser, foundAssistant bool
	for _, p := range persisted {
		if p.role == "user" && p.content == "save me" {
			foundUser = true
		}
		if p.role == "assistant" && p.content == "persisted response" {
			foundAssistant = true
		}
	}
	if !foundUser {
		t.Fatal("user message not persisted")
	}
	if !foundAssistant {
		t.Fatal("assistant message not persisted")
	}
}

func TestGateway_TitleGeneration(t *testing.T) {
	var titleSet string
	var mu sync.Mutex

	_, srv := testGateway(t, func(cfg *Config) {
		cfg.GenTitle = func(ctx context.Context, msg string) string {
			return "Generated Title"
		}
		cfg.SetTitle = func(ctx context.Context, convID, title string) {
			mu.Lock()
			titleSet = title
			mu.Unlock()
		}
		cfg.AgentStream = func(ctx context.Context, instanceID, conversationID, userID string, msgs []LLMMessage, stream StreamCallback) (string, error) {
			return "ok", nil
		}
	})

	conn := wsConnect(t, srv, "conv-title")
	wsSend(t, conn, ChatMessage{
		Type:    "message",
		Content: "first message",
		Messages: []LLMMessage{
			{Role: "user", Content: "first message"},
		},
	})

	// Read all messages including title_update
	var gotTitle string
	for i := 0; i < 10; i++ {
		msg := wsRead(t, conn, 2*time.Second)
		if msg.Type == "title_update" {
			gotTitle = msg.Content
			break
		}
		if msg.Type == "error" {
			t.Fatalf("error: %s", msg.Content)
		}
	}

	if gotTitle != "Generated Title" {
		// Title might arrive after response — wait a bit
		time.Sleep(300 * time.Millisecond)
	}

	mu.Lock()
	if titleSet != "Generated Title" {
		t.Fatalf("expected title 'Generated Title', got '%s'", titleSet)
	}
	mu.Unlock()
}

func TestGateway_ConversationNotFound(t *testing.T) {
	_, srv := testGateway(t, func(cfg *Config) {
		cfg.Lookup = func(ctx context.Context, id string) (*ConversationInfo, error) {
			return nil, fmt.Errorf("not found")
		}
	})

	conn := wsConnect(t, srv, "conv-missing")
	wsSend(t, conn, ChatMessage{
		Type:    "message",
		Content: "test",
		Messages: []LLMMessage{
			{Role: "user", Content: "test"},
		},
	})

	msg := wsRead(t, conn, 2*time.Second)
	if msg.Type != "error" || !strings.Contains(msg.Content, "not found") {
		t.Fatalf("expected error about not found, got type=%s content=%s", msg.Type, msg.Content)
	}
}

func TestGateway_RapidMessages(t *testing.T) {
	// Verify that sending two messages rapidly doesn't cause a race.
	// Second message should cancel first, and only second response arrives.
	var mu sync.Mutex
	var callOrder []string

	_, srv := testGateway(t, func(cfg *Config) {
		cfg.AgentStream = func(ctx context.Context, instanceID, conversationID, userID string, msgs []LLMMessage, stream StreamCallback) (string, error) {
			// Get last user message
			last := msgs[len(msgs)-1].Content
			mu.Lock()
			callOrder = append(callOrder, "start:"+last)
			mu.Unlock()

			select {
			case <-ctx.Done():
				mu.Lock()
				callOrder = append(callOrder, "cancel:"+last)
				mu.Unlock()
				return "", ctx.Err()
			case <-time.After(500 * time.Millisecond):
				return "response:" + last, nil
			}
		}
	})

	conn := wsConnect(t, srv, "conv-rapid")

	// Send first message
	wsSend(t, conn, ChatMessage{
		Type: "message", Content: "first",
		Messages: []LLMMessage{{Role: "user", Content: "first"}},
	})
	time.Sleep(50 * time.Millisecond) // let handler start

	// Send second message immediately — should cancel first
	wsSend(t, conn, ChatMessage{
		Type: "message", Content: "second",
		Messages: []LLMMessage{{Role: "user", Content: "second"}},
	})

	// Read until we get a response
	var lastResponse string
	for i := 0; i < 20; i++ {
		msg := wsRead(t, conn, 3*time.Second)
		if msg.Type == "response" {
			lastResponse = msg.Content
			break
		}
	}

	if lastResponse != "response:second" {
		t.Fatalf("expected response from second message, got: %s", lastResponse)
	}

	// Verify first was cancelled
	mu.Lock()
	defer mu.Unlock()
	foundCancelFirst := false
	for _, e := range callOrder {
		if e == "cancel:first" {
			foundCancelFirst = true
		}
	}
	if !foundCancelFirst {
		t.Fatalf("first handler was not cancelled, order: %v", callOrder)
	}
}

func TestGateway_ConnectionReplacement(t *testing.T) {
	_, srv := testGateway(t)

	conn1 := wsConnect(t, srv, "conv-replace")
	conn2 := wsConnect(t, srv, "conv-replace")

	// conn1 should be closed
	time.Sleep(100 * time.Millisecond)
	_, _, err := conn1.Read(context.Background())
	if err == nil {
		t.Fatal("expected conn1 to be closed after replacement")
	}

	// conn2 should work
	wsSend(t, conn2, ChatMessage{
		Type:    "message",
		Content: "test",
		Messages: []LLMMessage{
			{Role: "user", Content: "test"},
		},
	})

	var got ChatMessage
	for i := 0; i < 10; i++ {
		got = wsRead(t, conn2, 2*time.Second)
		if got.Type == "response" || got.Type == "error" {
			break
		}
	}
	if got.Type != "response" {
		t.Fatalf("expected response on new conn, got %s", got.Type)
	}
}
