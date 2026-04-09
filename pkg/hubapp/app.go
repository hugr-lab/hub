package hubapp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/hugr-lab/airport-go/catalog"
	"github.com/hugr-lab/hub/pkg/agentmgr"
	"github.com/hugr-lab/hub/pkg/auth"
	"github.com/hugr-lab/hub/pkg/llmrouter"
	"github.com/hugr-lab/hub/pkg/mcpserver"
	"github.com/hugr-lab/hub/pkg/wsgateway"
	"github.com/hugr-lab/query-engine/client"
	"github.com/hugr-lab/query-engine/client/app"
)

const (
	appName    = "hub"
	appVersion = "0.1.0"
)

type HubApp struct {
	config Config
	logger *slog.Logger
	mux    *app.CatalogMux
	client *client.Client
	server *http.Server
}

func New(cfg Config, logger *slog.Logger, c *client.Client) *HubApp {
	return &HubApp{
		config: cfg,
		logger: logger,
		mux:    app.New(),
		client: c,
	}
}

func (a *HubApp) Info() app.AppInfo {
	return app.AppInfo{
		Name:          appName,
		Description:   "Analytics Hub — agent memory, query registry, workspace management",
		Version:       appVersion,
		URI:           fmt.Sprintf("grpc://localhost%s", a.config.FlightAddr),
		DefaultSchema: "default",
	}
}

func (a *HubApp) Listner() (net.Listener, error) {
	return net.Listen("tcp", a.config.FlightAddr)
}

func (a *HubApp) Catalog(ctx context.Context) (catalog.Catalog, error) {
	if err := a.registerCatalog(); err != nil {
		return nil, fmt.Errorf("register catalog: %w", err)
	}
	return a.mux, nil
}

func (a *HubApp) DataSources(ctx context.Context) ([]app.DataSourceInfo, error) {
	return []app.DataSourceInfo{
		{
			Name:        "db",
			Type:        "postgres",
			Description: "Hub metadata database (agents, memory, budgets, usage)",
			Path:        a.config.DatabaseDSN,
			ReadOnly:    false,
			Version:     appVersion,
			HugrSchema:  hubGraphQLSchema,
		},
		{
			Name:        "redis",
			Type:        "redis",
			Description: "Hub rate limiting and cache store",
			Path:        a.config.RedisURL,
			ReadOnly:    false,
			HugrSchema:  " ", // non-empty to prevent self_defined=true (Redis has no schema)
		},
	}, nil
}

func (a *HubApp) InitDBSchemaTemplate(ctx context.Context, name string) (string, error) {
	if name == "db" {
		return hubDBSchema, nil
	}
	return "", fmt.Errorf("unknown data source: %s", name)
}

func (a *HubApp) Init(ctx context.Context) error {
	a.logger.Info("hub app initialized — DB provisioned, starting services")

	// Seed default agent types
	a.seedAgentTypes(ctx)

	// Start HTTP server
	router := llmrouter.New(a.client, a.logger)
	mcpSrv := mcpserver.New(a.client, router, a.logger, a.config.LogLevel == slog.LevelDebug)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user/login", a.handleUserLogin)
	mux.HandleFunc("/api/conversations/create", a.handleConversationCreate)
	mux.HandleFunc("/api/conversations/list", a.handleConversationList)
	mux.HandleFunc("/api/conversations/messages", a.handleConversationMessages)
	mux.HandleFunc("/api/conversations/delete", a.handleConversationDelete)
	mux.HandleFunc("/api/conversations/rename", a.handleConversationRename)
	mux.Handle("/mcp/", mcpSrv.Handler())
	mux.Handle("/v1/", router.OpenAICompatHandler()) // OpenAI-compatible for third-party agents

	// Agent management (Docker backend for now)
	agentNetwork := envOrDefault("HUB_AGENT_NETWORK", "hub-dev-network")
	dockerBackend, err := agentmgr.NewDockerBackend(agentNetwork)
	if err != nil {
		a.logger.Warn("Docker backend unavailable, agent management disabled", "error", err)
	} else {
		mgr := agentmgr.NewManager(dockerBackend, a.client, a.config.InternalURL, a.logger)
		mux.HandleFunc("/api/agent/start", a.handleAgentStart(mgr))
		mux.HandleFunc("/api/agent/stop", a.handleAgentStop(mgr))
		mux.HandleFunc("/api/agent/status", a.handleAgentStatus(mgr))
	}
	// WebSocket gateway for chat UI — conversation-based routing
	ws := wsgateway.New(wsgateway.Config{
		Lookup: func(ctx context.Context, conversationID string) (*wsgateway.ConversationInfo, error) {
			return a.lookupConversation(ctx, conversationID)
		},
		LLM: func(ctx context.Context, model string, messages []wsgateway.LLMMessage) (string, error) {
			// Convert to llmrouter.Message
			msgs := make([]llmrouter.Message, len(messages))
			for i, m := range messages {
				msgs[i] = llmrouter.Message{Role: m.Role, Content: m.Content}
			}
			resp, err := router.Complete(ctx, llmrouter.CompletionRequest{Messages: msgs})
			if err != nil {
				return "", err
			}
			return resp.Content, nil
		},
		Tools: func(ctx context.Context, userID string, messages []wsgateway.LLMMessage, stream wsgateway.StreamCallback) (string, error) {
			// Convert to llmrouter.Message
			msgs := make([]llmrouter.Message, len(messages))
			for i, m := range messages {
				msgs[i] = llmrouter.Message{
					Role: m.Role, Content: m.Content,
					ToolCallID: m.ToolCallID,
				}
				if m.ToolCalls != nil {
					msgs[i].ToolCalls = toAnySlice(m.ToolCalls)
				}
			}
			return mcpSrv.HandleUserMessage(ctx, userID, msgs, func(msgType, content string, toolCalls any, toolCallID string) {
				stream(wsgateway.ChatMessage{
					Type: msgType, Content: content,
					ToolCalls: toolCalls, ToolCallID: toolCallID,
				})
			})
		},
		Persist: func(ctx context.Context, conversationID, role, content string) {
			a.persistMessage(ctx, conversationID, role, content)
		},
		GenTitle: func(ctx context.Context, userMessage string) string {
			resp, err := router.Complete(ctx, llmrouter.CompletionRequest{
				Messages: []llmrouter.Message{
					{Role: "system", Content: "Generate a very short title (3-6 words, no quotes) for a chat that starts with this message. Reply with ONLY the title, nothing else."},
					{Role: "user", Content: userMessage},
				},
			})
			if err != nil {
				return ""
			}
			title := strings.TrimSpace(resp.Content)
			if len(title) > 60 {
				title = title[:60]
			}
			return title
		},
		SetTitle: func(ctx context.Context, conversationID, title string) {
			res, err := a.client.Query(ctx,
				`mutation($id: String!, $title: String!) { hub { db { update_conversations(
					filter: { id: { eq: $id } }, data: { title: $title }
				) { affected_rows } } } }`,
				map[string]any{"id": conversationID, "title": title},
			)
			if err != nil {
				a.logger.Warn("failed to update title", "conversation", conversationID, "error", err)
				return
			}
			defer res.Close()
			if res.Err() != nil {
				a.logger.Warn("update title query error", "conversation", conversationID, "error", res.Err())
			}
		},
		Logger: a.logger,
	})
	mux.Handle("/ws/", ws.Handler())

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	// Auth middleware — validates JWT, agent tokens, or secret key
	// HugrURL may have /ipc suffix — strip for OIDC discovery
	hugrBase := strings.TrimSuffix(a.config.HugrURL, "/ipc")
	jwksProvider := auth.NewJWKSProvider(hugrBase)
	jwtValidator := auth.NewJWTValidator(jwksProvider)
	agentValidator := auth.NewAgentTokenValidator(a.client)
	handler := auth.Middleware(mux, auth.AuthConfig{
		SecretKey:      a.config.HugrSecretKey,
		JWTValidator:   jwtValidator,
		AgentValidator: agentValidator,
		Logger:         a.logger,
	})

	a.server = &http.Server{Addr: a.config.ListenAddr, Handler: handler}
	go func() {
		a.logger.Info("HTTP server starting", "addr", a.config.ListenAddr)
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			a.logger.Error("HTTP server error", "error", err)
		}
	}()

	return nil
}

func (a *HubApp) lookupConversation(ctx context.Context, conversationID string) (*wsgateway.ConversationInfo, error) {
	res, err := a.client.Query(ctx,
		`query($id: String!) { hub { db { conversations(
			filter: { id: { eq: $id } }
			limit: 1
		) { id user_id mode agent_instance_id model } } } }`,
		map[string]any{"id": conversationID},
	)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	if res.Err() != nil {
		return nil, res.Err()
	}
	var convs []struct {
		ID              string `json:"id"`
		UserID          string `json:"user_id"`
		Mode            string `json:"mode"`
		AgentInstanceID string `json:"agent_instance_id"`
		Model           string `json:"model"`
	}
	if err := res.ScanData("hub.db.conversations", &convs); err != nil || len(convs) == 0 {
		return nil, fmt.Errorf("conversation %s not found", conversationID)
	}
	c := convs[0]
	return &wsgateway.ConversationInfo{
		ID: c.ID, UserID: c.UserID, Mode: c.Mode,
		AgentInstanceID: c.AgentInstanceID, Model: c.Model,
	}, nil
}

func toAnySlice(v any) []any {
	data, _ := json.Marshal(v)
	var result []any
	json.Unmarshal(data, &result)
	return result
}

func (a *HubApp) persistMessage(ctx context.Context, conversationID, role, content string) {
	// Use background context — persist must not be cancelled when WebSocket closes
	bgCtx := context.Background()
	msgID := fmt.Sprintf("msg-%d", time.Now().UnixNano())
	res, err := a.client.Query(bgCtx,
		`mutation($id: String!, $cid: String!, $role: String!, $content: String!) {
			hub { db { insert_agent_messages(data: {
				id: $id, conversation_id: $cid, role: $role, content: $content
			}) { id } } }
		}`,
		map[string]any{"id": msgID, "cid": conversationID, "role": role, "content": content},
	)
	if err != nil {
		a.logger.Warn("failed to persist message", "conversation", conversationID, "error", err)
		return
	}
	defer res.Close()
	if res.Err() != nil {
		a.logger.Warn("persist message query error", "conversation", conversationID, "error", res.Err())
	}
}

func (a *HubApp) Shutdown(ctx context.Context) error {
	a.logger.Info("hub app shutting down")
	if a.server != nil {
		return a.server.Shutdown(ctx)
	}
	return nil
}
