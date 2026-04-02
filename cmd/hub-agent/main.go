package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

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

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	a := agent.New(mcpURL, skillsDir, logger)

	logger.Info("hub-agent starting", "mcp_url", mcpURL, "skills_dir", skillsDir)

	if err := a.Run(ctx); err != nil {
		logger.Error("agent failed", "error", err)
		os.Exit(1)
	}

	a.Close()
	logger.Info("hub-agent stopped")
}
