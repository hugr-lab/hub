package hubapp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/hugr-lab/airport-go/catalog"
	"github.com/hugr-lab/hub/console"
	"github.com/hugr-lab/hub/pkg/agentmgr"
	"github.com/hugr-lab/hub/pkg/auth"
	"github.com/hugr-lab/hugen/pkg/store/schema"
	"github.com/hugr-lab/query-engine/client"
	"github.com/hugr-lab/query-engine/client/app"
	"github.com/hugr-lab/query-engine/pkg/db"
)

const (
	appName    = "hub"
	appVersion = "0.3.6"
)

type HubApp struct {
	config        Config
	logger        *slog.Logger
	mux           *app.CatalogMux
	client        *client.Client
	server        *http.Server
	tokenServer   *http.Server // dedicated /agent/token listener (HUB_AGENT_TOKEN_LISTEN), nil in shared mode
	// agentRuntime is the AgentRuntime seam (DockerRuntime today; a k8s-Pod
	// runtime slots in behind it). nil when no orchestrator is reachable.
	agentRuntime agentmgr.AgentRuntime

	supervisor       *supervisor        // desired-state reconcile loop (spec §4); nil when Docker is absent
	supervisorCancel context.CancelFunc // stops the supervisor goroutine on Shutdown

	// Skills marketplace (SK1). bundleStore holds published bundle bytes
	// (nil disables the /skills surface); skillsVerify caches auth.me results
	// for /skills callers (both user and agent tokens).
	bundleStore  BundleStore
	skillsVerify *verifyCache

	// accessCheck / chatLookup override the gateway's authz + chat-read seams
	// in tests; nil → checkAgentAccess / fetchChat (gateway.go, gateway_chats.go).
	accessCheck func(ctx context.Context, u auth.UserInfo, agentID string) error
	chatLookup  func(ctx context.Context, id string) (chatRow, error)

	// consoleAuth memoises hugr's /auth/config for the console's runtime-config
	// endpoint (console.go).
	consoleAuth consoleAuthCache

	// oidcDisc memoises the provider's discovery document for the console OIDC
	// reverse-proxy (oidc_proxy.go).
	oidcDisc oidcDiscoveryCache
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
	if a.agentRuntime == nil {
		agentNetwork := envOrDefault("HUB_AGENT_NETWORK", "hub-dev-network")
		rt, err := agentmgr.NewDockerRuntime(a.agentRuntimeConfig(agentNetwork), a.logger)
		if err != nil {
			a.logger.Warn("Docker runtime unavailable", "error", err)
		} else {
			a.agentRuntime = rt
		}
	}
	if err := a.registerCatalog(); err != nil {
		return nil, fmt.Errorf("register catalog: %w", err)
	}
	return a.mux, nil
}

// agentRuntimeConfig assembles the container spawn config (spec-agent-
// orchestration §3) from hub config. HugrURL is the agent-network view of hugr;
// TokenURL is derived listener-aware. An empty HugrIssuer is not fatal here
// (Docker may be absent at Catalog time) — DockerRuntime.Start fails loudly at
// spawn time instead.
func (a *HubApp) agentRuntimeConfig(network string) agentmgr.RuntimeConfig {
	hugrURL := a.config.AgentHugrURL
	if hugrURL == "" {
		// Fall back to the hub's own hugr base (correct only when hub + agents
		// share a network view — a dev convenience, not a production default).
		hugrURL = strings.TrimSuffix(a.config.HugrURL, "/ipc")
	}
	if a.config.AgentHugrIssuer == "" {
		a.logger.Warn("HUB_AGENT_HUGR_ISSUER unset — agent spawns will fail (hugen serve requires HUGR_ISSUER)")
	}
	return agentmgr.RuntimeConfig{
		Network:            network,
		StoragePath:        a.config.StoragePath,
		HugrURL:            hugrURL,
		HugrIssuer:         a.config.AgentHugrIssuer,
		TokenURL:           a.agentTokenURL(),
		LogLevel:           os.Getenv("HUGEN_LOG_LEVEL"),
		PublishAPI:         a.config.AgentPublishAPI,
		DefaultMemoryBytes: a.config.AgentMemoryBytes,
		DefaultNanoCPUs:    a.config.AgentNanoCPUs,
		DefaultPidsLimit:   a.config.AgentPidsLimit,
	}
}

// agentTokenURL is the /agent/token URL a spawned container redeems its
// bootstrap secret at (→ HUGR_TOKEN_URL). Explicit HUB_AGENT_TOKEN_URL wins.
// Otherwise it derives from InternalURL, and — crucially — when a dedicated
// token listener is set (HUB_AGENT_TOKEN_LISTEN), /agent/token exists ONLY there
// (see the token-listener branch in Init), so the shared InternalURL port is
// wrong: swap in the dedicated listener's port on the InternalURL host. The
// InternalURL default (hub-service:8082) must itself be reachable from the agent
// network and its :8082 disagrees with ListenAddr :10000 outside dev compose —
// set HUB_SERVICE_INTERNAL_URL / HUB_AGENT_TOKEN_URL explicitly in prod.
func (a *HubApp) agentTokenURL() string {
	if a.config.AgentTokenURL != "" {
		return a.config.AgentTokenURL
	}
	base := strings.TrimRight(a.config.InternalURL, "/")
	if listen := a.config.AgentTokenListen; listen != "" {
		if _, port, err := net.SplitHostPort(listen); err == nil && port != "" {
			if u, err := url.Parse(base); err == nil && u.Host != "" {
				u.Host = net.JoinHostPort(u.Hostname(), port)
				base = strings.TrimRight(u.String(), "/")
				a.logger.Info("agent token URL derived from dedicated listener port",
					"token_url", base+"/agent/token", "listen", listen)
			}
		}
	}
	return base + "/agent/token"
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
		// embedder/vector-size, mirroring the SDL in DataSources. The default
		// agent type is seeded HERE (idempotent INSERT appended to the DDL) rather
		// than via a runtime GraphQL call — hugr executes this SQL straight against
		// Postgres, so unlike an Init-time a.client call it does not depend on
		// hugr's HTTP endpoint being up (see Init).
		ddl, err := schema.InitDDL(db.SDBPostgres, a.agentSchemaParams())
		if err != nil {
			return "", err
		}
		return ddl + "\n" + defaultAgentTypeSeedSQL, nil
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
		// Skills marketplace byte-store (SK1) — the v1 FS backend on the same
		// persistent volume. Creating it is FS-only (no hugr), so it is safe in
		// Init; the catalog seed that fills it runs async post-boot.
		if bs, err := NewFSBundleStore(a.config.StoragePath + "/system/skills"); err != nil {
			a.logger.Warn("skills byte-store disabled", "error", err)
		} else {
			a.bundleStore = bs
			a.skillsVerify = newVerifyCache(60 * time.Second)
		}
	}

	// NOTE: Init must NOT call back into hugr. It runs as hugr's _mount/init
	// while hugr is still loading its data sources — hugr's HTTP endpoint is not
	// listening yet, so any a.client call here fails "connection refused" and the
	// mount (hence every hub function) fails to load. Consequently:
	//   - default agent types are seeded at SCHEMA-APPLICATION time (an idempotent
	//     INSERT appended in InitDBSchemaTemplate for agent.db), not via GraphQL;
	//   - the agent isolation floor is applied ON DEMAND at create_agent /
	//     update_agent (createAgentRoleWithFloor). Roles persist in hugr's core DB
	//     across restarts, so there is nothing to seed on boot.

	// REST surface is intentionally minimal — everything CRUD went to Hugr GraphQL
	// (airport-go mutating + table functions in handlers_*.go). What remains is
	// protocol-level transport that GraphQL is not suited for:
	//   /health   liveness
	//   /hugr     Hugr GraphQL proxy
	mux := http.NewServeMux()
	mux.HandleFunc("/hugr", a.hugrProxyHandler())

	// HB5 gateway transport plane — raw pass-through to the agent's native
	// HTTP API + chat-scoped verbs (spec-hub-gateway §4). Registered
	// unconditionally; handlers answer 503 when no agent runtime is wired.
	mux.HandleFunc("/api/v1/agents/{id}/hugen/{path...}", a.agentProxyHandler)
	registerChatTransport(mux, a)

	// Skills marketplace (SK1) — exempt from the OIDC middleware (verifies
	// callers in-handler via hugr auth.me so agent tokens work too). The seed
	// that populates the catalog from the embedded hub bundle runs async once
	// hugr is up (never from Init).
	if a.bundleStore != nil {
		mux.HandleFunc("GET /skills/catalog", a.handleSkillsCatalog)
		mux.HandleFunc("GET /skills/{name}/bundle", a.handleSkillsBundle)
		mux.HandleFunc("POST /skills/publish", a.handleSkillsPublish)
		a.startSkillsSeed()
	}

	// Agent management — DockerRuntime is initialized in Catalog() (if Docker available).
	if a.agentRuntime != nil {
		a.agentRuntime.Reconstruct(ctx)
		// The spawn-secret minter wraps the agent-token issuer, wired below —
		// inject it now so Start can mint-at-spawn. Without the issuer, Start
		// fails loudly (an agent cannot boot without a redeemable secret).
		if a.config.AgentJWTKeyFile != "" {
			a.agentRuntime.SetSecretMinter(a.mintSpawnSecret)
		} else {
			a.logger.Warn("agent secret minter disabled (HUB_AGENT_JWT_KEY not set) — agents cannot be spawned")
		}
		// Desired-state supervisor (spec §4): reconcile managed containers to
		// agents.status on a tick + on start/stop_agent kicks. With Reconstruct()
		// above, active agents revive after a hub restart. Started after the minter
		// so its first pass can spawn.
		a.startSupervisor()
	}

	// Management console SPA (design 009) — the embedded Vite build served at
	// /console with SPA-fallback routing, plus a public runtime-config endpoint.
	// Static assets + config.json are exempt from the auth middleware (they are
	// public, read before login); the SPA authenticates its own /hugr + /api/v1
	// calls with the user's OIDC token.
	if a.config.ConsoleEnabled {
		if h, fromDisk, err := console.Handler("/console/", a.config.ConsoleDir); err != nil {
			a.logger.Warn("management console disabled", "error", err)
		} else {
			mux.HandleFunc("GET /console/config.json", a.handleConsoleConfig)
			mux.Handle("GET /console/", h)
			mux.HandleFunc("GET /console", func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/console/", http.StatusMovedPermanently)
			})
			// OIDC reverse-proxy: the CORS-sensitive PKCE legs the SPA makes as
			// XHRs, forwarded same-origin to the provider (oidc_proxy.go).
			mux.HandleFunc("POST /oidc/token", a.handleOIDCToken)
			mux.HandleFunc("GET /oidc/userinfo", a.handleOIDCUserinfo)
			mux.HandleFunc("GET /oidc/certs", a.handleOIDCJwks)
			source := "embedded"
			if fromDisk {
				source = a.config.ConsoleDir
			} else if a.config.ConsoleDir != "" {
				a.logger.Warn("console: HUB_CONSOLE_DIR set but has no index.html — using embedded build",
					"dir", a.config.ConsoleDir)
			}
			a.logger.Info("management console mounted", "path", "/console/", "source", source)
		}
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

	// Auth middleware — validates the user OIDC JWT or the management secret key.
	// Agents never present a token to this HTTP surface (they talk to hugr
	// directly; /agent/token is self-authenticating), so the legacy agent-token
	// validator is gone (O3). See the note in pkg/auth/middleware.go.
	hugrBase := strings.TrimSuffix(a.config.HugrURL, "/ipc")
	jwksProvider := auth.NewJWKSProvider(hugrBase)
	jwtValidator := auth.NewJWTValidator(jwksProvider)

	handler := auth.Middleware(mux, auth.AuthConfig{
		SecretKey:    a.config.HugrSecretKey,
		JWTValidator: jwtValidator,
		Logger:       a.logger,
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
	if a.supervisorCancel != nil {
		a.supervisorCancel()
	}
	if a.tokenServer != nil {
		if err := a.tokenServer.Shutdown(ctx); err != nil && err != http.ErrServerClosed {
			a.logger.Warn("agent token listener shutdown", "error", err)
		}
	}
	if a.server != nil {
		if err := a.server.Shutdown(ctx); err != nil {
			// Graceful drain timed out — open SSE streams never finish on
			// their own; force-close so shutdown does not hang on them.
			_ = a.server.Close()
			return err
		}
	}
	return nil
}
