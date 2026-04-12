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
// enabled. Uses function.core.auth.check_access to evaluate against
// core.role_permissions — no local matching logic.
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
		`query($ns: String!, $f: String!) {
			function { core { auth { check_access(
				type_name: $ns, fields: $f
			) { field enabled } } } }
		}`,
		map[string]any{"ns": CapManagementNamespace, "f": capability},
	)
	if err != nil {
		return false, fmt.Errorf("check_access: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return false, fmt.Errorf("check_access: %w", res.Err())
	}

	var entries []struct {
		Field   string `json:"field"`
		Enabled bool   `json:"enabled"`
	}
	if err := res.ScanData("function.core.auth.check_access", &entries); err != nil {
		return false, fmt.Errorf("scan check_access: %w", err)
	}
	for _, e := range entries {
		if e.Field == capability {
			return e.Enabled, nil
		}
	}
	return false, nil
}
