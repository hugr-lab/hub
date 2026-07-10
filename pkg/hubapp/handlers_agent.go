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
		app.Desc("Start an agent: set desired status='active' and converge (the supervisor spawns the container). A 'manual' agent is launched one-shot WITHOUT changing its status (it stays hands-off; a crash won't auto-revive). Returns the runtime state {id, status, container_id}. Fails if the agent is admin-'disabled'. Requires owner access (or admin)."),
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
		app.Desc("Stop an agent: set desired status='paused' and converge (the supervisor removes the container). A 'manual' agent's container is stopped WITHOUT changing its status (stays 'manual'). Returns {id, status, container_id=''}. Fails if the agent is admin-'disabled'. Requires owner access (or admin)."),
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

// handleStartAgent is an owner-gated desired-state WRITER: it flips agents.status
// to 'active' and kicks an immediate converge (spec §4). The supervisor owns the
// actual Start; the kick makes the effect immediate so the response reflects the
// running container. An admin-disabled agent cannot be resurrected here — only
// update_agent (admin) leaves 'disabled'.
func (a *HubApp) handleStartAgent(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	agentID := r.String("agent_id")
	if agentID == "" {
		return fmt.Errorf("agent_id is required")
	}
	if a.agentRuntime == nil {
		return fmt.Errorf("docker runtime unavailable")
	}

	// The access check impersonates the caller (client.AsUser) — it must see the
	// world as the caller to evaluate their user_agents grant.
	if err := a.checkAgentAccess(withIdentity(r.Context(), u), u, agentID, "owner"); err != nil {
		return err
	}

	// Everything below is a PRIVILEGED hub op — read/write the Agent DB as the
	// service principal (admin, RLS-exempt), NOT impersonating the caller: a
	// non-admin owner's role is denied the Agent DB by the HB3 RLS floor.
	svcCtx := r.Context()

	rec, err := a.readAgentRecord(svcCtx, agentID)
	if err != nil {
		return fmt.Errorf("start agent: %w", err)
	}
	if rec.Status == "disabled" {
		return fmt.Errorf("agent %q is disabled; an administrator must re-enable it (update_agent status=active)", agentID)
	}

	if rec.Status == "manual" {
		// Manual is hands-off (spec §4): launch the container explicitly WITHOUT
		// flipping to 'active' — the supervisor keeps ignoring it. Route through
		// supervisor.startManual, which serializes against the tick (per-agent
		// lock), re-reads live status (aborts if a concurrent revoke moved it off
		// 'manual'), is idempotent on an already-running container, and Removes a
		// stale carcass before Start (C1). The container gets restart-policy 'no'.
		if a.supervisor != nil {
			if err := a.supervisor.startManual(svcCtx, agentID); err != nil {
				return fmt.Errorf("start agent (manual): %w", err)
			}
		} else {
			// Defensive fallback (the supervisor runs whenever agentRuntime != nil).
			identity, err := agentIdentityFromRecord(rec)
			if err != nil {
				return fmt.Errorf("start agent: %w", err)
			}
			_ = a.agentRuntime.Remove(svcCtx, agentID)
			if err := a.agentRuntime.Start(svcCtx, identity); err != nil {
				return fmt.Errorf("start agent (manual): %w", err)
			}
		}
		state := a.agentRuntime.Status(agentID)
		containerShort := shortID(state.ContainerID)
		a.logger.Info("manual agent started via mutation", "agent", agentID, "container", containerShort, "by", u.ID)
		return w.SetJSON(map[string]any{
			"id":           agentID,
			"status":       state.Status,
			"container_id": containerShort,
		})
	}

	if rec.Status != "active" {
		// Conditional flip — setAgentRunState only writes from an owner-writable
		// state (active/paused), NEVER from 'disabled'. This makes the transition
		// atomic against a concurrent admin disable_agent: an owner cannot
		// reactivate a revoked agent by racing the read→write window (spec §4
		// authority invariant).
		if err := a.setAgentRunState(svcCtx, agentID, "active"); err != nil {
			return fmt.Errorf("start agent: set status active: %w", err)
		}
		// hugr reports affected_rows unreliably, so confirm by re-read: a disable
		// that raced our write leaves the row 'disabled' (the filter dropped it).
		after, err := a.agentForToken(svcCtx, agentID)
		if err != nil {
			return fmt.Errorf("start agent: %w", err)
		}
		if after.Status == "disabled" {
			return fmt.Errorf("agent %q was disabled concurrently; an administrator must re-enable it", agentID)
		}
	}

	// Converge now. Prefer the supervisor kick (single owner of container
	// lifecycle); fall back to a direct Start only if Docker is up but the
	// supervisor somehow isn't (defensive — startSupervisor runs whenever
	// agentRuntime != nil).
	if a.supervisor != nil {
		a.supervisor.kick(svcCtx, agentID, "active")
	} else {
		identity, err := agentIdentityFromRecord(rec)
		if err != nil {
			return fmt.Errorf("start agent: %w", err)
		}
		if err := a.agentRuntime.Start(svcCtx, identity); err != nil {
			return fmt.Errorf("start agent: %w", err)
		}
	}

	state := a.agentRuntime.Status(agentID)
	containerShort := shortID(state.ContainerID)
	a.logger.Info("agent started via mutation", "agent", agentID, "container", containerShort, "by", u.ID)
	return w.SetJSON(map[string]any{
		"id":           agentID,
		"status":       state.Status,
		"container_id": containerShort,
	})
}

// handleStopAgent is an owner-gated desired-state WRITER: it flips agents.status
// to 'paused' and kicks a converge that stops the container (spec §4). It refuses
// to touch an admin-'disabled' agent — otherwise an owner could stop→paused then
// start→active and bypass an admin revocation.
func (a *HubApp) handleStopAgent(w *app.Result, r *app.Request) error {
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	agentID := r.String("agent_id")
	if agentID == "" {
		return fmt.Errorf("agent_id is required")
	}
	if a.agentRuntime == nil {
		return fmt.Errorf("docker runtime unavailable")
	}

	if err := a.checkAgentAccess(withIdentity(r.Context(), u), u, agentID, "owner"); err != nil {
		return err
	}

	// Privileged Agent-DB read/write as the service principal (RLS-exempt).
	svcCtx := r.Context()
	rec, err := a.readAgentRecord(svcCtx, agentID)
	if err != nil {
		return fmt.Errorf("stop agent: %w", err)
	}
	if rec.Status == "disabled" {
		return fmt.Errorf("agent %q is disabled; only an administrator can change its run state", agentID)
	}

	if rec.Status == "manual" {
		// Manual is hands-off: stop the container but keep status 'manual' (do NOT
		// flip to 'paused' — that would make it supervisor-managed). Serialized
		// against the tick via supervisor.stopManual. A later start_agent relaunches.
		if a.supervisor != nil {
			_ = a.supervisor.stopManual(svcCtx, agentID)
		} else {
			_ = a.agentRuntime.Stop(svcCtx, agentID)
		}
		a.logger.Info("manual agent stopped via mutation", "agent", agentID, "by", u.ID)
		return w.SetJSON(map[string]any{
			"id":           agentID,
			"status":       "manual",
			"container_id": "",
		})
	}

	if rec.Status != "paused" {
		// Conditional flip (see setAgentRunState) — the filter excludes a
		// 'disabled' row, so an owner stop can never move a revoked agent to
		// 'paused' (which would then be owner-startable), even under a race.
		if err := a.setAgentRunState(svcCtx, agentID, "paused"); err != nil {
			return fmt.Errorf("stop agent: set status paused: %w", err)
		}
	}
	if a.supervisor != nil {
		a.supervisor.kick(svcCtx, agentID, "paused")
	} else {
		_ = a.agentRuntime.Stop(svcCtx, agentID)
	}

	a.logger.Info("agent stopped via mutation", "agent", agentID, "by", u.ID)
	return w.SetJSON(map[string]any{
		"id":           agentID,
		"status":       "paused",
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

	// The admin gate impersonates the caller to evaluate their capability.
	if err := a.requireAdmin(withIdentity(r.Context(), u), u); err != nil {
		return err
	}

	// Everything below is a PRIVILEGED hub op — read/write the Agent DB + stop the
	// container as the service principal (admin, RLS-exempt), NOT impersonating the
	// caller: a non-admin owner's role is denied the Agent DB by the HB3 RLS floor.
	svcCtx := r.Context()

	// Stop the runtime first (idempotent — no error if not running).
	if a.agentRuntime != nil {
		_ = a.agentRuntime.Stop(svcCtx, agentID)
	}

	// Delete the identity from the AGENT DB (hub.agent.db.agents, the canon) — NOT
	// the platform hub.db.agents duplicate. Deleting the platform row left the real
	// identity alive, so /agent/token kept minting and the supervisor would keep
	// reviving the container (the bug the O2 smoke exposed live).
	delRes, err := a.client.Query(svcCtx,
		`mutation($id: String!) { hub { agent { db { delete_agents(
			filter: { id: { eq: $id } }
		) { affected_rows } } } } }`,
		map[string]any{"id": agentID},
	)
	if err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}
	defer delRes.Close()
	if delRes.Err() != nil {
		return fmt.Errorf("delete agent: %w", delRes.Err())
	}

	// Delete the owner grants — user_agents lives in the platform Hub DB
	// (hub.db.user_agents), not the Agent DB. chats.agent_id FK is ON DELETE SET
	// NULL, so chat threads detach themselves.
	if accessRes, err := a.client.Query(svcCtx,
		`mutation($aid: String!) { hub { db { delete_user_agents(
			filter: { agent_id: { eq: $aid } }
		) { affected_rows } } } }`,
		map[string]any{"aid": agentID},
	); err == nil {
		accessRes.Close()
	}

	// Best-effort: invalidate any unconsumed bootstrap secrets so a stale spawn
	// credential can never be redeemed after deletion (belt-and-suspenders — a
	// deleted agent's secret already 403s at agentForToken since the row is gone).
	if err := a.invalidatePriorBootstrapSecrets(svcCtx, agentID); err != nil {
		a.logger.Warn("delete agent: invalidate bootstrap secrets failed (non-fatal)", "agent", agentID, "error", err)
	}

	// Drop supervisor tracking. The orphan sweep only forgets agents whose
	// container it removes, so a deleted agent with no running container (e.g. a
	// paused one) would otherwise leak its agentTrack forever.
	if a.supervisor != nil {
		a.supervisor.forget(agentID)
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

// setAgentRunState flips an agent's run-state to target ('active' or 'paused')
// ONLY when it is currently an owner-writable state — the filter status ∈
// {active,paused} EXCLUDES a 'disabled' row, so an owner start/stop can never
// reactivate or alter an admin-revoked agent, even under a read→write race
// (spec §4 authority invariant). hugr reports affected_rows unreliably (postgres
// source quirk), so a caller that needs certainty re-reads the status.
func (a *HubApp) setAgentRunState(ctx context.Context, agentID, target string) error {
	res, err := a.client.Query(ctx,
		`mutation($id: String!, $data: hub_agent_db_agents_mut_data!) {
			hub { agent { db { update_agents(
				filter: { id: { eq: $id }, status: { in: ["active", "paused"] } }
				data: $data
			) { affected_rows } } } } }`,
		map[string]any{"id": agentID, "data": map[string]any{"status": target}},
	)
	if err != nil {
		return err
	}
	defer res.Close()
	return res.Err()
}

// agentIdentity reads an agent's record (service principal) and derives its
// container spawn identity. Shared by start_agent and the supervisor.
func (a *HubApp) agentIdentity(ctx context.Context, agentID string) (agentmgr.AgentIdentity, error) {
	rec, err := a.readAgentRecord(ctx, agentID)
	if err != nil {
		return agentmgr.AgentIdentity{}, err
	}
	return agentIdentityFromRecord(rec)
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
		Manual:       rec.Status == "manual",
	}, nil
}
