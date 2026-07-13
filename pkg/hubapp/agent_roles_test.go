package hubapp

import (
	"strings"
	"testing"

	"github.com/hugr-lab/hugen/pkg/store/schema"
)

// The agent-role seed is the RLS floor for every hub-run agent; these tests pin
// the row set so a regression (a missing table filter, a dropped platform deny,
// a duplicate) can't ship silently. The data-object COMPOSITION itself (the
// filter holding on relations/_join/aggregations) is covered by query-engine's
// permissions/data_object_filter e2e; here we assert the seed is complete.

const doPrefix = "data-object:"

func rowKey(r schema.RolePermission) string { return r.TypeName + "|" + r.FieldName }

func TestAgentRoleRows_NoDuplicatesAndWellFormed(t *testing.T) {
	rows := agentRoleRows()
	seen := map[string]bool{}
	for _, r := range rows {
		k := rowKey(r)
		if seen[k] {
			t.Errorf("duplicate permission row %q", k)
		}
		seen[k] = true

		// A data-object row carries exactly one of filter / data / disabled.
		if strings.HasPrefix(r.TypeName, doPrefix) {
			n := 0
			if r.Filter != nil {
				n++
			}
			if r.Data != nil {
				n++
			}
			if r.Disabled {
				n++
			}
			if n != 1 {
				t.Errorf("data-object row %q must carry exactly one of filter/data/disabled, got %d", k, n)
			}
		}
	}
	if len(rows) != len(seen) {
		t.Errorf("row count %d != unique keys %d", len(rows), len(seen))
	}
}

func TestAgentRoleRows_StoreFloorPresent(t *testing.T) {
	seen := map[string]schema.RolePermission{}
	for _, r := range agentRoleRows() {
		seen[rowKey(r)] = r
	}

	// Every agent-scoped table: read filter on agent_id + insert/update stamp.
	for _, tbl := range []string{
		"sessions", "session_events", "session_notes", "tool_policies",
		"tasks", "task_log", "skills", "skill_log", "skill_links",
	} {
		typ := "hub_agent_db_" + tbl
		if q, ok := seen["data-object:query|"+typ]; !ok || q.Filter["agent_id"] == nil {
			t.Errorf("%s: missing data-object:query agent_id filter", tbl)
		}
		for _, op := range []string{"data-object:insert", "data-object:update"} {
			if s, ok := seen[op+"|"+typ]; !ok || s.Data["agent_id"] == nil {
				t.Errorf("%s: missing %s agent_id stamp", tbl, op)
			}
		}
	}

	// agents: scoped by its own PK; identity mutations denied.
	if a, ok := seen["data-object:query|hub_agent_db_agents"]; !ok || a.Filter["id"] == nil {
		t.Error("agents must be data-object:query scoped on id (PK == agent)")
	}
	for _, op := range []string{"data-object:insert", "data-object:delete"} {
		if r, ok := seen[op+"|hub_agent_db_agents"]; !ok || !r.Disabled {
			t.Errorf("agents %s must be denied", op)
		}
	}
	// agent_types / version: mutations denied, reads open (no query row).
	for _, tbl := range []string{"agent_types", "version"} {
		for _, op := range []string{"data-object:insert", "data-object:update", "data-object:delete"} {
			if r, ok := seen[op+"|hub_agent_db_"+tbl]; !ok || !r.Disabled {
				t.Errorf("%s %s must be denied", tbl, op)
			}
		}
		if _, ok := seen["data-object:query|hub_agent_db_"+tbl]; ok {
			t.Errorf("%s must stay readable (no data-object:query row)", tbl)
		}
	}
}

func TestAgentRoleRows_PlatformDeniesPresent(t *testing.T) {
	seen := map[string]schema.RolePermission{}
	for _, r := range platformDenyRows() {
		seen[rowKey(r)] = r
	}
	// Every platform deny must be a disabled exact (type, field) row — NOT a
	// wildcard: a (type,*) wildcard on a module-navigation field (e.g.
	// _module_hub_query/db) was observed NOT to deny it.
	for _, key := range []string{
		"Query|core", "Mutation|core", "MutationFunction|core",
		"_module_hub_query|db", "_module_hub_mutation|db",
		"_module_hub_mut_function|start_agent", "_module_hub_mut_function|stop_agent",
		"_module_hub_mut_function|delete_agent",
		"_module_hub_mut_function|create_agent", "_module_hub_mut_function|update_agent",
		"_module_hub_mut_function|disable_agent",
		"_module_hub_agent_mut_function|bootstrap_token",
		// Subscription|core stays open for models.chat_completion, but core.store
		// (pub-sub subscribe/watch) is scoped out with an exact sub-field deny.
		"_module_core_subscription|store",
		// HB5 management plane — agents never touch threads/projects/grants.
		"_module_hub_query|my_chats", "_module_hub_query|my_projects",
		"_module_hub_query|agent_access",
		"_module_hub_mut_function|create_chat", "_module_hub_mut_function|update_chat",
		"_module_hub_mut_function|delete_chat",
		"_module_hub_mut_function|create_project", "_module_hub_mut_function|update_project",
		"_module_hub_mut_function|delete_project",
		"_module_hub_mut_function|grant_agent_access", "_module_hub_mut_function|revoke_agent_access",
		// hugen skills marketplace: publishing to the shared catalog is denied by
		// default (deny-by-default trust boundary); admin enables per agent.
		"hugen:skill|publish",
	} {
		r, ok := seen[key]
		if !ok {
			t.Errorf("missing platform deny %q", key)
			continue
		}
		if !r.Disabled {
			t.Errorf("platform row %q must be disabled", key)
		}
		if r.FieldName == "*" {
			t.Errorf("platform deny %q uses a field wildcard — use an exact field", key)
		}
	}
	// The agent store path, agent identity, and the LLM subscription must NOT be
	// denied here. Subscription|core is the agent's chat_completion path — denying
	// it (as an earlier HB3 seed did) breaks every model call in remote mode.
	for _, key := range []string{"_module_hub_query|agent", "_module_hub_agent_mut_function|info", "Subscription|core"} {
		if _, ok := seen[key]; ok {
			t.Errorf("%q must stay open (agent store / identity / LLM subscription)", key)
		}
	}

	// Every platform type carries a disabled data-object:query row — a
	// module-nav deny is bypassed by a relation/join, so per-type denies are
	// what actually keep an agent out of the platform DB on every path.
	for _, typ := range []string{
		"hub_db_users", "hub_db_agent_types", "hub_db_agents", "hub_db_user_agents",
		"hub_db_projects", "hub_db_chats", "hub_db_llm_budgets", "hub_db_agent_bootstrap_secrets",
	} {
		r, ok := seen["data-object:query|"+typ]
		if !ok || !r.Disabled {
			t.Errorf("platform type %s must have a disabled data-object:query deny", typ)
		}
	}
}

func TestPerAgentRoleName_AndRecognition(t *testing.T) {
	if got := perAgentRoleName("sales-bot"); got != "agent:sales-bot" {
		t.Fatalf("perAgentRoleName = %q, want agent:sales-bot", got)
	}
	if !isHubCreatedAgentRole("agent:sales-bot") {
		t.Error("agent:<id> must be recognised as hub-created")
	}
	// The shared base role and admin-named roles are NOT hub-created (delete_agent
	// must leave them intact).
	for _, r := range []string{"agent", "agent_template", "analyst", "admin", ""} {
		if isHubCreatedAgentRole(r) {
			t.Errorf("%q must not be recognised as a hub-created per-agent role", r)
		}
	}
}

func TestProtectedRoles_CoverPlatformBuiltins(t *testing.T) {
	for _, r := range []string{"admin", "public", "readonly"} {
		if !protectedRoles[r] {
			t.Errorf("%q must be protected (flooring it would break the platform)", r)
		}
	}
	for _, r := range []string{"agent", "agent_template", "agent:x", "user"} {
		if protectedRoles[r] {
			t.Errorf("%q must not be protected — it can be an agent role", r)
		}
	}
}

func TestAgentRoleFloorKeys_MatchRowsExactly(t *testing.T) {
	rows := agentRoleRows()
	keys := agentRoleFloorKeys()
	if len(keys) != len(rows) {
		t.Fatalf("floor keys %d != rows %d", len(keys), len(rows))
	}
	rowSet := map[permKey]bool{}
	for _, r := range rows {
		rowSet[permKey{r.TypeName, r.FieldName}] = true
	}
	seen := map[permKey]bool{}
	for _, k := range keys {
		if !rowSet[k] {
			t.Errorf("floor key %v has no matching row", k)
		}
		if seen[k] {
			t.Errorf("duplicate floor key %v", k)
		}
		seen[k] = true
	}
}

func TestBuildFloorDeleteDoc_ExactKeyMatchesOnly(t *testing.T) {
	keys := []permKey{
		{"data-object:query", "hub_agent_db_sessions"},
		{"Query", "core"},
	}
	query, vars := buildFloorDeleteDoc("agent:x", keys)
	// One aliased delete per key, each an EXACT (type_name, field_name) match —
	// never a type_name `in` list (which would also drop an admin grant sharing a
	// type_name).
	for _, want := range []string{
		"$role: String!",
		"d0: delete_role_permissions(filter: {role: {eq: $role}, type_name: {eq: $t0}, field_name: {eq: $f0}})",
		"d1: delete_role_permissions(filter: {role: {eq: $role}, type_name: {eq: $t1}, field_name: {eq: $f1}})",
	} {
		if !strings.Contains(query, want) {
			t.Fatalf("delete doc missing %q:\n%s", want, query)
		}
	}
	if vars["role"] != "agent:x" ||
		vars["t0"] != "data-object:query" || vars["f0"] != "hub_agent_db_sessions" ||
		vars["t1"] != "Query" || vars["f1"] != "core" {
		t.Fatalf("delete vars wrong: %v", vars)
	}
}

func TestBuildRoleRowsInsert_CarriesFilterAndData(t *testing.T) {
	// Pick a query row (has filter) and an insert row (has data) to prove both
	// JSON args reach the mutation variables.
	var q, ins schema.RolePermission
	for _, r := range agentRoleRows() {
		if r.TypeName == "data-object:query" && r.FieldName == "hub_agent_db_sessions" {
			q = r
		}
		if r.TypeName == "data-object:insert" && r.FieldName == "hub_agent_db_sessions" {
			ins = r
		}
	}
	query, vars := buildRoleRowsInsert("agent", []schema.RolePermission{q, ins})
	if !strings.Contains(query, "$d0: core_role_permissions_mut_input_data!") ||
		!strings.Contains(query, "r1: insert_role_permissions(data: $d1)") {
		t.Fatalf("malformed batched insert:\n%s", query)
	}
	d0 := vars["d0"].(map[string]any)
	if d0["filter"] == nil || d0["role"] != "agent" {
		t.Errorf("query row var missing filter/role: %v", d0)
	}
	d1 := vars["d1"].(map[string]any)
	if d1["data"] == nil {
		t.Errorf("insert row var missing data: %v", d1)
	}
}
