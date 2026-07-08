package hubapp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/hugr-lab/airport-go/catalog"
	"github.com/hugr-lab/hub/pkg/agentmgr"
	"github.com/hugr-lab/hub/pkg/auth"
	"github.com/hugr-lab/hugen/pkg/store/schema"
	"github.com/hugr-lab/query-engine/client"
	"github.com/hugr-lab/query-engine/client/app"
	"github.com/hugr-lab/query-engine/pkg/db"
)

const (
	appName    = "hub"
	appVersion = "0.3.2"
)

type HubApp struct {
	config        Config
	logger        *slog.Logger
	mux           *app.CatalogMux
	client        *client.Client
	server        *http.Server
	tokenServer   *http.Server // dedicated /agent/token listener (HUB_AGENT_TOKEN_LISTEN), nil in shared mode
	dockerRuntime *agentmgr.DockerRuntime
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

// agentSchemaParams are the template vars the hub renders the agent-store
// schema with — its OWN embedder (hub-configured), NOT query-engine's global
// _system_embedder. The hub renders the SDL/DDL itself (below) instead of
// handing raw templates to the provisioner, so it controls which embedder the
// @embeddings directives reference.
func (a *HubApp) agentSchemaParams() schema.Params {
	return schema.Params{
		VectorSize:   a.config.AgentVectorSize,
		EmbedderName: a.config.AgentEmbedder,
	}
}

func (a *HubApp) DataSources(ctx context.Context) ([]app.DataSourceInfo, error) {
	// The hub "db" and "agent.db" sources are distinct Hugr apps but share the
	// per-physical-DB _hugr_app_meta version row, so they MUST live in separate
	// physical databases — otherwise their schema-version rows collide and
	// Init/Migrate dispatch corrupts. Fail fast on a misconfiguration.
	if a.config.AgentDatabaseDSN == a.config.DatabaseDSN {
		return nil, fmt.Errorf("HUB_AGENT_DATABASE_DSN must be a separate physical database from HUB_DATABASE_DSN (both are %q); they collide on the _hugr_app_meta version row", a.config.DatabaseDSN)
	}
	// Render the agent-store SDL here (native Postgres) with the hub's embedder
	// so @embeddings(model: …) points at HUB_AGENT_EMBEDDER, not _system_embedder.
	agentSDL, err := schema.SDL(db.SDBPostgres, a.agentSchemaParams())
	if err != nil {
		return nil, fmt.Errorf("render agent store SDL: %w", err)
	}
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
			// Agent runtime store — hugen owns the schema + its version stream.
			// Path is a SEPARATE physical DB (see Config.AgentDatabaseDSN). The
			// hub renders the hugen SDL/DDL with ITS embedder (agentSchemaParams)
			// via the shared common template convention. Name "agent.db" →
			// GraphQL path hub.agent.db, prefix hub_agent_db — nests under a FRESH
			// hub.agent module, NOT under hub.db (nesting a source under an
			// EXISTING source module, hub.db.agent under hub.db, breaks the hub.db
			// module merge in hugr).
			Name:        "agent.db",
			Type:        "postgres",
			Description: "Agent runtime store (hugen schema): sessions, events, notes, skills, tasks, tool policies",
			Path:        a.config.AgentDatabaseDSN,
			ReadOnly:    false,
			Version:     schema.Version,
			HugrSchema:  agentSDL,
		},
		{
			Name:        "redis",
			Type:        "redis",
			Description: "Hub rate limiting and cache store",
			Path:        a.config.RedisURL,
			ReadOnly:    false,
			HugrSchema:  " ", // non-empty to prevent self_defined=true (Redis has no schema)
		},
		{
			// HB-EXT cross-source relation graph. An `extension` source is
			// DuckDB-backed with no connection string (Path ""); it declares no
			// tables of its own, only cross-source @join fields linking the
			// platform DB (hub.db) and the Agent DB (hub.agent.db). MUST come
			// AFTER both dependency sources so their types exist at load time
			// (cross-catalog extensions also re-apply when a dependency reloads).
			// Not Postgres → skips DB provisioning; HugrSchema is registered as a
			// text catalog by the app framework.
			Name:        "graph",
			Type:        "extension",
			Description: "Cross-source relation graph: chat/grant → agent identity + live session",
			Path:        "",
			// An extension is backed by an in-memory DuckDB catalog; it must be
			// writable — DuckDB refuses to launch an in-memory DB in read-only
			// mode ("Cannot launch in-memory database in read-only mode"). It
			// defines no writable tables of its own, so this is not a data-write
			// surface.
			ReadOnly:   false,
			Version:    appVersion,
			HugrSchema: hubGraphExtSchema,
		},
	}, nil
}

func (a *HubApp) InitDBSchemaTemplate(ctx context.Context, name string) (string, error) {
	switch name {
	case "db":
		return hubDBSchema, nil
	case "agent.db":
		// hugen physical DDL rendered here (native Postgres) with the hub's
		// embedder/vector-size, mirroring the SDL in DataSources.
		return schema.InitDDL(db.SDBPostgres, a.agentSchemaParams())
	}
	return "", fmt.Errorf("unknown data source: %s", name)
}

// MigrateDBSchemaTemplate implements [app.ApplicationDBMigrator].
// Hugr calls this when the stored schema version differs from appVersion.
// fromVersion is the version currently in the database.
func (a *HubApp) MigrateDBSchemaTemplate(ctx context.Context, name, fromVersion string) (string, error) {
	switch name {
	case "db":
		sql, ok, err := migrationSQL(fromVersion)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("no migration path from version %s to %s", fromVersion, appVersion)
		}
		return sql, nil
	case "agent.db":
		// hugen owns the agent-store migration stream; rendered here (Postgres).
		return schema.MigrateDDL(db.SDBPostgres, fromVersion, a.agentSchemaParams())
	}
	return "", fmt.Errorf("unknown data source: %s", name)
}

func (a *HubApp) Init(ctx context.Context) error {
	// Two independent schema streams, two separate physical DBs (D11): the
	// platform DB tracks appVersion, the agent store tracks hugen's schema.Version.
	a.logger.Info("hub app initialized — DB provisioned, starting services",
		"platform_schema", appVersion, "agent_schema", schema.Version)

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

	// RLS floor for agent roles (data-object permissions) — fail-closed: without
	// it a second agent reads the first one's store (Hugr is allow-by-default).
	if err := a.seedAgentRoles(ctx); err != nil {
		return fmt.Errorf("agent role RLS seed: %w", err)
	}

	// REST surface is intentionally minimal — everything CRUD went to Hugr GraphQL
	// (airport-go mutating + table functions in handlers_*.go). What remains is
	// protocol-level transport that GraphQL is not suited for:
	//   /health   liveness
	//   /hugr     Hugr GraphQL proxy
	mux := http.NewServeMux()
	mux.HandleFunc("/hugr", a.hugrProxyHandler())

	// Agent management — DockerRuntime is initialized in Catalog() (if Docker available).
	if a.dockerRuntime != nil {
		a.dockerRuntime.Reconstruct(ctx)
		a.agentMgr = agentmgr.NewManager(a.dockerRuntime, a.client, a.logger)
	}

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	// Agent token authority (spec-hub-side §1, HB1). Enabled by
	// HUB_AGENT_JWT_KEY; HUB_AGENT_TOKEN_LISTEN picks a dedicated internal
	// listener, empty mounts on the shared one (paths are exempt from the
	// auth middleware — the body token IS the auth).
	if a.config.AgentJWTKeyFile != "" {
		issuer, err := newAgentTokenIssuer(a.config, a, a.logger)
		if err != nil {
			return err
		}
		if listen := a.config.AgentTokenListen; listen != "" {
			tokenMux := http.NewServeMux()
			issuer.mount(tokenMux)
			a.tokenServer = &http.Server{Addr: listen, Handler: tokenMux}
			go func() {
				a.logger.Info("agent token listener starting", "addr", listen, "issuer", a.config.AgentJWTIssuer)
				if err := a.tokenServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					a.logger.Error("agent token listener error", "error", err)
				}
			}()
		} else {
			issuer.mount(mux)
			a.logger.Info("agent token endpoint mounted on shared listener",
				"addr", a.config.ListenAddr, "issuer", a.config.AgentJWTIssuer)
		}
	} else {
		a.logger.Warn("agent token issuer disabled (HUB_AGENT_JWT_KEY not set) — agents cannot refresh tokens")
	}

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

func (a *HubApp) Shutdown(ctx context.Context) error {
	a.logger.Info("hub app shutting down")
	if a.tokenServer != nil {
		if err := a.tokenServer.Shutdown(ctx); err != nil && err != http.ErrServerClosed {
			a.logger.Warn("agent token listener shutdown", "error", err)
		}
	}
	if a.server != nil {
		return a.server.Shutdown(ctx)
	}
	return nil
}
