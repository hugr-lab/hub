package agent

import (
	"sync"
	"time"

	"github.com/hugr-lab/hub/pkg/llmrouter"
)

// ConversationSession holds per-conversation agent↔LLM state.
// Messages are stored WITHOUT the system prompt — it is rebuilt per-turn
// by the skill router based on the user's message content.
//
// Future extensions (Spec H+):
//   - HITL: pendingHITL chan for pausing the current step
//   - Sub-agents: child goroutine tracking
//   - Plan mode: active plan state
//   - Context budget: token tracking + auto-compaction
type ConversationSession struct {
	ID string

	mu       sync.Mutex
	messages []llmrouter.Message // user + assistant (with ToolCalls) + tool

	created    time.Time
	lastActive time.Time
}

// AppendUser appends a user message and returns a snapshot of all messages
// (copy — safe for concurrent use by the agentic loop).
func (s *ConversationSession) AppendUser(content string) []llmrouter.Message {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.messages = append(s.messages, llmrouter.Message{Role: "user", Content: content})
	s.lastActive = time.Now()

	snapshot := make([]llmrouter.Message, len(s.messages))
	copy(snapshot, s.messages)
	return snapshot
}

// SetMessages replaces the session's messages with the updated history
// from the agentic loop. Strips a leading system message if present
// (system prompt is per-turn, not stored in session).
func (s *ConversationSession) SetMessages(history []llmrouter.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Strip system prompt if present at index 0
	start := 0
	if len(history) > 0 && history[0].Role == "system" {
		start = 1
	}

	// Store a copy to avoid aliasing with the caller's slice
	s.messages = make([]llmrouter.Message, len(history)-start)
	copy(s.messages, history[start:])
	s.lastActive = time.Now()
}

// Messages returns a copy of the current message history.
func (s *ConversationSession) Messages() []llmrouter.Message {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]llmrouter.Message, len(s.messages))
	copy(result, s.messages)
	return result
}

// Len returns the number of messages in the session.
func (s *ConversationSession) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.messages)
}

// SessionManager manages per-conversation sessions.
// Sessions are created on first access and live in memory (L3 per design).
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*ConversationSession
}

// NewSessionManager creates a new session manager.
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*ConversationSession),
	}
}

// Get returns the session for a conversation, creating one if needed.
func (m *SessionManager) Get(id string) *ConversationSession {
	m.mu.RLock()
	s, ok := m.sessions[id]
	m.mu.RUnlock()
	if ok {
		return s
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if s, ok = m.sessions[id]; ok {
		return s
	}

	s = &ConversationSession{
		ID:      id,
		created: time.Now(),
	}
	m.sessions[id] = s
	return s
}

// Delete removes a session.
func (m *SessionManager) Delete(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
}

// Count returns the number of active sessions.
func (m *SessionManager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}
