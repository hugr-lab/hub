package hubapp

// Desired-state supervisor (spec-agent-orchestration §4). A hub goroutine
// reconciles managed containers to `agents.status`, the single desired state:
//
//	active   → a container must be running          (owner run-state)
//	paused   → no container                         (owner run-state)
//	disabled → no container; admin-only revocation   (only update_agent leaves it)
//
// Observed health (ContainerInspect, not the in-memory states map — that mutates
// only on Start/Stop and cannot see a crash, restart loop, or failing
// healthcheck) drives recreate: an unhealthy or crash-looping container under
// desired=active is torn down and re-spawned with a FRESH bootstrap secret. The
// typical cause is a wiped /data — persisted JWT gone + env secret consumed → the
// container exit/restart-loops (M5 row 3). Recreate is rate-limited per agent
// (backoff 1m → 5m → 15m, reset on healthy) so a permanently-broken agent does
// not hammer Docker.
//
// The supervisor reads the Agent DB with the hub's privileged client (service
// principal) — no RLS concern, authoritative. Combined with Reconstruct() and an
// immediate first pass, it gives hub-restart survival for free (M5 rows 2/4/5).

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/hugr-lab/hub/pkg/agentmgr"
	"github.com/hugr-lab/query-engine/types"
)

const (
	supervisorInterval = 30 * time.Second
	// crashLoopDelta restarts within crashLoopWindow ⇒ a crash loop.
	crashLoopDelta  = 3
	crashLoopWindow = 5 * time.Minute
	// unhealthyTicksTrip consecutive `unhealthy` observations before a recreate
	// (health `starting` is a slow boot, NOT a failure — it neither trips nor
	// resets the streak).
	unhealthyTicksTrip = 2
	backoffStart       = time.Minute
	backoffMax         = 15 * time.Minute
)

// agentTrack is the per-agent supervisor memory the single states map cannot
// hold: the unhealthy streak, the RestartCount window baseline, and the recreate
// backoff. Its own mutex serializes a tick-reconcile and a handler kick for the
// SAME agent (so start/stop_agent and the tick never converge it concurrently).
type agentTrack struct {
	mu sync.Mutex

	unhealthyStreak int

	restartBaseline int // RestartCount at the current window's start
	windowStart     time.Time

	backoff      time.Duration // current recreate backoff (0 = none)
	nextRecreate time.Time     // earliest next Start/recreate attempt
}

type supervisor struct {
	app *HubApp

	mu     sync.Mutex // guards tracks
	tracks map[string]*agentTrack
}

// startSupervisor launches the reconcile goroutine on a background-derived ctx
// (cancelled in Shutdown), so its lifetime is the process, not Init's ctx.
func (a *HubApp) startSupervisor() {
	if a.dockerRuntime == nil {
		return // no Docker → nothing to supervise
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.supervisorCancel = cancel
	a.supervisor = &supervisor{app: a, tracks: map[string]*agentTrack{}}
	go a.supervisor.run(ctx)
}

func (s *supervisor) run(ctx context.Context) {
	s.app.logger.Info("agent supervisor started", "interval", supervisorInterval)
	// First pass immediately — with Reconstruct() this converges hub-restart
	// survival (revive active agents, stop paused/disabled) without a tick wait.
	s.reconcileAll(ctx)
	t := time.NewTicker(supervisorInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.app.logger.Info("agent supervisor stopped")
			return
		case <-t.C:
			s.reconcileAll(ctx)
		}
	}
}

// reconcileAll converges every Agent-DB agent to its desired status, then sweeps
// orphan containers (hub.managed with no agent row).
func (s *supervisor) reconcileAll(ctx context.Context) {
	agents, err := s.app.listAgentsForSupervision(ctx)
	if err != nil {
		s.app.logger.Warn("supervisor: list agents failed", "error", err)
		return
	}
	desired := make(map[string]bool, len(agents))
	for _, ag := range agents {
		desired[ag.ID] = true
		s.reconcileAgent(ctx, ag.ID, ag.Status)
	}

	// Orphan rule (§4.4): a hub.managed container with NO Agent-DB row → stop +
	// remove. Pairs with delete_agent — otherwise a deleted agent's container
	// (still labelled hub.managed) would be revived/kept forever.
	managed, err := s.app.dockerRuntime.ListManaged(ctx)
	if err != nil {
		s.app.logger.Warn("supervisor: list managed containers failed", "error", err)
		return
	}
	for _, m := range managed {
		if desired[m.AgentID] {
			continue
		}
		s.app.logger.Info("supervisor: removing orphan container (no agent row)",
			"agent", m.AgentID, "container", shortID(m.ContainerID))
		if err := s.app.dockerRuntime.Remove(ctx, m.AgentID); err != nil {
			s.app.logger.Warn("supervisor: orphan remove failed", "agent", m.AgentID, "error", err)
		}
		s.forget(m.AgentID)
	}
}

// reconcileAction is what decide() resolves an observation to. The apply side
// (reconcileAgent) performs the Docker I/O; decide() is pure of it.
type reconcileAction int

const (
	actNone reconcileAction = iota
	actStart
	actRecreate
	actStop
)

// reconcileAgent converges one agent's container to its desired status. Safe to
// call from a handler kick and the tick concurrently — serialized per agent.
// The DECISION is delegated to the pure decide(); this method only performs the
// resulting Docker I/O + logging.
func (s *supervisor) reconcileAgent(ctx context.Context, agentID, desired string) {
	tr := s.trackFor(agentID)
	tr.mu.Lock()
	defer tr.mu.Unlock()

	rt := s.app.dockerRuntime
	obs, err := rt.Observe(ctx, agentID)
	if err != nil {
		s.app.logger.Warn("supervisor: observe failed", "agent", agentID, "error", err)
		return
	}

	switch decide(desired, obs, tr, time.Now()) {
	case actStart:
		switch s.spawn(ctx, agentID) {
		case spawnStarted:
			s.app.logger.Info("supervisor: started agent", "agent", agentID)
			resetBackoff(tr)
		case spawnFailed:
			bumpBackoff(tr, time.Now())
		case spawnSkipped:
			// Desired changed / agent gone under us — the next tick reconciles
			// reality; not a failure, so no backoff.
		}

	case actRecreate:
		s.app.logger.Warn("supervisor: recreating agent", "agent", agentID,
			"restart_count", obs.RestartCount, "unhealthy_streak", tr.unhealthyStreak, "health", obs.Health)
		// Fresh window after a deliberate teardown (streak already reset by decide).
		tr.windowStart = time.Time{}
		if s.spawn(ctx, agentID) != spawnSkipped {
			// A recreate attempt (started OR failed) costs a backoff step; a
			// healthy next tick resets it. A skip (desired changed) does not.
			bumpBackoff(tr, time.Now())
		}

	case actStop:
		s.app.logger.Info("supervisor: stopping agent", "agent", agentID, "desired", desired)
		if err := rt.Stop(ctx, agentID); err != nil {
			s.app.logger.Warn("supervisor: stop failed", "agent", agentID, "error", err)
		}

	case actNone:
	}
}

// spawnResult is the outcome of spawn().
type spawnResult int

const (
	spawnStarted spawnResult = iota // a fresh container was created
	spawnFailed                     // Start attempted but errored → back off
	spawnSkipped                    // desired changed / agent gone → no-op, no back off
)

// spawn (re)creates the agent's container. It guards against two traps the
// review surfaced:
//
//   - STALE DESIRED (M1 / the delete race): the tick's snapshot and a kick's
//     desired can both be stale by the time we act. Re-read the LIVE status and
//     skip if it is no longer 'active' or the agent is gone — a concurrent
//     stop/disable/delete must not be overridden by a spurious start.
//   - STALE STATE (C1): DockerRuntime.Start's idempotency guard trusts the
//     in-memory states cache, which Observe has already shown to be wrong (e.g. a
//     `docker rm` left states[id]=="running"). Remove() first clears that cache
//     entry (and any exited carcass) so Start actually creates a fresh container.
func (s *supervisor) spawn(ctx context.Context, agentID string) spawnResult {
	info, err := s.app.agentForToken(ctx, agentID)
	if err != nil {
		if errors.Is(err, errAgentNotRegistered) {
			s.app.logger.Info("supervisor: skip spawn — agent no longer registered", "agent", agentID)
		} else {
			s.app.logger.Warn("supervisor: skip spawn — status re-read failed", "agent", agentID, "error", err)
		}
		return spawnSkipped
	}
	if info.Status != "active" {
		s.app.logger.Info("supervisor: skip spawn — desired no longer active", "agent", agentID, "status", info.Status)
		return spawnSkipped
	}
	// Clear stale in-memory state + any exited carcass so Start's running-guard
	// cannot no-op on a container Observe already reported absent/exited.
	if err := s.app.dockerRuntime.Remove(ctx, agentID); err != nil {
		s.app.logger.Warn("supervisor: pre-spawn remove failed", "agent", agentID, "error", err)
	}
	if err := s.app.startContainer(ctx, agentID); err != nil {
		s.app.logger.Warn("supervisor: spawn start failed", "agent", agentID, "error", err)
		return spawnFailed
	}
	return spawnStarted
}

// decide advances the per-agent state machine for one observation and returns the
// action to take. It mutates tr (unhealthy streak, restart window, backoff) but
// performs NO Docker I/O, so it is unit-testable with a synthetic Observation +
// injected clock. `now` is threaded in for the same reason.
func decide(desired string, obs agentmgr.Observation, tr *agentTrack, now time.Time) reconcileAction {
	switch desired {
	case "active":
		crashLoop := trackRestart(tr, obs, now)

		switch obs.Health {
		case "unhealthy":
			tr.unhealthyStreak++
		case "healthy", "none", "":
			tr.unhealthyStreak = 0
		case "starting":
			// slow boot ≠ failure — hold the streak (neither grow nor reset)
		}
		unhealthy := tr.unhealthyStreak >= unhealthyTicksTrip

		switch {
		case !obs.Present || (!obs.Running && !obs.Restarting):
			// Absent, or exited and not mid restart-policy bounce → (re)start.
			if backoffActive(tr, now) {
				return actNone
			}
			return actStart
		case unhealthy || crashLoop:
			if backoffActive(tr, now) {
				return actNone
			}
			tr.unhealthyStreak = 0 // consumed by the recreate we are about to do
			return actRecreate
		default:
			// Healthy / running / starting → converged.
			resetBackoff(tr)
			return actNone
		}

	case "paused", "disabled":
		// A stopped agent's tracking is meaningless — reset so a later re-activate
		// starts from a clean backoff/streak.
		resetBackoff(tr)
		tr.unhealthyStreak = 0
		if obs.Present {
			return actStop
		}
		return actNone

	default:
		return actNone // unknown desired status → no-op (logged by caller via listAgents default)
	}
}

// kick runs an immediate single-agent reconcile so a start/stop_agent mutation
// takes effect at once rather than waiting for the next tick. Synchronous — the
// calling request goroutine reads the runtime state right after.
func (s *supervisor) kick(ctx context.Context, agentID, desired string) {
	s.reconcileAgent(ctx, agentID, desired)
}

// trackRestart maintains a sliding RestartCount window and reports whether the
// container bounced ≥ crashLoopDelta times within crashLoopWindow.
func trackRestart(tr *agentTrack, obs agentmgr.Observation, now time.Time) bool {
	if tr.windowStart.IsZero() || now.Sub(tr.windowStart) > crashLoopWindow {
		tr.windowStart = now
		tr.restartBaseline = obs.RestartCount
		return false
	}
	return obs.RestartCount-tr.restartBaseline >= crashLoopDelta
}

func backoffActive(tr *agentTrack, now time.Time) bool {
	return !tr.nextRecreate.IsZero() && now.Before(tr.nextRecreate)
}

func bumpBackoff(tr *agentTrack, now time.Time) {
	tr.backoff = nextBackoff(tr.backoff)
	tr.nextRecreate = now.Add(tr.backoff)
}

func resetBackoff(tr *agentTrack) {
	tr.backoff = 0
	tr.nextRecreate = time.Time{}
}

// nextBackoff steps the recreate backoff 1m → 5m → 15m (cap).
func nextBackoff(cur time.Duration) time.Duration {
	switch {
	case cur < backoffStart:
		return backoffStart
	case cur < 5*time.Minute:
		return 5 * time.Minute
	default:
		return backoffMax
	}
}

func (s *supervisor) trackFor(agentID string) *agentTrack {
	s.mu.Lock()
	defer s.mu.Unlock()
	tr, ok := s.tracks[agentID]
	if !ok {
		tr = &agentTrack{}
		s.tracks[agentID] = tr
	}
	return tr
}

func (s *supervisor) forget(agentID string) {
	s.mu.Lock()
	delete(s.tracks, agentID)
	s.mu.Unlock()
}

// supervisedAgent is the minimal desired-state row the supervisor reconciles on.
type supervisedAgent struct {
	ID     string
	Status string
}

// listAgentsForSupervision reads every agent's id + desired status from the Agent
// DB as the service principal. An empty status defaults to `active` (create_agent
// seeds it, but a hand-inserted row may omit it).
func (a *HubApp) listAgentsForSupervision(ctx context.Context) ([]supervisedAgent, error) {
	res, err := a.client.Query(ctx,
		`{ hub { agent { db { agents { id status } } } } }`, nil)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	if res.Err() != nil {
		return nil, res.Err()
	}
	var rows []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := res.ScanData("hub.agent.db.agents", &rows); err != nil {
		if errors.Is(err, types.ErrNoData) {
			return nil, nil // no agents yet
		}
		return nil, err
	}
	out := make([]supervisedAgent, 0, len(rows))
	for _, r := range rows {
		st := r.Status
		if st == "" {
			st = "active"
		}
		out = append(out, supervisedAgent{ID: r.ID, Status: st})
	}
	return out, nil
}

// startContainer reads the agent identity (Agent DB) and starts its container
// with a fresh mint. Shared by the supervisor and the start_agent kick fallback.
func (a *HubApp) startContainer(ctx context.Context, agentID string) error {
	identity, err := a.agentIdentity(ctx, agentID)
	if err != nil {
		return err
	}
	return a.dockerRuntime.Start(ctx, identity)
}

// shortID truncates a container id for logging.
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
