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
	Intent     string    `json:"intent,omitempty"` // routing hint: default, planning, tool_calling, summarization, classification
}

// RoutingConfig maps intent hints to model names for cost-efficient routing.
// If a model for a given intent is not configured, Default is used.
// If Default is empty, auto-resolve picks the first available LLM.
type RoutingConfig struct {
	Default   string            // model for unspecified or unknown intent
	IntentMap map[string]string // intent → model name
	Fallback  string            // model to try on error (empty = no fallback)
}

// ResolveModel returns the model name for a given intent.
// Falls back: IntentMap[intent] → Default → "" (auto-resolve).
func (rc *RoutingConfig) ResolveModel(intent string) string {
	if rc == nil {
		return ""
	}
	if intent != "" && rc.IntentMap != nil {
		if m, ok := rc.IntentMap[intent]; ok && m != "" {
			return m
		}
	}
	return rc.Default
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
	routing    *RoutingConfig
	logger     *slog.Logger
}

func New(hugrClient *client.Client, logger *slog.Logger) *Router {
	return &Router{
		hugrClient: hugrClient,
		budget:     NewBudgetChecker(hugrClient, logger),
		logger:     logger,
	}
}

// SetRoutingConfig installs intent→model routing. Safe to call before any
// Complete/Stream call. Nil config = all intents auto-resolve.
func (r *Router) SetRoutingConfig(rc *RoutingConfig) {
	r.routing = rc
}

// Complete routes a request through Hugr's core.models.chat_completion.
func (r *Router) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	// 0. Resolve model via intent routing, then auto-resolve if still empty
	if req.Model == "" && req.Intent != "" {
		req.Model = r.routing.ResolveModel(req.Intent)
	}
	if req.Model == "" {
		models, err := r.ListModels(ctx)
		if err != nil {
			r.logger.Warn("auto-resolve model: list failed", "error", err)
			return CompletionResponse{}, fmt.Errorf("list models: %w", err)
		}
		r.logger.Debug("auto-resolve model", "available", len(models))
		for _, m := range models {
			r.logger.Debug("model candidate", "name", m.Name, "type", m.Type)
			if m.Type == "llm" {
				req.Model = m.Name
				break
			}
		}
		if req.Model == "" {
			return CompletionResponse{}, fmt.Errorf("no LLM models configured")
		}
		r.logger.Info("auto-resolved model", "model", req.Model)
	}

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

	varsDebug, _ := json.Marshal(map[string]any{"model": req.Model, "messages": msgStrings})
	r.logger.Debug("graphql request", "vars", string(varsDebug))

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

	// Build GraphQL query dynamically — only include optional args that are set.
	// Hugr query-engine rejects null values for [String!] typed variables.
	var extraParams, extraArgs string
	if len(toolStrings) > 0 {
		extraParams += ", $tools: [String!]"
		extraArgs += ", tools: $tools"
	}
	if req.ToolChoice != "" {
		extraParams += ", $tool_choice: String"
		extraArgs += ", tool_choice: $tool_choice"
	}
	if req.MaxTokens > 0 {
		extraParams += ", $max_tokens: Int"
		extraArgs += ", max_tokens: $max_tokens"
	}

	gql := fmt.Sprintf(`query($model: String!, $messages: [String!]!%s) {
		function { core { models {
			chat_completion(model: $model, messages: $messages%s) {
				content model finish_reason prompt_tokens completion_tokens total_tokens provider latency_ms tool_calls
			}
		} } }
	}`, extraParams, extraArgs)

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

	// 5. Record usage with intent metadata
	r.budget.RecordUsage(ctx, req.UserID, req.Model, resp.TokensIn, resp.TokensOut, req.Intent, resp.Model)

	r.logger.Info("llm completion",
		"user", req.UserID,
		"model", req.Model,
		"intent", req.Intent,
		"resolved_model", resp.Model,
		"provider", resp.Provider,
		"tokens_in", resp.TokensIn,
		"tokens_out", resp.TokensOut,
		"latency_ms", resp.LatencyMs,
	)

	return resp, nil
}

// ListModels returns available LLM model sources from Hugr.
// Returns empty list (not error) when no LLM data sources are configured.
func (r *Router) ListModels(ctx context.Context) ([]ModelInfo, error) {
	gql := `{ function { core { models { model_sources { name type provider model } } } } }`
	res, err := r.hugrClient.Query(ctx, gql, nil)
	if err != nil {
		// No LLM configured — return empty list, not error
		r.logger.Debug("list models failed (no LLM configured?)", "error", err)
		return []ModelInfo{}, nil
	}
	defer res.Close()
	if res.Err() != nil {
		r.logger.Debug("list models query error (no LLM configured?)", "error", res.Err())
		return []ModelInfo{}, nil
	}
	var models []ModelInfo
	if err := res.ScanData("function.core.models.model_sources", &models); err != nil {
		return []ModelInfo{}, nil
	}
	return models, nil
}

// CompleteDirect does a simple LLM chat completion without tools (for mode=llm).
func (r *Router) CompleteDirect(ctx context.Context, model, message string) (string, error) {
	resp, err := r.Complete(ctx, CompletionRequest{
		Model: model,
		Messages: []Message{
			{Role: "user", Content: message},
		},
	})
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// MakeSummaryRequest creates a CompletionRequest for summarizing conversation history.
func (r *Router) MakeSummaryRequest(history string) CompletionRequest {
	return CompletionRequest{
		Messages: []Message{
			{Role: "system", Content: "Summarize the following conversation concisely, preserving key facts, decisions, and context. Reply with ONLY the summary, nothing else."},
			{Role: "user", Content: history},
		},
	}
}

// ModelInfo describes a registered AI model data source.
type ModelInfo struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}
