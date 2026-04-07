// Package agent implements the hub-agent runtime — a pure MCP client
// with multi-turn LLM reasoning. All tools are accessed via MCP protocol:
// Hub Service MCP (HTTP) for Hugr tools, local MCP servers (stdio) for sandbox.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// Agent is the main runtime. Connects to Hub Service MCP and local MCP servers,
// collects tools into a unified registry, and runs multi-turn LLM reasoning.
type Agent struct {
	hubURL     string
	authToken  string // AGENT_TOKEN for hub-service authentication
	hubClient  *mcpclient.Client
	mcpManager *MCPServerManager
	registry   *ToolRegistry
	skills     *SkillsEngine
	learner    *Learner
	config     *AgentConfig
	logger     *slog.Logger
}

func New(hubURL, authToken, skillsDir string, config *AgentConfig, logger *slog.Logger) *Agent {
	a := &Agent{
		hubURL:    hubURL,
		authToken: authToken,
		registry:  NewToolRegistry(),
		skills:    NewSkillsEngine(skillsDir, logger),
		config:    config,
		logger:    logger,
	}
	a.learner = NewLearner(a)
	a.mcpManager = NewMCPServerManager(config.MCPServers, logger)
	return a
}

// Connect establishes all MCP connections and builds the tool registry.
func (a *Agent) Connect(ctx context.Context) error {
	// 1. Connect to Hub Service MCP (HTTP transport)
	var opts []transport.StreamableHTTPCOption
	if a.authToken != "" {
		opts = append(opts, transport.WithHTTPHeaders(map[string]string{
			"Authorization": "Bearer " + a.authToken,
		}))
	}
	client, err := mcpclient.NewStreamableHttpClient(a.hubURL, opts...)
	if err != nil {
		return fmt.Errorf("create Hub MCP client: %w", err)
	}
	a.hubClient = client

	if err := a.hubClient.Start(ctx); err != nil {
		return fmt.Errorf("start Hub MCP client: %w", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ClientInfo = mcp.Implementation{Name: "hub-agent", Version: "0.1.0"}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION

	if _, err = a.hubClient.Initialize(ctx, initReq); err != nil {
		return fmt.Errorf("Hub MCP initialize: %w", err)
	}

	hubTools, err := a.hubClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return fmt.Errorf("Hub MCP list tools: %w", err)
	}
	a.registry.Register(sourceHub, hubTools.Tools)

	a.logger.Info("connected to Hub Service MCP",
		"url", a.hubURL,
		"tools", len(hubTools.Tools),
	)

	// 2. Start local MCP servers
	if err := a.mcpManager.Start(ctx); err != nil {
		a.logger.Warn("error starting local MCP servers", "error", err)
	}
	for _, srv := range a.mcpManager.servers {
		a.registry.Register(srv.Name, srv.Tools)
	}

	a.logger.Info("tool registry ready", "total_tools", len(a.registry.AllTools()))
	return nil
}

// CallHubTool calls a tool on Hub Service MCP.
func (a *Agent) CallHubTool(ctx context.Context, name string, args map[string]any) (string, error) {
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args

	result, err := a.hubClient.CallTool(ctx, req)
	if err != nil {
		return "", fmt.Errorf("call hub tool %s: %w", name, err)
	}

	if result.IsError {
		return extractText(result), fmt.Errorf("hub tool error: %s", extractText(result))
	}
	return extractText(result), nil
}

// HandleMessage processes a user message through the agentic loop.
func (a *Agent) HandleMessage(ctx context.Context, userMessage string) (string, error) {
	a.logger.Info("handling message", "length", len(userMessage))
	return a.RunLoop(ctx, userMessage)
}

// Run starts the agent in stdin/stdout mode.
func (a *Agent) Run(ctx context.Context) error {
	if err := a.Connect(ctx); err != nil {
		return err
	}
	a.skills.Load()

	a.logger.Info("agent ready, reading from stdin")

	scanner := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		var msg struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		}
		if err := scanner.Decode(&msg); err != nil {
			return nil // EOF
		}

		if msg.Type == "message" {
			response, err := a.HandleMessage(ctx, msg.Content)
			if err != nil {
				encoder.Encode(map[string]string{"type": "error", "content": err.Error()})
				continue
			}
			encoder.Encode(map[string]string{"type": "response", "content": response})
		}
	}
}

// Close disconnects from all MCP servers.
func (a *Agent) Close() error {
	a.mcpManager.Stop()
	if a.hubClient != nil {
		return a.hubClient.Close()
	}
	return nil
}

// extractText concatenates TextContent from an MCP call result.
func extractText(result *mcp.CallToolResult) string {
	var text string
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			text += tc.Text
		}
	}
	return text
}
