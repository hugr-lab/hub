package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hugr-lab/hub/pkg/llmrouter"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (s *Server) registerLLMTools(mcpSrv *server.MCPServer, userID string) {
	mcpSrv.AddTool(
		mcp.NewTool("llm-complete",
			mcp.WithDescription("Send messages to LLM and get completion. Budget is enforced per user."),
			mcp.WithString("provider", mcp.Description("Provider ID (empty = default for user's role)")),
			mcp.WithObject("messages", mcp.Required(), mcp.Description("Array of {role, content} message objects")),
			mcp.WithNumber("max_tokens", mcp.Description("Max output tokens")),
		),
		s.handleLLMComplete(userID),
	)

	mcpSrv.AddTool(
		mcp.NewTool("llm-list-providers",
			mcp.WithDescription("List available LLM providers for the current user."),
		),
		s.handleLLMListProviders(userID),
	)
}

func (s *Server) handleLLMComplete(userID string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if s.llmRouter == nil {
			return toolError("LLM router not configured"), nil
		}

		args := request.GetArguments()
		provider, _ := args["provider"].(string)

		var messages []llmrouter.Message
		if msgsRaw, ok := args["messages"]; ok {
			data, _ := json.Marshal(msgsRaw)
			json.Unmarshal(data, &messages)
		}
		if len(messages) == 0 {
			return toolError("messages required"), nil
		}

		maxTokens := 0
		if mt, ok := args["max_tokens"].(float64); ok {
			maxTokens = int(mt)
		}

		resp, err := s.llmRouter.Complete(ctx, llmrouter.CompletionRequest{
			Provider:  provider,
			Messages:  messages,
			MaxTokens: maxTokens,
			UserID:    userID,
		})
		if err != nil {
			return toolError(fmt.Sprintf("LLM error: %v", err)), nil
		}

		result, _ := json.MarshalIndent(resp, "", "  ")
		return toolResult(string(result)), nil
	}
}

func (s *Server) handleLLMListProviders(userID string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := s.hugrClient.Query(ctx,
			`{ hub { hub { llm_providers(filter: { enabled: { eq: true } }) {
				id provider model max_tokens_per_request
			} } } }`, nil)
		if err != nil {
			return toolError(fmt.Sprintf("failed to list providers: %v", err)), nil
		}

		data, _ := json.MarshalIndent(res.Data, "", "  ")
		return toolResult(string(data)), nil
	}
}
