package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hugr-lab/hub/pkg/agent"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{}))
	slog.SetDefault(logger)

	mcpURL := os.Getenv("HUB_SERVICE_MCP_URL")
	if mcpURL == "" {
		logger.Error("HUB_SERVICE_MCP_URL not set")
		os.Exit(1)
	}

	skillsDir := os.Getenv("AGENT_SKILLS_DIR")
	if skillsDir == "" {
		skillsDir = "/.agent/skills"
	}

	configPath := os.Getenv("AGENT_CONFIG")
	if configPath == "" {
		configPath = "/.agent/config.json"
	}

	authToken := os.Getenv("AGENT_TOKEN")
	agentContext := os.Getenv("HUB_AGENT_CONTEXT") // "local" or "remote"
	listenAddr := os.Getenv("HUB_AGENT_LISTEN")    // e.g. "localhost:18888"

	cfg, err := agent.LoadConfig(configPath)
	if err != nil {
		logger.Error("failed to load config", "path", configPath, "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	a := agent.New(mcpURL, authToken, skillsDir, cfg, logger)

	// Workspace context: read OIDC token from connections.json
	// (written by hugr_connection_service hub_token_provider)
	if agentContext == "local" {
		configPath := os.Getenv("HUGR_CONFIG_PATH")
		connName := os.Getenv("HUGR_CONNECTION_NAME")
		ts := agent.NewTokenSource(configPath, connName)
		ts.Start()
		defer ts.Stop()
		a.SetTokenSource(ts)
	}

	logger.Info("hub-agent starting",
		"mcp_url", mcpURL,
		"skills_dir", skillsDir,
		"config", configPath,
		"context", agentContext,
		"listen", listenAddr,
		"mcp_servers", len(cfg.MCPServers),
		"max_turns", cfg.MaxTurns,
	)

	switch {
	case agentContext == "local" && listenAddr != "":
		// Local mode: listen for WebSocket connections from agent-bridge
		if err := a.RunLocal(ctx, listenAddr); err != nil {
			logger.Error("agent local server failed", "error", err)
			os.Exit(1)
		}

	case os.Getenv("HUB_SERVICE_AGENT_WS") != "" && os.Getenv("AGENT_INSTANCE_ID") != "":
		// Remote mode: connect to Hub Service via WebSocket with retry.
		// Hub-service may restart and lose tokenIndex temporarily; Reconstruct
		// recovers it from container labels, but there's a race window.
		wsURL := os.Getenv("HUB_SERVICE_AGENT_WS")
		instanceID := os.Getenv("AGENT_INSTANCE_ID")
		maxRetries := 10
		for attempt := 0; attempt <= maxRetries; attempt++ {
			err := a.RunWebSocket(ctx, wsURL, instanceID)
			if err == nil || ctx.Err() != nil {
				break
			}
			if attempt < maxRetries {
				delay := time.Duration(1<<min(attempt, 5)) * time.Second // 1s, 2s, 4s, 8s, 16s, 32s...
				logger.Warn("agent connection failed, retrying", "attempt", attempt+1, "delay", delay, "error", err)
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					break
				}
			} else {
				logger.Error("agent failed after retries", "error", err)
				os.Exit(1)
			}
		}

	default:
		// Stdin mode: for development and testing
		if err := a.Run(ctx); err != nil {
			logger.Error("agent failed", "error", err)
			os.Exit(1)
		}
	}

	a.Close()
	logger.Info("hub-agent stopped")
}
