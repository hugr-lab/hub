package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{}))
	slog.SetDefault(logger)

	mcpURL := os.Getenv("HUB_SERVICE_MCP_URL")
	if mcpURL == "" {
		logger.Error("HUB_SERVICE_MCP_URL not set")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger.Info("hub-agent starting", "mcp_url", mcpURL)

	// TODO: implement MCP client connection, skills loading, conversation loop
	<-ctx.Done()

	logger.Info("hub-agent stopped")
}
