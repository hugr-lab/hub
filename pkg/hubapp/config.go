package hubapp

import (
	"log/slog"
	"os"
	"time"
)

type Config struct {
	HugrURL       string
	HugrSecretKey string
	ListenAddr    string // HTTP server (MCP, WebSocket, API)
	FlightAddr    string // gRPC Flight server
	DatabaseDSN   string // PostgreSQL DSN for hub DB
	RedisURL      string // Redis URL for per-user rate limiting (required)
	QueryTimeout  time.Duration // Timeout for Hugr GraphQL queries (HUGR_QUERY_TIMEOUT)
	LogLevel      slog.Level
}

func LoadConfig() Config {
	cfg := Config{
		HugrURL:       envOrDefault("HUGR_URL", "http://localhost:15004"),
		HugrSecretKey: envOrDefault("HUGR_SECRET_KEY", ""),
		ListenAddr:    envOrDefault("HUB_SERVICE_LISTEN", ":10000"),
		FlightAddr:    envOrDefault("HUB_SERVICE_FLIGHT", ":10001"),
		DatabaseDSN:   envOrDefault("HUB_DATABASE_DSN", "postgres://hugr:hugr_password@localhost:18032/hub"),
		RedisURL:      envOrDefault("HUB_REDIS_URL", "redis://localhost:6379/0"),
		QueryTimeout:  envDuration("HUGR_QUERY_TIMEOUT", 5*time.Minute),
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

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
