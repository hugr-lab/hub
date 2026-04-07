package auth

import (
	"context"
	"net/http"
)

type userContextKey struct{}

// UserInfo holds user identity for Hugr requests.
type UserInfo struct {
	ID       string
	Name     string
	Role     string
	AuthType string // "management", "jwt", "agent"
}

// ContextWithUser returns a context with user identity.
func ContextWithUser(ctx context.Context, u UserInfo) context.Context {
	return context.WithValue(ctx, userContextKey{}, u)
}

// UserFromContext extracts user identity from context.
func UserFromContext(ctx context.Context) (UserInfo, bool) {
	u, ok := ctx.Value(userContextKey{}).(UserInfo)
	return u, ok
}

// UserTransport injects x-hugr-user-* headers from context into every request.
type UserTransport struct {
	Base http.RoundTripper
}

func (t *UserTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if u, ok := UserFromContext(req.Context()); ok {
		if u.ID != "" {
			req.Header.Set("x-hugr-user-id", u.ID)
		}
		if u.Name != "" {
			req.Header.Set("x-hugr-user-name", u.Name)
		}
		if u.Role != "" {
			req.Header.Set("x-hugr-role", u.Role)
		}
	}
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}
