package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/hugr-lab/hub/pkg/agentconn"
	"github.com/hugr-lab/hub/pkg/auth"
	"github.com/hugr-lab/hub/pkg/llmrouter"
	"github.com/hugr-lab/query-engine/client"
	qemcp "github.com/hugr-lab/query-engine/pkg/mcp"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Server provides per-user MCP endpoints at /mcp/{user_id}.
// Hugr discovery/schema/data tools come from query-engine/pkg/mcp.
// Memory, registry, and LLM tools are added on top.
type Server struct {
	hugrClient *client.Client
	llmRouter  *llmrouter.Router
	agentConn  *agentconn.Manager
	logger     *slog.Logger
	debug      bool
}

func New(hugrClient *client.Client, llmRouter *llmrouter.Router, logger *slog.Logger, debug bool) *Server {
	return &Server{
		hugrClient: hugrClient,
		llmRouter:  llmRouter,
		logger:     logger,
		debug:      debug,
	}
}

// SetAgentConn sets the agent connection manager for inter-agent communication.
func (s *Server) SetAgentConn(mgr *agentconn.Manager) {
	s.agentConn = mgr
}

// Handler returns an http.Handler for /mcp/{user_id} endpoints.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/mcp/"), "/", 2)
		if len(parts) == 0 || parts[0] == "" {
			http.Error(w, "user_id required in path", http.StatusBadRequest)
			return
		}
		userID := parts[0]

		// Verify auth context matches path user
		if authUser, ok := auth.UserFromContext(r.Context()); ok {
			if authUser.AuthType == "jwt" && authUser.ID != userID {
				http.Error(w, "forbidden: user mismatch", http.StatusForbidden)
				return
			}
			// For agent/management: allow access to path user's MCP
		}

		// Inject user identity into context — UserTransport adds headers to Hugr requests
		ctx := auth.ContextWithUser(r.Context(), auth.UserInfo{ID: userID})
		r = r.WithContext(ctx)

		// Create MCP server with Hub tools, then pass to query-engine mcp.New
		// which adds all Hugr discovery/schema/data tools on top
		mcpSrv := server.NewMCPServer(
			"hub-service",
			"0.1.0",
			server.WithToolCapabilities(true),
		)

		// Hub-specific tools
		s.registerMemoryTools(mcpSrv, userID)
		s.registerRegistryTools(mcpSrv, userID)
		s.registerLLMTools(mcpSrv, userID)
		s.registerConversationTools(mcpSrv, userID)
		s.registerAgentTools(mcpSrv, userID)

		// Hugr tools added on top by query-engine mcp package
		hugrMCP := qemcp.New(s.hugrClient, mcpSrv, s.debug)
		hugrMCP.Handler().ServeHTTP(w, r)
	})
}

// StatusCallback sends intermediate messages to the client.
type StatusCallback func(msgType, content string, toolCalls any, toolCallID string)

// HandleUserMessage processes a chat via multi-turn agentic loop.
// Client sends full message history. Hub streams tool_calls/tool_results back.
func (s *Server) HandleUserMessage(ctx context.Context, userID string, clientMessages []llmrouter.Message, stream StatusCallback) (string, error) {
	const maxTurns = 15

	tools := s.getToolsForLLM()

	// Use client history as-is — client owns the conversation state
	history := make([]llmrouter.Message, len(clientMessages))
	copy(history, clientMessages)

	// Multi-turn loop with streaming
	for turn := 0; turn < maxTurns; turn++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		var accumulated strings.Builder
		var toolCallsJSON string
		var finishReason string

		err := s.llmRouter.Stream(ctx, llmrouter.CompletionRequest{
			Messages: history,
			Tools:    tools,
			UserID:   userID,
		}, func(chunk llmrouter.StreamChunk) {
			switch chunk.Type {
			case "token":
				accumulated.WriteString(chunk.Content)
				if stream != nil {
					stream("token", chunk.Content, nil, "")
				}
			case "thinking":
				if stream != nil {
					stream("thinking", chunk.Content, nil, "")
				}
			case "tool_calls":
				toolCallsJSON = chunk.ToolCalls
			case "done":
				finishReason = chunk.FinishReason
			}
		})
		if err != nil {
			return "", fmt.Errorf("turn %d: %w", turn, err)
		}

		responseText := accumulated.String()

		// No tool calls — final response
		if toolCallsJSON == "" || finishReason == "stop" {
			return responseText, nil
		}

		// Parse tool calls
		var toolCalls []struct {
			ID        string         `json:"id"`
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(toolCallsJSON), &toolCalls); err != nil {
			return responseText, nil
		}
		if len(toolCalls) == 0 {
			return responseText, nil
		}

		// Stream tool_calls to client
		if stream != nil {
			stream("tool_call", responseText, toolCalls, "")
		}

		// Add assistant message with tool calls
		history = append(history, llmrouter.Message{
			Role:      "assistant",
			Content:   responseText,
			ToolCalls: toAnySlice(toolCalls),
		})

		// Execute each tool call
		for _, tc := range toolCalls {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}

			if stream != nil {
				stream("status", fmt.Sprintf("tool:%s", tc.Name), nil, "")
			}

			result, toolErr := s.executeTool(ctx, userID, tc.Name, tc.Arguments)
			if toolErr != nil {
				result = fmt.Sprintf("Error: %v", toolErr)
			}

			// Stream tool_result to client
			if stream != nil {
				stream("tool_result", result, nil, tc.ID)
			}

			history = append(history, llmrouter.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}

	return "", fmt.Errorf("max turns (%d) reached", maxTurns)
}

// executeTool calls a tool handler directly for tools mode.
func (s *Server) executeTool(ctx context.Context, userID, toolName string, args map[string]any) (string, error) {
	authCtx := auth.ContextWithUser(ctx, auth.UserInfo{ID: userID})

	// Build handler map — register all Hub tools + get their handlers
	handlers := s.buildToolHandlers(userID)

	handler, ok := handlers[toolName]
	if !ok {
		// Try Hugr tools via query-engine MCP (discovery, schema, data)
		return s.executeHugrTool(authCtx, toolName, args)
	}

	req := mcp.CallToolRequest{}
	req.Params.Name = toolName
	req.Params.Arguments = args

	result, err := handler(authCtx, req)
	if err != nil {
		return "", fmt.Errorf("tool %s: %w", toolName, err)
	}
	return extractToolText(result), nil
}

// buildToolHandlers returns a map of tool name → handler for Hub-specific tools.
func (s *Server) buildToolHandlers(userID string) map[string]server.ToolHandlerFunc {
	handlers := make(map[string]server.ToolHandlerFunc)

	// We can't extract handlers from MCPServer directly, so we maintain a parallel map.
	// This is the same handlers as registered in registerXxxTools.
	handlers["memory-store"] = s.handleMemoryStore(userID)
	handlers["memory-search"] = s.handleMemorySearch(userID)
	handlers["memory-list"] = s.handleMemoryList(userID)
	handlers["registry-save"] = s.handleRegistrySave(userID)
	handlers["registry-search"] = s.handleRegistrySearch(userID)
	if s.agentConn != nil {
		handlers["agent-message"] = s.handleAgentMessage(userID)
	}
	return handlers
}

// executeHugrTool calls a Hugr discovery/schema/data tool via query-engine MCP.
func (s *Server) executeHugrTool(ctx context.Context, toolName string, args map[string]any) (string, error) {
	// Create initialized MCP server with Hugr tools
	mcpSrv := server.NewMCPServer("hugr-tool-exec", "0.1.0", server.WithToolCapabilities(true))
	wrapped := qemcp.New(s.hugrClient, mcpSrv, s.debug)
	_ = wrapped

	// Initialize the MCP server (required before tool calls)
	initMsg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "hub-internal", "version": "0.1.0"},
		},
	})
	mcpSrv.HandleMessage(ctx, json.RawMessage(initMsg))

	// Now call the tool
	callMsg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": toolName, "arguments": args},
	})
	result := mcpSrv.HandleMessage(ctx, json.RawMessage(callMsg))

	resultBytes, _ := json.Marshal(result)
	var resp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resultBytes, &resp); err != nil {
		return string(resultBytes), nil
	}

	var text string
	for _, c := range resp.Result.Content {
		if c.Type == "text" {
			text += c.Text
		}
	}
	if resp.Result.IsError {
		return text, fmt.Errorf("tool error: %s", text)
	}
	return text, nil
}

func extractToolText(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	var text string
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			text += tc.Text
		}
	}
	return text
}

// getToolsForLLM returns tool definitions dynamically from query-engine MCP.
func (s *Server) getToolsForLLM() []llmrouter.Tool {
	// Create MCP server with all tools and list them
	mcpSrv := server.NewMCPServer("hugr-tools-list", "0.1.0", server.WithToolCapabilities(true))
	wrapped := qemcp.New(s.hugrClient, mcpSrv, s.debug)
	_ = wrapped

	// Initialize
	initMsg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "hub-tools-list", "version": "0.1.0"},
		},
	})
	mcpSrv.HandleMessage(context.Background(), json.RawMessage(initMsg))

	// List tools
	listMsg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/list",
	})
	result := mcpSrv.HandleMessage(context.Background(), json.RawMessage(listMsg))

	resultBytes, _ := json.Marshal(result)
	var resp struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				InputSchema any    `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resultBytes, &resp); err != nil {
		s.logger.Warn("failed to list MCP tools", "error", err)
		return nil
	}

	tools := make([]llmrouter.Tool, 0, len(resp.Result.Tools))
	for _, t := range resp.Result.Tools {
		// Convert inputSchema to map
		var params map[string]any
		if schema, ok := t.InputSchema.(map[string]any); ok {
			params = schema
		} else {
			schemaBytes, _ := json.Marshal(t.InputSchema)
			json.Unmarshal(schemaBytes, &params)
		}
		tools = append(tools, llmrouter.Tool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  params,
		})
	}

	s.logger.Info("loaded LLM tools from MCP", "count", len(tools))
	return tools
}

func toAnySlice(v any) []any {
	data, _ := json.Marshal(v)
	var result []any
	json.Unmarshal(data, &result)
	return result
}

func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(msg)},
		IsError: true,
	}
}

func toolResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(text)},
	}
}
