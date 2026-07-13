package hubapp

// SK5 — skills-marketplace capability tooling (spec-skills-distribution §4/§6).
//
// The authority for both the publish gate and the capability (install/visibility)
// gate is hugr `core.role_permissions` rows. Enabling an agent therefore means
// flipping a row on its role AND invalidating the $role_permissions cache — a
// two-step dance that is easy to get wrong by hand (a dropped cache-invalidate
// leaves the grant inert until the next restart). These admin functions bundle
// both steps behind one call:
//
//	grant_skill_capability(hugr_role, capability)   → +positive (hugen:skill:capability, capability)
//	revoke_skill_capability(hugr_role, capability)  → drop that row
//	set_skill_publish(hugr_role, enabled)           → flip (hugen:skill, publish) enabled/disabled
//
// They are admin-only (hub:management.admin) and operate on any hugr role — a
// per-agent `agent:<id>`, the shared `agent` role, or an admin-named role. The
// capability grant is a POSITIVE row the deny-by-default capability gate
// (callerHasCaps, skills_marketplace.go) reads; set_skill_publish flips the
// floor's publish deny (create_agent seeds `(hugen:skill, publish)` disabled).

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hugr-lab/query-engine/client/app"
)

// skillPermType is the return shape of the SK5 grant/revoke/publish functions.
func skillPermType() app.Type {
	return app.Struct("skill_permission").
		Desc("A skills-marketplace permission row after a grant/revoke/publish change.").
		Field("role", app.String).
		Field("permission", app.String).
		Field("enabled", app.Boolean).
		AsType()
}

// skillCapabilityKey is the role_permissions key of a capability grant.
func skillCapabilityKey(capability string) permKey {
	return permKey{TypeName: capabilitySkillNamespace, FieldName: capability}
}

// skillPublishKey is the role_permissions key of the publish grant.
func skillPublishKey() permKey {
	return permKey{TypeName: skillPublishNamespace, FieldName: skillPublishField}
}

// registerSkillsAdminMutations wires the SK5 capability tooling into the
// `default` module, next to the identity-CRUD provisioning functions.
func (a *HubApp) registerSkillsAdminMutations() error {
	if err := a.mux.HandleFunc("default", "grant_skill_capability", a.handleGrantSkillCapability,
		app.Arg("hugr_role", app.String),
		app.Arg("capability", app.String),
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
		app.Return(skillPermType()),
		app.Mutation(),
		app.Desc("Grant a skills-marketplace capability to a hugr role: adds a positive core.role_permissions row (hugen:skill:capability, <capability>) so the role passes the deny-by-default capability gate for skills that declare metadata.hugen.required_capabilities=[<capability>], then invalidates the permission cache. hugr_role is any role (a per-agent 'agent:<id>', the shared 'agent', or an admin-named role); capability is the exact declared name. Idempotent. Returns {role, permission, enabled}. Admin only."),
	); err != nil {
		return err
	}

	if err := a.mux.HandleFunc("default", "revoke_skill_capability", a.handleRevokeSkillCapability,
		app.Arg("hugr_role", app.String),
		app.Arg("capability", app.String),
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
		app.Return(skillPermType()),
		app.Mutation(),
		app.Desc("Revoke a skills-marketplace capability from a hugr role: drops the (hugen:skill:capability, <capability>) row and invalidates the permission cache. Deny-by-default, so a missing row already denies — this makes revocation explicit. Idempotent (a no-op if the row is absent). Returns {role, permission, enabled:false}. Admin only."),
	); err != nil {
		return err
	}

	if err := a.mux.HandleFunc("default", "set_skill_publish", a.handleSetSkillPublish,
		app.Arg("hugr_role", app.String),
		app.Arg("enabled", app.Boolean),
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
		app.Return(skillPermType()),
		app.Mutation(),
		app.Desc("Enable or disable skill publishing for a hugr role by flipping the (hugen:skill, publish) row and invalidating the permission cache. Publishing to the shared catalog is denied by default (create_agent floors an agent role with this row disabled); enabled=true lets that one agent publish, enabled=false restores the deny. Admin (a protected, unfloored role) publishes without this. Returns {role, permission, enabled}. Admin only."),
	); err != nil {
		return err
	}

	return nil
}

// handleGrantSkillCapability adds a positive capability grant to a role (admin).
func (a *HubApp) handleGrantSkillCapability(w *app.Result, r *app.Request) error {
	role, capability, err := a.skillCapArgs(w, r)
	if err != nil {
		return err
	}
	// Privileged platform op → service principal (r.Context()).
	if err := a.setRolePermission(r.Context(), role, skillCapabilityKey(capability), false); err != nil {
		return fmt.Errorf("grant capability %q to %q: %w", capability, role, err)
	}
	a.logger.Info("skill capability granted", "role", role, "capability", capability, "by", userFromArgs(r).ID)
	return w.SetJSON(map[string]any{
		"role":       role,
		"permission": capabilitySkillNamespace + "." + capability,
		"enabled":    true,
	})
}

// handleRevokeSkillCapability drops a capability grant from a role (admin).
func (a *HubApp) handleRevokeSkillCapability(w *app.Result, r *app.Request) error {
	role, capability, err := a.skillCapArgs(w, r)
	if err != nil {
		return err
	}
	if err := a.clearRolePermission(r.Context(), role, skillCapabilityKey(capability)); err != nil {
		return fmt.Errorf("revoke capability %q from %q: %w", capability, role, err)
	}
	a.logger.Info("skill capability revoked", "role", role, "capability", capability, "by", userFromArgs(r).ID)
	return w.SetJSON(map[string]any{
		"role":       role,
		"permission": capabilitySkillNamespace + "." + capability,
		"enabled":    false,
	})
}

// handleSetSkillPublish enables/disables publishing for a role (admin).
func (a *HubApp) handleSetSkillPublish(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	ctx := withIdentity(r.Context(), u)
	if err := a.requireAdmin(ctx, u); err != nil {
		return err
	}
	role := strings.TrimSpace(r.String("hugr_role"))
	if role == "" {
		return errors.New("hugr_role is required")
	}
	enabled := r.Bool("enabled")
	// disabled row = !enabled; delete-then-insert flips the floor's publish deny.
	if err := a.setRolePermission(r.Context(), role, skillPublishKey(), !enabled); err != nil {
		return fmt.Errorf("set publish=%v on %q: %w", enabled, role, err)
	}
	a.logger.Info("skill publish set", "role", role, "enabled", enabled, "by", u.ID)
	return w.SetJSON(map[string]any{
		"role":       role,
		"permission": skillPublishNamespace + "." + skillPublishField,
		"enabled":    enabled,
	})
}

// skillCapArgs verifies admin + reads the (hugr_role, capability) args shared by
// grant/revoke.
func (a *HubApp) skillCapArgs(_ *app.Result, r *app.Request) (role, capability string, err error) {
	u := userFromArgs(r)
	if err = requireUser(u); err != nil {
		return "", "", err
	}
	ctx := withIdentity(r.Context(), u)
	if err = a.requireAdmin(ctx, u); err != nil {
		return "", "", err
	}
	role = strings.TrimSpace(r.String("hugr_role"))
	if role == "" {
		return "", "", errors.New("hugr_role is required")
	}
	capability = strings.TrimSpace(r.String("capability"))
	if capability == "" {
		return "", "", errors.New("capability is required")
	}
	return role, capability, nil
}
