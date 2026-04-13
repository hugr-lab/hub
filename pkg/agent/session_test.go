package agent

import (
	"sync"
	"testing"

	"github.com/hugr-lab/hub/pkg/llmrouter"
)

func TestSessionManager_GetOrCreate(t *testing.T) {
	mgr := NewSessionManager()

	s1 := mgr.Get("conv-1")
	if s1 == nil {
		t.Fatal("Get returned nil")
	}
	if s1.ID != "conv-1" {
		t.Errorf("ID = %q, want %q", s1.ID, "conv-1")
	}

	// Second call returns the same session
	s2 := mgr.Get("conv-1")
	if s1 != s2 {
		t.Error("second Get returned a different session pointer")
	}

	// Different ID creates a new session
	s3 := mgr.Get("conv-2")
	if s1 == s3 {
		t.Error("different ID returned the same session")
	}

	if mgr.Count() != 2 {
		t.Errorf("Count = %d, want 2", mgr.Count())
	}
}

func TestSession_AppendUser(t *testing.T) {
	s := &ConversationSession{ID: "test"}

	snap1 := s.AppendUser("hello")
	if len(snap1) != 1 {
		t.Fatalf("snapshot length = %d, want 1", len(snap1))
	}
	if snap1[0].Role != "user" || snap1[0].Content != "hello" {
		t.Errorf("snap1[0] = %+v, want user/hello", snap1[0])
	}

	snap2 := s.AppendUser("world")
	if len(snap2) != 2 {
		t.Fatalf("snapshot length = %d, want 2", len(snap2))
	}

	// Verify snapshot is a copy (mutating snap1 should not affect session)
	snap1[0].Content = "MUTATED"
	msgs := s.Messages()
	if msgs[0].Content == "MUTATED" {
		t.Error("snapshot was not a copy — session data was mutated")
	}
}

func TestSession_SetMessages_StripsSystemPrompt(t *testing.T) {
	s := &ConversationSession{ID: "test"}

	updated := []llmrouter.Message{
		{Role: "system", Content: "You are helpful..."},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "Hi!", ToolCalls: []any{map[string]any{"id": "tc-1", "name": "search"}}},
		{Role: "tool", Content: "result data", ToolCallID: "tc-1"},
		{Role: "assistant", Content: "Here is the data."},
	}

	s.SetMessages(updated)

	msgs := s.Messages()
	if len(msgs) != 4 {
		t.Fatalf("messages length = %d, want 4 (system stripped)", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("msgs[0].Role = %q, want %q (system should be stripped)", msgs[0].Role, "user")
	}
	// Verify tool call metadata preserved
	if msgs[1].ToolCalls == nil {
		t.Error("ToolCalls lost after SetMessages")
	}
	if msgs[2].ToolCallID != "tc-1" {
		t.Errorf("ToolCallID = %q, want %q", msgs[2].ToolCallID, "tc-1")
	}
}

func TestSession_SetMessages_NoSystemPrompt(t *testing.T) {
	s := &ConversationSession{ID: "test"}

	updated := []llmrouter.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "Hi!"},
	}

	s.SetMessages(updated)

	msgs := s.Messages()
	if len(msgs) != 2 {
		t.Fatalf("messages length = %d, want 2", len(msgs))
	}
}

func TestSession_FullTwoTurns(t *testing.T) {
	s := &ConversationSession{ID: "test"}

	// Turn 1: simple greeting
	snap := s.AppendUser("hi")
	if len(snap) != 1 {
		t.Fatalf("turn 1: snapshot length = %d, want 1", len(snap))
	}

	// Simulate agentic loop result (system prepended, no tools)
	turnHistory := []llmrouter.Message{
		{Role: "system", Content: "constitution prompt"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "Hello!"},
	}
	s.SetMessages(turnHistory)

	if s.Len() != 2 {
		t.Fatalf("after turn 1: Len = %d, want 2", s.Len())
	}

	// Turn 2: data query with tools
	snap = s.AppendUser("show tables")
	if len(snap) != 3 {
		t.Fatalf("turn 2: snapshot length = %d, want 3 (prev 2 + new user)", len(snap))
	}

	// Simulate agentic loop with tool call
	turnHistory2 := []llmrouter.Message{
		{Role: "system", Content: "hugr-data prompt"},
		// Previous turn
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "Hello!"},
		// Current turn
		{Role: "user", Content: "show tables"},
		{Role: "assistant", Content: "", ToolCalls: []any{map[string]any{"id": "tc-1", "name": "list-tables"}}},
		{Role: "tool", Content: "synthea, taxi", ToolCallID: "tc-1"},
		{Role: "assistant", Content: "Found 2 modules."},
	}
	s.SetMessages(turnHistory2)

	msgs := s.Messages()
	if len(msgs) != 6 {
		t.Fatalf("after turn 2: len = %d, want 6 (system stripped)", len(msgs))
	}

	// Verify all message types preserved
	roles := make([]string, len(msgs))
	for i, m := range msgs {
		roles[i] = m.Role
	}
	wantRoles := []string{"user", "assistant", "user", "assistant", "tool", "assistant"}
	for i, want := range wantRoles {
		if roles[i] != want {
			t.Errorf("msgs[%d].Role = %q, want %q", i, roles[i], want)
		}
	}

	// Verify tool call metadata
	if msgs[3].ToolCalls == nil {
		t.Error("turn 2: assistant ToolCalls lost")
	}
	if msgs[4].ToolCallID != "tc-1" {
		t.Errorf("turn 2: tool ToolCallID = %q, want %q", msgs[4].ToolCallID, "tc-1")
	}
}

func TestSession_PreservesToolCalls(t *testing.T) {
	s := &ConversationSession{ID: "test"}

	tc := []any{
		map[string]any{
			"id":   "call-123",
			"name": "data-query",
			"arguments": map[string]any{
				"query": "SELECT * FROM users",
			},
		},
	}

	history := []llmrouter.Message{
		{Role: "system", Content: "prompt"},
		{Role: "user", Content: "query"},
		{Role: "assistant", Content: "", ToolCalls: tc},
		{Role: "tool", Content: "rows...", ToolCallID: "call-123"},
		{Role: "assistant", Content: "Here are the results."},
	}

	s.SetMessages(history)
	msgs := s.Messages()

	// Verify ToolCalls survived
	assistantTC := msgs[1] // index 1 after system strip
	if len(assistantTC.ToolCalls) != 1 {
		t.Fatalf("ToolCalls length = %d, want 1", len(assistantTC.ToolCalls))
	}
}

func TestSession_ConcurrentAccess(t *testing.T) {
	s := &ConversationSession{ID: "test"}
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.AppendUser("message")
			s.Messages()
			s.SetMessages([]llmrouter.Message{
				{Role: "system", Content: "p"},
				{Role: "user", Content: "m"},
			})
			s.Len()
		}()
	}

	wg.Wait()
	// No race detector failure = pass
}

func TestSessionManager_Delete(t *testing.T) {
	mgr := NewSessionManager()

	s1 := mgr.Get("conv-1")
	s1.AppendUser("hello")

	mgr.Delete("conv-1")

	if mgr.Count() != 0 {
		t.Errorf("Count after delete = %d, want 0", mgr.Count())
	}

	// Get after delete creates a new empty session
	s2 := mgr.Get("conv-1")
	if s2.Len() != 0 {
		t.Errorf("new session Len = %d, want 0", s2.Len())
	}
	if s1 == s2 {
		t.Error("Get after delete returned the same session pointer")
	}
}
