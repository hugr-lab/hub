package llmrouter

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
)

// StreamChunk represents a single chunk in a streaming LLM response.
type StreamChunk struct {
	Type         string // "token", "tool_calls", "thinking", "done", "error"
	Content      string // token text or thinking text
	ToolCalls    string // JSON tool_calls array (type="tool_calls")
	Model        string // on "done"
	FinishReason string // on "done"
	TokensIn     int    // on "done"
	TokensOut    int    // on "done"
}

// StreamCallback receives streaming chunks during LLM generation.
type StreamCallback func(chunk StreamChunk)

// Stream executes chat_completion as a Hugr subscription for token streaming.
func (r *Router) Stream(ctx context.Context, req CompletionRequest, cb StreamCallback) error {
	// 0. Resolve model
	if req.Model == "" {
		models, err := r.ListModels(ctx)
		if err != nil {
			return fmt.Errorf("list models: %w", err)
		}
		for _, m := range models {
			if m.Type == "llm" {
				req.Model = m.Name
				break
			}
		}
		if req.Model == "" {
			return fmt.Errorf("no LLM models configured")
		}
	}

	// 1. Check budget
	if err := r.budget.Check(ctx, req.UserID, req.Model); err != nil {
		return fmt.Errorf("budget exceeded: %w", err)
	}

	// 2. Build messages as JSON strings
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

	// 4. Build subscription query
	vars := map[string]any{
		"model":    req.Model,
		"messages": msgStrings,
	}

	var extraParams, extraArgs string
	if len(toolStrings) > 0 {
		extraParams += ", $tools: [String!]"
		extraArgs += ", tools: $tools"
		vars["tools"] = toolStrings
	}
	if req.ToolChoice != "" {
		extraParams += ", $tool_choice: String"
		extraArgs += ", tool_choice: $tool_choice"
		vars["tool_choice"] = req.ToolChoice
	}
	if req.MaxTokens > 0 {
		extraParams += ", $max_tokens: Int"
		extraArgs += ", max_tokens: $max_tokens"
		vars["max_tokens"] = req.MaxTokens
	}

	gql := fmt.Sprintf(`subscription($model: String!, $messages: [String!]!%s) {
		function { core { models {
			chat_completion(model: $model, messages: $messages%s) {
				content model finish_reason prompt_tokens completion_tokens total_tokens provider latency_ms tool_calls thinking
			}
		} } }
	}`, extraParams, extraArgs)

	// 5. Subscribe
	sub, err := r.hugrClient.Subscribe(ctx, gql, vars)
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	defer sub.Cancel()

	// 6. Iterate events
	var lastModel, lastFinishReason string
	var totalIn, totalOut int

	for event := range sub.Events {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		reader := event.Reader
		for reader.Next() {
			rec := reader.Record()

			// Extract fields from Arrow record
			content := getStringField(rec, "content")
			toolCalls := getStringField(rec, "tool_calls")
			thinking := getStringField(rec, "thinking")
			finishReason := getStringField(rec, "finish_reason")
			model := getStringField(rec, "model")
			promptTokens := getIntField(rec, "prompt_tokens")
			completionTokens := getIntField(rec, "completion_tokens")

			if model != "" {
				lastModel = model
			}
			if finishReason != "" {
				lastFinishReason = finishReason
			}
			if promptTokens > 0 {
				totalIn = promptTokens
			}
			if completionTokens > 0 {
				totalOut = completionTokens
			}

			// Emit chunks
			if thinking != "" {
				cb(StreamChunk{Type: "thinking", Content: thinking})
			}
			if content != "" {
				cb(StreamChunk{Type: "token", Content: content})
			}
			if toolCalls != "" && toolCalls != "[]" {
				cb(StreamChunk{Type: "tool_calls", ToolCalls: toolCalls})
			}
		}

		if err := reader.Err(); err != nil {
			cb(StreamChunk{Type: "error", Content: err.Error()})
			return fmt.Errorf("subscription read: %w", err)
		}
	}

	// 7. Done
	cb(StreamChunk{
		Type:         "done",
		Model:        lastModel,
		FinishReason: lastFinishReason,
		TokensIn:     totalIn,
		TokensOut:    totalOut,
	})

	// 8. Record budget usage
	r.budget.RecordUsage(ctx, req.UserID, req.Model, totalIn, totalOut)

	r.logger.Info("llm stream complete",
		"user", req.UserID,
		"model", lastModel,
		"tokens_in", totalIn,
		"tokens_out", totalOut,
	)

	return nil
}

// getStringField extracts a string field from an Arrow record by name.
func getStringField(rec arrow.Record, name string) string {
	schema := rec.Schema()
	for i, f := range schema.Fields() {
		if f.Name == name {
			col := rec.Column(i)
			if col.Len() == 0 || col.IsNull(0) {
				return ""
			}
			if sc, ok := col.(*array.String); ok {
				return sc.Value(0)
			}
			return ""
		}
	}
	return ""
}

// getIntField extracts an int field from an Arrow record by name.
func getIntField(rec arrow.Record, name string) int {
	schema := rec.Schema()
	for i, f := range schema.Fields() {
		if f.Name == name {
			col := rec.Column(i)
			if col.Len() == 0 || col.IsNull(0) {
				return 0
			}
			switch c := col.(type) {
			case *array.Int32:
				return int(c.Value(0))
			case *array.Int64:
				return int(c.Value(0))
			}
			return 0
		}
	}
	return 0
}
