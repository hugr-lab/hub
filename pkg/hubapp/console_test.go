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

func TestHandleConsoleConfig_SeedsProxiedOIDCMetadata(t *testing.T) {
	// One server plays both hugr (/auth/config) and the OIDC provider
	// (/.well-known/openid-configuration), so the discovered issuer is itself.
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/config":
			_ = json.NewEncoder(w).Encode(hugrAuthConfig{Issuer: srv.URL, ClientID: "hugr"})
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(oidcMetadata{
				Issuer:                srv.URL,
				AuthorizationEndpoint: srv.URL + "/auth",
				TokenEndpoint:         srv.URL + "/token",
				UserinfoEndpoint:      srv.URL + "/userinfo",
				JwksURI:               srv.URL + "/certs",
				EndSessionEndpoint:    srv.URL + "/logout",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	a := &HubApp{logger: discardLogger(), config: Config{HugrURL: srv.URL + "/ipc"}}
	got := decodeConfig(t, a)

	if got.OIDC == nil {
		t.Fatal("expected an oidc metadata block")
	}
	// Provider-real legs stay: issuer (iss validation) + the redirect legs.
	if got.OIDC.Issuer != srv.URL {
		t.Fatalf("issuer: got %q, want %q", got.OIDC.Issuer, srv.URL)
	}
	if got.OIDC.AuthorizationEndpoint != srv.URL+"/auth" {
		t.Fatalf("authorization_endpoint should stay real: got %q", got.OIDC.AuthorizationEndpoint)
	}
	if got.OIDC.EndSessionEndpoint != srv.URL+"/logout" {
		t.Fatalf("end_session_endpoint should stay real: got %q", got.OIDC.EndSessionEndpoint)
	}
	// CORS-sensitive legs are rewritten to the hub's same-origin proxy — the
	// request Host (example.com by default) drives externalOrigin.
	for name, want := range map[string]string{
		"token_endpoint":    "http://example.com/oidc/token",
		"userinfo_endpoint": "http://example.com/oidc/userinfo",
		"jwks_uri":          "http://example.com/oidc/certs",
	} {
		var got1 string
		switch name {
		case "token_endpoint":
			got1 = got.OIDC.TokenEndpoint
		case "userinfo_endpoint":
			got1 = got.OIDC.UserinfoEndpoint
		case "jwks_uri":
			got1 = got.OIDC.JwksURI
		}
		if got1 != want {
			t.Fatalf("%s: got %q, want %q", name, got1, want)
		}
	}
}

func TestExternalOrigin(t *testing.T) {
	// Default: scheme from TLS (none → http), host from r.Host.
	r := httptest.NewRequest(http.MethodGet, "/console/config.json", nil)
	if o := externalOrigin(r); o != "http://example.com" {
		t.Fatalf("default: got %q", o)
	}
	// Forwarded headers (a tunnel/ingress) win, first value only.
	r.Header.Set("X-Forwarded-Proto", "https, http")
	r.Header.Set("X-Forwarded-Host", "console.acme.io")
	if o := externalOrigin(r); o != "https://console.acme.io" {
		t.Fatalf("forwarded: got %q", o)
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
