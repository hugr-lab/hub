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
		DatabaseDSN:   envOrDefault("HUB_DATABASE_DSN", "postgres://hugr:hugr_password@localhost:18032/hub"),
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
