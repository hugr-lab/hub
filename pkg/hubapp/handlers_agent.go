package hubapp

import (
	"context"
	"fmt"

	"github.com/hugr-lab/hub/pkg/agentmgr"
	"github.com/hugr-lab/query-engine/client/app"
)

// agentRuntimeStateType is the Struct() return for start/stop mutations — gives
// the frontend enough state to patch its runtime view without re-fetching
// hub.agent_runtime.
func agentRuntimeStateType() app.Type {
	return app.Struct("agent_runtime_state").
		Desc("Runtime state of an agent after a lifecycle mutation.").
		Field("id", app.String).
		Field("status", app.String).
		Field("container_id", app.String).
		AsType()
}

// registerAgentMutations registers mutating functions for agent runtime lifecycle.
func (a *HubApp) registerAgentMutations() error {
	if err := a.mux.HandleFunc("default", "start_agent", a.handleStartAgent,
		app.Arg("agent_id", app.String),
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
		app.Return(agentRuntimeStateType()),
		app.Mutation(),
		app.Desc("Start an agent container. Returns the new runtime state {id, status, container_id}. Requires owner access on the agent (or admin)."),
	); err != nil {
		return err
	}

	if err := a.mux.HandleFunc("default", "stop_agent", a.handleStopAgent,
		app.Arg("agent_id", app.String),
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
		app.Return(agentRuntimeStateType()),
		app.Mutation(),
		app.Desc("Stop a running agent container. Returns the resulting runtime state {id, status='stopped', container_id=''}. Requires owner access (or admin)."),
	); err != nil {
		return err
	}

	if err := a.mux.HandleFunc("default", "delete_agent", a.handleDeleteAgent,
		app.Arg("agent_id", app.String),
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
		app.Return(app.String),
		app.Mutation(),
		app.Desc("Stop the agent (if running) and delete identity from agents + user_agents tables. Returns the deleted agent_id. Admin only."),
	); err != nil {
		return err
	}

	return nil
}

// handleStartAgent looks up agent identity from DB and starts a container via DockerRuntime.
func (a *HubApp) handleStartAgent(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	agentID := r.String("agent_id")
	if agentID == "" {
		return fmt.Errorf("agent_id is required")
	}
	if a.dockerRuntime == nil {
		return fmt.Errorf("docker runtime unavailable")
	}

	ctx := withIdentity(r.Context(), u)

	if err := a.checkAgentAccess(ctx, u, agentID, "owner"); err != nil {
		return err
	}

	identity, err := a.lookupAgentIdentity(ctx, agentID)
	if err != nil {
		return fmt.Errorf("lookup agent: %w", err)
	}

	if err := a.dockerRuntime.Start(ctx, identity); err != nil {
		return fmt.Errorf("start agent: %w", err)
	}

	state := a.dockerRuntime.Status(agentID)
	containerShort := state.ContainerID
	if len(containerShort) > 12 {
		containerShort = containerShort[:12]
	}
	a.logger.Info("agent started via mutation", "agent", agentID, "container", containerShort, "by", u.ID)
	return w.SetJSON(map[string]any{
		"id":           agentID,
		"status":       state.Status,
		"container_id": containerShort,
	})
}

// handleStopAgent stops an agent container via DockerRuntime.
func (a *HubApp) handleStopAgent(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	agentID := r.String("agent_id")
	if agentID == "" {
		return fmt.Errorf("agent_id is required")
	}
	if a.dockerRuntime == nil {
		return fmt.Errorf("docker runtime unavailable")
	}

	ctx := withIdentity(r.Context(), u)

	if err := a.checkAgentAccess(ctx, u, agentID, "owner"); err != nil {
		return err
	}

	if err := a.dockerRuntime.Stop(ctx, agentID); err != nil {
		return fmt.Errorf("stop agent: %w", err)
	}

	a.logger.Info("agent stopped via mutation", "agent", agentID, "by", u.ID)
	return w.SetJSON(map[string]any{
		"id":           agentID,
		"status":       "stopped",
		"container_id": "",
	})
}

// handleDeleteAgent stops the runtime (if running) and deletes the agent identity.
// Requires the hub:management.admin capability — agent identity removal affects
// all users with access grants.
func (a *HubApp) handleDeleteAgent(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	agentID := r.String("agent_id")
	if agentID == "" {
		return fmt.Errorf("agent_id is required")
	}

	ctx := withIdentity(r.Context(), u)

	if err := a.requireAdmin(ctx, u); err != nil {
		return err
	}

	// Stop runtime (idempotent — no error if not running)
	if a.dockerRuntime != nil {
		_ = a.dockerRuntime.Stop(ctx, agentID)
	}

	// Delete user_agents grants
	// Note: conversations.agent_id FK is ON DELETE SET NULL — no need to
	// detach conversations manually, the DB nullifies them on agent delete.
	if accessRes, err := a.client.Query(ctx,
		`mutation($aid: String!) { hub { db { delete_user_agents(
			filter: { agent_id: { eq: $aid } }
		) { affected_rows } } } }`,
		map[string]any{"aid": agentID},
	); err == nil {
		accessRes.Close()
	}

	// Delete agent identity
	delRes, err := a.client.Query(ctx,
		`mutation($id: String!) { hub { db { delete_agents(
			filter: { id: { eq: $id } }
		) { affected_rows } } } }`,
		map[string]any{"id": agentID},
	)
	if err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}
	defer delRes.Close()
	if delRes.Err() != nil {
		return fmt.Errorf("delete agent: %w", delRes.Err())
	}

	a.logger.Info("agent deleted via mutation", "agent", agentID, "by", u.ID)
	return w.Set(agentID)
}

// lookupAgentIdentity fetches agent identity (display_name, hugr_*, image) from hub.db.agents.
func (a *HubApp) lookupAgentIdentity(ctx context.Context, agentID string) (agentmgr.AgentIdentity, error) {
	res, err := a.client.Query(ctx,
		`query($id: String!) { hub { db { agents(
			filter: { id: { eq: $id } } limit: 1
		) { id agent_type_id display_name hugr_user_id hugr_user_name hugr_role
		   agent_type { image } } } } }`,
		map[string]any{"id": agentID},
	)
	if err != nil {
		return agentmgr.AgentIdentity{}, err
	}
	defer res.Close()
	if res.Err() != nil {
		return agentmgr.AgentIdentity{}, res.Err()
	}

	var agents []struct {
		ID           string `json:"id"`
		AgentTypeID  string `json:"agent_type_id"`
		DisplayName  string `json:"display_name"`
		HugrUserID   string `json:"hugr_user_id"`
		HugrUserName string `json:"hugr_user_name"`
		HugrRole     string `json:"hugr_role"`
		AgentType    struct {
			Image string `json:"image"`
		} `json:"agent_type"`
	}
	if err := res.ScanData("hub.db.agents", &agents); err != nil || len(agents) == 0 {
		return agentmgr.AgentIdentity{}, fmt.Errorf("agent %q not found", agentID)
	}

	return agentmgr.AgentIdentity{
		ID:           agents[0].ID,
		AgentTypeID:  agents[0].AgentTypeID,
		DisplayName:  agents[0].DisplayName,
		HugrUserID:   agents[0].HugrUserID,
		HugrUserName: agents[0].HugrUserName,
		HugrRole:     agents[0].HugrRole,
		Image:        agents[0].AgentType.Image,
	}, nil
}
