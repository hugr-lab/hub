package hubapp

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// HB-EXT registers a cross-source `extension` data source whose entire payload
// is the relation SDL (schema/graph.graphql). A typo in a join field silently
// breaks navigation, so these tests pin the relation set deterministically —
// the live cross-source resolution is gated separately (needs both physical
// Postgres sources up), same split as the RLS seed in agent_roles_test.go.

// extendBlock returns the body between `extend type <typ> ... {` and its
// matching closing brace, or "" if the block is absent.
func extendBlock(t *testing.T, typ string) string {
	t.Helper()
	re := regexp.MustCompile(`(?ms)^extend type ` + regexp.QuoteMeta(typ) + `\b(.*?)^}`)
	m := re.FindStringSubmatch(hubGraphExtSchema)
	if m == nil {
		t.Fatalf("extend type %s block not found in graph.graphql", typ)
	}
	return m[1]
}

// extendOrTypeBlock matches either an `extend type X` or a `type X` block —
// used for the @view type, which is a plain `type`, not an extension.
func extendOrTypeBlock(t *testing.T, typ string) string {
	t.Helper()
	re := regexp.MustCompile(`(?ms)^(?:extend )?type ` + regexp.QuoteMeta(typ) + `\b(.*?)^}`)
	m := re.FindStringSubmatch(hubGraphExtSchema)
	if m == nil {
		t.Fatalf("type %s block not found in graph.graphql", typ)
	}
	return m[1]
}

func TestGraphExt_ExtendBlocksDeclareBothDependencies(t *testing.T) {
	// Every cross-source extend block must depend on BOTH the source of the
	// extended type and the source of the referenced types, or the engine
	// refuses to compile it ("Dependency not loaded").
	for _, typ := range []string{"hub_db_chats", "hub_db_user_agents"} {
		block := extendBlock(t, typ)
		for _, dep := range []string{`@dependency(name: "hub.db")`, `@dependency(name: "hub.agent.db")`} {
			if !strings.Contains(block, dep) {
				t.Errorf("extend type %s is missing %s", typ, dep)
			}
		}
	}
}

func TestGraphExt_NoReverseRelationFromAgentStore(t *testing.T) {
	// SECURITY: the graph must NOT extend an Agent-DB type with a relation into
	// a platform type. That would give an `agent`-role principal a direct read
	// path into the platform DB (data-object filters cover only hub_agent_db_*),
	// violating "the agent reaches platform data only via the hub MCP surface".
	// Platform relations are declared ONLY on platform types (forward), reached
	// by users/admins — never from the agent's own store.
	if regexp.MustCompile(`(?m)^extend type hub_agent_db_`).MatchString(hubGraphExtSchema) {
		t.Error("graph must not extend an Agent-DB (hub_agent_db_*) type — it opens a platform read path for agents")
	}
}

func TestGraphExt_JoinFieldsWellFormed(t *testing.T) {
	// (extended type → field, target type, source_fields, references_fields).
	type rel struct {
		parent, field, target, src, ref string
	}
	rels := []rel{
		{"hub_db_chats", "agent", "hub_agent_db_agents", "agent_id", "id"},
		{"hub_db_chats", "root_session", "hub_agent_db_sessions", "root_session_id", "id"},
		{"hub_db_user_agents", "agent", "hub_agent_db_agents", "agent_id", "id"},
	}
	for _, r := range rels {
		block := extendBlock(t, r.parent)
		// The field, its list-typed target, the @join and both key lists must
		// all appear inside the block. @join fields are list-typed by the engine.
		field := regexp.MustCompile(
			r.field + `:\s*\[` + r.target + `\][^}]*?@join\([^)]*?` +
				`references_name:\s*"` + r.target + `"[^)]*?` +
				`source_fields:\s*\["` + r.src + `"\][^)]*?` +
				`references_fields:\s*\["` + r.ref + `"\]`,
		)
		if !field.MatchString(block) {
			t.Errorf("%s.%s: expected @join → %s on [%s]→[%s] not found in block:\n%s",
				r.parent, r.field, r.target, r.src, r.ref, block)
		}
	}
}

func TestGraphExt_ChatOverviewViewSelfScoped(t *testing.T) {
	// A raw cross-source @view bypasses the data-object RLS floor (it reads the
	// physical attached tables), so chat_overview MUST self-scope in SQL via a
	// server-injected placeholder — never a client arg. These assertions guard
	// against a regression that drops the scope (→ every user sees every chat).
	if !strings.Contains(hubGraphExtSchema, "type chat_overview") {
		t.Fatal("chat_overview view missing")
	}
	// Cross-source view names each attached source explicitly by its dotted,
	// quoted data_sources name.
	for _, from := range []string{`"hub.db".public.chats`, `"hub.agent.db".public.agents`, `"hub.agent.db".public.sessions`} {
		if !strings.Contains(hubGraphExtSchema, from) {
			t.Errorf("chat_overview SQL must read %s", from)
		}
	}
	// Self-scope: the WHERE binds the server-resolved [$auth.user_id] context
	// placeholder directly (argless view; substituted by the planner from
	// perm.AuthVars, so a client cannot spoof it).
	if !strings.Contains(hubGraphExtSchema, "WHERE c.user_id = [$auth.user_id]") {
		t.Error("chat_overview must scope WHERE c.user_id = [$auth.user_id]")
	}
	// The view depends on both sources.
	block := extendOrTypeBlock(t, "chat_overview")
	for _, dep := range []string{`@dependency(name: "hub.db")`, `@dependency(name: "hub.agent.db")`} {
		if !strings.Contains(block, dep) {
			t.Errorf("chat_overview is missing %s", dep)
		}
	}
}

func TestGraphExt_RegisteredAsExtensionSource(t *testing.T) {
	a := &HubApp{config: Config{
		DatabaseDSN:      "postgres://hub/db",
		AgentDatabaseDSN: "postgres://hub/agent", // MUST differ from DatabaseDSN
	}}
	sources, err := a.DataSources(context.Background())
	if err != nil {
		t.Fatalf("DataSources: %v", err)
	}
	var found bool
	for _, ds := range sources {
		if ds.Name != "graph" {
			continue
		}
		found = true
		if ds.Type != "extension" {
			t.Errorf("graph source type = %q, want extension", ds.Type)
		}
		if ds.Path != "" {
			t.Errorf("graph source path = %q, want empty (extension needs no connection)", ds.Path)
		}
		if ds.HugrSchema != hubGraphExtSchema {
			t.Error("graph source HugrSchema is not the embedded graph.graphql")
		}
	}
	if !found {
		t.Error("no `graph` extension source registered in DataSources()")
	}
}
