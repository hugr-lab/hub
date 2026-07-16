package hubapp

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

type Config struct {
	HugrURL       string
	HugrSecretKey string
	ListenAddr    string // HTTP server (MCP, WebSocket, API)
	FlightAddr    string // gRPC Flight server
	InternalURL   string // Hub Service URL accessible from agent containers
	DatabaseDSN   string // PostgreSQL DSN for hub DB
	// AgentDatabaseDSN is the PostgreSQL DSN for the hugen Agent store
	// (hub.agent.db). MUST be a physically SEPARATE database from DatabaseDSN:
	// the Hugr app framework tracks per-app schema version in _hugr_app_meta
	// keyed by app name, so two sources of the same app sharing one physical DB
	// would collide on the version row. The empty database must be created
	// out-of-band (the provisioner needs direct non-SSL Postgres access and does
	// not CREATE DATABASE for app sources).
	AgentDatabaseDSN string
	// AgentEmbedder is the embedding data source (registered in hugr) the agent
	// store's @embeddings directives reference. The hub renders the SDL/DDL with
	// THIS embedder (its own, hub-configured) rather than letting the provisioner
	// inject query-engine's global _system_embedder. AgentVectorSize is that
	// embedder's dimension; 0 disables embeddings.
	AgentEmbedder   string
	AgentVectorSize int
	// Agent token authority (spec-hub-side §1, Mode B) — hub-service issues
	// the agent JWTs hugr verifies against this issuer's public key.
	// AgentJWTKeyFile is the PEM private key path (RSA / EC / Ed25519, PKCS8
	// or OpenSSH); empty disables the issuer and the /agent/token endpoint.
	AgentJWTKeyFile   string        // HUB_AGENT_JWT_KEY
	AgentJWTIssuer    string        // HUB_AGENT_JWT_ISSUER — `iss` claim; must match the hugr auth-config jwt entry
	AgentTokenTTL     time.Duration // HUB_AGENT_TOKEN_TTL — issued JWT lifetime (= revocation latency ceiling)
	AgentBootstrapTTL time.Duration // HUB_AGENT_BOOTSTRAP_TTL — mint-to-redeem deadline for spawn secrets
	// AgentTokenListen, when set, serves /agent/token on a dedicated internal
	// listener (container network only); empty mounts it on the shared listener.
	AgentTokenListen string // HUB_AGENT_TOKEN_LISTEN

	// Agent spawn contract (spec-agent-orchestration §3) — the remote-mode env
	// the DockerRuntime bakes into every agent container.
	//
	// AgentHugrURL is hugr's BASE url AS SEEN FROM THE AGENT NETWORK (NO /ipc —
	// hugen appends it). It differs from HugrURL (hub's own view): on macOS dev
	// the agent reaches the host via host.docker.internal. Empty falls back to
	// HugrURL with /ipc trimmed (correct only when hub and agents share a view).
	AgentHugrURL string // HUB_AGENT_HUGR_URL
	// AgentHugrIssuer is the user-token issuer hugen's API verifies against
	// (same issuer hugr trusts). Boot-fatal in hugen if empty — Start refuses to
	// spawn without it.
	AgentHugrIssuer string // HUB_AGENT_HUGR_ISSUER
	// AgentTokenURL is the /agent/token URL the container redeems its bootstrap
	// secret at (→ HUGR_TOKEN_URL). Empty derives it listener-aware from
	// InternalURL / AgentTokenListen (see HubApp.agentTokenURL).
	AgentTokenURL string // HUB_AGENT_TOKEN_URL
	// AgentPublishAPI publishes each agent's API port on an ephemeral host port
	// (dev only; prod-forbidden — agents are reached container-network-only).
	AgentPublishAPI bool // HUB_AGENT_PUBLISH_API
	// Per-agent resource caps applied when agent_type.config.orchestration omits
	// them (0 = unlimited).
	AgentMemoryBytes int64 // HUB_AGENT_MEMORY_BYTES
	AgentNanoCPUs    int64 // HUB_AGENT_NANO_CPUS
	AgentPidsLimit   int64 // HUB_AGENT_PIDS_LIMIT

	RedisURL         string        // Redis URL for per-user rate limiting (required)
	StoragePath      string        // Root directory for persistent storage (HUB_STORAGE_PATH)
	QueryTimeout     time.Duration // Timeout for Hugr GraphQL queries (HUGR_QUERY_TIMEOUT)
	SubscriptionPool int           // Max WebSocket connections for subscriptions (HUB_SUBSCRIPTION_POOL_MAX)
	LogLevel         slog.Level

	// Management console (design 009) — the embedded admin/chat SPA served at
	// /console. The BROWSER-side OIDC issuer + public client id are discovered
	// from hugr's public /auth/config (hugr returns the browser-reachable issuer),
	// so nothing is pinned per deployment. The two override knobs stay empty by
	// default and only exist as an escape hatch.
	ConsoleEnabled      bool   // HUB_CONSOLE_ENABLED — serve /console (default true)
	ConsoleOIDCIssuer   string // HUB_CONSOLE_OIDC_ISSUER — override; empty = discover from hugr /auth/config
	ConsoleOIDCClientID string // HUB_CONSOLE_OIDC_CLIENT_ID — override; empty = discover from hugr /auth/config
	ConsoleOIDCScopes   string // HUB_CONSOLE_OIDC_SCOPES
	ConsoleAPIBase      string // HUB_CONSOLE_API_BASE — empty means same origin
}

func LoadConfig() Config {
	cfg := Config{
		HugrURL:           envOrDefault("HUGR_URL", "http://localhost:15004"),
		HugrSecretKey:     envOrDefault("HUGR_SECRET_KEY", ""),
		ListenAddr:        envOrDefault("HUB_SERVICE_LISTEN", ":10000"),
		FlightAddr:        envOrDefault("HUB_SERVICE_FLIGHT", ":10001"),
		DatabaseDSN:       envOrDefault("HUB_DATABASE_DSN", "postgres://hugr:hugr_password@localhost:18032/hub"),
		AgentDatabaseDSN:  envOrDefault("HUB_AGENT_DATABASE_DSN", "postgres://hugr:hugr_password@localhost:18032/agent"),
		AgentEmbedder:     envOrDefault("HUB_AGENT_EMBEDDER", "gemma-embedding"),
		AgentVectorSize:   envInt("HUB_AGENT_VECTOR_SIZE", 768),
		AgentJWTKeyFile:   envOrDefault("HUB_AGENT_JWT_KEY", ""),
		AgentJWTIssuer:    envOrDefault("HUB_AGENT_JWT_ISSUER", "hub-agents"),
		AgentTokenTTL:     envDuration("HUB_AGENT_TOKEN_TTL", 30*time.Minute),
		AgentBootstrapTTL: envDuration("HUB_AGENT_BOOTSTRAP_TTL", 10*time.Minute),
		AgentTokenListen:  envOrDefault("HUB_AGENT_TOKEN_LISTEN", ""),
		AgentHugrURL:      envOrDefault("HUB_AGENT_HUGR_URL", ""),
		AgentHugrIssuer:   envOrDefault("HUB_AGENT_HUGR_ISSUER", ""),
		AgentTokenURL:     envOrDefault("HUB_AGENT_TOKEN_URL", ""),
		AgentPublishAPI:   envBool("HUB_AGENT_PUBLISH_API", false),
		AgentMemoryBytes:  envInt64("HUB_AGENT_MEMORY_BYTES", 0),
		AgentNanoCPUs:     envInt64("HUB_AGENT_NANO_CPUS", 0),
		AgentPidsLimit:    envInt64("HUB_AGENT_PIDS_LIMIT", 0),
		InternalURL:       envOrDefault("HUB_SERVICE_INTERNAL_URL", "http://hub-service:8082"),
		RedisURL:          envOrDefault("HUB_REDIS_URL", "redis://localhost:6379/0"),
		StoragePath:       envOrDefault("HUB_STORAGE_PATH", "/var/hub-storage"),
		QueryTimeout:      envDuration("HUGR_QUERY_TIMEOUT", 5*time.Minute),
		SubscriptionPool:  envInt("HUB_SUBSCRIPTION_POOL_MAX", 20),

		ConsoleEnabled:      envBool("HUB_CONSOLE_ENABLED", true),
		ConsoleOIDCIssuer:   envOrDefault("HUB_CONSOLE_OIDC_ISSUER", ""),
		ConsoleOIDCClientID: envOrDefault("HUB_CONSOLE_OIDC_CLIENT_ID", ""),
		ConsoleOIDCScopes:   envOrDefault("HUB_CONSOLE_OIDC_SCOPES", "openid profile email"),
		ConsoleAPIBase:      envOrDefault("HUB_CONSOLE_API_BASE", ""),
	}

	switch os.Getenv("LOG_LEVEL") {
	case "debug":
		cfg.LogLevel = slog.LevelDebug
	case "warn":
		cfg.LogLevel = slog.LevelWarn
	case "error":
		cfg.LogLevel = slog.LevelError
	default:
		cfg.LogLevel = slog.LevelInfo
	}

	return cfg
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		var n int
		// Accept an explicit 0 (a valid value — e.g. HUB_AGENT_VECTOR_SIZE=0
		// disables embeddings); only a negative or unparseable value falls
		// back to the default.
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n >= 0 {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func envInt64(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		var n int64
		// Accept an explicit 0 (a valid "unlimited" value); only a negative or
		// unparseable value falls back to the default.
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n >= 0 {
			return n
		}
	}
	return def
}

// envBool reads a boolean env var. True values: 1, t, true, yes, on
// (case-insensitive); anything else (including unset) yields def when unset, or
// false when set to a non-true value.
func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "t", "true", "yes", "on":
		return true
	default:
		return false
	}
}
