package hubapp

// HB5 gateway — the transport plane of the application-facing API
// (spec-hub-gateway §4). This file carries the raw by-agent pass-through:
//
//	ANY /api/v1/agents/{id}/hugen/{path...}  →  container /{path}
//
// The caller's own bearer is forwarded VERBATIM — hugen verifies it against
// hugr (`auth.me`) and enforces session-level access; the hub enforces the
// platform layer here (user_agents grant, re-checked per call). The hub
// management secret never goes downstream. Chat-scoped verbs (G4) build on
// the same forwarding path.

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/hugr-lab/hub/pkg/auth"
)

const (
	// gatewayBodyCap bounds an inbound proxied body (artifact uploads are the
	// largest legitimate payload; hugen enforces its own ingest limit too).
	gatewayBodyCap = 64 << 20 // 64 MiB

	// gatewayCallTimeout bounds non-streaming proxied calls. SSE streams are
	// exempt — they live as long as the client holds the connection.
	gatewayCallTimeout = 60 * time.Second
)

// gatewayTransport is the shared upstream transport: bounded dial (a down
// container must fail fast), keep-alives on, and NO overall response timeout —
// SSE responses stream indefinitely.
var gatewayTransport http.RoundTripper = &http.Transport{
	DialContext:         (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
	MaxIdleConnsPerHost: 8,
	IdleConnTimeout:     90 * time.Second,
}

// gatewayError is the transport-plane error envelope (spec-hub-gateway §4).
func gatewayError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	if status == http.StatusServiceUnavailable {
		w.Header().Set("Retry-After", "5")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": msg},
	})
}

// agentProxyHandler serves ANY /api/v1/agents/{id}/hugen/{path...}.
func (a *HubApp) agentProxyHandler(w http.ResponseWriter, r *http.Request) {
	u, ok := a.gatewayCaller(w, r)
	if !ok {
		return
	}
	agentID := r.PathValue("id")

	// Platform-layer authz: user_agents grant, re-checked on every call so a
	// revocation bites immediately. Admin capability / management bypass live
	// inside checkAgentAccess.
	if err := a.checkAccess(r.Context(), u, agentID); err != nil {
		gatewayError(w, http.StatusForbidden, "no_agent_access", err.Error())
		return
	}

	base, ok := a.resolveAgentBase(w, r, agentID)
	if !ok {
		return
	}
	a.proxyToAgent(w, r, agentID, base, "/"+r.PathValue("path"))
}

// gatewayCaller authenticates a transport-plane request: an identity from the
// auth middleware AND a bearer to forward. The container authenticates the END
// USER's own token (D1) — a caller authenticated by the management secret has
// nothing to forward.
func (a *HubApp) gatewayCaller(w http.ResponseWriter, r *http.Request) (auth.UserInfo, bool) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok || u.ID == "" {
		gatewayError(w, http.StatusUnauthorized, "unauthorized", "no user identity")
		return auth.UserInfo{}, false
	}
	bearer := r.Header.Get("Authorization")
	if !strings.HasPrefix(bearer, "Bearer ") && !strings.HasPrefix(bearer, "bearer ") {
		gatewayError(w, http.StatusForbidden, "user_token_required",
			"the agent transport requires a user bearer token (the management key is never forwarded)")
		return auth.UserInfo{}, false
	}
	return u, true
}

// resolveAgentBase resolves the agent's dialable API base URL, writing the
// error envelope (503 agent_not_running / agent_not_started) when it can't.
func (a *HubApp) resolveAgentBase(w http.ResponseWriter, r *http.Request, agentID string) (string, bool) {
	if a.agentRuntime == nil {
		gatewayError(w, http.StatusServiceUnavailable, "agent_not_running", "agent runtime unavailable on this hub")
		return "", false
	}
	base, err := a.agentRuntime.APIBaseURL(agentID)
	if err != nil {
		code := "agent_not_running"
		msg := "agent has no running container"
		// Refine for hands-off agents: starting one is an explicit action.
		if a.client != nil {
			if info, aerr := a.agentForToken(r.Context(), agentID); aerr == nil && info.Status == "manual" {
				code = "agent_not_started"
				msg = "agent is in manual mode and not started"
			}
		}
		gatewayError(w, http.StatusServiceUnavailable, code, msg)
		return "", false
	}
	return base, true
}

// proxyToAgent forwards the request to `base` with its path rewritten to
// `rest`, streaming SSE responses through unbuffered. The caller has already
// authenticated + authorized.
func (a *HubApp) proxyToAgent(w http.ResponseWriter, r *http.Request, agentID, base, rest string) {
	target, err := url.Parse(base)
	if err != nil {
		gatewayError(w, http.StatusBadGateway, "agent_unreachable", "invalid agent endpoint")
		return
	}

	// SSE streams are exempt from the call timeout; everything else is bounded.
	if !isStreamRequest(r, rest) {
		ctx, cancel := context.WithTimeout(r.Context(), gatewayCallTimeout)
		defer cancel()
		r = r.WithContext(ctx)
	}
	if r.Body != nil && r.ContentLength != 0 {
		r.Body = http.MaxBytesReader(w, r.Body, gatewayBodyCap)
	}

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = target.Scheme
			pr.Out.URL.Host = target.Host
			pr.Out.URL.Path = rest
			pr.Out.URL.RawQuery = r.URL.RawQuery
			pr.Out.Host = target.Host
			pr.SetXForwarded()
			// The inbound Authorization (the user's bearer) survives on pr.Out —
			// ProxyRequest clones inbound headers minus hop-by-hop. Hub-side
			// credentials must NOT: the management secret and the trusted
			// identity headers stop at the hub (the container trusts only the
			// bearer it verifies itself).
			stripHubAuthHeaders(pr.Out.Header)
		},
		// Flush every write immediately — SSE frames must not sit in a buffer.
		FlushInterval: -1,
		Transport:     gatewayTransport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if r.Context().Err() != nil {
				return // client went away / call timeout — nothing to write
			}
			a.logger.Warn("agent proxy error", "agent", agentID, "path", rest, "error", err)
			gatewayError(w, http.StatusBadGateway, "agent_unreachable", "agent did not respond")
		},
	}
	rp.ServeHTTP(w, r)
}

// stripHubAuthHeaders removes hub-side credentials from an outbound proxied
// request: the management secret key and the secret-key-trusted identity /
// impersonation headers. Only the user's own bearer may travel downstream (D1).
func stripHubAuthHeaders(h http.Header) {
	for _, k := range []string{
		"X-Hugr-Secret-Key",
		"X-Hugr-User-Id",
		"X-Hugr-Role",
		"X-Hugr-Impersonated-User-Id",
		"X-Hugr-Impersonated-User-Name",
		"X-Hugr-Impersonated-Role",
	} {
		h.Del(k)
	}
}

// checkAccess is the gateway's authz seam — defaults to checkAgentAccess
// (user_agents grant via hugr); tests override accessCheck.
func (a *HubApp) checkAccess(ctx context.Context, u auth.UserInfo, agentID string) error {
	if a.accessCheck != nil {
		return a.accessCheck(ctx, u, agentID)
	}
	return a.checkAgentAccess(ctx, u, agentID, "")
}

// isStreamRequest reports whether the proxied call is an SSE stream — by the
// hugen surface shape (`…/stream`) or an explicit event-stream Accept.
func isStreamRequest(r *http.Request, path string) bool {
	return strings.HasSuffix(path, "/stream") ||
		strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}
