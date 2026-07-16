package hubapp

import (
	"io"
	"net/http"
	"strings"

	"github.com/hugr-lab/hub/pkg/auth"
)

// hugrProxyHandler forwards GraphQL requests to Hugr as the caller.
//
// A caller authenticated by their own JWT is forwarded with THAT bearer
// verbatim — the user's token is a hugr-trusted credential (the same D1
// principle the agent gateway rides), so hugr authenticates it natively
// (`auth_type: jwt`). This deliberately avoids the impersonation path for
// real users: hugr silently no-ops mutating app functions under
// impersonation auth (query-engine ask #7) — management-plane mutations
// (create_chat, …) would return null without executing.
//
// Only secret-key (management) callers — who have no bearer to forward —
// use management secret + x-hugr-impersonated-* headers, preserving the
// admin/ops flow (impersonated TABLE functions and CRUD work fine).
//
// Route: /hugr (registered in app.go)
// Method: POST (GraphQL query/mutation in JSON body)
// Auth: Bearer token (user OIDC JWT) or x-hugr-secret-key — resolved by auth middleware
func (a *HubApp) hugrProxyHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		u, ok := auth.UserFromContext(r.Context())
		if !ok || u.ID == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		hugrURL := a.config.HugrURL
		// Strip /ipc suffix if present — proxy goes to /query (JSON, not IPC)
		if len(hugrURL) > 4 && hugrURL[len(hugrURL)-4:] == "/ipc" {
			hugrURL = hugrURL[:len(hugrURL)-4]
		}

		proxyReq, err := http.NewRequestWithContext(r.Context(), "POST", hugrURL+"/query", r.Body)
		if err != nil {
			http.Error(w, "proxy error", http.StatusInternalServerError)
			return
		}
		proxyReq.Header.Set("Content-Type", "application/json")
		if bearer := r.Header.Get("Authorization"); u.AuthType == "jwt" && strings.HasPrefix(bearer, "Bearer ") {
			// The middleware validated this JWT — hand hugr the original
			// credential, not a downgraded impersonation. Gated on jwt
			// specifically (not merely "not management"): a future
			// agent-facing auth path would carry a bearer signed by a
			// different issuer hugr need not trust here, and must fall
			// through to the secret+impersonation flow, never be forwarded.
			proxyReq.Header.Set("Authorization", bearer)
			// Preview-as-role: forward an explicit impersonation directive the
			// caller attached (the console's role-access preview sends it ONLY on
			// the read-only check_access query — general traffic never does, so
			// mutations are unaffected). Safe to forward verbatim: hugr gates
			// impersonation on the CALLER's own role carrying can_impersonate and
			// 403s anyone else — the hub adds no gate of its own. Role only; we do
			// not forward an impersonated user id/name over the JWT path.
			if role := r.Header.Get("X-Hugr-Impersonated-Role"); role != "" {
				proxyReq.Header.Set("X-Hugr-Impersonated-Role", role)
			}
		} else {
			proxyReq.Header.Set("X-Hugr-Secret-Key", a.config.HugrSecretKey)
			proxyReq.Header.Set("X-Hugr-Impersonated-User-Id", u.ID)
			if u.Name != "" {
				proxyReq.Header.Set("X-Hugr-Impersonated-User-Name", u.Name)
			}
			if u.Role != "" {
				proxyReq.Header.Set("X-Hugr-Impersonated-Role", u.Role)
			}
		}

		resp, err := http.DefaultClient.Do(proxyReq)
		if err != nil {
			a.logger.Warn("hugr proxy error", "error", err)
			http.Error(w, "hugr unavailable", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		// Forward response headers and body
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}
}
