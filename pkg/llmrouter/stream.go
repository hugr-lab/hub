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
	// 0. Resolve model via intent routing, then auto-resolve
	if req.Model == "" && req.Intent != "" {
		req.Model = r.routing.ResolveModel(req.Intent)
	}
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

	// 4. Build subscription variables. All optional args are passed as
	// nullable GraphQL variables — when unset they go in as `null` and the
	// server uses its default. No string interpolation.
	vars := map[string]any{
		"model":    req.Model,
		"messages": msgStrings,
	}
	if len(toolStrings) > 0 {
		vars["tools"] = toolStrings
	} else {
		vars["tools"] = nil
	}
	if req.ToolChoice != "" {
		vars["tool_choice"] = req.ToolChoice
	} else {
		vars["tool_choice"] = nil
	}
	if req.MaxTokens > 0 {
		vars["max_tokens"] = req.MaxTokens
	} else {
		vars["max_tokens"] = nil
	}

	// chat_completion is exposed on Hugr's Subscription root under
	//   subscription { core { models { chat_completion(...) { ... } } } }
	// (verified via __schema introspection of subscriptionType — the field
	// path is core → models → chat_completion). Returns llm_stream_event:
	//   { type, content, model, finish_reason, tool_calls,
	//     prompt_tokens, completion_tokens }
	// where `type` is one of "content_delta", "reasoning", "tool_use",
	// "finish", "error".
	const gql = `subscription(
		$model: String!
		$messages: [String!]!
		$tools: [String!]
		$tool_choice: String
		$max_tokens: Int
	) {
		core { models { chat_completion(
			model: $model
			messages: $messages
			tools: $tools
			tool_choice: $tool_choice
			max_tokens: $max_tokens
		) {
			type content model finish_reason tool_calls prompt_tokens completion_tokens
		} } }
	}`

	// 5. Subscribe
	sub, err := r.hugrClient.Subscribe(ctx, gql, vars)
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	defer sub.Cancel()

	// 6. Iterate events
	var lastModel, lastFinishReason string
	var totalIn, totalOut int
	var contentChunks, totalChunks int

	for event := range sub.Events {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		reader := event.Reader
		for reader.Next() {
			rec := reader.Record()
			totalChunks++

			// llm_stream_event columns from query-engine's runtime/models source.
			eventType := getStringField(rec, "type")
			content := getStringField(rec, "content")
			toolCalls := getStringField(rec, "tool_calls")
			finishReason := getStringField(rec, "finish_reason")
			model := getStringField(rec, "model")
			promptTokens := getIntField(rec, "prompt_tokens")
			completionTokens := getIntField(rec, "completion_tokens")
			if content != "" && (eventType == "" || eventType == "content_delta") {
				contentChunks++
			}

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

			// Dispatch on the explicit `type` column (preferred). Fall back to
			// content/tool_calls heuristics for older Hugr builds that didn't
			// expose `type` on the event row.
			switch eventType {
			case "content_delta":
				if content != "" {
					cb(StreamChunk{Type: "token", Content: content})
				}
			case "reasoning":
				if content != "" {
					cb(StreamChunk{Type: "thinking", Content: content})
				}
			case "tool_use":
				if toolCalls != "" && toolCalls != "[]" {
					cb(StreamChunk{Type: "tool_calls", ToolCalls: toolCalls})
				}
			case "finish":
				// finish is handled by the post-loop "done" emit below.
			case "error":
				cb(StreamChunk{Type: "error", Content: content})
			default:
				// Unknown / empty type — fall back to old heuristic.
				if content != "" {
					cb(StreamChunk{Type: "token", Content: content})
				}
				if toolCalls != "" && toolCalls != "[]" {
					cb(StreamChunk{Type: "tool_calls", ToolCalls: toolCalls})
				}
			}
		}

		if err := reader.Err(); err != nil {
			cb(StreamChunk{Type: "error", Content: err.Error()})
			return fmt.Errorf("subscription read: %w", err)
		}
	}

	// Surface "subscription closed without producing data" as an explicit
	// error rather than silently returning empty content. This catches
	// upstream subscription errors that the client only logs at debug level
	// and would otherwise turn into a mysterious blank LLM response.
	if totalChunks == 0 {
		// Check if the subscription reported an error (e.g. invalid model, missing args).
		if subErr := sub.Err(); subErr != nil {
			cb(StreamChunk{Type: "error", Content: subErr.Error()})
			return fmt.Errorf("LLM subscription error: %w", subErr)
		}
		err := fmt.Errorf("LLM stream returned no events (subscription closed without data)")
		cb(StreamChunk{Type: "error", Content: err.Error()})
		return err
	}
	if contentChunks == 0 && lastFinishReason == "" {
		r.logger.Warn("llm stream returned chunks but no content delta",
			"model", lastModel, "total_chunks", totalChunks)
	}

	// 7. Done
	cb(StreamChunk{
		Type:         "done",
		Model:        lastModel,
		FinishReason: lastFinishReason,
		TokensIn:     totalIn,
		TokensOut:    totalOut,
	})

	// 8. Record budget usage with intent metadata
	r.budget.RecordUsage(ctx, req.UserID, req.Model, totalIn, totalOut, req.Intent, lastModel)

	r.logger.Info("llm stream complete",
		"user", req.UserID,
		"model", lastModel,
		"intent", req.Intent,
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
