package llmrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/hugr-lab/query-engine/client"
)

// Message represents a chat message for LLM completion.
type Message struct {
	Role       string    `json:"role"`
	Content    string    `json:"content"`
	ToolCalls  []any     `json:"tool_calls,omitempty"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
}

// Tool represents an LLM tool definition (JSON Schema).
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

// CompletionRequest is the input to the LLM router.
type CompletionRequest struct {
	Model      string    `json:"model"`      // data source name (e.g. "claude", "gpt4")
	Messages   []Message `json:"messages"`
	Tools      []Tool    `json:"tools,omitempty"`
	ToolChoice string    `json:"tool_choice,omitempty"`
	MaxTokens  int       `json:"max_tokens,omitempty"`
	UserID     string    `json:"user_id"`
}

// CompletionResponse is the LLM output.
type CompletionResponse struct {
	Content      string `json:"content"`
	Model        string `json:"model"`
	Provider     string `json:"provider"`
	FinishReason string `json:"finish_reason"`
	TokensIn     int    `json:"tokens_in"`
	TokensOut    int    `json:"tokens_out"`
	TotalTokens  int    `json:"total_tokens"`
	LatencyMs    int    `json:"latency_ms"`
	ToolCalls    string `json:"tool_calls,omitempty"` // JSON string
}

// Router dispatches LLM requests via Hugr's core.models with budget enforcement.
type Router struct {
	hugrClient *client.Client
	budget     *BudgetChecker
	logger     *slog.Logger
}

func New(hugrClient *client.Client, logger *slog.Logger) *Router {
	return &Router{
		hugrClient: hugrClient,
		budget:     NewBudgetChecker(hugrClient, logger),
		logger:     logger,
	}
}

// Complete routes a request through Hugr's core.models.chat_completion.
func (r *Router) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	// 1. Check budget
	if err := r.budget.Check(ctx, req.UserID, req.Model); err != nil {
		return CompletionResponse{}, fmt.Errorf("budget exceeded: %w", err)
	}

	// 2. Build messages as JSON strings (core.models expects [String!]!)
	msgStrings := make([]string, len(req.Messages))
	for i, m := range req.Messages {
		b, _ := json.Marshal(m)
		msgStrings[i] = string(b)
	}

	// 3. Build tools as JSON strings
	var toolStrings []string
	for _, t := range req.Tools {
		b, _ := json.Marshal(t)
		toolStrings = append(toolStrings, string(b))
	}

	// 4. Call Hugr GraphQL: function.core.models.chat_completion
	vars := map[string]any{
		"model":    req.Model,
		"messages": msgStrings,
	}
	if len(toolStrings) > 0 {
		vars["tools"] = toolStrings
	}
	if req.ToolChoice != "" {
		vars["tool_choice"] = req.ToolChoice
	}
	if req.MaxTokens > 0 {
		vars["max_tokens"] = req.MaxTokens
	}

	gql := `query($model: String!, $messages: [String!]!, $tools: [String!], $tool_choice: String, $max_tokens: Int) {
		function { core { models {
			chat_completion(model: $model, messages: $messages, tools: $tools, tool_choice: $tool_choice, max_tokens: $max_tokens) {
				content model finish_reason prompt_tokens completion_tokens total_tokens provider latency_ms tool_calls
			}
		} } }
	}`

	res, err := r.hugrClient.Query(ctx, gql, vars)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("hugr query: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return CompletionResponse{}, fmt.Errorf("hugr graphql error: %w", res.Err())
	}

	var result struct {
		Content          string `json:"content"`
		Model            string `json:"model"`
		FinishReason     string `json:"finish_reason"`
		PromptTokens     int    `json:"prompt_tokens"`
		CompletionTokens int    `json:"completion_tokens"`
		TotalTokens      int    `json:"total_tokens"`
		Provider         string `json:"provider"`
		LatencyMs        int    `json:"latency_ms"`
		ToolCalls        string `json:"tool_calls"`
	}
	if err := res.ScanData("function.core.models.chat_completion", &result); err != nil {
		return CompletionResponse{}, fmt.Errorf("scan result: %w", err)
	}

	resp := CompletionResponse{
		Content:      result.Content,
		Model:        result.Model,
		Provider:     result.Provider,
		FinishReason: result.FinishReason,
		TokensIn:     result.PromptTokens,
		TokensOut:    result.CompletionTokens,
		TotalTokens:  result.TotalTokens,
		LatencyMs:    result.LatencyMs,
		ToolCalls:    result.ToolCalls,
	}

	// 5. Record usage
	r.budget.RecordUsage(ctx, req.UserID, req.Model, resp.TokensIn, resp.TokensOut)

	r.logger.Info("llm completion",
		"user", req.UserID,
		"model", req.Model,
		"provider", resp.Provider,
		"tokens_in", resp.TokensIn,
		"tokens_out", resp.TokensOut,
		"latency_ms", resp.LatencyMs,
	)

	return resp, nil
}

// ListModels returns available LLM model sources from Hugr.
func (r *Router) ListModels(ctx context.Context) ([]ModelInfo, error) {
	gql := `{ function { core { models { model_sources { name type provider model } } } } }`
	res, err := r.hugrClient.Query(ctx, gql, nil)
	if err != nil {
		return nil, fmt.Errorf("query model sources: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return nil, fmt.Errorf("model sources error: %w", res.Err())
	}
	var models []ModelInfo
	if err := res.ScanData("function.core.models.model_sources", &models); err != nil {
		return nil, err
	}
	return models, nil
}

// ModelInfo describes a registered AI model data source.
type ModelInfo struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}
