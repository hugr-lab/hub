package hubapp

import (
	"context"
	"fmt"
	"strings"

	"github.com/hugr-lab/hugen/pkg/store/schema"
)

// RLS seed for the agent roles (hugen design 008 spec-hub-side §5 HB3), built on
// query-engine's data-object (table-level) permissions. Two row sets applied to
// the roles `agent` and `agent_template` at boot:
//
//   - the agent-store isolation floor exported by hugen's schema library
//     (schema.AgentPermissions — data-object:query/insert/update rows that
//     compose on every path: direct, _by_pk, relations, _join, aggregations);
//   - the hub platform deny set below (core.* config+secrets, the hub.db
//     platform module, the hub user-facing functions, bootstrap mint).
//
// Hugr data access is allow-by-default, which is correct for mesh + LLM sources
// (agents are data analysts) and wrong for everything hub-internal — hence the
// deny set. The agent reaches platform capabilities only through the hub MCP
// tool surface, never the platform DB directly.
//
// Seeding is fail-closed: without the floor a second agent reads the first's
// store, so a seed failure aborts Init.
const (
	agentRoleName         = "agent"
	agentTemplateRoleName = "agent_template"

	agentRoleDescription         = "hugen agent principal — RLS floor seeded by hub-service at boot (managed, do not hand-edit)"
	agentTemplateRoleDescription = "copyable base for bespoke agent roles — same managed RLS floor as `agent`; copy, then customize the copy"
)

var seededAgentRoles = []struct{ name, description string }{
	{agentRoleName, agentRoleDescription},
	{agentTemplateRoleName, agentTemplateRoleDescription},
}

// perAgentRolePrefix marks a role create_agent minted for a single agent
// (`agent:<agent_id>`). agent_id grammar excludes ':' so the prefix is an
// unambiguous "hub created + hub may delete this" marker (delete_agent drops it).
const perAgentRolePrefix = "agent:"

func perAgentRoleName(agentID string) string { return perAgentRolePrefix + agentID }

// isHubCreatedAgentRole reports whether create_agent minted the role (and so
// delete_agent may drop it, and hub owns its description).
func isHubCreatedAgentRole(role string) bool { return strings.HasPrefix(role, perAgentRolePrefix) }

// perAgentRoleDescription is stamped on a create_agent-minted role.
func perAgentRoleDescription(agentID string) string {
	return "per-agent isolation role for " + agentID +
		" — floor managed by hub-service; add capability grants with core.insert_role_permissions (do not hand-edit the floor rows)"
}

// protectedRoles are hugr platform roles the agent floor must never touch:
// flooring them (deny core.* / hub platform DB) would break the platform, so an
// agent may not be assigned one.
var protectedRoles = map[string]bool{"admin": true, "public": true, "readonly": true}

// permKey identifies one core.role_permissions row within a role (the PK is
// (role, type_name, field_name)).
type permKey struct{ TypeName, FieldName string }

// agentRoleFloorKeys is the (type_name, field_name) set hub manages on an agent
// role — exactly what agentRoleRows emits. A managed-subset (re)seed deletes
// only these keys before re-inserting, so admin grant rows on the same role
// survive. agentRoleRows keys are unique (asserted in tests), so no dedup.
func agentRoleFloorKeys() []permKey {
	rows := agentRoleRows()
	keys := make([]permKey, 0, len(rows))
	for _, r := range rows {
		keys = append(keys, permKey{r.TypeName, r.FieldName})
	}
	return keys
}

// platformDenyRows is the hub-owned deny set stacked on the hugen floor. Uses
// EXACT (type, field) denies — a `(type, *)` wildcard on a module-navigation
// field (e.g. `_module_hub_query/db`) was observed NOT to deny it, so the
// module fields are named explicitly. The agent store path (`hub.agent.db.*`
// via `_module_hub_query/agent`) and `function.core` stay allow-by-default.
func platformDenyRows() []schema.RolePermission {
	deny := func(typeName, fieldName string) schema.RolePermission {
		return schema.RolePermission{TypeName: typeName, FieldName: fieldName, Disabled: true}
	}
	rows := []schema.RolePermission{
		// core.* — roles / permissions / api_keys / data sources. Reads leak
		// secrets; writes are privilege escalation. function.core stays open
		// (cache/meta/embeddings/version).
		deny("Query", "core"),
		deny("Mutation", "core"),
		deny("MutationFunction", "core"),
		// Subscription|core stays OPEN for the agent's LLM path
		// (`subscription { core { models { chat_completion }}}`, pkg/models/hugr.go
		// — the only way to reach a model in remote mode). But SCOPE it: the core
		// subscription module (`_module_core_subscription`) also exposes `store` =
		// the always-attached core.store pub-sub (`subscribe`/`watch`), which would
		// let an agent stream hub-wide keyspace events / cross-agent pub-sub. Deny
		// that sub-field EXACTLY (type,field, not a module-nav wildcard); `models`
		// stays reachable, `store` does not.
		deny("_module_core_subscription", "store"),

		// hub platform DB (users / user_agents / projects / chats / budgets /
		// bootstrap secrets) — the agent reaches platform data only via the hub
		// MCP tool surface, never directly. `agent` (the store) stays open.
		deny("_module_hub_query", "db"),
		deny("_module_hub_mutation", "db"),

		// hub agent-lifecycle mutations — admin only.
		deny("_module_hub_mut_function", "start_agent"),
		deny("_module_hub_mut_function", "stop_agent"),
		deny("_module_hub_mut_function", "delete_agent"),

		// hub agent-provisioning mutations (HB2) — admin only. An agent minting
		// or editing an identity (its own role especially) is privilege
		// escalation; the handlers also enforce, this is the RLS floor.
		deny("_module_hub_mut_function", "create_agent"),
		deny("_module_hub_mut_function", "update_agent"),
		deny("_module_hub_mut_function", "disable_agent"),

		// bootstrap-secret mint — admin only (handler also enforces).
		// function.hub.agent.info stays open — it is the agent identity call.
		deny("_module_hub_agent_mut_function", "bootstrap_token"),

		// HB5 management plane (handlers_chats.go) — user-facing thread/project
		// organization + admin access management. The agent reaches platform
		// capabilities only via the hub MCP tool surface; every one of these is
		// denied at the floor (handlers also enforce ownership/admin).
		deny("_module_hub_query", "my_chats"),
		deny("_module_hub_query", "my_projects"),
		deny("_module_hub_query", "agent_access"),
		deny("_module_hub_mut_function", "create_chat"),
		deny("_module_hub_mut_function", "update_chat"),
		deny("_module_hub_mut_function", "delete_chat"),
		deny("_module_hub_mut_function", "create_project"),
		deny("_module_hub_mut_function", "update_project"),
		deny("_module_hub_mut_function", "delete_project"),
		deny("_module_hub_mut_function", "grant_agent_access"),
		deny("_module_hub_mut_function", "revoke_agent_access"),
	}
	// Data-object denies on every PLATFORM type. The `_module_hub_query/db`
	// deny above only fires on the module-navigation path (`hub.db.*`); a
	// platform type reached via a cross-source relation (e.g. an HB-EXT `@join`
	// from an Agent DB type, or a forward ref off an already-visible platform
	// row) bypasses it, because per-type access is allow-by-default and the
	// agent store seeds data-object rows ONLY for `hub_agent_db_*`. A disabled
	// `data-object:query` row IS enforced on every path (it composes through
	// relations/joins), so it closes any current or future platform read path.
	// The agent reaches platform data only via the hub MCP tool surface;
	// `function.hub.agent.info` (config in remote mode) stays open.
	for _, t := range []string{
		"hub_db_users", "hub_db_agent_types", "hub_db_agents", "hub_db_user_agents",
		"hub_db_projects", "hub_db_chats", "hub_db_llm_budgets", "hub_db_agent_bootstrap_secrets",
	} {
		rows = append(rows, deny("data-object:query", t))
	}
	return rows
}

// agentRoleRows is the full managed row set stamped onto each seeded role.
func agentRoleRows() []schema.RolePermission {
	return append(schema.AgentPermissions(), platformDenyRows()...)
}

// seedAgentRoles applies the managed isolation floor at boot. Two parts:
//
//   - the base roles (`agent` shared default + `agent_template` floor base) are
//     seeded fail-closed — without them a created agent has no role to fall back
//     to and no template to build on;
//   - a best-effort reconcile re-applies the floor (managed-subset) to every
//     distinct role a live agent identity references, so a floor-schema change
//     propagates to per-agent roles without recreating each agent.
//
// Reconcile is managed-subset (agent grant rows survive) and skips protected
// built-ins. It is best-effort: the hard floor is applied at create_agent time,
// this only repairs drift.
func (a *HubApp) seedAgentRoles(ctx context.Context) error {
	for _, role := range seededAgentRoles {
		if err := a.ensureAgentRoleFloor(ctx, role.name, role.description); err != nil {
			return fmt.Errorf("seed base role %s: %w", role.name, err)
		}
	}

	roles, err := a.liveAgentRoles(ctx)
	if err != nil {
		// The agent store may be empty or mid-provision at boot — non-fatal.
		a.logger.Warn("agent-role reconcile skipped: enumerate live roles failed", "error", err)
	}
	reconciled := 0
	for _, role := range roles {
		if role == agentRoleName || role == agentTemplateRoleName {
			continue // base roles already seeded above
		}
		if protectedRoles[role] {
			// A live agent sitting on a protected platform role is a
			// misconfiguration (it is over-privileged, and the floor cannot be
			// applied without breaking the platform) — surface it, don't floor it.
			a.logger.Warn("agent on a protected platform role — not floored (over-privileged)", "role", role)
			continue
		}
		if err := a.ensureAgentRoleFloor(ctx, role, perAgentRoleReconcileDesc(role)); err != nil {
			a.logger.Warn("agent-role floor reconcile failed", "role", role, "error", err)
			continue
		}
		reconciled++
	}

	if err := a.invalidateRolePermCache(ctx); err != nil {
		return err
	}
	a.logger.Info("agent role RLS floor seeded",
		"base_roles", []string{agentRoleName, agentTemplateRoleName},
		"reconciled_per_agent_roles", reconciled)
	return nil
}

// perAgentRoleReconcileDesc supplies a description used only when reconcile has
// to CREATE a hub-owned role that vanished; for admin-named roles the value is
// ignored (ensureRoleExists leaves an existing role's description untouched).
func perAgentRoleReconcileDesc(role string) string {
	if isHubCreatedAgentRole(role) {
		return perAgentRoleDescription(strings.TrimPrefix(role, perAgentRolePrefix))
	}
	return "agent role — isolation floor managed by hub-service"
}

// ensureAgentRoleFloor guarantees the isolation floor (agentRoleRows) is present
// on `role`, managed-subset: it replaces ONLY hub's floor rows (keyed by
// (type_name, field_name)) and never touches admin-added grant rows — so an
// admin layers data-source / function access with ordinary
// core.insert_role_permissions mutations and the floor coexists. Creates the
// role if absent. Protected platform roles are refused (flooring them would
// break the platform). The caller invalidates the $role_permissions cache once.
func (a *HubApp) ensureAgentRoleFloor(ctx context.Context, role, description string) error {
	if protectedRoles[role] {
		return fmt.Errorf("role %q is a protected platform role — cannot be an agent role", role)
	}
	// hub owns the description of its own roles (base + agent:<id>); it must not
	// rewrite the description of an admin-named role it is only flooring.
	if role == agentRoleName || role == agentTemplateRoleName || isHubCreatedAgentRole(role) {
		if err := a.ensureRole(ctx, role, description); err != nil {
			return fmt.Errorf("ensure role %s: %w", role, err)
		}
	} else {
		if err := a.ensureRoleExists(ctx, role, description); err != nil {
			return fmt.Errorf("ensure role %s: %w", role, err)
		}
	}
	if err := a.deleteFloorRows(ctx, role); err != nil {
		return fmt.Errorf("clear floor on %s: %w", role, err)
	}
	rows := agentRoleRows()
	if err := a.insertRoleRows(ctx, role, rows); err != nil {
		return fmt.Errorf("seed floor on %s: %w", role, err)
	}
	if err := a.verifyFloor(ctx, role, rows); err != nil {
		return fmt.Errorf("verify floor on %s: %w", role, err)
	}
	return nil
}

// liveAgentRoles returns the distinct non-empty `role` values across all agent
// identities in the store — the reconcile target set.
func (a *HubApp) liveAgentRoles(ctx context.Context) ([]string, error) {
	res, err := a.client.Query(ctx,
		`{ hub { agent { db { agents { role } } } } }`, nil)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	if res.Err() != nil {
		return nil, res.Err()
	}
	var rows []struct {
		Role string `json:"role"`
	}
	if err := res.ScanData("hub.agent.db.agents", &rows); err != nil && !isNoData(err) {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		if r.Role != "" && !seen[r.Role] {
			seen[r.Role] = true
			out = append(out, r.Role)
		}
	}
	return out, nil
}

const deleteFloorRowsChunk = 50

// deleteFloorRows removes exactly hub's floor rows from `role` (managed-subset)
// via exact (type_name, field_name) matches — NOT a type_name `in` list, which
// would also delete an admin grant that happens to share a type_name (e.g. a
// data-object:query grant on a source table).
func (a *HubApp) deleteFloorRows(ctx context.Context, role string) error {
	keys := agentRoleFloorKeys()
	for start := 0; start < len(keys); start += deleteFloorRowsChunk {
		end := start + deleteFloorRowsChunk
		if end > len(keys) {
			end = len(keys)
		}
		query, vars := buildFloorDeleteDoc(role, keys[start:end])
		res, err := a.client.Query(ctx, query, vars)
		if err != nil {
			return fmt.Errorf("chunk at %d: %w", start, err)
		}
		res.Close()
		if res.Err() != nil {
			return fmt.Errorf("chunk at %d: %w", start, res.Err())
		}
	}
	return nil
}

// buildFloorDeleteDoc assembles one alias-batched delete_role_permissions
// document — one exact-key delete per floor row.
func buildFloorDeleteDoc(role string, keys []permKey) (string, map[string]any) {
	var decl, body strings.Builder
	vars := map[string]any{"role": role}
	decl.WriteString("$role: String!")
	for i, k := range keys {
		tn := fmt.Sprintf("t%d", i)
		fn := fmt.Sprintf("f%d", i)
		fmt.Fprintf(&decl, ", $%s: String!, $%s: String!", tn, fn)
		fmt.Fprintf(&body,
			" d%d: delete_role_permissions(filter: {role: {eq: $role}, type_name: {eq: $%s}, field_name: {eq: $%s}}) { affected_rows }",
			i, tn, fn)
		vars[tn] = k.TypeName
		vars[fn] = k.FieldName
	}
	return fmt.Sprintf("mutation(%s) { core {%s } }", decl.String(), body.String()), vars
}

// verifyFloor confirms every floor row is present on `role` after the seed
// (defence against a partial insert leaving an under-isolated agent).
func (a *HubApp) verifyFloor(ctx context.Context, role string, want []schema.RolePermission) error {
	res, err := a.client.Query(ctx,
		`query($role: String!) { core { roles_by_pk(name: $role) { permissions { type_name field_name } } } }`,
		map[string]any{"role": role},
	)
	if err != nil {
		return err
	}
	defer res.Close()
	if res.Err() != nil {
		return res.Err()
	}
	var role_ struct {
		Permissions []struct {
			TypeName  string `json:"type_name"`
			FieldName string `json:"field_name"`
		} `json:"permissions"`
	}
	if err := res.ScanData("core.roles_by_pk", &role_); err != nil && !isNoData(err) {
		return err
	}
	have := make(map[permKey]bool, len(role_.Permissions))
	for _, p := range role_.Permissions {
		have[permKey{p.TypeName, p.FieldName}] = true
	}
	for _, w := range want {
		if !have[permKey{w.TypeName, w.FieldName}] {
			return fmt.Errorf("floor row %s/%s missing after seed", w.TypeName, w.FieldName)
		}
	}
	return nil
}

// invalidateRolePermCache clears the $role_permissions-tagged caches so a
// reseeded floor takes effect without a restart.
func (a *HubApp) invalidateRolePermCache(ctx context.Context) error {
	res, err := a.client.Query(ctx,
		`{ function { core { cache { invalidate(tags: ["$role_permissions"]) { success } } } } }`, nil)
	if err != nil {
		return fmt.Errorf("invalidate role permission cache: %w", err)
	}
	res.Close()
	if res.Err() != nil {
		return fmt.Errorf("invalidate role permission cache: %w", res.Err())
	}
	return nil
}

// maybeDeleteAgentRole drops a hub-created per-agent role (agent:<id>) and its
// permissions once no agent references it — called after delete_agent removes an
// agent, or after update_agent moves an agent onto a different role. Admin-named
// and base/shared roles are left intact. Best-effort: a failure only leaks an
// unused role, never touches a live agent.
func (a *HubApp) maybeDeleteAgentRole(ctx context.Context, role string) {
	if !isHubCreatedAgentRole(role) {
		return
	}
	inUse, err := a.agentRoleInUse(ctx, role)
	if err != nil {
		a.logger.Warn("agent-role cleanup skipped: usage check failed", "role", role, "error", err)
		return
	}
	if inUse {
		return
	}
	if err := a.deleteRoleAndPerms(ctx, role); err != nil {
		a.logger.Warn("agent-role cleanup failed", "role", role, "error", err)
		return
	}
	a.logger.Info("per-agent role deleted", "role", role)
}

// agentRoleInUse reports whether any agent identity still references `role`.
func (a *HubApp) agentRoleInUse(ctx context.Context, role string) (bool, error) {
	res, err := a.client.Query(ctx,
		`query($role: String!) { hub { agent { db { agents(filter: {role: {eq: $role}}, limit: 1) { id } } } } }`,
		map[string]any{"role": role},
	)
	if err != nil {
		return false, err
	}
	defer res.Close()
	if res.Err() != nil {
		return false, res.Err()
	}
	var rows []struct {
		ID string `json:"id"`
	}
	if err := res.ScanData("hub.agent.db.agents", &rows); err != nil && !isNoData(err) {
		return false, err
	}
	return len(rows) > 0, nil
}

// deleteRoleAndPerms removes a role and all its core.permissions, then
// invalidates the permission cache. Permissions are deleted first (a mutation
// runs its selections in order).
func (a *HubApp) deleteRoleAndPerms(ctx context.Context, role string) error {
	res, err := a.client.Query(ctx,
		`mutation($role: String!) { core {
			delete_role_permissions(filter: {role: {eq: $role}}) { affected_rows }
			delete_roles(filter: {name: {eq: $role}}) { affected_rows }
		} }`,
		map[string]any{"role": role},
	)
	if err != nil {
		return err
	}
	res.Close()
	if res.Err() != nil {
		return res.Err()
	}
	return a.invalidateRolePermCache(ctx)
}

func (a *HubApp) ensureRole(ctx context.Context, name, description string) error {
	res, err := a.client.Query(ctx,
		`query($name: String!) { core { roles(filter: {name: {eq: $name}}) { name } } }`,
		map[string]any{"name": name},
	)
	if err != nil {
		return err
	}
	defer res.Close()
	if res.Err() != nil {
		return res.Err()
	}
	var existing []struct {
		Name string `json:"name"`
	}
	_ = res.ScanData("core.roles", &existing)
	if len(existing) > 0 {
		resU, err := a.client.Query(ctx,
			`mutation($name: String!, $desc: String!) {
				core { update_roles(filter: {name: {eq: $name}}, data: {description: $desc}) { affected_rows } }
			}`,
			map[string]any{"name": name, "desc": description},
		)
		if err != nil {
			return err
		}
		resU.Close()
		return resU.Err()
	}

	res2, err := a.client.Query(ctx,
		`mutation($name: String!, $desc: String!) {
			core { insert_roles(data: {name: $name, description: $desc, disabled: false}) { name } }
		}`,
		map[string]any{"name": name, "desc": description},
	)
	if err != nil {
		return err
	}
	defer res2.Close()
	if res2.Err() != nil {
		return res2.Err()
	}
	a.logger.Info("agent role created", "role", name)
	return nil
}

// ensureRoleExists creates `role` with `description` if it is absent and leaves
// an existing role UNTOUCHED — including its description. Used when hub only
// needs to guarantee an admin-named role is present to floor it, without
// clobbering a description the admin owns (contrast ensureRole, which refreshes
// the description of hub's own roles).
func (a *HubApp) ensureRoleExists(ctx context.Context, role, description string) error {
	res, err := a.client.Query(ctx,
		`query($name: String!) { core { roles(filter: {name: {eq: $name}}) { name } } }`,
		map[string]any{"name": role},
	)
	if err != nil {
		return err
	}
	var existing []struct {
		Name string `json:"name"`
	}
	_ = res.ScanData("core.roles", &existing)
	closeErr := res.Err()
	res.Close()
	if closeErr != nil {
		return closeErr
	}
	if len(existing) > 0 {
		return nil
	}
	res2, err := a.client.Query(ctx,
		`mutation($name: String!, $desc: String!) {
			core { insert_roles(data: {name: $name, description: $desc, disabled: false}) { name } }
		}`,
		map[string]any{"name": role, "desc": description},
	)
	if err != nil {
		return err
	}
	defer res2.Close()
	if res2.Err() != nil {
		return res2.Err()
	}
	a.logger.Info("agent role created", "role", role)
	return nil
}

const insertRoleRowsChunk = 50

func (a *HubApp) insertRoleRows(ctx context.Context, role string, rows []schema.RolePermission) error {
	for start := 0; start < len(rows); start += insertRoleRowsChunk {
		end := start + insertRoleRowsChunk
		if end > len(rows) {
			end = len(rows)
		}
		query, vars := buildRoleRowsInsert(role, rows[start:end])
		res, err := a.client.Query(ctx, query, vars)
		if err != nil {
			return fmt.Errorf("chunk at %d: %w", start, err)
		}
		res.Close()
		if res.Err() != nil {
			return fmt.Errorf("chunk at %d: %w", start, res.Err())
		}
	}
	return nil
}

// buildRoleRowsInsert assembles one alias-batched insert_role_permissions
// document (the mutation takes a single row per call).
func buildRoleRowsInsert(role string, rows []schema.RolePermission) (string, map[string]any) {
	var decl, body strings.Builder
	vars := make(map[string]any, len(rows))
	for i, r := range rows {
		name := fmt.Sprintf("d%d", i)
		if i > 0 {
			decl.WriteString(", ")
		}
		fmt.Fprintf(&decl, "$%s: core_role_permissions_mut_input_data!", name)
		fmt.Fprintf(&body, " r%d: insert_role_permissions(data: $%s) { role }", i, name)
		data := map[string]any{
			"role":       role,
			"type_name":  r.TypeName,
			"field_name": r.FieldName,
			"disabled":   r.Disabled,
			"hidden":     false,
		}
		if r.Filter != nil {
			data["filter"] = r.Filter
		}
		if r.Data != nil {
			data["data"] = r.Data
		}
		vars[name] = data
	}
	return fmt.Sprintf("mutation(%s) { core {%s } }", decl.String(), body.String()), vars
}

