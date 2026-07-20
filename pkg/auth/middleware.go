package auth

import (
	"log/slog"
	"net/http"
	"strings"
)

// AuthConfig configures the auth middleware.
type AuthConfig struct {
	SecretKey    string // x-hugr-secret-key for management auth
	JWTValidator *JWTValidator
	Logger       *slog.Logger
}

// Middleware authenticates requests via secret key or user OIDC JWT. Sets
// UserInfo in context. Skips /health endpoint.
//
// NOTE (O3): the legacy in-memory agent-token path is gone — spawned agents
// talk to hugr directly (they never present a token to hub-service HTTP), and
// /agent/token is self-authenticating (body token IS the credential).
//
// The skills marketplace (/skills/*, SK1) IS agent-facing — the reconciler
// calls it with the agent JWT — so it is exempt here and verifies the caller
// IN-HANDLER against hugr auth.me (skills_auth.go). auth.me is the authority
// hugr trusts for BOTH end-user IdP tokens AND hub-minted agent tokens, so it
// supersedes the O3 note's "verify agent JWT against the issuer's own key":
// one path covers both caller kinds. The user-OIDC JWKS branch below still
// cannot validate agent tokens, which is exactly why /skills/* skips it.
func Middleware(next http.Handler, cfg AuthConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip health check, the agent token endpoints, the skills
		// marketplace, and the management-console SPA. /agent/token is
		// self-authenticating (the body token/secret IS the credential,
		// spec-hub-side §1.2); the public key is, well, public; /skills/*
		// verifies the bearer in-handler via hugr auth.me (SK1) so it can accept
		// agent tokens the JWKS branch cannot; /console/* and /app/* are the
		// public SPA assets + pre-login runtime config (design 009 — the same
		// build served at both prefixes) — the SPA authenticates its own /hugr +
		// /api/v1 calls with the user's OIDC token; /oidc/* is the
		// pre-login OIDC token/userinfo/jwks reverse-proxy (oidc_proxy.go) — the
		// provider authenticates those legs itself (token body / forwarded bearer).
		if r.URL.Path == "/health" ||
			r.URL.Path == "/agent/token" ||
			r.URL.Path == "/agent/token/public-key" ||
			strings.HasPrefix(r.URL.Path, "/skills/") ||
			r.URL.Path == "/console" ||
			strings.HasPrefix(r.URL.Path, "/console/") ||
			r.URL.Path == "/app" ||
			strings.HasPrefix(r.URL.Path, "/app/") ||
			strings.HasPrefix(r.URL.Path, "/oidc/") {
			next.ServeHTTP(w, r)
			return
		}

		// 1. Secret key (management)
		if key := r.Header.Get("X-Hugr-Secret-Key"); key != "" && key == cfg.SecretKey {
			userID := r.Header.Get("X-Hugr-User-Id")
			role := r.Header.Get("X-Hugr-Role")
			if userID == "" {
				userID = "admin"
			}
			if role == "" {
				role = "admin"
			}
			ctx := ContextWithUser(r.Context(), UserInfo{
				ID:       userID,
				Name:     userID,
				Role:     role,
				AuthType: "management",
			})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// 2. Bearer token (user OIDC JWT)
		bearer := extractBearer(r)
		if bearer != "" {
			if IsJWT(bearer) && cfg.JWTValidator != nil {
				user, err := cfg.JWTValidator.Validate(bearer)
				if err == nil {
					ctx := ContextWithUser(r.Context(), *user)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				if cfg.Logger != nil {
					cfg.Logger.Warn("JWT validation failed", "error", err)
				}
			}
		}

		// No valid auth
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

func extractBearer(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return auth[7:]
	}
	return ""
}
