package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hugr-lab/hub/pkg/agentconn"
	"github.com/hugr-lab/hub/pkg/auth"
	"github.com/hugr-lab/hub/pkg/llmrouter"
	"github.com/hugr-lab/query-engine/client"
	qemcp "github.com/hugr-lab/query-engine/pkg/mcp"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Server provides per-user MCP endpoints at /mcp.
// Hugr discovery/schema/data tools come from query-engine/pkg/mcp.
// Memory, registry, and LLM tools are added on top.
type Server struct {
	hugrClient  *client.Client
	llmRouter   *llmrouter.Router
	agentConn   *agentconn.Manager
	logger      *slog.Logger
	debug       bool
	storagePath string // HUB_STORAGE_PATH for disk checkpoints
}

func New(hugrClient *client.Client, llmRouter *llmrouter.Router, logger *slog.Logger, debug bool) *Server {
	storagePath := os.Getenv("HUB_STORAGE_PATH")
	if storagePath == "" {
		storagePath = "/var/hub-storage"
	}
	return &Server{
		hugrClient:  hugrClient,
		llmRouter:   llmRouter,
		logger:      logger,
		debug:       debug,
		storagePath: storagePath,
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

// ChatUsage carries token usage info aggregated across all turns of an
// agentic loop. Returned alongside the final response text from
// HandleUserMessage so the WebSocket gateway can attach a usage block to the
// `response` event the client renders in its message footer.
type ChatUsage struct {
	Model     string
	TokensIn  int
	TokensOut int
}

// HandleUserMessage processes a chat via multi-turn agentic loop.
// Hub streams tool_calls/tool_results back to the client.
// Returns the final assistant text + cumulative token usage across all turns.
//
// conversationID is used to load history from DB when clientMessages is empty
// (Spec F stateful sessions). When clientMessages is non-empty it is used
// as-is for backward compatibility during the migration window.
func (s *Server) HandleUserMessage(ctx context.Context, userID string, clientMessages []llmrouter.Message, stream StatusCallback, conversationID string) (string, *ChatUsage, error) {
	const maxTurns = 15

	tools := s.getToolsForLLM()

	// Stateful sessions (Spec F): if client sent no history, load from DB.
	// This enables tab-close resilience and cross-device resume.
	var history []llmrouter.Message
	if len(clientMessages) > 0 {
		history = make([]llmrouter.Message, len(clientMessages))
		copy(history, clientMessages)
	} else if conversationID != "" {
		loaded, err := s.loadConversationHistory(ctx, conversationID)
		if err != nil {
			s.logger.Warn("failed to load conversation history from DB, proceeding empty", "conversation", conversationID, "error", err)
		} else {
			history = loaded
		}
	}

	// Aggregate usage across turns. The model name comes from the last
	// `done` event we observed (all turns of one chat message hit the same
	// model). Token counts are summed so the user sees the full cost of
	// the agentic loop, not just the last turn.
	usage := &ChatUsage{}

	// Write agent_runs row (Spec F: lightweight run index).
	// The deferred update sets final status after the loop returns.
	runID := fmt.Sprintf("run-%d", time.Now().UnixNano())
	if conversationID != "" {
		s.insertAgentRun(ctx, runID, conversationID)
		defer func() {
			status := "done"
			errMsg := ""
			if usage.TokensIn == 0 && usage.TokensOut == 0 {
				status = "crashed"
				errMsg = "no tokens recorded"
			}
			s.updateAgentRun(ctx, runID, status, errMsg, usage.TokensIn, usage.TokensOut, usage.Model)
			if status == "done" {
				s.writeCheckpoint(conversationID, runID, usage)
			}
		}()
	}

	// Multi-turn loop with streaming
	for turn := 0; turn < maxTurns; turn++ {
		if ctx.Err() != nil {
			return "", usage, ctx.Err()
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
				if chunk.Model != "" {
					usage.Model = chunk.Model
				}
				usage.TokensIn += chunk.TokensIn
				usage.TokensOut += chunk.TokensOut
			}
		})
		if err != nil {
			return "", usage, fmt.Errorf("turn %d: %w", turn, err)
		}

		responseText := accumulated.String()

		// No tool calls — final response
		if toolCallsJSON == "" || finishReason == "stop" {
			return responseText, usage, nil
		}

		// Parse tool calls
		var toolCalls []struct {
			ID        string         `json:"id"`
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(toolCallsJSON), &toolCalls); err != nil {
			return responseText, usage, nil
		}
		if len(toolCalls) == 0 {
			return responseText, usage, nil
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
				return "", usage, ctx.Err()
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

	return "", usage, fmt.Errorf("max turns (%d) reached", maxTurns)
}

// loadConversationHistory loads messages for a conversation from DB via Hugr
// GraphQL. For branched conversations this uses the conversation_context view
// which walks the parent chain. Returns messages ordered by created_at.
func (s *Server) loadConversationHistory(ctx context.Context, conversationID string) ([]llmrouter.Message, error) {
	res, err := s.hugrClient.Query(ctx,
		`query($cid: String!) { hub { db { conversation_context(
			conversation_id: $cid
		) { role content tool_calls tool_call_id } } } }`,
		map[string]any{"cid": conversationID},
	)
	if err != nil {
		return nil, fmt.Errorf("load conversation history: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return nil, fmt.Errorf("load conversation history: %w", res.Err())
	}

	var rows []struct {
		Role       string `json:"role"`
		Content    string `json:"content"`
		ToolCalls  any    `json:"tool_calls"`
		ToolCallID string `json:"tool_call_id"`
	}
	if err := res.ScanData("hub.db.conversation_context", &rows); err != nil {
		return nil, fmt.Errorf("scan conversation history: %w", err)
	}

	messages := make([]llmrouter.Message, len(rows))
	for i, r := range rows {
		messages[i] = llmrouter.Message{
			Role:       r.Role,
			Content:    r.Content,
			ToolCallID: r.ToolCallID,
		}
		if r.ToolCalls != nil {
			data, _ := json.Marshal(r.ToolCalls)
			var tc []any
			json.Unmarshal(data, &tc)
			messages[i].ToolCalls = tc
		}
	}
	return messages, nil
}

// insertAgentRun creates an agent_runs row with status=running.
func (s *Server) insertAgentRun(ctx context.Context, runID, conversationID string) {
	res, err := s.hugrClient.Query(ctx,
		`mutation($id: String!, $cid: String!) {
			hub { db { insert_agent_runs(data: {
				id: $id, conversation_id: $cid, turn_index: 0, status: "running"
			}) { id } } }
		}`,
		map[string]any{"id": runID, "cid": conversationID},
	)
	if err != nil {
		s.logger.Warn("failed to insert agent_run", "run", runID, "error", err)
		return
	}
	defer res.Close()
	if res.Err() != nil {
		s.logger.Warn("insert agent_run error", "run", runID, "error", res.Err())
	}
}

// updateAgentRun finalizes an agent_runs row with status, tokens, model.
func (s *Server) updateAgentRun(ctx context.Context, runID, status, errMsg string, tokensIn, tokensOut int, model string) {
	vars := map[string]any{"id": runID, "status": status, "ti": tokensIn, "to": tokensOut}
	dataParts := `status: $status, tokens_in: $ti, tokens_out: $to`
	varDefs := `$id: String!, $status: String!, $ti: Int!, $to: Int!`

	if errMsg != "" {
		varDefs += `, $err: String`
		dataParts += `, error: $err`
		vars["err"] = errMsg
	}

	// Use context.WithoutCancel so the update survives request cancellation.
	updateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	gql := fmt.Sprintf(`mutation(%s) {
		hub { db { update_agent_runs(
			filter: { id: { eq: $id } },
			data: { %s }
		) { affected_rows } } }
	}`, varDefs, dataParts)

	res, err := s.hugrClient.Query(updateCtx, gql, vars)
	if err != nil {
		s.logger.Warn("failed to update agent_run", "run", runID, "error", err)
		return
	}
	defer res.Close()
	if res.Err() != nil {
		s.logger.Warn("update agent_run error", "run", runID, "error", res.Err())
	}
}

// writeCheckpoint writes L2 context snapshot and L1 turn state to disk.
// This is write-only in Spec F — recovery reads are deferred to Spec G.
func (s *Server) writeCheckpoint(conversationID, runID string, usage *ChatUsage) {
	if s.storagePath == "" || conversationID == "" {
		return
	}

	convDir := filepath.Join(s.storagePath, "conversations", conversationID)
	turnDir := filepath.Join(convDir, "turns", runID)

	// L1: turn directory with state.json
	if err := os.MkdirAll(turnDir, 0o755); err != nil {
		s.logger.Warn("failed to create turn dir", "path", turnDir, "error", err)
		return
	}
	stateJSON, _ := json.Marshal(map[string]any{
		"status":    "done",
		"tokens_in": usage.TokensIn,
		"tokens_out": usage.TokensOut,
		"model":     usage.Model,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
	if err := os.WriteFile(filepath.Join(turnDir, "state.json"), stateJSON, 0o644); err != nil {
		s.logger.Warn("failed to write state.json", "run", runID, "error", err)
	}

	// L2: append to context.jsonl
	contextFile := filepath.Join(convDir, "context.jsonl")
	entry, _ := json.Marshal(map[string]any{
		"turn_id":    runID,
		"tokens_in":  usage.TokensIn,
		"tokens_out": usage.TokensOut,
		"model":      usage.Model,
		"ts":         time.Now().UTC().Format(time.RFC3339),
	})
	f, err := os.OpenFile(contextFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		s.logger.Warn("failed to open context.jsonl", "path", contextFile, "error", err)
		return
	}
	f.Write(append(entry, '\n'))
	f.Close()

	// L2: checkpoint snapshot (copy of context.jsonl for branching)
	checkpointDir := filepath.Join(convDir, "checkpoints")
	os.MkdirAll(checkpointDir, 0o755)
	// The actual message_id for the snapshot ref would come from the
	// persist callback — for now we use the runID as a proxy.
	src, err := os.ReadFile(contextFile)
	if err == nil {
		os.WriteFile(filepath.Join(checkpointDir, runID+".bin"), src, 0o644)
	}
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
