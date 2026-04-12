package hubapp

import (
	"io"
	"net/http"

	"github.com/hugr-lab/hub/pkg/auth"
)

// hugrProxyHandler forwards GraphQL requests to Hugr with impersonation.
// The caller's identity (from auth middleware) is propagated via
// x-hugr-impersonated-* headers so Hugr evaluates RBAC as that user.
//
// Route: /hugr (registered in app.go)
// Method: POST (GraphQL query/mutation in JSON body)
// Auth: Bearer token (OIDC JWT or AGENT_TOKEN) — resolved by auth middleware
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

		// Forward to Hugr /query with management secret + impersonation
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
		proxyReq.Header.Set("X-Hugr-Secret-Key", a.config.HugrSecretKey)
		proxyReq.Header.Set("X-Hugr-Impersonated-User-Id", u.ID)
		if u.Name != "" {
			proxyReq.Header.Set("X-Hugr-Impersonated-User-Name", u.Name)
		}
		if u.Role != "" {
			proxyReq.Header.Set("X-Hugr-Impersonated-Role", u.Role)
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
