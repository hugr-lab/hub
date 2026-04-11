package auth

import (
	"fmt"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// JWTValidator validates OIDC JWT tokens using JWKS-discovered public key.
type JWTValidator struct {
	jwks     *JWKSProvider
	roleClaim string // JWT claim for role (default: "x-hugr-role")
}

func NewJWTValidator(jwks *JWKSProvider) *JWTValidator {
	return &JWTValidator{
		jwks:      jwks,
		roleClaim: "x-hugr-role",
	}
}

// Validate parses and validates a JWT token, returning user info.
func (v *JWTValidator) Validate(tokenStr string) (*UserInfo, error) {
	key, err := v.jwks.PublicKey()
	if err != nil {
		return nil, fmt.Errorf("get public key: %w", err)
	}

	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return key, nil
	})

	if err != nil {
		// Try refreshing the key (key rotation)
		key, refreshErr := v.jwks.Refresh()
		if refreshErr != nil {
			return nil, fmt.Errorf("validate JWT: %w", err)
		}
		token, err = jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
			return key, nil
		})
		if err != nil {
			return nil, fmt.Errorf("validate JWT after key refresh: %w", err)
		}
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	// User identity is keyed by the OIDC `sub` claim — same as what Hugr's
	// own JWT provider uses for `[$auth.user_id]`. This must match so that:
	//   - hub.db.users.id (seeded by jupyterhub_config.py from sub)
	//   - the user_id stored on conversations / agents / user_agents
	//     (set via Hugr GraphQL with the sub in [$auth.user_id])
	//   - the WebSocket gateway's userID extracted here
	// all reference the same identifier. We fall back to preferred_username
	// for tokens that lack a sub claim.
	userID := claimString(claims, "sub")
	if userID == "" {
		userID = claimString(claims, "preferred_username")
	}

	name := claimString(claims, "preferred_username")
	if name == "" {
		name = claimString(claims, "name")
	}
	role := claimString(claims, v.roleClaim)

	return &UserInfo{
		ID:       userID,
		Name:     name,
		Role:     role,
		AuthType: "jwt",
	}, nil
}

// IsJWT checks if a token string looks like a JWT (3 dot-separated parts).
func IsJWT(token string) bool {
	return strings.Count(token, ".") == 2
}

func claimString(claims jwt.MapClaims, key string) string {
	v, ok := claims[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}
