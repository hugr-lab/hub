package auth

import (
	"context"

	"github.com/hugr-lab/query-engine/client"
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

// InjectIdentity wraps ctx with query-engine AsUser for Hugr calls.
// Both client.Query() and client.Subscribe() respect this context.
func InjectIdentity(ctx context.Context, u UserInfo) context.Context {
	return client.AsUser(ctx, u.ID, u.Name, u.Role)
}
