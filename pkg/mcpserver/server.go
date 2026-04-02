package mcpserver

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/hugr-lab/hub/pkg/auth"
	"github.com/hugr-lab/query-engine/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Server provides per-user MCP endpoints at /mcp/{user_id}.
type Server struct {
	hugrClient *client.Client
	logger     *slog.Logger
}

func New(hugrClient *client.Client, logger *slog.Logger) *Server {
	return &Server{hugrClient: hugrClient, logger: logger}
}

// Handler returns an http.Handler for /mcp/{user_id} endpoints.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract user_id from path: /mcp/{user_id}
		parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/mcp/"), "/", 2)
		if len(parts) == 0 || parts[0] == "" {
			http.Error(w, "user_id required in path", http.StatusBadRequest)
			return
		}
		userID := parts[0]

		// Inject user identity into request context for Hugr client transport
		// TODO: lookup full user info (name, role) from hub.users
		ctx := auth.ContextWithUser(r.Context(), auth.UserInfo{ID: userID})
		r = r.WithContext(ctx)

		// Create per-request MCP server with user context
		mcpSrv := s.newUserMCPServer(userID)
		httpSrv := server.NewStreamableHTTPServer(mcpSrv)
		httpSrv.ServeHTTP(w, r)
	})
}

func (s *Server) newUserMCPServer(userID string) *server.MCPServer {
	mcpSrv := server.NewMCPServer(
		"hub-service",
		"0.1.0",
		server.WithToolCapabilities(true),
	)

	// Register Hugr discovery/query tools
	s.registerHugrTools(mcpSrv, userID)

	// Register memory tools
	s.registerMemoryTools(mcpSrv, userID)

	// Register registry tools
	s.registerRegistryTools(mcpSrv, userID)

	return mcpSrv
}

// toolError creates an MCP tool error result.
func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(msg),
		},
		IsError: true,
	}
}

// toolResult creates an MCP tool text result.
func toolResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(text),
		},
	}
}
