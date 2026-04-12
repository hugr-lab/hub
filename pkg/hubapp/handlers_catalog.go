package hubapp

// Airport-go table function: agent_capabilities
//
// Returns the list of allowed skills for a given agent, filtered by
// the agent type's runtime_context. Used by hub-agent at startup to
// determine which skills to load.

import (
	"fmt"

	"github.com/hugr-lab/query-engine/client/app"
)

func (a *HubApp) registerCatalogFunctions() error {
	return a.mux.HandleTableFunc("default", "agent_capabilities", a.handleAgentCapabilities,
		app.Arg("agent_id", app.String),
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
		app.ColPK("skill_id", app.String),
		app.Col("context", app.String),
		app.Desc("Returns allowed skills for an agent, filtered by runtime_context. Agent calls this at startup via /mcp."),
	)
}

func (a *HubApp) handleAgentCapabilities(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	agentID := r.String("agent_id")
	if agentID == "" {
		return fmt.Errorf("agent_id is required")
	}

	ctx := withIdentity(r.Context(), u)

	// Look up agent → agent_type → allowed_skills + runtime_context
	res, err := a.client.Query(ctx,
		`query($aid: String!) { hub { db { agents(
			filter: { id: { eq: $aid } } limit: 1
		) { agent_type { allowed_skills runtime_context } } } } }`,
		map[string]any{"aid": agentID},
	)
	if err != nil {
		return fmt.Errorf("lookup agent: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return fmt.Errorf("lookup agent: %w", res.Err())
	}

	var agents []struct {
		AgentType struct {
			AllowedSkills []string `json:"allowed_skills"`
			RuntimeContext string  `json:"runtime_context"`
		} `json:"agent_type"`
	}
	if err := res.ScanData("hub.db.agents", &agents); err != nil || len(agents) == 0 {
		return fmt.Errorf("agent %q not found", agentID)
	}

	runtimeCtx := agents[0].AgentType.RuntimeContext
	if runtimeCtx == "" {
		runtimeCtx = "any"
	}

	for _, skillID := range agents[0].AgentType.AllowedSkills {
		if err := w.Append(skillID, runtimeCtx); err != nil {
			return err
		}
	}
	return nil
}
