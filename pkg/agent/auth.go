package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TokenSource reads the current OIDC access token from connections.json.
// In workspace context, hub_token_provider writes this file on every refresh
// (5-60s polling cycle from JupyterHub API). The Go agent re-reads periodically.
//
// For remote agents this is not used — they have a static AGENT_TOKEN.
type TokenSource struct {
	configPath     string // path to connections.json
	connectionName string // which connection to read
	mu             sync.RWMutex
	token          string
	interval       time.Duration
	stop           chan struct{}
}

// NewTokenSource creates a file-based token source.
// configPath: path to connections.json (from HUGR_CONFIG_PATH env or ~/.hugr/connections.json)
// connectionName: which connection entry to read (from HUGR_CONNECTION_NAME env or "default")
func NewTokenSource(configPath, connectionName string) *TokenSource {
	if configPath == "" {
		home, _ := os.UserHomeDir()
		configPath = filepath.Join(home, ".hugr", "connections.json")
	}
	if connectionName == "" {
		connectionName = "default"
	}
	return &TokenSource{
		configPath:     configPath,
		connectionName: connectionName,
		interval:       30 * time.Second,
		stop:           make(chan struct{}),
	}
}

// Start begins periodic token reading from connections.json.
func (ts *TokenSource) Start() {
	ts.refresh()
	go func() {
		ticker := time.NewTicker(ts.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				ts.refresh()
			case <-ts.stop:
				return
			}
		}
	}()
}

// Stop ends periodic refresh.
func (ts *TokenSource) Stop() {
	select {
	case <-ts.stop:
	default:
		close(ts.stop)
	}
}

// Token returns the current access token.
func (ts *TokenSource) Token() string {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.token
}

func (ts *TokenSource) refresh() {
	data, err := os.ReadFile(ts.configPath)
	if err != nil {
		return
	}

	var cfg struct {
		Connections []struct {
			Name   string `json:"name"`
			Tokens struct {
				AccessToken string `json:"access_token"`
			} `json:"tokens"`
		} `json:"connections"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return
	}

	for _, conn := range cfg.Connections {
		if conn.Name == ts.connectionName && conn.Tokens.AccessToken != "" {
			ts.mu.Lock()
			ts.token = conn.Tokens.AccessToken
			ts.mu.Unlock()
			return
		}
	}
}
