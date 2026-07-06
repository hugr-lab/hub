package auth

import (
	"log/slog"
	"net/http"
	"strings"
)

// AuthConfig configures the auth middleware.
type AuthConfig struct {
	SecretKey      string // x-hugr-secret-key for management auth
	JWTValidator   *JWTValidator
	AgentValidator *AgentTokenValidator
	Logger         *slog.Logger
}

// Middleware authenticates requests via secret key, JWT, or agent token.
// Sets UserInfo in context. Skips /health endpoint.
func Middleware(next http.Handler, cfg AuthConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip health check and the agent token endpoints — /agent/token is
		// self-authenticating (the body token/secret IS the credential,
		// spec-hub-side §1.2) and the public key is, well, public.
		if r.URL.Path == "/health" ||
			r.URL.Path == "/agent/token" ||
			r.URL.Path == "/agent/token/public-key" {
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

		// 2. Bearer token (JWT or agent token)
		bearer := extractBearer(r)
		if bearer != "" {
			// Try JWT first
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

			// Try agent token
			if !IsJWT(bearer) && cfg.AgentValidator != nil {
				user, err := cfg.AgentValidator.Validate(r.Context(), bearer)
				if err == nil {
					ctx := ContextWithUser(r.Context(), *user)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				if cfg.Logger != nil {
					cfg.Logger.Warn("agent token validation failed", "error", err)
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
