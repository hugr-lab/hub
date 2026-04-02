package llmrouter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/hugr-lab/hub/pkg/auth"
)

// OpenAICompatHandler returns an http.Handler for /v1/chat/completions.
// Third-party agents (OpenClaw) can use this endpoint with Hub Service budget enforcement.
func (r *Router) OpenAICompatHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", r.handleChatCompletions)
	return mux
}

func (r *Router) handleChatCompletions(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract user from context or Authorization header
	userID := ""
	if u, ok := auth.UserFromContext(req.Context()); ok {
		userID = u.ID
	}
	if userID == "" {
		// Try Bearer token as user_id (agent uses session token)
		if auth := req.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			userID = strings.TrimPrefix(auth, "Bearer ")
		}
	}
	if userID == "" {
		http.Error(w, `{"error":{"message":"user identity required"}}`, http.StatusUnauthorized)
		return
	}

	var body struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		MaxTokens int `json:"max_tokens"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"invalid request: %v"}}`, err), http.StatusBadRequest)
		return
	}

	var messages []Message
	for _, m := range body.Messages {
		messages = append(messages, Message{Role: m.Role, Content: m.Content})
	}

	resp, err := r.Complete(req.Context(), CompletionRequest{
		Provider:  body.Model, // model as provider hint
		Messages:  messages,
		MaxTokens: body.MaxTokens,
		UserID:    userID,
	})
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "budget exceeded") {
			status = http.StatusTooManyRequests
		}
		http.Error(w, fmt.Sprintf(`{"error":{"message":"%s"}}`, err.Error()), status)
		return
	}

	// OpenAI-compatible response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":      "hub-" + resp.Provider,
		"object":  "chat.completion",
		"model":   resp.Model,
		"choices": []map[string]any{
			{
				"index":         0,
				"message":       map[string]string{"role": "assistant", "content": resp.Content},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]int{
			"prompt_tokens":     resp.TokensIn,
			"completion_tokens": resp.TokensOut,
			"total_tokens":      resp.TokensIn + resp.TokensOut,
		},
	})
}
