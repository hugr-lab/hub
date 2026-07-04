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
// Auto-registration (create the agent row when missing) is deferred. Until an
// agent is seeded, a testing fallback returns HUB_AGENT_CONFIG_FILE verbatim so
// a remote agent can boot with the settings we have locally.

import (
	"fmt"
	"os"

	"github.com/hugr-lab/query-engine/client/app"
	"gopkg.in/yaml.v3"
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
		app.Desc("Resolve the calling agent's settings. Identity = auth principal (user_id == agent_id). Config = agent_type.config ⊕ config_override from hub.agent.db; falls back to HUB_AGENT_CONFIG_FILE when the agent is not yet registered."),
	)
}

func (a *HubApp) handleAgentInfo(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	aid := u.ID // agent principal: user_id == agent_id (D11)
	ctx := withIdentity(r.Context(), u)

	// 1. Real path — read the agent + its type from the Agent DB, merge configs.
	//    A DB-level failure (query / result / scan) PROPAGATES: it must not
	//    degrade to the config-file fallback, which would boot a registered
	//    agent on the wrong (local) config or misreport a DB outage as "not
	//    registered". Only a genuinely absent agent (empty result) falls
	//    through to the testing fallback below.
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

	// 2. Testing fallback — the agent is not registered yet; return the local
	//    config file so a remote agent can boot. Auto-registration lands later.
	if a.config.AgentConfigFile == "" {
		return fmt.Errorf("agent_info: agent %q not registered in hub.agent.db and HUB_AGENT_CONFIG_FILE is unset", aid)
	}
	cfg, err := loadAgentConfigFile(a.config.AgentConfigFile)
	if err != nil {
		return fmt.Errorf("agent_info: load config file: %w", err)
	}
	a.logger.Info("agent_info: served fallback config file", "agent", aid, "file", a.config.AgentConfigFile)
	return w.SetJSON(map[string]any{
		"id":            aid,
		"agent_type_id": "",
		"short_id":      aid,
		"name":          u.Name,
		"status":        "active",
		"config":        cfg,
	})
}

// loadAgentConfigFile reads a YAML (or JSON — YAML is a superset) agent-config
// file into the generic map hugen's config.LoadStaticInput consumes.
func loadAgentConfigFile(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}
