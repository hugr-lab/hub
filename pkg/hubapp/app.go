package hubapp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hugr-lab/airport-go/catalog"
	"github.com/hugr-lab/hub/pkg/agentconn"
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
	appVersion = "0.3.0"
)

type HubApp struct {
	config        Config
	logger        *slog.Logger
	mux           *app.CatalogMux
	client        *client.Client
	server        *http.Server
	llmRouter     *llmrouter.Router
	dockerRuntime *agentmgr.DockerRuntime
	agentConnMgr  *agentconn.Manager
	agentMgr      *agentmgr.Manager
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
		Name:        appName,
		Description: "Analytics Hub — agent memory, query registry, workspace management",
		Version:     appVersion,
		URI:         fmt.Sprintf("grpc://localhost%s", a.config.FlightAddr),
	}
}

func (a *HubApp) Listner() (net.Listener, error) {
	return net.Listen("tcp", a.config.FlightAddr)
}

func (a *HubApp) Catalog(ctx context.Context) (catalog.Catalog, error) {
	// Initialize DockerRuntime early so catalog handlers can read state.
	// Reconstruct() is called later in Init() once the Docker daemon is reachable.
	if a.dockerRuntime == nil {
		agentNetwork := envOrDefault("HUB_AGENT_NETWORK", "hub-dev-network")
		rt, err := agentmgr.NewDockerRuntime(agentNetwork, a.config.StoragePath, a.config.InternalURL, a.logger)
		if err != nil {
			a.logger.Warn("Docker runtime unavailable", "error", err)
		} else {
			a.dockerRuntime = rt
		}
	}
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

// MigrateDBSchemaTemplate implements [app.ApplicationDBMigrator].
// Hugr calls this when the stored schema version differs from appVersion.
// fromVersion is the version currently in the database.
func (a *HubApp) MigrateDBSchemaTemplate(ctx context.Context, name, fromVersion string) (string, error) {
	if name != "db" {
		return "", fmt.Errorf("unknown data source: %s", name)
	}
	sql, ok := migrations[fromVersion]
	if !ok {
		return "", fmt.Errorf("no migration path from version %s to %s", fromVersion, appVersion)
	}
	return sql, nil
}

func (a *HubApp) Init(ctx context.Context) error {
	a.logger.Info("hub app initialized — DB provisioned, starting services")

	// Ensure conversation state directory exists on the persistent volume.
	// Agent processes and hub-service itself write per-turn checkpoints here.
	if a.config.StoragePath != "" {
		for _, dir := range []string{"/conversations", "/system/skills"} {
			p := a.config.StoragePath + dir
			if err := os.MkdirAll(p, 0o755); err != nil {
				a.logger.Warn("failed to create state dir", "path", p, "error", err)
			}
		}
	}

	// Seed default agent types
	a.seedAgentTypes(ctx)

	// LLM router — graceful when no models configured
	router := llmrouter.New(a.client, a.logger)
	// Intent routing from LLM_ROUTING_* env vars (Spec F / US4).
	if intentMap := LoadRoutingConfig(); intentMap != nil {
		def := intentMap["default"]
		delete(intentMap, "default")
		router.SetRoutingConfig(&llmrouter.RoutingConfig{
			Default:   def,
			IntentMap: intentMap,
			Fallback:  os.Getenv("LLM_ROUTING_FALLBACK"),
		})
		a.logger.Info("LLM intent routing configured", "default", def, "intents", len(intentMap))
	}
	a.llmRouter = router
	mcpSrv := mcpserver.New(a.client, router, a.logger, a.config.LogLevel == slog.LevelDebug)

	// Agent connection manager (WebSocket for agent containers)
	agentConnMgr := agentconn.NewManager(a.logger)
	a.agentConnMgr = agentConnMgr
	mcpSrv.SetAgentConn(agentConnMgr)

	// REST surface is intentionally minimal — everything CRUD went to Hugr GraphQL
	// (airport-go mutating + table functions in handlers_*.go). What remains is
	// protocol-level transport that GraphQL is not suited for:
	//   /health               liveness
	//   /mcp/                 MCP JSON-RPC tool protocol
	//   /v1/                  OpenAI-compatible chat completions for third-party clients
	//   /agent/ws/{id}        WebSocket uplink from agent containers
	//   /ws/{conversation_id} WebSocket stream to chat UI (registered below)
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpSrv.Handler())
	// Legacy /mcp/{user_id} redirect — 307 to /mcp for backward compat.
	mux.HandleFunc("/mcp/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/mcp", http.StatusTemporaryRedirect)
	})
	mux.Handle("/v1/", router.OpenAICompatHandler()) // OpenAI-compatible for third-party agents
	mux.Handle("/agent/ws/", agentConnMgr.Handler())
	go agentConnMgr.StartHeartbeat(ctx)

	// Agent management — DockerRuntime is initialized in Catalog() (if Docker available).
	if a.dockerRuntime != nil {
		a.dockerRuntime.Reconstruct(ctx)
		a.agentMgr = agentmgr.NewManager(a.dockerRuntime, a.client, a.logger)
	}

	// WebSocket gateway for chat UI — conversation-based routing
	ws := wsgateway.New(wsgateway.Config{
		AgentStream: func(ctx context.Context, agentID, conversationID, userID string, messages []wsgateway.LLMMessage, stream wsgateway.StreamCallback) (string, error) {
			agentMsgs := make([]agentconn.ChatMessage, len(messages))
			for i, m := range messages {
				agentMsgs[i] = agentconn.ChatMessage{
					Role: m.Role, Content: m.Content,
					ToolCalls: m.ToolCalls, ToolCallID: m.ToolCallID,
				}
			}
			return agentConnMgr.SendMessageStream(ctx, agentID, conversationID, userID, agentMsgs, func(msg agentconn.AgentMessage) {
				stream(wsgateway.ChatMessage{
					Type:       msg.Type,
					Content:    msg.Content,
					ToolCalls:  msg.ToolCalls,
					ToolCallID: msg.ToolCallID,
				})
			})
		},
		Lookup: func(ctx context.Context, conversationID string) (*wsgateway.ConversationInfo, error) {
			return a.lookupConversation(ctx, conversationID)
		},
		LLM: func(ctx context.Context, model string, messages []wsgateway.LLMMessage) (string, *wsgateway.UsageInfo, error) {
			msgs := make([]llmrouter.Message, len(messages))
			for i, m := range messages {
				msgs[i] = llmrouter.Message{Role: m.Role, Content: m.Content}
			}
			// Inject identity for Hugr calls
			if u, ok := auth.UserFromContext(ctx); ok {
				ctx = auth.InjectIdentity(ctx, u)
			}
			resp, err := router.Complete(ctx, llmrouter.CompletionRequest{Messages: msgs})
			if err != nil {
				return "", nil, err
			}
			usage := &wsgateway.UsageInfo{
				TokensIn:  resp.TokensIn,
				TokensOut: resp.TokensOut,
				Model:     resp.Model,
			}
			return resp.Content, usage, nil
		},
		Tools: func(ctx context.Context, userID, conversationID string, messages []wsgateway.LLMMessage, stream wsgateway.StreamCallback) (string, *wsgateway.UsageInfo, error) {
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
			// Inject identity for Hugr calls
			if u, ok := auth.UserFromContext(ctx); ok {
				ctx = auth.InjectIdentity(ctx, u)
			}
			text, chatUsage, err := mcpSrv.HandleUserMessage(ctx, userID, msgs, func(msgType, content string, toolCalls any, toolCallID string) {
				stream(wsgateway.ChatMessage{
					Type: msgType, Content: content,
					ToolCalls: toolCalls, ToolCallID: toolCallID,
				})
			}, conversationID)
			var usage *wsgateway.UsageInfo
			if chatUsage != nil {
				usage = &wsgateway.UsageInfo{
					TokensIn:  chatUsage.TokensIn,
					TokensOut: chatUsage.TokensOut,
					Model:     chatUsage.Model,
				}
			}
			return text, usage, err
		},
		Persist: func(ctx context.Context, conversationID, role, content string) {
			a.persistMessage(ctx, conversationID, role, content)
		},
		PersistFull: func(ctx context.Context, conversationID, role, content string, toolCalls any, toolCallID string, channel string, tokenCount int, modelUsed string) {
			a.persistMessageFull(ctx, conversationID, role, content, toolCalls, toolCallID, channel, tokenCount, modelUsed)
		},
		GenTitle: func(ctx context.Context, userMessage string) string {
			resp, err := router.Complete(ctx, llmrouter.CompletionRequest{
				Messages: []llmrouter.Message{
					{Role: "system", Content: "Generate a very short title (3-6 words, no quotes) for a chat that starts with this message. Reply with ONLY the title, nothing else."},
					{Role: "user", Content: userMessage},
				},
				Intent: "classification",
			})
			if err != nil {
				return ""
			}
			title := strings.TrimSpace(resp.Content)
			runes := []rune(title)
			if len(runes) > 60 {
				title = string(runes[:60])
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
	hugrBase := strings.TrimSuffix(a.config.HugrURL, "/ipc")
	jwksProvider := auth.NewJWKSProvider(hugrBase)
	jwtValidator := auth.NewJWTValidator(jwksProvider)

	// Agent token validator — uses AgentRuntime.ValidateToken (O(1) in-memory)
	var validateFunc func(string) (string, bool)
	if a.dockerRuntime != nil {
		validateFunc = a.dockerRuntime.ValidateToken
	} else {
		validateFunc = func(string) (string, bool) { return "", false }
	}
	agentValidator := auth.NewAgentTokenValidator(a.client, validateFunc)

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
		) { id user_id mode agent_type_id agent_id model } } } }`,
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
		ID          string `json:"id"`
		UserID      string `json:"user_id"`
		Mode        string `json:"mode"`
		AgentTypeID string `json:"agent_type_id"`
		AgentID     string `json:"agent_id"`
		Model       string `json:"model"`
	}
	if err := res.ScanData("hub.db.conversations", &convs); err != nil || len(convs) == 0 {
		return nil, fmt.Errorf("conversation %s not found", conversationID)
	}
	c := convs[0]
	info := &wsgateway.ConversationInfo{
		ID: c.ID, UserID: c.UserID, Mode: c.Mode,
		AgentInstanceID: c.AgentID, Model: c.Model,
	}

	// Fetch agent display name if agent mode
	if info.AgentInstanceID != "" {
		agentRes, err := a.client.Query(ctx,
			`query($id: String!) { hub { db { agents(
				filter: { id: { eq: $id } } limit: 1
			) { display_name agent_type_id } } } }`,
			map[string]any{"id": info.AgentInstanceID},
		)
		if err == nil {
			defer agentRes.Close()
			if agentRes.Err() == nil {
				var agents []struct {
					DisplayName string `json:"display_name"`
					AgentTypeID string `json:"agent_type_id"`
				}
				if err := agentRes.ScanData("hub.db.agents", &agents); err == nil && len(agents) > 0 {
					info.AgentName = agents[0].DisplayName
					if info.AgentName == "" {
						info.AgentName = agents[0].AgentTypeID
					}
				}
			}
		}
	}

	return info, nil
}

func toAnySlice(v any) []any {
	data, _ := json.Marshal(v)
	var result []any
	json.Unmarshal(data, &result)
	return result
}

func (a *HubApp) persistMessage(ctx context.Context, conversationID, role, content string) {
	a.persistMessageFull(ctx, conversationID, role, content, nil, "", "final", 0, "")
}

func (a *HubApp) persistMessageFull(ctx context.Context, conversationID, role, content string, toolCalls any, toolCallID string, channel string, tokenCount int, modelUsed string) {
	msgID := fmt.Sprintf("msg-%d", time.Now().UnixNano())
	vars := map[string]any{"id": msgID, "cid": conversationID, "role": role, "content": content}
	dataFields := `id: $id, conversation_id: $cid, role: $role, content: $content`
	varDefs := `$id: String!, $cid: String!, $role: String!, $content: String!`

	if toolCallID != "" {
		varDefs += `, $tcid: String`
		dataFields += `, tool_call_id: $tcid`
		vars["tcid"] = toolCallID
	}
	if toolCalls != nil {
		tcJSON, _ := json.Marshal(toolCalls)
		varDefs += `, $tc: JSON`
		dataFields += `, tool_calls: $tc`
		vars["tc"] = json.RawMessage(tcJSON)
	}

	// Channel protocol metadata
	if channel != "" {
		varDefs += `, $ch: String`
		dataFields += `, channel: $ch`
		vars["ch"] = channel
	}
	if tokenCount > 0 {
		varDefs += `, $tc_count: Int`
		dataFields += `, token_count: $tc_count`
		vars["tc_count"] = tokenCount
	}
	if modelUsed != "" {
		varDefs += `, $mu: String`
		dataFields += `, model_used: $mu`
		vars["mu"] = modelUsed
	}

	gql := fmt.Sprintf(`mutation(%s) {
		hub { db { insert_agent_messages(data: { %s }) { id } } }
	}`, varDefs, dataFields)

	res, err := a.client.Query(ctx, gql, vars)
	if err != nil {
		a.logger.Warn("failed to persist message", "conversation", conversationID, "role", role, "error", err)
		return
	}
	defer res.Close()
	if res.Err() != nil {
		a.logger.Warn("persist message query error", "conversation", conversationID, "role", role, "error", res.Err())
	}
}

func (a *HubApp) Shutdown(ctx context.Context) error {
	a.logger.Info("hub app shutting down")
	if a.server != nil {
		return a.server.Shutdown(ctx)
	}
	return nil
}
