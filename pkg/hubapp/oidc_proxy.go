package hubapp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// The console OIDC reverse-proxy (design 009). The browser SPA runs an
// Authorization-Code-with-PKCE flow, but a public OIDC provider CORS-blocks the
// XHR legs (token exchange, refresh, userinfo, JWKS) unless the console's exact
// origin is registered as a provider "web origin" — brittle and per-deployment.
//
// Instead the hub proxies those legs same-origin: /console/config.json hands the
// SPA an OIDC metadata block whose token/userinfo/jwks endpoints point back at
// the hub (handleConsoleConfig), and the handlers here forward server-side to
// the provider's real endpoints. The authorize + end-session legs stay pointed
// at the real provider — they are full-page browser redirects (no CORS) — and
// the issuer stays real so the id_token `iss` still validates. Tokens still live
// in the browser (this removes the CORS wall; it is not a session-cookie BFF).

// oidcMetadata is the subset of the provider's discovery document the console
// flow needs.
type oidcMetadata struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
	JwksURI               string `json:"jwks_uri"`
	EndSessionEndpoint    string `json:"end_session_endpoint,omitempty"`
}

// oidcDiscoveryCache memoises the provider's discovery document (~5m) so the
// config endpoint and each proxied leg don't re-fetch it per request.
type oidcDiscoveryCache struct {
	mu  sync.RWMutex
	val oidcMetadata
	exp time.Time
}

// fetchOIDCDiscovery reads the provider's real .well-known/openid-configuration
// (cached ~5m). The provider base is the same issuer the console config uses:
// the HUB_CONSOLE_OIDC_ISSUER override, else hugr's /auth/config issuer.
func (a *HubApp) fetchOIDCDiscovery(ctx context.Context) (oidcMetadata, error) {
	a.oidcDisc.mu.RLock()
	if a.oidcDisc.exp.After(time.Now()) {
		v := a.oidcDisc.val
		a.oidcDisc.mu.RUnlock()
		return v, nil
	}
	a.oidcDisc.mu.RUnlock()

	issuer := a.config.ConsoleOIDCIssuer
	if issuer == "" {
		ac, err := a.fetchHugrAuthConfig(ctx)
		if err != nil {
			return oidcMetadata{}, err
		}
		issuer = ac.Issuer
	}
	if issuer == "" {
		return oidcMetadata{}, fmt.Errorf("no OIDC issuer available")
	}

	discURL := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discURL, nil)
	if err != nil {
		return oidcMetadata{}, err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return oidcMetadata{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return oidcMetadata{}, fmt.Errorf("OIDC discovery %s: %s", discURL, resp.Status)
	}
	var md oidcMetadata
	if err := json.NewDecoder(resp.Body).Decode(&md); err != nil {
		return oidcMetadata{}, err
	}
	if md.TokenEndpoint == "" {
		return oidcMetadata{}, fmt.Errorf("OIDC discovery %s missing token_endpoint", discURL)
	}

	a.oidcDisc.mu.Lock()
	a.oidcDisc.val = md
	a.oidcDisc.exp = time.Now().Add(5 * time.Minute)
	a.oidcDisc.mu.Unlock()
	return md, nil
}

// handleOIDCToken / _Userinfo / _Jwks proxy the three CORS-sensitive legs to the
// provider's real endpoints. They are mounted under /oidc/ and exempt from the
// auth middleware — they are part of the pre-login token flow and the provider
// authenticates them itself (the token body / the forwarded bearer).
func (a *HubApp) handleOIDCToken(w http.ResponseWriter, r *http.Request) {
	a.proxyOIDCLeg(w, r, func(md oidcMetadata) string { return md.TokenEndpoint })
}

func (a *HubApp) handleOIDCUserinfo(w http.ResponseWriter, r *http.Request) {
	a.proxyOIDCLeg(w, r, func(md oidcMetadata) string { return md.UserinfoEndpoint })
}

func (a *HubApp) handleOIDCJwks(w http.ResponseWriter, r *http.Request) {
	a.proxyOIDCLeg(w, r, func(md oidcMetadata) string { return md.JwksURI })
}

// proxyOIDCLeg forwards the request to the provider endpoint pick(md) returns,
// preserving method, body, and the content-type / authorization / accept
// headers the OIDC client set. It does not forward Host or any hub credential.
func (a *HubApp) proxyOIDCLeg(w http.ResponseWriter, r *http.Request, pick func(oidcMetadata) string) {
	md, err := a.fetchOIDCDiscovery(r.Context())
	if err != nil {
		a.logger.Warn("oidc proxy: discovery unavailable", "error", err)
		http.Error(w, "oidc discovery unavailable", http.StatusBadGateway)
		return
	}
	upstream := pick(md)
	if upstream == "" {
		http.Error(w, "endpoint not offered by the OIDC provider", http.StatusNotFound)
		return
	}
	if r.URL.RawQuery != "" {
		if strings.Contains(upstream, "?") {
			upstream += "&" + r.URL.RawQuery
		} else {
			upstream += "?" + r.URL.RawQuery
		}
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, upstream, r.Body)
	if err != nil {
		http.Error(w, "proxy error", http.StatusInternalServerError)
		return
	}
	for _, h := range []string{"Content-Type", "Authorization", "Accept"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.logger.Warn("oidc proxy: upstream unreachable", "error", err)
		http.Error(w, "oidc upstream unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// externalOrigin reconstructs the browser-facing origin (scheme://host) of this
// request, honouring the X-Forwarded-* headers a tunnel / ingress sets, so the
// proxied endpoint URLs the console receives are the ones the browser can reach.
func externalOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if xf := r.Header.Get("X-Forwarded-Proto"); xf != "" {
		scheme = strings.Split(xf, ",")[0]
	}
	host := r.Host
	if xh := r.Header.Get("X-Forwarded-Host"); xh != "" {
		host = strings.Split(xh, ",")[0]
	}
	return scheme + "://" + strings.TrimSpace(host)
}
