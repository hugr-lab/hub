package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// LocalMCPServer represents a running local MCP server process.
type LocalMCPServer struct {
	Name   string
	Config MCPServerConfig
	Client *mcpclient.Client
	Tools  []mcp.Tool
}

// MCPServerManager starts and manages local MCP server processes.
type MCPServerManager struct {
	servers map[string]*LocalMCPServer
	configs []MCPServerConfig
	logger  *slog.Logger
}

func NewMCPServerManager(configs []MCPServerConfig, logger *slog.Logger) *MCPServerManager {
	return &MCPServerManager{
		servers: make(map[string]*LocalMCPServer),
		configs: configs,
		logger:  logger,
	}
}

// Start launches all auto_start servers.
func (m *MCPServerManager) Start(ctx context.Context) error {
	for _, cfg := range m.configs {
		if !cfg.AutoStart {
			continue
		}
		if err := m.StartServer(ctx, cfg); err != nil {
			m.logger.Warn("failed to start MCP server", "name", cfg.Name, "error", err)
		}
	}
	return nil
}

// StartServer starts a single local MCP server and connects to it via stdio.
func (m *MCPServerManager) StartServer(ctx context.Context, cfg MCPServerConfig) error {
	// Build environment
	var env []string
	env = append(env, os.Environ()...)
	for k, v := range cfg.Env {
		env = append(env, k+"="+v)
	}

	client, err := mcpclient.NewStdioMCPClient(cfg.Command, env, cfg.Args...)
	if err != nil {
		return fmt.Errorf("create stdio client for %s: %w", cfg.Name, err)
	}

	if err := client.Start(ctx); err != nil {
		return fmt.Errorf("start client for %s: %w", cfg.Name, err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ClientInfo = mcp.Implementation{Name: "hub-agent", Version: "0.1.0"}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION

	if _, err := client.Initialize(ctx, initReq); err != nil {
		client.Close()
		return fmt.Errorf("initialize %s: %w", cfg.Name, err)
	}

	toolsResp, err := client.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		client.Close()
		return fmt.Errorf("list tools for %s: %w", cfg.Name, err)
	}

	srv := &LocalMCPServer{
		Name:   cfg.Name,
		Config: cfg,
		Client: client,
		Tools:  toolsResp.Tools,
	}
	m.servers[cfg.Name] = srv

	m.logger.Info("started local MCP server",
		"name", cfg.Name,
		"command", cfg.Command,
		"tools", len(toolsResp.Tools),
	)
	return nil
}

// CallTool calls a tool on a specific local MCP server.
func (m *MCPServerManager) CallTool(ctx context.Context, serverName, toolName string, args map[string]any) (*mcp.CallToolResult, error) {
	srv, ok := m.servers[serverName]
	if !ok {
		return nil, fmt.Errorf("MCP server %q not running", serverName)
	}

	req := mcp.CallToolRequest{}
	req.Params.Name = toolName
	req.Params.Arguments = args

	return srv.Client.CallTool(ctx, req)
}

// AllTools returns tools from all running servers.
func (m *MCPServerManager) AllTools() []mcp.Tool {
	var tools []mcp.Tool
	for _, srv := range m.servers {
		tools = append(tools, srv.Tools...)
	}
	return tools
}

// ServerForTool finds which server owns a given tool name.
func (m *MCPServerManager) ServerForTool(toolName string) (string, bool) {
	for _, srv := range m.servers {
		for _, t := range srv.Tools {
			if t.Name == toolName {
				return srv.Name, true
			}
		}
	}
	return "", false
}

// Stop shuts down all running servers.
func (m *MCPServerManager) Stop() {
	for name, srv := range m.servers {
		if err := srv.Client.Close(); err != nil {
			m.logger.Warn("error closing MCP server", "name", name, "error", err)
		}
		m.logger.Info("stopped local MCP server", "name", name)
	}
	m.servers = make(map[string]*LocalMCPServer)
}
