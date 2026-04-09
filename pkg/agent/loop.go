package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hugr-lab/hub/pkg/llmrouter"
)

// ToolCall represents a parsed tool call from LLM response.
type ToolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// LLMResponse is the parsed response from llm-complete.
type LLMResponse struct {
	Content      string     `json:"content"`
	FinishReason string     `json:"finish_reason"`
	ToolCalls    []ToolCall `json:"tool_calls_parsed"`
	TokensIn     int        `json:"tokens_in"`
	TokensOut    int        `json:"tokens_out"`
}

// RunLoop executes the multi-turn agentic loop with a single user message.
func (a *Agent) RunLoop(ctx context.Context, userMessage string) (string, error) {
	// 1. Retrieve learned context
	learnedContext := a.learner.RetrieveContext(ctx, userMessage)

	// 2. Build system prompt
	systemPrompt := a.skills.SystemPrompt()
	if learnedContext != "" {
		systemPrompt += "\n\n" + learnedContext
	}

	// 3. Initialize message history
	history := []llmrouter.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userMessage},
	}

	return a.runAgenticLoop(ctx, history)
}

// RunLoopWithHistory executes the multi-turn agentic loop with full conversation history.
// The history is used as-is (client owns it), with learned context prepended if missing system prompt.
func (a *Agent) RunLoopWithHistory(ctx context.Context, history []llmrouter.Message) (string, error) {
	// If no system prompt in history, prepend one
	if len(history) == 0 || history[0].Role != "system" {
		// Retrieve context from last user message
		var lastUserMsg string
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].Role == "user" {
				lastUserMsg = history[i].Content
				break
			}
		}
		learnedContext := a.learner.RetrieveContext(context.Background(), lastUserMsg)
		systemPrompt := a.skills.SystemPrompt()
		if learnedContext != "" {
			systemPrompt += "\n\n" + learnedContext
		}
		history = append([]llmrouter.Message{{Role: "system", Content: systemPrompt}}, history...)
	}

	return a.runAgenticLoop(ctx, history)
}

// runAgenticLoop is the core multi-turn loop shared by RunLoop and RunLoopWithHistory.
func (a *Agent) runAgenticLoop(ctx context.Context, history []llmrouter.Message) (string, error) {
	// Get tools for LLM
	tools := a.registry.ToLLMTools()

	// Agentic loop
	for turn := 0; turn < a.config.MaxTurns; turn++ {
		resp, err := a.callLLM(ctx, history, tools)
		if err != nil {
			a.logger.Error("LLM call failed", "turn", turn, "history_len", len(history), "error", err)
			return "", fmt.Errorf("turn %d: %w", turn, err)
		}

		// No tool calls — final response
		if len(resp.ToolCalls) == 0 {
			return resp.Content, nil
		}

		// Append assistant message with tool calls
		history = append(history, llmrouter.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: toolCallsToAny(resp.ToolCalls),
		})

		// Execute each tool call
		for _, tc := range resp.ToolCalls {
			result, err := a.executeTool(ctx, tc)
			resultText := result
			if err != nil {
				resultText = fmt.Sprintf("Error: %v", err)
			}

			// Append tool result
			history = append(history, llmrouter.Message{
				Role:       "tool",
				Content:    resultText,
				ToolCallID: tc.ID,
			})

			// Learn from tool call
			if err == nil {
				a.learner.LearnFromToolCall(ctx, tc.Name, tc.Arguments, result)
			}
		}
	}

	return "", fmt.Errorf("max turns (%d) reached", a.config.MaxTurns)
}

// callLLM calls llm-complete on Hub Service MCP with messages and tools.
func (a *Agent) callLLM(ctx context.Context, messages []llmrouter.Message, tools []llmrouter.Tool) (*LLMResponse, error) {
	args := map[string]any{
		"messages": messages,
	}
	if len(tools) > 0 {
		args["tools"] = tools
		args["tool_choice"] = "auto"
	}

	text, err := a.CallHubTool(ctx, "llm-complete", args)
	if err != nil {
		return nil, err
	}

	var resp llmrouter.CompletionResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		// If not JSON, treat as plain text response
		return &LLMResponse{Content: text, FinishReason: "stop"}, nil
	}

	llmResp := &LLMResponse{
		Content:      resp.Content,
		FinishReason: resp.FinishReason,
		TokensIn:     resp.TokensIn,
		TokensOut:    resp.TokensOut,
	}

	// Parse tool_calls JSON string
	if resp.ToolCalls != "" {
		var tcs []ToolCall
		if err := json.Unmarshal([]byte(resp.ToolCalls), &tcs); err == nil {
			llmResp.ToolCalls = tcs
		}
	}

	return llmResp, nil
}

// executeTool routes a tool call to the correct MCP server.
func (a *Agent) executeTool(ctx context.Context, tc ToolCall) (string, error) {
	source, found := a.registry.Lookup(tc.Name)
	if !found {
		return "", fmt.Errorf("unknown tool: %s", tc.Name)
	}

	if source == sourceHub {
		return a.CallHubTool(ctx, tc.Name, tc.Arguments)
	}

	// Local MCP server
	result, err := a.mcpManager.CallTool(ctx, source, tc.Name, tc.Arguments)
	if err != nil {
		return "", err
	}

	if result.IsError {
		return extractText(result), fmt.Errorf("tool error")
	}
	return extractText(result), nil
}

func toolCallsToAny(tcs []ToolCall) []any {
	result := make([]any, len(tcs))
	for i, tc := range tcs {
		result[i] = tc
	}
	return result
}
