package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/hugr-lab/hub/pkg/hubapp"
	"github.com/hugr-lab/query-engine/client"
)

func main() {
	cfg := hubapp.LoadConfig()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))
	slog.SetDefault(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	app := hubapp.New(cfg, logger)

	logger.Info("starting hub-service",
		"hugr_url", cfg.HugrURL,
		"listen", cfg.ListenAddr,
		"flight", cfg.FlightAddr,
	)

	c := client.NewClient(cfg.HugrURL, client.WithApiKeyCustomHeader(cfg.HugrSecretKey, "x-hugr-secret-key"))

	err := c.RunApplication(ctx, app,
		client.WithSecretKey(cfg.HugrSecretKey),
		client.WithLogger(logger),
	)
	if err != nil && ctx.Err() == nil {
		logger.Error("hub-service failed", "error", err)
		os.Exit(1)
	}

	logger.Info("hub-service stopped")
}
