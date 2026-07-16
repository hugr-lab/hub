package hubapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// consoleRuntimeConfig is the public runtime config the management-console SPA
// (design 009) fetches at GET /console/config.json before login. It carries no
// secrets — only the browser-reachable OIDC issuer, the public PKCE client id,
// the requested scopes, and the API base (empty = same origin as the SPA).
type consoleRuntimeConfig struct {
	OIDCIssuer   string `json:"oidc_issuer"`
	OIDCClientID string `json:"oidc_client_id"`
	OIDCScopes   string `json:"oidc_scopes"`
	APIBase      string `json:"api_base"`
}

// hugrAuthConfig is hugr's public OIDC descriptor, served at GET /auth/config
// (hugr registers it only when OIDC is enabled). hugr returns the
// browser-reachable issuer + the public client id — so the console discovers the
// provider dynamically instead of pinning a static issuer per deployment.
type hugrAuthConfig struct {
	Issuer   string `json:"issuer"`
	ClientID string `json:"client_id"`
}

// consoleAuthCache memoises hugr's /auth/config so the SPA's config endpoint
// doesn't round-trip to hugr on every load.
type consoleAuthCache struct {
	mu  sync.RWMutex
	val hugrAuthConfig
	exp time.Time
}

// fetchHugrAuthConfig reads hugr's public /auth/config (cached ~5m). The hugr
// base is HugrURL with a trailing /ipc (the app-framework registration path)
// trimmed off.
func (a *HubApp) fetchHugrAuthConfig(ctx context.Context) (hugrAuthConfig, error) {
	a.consoleAuth.mu.RLock()
	if a.consoleAuth.exp.After(time.Now()) {
		v := a.consoleAuth.val
		a.consoleAuth.mu.RUnlock()
		return v, nil
	}
	a.consoleAuth.mu.RUnlock()

	base := strings.TrimSuffix(strings.TrimRight(a.config.HugrURL, "/"), "/ipc")
	url := strings.TrimRight(base, "/") + "/auth/config"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return hugrAuthConfig{}, err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return hugrAuthConfig{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return hugrAuthConfig{}, fmt.Errorf("hugr GET /auth/config: %s", resp.Status)
	}
	var out hugrAuthConfig
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return hugrAuthConfig{}, err
	}

	a.consoleAuth.mu.Lock()
	a.consoleAuth.val = out
	a.consoleAuth.exp = time.Now().Add(5 * time.Minute)
	a.consoleAuth.mu.Unlock()
	return out, nil
}

// handleConsoleConfig serves the SPA's runtime config. It is public (exempt
// from the auth middleware) because the SPA reads it before the user logs in.
// The OIDC issuer + client id are discovered from hugr's /auth/config unless
// explicitly overridden by HUB_CONSOLE_OIDC_ISSUER / _CLIENT_ID.
func (a *HubApp) handleConsoleConfig(w http.ResponseWriter, r *http.Request) {
	issuer := a.config.ConsoleOIDCIssuer
	clientID := a.config.ConsoleOIDCClientID
	if issuer == "" || clientID == "" {
		if up, err := a.fetchHugrAuthConfig(r.Context()); err != nil {
			a.logger.Warn("console: hugr /auth/config unavailable — OIDC login disabled until reachable", "error", err)
		} else {
			if issuer == "" {
				issuer = up.Issuer
			}
			if clientID == "" {
				clientID = up.ClientID
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(consoleRuntimeConfig{
		OIDCIssuer:   issuer,
		OIDCClientID: clientID,
		OIDCScopes:   a.config.ConsoleOIDCScopes,
		APIBase:      a.config.ConsoleAPIBase,
	})
}
