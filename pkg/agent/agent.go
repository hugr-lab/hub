// Package agent implements the hub-agent runtime — a pure MCP client
// with multi-turn LLM reasoning. All tools are accessed via MCP protocol:
// Hub Service MCP (HTTP) for Hugr tools, local MCP servers (stdio) for sandbox.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"nhooyr.io/websocket"
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

// agentMessage is the wire format for Hub Service ↔ Agent WebSocket communication.
type agentMessage struct {
	RequestID      string `json:"request_id"`
	ConversationID string `json:"conversation_id,omitempty"`
	UserID         string `json:"user_id,omitempty"`
	Type           string `json:"type"` // request, response, status, error, ping, pong
	Content        string `json:"content,omitempty"`
}

// RunWebSocket connects to Hub Service via WebSocket and processes messages.
// Reconnects with exponential backoff on disconnect.
func (a *Agent) RunWebSocket(ctx context.Context, wsURL, instanceID string) error {
	if err := a.Connect(ctx); err != nil {
		return err
	}
	a.skills.Load()

	endpoint := wsURL + "/agent/ws/" + instanceID
	a.logger.Info("agent WebSocket mode", "endpoint", endpoint)

	attempt := 0
	for {
		if ctx.Err() != nil {
			return nil
		}

		err := a.wsSession(ctx, endpoint)
		if ctx.Err() != nil {
			return nil
		}

		// Exponential backoff: 1s, 2s, 4s, 8s, ... max 60s
		delay := time.Duration(math.Min(float64(time.Second)*math.Pow(2, float64(attempt)), float64(60*time.Second)))
		attempt++
		a.logger.Warn("WebSocket disconnected, reconnecting", "attempt", attempt, "delay", delay, "error", err)

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
	}
}

// wsSession runs a single WebSocket connection session.
func (a *Agent) wsSession(ctx context.Context, endpoint string) error {
	headers := make(map[string][]string)
	if a.authToken != "" {
		headers["Authorization"] = []string{"Bearer " + a.authToken}
	}

	conn, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{
		HTTPHeader: headers,
	})
	if err != nil {
		return fmt.Errorf("ws dial: %w", err)
	}
	defer conn.CloseNow()

	a.logger.Info("WebSocket connected", "endpoint", endpoint)

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("ws read: %w", err)
		}

		var msg agentMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			a.logger.Warn("invalid message from hub", "error", err)
			continue
		}

		switch msg.Type {
		case "ping":
			pong := agentMessage{Type: "pong"}
			d, _ := json.Marshal(pong)
			conn.Write(ctx, websocket.MessageText, d)

		case "request":
			// Process in goroutine so we can keep reading (e.g. for cancellation)
			go func(req agentMessage) {
				// Send status
				status := agentMessage{RequestID: req.RequestID, Type: "status", Content: "processing"}
				d, _ := json.Marshal(status)
				conn.Write(ctx, websocket.MessageText, d)

				response, err := a.HandleMessage(ctx, req.Content)
				var resp agentMessage
				if err != nil {
					resp = agentMessage{RequestID: req.RequestID, Type: "error", Content: err.Error()}
				} else {
					resp = agentMessage{RequestID: req.RequestID, Type: "response", Content: response}
				}
				d, _ = json.Marshal(resp)
				conn.Write(ctx, websocket.MessageText, d)
			}(msg)

		default:
			a.logger.Debug("ignoring message type", "type", msg.Type)
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
