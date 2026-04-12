package mcpserver

import (
	"log/slog"
	"net/http"
	"os"

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
//
// The server-side agentic loop (HandleUserMessage) has been removed in Spec G.
// All tool-using conversations now route through hub-agent. This server
// retains only the /mcp HTTP handler with tool registrations — agents
// (workspace and remote) call these tools via MCP protocol.
type Server struct {
	hugrClient  *client.Client
	llmRouter   *llmrouter.Router
	agentConn   *agentconn.Manager
	logger      *slog.Logger
	debug       bool
	storagePath string
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

// Handler returns an http.Handler for /mcp endpoints.
// Identity is resolved from the auth middleware (bearer token), not from URL path.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authUser, ok := auth.UserFromContext(r.Context())
		if !ok || authUser.ID == "" {
			http.Error(w, "unauthorized: user identity required", http.StatusUnauthorized)
			return
		}
		userID := authUser.ID

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
