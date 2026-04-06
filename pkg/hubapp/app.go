package hubapp

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"net/http"

	"github.com/hugr-lab/airport-go/catalog"
	"github.com/hugr-lab/hub/pkg/agentmgr"
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
	sources := []app.DataSourceInfo{
		{
			Name:        "db",
			Type:        "postgres",
			Description: "Hub Service database (agent metadata, memory, LLM usage)",
			Path:        a.config.DatabaseDSN,
			ReadOnly:    false,
			Version:     appVersion,
			HugrSchema:  hubGraphQLSchema,
		},
	}

	for _, p := range a.config.LLMProviders {
		sources = append(sources, app.DataSourceInfo{
			Name:        p.Name,
			Type:        p.Type,
			Description: p.Description(),
			Path:        p.BuildPath(),
			ReadOnly:    true,
		})
	}

	if a.config.RedisURL != "" {
		sources = append(sources, app.DataSourceInfo{
			Name:        "redis",
			Type:        "redis",
			Description: "Hub cache and rate limit store",
			Path:        a.config.RedisURL,
			ReadOnly:    false,
		})
	}

	return sources, nil
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
	mux.Handle("/mcp/", mcpSrv.Handler())
	mux.Handle("/v1/", router.OpenAICompatHandler()) // OpenAI-compatible for third-party agents

	// Agent management (Docker backend for now)
	dockerBackend, err := agentmgr.NewDockerBackend("hub-network")
	if err != nil {
		a.logger.Warn("Docker backend unavailable, agent management disabled", "error", err)
	} else {
		mgr := agentmgr.NewManager(dockerBackend, a.client, "http://localhost"+a.config.ListenAddr, a.logger)
		mux.HandleFunc("/api/agent/start", a.handleAgentStart(mgr))
		mux.HandleFunc("/api/agent/stop", a.handleAgentStop(mgr))
		mux.HandleFunc("/api/agent/status", a.handleAgentStatus(mgr))
	}
	// WebSocket gateway for chat UI
	ws := wsgateway.New(func(ctx context.Context, userID, message string) (string, error) {
		// Route through MCP — same path as agent
		return mcpSrv.HandleUserMessage(ctx, userID, message)
	}, a.logger)
	mux.Handle("/ws/", ws.Handler())

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	a.server = &http.Server{Addr: a.config.ListenAddr, Handler: mux}
	go func() {
		a.logger.Info("HTTP server starting", "addr", a.config.ListenAddr)
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			a.logger.Error("HTTP server error", "error", err)
		}
	}()

	return nil
}

func (a *HubApp) Shutdown(ctx context.Context) error {
	a.logger.Info("hub app shutting down")
	if a.server != nil {
		return a.server.Shutdown(ctx)
	}
	return nil
}
