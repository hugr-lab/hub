package hubapp

import (
	"log/slog"
	"os"
)

type Config struct {
	HugrURL       string
	HugrSecretKey string
	ListenAddr    string // HTTP server (MCP, WebSocket, API)
	FlightAddr    string // gRPC Flight server
	LogLevel      slog.Level
}

func LoadConfig() Config {
	cfg := Config{
		HugrURL:       envOrDefault("HUGR_URL", "http://localhost:15004"),
		HugrSecretKey: envOrDefault("HUGR_SECRET_KEY", ""),
		ListenAddr:    envOrDefault("HUB_SERVICE_LISTEN", ":8082"),
		FlightAddr:    envOrDefault("HUB_SERVICE_FLIGHT", ":50051"),
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
