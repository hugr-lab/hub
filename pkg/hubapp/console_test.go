package hubapp

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func decodeConfig(t *testing.T, a *HubApp) consoleRuntimeConfig {
	t.Helper()
	rec := httptest.NewRecorder()
	a.handleConsoleConfig(rec, httptest.NewRequest(http.MethodGet, "/console/config.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	var got consoleRuntimeConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

func TestHandleConsoleConfig_DiscoversFromHugr(t *testing.T) {
	var gotPath string
	hugr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(hugrAuthConfig{
			Issuer:   "https://idp.example/realms/acme",
			ClientID: "hugr",
		})
	}))
	defer hugr.Close()

	// HugrURL carries the app-framework /ipc suffix; /auth/config must be fetched
	// from the trimmed base.
	a := &HubApp{
		logger: discardLogger(),
		config: Config{
			HugrURL:           hugr.URL + "/ipc",
			ConsoleOIDCScopes: "openid profile email",
		},
	}

	got := decodeConfig(t, a)
	if gotPath != "/auth/config" {
		t.Fatalf("fetched path %q, want /auth/config", gotPath)
	}
	if got.OIDCIssuer != "https://idp.example/realms/acme" || got.OIDCClientID != "hugr" {
		t.Fatalf("discovered config: got %+v", got)
	}
	if got.OIDCScopes != "openid profile email" {
		t.Fatalf("scopes: got %q", got.OIDCScopes)
	}
}

func TestHandleConsoleConfig_ExplicitOverrideWins(t *testing.T) {
	called := false
	hugr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_ = json.NewEncoder(w).Encode(hugrAuthConfig{Issuer: "https://discovered/realms/x", ClientID: "hugr"})
	}))
	defer hugr.Close()

	a := &HubApp{
		logger: discardLogger(),
		config: Config{
			HugrURL:             hugr.URL,
			ConsoleOIDCIssuer:   "https://override.example/realms/acme",
			ConsoleOIDCClientID: "console",
		},
	}

	got := decodeConfig(t, a)
	if got.OIDCIssuer != "https://override.example/realms/acme" || got.OIDCClientID != "console" {
		t.Fatalf("override: got %+v", got)
	}
	if called {
		t.Fatalf("hugr /auth/config should not be fetched when both values are overridden")
	}
}

func TestHandleConsoleConfig_GracefulWhenHugrDown(t *testing.T) {
	// Point at a closed server; discovery fails, the endpoint still returns 200
	// with an empty issuer so the SPA can show "sign-in unavailable".
	down := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := down.URL
	down.Close()

	a := &HubApp{logger: discardLogger(), config: Config{HugrURL: url}}
	got := decodeConfig(t, a)
	if got.OIDCIssuer != "" || got.OIDCClientID != "" {
		t.Fatalf("expected empty config on discovery failure, got %+v", got)
	}
}
