package hubapp

// Read-side airport-go table functions.
//
// User-scoped lists that inject the caller's identity via ArgFromContext and
// enforce ownership server-side, so the browser never needs to know its own
// user_id. Registered as TABLE functions — the caller does:
//
//	query {
//	  hub {
//	    my_agent_instances { id display_name status access_role }
//	  }
//	}
//
// The ADK conversation lists (my_conversations / my_conversation_messages) were
// removed with the HB6 store-prune.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hugr-lab/query-engine/client/app"
	"github.com/hugr-lab/query-engine/types"
)

// isNoData reports whether err is a benign "zero-rows" signal from the query
// engine's ScanData. Listing queries use this to treat empty results as an
// empty slice rather than an error.
func isNoData(err error) bool {
	return errors.Is(err, types.ErrNoData)
}

// registerReadFunctions registers the read-side table functions.
// Call from registerCatalog().
func (a *HubApp) registerReadFunctions() error {
	// HB6 store-prune: my_conversations / my_conversation_messages removed with
	// the ADK transcript tables. my_agent_instances stays until HB4 rewires the
	// agent-identity read off the (dropped-in-HB4) hub.db.agents duplicate onto
	// the Agent DB (then it becomes an extension-declared cross-source field).
	if err := a.registerMyAgentInstances(); err != nil {
		return err
	}
	return nil
}

// ───── my_agent_instances ─────

func (a *HubApp) registerMyAgentInstances() error {
	if err := a.mux.HandleTableFunc("default", "my_agent_instances", a.handleMyAgentInstances,
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
		app.ColPK("id", app.String),
		app.Col("agent_type_id", app.String),
		app.Col("display_name", app.String),
		app.Col("hugr_role", app.String),
		app.Col("status", app.String),
		app.Col("access_role", app.String),
		app.Desc("Agent instances the current user has a grant on (own agents), enriched with container runtime status. The chat picker + Me/Access read this."),
	); err != nil {
		return err
	}
	// Admin-only whole-fleet variant for the management console.
	return a.mux.HandleTableFunc("default", "all_agent_instances", a.handleAllAgentInstances,
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
		app.ColPK("id", app.String),
		app.Col("agent_type_id", app.String),
		app.Col("display_name", app.String),
		app.Col("hugr_role", app.String),
		app.Col("status", app.String),
		app.Col("access_role", app.String),
		app.Desc("EVERY agent in the fleet with live runtime status — admin only (hub:management.admin). The console's agent-management screen reads this; chatting stays gated to my_agent_instances."),
	)
}

func (a *HubApp) handleMyAgentInstances(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	ctx := withIdentity(r.Context(), u)

	var rows []agentInstanceRow

	// Only engine-internal management auth lists everything here. Everyone else —
	// including OIDC users whose deployment calls them "admin" — goes through
	// hub.db.user_agents grants (own agents; the chat picker + Me/Access rely on
	// this being MINE-only). An admin console lists the whole fleet via the
	// dedicated `all_agent_instances` function instead.
	if u.AuthType == "management" {
		all, err := a.listAllAgentRows(ctx)
		if err != nil {
			return err
		}
		rows = all
	} else {
		// The `agent` field is the cross-source @join (graph.graphql) from the
		// platform grant into the Agent DB identity — LIST-typed (single element),
		// so scan a slice and take the first. Grants pointing at a deleted agent
		// (empty join) are skipped. name/role → display_name/hugr_role columns.
		res, err := a.client.Query(ctx,
			`query($uid: String!) { hub { db { user_agents(
				filter: { user_id: { eq: $uid } }
			) { role agent { id agent_type_id name role } } } } }`,
			map[string]any{"uid": u.ID},
		)
		if err != nil {
			return fmt.Errorf("list user_agents: %w", err)
		}
		defer res.Close()
		if res.Err() != nil {
			return fmt.Errorf("list user_agents: %w", res.Err())
		}
		var access []struct {
			Role  string `json:"role"`
			Agent []struct {
				ID          string `json:"id"`
				AgentTypeID string `json:"agent_type_id"`
				Name        string `json:"name"`
				Role        string `json:"role"`
			} `json:"agent"`
		}
		if err := res.ScanData("hub.db.user_agents", &access); err != nil && !isNoData(err) {
			return fmt.Errorf("scan user_agents: %w", err)
		}
		for _, ua := range access {
			if len(ua.Agent) == 0 {
				continue // grant to a deleted agent — no identity to show
			}
			ag := ua.Agent[0]
			rows = append(rows, agentInstanceRow{ag.ID, ag.AgentTypeID, ag.Name, ag.Role, ua.Role})
		}
	}

	return a.appendAgentInstances(w, rows)
}

// agentInstanceRow is one identity row before the live runtime status is
// stamped by appendAgentInstances.
type agentInstanceRow struct {
	id, agentTypeID, displayName, hugrRole, accessRole string
}

// listAllAgentRows reads the whole fleet from the Agent DB identity canon
// (hub.agent.db.agents) — used by the management-auth my_agent_instances branch
// and the admin-only all_agent_instances function. accessRole is "admin".
func (a *HubApp) listAllAgentRows(ctx context.Context) ([]agentInstanceRow, error) {
	res, err := a.client.Query(ctx,
		`{ hub { agent { db { agents { id agent_type_id name role } } } } }`, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return nil, fmt.Errorf("list agents: %w", res.Err())
	}
	var agents []struct {
		ID          string `json:"id"`
		AgentTypeID string `json:"agent_type_id"`
		Name        string `json:"name"`
		Role        string `json:"role"`
	}
	if err := res.ScanData("hub.agent.db.agents", &agents); err != nil && !isNoData(err) {
		return nil, fmt.Errorf("scan agents: %w", err)
	}
	rows := make([]agentInstanceRow, 0, len(agents))
	for _, ag := range agents {
		rows = append(rows, agentInstanceRow{ag.ID, ag.AgentTypeID, ag.Name, ag.Role, "admin"})
	}
	return rows, nil
}

// appendAgentInstances stamps each identity row with its LIVE runtime status
// (from the agent runtime cache) and emits it as a table-function row.
func (a *HubApp) appendAgentInstances(w *app.Result, rows []agentInstanceRow) error {
	for _, rr := range rows {
		status := "stopped"
		if a.agentRuntime != nil {
			if st := a.agentRuntime.Status(rr.id); st.Status != "" {
				status = st.Status
			}
		}
		if err := w.Append(rr.id, rr.agentTypeID, rr.displayName, rr.hugrRole, status, rr.accessRole); err != nil {
			return err
		}
	}
	return nil
}

// handleAllAgentInstances backs the admin-only `all_agent_instances` table
// function: the WHOLE fleet (every agent, not just the caller's grants) with
// live status. The console's admin agent-management screen reads it; chatting is
// still gated to the caller's own agents via my_agent_instances + checkAccess.
func (a *HubApp) handleAllAgentInstances(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	ctx := withIdentity(r.Context(), u)
	if u.AuthType != "management" {
		if err := a.requireAdmin(ctx, u); err != nil {
			return fmt.Errorf("all_agent_instances is admin only: %w", err)
		}
	}
	rows, err := a.listAllAgentRows(ctx)
	if err != nil {
		return err
	}
	return a.appendAgentInstances(w, rows)
}

// ───── small helpers ─────

func strPtrOrNil(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func int64PtrOrNil(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// jsonMarshal is a silent JSON encoder — returns "null" on error so table-function
// rows never fail on marshaling a foreign JSON payload.
func jsonMarshal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}
