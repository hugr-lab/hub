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

	userID := claimString(claims, "preferred_username")
	if userID == "" {
		userID = claimString(claims, "sub")
	}

	name := claimString(claims, "name")
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
