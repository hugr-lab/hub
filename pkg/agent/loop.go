package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hugr-lab/hub/pkg/llmrouter"
)

// lastUserMessage extracts the last user message from history for routing.
func lastUserMessage(history []llmrouter.Message) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "user" {
			return history[i].Content
		}
	}
	return ""
}

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
	learnedContext := a.learner.RetrieveContext(ctx, userMessage)
	systemPrompt := a.buildSystemPrompt(userMessage, learnedContext)

	history := []llmrouter.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userMessage},
	}

	return a.runAgenticLoop(ctx, history)
}

// buildSystemPrompt routes the user message through the skill router and
// assembles a per-turn system prompt from matching skills.
func (a *Agent) buildSystemPrompt(userMessage, learnedContext string) string {
	available := a.skills.All()
	selectedIDs := a.skillRouter.Route(userMessage, available)

	var parts []string
	for _, id := range selectedIDs {
		full, err := a.skills.LoadFull(id)
		if err != nil {
			a.logger.Warn("failed to load skill", "id", id, "error", err)
			continue
		}
		if full.SystemPrompt != "" {
			parts = append(parts, full.SystemPrompt)
		}
	}

	if len(parts) == 0 {
		parts = append(parts, "You are a data analysis assistant. Help users explore and query data.")
	}

	prompt := strings.Join(parts, "\n\n")
	if learnedContext != "" {
		prompt += "\n\n" + learnedContext
	}

	a.logger.Debug("skill routing", "message_preview", truncate(userMessage, 50), "selected", selectedIDs)
	return prompt
}


// RunLoopWithHistory executes the multi-turn agentic loop with full conversation history.
// The history is used as-is (client owns it), with learned context prepended if missing system prompt.
func (a *Agent) RunLoopWithHistory(ctx context.Context, history []llmrouter.Message) (string, error) {
	if len(history) == 0 || history[0].Role != "system" {
		lastUserMsg := lastUserMessage(history)
		learnedContext := a.learner.RetrieveContext(ctx, lastUserMsg)
		systemPrompt := a.buildSystemPrompt(lastUserMsg, learnedContext)
		history = append([]llmrouter.Message{{Role: "system", Content: systemPrompt}}, history...)
	}
	return a.runAgenticLoop(ctx, history)
}

// runAgenticLoopWithStream is the multi-turn loop with streaming callbacks.
func (a *Agent) runAgenticLoopWithStream(ctx context.Context, history []llmrouter.Message, stream AgentStreamCallback) (string, error) {
	if len(history) == 0 || history[0].Role != "system" {
		lastUserMsg := lastUserMessage(history)
		learnedContext := a.learner.RetrieveContext(ctx, lastUserMsg)
		systemPrompt := a.buildSystemPrompt(lastUserMsg, learnedContext)
		history = append([]llmrouter.Message{{Role: "system", Content: systemPrompt}}, history...)
	}

	tools := a.registry.ToLLMTools()

	for turn := 0; turn < a.config.MaxTurns; turn++ {
		resp, err := a.callLLM(ctx, history, tools)
		if err != nil {
			return "", fmt.Errorf("turn %d: %w", turn, err)
		}

		if len(resp.ToolCalls) == 0 {
			return resp.Content, nil
		}

		// Stream tool_call
		if stream != nil {
			stream("tool_call", resp.Content, resp.ToolCalls, "")
		}

		history = append(history, llmrouter.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: toolCallsToAny(resp.ToolCalls),
		})

		for _, tc := range resp.ToolCalls {
			if stream != nil {
				stream("status", fmt.Sprintf("tool:%s", tc.Name), nil, "")
			}

			result, err := a.executeTool(ctx, tc)
			resultText := result
			if err != nil {
				resultText = fmt.Sprintf("Error: %v", err)
			}

			// Stream tool_result
			if stream != nil {
				stream("tool_result", resultText, nil, tc.ID)
			}

			history = append(history, llmrouter.Message{
				Role:       "tool",
				Content:    resultText,
				ToolCallID: tc.ID,
			})

			if err == nil {
				a.learner.LearnFromToolCall(ctx, tc.Name, tc.Arguments, result)
			}
		}
	}

	return "", fmt.Errorf("max turns (%d) reached", a.config.MaxTurns)
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
