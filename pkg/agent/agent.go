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
	"sync"
	"time"

	"github.com/hugr-lab/hub/pkg/llmrouter"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/coder/websocket"
)

// Agent is the main runtime. Connects to Hub Service MCP and local MCP servers,
// collects tools into a unified registry, and runs multi-turn LLM reasoning.
type Agent struct {
	hubURL      string
	authToken   string       // static AGENT_TOKEN (remote context)
	tokenSource *TokenSource // dynamic OIDC token (workspace context)
	hubClient   *mcpclient.Client
	mcpManager  *MCPServerManager
	registry    *ToolRegistry
	skills      *SkillCatalog
	skillRouter *SkillRouter
	learner     *Learner
	config      *AgentConfig
	logger      *slog.Logger

	// Per-conversation session state (local mode only)
	sessions *SessionManager
}

func New(hubURL, authToken, skillsDir string, config *AgentConfig, logger *slog.Logger) *Agent {
	a := &Agent{
		hubURL:      hubURL,
		authToken:   authToken,
		registry:    NewToolRegistry(),
		skills:      NewSkillCatalog(skillsDir, logger),
		skillRouter: NewSkillRouter(),
		sessions:    NewSessionManager(),
		config:      config,
		logger:      logger,
	}
	a.learner = NewLearner(a)
	a.mcpManager = NewMCPServerManager(config.MCPServers, logger)
	return a
}

// SetTokenSource sets a dynamic token source (for workspace context).
// When set, CurrentToken() returns the refreshed token instead of static authToken.
func (a *Agent) SetTokenSource(ts *TokenSource) {
	a.tokenSource = ts
}

// CurrentToken returns the current auth token — dynamic from TokenSource
// if set (workspace), otherwise static authToken (remote).
func (a *Agent) CurrentToken() string {
	if a.tokenSource != nil {
		if t := a.tokenSource.Token(); t != "" {
			return t
		}
	}
	return a.authToken
}

// Connect establishes all MCP connections and builds the tool registry.
func (a *Agent) Connect(ctx context.Context) error {
	// 1. Connect to Hub Service MCP (HTTP transport)
	// Use dynamic header function so the token is refreshed on every request.
	// OIDC tokens in workspace expire every 60s; static headers cause 401.
	opts := []transport.StreamableHTTPCOption{
		transport.WithHTTPHeaderFunc(func(_ context.Context) map[string]string {
			if t := a.CurrentToken(); t != "" {
				return map[string]string{"Authorization": "Bearer " + t}
			}
			return nil
		}),
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

	// 3. Load skill catalog and filter by agent capabilities (if agent_id set)
	a.skills.Load()
	agentID := os.Getenv("HUB_AGENT_ID")
	if agentID == "" {
		agentID = os.Getenv("AGENT_INSTANCE_ID")
	}
	if agentID != "" {
		a.filterSkillsByCapabilities(ctx, agentID)
	}
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

// filterSkillsByCapabilities calls hub.agent_capabilities via MCP to get
// the allowed skill list for this agent, then filters the skill catalog.
func (a *Agent) filterSkillsByCapabilities(ctx context.Context, agentID string) {
	// Call the agent_capabilities tool to get allowed skills
	result, err := a.CallHubTool(ctx, "data-inline_graphql_result", map[string]any{
		"query": fmt.Sprintf(`{ hub { agent_capabilities(args: {agent_id: "%s"}) { skill_id context } } }`, agentID),
	})
	if err != nil {
		a.logger.Warn("failed to fetch agent capabilities, using all skills", "agent", agentID, "error", err)
		return
	}

	// Parse the result to extract skill IDs
	var parsed struct {
		Hub struct {
			Capabilities []struct {
				SkillID string `json:"skill_id"`
				Context string `json:"context"`
			} `json:"agent_capabilities"`
		} `json:"hub"`
	}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		a.logger.Warn("failed to parse agent capabilities", "error", err)
		return
	}

	if len(parsed.Hub.Capabilities) == 0 {
		a.logger.Debug("no capabilities returned, using all skills", "agent", agentID)
		return
	}

	var allowedIDs []string
	runtimeCtx := "any"
	for _, c := range parsed.Hub.Capabilities {
		allowedIDs = append(allowedIDs, c.SkillID)
		if c.Context != "" && c.Context != "any" {
			runtimeCtx = c.Context
		}
	}

	// Filter catalog
	filtered := a.skills.Filter(runtimeCtx, allowedIDs)
	a.logger.Info("skills filtered by capabilities", "agent", agentID, "context", runtimeCtx, "allowed", len(filtered), "total", len(a.skills.All()))
}

// HandleMessage processes a user message through the agentic loop.
func (a *Agent) HandleMessage(ctx context.Context, userMessage string) (string, error) {
	a.logger.Info("handling message", "length", len(userMessage))
	return a.RunLoop(ctx, userMessage)
}

// AgentStreamCallback sends streaming messages back to Hub Service.
type AgentStreamCallback func(msgType, content string, toolCalls any, toolCallID string)

// HandleMessagesWithStream processes conversation history with streaming callbacks.
func (a *Agent) HandleMessagesWithStream(ctx context.Context, messages []historyMessage, stream AgentStreamCallback) (string, error) {
	a.logger.Info("handling messages with stream", "count", len(messages))

	history := make([]llmrouter.Message, 0, len(messages))
	for _, m := range messages {
		msg := llmrouter.Message{
			Role: m.Role, Content: m.Content,
			ToolCallID: m.ToolCallID,
		}
		if m.ToolCalls != nil {
			data, _ := json.Marshal(m.ToolCalls)
			var tcs []any
			json.Unmarshal(data, &tcs)
			msg.ToolCalls = tcs
		}
		history = append(history, msg)
	}

	resp, _, err := a.runAgenticLoopWithStream(ctx, history, stream)
	return resp, err
}

// HandleMessages processes a full conversation history through the agentic loop.
func (a *Agent) HandleMessages(ctx context.Context, messages []historyMessage) (string, error) {
	a.logger.Info("handling messages", "count", len(messages))

	// Convert to llmrouter.Message
	history := make([]llmrouter.Message, 0, len(messages))
	for _, m := range messages {
		msg := llmrouter.Message{
			Role: m.Role, Content: m.Content,
			ToolCallID: m.ToolCallID,
		}
		if m.ToolCalls != nil {
			data, _ := json.Marshal(m.ToolCalls)
			var tcs []any
			json.Unmarshal(data, &tcs)
			msg.ToolCalls = tcs
		}
		history = append(history, msg)
	}

	return a.RunLoopWithHistory(ctx, history)
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
	RequestID      string           `json:"request_id"`
	ConversationID string           `json:"conversation_id,omitempty"`
	UserID         string           `json:"user_id,omitempty"`
	Type           string           `json:"type"` // request, response, status, error, ping, pong, token, thinking, tool_call, tool_result
	Content        string           `json:"content,omitempty"`
	Messages       []historyMessage `json:"messages,omitempty"` // full conversation history
	ToolCalls      any              `json:"tool_calls,omitempty"`
	ToolCallID     string           `json:"tool_call_id,omitempty"`
}

// historyMessage is a single message in conversation history.
type historyMessage struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCalls  any    `json:"tool_calls,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// RunWebSocket connects to Hub Service via WebSocket and processes messages.
// Reconnects with exponential backoff on disconnect.
func (a *Agent) RunWebSocket(ctx context.Context, wsURL, instanceID string) error {
	// Connect if not already connected (idempotent — RunLocal may have called first)
	if a.hubClient == nil {
		if err := a.Connect(ctx); err != nil {
			return err
		}
	}

	endpoint := wsURL + "/agent/ws/" + instanceID
	a.logger.Info("agent WebSocket mode", "endpoint", endpoint)

	attempt := 0
	for {
		if ctx.Err() != nil {
			return nil
		}

		connected, err := a.wsSession(ctx, endpoint)
		if ctx.Err() != nil {
			return nil
		}
		// Reset backoff if we were connected (session ran, not just dial failure)
		if connected {
			attempt = 0
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

// wsSession runs a single WebSocket connection session. Returns true if connection was established.
func (a *Agent) wsSession(ctx context.Context, endpoint string) (bool, error) {
	headers := make(map[string][]string)
	token := a.CurrentToken()
	if token != "" {
		headers["Authorization"] = []string{"Bearer " + token}
	}

	conn, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{
		HTTPHeader: headers,
	})
	if err != nil {
		return false, fmt.Errorf("ws dial: %w", err)
	}
	defer conn.CloseNow()

	a.logger.Info("WebSocket connected", "endpoint", endpoint)

	// Serialize all writes — nhooyr.io/websocket does not allow concurrent writes
	var writeMu sync.Mutex
	writeMsg := func(msg agentMessage) {
		d, _ := json.Marshal(msg)
		writeMu.Lock()
		defer writeMu.Unlock()
		conn.Write(ctx, websocket.MessageText, d)
	}

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return true, fmt.Errorf("ws read: %w", err)
		}

		var msg agentMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			a.logger.Warn("invalid message from hub", "error", err)
			continue
		}

		switch msg.Type {
		case "ping":
			writeMsg(agentMessage{Type: "pong"})

		case "request":
			go func(req agentMessage) {
				writeMsg(agentMessage{RequestID: req.RequestID, Type: "status", Content: "processing"})

				// Stream callback for intermediate messages
				streamCb := func(msgType, content string, toolCalls any, toolCallID string) {
					writeMsg(agentMessage{
						RequestID:  req.RequestID,
						Type:       msgType,
						Content:    content,
						ToolCalls:  toolCalls,
						ToolCallID: toolCallID,
					})
				}

				var response string
				var err error
				if len(req.Messages) > 0 {
					response, err = a.HandleMessagesWithStream(ctx, req.Messages, streamCb)
				} else {
					response, err = a.HandleMessage(ctx, req.Content)
				}

				if err != nil {
					writeMsg(agentMessage{RequestID: req.RequestID, Type: "error", Content: err.Error()})
				} else {
					writeMsg(agentMessage{RequestID: req.RequestID, Type: "response", Content: response})
				}
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
