package hubapp

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
)

type Config struct {
	HugrURL       string
	HugrSecretKey string
	ListenAddr    string // HTTP server (MCP, WebSocket, API)
	FlightAddr    string // gRPC Flight server
	DatabaseDSN   string // PostgreSQL DSN for hub DB
	RedisURL      string // Redis URL for rate limiting and caching
	LLMProviders  []LLMProviderConfig
	LogLevel      slog.Level
}

// LLMProviderConfig describes an LLM provider registered as a Hugr data source.
type LLMProviderConfig struct {
	Name      string `json:"name"`                // data source name: "claude", "gpt4", "local-mistral"
	Type      string `json:"type"`                // "llm-openai", "llm-anthropic", "llm-gemini"
	BaseURL   string `json:"base_url"`            // API endpoint
	Model     string `json:"model"`               // model identifier
	APIKey    string `json:"api_key,omitempty"`    // "${secret:ENV_VAR}" or raw key (optional for local)
	MaxTokens int    `json:"max_tokens,omitempty"` // default max tokens (0 = provider default)
	Timeout   string `json:"timeout,omitempty"`    // request timeout (e.g. "60s", "120s")
	RPM       int    `json:"rpm,omitempty"`        // source-level requests per minute
	TPM       int    `json:"tpm,omitempty"`        // source-level tokens per minute
}

// BuildPath constructs the data source Path URL with query params.
func (p LLMProviderConfig) BuildPath() string {
	u, err := url.Parse(p.BaseURL)
	if err != nil {
		return p.BaseURL
	}
	q := u.Query()
	q.Set("model", p.Model)
	if p.APIKey != "" {
		q.Set("api_key", p.APIKey)
	}
	if p.MaxTokens > 0 {
		q.Set("max_tokens", strconv.Itoa(p.MaxTokens))
	}
	if p.Timeout != "" {
		q.Set("timeout", p.Timeout)
	}
	if p.RPM > 0 {
		q.Set("rpm", strconv.Itoa(p.RPM))
	}
	if p.TPM > 0 {
		q.Set("tpm", strconv.Itoa(p.TPM))
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// Description returns a human-readable description for the data source.
func (p LLMProviderConfig) Description() string {
	return fmt.Sprintf("LLM: %s (%s)", p.Model, p.Type)
}

func LoadConfig() Config {
	cfg := Config{
		HugrURL:       envOrDefault("HUGR_URL", "http://localhost:15004"),
		HugrSecretKey: envOrDefault("HUGR_SECRET_KEY", ""),
		ListenAddr:    envOrDefault("HUB_SERVICE_LISTEN", ":10000"),
		FlightAddr:    envOrDefault("HUB_SERVICE_FLIGHT", ":10001"),
		DatabaseDSN:   envOrDefault("HUB_DATABASE_DSN", "postgres://hugr:hugr_password@localhost:18032/hub"),
		RedisURL:      os.Getenv("HUB_REDIS_URL"),
	}

	// Parse LLM providers from JSON env var
	if raw := os.Getenv("HUB_LLM_PROVIDERS"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg.LLMProviders); err != nil {
			slog.Warn("failed to parse HUB_LLM_PROVIDERS", "error", err)
		}
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
