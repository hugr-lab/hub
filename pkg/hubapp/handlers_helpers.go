package hubapp

import (
	"context"
	"errors"
	"fmt"

	"github.com/hugr-lab/hub/pkg/auth"
	"github.com/hugr-lab/query-engine/client"
	"github.com/hugr-lab/query-engine/client/app"
)

// userFromArgs extracts caller identity from hidden ArgFromContext args.
// Hugr planner injects these from the request auth context before calling the handler.
//
// Use together with the standard hidden args:
//
//	app.ArgFromContext("user_id",   app.String, app.AuthUserID),
//	app.ArgFromContext("user_name", app.String, app.AuthUserName),
//	app.ArgFromContext("role",      app.String, app.AuthRole),
//	app.ArgFromContext("auth_type", app.String, app.AuthType),
func userFromArgs(r *app.Request) auth.UserInfo {
	return auth.UserInfo{
		ID:       r.String("user_id"),
		Name:     r.String("user_name"),
		Role:     r.String("role"),
		AuthType: r.String("auth_type"),
	}
}

// requireAdmin returns nil if the caller has the hub:management.admin
// capability, or is engine-internal management auth. It is ctx-aware because
// capability lookup queries Hugr (see capabilities.go).
//
// Role names are never hard-coded — deployments grant capabilities to roles
// via `core.role_permissions` mutations. See pkg/hubapp/capabilities.go for
// the full list of declared capability points.
func (a *HubApp) requireAdmin(ctx context.Context, u auth.UserInfo) error {
	if u.AuthType == "management" {
		return nil
	}
	ok, err := a.hasCapability(ctx, u, CapManagementAdmin)
	if err != nil {
		return fmt.Errorf("check capability: %w", err)
	}
	if !ok {
		return errors.New("forbidden: hub:management.admin capability required")
	}
	return nil
}

// requireUser returns an error if no user identity is present.
func requireUser(u auth.UserInfo) error {
	if u.ID == "" {
		return errors.New("unauthorized: no user identity")
	}
	return nil
}

// withIdentity returns a context that propagates the caller's identity to
// internal Hugr client calls (a.client.Query, a.client.Subscribe).
// Server-internal calls run as the caller, so Hugr RBAC + row-level security apply.
func withIdentity(ctx context.Context, u auth.UserInfo) context.Context {
	return client.AsUser(ctx, u.ID, u.Name, u.Role)
}

// checkAgentAccess verifies the user has at least the requested access grant
// on the agent. Grant levels: "owner" > "member" (stored in
// hub.db.user_agents.role, unrelated to the authentication role from the JWT).
//
// Engine-internal `management` auth bypasses unconditionally. An OIDC caller
// also bypasses if they have the hub:management.admin capability granted via
// Hugr role_permissions. Otherwise they must have an explicit grant row in
// hub.db.user_agents.
func (a *HubApp) checkAgentAccess(ctx context.Context, u auth.UserInfo, agentID, requiredRole string) error {
	if u.AuthType == "management" {
		return nil
	}
	if u.ID != "" {
		ok, err := a.hasCapability(ctx, u, CapManagementAdmin)
		if err != nil {
			return fmt.Errorf("check capability: %w", err)
		}
		if ok {
			return nil
		}
	}
	if u.ID == "" {
		return errors.New("unauthorized")
	}

	res, err := a.client.Query(ctx,
		`query($uid: String!, $aid: String!) { hub { db { user_agents(
			filter: { user_id: { eq: $uid }, agent_id: { eq: $aid } }
			limit: 1
		) { role } } } }`,
		map[string]any{"uid": u.ID, "aid": agentID},
	)
	if err != nil {
		return fmt.Errorf("check access: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return res.Err()
	}

	var access []struct {
		Role string `json:"role"`
	}
	if err := res.ScanData("hub.db.user_agents", &access); err != nil || len(access) == 0 {
		return fmt.Errorf("forbidden: no access to agent %s", agentID)
	}

	got := access[0].Role
	if requiredRole == "owner" && got != "owner" {
		return fmt.Errorf("forbidden: owner access required (have %s)", got)
	}
	return nil
}
