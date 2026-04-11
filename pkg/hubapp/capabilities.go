package hubapp

// Capability points declared by hub-service.
//
// Virtual type `hub:management` is used as a namespaced permission key in
// `core.role_permissions`. Hub-service never hard-codes role names; whoever
// the deployment grants the capability to — regardless of what that role is
// called — is the authorized caller. Both this package and
// `jupyterhub_config.py` consult the same table so admin recognition is
// unified across the stack.
//
// Full setup, cache behavior, and deployment runbook:
// see pkg/hubapp/CAPABILITIES.md
//
// # Declared capabilities
//
// | Capability                   | Governs                                                 |
// |------------------------------|---------------------------------------------------------|
// | hub:management.admin         | Cross-user admin — delete_agent, bypass agent ownership |
//
// New capability points should be added here as constants AND documented in
// CAPABILITIES.md.

import (
	"context"
	"fmt"

	"github.com/hugr-lab/hub/pkg/auth"
)

const (
	// CapManagementNamespace is the logical "type" under which hub-service
	// declares its capability points in core.role_permissions.
	CapManagementNamespace = "hub:management"

	// CapManagementAdmin gates cross-user admin operations such as
	// `delete_agent` and bypass of hub.db.user_agents ownership in
	// `checkAgentAccess`.
	CapManagementAdmin = "admin"
)

// hasCapability returns true if the caller's role has the given capability
// enabled. Looks up via `function.core.auth.my_permissions` (which returns the
// caller's full role permission list) and matches locally.
//
// NOTE: we deliberately avoid `function.core.auth.check_access` here because
// in the current query-engine build it returns a different result than
// `my_permissions` in the same request (scalar vs table function context
// propagation issue). Matching the raw permission list ourselves is
// consistent with what the planner itself evaluates on mutation gating.
//
// Management auth always has every capability (engine-internal bus).
func (a *HubApp) hasCapability(ctx context.Context, u auth.UserInfo, capability string) (bool, error) {
	if u.AuthType == "management" {
		return true, nil
	}
	// Propagate identity so Hugr evaluates the lookup against the caller's
	// real role, not hub-service's service identity.
	q := withIdentity(ctx, u)
	res, err := a.client.Query(q,
		`{ function { core { auth { my_permissions {
			role_name disabled permissions { object field hidden disabled }
		} } } } }`, nil,
	)
	if err != nil {
		return false, fmt.Errorf("my_permissions: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return false, fmt.Errorf("my_permissions: %w", res.Err())
	}

	var perms struct {
		RoleName    string `json:"role_name"`
		Disabled    bool   `json:"disabled"`
		Permissions []struct {
			Object   string `json:"object"`
			Field    string `json:"field"`
			Hidden   bool   `json:"hidden"`
			Disabled bool   `json:"disabled"`
		} `json:"permissions"`
	}
	if err := res.ScanData("function.core.auth.my_permissions", &perms); err != nil {
		return false, fmt.Errorf("scan my_permissions: %w", err)
	}
	if perms.Disabled {
		return false, nil
	}

	// Match against the caller's role permission list, mirroring query-engine's
	// own checkObjectField logic: specific (object,field) match wins, wildcard
	// rules set the baseline, absence of a rule falls through to default-allow.
	//
	// For hub-service capabilities we want "explicit allow only": absence of a
	// rule means NOT granted. This is safer than Hugr's default because
	// capability points are always in the "hub:management" namespace, where
	// there are no schema-level defaults to fall back on.
	for _, p := range perms.Permissions {
		if p.Object == CapManagementNamespace && p.Field == capability {
			return !p.Disabled, nil
		}
	}
	return false, nil
}
