package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

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

		// Hugr tools added on top by query-engine mcp package
		hugrMCP := qemcp.New(s.hugrClient, mcpSrv, s.debug)
		hugrMCP.Handler().ServeHTTP(w, r)
	})
}

// HandleUserMessage processes a chat message from the WebSocket gateway.
// Searches memory for context, then calls LLM with the user message.
func (s *Server) HandleUserMessage(ctx context.Context, userID, message string) (string, error) {
	// Search relevant memories
	var memoryCtx string
	res, err := s.hugrClient.Query(ctx,
		`query($uid: String!) { hub { db { agent_memory(filter: { user_id: { eq: $uid } }, limit: 5, order_by: [{field: "created_at", direction: DESC}]) { content category } } } }`,
		map[string]any{"uid": userID},
	)
	if err == nil {
		defer res.Close()
		if res.Err() == nil {
			data, _ := json.Marshal(res.Data)
			memoryCtx = string(data)
		}
	}

	// Call LLM
	systemPrompt := "You are a helpful data assistant for Analytics Hub."
	if memoryCtx != "" {
		systemPrompt += "\n\nRelevant memories:\n" + memoryCtx
	}

	resp, err := s.llmRouter.Complete(ctx, llmrouter.CompletionRequest{
		Messages: []llmrouter.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: message},
		},
		UserID: userID,
	})
	if err != nil {
		return "", fmt.Errorf("llm complete: %w", err)
	}

	// Store user pattern
	storeCtx := auth.ContextWithUser(ctx, auth.UserInfo{ID: userID})
	storeRes, err := s.hugrClient.Query(storeCtx,
		`mutation($uid: String!, $content: String!) {
			hub { db { insert_agent_memory(
				data: { id: $uid, user_id: $uid, content: $content, category: "user_pattern" }
				summary: $content
			) { id } } }
		}`,
		map[string]any{"uid": userID, "content": fmt.Sprintf("User asked: %s", message)},
	)
	if err == nil {
		defer storeRes.Close()
	}

	return resp.Content, nil
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
