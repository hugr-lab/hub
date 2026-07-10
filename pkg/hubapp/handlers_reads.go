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
	return a.mux.HandleTableFunc("default", "my_agent_instances", a.handleMyAgentInstances,
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
		app.Desc("Agent instances the current user has access to, enriched with container runtime status. Admins see all agents."),
	)
}

func (a *HubApp) handleMyAgentInstances(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	ctx := withIdentity(r.Context(), u)

	type row struct {
		id, agentTypeID, displayName, hugrRole, accessRole string
	}
	var rows []row

	// Only engine-internal management auth lists everything. Everyone else —
	// including OIDC users whose deployment calls them "admin" — goes through
	// hub.db.user_agents grants. The management-auth branch lists the whole Agent
	// DB (hub.agent.db.agents); Hugr RBAC gates the principal.
	if u.AuthType == "management" {
		// Identity canon is the Agent DB (hub.agent.db.agents), NOT the legacy
		// platform hub.db.agents duplicate (dropped in HB6). name/role map to the
		// display_name/hugr_role columns this table function exposes.
		res, err := a.client.Query(ctx,
			`{ hub { agent { db { agents {
				id agent_type_id name role
			} } } } }`, nil,
		)
		if err != nil {
			return fmt.Errorf("list agents: %w", err)
		}
		defer res.Close()
		if res.Err() != nil {
			return fmt.Errorf("list agents: %w", res.Err())
		}
		var agents []struct {
			ID          string `json:"id"`
			AgentTypeID string `json:"agent_type_id"`
			Name        string `json:"name"`
			Role        string `json:"role"`
		}
		if err := res.ScanData("hub.agent.db.agents", &agents); err != nil && !isNoData(err) {
			return fmt.Errorf("scan agents: %w", err)
		}
		for _, ag := range agents {
			rows = append(rows, row{ag.ID, ag.AgentTypeID, ag.Name, ag.Role, "admin"})
		}
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
			rows = append(rows, row{ag.ID, ag.AgentTypeID, ag.Name, ag.Role, ua.Role})
		}
	}

	for _, rr := range rows {
		status := "stopped"
		if a.agentRuntime != nil {
			st := a.agentRuntime.Status(rr.id)
			if st.Status != "" {
				status = st.Status
			}
		}
		if err := w.Append(
			rr.id, rr.agentTypeID, rr.displayName, rr.hugrRole,
			status, rr.accessRole,
		); err != nil {
			return err
		}
	}
	return nil
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
