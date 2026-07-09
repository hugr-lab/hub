package hubapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hugr-lab/hub/pkg/agentmgr"
	"github.com/hugr-lab/query-engine/client/app"
	"github.com/hugr-lab/query-engine/types"
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

	// The access check impersonates the caller (client.AsUser) — it must see the
	// world as the caller to evaluate their user_agents grant.
	if err := a.checkAgentAccess(withIdentity(r.Context(), u), u, agentID, "owner"); err != nil {
		return err
	}

	// The spawn itself is a PRIVILEGED hub operation: read the agent catalog and
	// mint/start as the service principal, NOT impersonating the caller. A
	// non-admin owner's hugr role is denied the Agent DB by the HB3 RLS floor, so
	// an impersonated read returns 0 rows ("not found"). agent_info reads
	// un-impersonated for exactly this reason; the access check above already
	// gated the caller.
	svcCtx := r.Context()

	rec, err := a.readAgentRecord(svcCtx, agentID)
	if err != nil {
		return fmt.Errorf("lookup agent: %w", err)
	}
	identity, err := agentIdentityFromRecord(rec)
	if err != nil {
		return fmt.Errorf("lookup agent: %w", err)
	}

	if err := a.dockerRuntime.Start(svcCtx, identity); err != nil {
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

// agentRecord is an agent's identity plus its EFFECTIVE config — the agent_type
// config merged with the per-instance config_override (override wins), the same
// shape agent_info resolves.
type agentRecord struct {
	ID          string
	AgentTypeID string
	ShortID     string
	Name        string
	Status      string
	Role        string
	Config      map[string]any // agent_type.config ⊕ config_override
}

// readAgentRecord reads an agent's identity and effective config from the Agent
// DB (hub.agent.db.agents, the canon), merging agent_type.config with
// config_override exactly as agent_info does.
//
// The caller MUST pass an un-impersonated (service-principal) ctx: a non-admin
// caller's hugr role is denied the Agent DB catalog by the HB3 RLS floor, so an
// impersonated read returns 0 rows. config is a JSON column the engine returns
// as Arrow utf8 — it scans into a map, NOT json.RawMessage ([]byte). Only a
// genuinely empty result (ErrNoData) is "not found"; every other query/scan
// error propagates rather than being masked as not-found.
func (a *HubApp) readAgentRecord(ctx context.Context, agentID string) (agentRecord, error) {
	res, err := a.client.Query(ctx,
		`query($id: String!) { hub { agent { db { agents(
			filter: { id: { eq: $id } } limit: 1
		) { id agent_type_id short_id name status role config_override agent_type { config } } } } } }`,
		map[string]any{"id": agentID},
	)
	if err != nil {
		return agentRecord{}, fmt.Errorf("read agent %q: %w", agentID, err)
	}
	defer res.Close()
	if res.Err() != nil {
		return agentRecord{}, fmt.Errorf("read agent %q: %w", agentID, res.Err())
	}

	var rows []struct {
		ID             string         `json:"id"`
		AgentTypeID    string         `json:"agent_type_id"`
		ShortID        string         `json:"short_id"`
		Name           string         `json:"name"`
		Status         string         `json:"status"`
		Role           string         `json:"role"`
		ConfigOverride map[string]any `json:"config_override"`
		AgentType      struct {
			Config map[string]any `json:"config"`
		} `json:"agent_type"`
	}
	if err := res.ScanData("hub.agent.db.agents", &rows); err != nil {
		if errors.Is(err, types.ErrNoData) {
			return agentRecord{}, fmt.Errorf("agent %q not found", agentID)
		}
		return agentRecord{}, fmt.Errorf("read agent %q: scan: %w", agentID, err)
	}
	if len(rows) == 0 {
		return agentRecord{}, fmt.Errorf("agent %q not found", agentID)
	}

	row := rows[0]
	merged := make(map[string]any, len(row.AgentType.Config)+len(row.ConfigOverride))
	for k, v := range row.AgentType.Config { // base: the type's config
		merged[k] = v
	}
	for k, v := range row.ConfigOverride { // per-instance override wins
		merged[k] = v
	}
	return agentRecord{
		ID:          row.ID,
		AgentTypeID: row.AgentTypeID,
		ShortID:     row.ShortID,
		Name:        row.Name,
		Status:      row.Status,
		Role:        row.Role,
		Config:      merged,
	}, nil
}

// agentIdentityFromRecord derives the container spawn identity (image + resource
// caps from the EFFECTIVE orchestration block) from an agent record.
func agentIdentityFromRecord(rec agentRecord) (agentmgr.AgentIdentity, error) {
	cfg, err := json.Marshal(rec.Config)
	if err != nil {
		return agentmgr.AgentIdentity{}, fmt.Errorf("marshal agent %q config: %w", rec.ID, err)
	}
	orch := agentmgr.OrchestrationFromConfig(cfg)
	return agentmgr.AgentIdentity{
		ID:           rec.ID,
		AgentTypeID:  rec.AgentTypeID,
		DisplayName:  rec.Name,
		HugrUserID:   rec.ID,
		HugrUserName: rec.Name,
		HugrRole:     rec.Role,
		Image:        orch.Image,
		MemoryBytes:  orch.MemoryBytes,
		NanoCPUs:     orch.NanoCPUs,
		PidsLimit:    orch.PidsLimit,
	}, nil
}
