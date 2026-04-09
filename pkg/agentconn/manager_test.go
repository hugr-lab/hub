package agentconn

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hugr-lab/hub/pkg/auth"

	"nhooyr.io/websocket"
)

func testManager(t *testing.T) (*Manager, *httptest.Server) {
	t.Helper()
	mgr := NewManager(slog.Default())

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.ContextWithUser(r.Context(), auth.UserInfo{ID: "agent-user", Role: "agent", AuthType: "agent"})
		mgr.Handler().ServeHTTP(w, r.WithContext(ctx))
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return mgr, srv
}

func agentConnect(t *testing.T, srv *httptest.Server, instanceID string) *websocket.Conn {
	t.Helper()
	url := strings.Replace(srv.URL, "http://", "ws://", 1) + "/agent/ws/" + instanceID
	conn, _, err := websocket.Dial(context.Background(), url, nil)
	if err != nil {
		t.Fatalf("agent ws dial: %v", err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

func TestManager_ConnectDisconnect(t *testing.T) {
	mgr, srv := testManager(t)

	if mgr.IsConnected("inst-1") {
		t.Fatal("should not be connected before dial")
	}

	conn := agentConnect(t, srv, "inst-1")
	time.Sleep(50 * time.Millisecond) // let handler register

	if !mgr.IsConnected("inst-1") {
		t.Fatal("should be connected after dial")
	}

	ids := mgr.ConnectedInstances()
	if len(ids) != 1 || ids[0] != "inst-1" {
		t.Fatalf("unexpected connected instances: %v", ids)
	}

	conn.Close(websocket.StatusNormalClosure, "test done")
	time.Sleep(100 * time.Millisecond)

	if mgr.IsConnected("inst-1") {
		t.Fatal("should not be connected after close")
	}
}

func TestManager_SendMessage(t *testing.T) {
	mgr, srv := testManager(t)
	conn := agentConnect(t, srv, "inst-send")
	time.Sleep(50 * time.Millisecond)

	// Agent reads request and sends response
	go func() {
		_, data, err := conn.Read(context.Background())
		if err != nil {
			return
		}
		var req AgentMessage
		json.Unmarshal(data, &req)

		resp := AgentMessage{
			RequestID: req.RequestID,
			Type:      "response",
			Content:   "agent says hello",
		}
		respData, _ := json.Marshal(resp)
		conn.Write(context.Background(), websocket.MessageText, respData)
	}()

	result, err := mgr.SendMessage(context.Background(), "inst-send", "conv-1", "user-1", "hello agent")
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	if result != "agent says hello" {
		t.Fatalf("unexpected result: %s", result)
	}
}

func TestManager_SendMessageStream(t *testing.T) {
	mgr, srv := testManager(t)
	conn := agentConnect(t, srv, "inst-stream")
	time.Sleep(50 * time.Millisecond)

	// Agent sends status updates then response
	go func() {
		_, data, err := conn.Read(context.Background())
		if err != nil {
			return
		}
		var req AgentMessage
		json.Unmarshal(data, &req)

		// Send status
		status := AgentMessage{RequestID: req.RequestID, Type: "status", Content: "processing"}
		d1, _ := json.Marshal(status)
		conn.Write(context.Background(), websocket.MessageText, d1)

		// Send response
		resp := AgentMessage{RequestID: req.RequestID, Type: "response", Content: "done"}
		d2, _ := json.Marshal(resp)
		conn.Write(context.Background(), websocket.MessageText, d2)
	}()

	var statuses []string
	result, err := mgr.SendMessageStream(context.Background(), "inst-stream", "conv-1", "user-1", "work",
		func(s string) { statuses = append(statuses, s) })
	if err != nil {
		t.Fatalf("send stream: %v", err)
	}
	if result != "done" {
		t.Fatalf("unexpected result: %s", result)
	}
	if len(statuses) == 0 || statuses[0] != "processing" {
		t.Fatalf("expected status 'processing', got %v", statuses)
	}
}

func TestManager_NotConnected(t *testing.T) {
	mgr := NewManager(slog.Default())

	_, err := mgr.SendMessage(context.Background(), "nonexistent", "", "", "hello")
	if err == nil {
		t.Fatal("expected error for non-connected agent")
	}
	if !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("expected 'not connected' error, got: %v", err)
	}
}

func TestManager_AgentError(t *testing.T) {
	mgr, srv := testManager(t)
	conn := agentConnect(t, srv, "inst-err")
	time.Sleep(50 * time.Millisecond)

	go func() {
		_, data, err := conn.Read(context.Background())
		if err != nil {
			return
		}
		var req AgentMessage
		json.Unmarshal(data, &req)

		resp := AgentMessage{RequestID: req.RequestID, Type: "error", Content: "something broke"}
		d, _ := json.Marshal(resp)
		conn.Write(context.Background(), websocket.MessageText, d)
	}()

	_, err := mgr.SendMessage(context.Background(), "inst-err", "", "", "break")
	if err == nil {
		t.Fatal("expected error from agent")
	}
	if !strings.Contains(err.Error(), "something broke") {
		t.Fatalf("expected agent error message, got: %v", err)
	}
}

func TestManager_ContextCancellation(t *testing.T) {
	mgr, srv := testManager(t)
	agentConnect(t, srv, "inst-ctx")
	time.Sleep(50 * time.Millisecond)

	// Agent never responds — context should cancel
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := mgr.SendMessage(ctx, "inst-ctx", "", "", "no reply")
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestManager_ConnectionReplacement(t *testing.T) {
	mgr, srv := testManager(t)

	conn1 := agentConnect(t, srv, "inst-repl")
	time.Sleep(50 * time.Millisecond)

	if !mgr.IsConnected("inst-repl") {
		t.Fatal("first connection should be active")
	}

	// Second connection replaces first
	conn2 := agentConnect(t, srv, "inst-repl")
	time.Sleep(100 * time.Millisecond)

	// conn1 should be closed
	_, _, err := conn1.Read(context.Background())
	if err == nil {
		t.Fatal("old connection should be closed")
	}

	// conn2 should work — agent responds
	go func() {
		_, data, err := conn2.Read(context.Background())
		if err != nil {
			return
		}
		var req AgentMessage
		json.Unmarshal(data, &req)
		resp := AgentMessage{RequestID: req.RequestID, Type: "response", Content: "from new conn"}
		d, _ := json.Marshal(resp)
		conn2.Write(context.Background(), websocket.MessageText, d)
	}()

	result, err := mgr.SendMessage(context.Background(), "inst-repl", "", "", "test")
	if err != nil {
		t.Fatalf("send via new conn: %v", err)
	}
	if result != "from new conn" {
		t.Fatalf("unexpected: %s", result)
	}
}

func TestManager_Heartbeat(t *testing.T) {
	mgr, srv := testManager(t)
	mgr.heartbeatInterval = 100 * time.Millisecond

	conn := agentConnect(t, srv, "inst-hb")
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	go mgr.StartHeartbeat(ctx)

	// Read ping from agent side
	readCtx, readCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer readCancel()
	_, data, err := conn.Read(readCtx)
	if err != nil {
		t.Fatalf("expected ping, got error: %v", err)
	}

	var msg AgentMessage
	json.Unmarshal(data, &msg)
	if msg.Type != "ping" {
		t.Fatalf("expected ping, got %s", msg.Type)
	}

	// Reply pong
	pong := AgentMessage{Type: "pong"}
	d, _ := json.Marshal(pong)
	conn.Write(context.Background(), websocket.MessageText, d)

	cancel()
	time.Sleep(50 * time.Millisecond)
}
