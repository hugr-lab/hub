package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// Agent is the main agent runtime. Connects to Hub Service MCP,
// receives messages, calls LLM, executes tools, returns results.
type Agent struct {
	mcpURL    string
	mcpClient *mcpclient.Client
	skills    *SkillsEngine
	sandbox   *Sandbox
	learner   *Learner
	logger    *slog.Logger
}

func New(mcpURL string, skillsDir string, logger *slog.Logger) *Agent {
	a := &Agent{
		mcpURL:  mcpURL,
		skills:  NewSkillsEngine(skillsDir, logger),
		sandbox: NewSandbox(logger),
		logger:  logger,
	}
	a.learner = NewLearner(a)
	return a
}

// Connect establishes MCP connection to Hub Service.
func (a *Agent) Connect(ctx context.Context) error {
	client, err := mcpclient.NewStreamableHttpClient(a.mcpURL)
	if err != nil {
		return fmt.Errorf("create MCP client: %w", err)
	}
	a.mcpClient = client

	if err := a.mcpClient.Start(ctx); err != nil {
		return fmt.Errorf("start MCP client: %w", err)
	}

	// Verify connection by listing tools
	initReq := mcp.InitializeRequest{}
	initReq.Params.ClientInfo = mcp.Implementation{Name: "hub-agent", Version: "0.1.0"}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION

	_, err = a.mcpClient.Initialize(ctx, initReq)
	if err != nil {
		return fmt.Errorf("MCP initialize: %w", err)
	}

	tools, err := a.mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return fmt.Errorf("list tools: %w", err)
	}

	a.logger.Info("connected to Hub Service MCP",
		"url", a.mcpURL,
		"tools", len(tools.Tools),
	)

	return nil
}

// CallTool calls a tool on Hub Service MCP.
func (a *Agent) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args

	result, err := a.mcpClient.CallTool(ctx, req)
	if err != nil {
		return "", fmt.Errorf("call tool %s: %w", name, err)
	}

	if result.IsError {
		for _, c := range result.Content {
			if tc, ok := c.(mcp.TextContent); ok {
				return "", fmt.Errorf("tool error: %s", tc.Text)
			}
		}
		return "", fmt.Errorf("tool %s returned error", name)
	}

	var text string
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			text += tc.Text
		}
	}
	return text, nil
}

// HandleMessage processes a user message: load memories, call LLM, execute tools.
// MVP: simple single-turn — no multi-step reasoning.
func (a *Agent) HandleMessage(ctx context.Context, userMessage string) (string, error) {
	a.logger.Info("handling message", "length", len(userMessage))

	// 1. Load relevant context (memories + registry) via learner
	learnedContext := a.learner.RetrieveContext(ctx, userMessage)

	// 2. Build system prompt with skills + learned context
	systemPrompt := a.skills.SystemPrompt()
	if learnedContext != "" {
		systemPrompt += "\n\n" + learnedContext
	}

	// 3. Call LLM via Hub Service
	// TODO: implement multi-step reasoning with tool calls
	response, err := a.CallTool(ctx, "llm-complete", map[string]any{
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userMessage},
		},
	})
	if err != nil {
		// Fallback: return context info if LLM not available
		response = fmt.Sprintf("Agent received: %q\nSystem prompt length: %d\nContext: %s",
			userMessage, len(systemPrompt), learnedContext)
	}

	// 4. Store user pattern in memory
	_, _ = a.CallTool(ctx, "memory-store", map[string]any{
		"content":  fmt.Sprintf("User asked: %s", userMessage),
		"category": "user_pattern",
	})

	return response, nil
}

// Run starts the agent in stdin/stdout mode for testing.
func (a *Agent) Run(ctx context.Context) error {
	if err := a.Connect(ctx); err != nil {
		return err
	}

	// Load skills
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

// Close disconnects from Hub Service.
func (a *Agent) Close() error {
	if a.mcpClient != nil {
		return a.mcpClient.Close()
	}
	return nil
}
