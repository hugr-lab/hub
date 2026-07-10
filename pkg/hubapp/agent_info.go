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

	// Resolve the calling agent's row via the shared service-principal reader (do
	// NOT impersonate the agent — its hugr role is denied the Agent DB by the HB3
	// RLS floor). The lookup is self-scoped: aid is the authenticated agent id
	// (JWT sub, unspoofable), so a service-principal read filtered by aid returns
	// exactly the calling agent's row. readAgentRecord propagates real query/scan
	// errors and returns "not found" only on a genuinely empty result.
	rec, err := a.readAgentRecord(r.Context(), aid)
	if err != nil {
		return fmt.Errorf("agent_info: %w", err)
	}
	return w.SetJSON(map[string]any{
		"id":            rec.ID,
		"agent_type_id": rec.AgentTypeID,
		"short_id":      rec.ShortID,
		"name":          rec.Name,
		"status":        rec.Status,
		"config":        rec.Config,
	})
}
