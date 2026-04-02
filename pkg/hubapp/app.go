package hubapp

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/hugr-lab/airport-go/catalog"
	"github.com/hugr-lab/query-engine/client/app"
)

const (
	appName    = "hub"
	appVersion = "0.1.0"
)

type HubApp struct {
	config Config
	logger *slog.Logger
	mux    *app.CatalogMux
}

func New(cfg Config, logger *slog.Logger) *HubApp {
	return &HubApp{
		config: cfg,
		logger: logger,
		mux:    app.New(),
	}
}

func (a *HubApp) Info() app.AppInfo {
	return app.AppInfo{
		Name:          appName,
		Description:   "Analytics Hub — agent memory, query registry, workspace management",
		Version:       appVersion,
		URI:           fmt.Sprintf("grpc://localhost%s", a.config.FlightAddr),
		DefaultSchema: "default",
	}
}

func (a *HubApp) Listner() (net.Listener, error) {
	return net.Listen("tcp", a.config.FlightAddr)
}

func (a *HubApp) Catalog(ctx context.Context) (catalog.Catalog, error) {
	if err := a.registerCatalog(); err != nil {
		return nil, fmt.Errorf("register catalog: %w", err)
	}
	return a.mux, nil
}

func (a *HubApp) DataSources(ctx context.Context) ([]app.DataSourceInfo, error) {
	return []app.DataSourceInfo{
		{
			Name:        "hub",
			Type:        "postgres",
			Description: "Hub Service database (agent metadata, memory, LLM usage)",
			Path:        a.config.DatabaseDSN,
			ReadOnly:    false,
			Version:     appVersion,
			HugrSchema:  hubGraphQLSchema,
		},
	}, nil
}

func (a *HubApp) InitDBSchemaTemplate(ctx context.Context, name string) (string, error) {
	if name == "hub" {
		return hubDBSchema, nil
	}
	return "", fmt.Errorf("unknown data source: %s", name)
}

func (a *HubApp) Init(ctx context.Context) error {
	a.logger.Info("hub app initialized — DB provisioned, starting services")

	// TODO: start MCP server, WebSocket gateway, Agent Manager
	// These will be added in subsequent phases

	return nil
}

func (a *HubApp) Shutdown(ctx context.Context) error {
	a.logger.Info("hub app shutting down")
	return nil
}
