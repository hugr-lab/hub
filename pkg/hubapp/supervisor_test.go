package hubapp

import (
	"testing"
	"time"

	"github.com/hugr-lab/hub/pkg/agentmgr"
)

// running is a healthy, running container observation.
func running() agentmgr.Observation {
	return agentmgr.Observation{Present: true, Running: true, Health: "healthy"}
}

func TestDecide_ActiveStartAndConverge(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)

	// Absent container under desired=active → start.
	tr := &agentTrack{}
	if got := decide("active", agentmgr.Observation{Present: false}, tr, now); got != actStart {
		t.Fatalf("absent+active: got %v, want actStart", got)
	}

	// Exited (present but not running, not restarting) → start.
	tr = &agentTrack{}
	if got := decide("active", agentmgr.Observation{Present: true, Running: false}, tr, now); got != actStart {
		t.Fatalf("exited+active: got %v, want actStart", got)
	}

	// Mid restart-policy bounce (not running but Restarting) → leave it (actNone).
	tr = &agentTrack{}
	if got := decide("active", agentmgr.Observation{Present: true, Running: false, Restarting: true}, tr, now); got != actNone {
		t.Fatalf("restarting+active: got %v, want actNone", got)
	}

	// Healthy + running → converged.
	tr = &agentTrack{}
	if got := decide("active", running(), tr, now); got != actNone {
		t.Fatalf("healthy+active: got %v, want actNone", got)
	}
}

func TestDecide_UnhealthyNeedsConsecutiveTicks(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tr := &agentTrack{}
	unhealthy := agentmgr.Observation{Present: true, Running: true, Health: "unhealthy"}

	// First unhealthy tick: streak=1 < trip → no recreate yet.
	if got := decide("active", unhealthy, tr, now); got != actNone {
		t.Fatalf("unhealthy tick 1: got %v, want actNone", got)
	}
	if tr.unhealthyStreak != 1 {
		t.Fatalf("streak after tick 1: got %d, want 1", tr.unhealthyStreak)
	}
	// Second consecutive unhealthy tick reaches the trip → recreate.
	if got := decide("active", unhealthy, tr, now); got != actRecreate {
		t.Fatalf("unhealthy tick 2: got %v, want actRecreate", got)
	}
	// decide consumes the streak for the recreate it just ordered.
	if tr.unhealthyStreak != 0 {
		t.Fatalf("streak after recreate: got %d, want 0", tr.unhealthyStreak)
	}
}

func TestDecide_StartingIsNotUnhealthy(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tr := &agentTrack{unhealthyStreak: 1} // one prior unhealthy tick

	// A `starting` health holds the streak (slow boot ≠ failure) — no recreate,
	// and it must NOT reset the streak either.
	got := decide("active", agentmgr.Observation{Present: true, Running: true, Health: "starting"}, tr, now)
	if got != actNone {
		t.Fatalf("starting: got %v, want actNone", got)
	}
	if tr.unhealthyStreak != 1 {
		t.Fatalf("starting must hold streak: got %d, want 1", tr.unhealthyStreak)
	}

	// A subsequent healthy tick clears it.
	if got := decide("active", running(), tr, now); got != actNone {
		t.Fatalf("healthy after starting: got %v, want actNone", got)
	}
	if tr.unhealthyStreak != 0 {
		t.Fatalf("healthy must reset streak: got %d, want 0", tr.unhealthyStreak)
	}
}

func TestDecide_CrashLoopRecreates(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	tr := &agentTrack{}

	// First observation seeds the restart window baseline (RestartCount 0).
	if got := decide("active", agentmgr.Observation{Present: true, Running: true, Health: "healthy", RestartCount: 0}, tr, t0); got != actNone {
		t.Fatalf("window seed: got %v, want actNone", got)
	}
	// Within the window the restart counter jumped by ≥ crashLoopDelta → recreate.
	within := t0.Add(time.Minute)
	if got := decide("active", agentmgr.Observation{Present: true, Running: true, Health: "healthy", RestartCount: 3}, tr, within); got != actRecreate {
		t.Fatalf("crash loop: got %v, want actRecreate", got)
	}
}

func TestDecide_BackoffSuppressesAction(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tr := &agentTrack{}
	bumpBackoff(tr, now) // nextRecreate = now+1m

	// Absent + active would normally start, but an active backoff suppresses it.
	if got := decide("active", agentmgr.Observation{Present: false}, tr, now.Add(30*time.Second)); got != actNone {
		t.Fatalf("within backoff: got %v, want actNone", got)
	}
	// After the backoff window elapses, the start proceeds.
	if got := decide("active", agentmgr.Observation{Present: false}, tr, now.Add(2*time.Minute)); got != actStart {
		t.Fatalf("after backoff: got %v, want actStart", got)
	}
}

func TestDecide_PausedAndDisabledStop(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	for _, desired := range []string{"paused", "disabled"} {
		// Present → stop.
		tr := &agentTrack{unhealthyStreak: 2, backoff: 5 * time.Minute}
		if got := decide(desired, running(), tr, now); got != actStop {
			t.Fatalf("%s+present: got %v, want actStop", desired, got)
		}
		// Stopping resets tracking so a later re-activate starts clean.
		if tr.unhealthyStreak != 0 || tr.backoff != 0 {
			t.Fatalf("%s must reset tracking: streak=%d backoff=%v", desired, tr.unhealthyStreak, tr.backoff)
		}
		// Absent → nothing to do.
		if got := decide(desired, agentmgr.Observation{Present: false}, &agentTrack{}, now); got != actNone {
			t.Fatalf("%s+absent: got %v, want actNone", desired, got)
		}
	}
}

func TestDecide_ManualIsHandsOff(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)

	// Running/healthy → leave it (never stop a manually-launched container) and
	// reset any stale tracking so a later flip to 'active' starts clean.
	tr := &agentTrack{unhealthyStreak: 2, backoff: 5 * time.Minute}
	if got := decide("manual", running(), tr, now); got != actNone {
		t.Fatalf("manual+running: got %v, want actNone", got)
	}
	if tr.unhealthyStreak != 0 || tr.backoff != 0 {
		t.Fatalf("manual must reset tracking: streak=%d backoff=%v", tr.unhealthyStreak, tr.backoff)
	}

	// Absent → do NOT auto-start (the whole point of manual).
	if got := decide("manual", agentmgr.Observation{Present: false}, &agentTrack{}, now); got != actNone {
		t.Fatalf("manual+absent: got %v, want actNone (no auto-start)", got)
	}
	// Exited → stays down (restart-policy 'no' at spawn; supervisor never restarts).
	if got := decide("manual", agentmgr.Observation{Present: true, Running: false}, &agentTrack{}, now); got != actNone {
		t.Fatalf("manual+exited: got %v, want actNone (no restart)", got)
	}
	// Unhealthy → still hands-off (no recreate).
	unhealthy := agentmgr.Observation{Present: true, Running: true, Health: "unhealthy"}
	if got := decide("manual", unhealthy, &agentTrack{}, now); got != actNone {
		t.Fatalf("manual+unhealthy: got %v, want actNone (no recreate)", got)
	}
}

func TestDecide_UnknownStatusNoop(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	if got := decide("banana", running(), &agentTrack{}, now); got != actNone {
		t.Fatalf("unknown status: got %v, want actNone", got)
	}
}

func TestTrackRestart_WindowResetsAfterExpiry(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	tr := &agentTrack{}

	// Seed.
	if trackRestart(tr, agentmgr.Observation{RestartCount: 10}, t0); tr.restartBaseline != 10 {
		t.Fatalf("baseline: got %d, want 10", tr.restartBaseline)
	}
	// Delta 2 within window → not a loop.
	if trackRestart(tr, agentmgr.Observation{RestartCount: 12}, t0.Add(time.Minute)) {
		t.Fatal("delta 2 should not be a crash loop")
	}
	// Delta 3 within window → loop.
	if !trackRestart(tr, agentmgr.Observation{RestartCount: 13}, t0.Add(2*time.Minute)) {
		t.Fatal("delta 3 should be a crash loop")
	}
	// After the window expires, the baseline re-seeds and the count resets.
	if trackRestart(tr, agentmgr.Observation{RestartCount: 20}, t0.Add(crashLoopWindow+time.Second)) {
		t.Fatal("post-window observation should re-seed, not fire")
	}
	if tr.restartBaseline != 20 {
		t.Fatalf("re-seeded baseline: got %d, want 20", tr.restartBaseline)
	}
}

func TestBackoffLifecycle(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tr := &agentTrack{}

	if backoffActive(tr, now) {
		t.Fatal("fresh track must not be backing off")
	}
	bumpBackoff(tr, now)
	if tr.backoff != backoffStart {
		t.Fatalf("first bump: got %v, want %v", tr.backoff, backoffStart)
	}
	if !backoffActive(tr, now.Add(30*time.Second)) {
		t.Fatal("should be backing off 30s in")
	}
	if backoffActive(tr, now.Add(2*time.Minute)) {
		t.Fatal("should be clear 2m in")
	}
	bumpBackoff(tr, now)
	if tr.backoff != 5*time.Minute {
		t.Fatalf("second bump: got %v, want 5m", tr.backoff)
	}
	resetBackoff(tr)
	if tr.backoff != 0 || backoffActive(tr, now) {
		t.Fatalf("reset: backoff=%v active=%v", tr.backoff, backoffActive(tr, now))
	}
}

func TestNextBackoff(t *testing.T) {
	cases := []struct {
		in, want time.Duration
	}{
		{0, backoffStart},
		{backoffStart, 5 * time.Minute},
		{5 * time.Minute, backoffMax},
		{backoffMax, backoffMax}, // cap
	}
	for _, c := range cases {
		if got := nextBackoff(c.in); got != c.want {
			t.Errorf("nextBackoff(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestValidAgentStatus(t *testing.T) {
	for _, s := range []string{"active", "manual", "paused", "disabled"} {
		if !validAgentStatus(s) {
			t.Errorf("validAgentStatus(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "running", "stopped", "Active", "banana"} {
		if validAgentStatus(s) {
			t.Errorf("validAgentStatus(%q) = true, want false", s)
		}
	}
}
