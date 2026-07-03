package hubapp

import (
	"fmt"
	"log/slog"
	"os"
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
	// AgentConfigFile is a YAML/JSON agent-config file returned by agent_info as
	// a testing fallback when the calling agent is not yet registered in
	// hub.agent.db (the "return the settings we have locally" path). Same shape
	// hugen's config.LoadStaticInput parses. Empty disables the fallback.
	AgentConfigFile string
	RedisURL      string // Redis URL for per-user rate limiting (required)
	StoragePath   string // Root directory for persistent storage (HUB_STORAGE_PATH)
	QueryTimeout      time.Duration // Timeout for Hugr GraphQL queries (HUGR_QUERY_TIMEOUT)
	SubscriptionPool  int           // Max WebSocket connections for subscriptions (HUB_SUBSCRIPTION_POOL_MAX)
	LogLevel          slog.Level
}

func LoadConfig() Config {
	cfg := Config{
		HugrURL:       envOrDefault("HUGR_URL", "http://localhost:15004"),
		HugrSecretKey: envOrDefault("HUGR_SECRET_KEY", ""),
		ListenAddr:    envOrDefault("HUB_SERVICE_LISTEN", ":10000"),
		FlightAddr:    envOrDefault("HUB_SERVICE_FLIGHT", ":10001"),
		DatabaseDSN:      envOrDefault("HUB_DATABASE_DSN", "postgres://hugr:hugr_password@localhost:18032/hub"),
		AgentDatabaseDSN: envOrDefault("HUB_AGENT_DATABASE_DSN", "postgres://hugr:hugr_password@localhost:18032/agent"),
		AgentConfigFile:  envOrDefault("HUB_AGENT_CONFIG_FILE", ""),
		InternalURL:   envOrDefault("HUB_SERVICE_INTERNAL_URL", "http://hub-service:8082"),
		RedisURL:      envOrDefault("HUB_REDIS_URL", "redis://localhost:6379/0"),
		StoragePath:   envOrDefault("HUB_STORAGE_PATH", "/var/hub-storage"),
		QueryTimeout:     envDuration("HUGR_QUERY_TIMEOUT", 5*time.Minute),
		SubscriptionPool: envInt("HUB_SUBSCRIPTION_POOL_MAX", 20),
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

// LoadRoutingConfig builds an LLM intent routing config from LLM_ROUTING_* env vars.
// Returns nil if no routing env vars are set (= auto-resolve for all intents).
func LoadRoutingConfig() map[string]string {
	intents := map[string]string{
		"default":        os.Getenv("LLM_ROUTING_DEFAULT"),
		"planning":       os.Getenv("LLM_ROUTING_PLANNING"),
		"tool_calling":   os.Getenv("LLM_ROUTING_TOOL_CALLING"),
		"summarization":  os.Getenv("LLM_ROUTING_SUMMARIZATION"),
		"classification": os.Getenv("LLM_ROUTING_CLASSIFICATION"),
	}
	// Filter empty entries.
	result := make(map[string]string)
	for k, v := range intents {
		if v != "" {
			result[k] = v
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
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
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
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
