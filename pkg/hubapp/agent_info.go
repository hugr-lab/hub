package hubapp

// agent_info — the Hugr function hugen's remote identity source calls at
// `mutation { function { hub { agent { info } } } }` to fetch its own settings.
// It lives in the `agent` module (path hub.agent.info), a sibling of the
// hub.agent.db data source.
//
// It resolves the CALLING agent from the auth context (the service principal is
// provisioned so user_id == agent_id, D11) and returns its identity plus the
// resolved runtime config: agent_type.config overlaid with the agent's
// config_override, both read from the Agent DB (hub.agent.db).
//
// Auto-registration (create the agent row when missing) is deferred; until an
// agent is seeded, agent_info returns a "not registered" error. The config is
// sourced only from hub.agent.db — there is no config-file fallback.

import (
	"fmt"

	"github.com/hugr-lab/query-engine/client/app"
)

// agentInfoType is the object agent_info returns. The hugen client scans it into
// identity.Agent (config is a JSON blob parsed by config.LoadStaticInput).
func agentInfoType() app.Type {
	return app.Struct("agent_info").
		Desc("Calling agent's identity + resolved runtime config (agent_type.config ⊕ config_override).").
		Field("id", app.String).
		Field("agent_type_id", app.String).
		Field("short_id", app.String).
		Field("name", app.String).
		Field("status", app.String).
		Field("config", app.JSON).
		AsType()
}

// registerAgentInfo wires the info mutation into the `agent` module → the
// function resolves at `mutation { function { hub { agent { info } } } }`
// (path hub.agent.info).
func (a *HubApp) registerAgentInfo() error {
	return a.mux.HandleFunc("agent", "info", a.handleAgentInfo,
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
		app.Return(agentInfoType()),
		app.Mutation(),
		app.Desc("Resolve the calling agent's settings. Identity = auth principal (user_id == agent_id). Config = agent_type.config ⊕ config_override from hub.agent.db."),
	)
}

func (a *HubApp) handleAgentInfo(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	aid := u.ID // agent principal: user_id == agent_id (D11)
	ctx := withIdentity(r.Context(), u)

	// Read the agent + its type from the Agent DB and merge configs. A DB-level
	// failure (query / result / scan) PROPAGATES as an error — it must not be
	// misreported as "agent not registered"; only a genuinely empty result is.
	res, err := a.client.Query(ctx,
		`query($aid: String!) { hub { agent { db { agents(
			filter: { id: { eq: $aid } } limit: 1
		) { id agent_type_id short_id name status config_override agent_type { config } } } } } }`,
		map[string]any{"aid": aid},
	)
	if err != nil {
		return fmt.Errorf("agent_info: Agent DB lookup failed for %q: %w", aid, err)
	}
	defer res.Close()
	if res.Err() != nil {
		return fmt.Errorf("agent_info: Agent DB query error for %q: %w", aid, res.Err())
	}
	var agents []struct {
		ID             string         `json:"id"`
		AgentTypeID    string         `json:"agent_type_id"`
		ShortID        string         `json:"short_id"`
		Name           string         `json:"name"`
		Status         string         `json:"status"`
		ConfigOverride map[string]any `json:"config_override"`
		AgentType      struct {
			Config map[string]any `json:"config"`
		} `json:"agent_type"`
	}
	if scanErr := res.ScanData("hub.agent.db.agents", &agents); scanErr != nil {
		return fmt.Errorf("agent_info: scan agents for %q: %w", aid, scanErr)
	}
	if len(agents) > 0 {
		ag := agents[0]
		merged := make(map[string]any, len(ag.AgentType.Config)+len(ag.ConfigOverride))
		for k, v := range ag.AgentType.Config { // base: the type's config
			merged[k] = v
		}
		for k, v := range ag.ConfigOverride { // per-instance override wins
			merged[k] = v
		}
		return w.SetJSON(map[string]any{
			"id":            ag.ID,
			"agent_type_id": ag.AgentTypeID,
			"short_id":      ag.ShortID,
			"name":          ag.Name,
			"status":        ag.Status,
			"config":        merged,
		})
	}

	// Not found — config lives only in hub.agent.db; there is no file fallback.
	// Registration (deferred) seeds the row.
	return fmt.Errorf("agent_info: agent %q not registered in hub.agent.db", aid)
}
