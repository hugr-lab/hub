package mcpserver

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/hugr-lab/hub/pkg/auth"
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
	logger     *slog.Logger
	debug      bool
}

func New(hugrClient *client.Client, logger *slog.Logger, debug bool) *Server {
	return &Server{
		hugrClient: hugrClient,
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

		// Hugr tools added on top by query-engine mcp package
		hugrMCP := qemcp.New(s.hugrClient, mcpSrv, s.debug)
		hugrMCP.Handler().ServeHTTP(w, r)
	})
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
