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
		// (cache/meta/embeddings/version). Subscription|core stays OPEN too: the
		// agent's LLM access is `subscription { core { models { chat_completion }}}`
		// (pkg/models/hugr.go) — the agent's only path to a model in remote mode,
		// same model-access category as the open function.core.models.embedding.
		deny("Query", "core"),
		deny("Mutation", "core"),
		deny("MutationFunction", "core"),

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

// seedAgentRoles applies the managed RLS row set. Idempotent and authoritative:
// existing rows of the two roles are replaced wholesale each boot (drift-proof).
func (a *HubApp) seedAgentRoles(ctx context.Context) error {
	rows := agentRoleRows()

	for _, role := range seededAgentRoles {
		if err := a.ensureRole(ctx, role.name, role.description); err != nil {
			return fmt.Errorf("ensure role %s: %w", role.name, err)
		}
	}

	res, err := a.client.Query(ctx,
		`mutation($roles: [String!]) { core { delete_role_permissions(filter: {role: {in: $roles}}) { affected_rows } } }`,
		map[string]any{"roles": []string{agentRoleName, agentTemplateRoleName}},
	)
	if err != nil {
		return fmt.Errorf("wipe role permissions: %w", err)
	}
	res.Close()
	if res.Err() != nil {
		return fmt.Errorf("wipe role permissions: %w", res.Err())
	}

	for _, role := range seededAgentRoles {
		if err := a.insertRoleRows(ctx, role.name, rows); err != nil {
			return fmt.Errorf("seed rows for role %s: %w", role.name, err)
		}
		n, err := a.countRoleRows(ctx, role.name)
		if err != nil {
			return fmt.Errorf("verify rows for role %s: %w", role.name, err)
		}
		if n != len(rows) {
			return fmt.Errorf("role %s: seeded %d rows, store reports %d", role.name, len(rows), n)
		}
	}

	// Permission caches are tagged `$role_permissions`; tag-based invalidation
	// clears every role's cached permissions so the new floor takes effect.
	res, err = a.client.Query(ctx,
		`{ function { core { cache { invalidate(tags: ["$role_permissions"]) { success } } } } }`, nil)
	if err != nil {
		return fmt.Errorf("invalidate role permission cache: %w", err)
	}
	res.Close()
	if res.Err() != nil {
		return fmt.Errorf("invalidate role permission cache: %w", res.Err())
	}

	a.logger.Info("agent role RLS floor seeded",
		"roles", []string{agentRoleName, agentTemplateRoleName}, "rows_per_role", len(rows))
	return nil
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

func (a *HubApp) countRoleRows(ctx context.Context, role string) (int, error) {
	res, err := a.client.Query(ctx,
		`query($role: String!) { core { role_permissions_aggregation(filter: {role: {eq: $role}}) { _rows_count } } }`,
		map[string]any{"role": role},
	)
	if err != nil {
		return 0, err
	}
	defer res.Close()
	if res.Err() != nil {
		return 0, res.Err()
	}
	var agg struct {
		Count int `json:"_rows_count"`
	}
	if err := res.ScanData("core.role_permissions_aggregation", &agg); err != nil {
		return 0, err
	}
	return agg.Count, nil
}
