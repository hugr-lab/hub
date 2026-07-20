package agentmgr

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
)

// agentAPIPort is the in-container hugen API port (HUGEN_API_PORT, baked in the
// image). Health probing + the optional dev publish flag reference it.
const agentAPIPort = "10200/tcp"

// DockerRuntime manages agent containers via Docker API.
// Runtime state lives in memory maps, reconstructed on startup.
type DockerRuntime struct {
	mu     sync.RWMutex
	states map[string]*RuntimeState // agentID → state

	docker     *dockerclient.Client
	cfg        RuntimeConfig
	mintSecret SecretMinter
	logger     *slog.Logger
}

// NewDockerRuntime builds a DockerRuntime from the spawn config. The secret
// minter is injected separately (SetSecretMinter) because the agent-token
// issuer it wraps is wired later in Init, after this runtime is created in
// Catalog().
func NewDockerRuntime(cfg RuntimeConfig, logger *slog.Logger) (*DockerRuntime, error) {
	cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	rt := &DockerRuntime{
		states: make(map[string]*RuntimeState),
		docker: cli,
		cfg:    cfg,
		logger: logger,
	}
	if cfg.PublishAPI {
		// The hub's authz model rests on agents being reachable ONLY through the
		// hub (their API port lives on the internal agent network). Publishing a
		// host port breaks that guarantee — a user could hit the agent directly,
		// bypassing the hub's agent-access gate. Dev convenience only.
		logger.Warn("HUB_AGENT_PUBLISH_API is set — agent API ports are published to the host, bypassing the hub-only network guarantee; DEV ONLY, never enable in production")
	}
	return rt, nil
}

// SetSecretMinter injects the spawn-secret minter. Start fails loudly when it is
// nil (the agent-token issuer is disabled — an agent cannot boot without a
// redeemable bootstrap secret).
func (rt *DockerRuntime) SetSecretMinter(m SecretMinter) {
	rt.mu.Lock()
	rt.mintSecret = m
	rt.mu.Unlock()
}

// Reconstruct scans existing containers with hub.managed=true label on startup
// and rebuilds the in-memory state map — the source of hub-restart survival
// (spec-agent-orchestration §4). Scans ALL containers (including exited) so the
// supervisor's first tick sees stopped/crashed agents too.
func (rt *DockerRuntime) Reconstruct(ctx context.Context) {
	containers, err := rt.docker.ContainerList(ctx, container.ListOptions{
		All:     true, // include exited containers
		Filters: filters.NewArgs(filters.Arg("label", "hub.managed=true")),
	})
	if err != nil {
		rt.logger.Warn("failed to scan managed containers", "error", err)
		return
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for _, c := range containers {
		agentID := c.Labels["hub.agent-id"]
		if agentID == "" {
			continue
		}
		status := "running"
		if c.State != "running" {
			status = "stopped"
		}
		// Recover the published API host port (dev publish flag) — without it a
		// hub restart would silently downgrade APIBaseURL to the in-network DNS
		// name, unreachable from a dev host.
		hostPort := ""
		for _, p := range c.Ports {
			if p.PublicPort != 0 && string(p.Type) == "tcp" &&
				strconv.Itoa(int(p.PrivatePort)) == nat.Port(agentAPIPort).Port() {
				hostPort = strconv.Itoa(int(p.PublicPort))
				break
			}
		}
		state := &RuntimeState{
			AgentID:     agentID,
			DisplayName: c.Labels["hub.agent-name"],
			AgentTypeID: c.Labels["hub.agent-type"],
			ContainerID: c.ID,
			HostPort:    hostPort,
			Status:      status,
			StartedAt:   time.Unix(c.Created, 0),
		}
		rt.states[agentID] = state
		rt.logger.Info("reconstructed agent state", "agent", agentID, "status", status, "container", c.ID[:12])
	}
}

func (rt *DockerRuntime) Start(ctx context.Context, agent AgentIdentity) error {
	rt.mu.Lock()
	if existing, ok := rt.states[agent.ID]; ok && existing.Status == "running" {
		rt.mu.Unlock()
		return nil // already running, idempotent — no wasted secret
	}
	minter := rt.mintSecret
	rt.mu.Unlock()

	// Fail before touching Docker: a container that can neither redeem a secret
	// (no minter) nor pass hugen's fail-closed API auth (no issuer) would only
	// crash-loop.
	if minter == nil {
		return fmt.Errorf("agent %q: secret minter not configured (agent token issuer disabled?)", agent.ID)
	}
	if rt.cfg.HugrIssuer == "" {
		return fmt.Errorf("agent %q: HUB_AGENT_HUGR_ISSUER is required (hugen serve fails closed without HUGR_ISSUER)", agent.ID)
	}
	if agent.Image == "" {
		return fmt.Errorf("agent %q: no container image (agent_type.config.orchestration.image is empty)", agent.ID)
	}

	// Mint a fresh one-shot secret; this invalidates the agent's prior
	// unconsumed secrets hub-side, so a recreate orphans the previous
	// container's credential immediately.
	secret, _, err := minter(ctx, agent.ID)
	if err != nil {
		return fmt.Errorf("mint bootstrap secret: %w", err)
	}

	// Single persistent /data bind (holds the agent JWT → restart survival).
	var dataDir string
	if rt.cfg.StoragePath != "" {
		dataDir = filepath.Join(rt.cfg.StoragePath, "agents", agent.ID)
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			return fmt.Errorf("create agent data dir: %w", err)
		}
	}

	containerName := containerNameFor(agent.ID)
	// Remove any stale container with the same name (a prior crashed spawn).
	_ = rt.docker.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true})

	envMap := map[string]string{
		"HUGR_URL":          rt.cfg.HugrURL,
		"HUGR_ISSUER":       rt.cfg.HugrIssuer,
		"HUGR_ACCESS_TOKEN": secret,
		"HUGR_TOKEN_URL":    rt.cfg.TokenURL,
	}
	if rt.cfg.LogLevel != "" {
		envMap["HUGEN_LOG_LEVEL"] = rt.cfg.LogLevel
	}
	// Overlay the agent_type's orchestration.env (e.g. HUGEN_LOG_LEVEL, LLM keys).
	// The hub-owned remote-mode credentials are reserved — an agent type can never
	// override them (a stale/forged token would break the agent's identity).
	reserved := map[string]bool{"HUGR_URL": true, "HUGR_ISSUER": true, "HUGR_ACCESS_TOKEN": true, "HUGR_TOKEN_URL": true}
	for k, v := range agent.Env {
		if k == "" || reserved[k] {
			continue
		}
		envMap[k] = v
	}
	env := make([]string, 0, len(envMap))
	for k, v := range envMap {
		env = append(env, k+"="+v)
	}

	containerCfg := &container.Config{
		Image: agent.Image,
		Env:   env,
		Labels: map[string]string{
			"hub.managed":    "true",
			"hub.agent-id":   agent.ID,
			"hub.agent-type": agent.AgentTypeID,
			"hub.agent-name": agent.DisplayName,
		},
	}

	// 'active' agents get unless-stopped (Docker revives a bare process crash —
	// M5 row 1). A 'manual' agent is hands-off: restart-policy 'no' so a crash
	// stays down until an explicit start_agent relaunches it (spec §4).
	restartPolicy := container.RestartPolicy{Name: container.RestartPolicyUnlessStopped}
	if agent.Manual {
		restartPolicy = container.RestartPolicy{Name: container.RestartPolicyDisabled}
	}
	hostCfg := &container.HostConfig{
		NetworkMode:   container.NetworkMode(rt.cfg.Network),
		RestartPolicy: restartPolicy,
		Resources:     rt.resourceLimits(agent),
	}
	if dataDir != "" {
		hostCfg.Mounts = []mount.Mount{{
			Type:   mount.TypeBind,
			Source: dataDir,
			Target: "/data",
		}}
	}
	if rt.cfg.PublishAPI {
		// Dev only: publish the API on an ephemeral host port (empty HostPort).
		containerCfg.ExposedPorts = nat.PortSet{agentAPIPort: struct{}{}}
		hostCfg.PortBindings = nat.PortMap{agentAPIPort: []nat.PortBinding{{HostPort: ""}}}
	}

	resp, err := rt.docker.ContainerCreate(ctx, containerCfg, hostCfg, nil, nil, containerName)
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}

	if err := rt.docker.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = rt.docker.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return fmt.Errorf("start container: %w", err)
	}

	// Learn the ephemeral host port assignment (dev publish flag only).
	hostPort := ""
	if rt.cfg.PublishAPI {
		if insp, err := rt.docker.ContainerInspect(ctx, resp.ID); err != nil {
			rt.logger.Warn("inspect for published port failed", "agent", agent.ID, "error", err)
		} else {
			hostPort = firstHostPort(insp.NetworkSettings.Ports, agentAPIPort)
		}
	}

	state := &RuntimeState{
		AgentID:     agent.ID,
		DisplayName: agent.DisplayName,
		AgentTypeID: agent.AgentTypeID,
		ContainerID: resp.ID,
		HostPort:    hostPort,
		Status:      "running",
		StartedAt:   time.Now(),
	}

	rt.mu.Lock()
	rt.states[agent.ID] = state
	rt.mu.Unlock()

	rt.logger.Info("agent started", "agent", agent.ID, "container", resp.ID[:12], "host_port", hostPort)
	return nil
}

// resourceLimits builds the container resource caps: agent_type orchestration
// values win, else the runtime defaults, else unlimited (0 / nil).
func (rt *DockerRuntime) resourceLimits(agent AgentIdentity) container.Resources {
	mem := agent.MemoryBytes
	if mem == 0 {
		mem = rt.cfg.DefaultMemoryBytes
	}
	cpus := agent.NanoCPUs
	if cpus == 0 {
		cpus = rt.cfg.DefaultNanoCPUs
	}
	pids := agent.PidsLimit
	if pids == 0 {
		pids = rt.cfg.DefaultPidsLimit
	}
	res := container.Resources{Memory: mem, NanoCPUs: cpus}
	if pids > 0 {
		res.PidsLimit = &pids
	}
	return res
}

func (rt *DockerRuntime) Stop(ctx context.Context, agentID string) error {
	rt.mu.Lock()
	state, ok := rt.states[agentID]
	if !ok {
		rt.mu.Unlock()
		return nil // not running
	}
	delete(rt.states, agentID)
	rt.mu.Unlock()

	timeout := 10
	if err := rt.docker.ContainerStop(ctx, state.ContainerID, container.StopOptions{Timeout: &timeout}); err != nil {
		rt.logger.Warn("stop failed, force removing", "agent", agentID, "error", err)
	}
	if err := rt.docker.ContainerRemove(ctx, state.ContainerID, container.RemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("remove container: %w", err)
	}

	rt.logger.Info("agent stopped", "agent", agentID, "container", state.ContainerID[:12])
	return nil
}

func (rt *DockerRuntime) Status(agentID string) RuntimeState {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	if state, ok := rt.states[agentID]; ok {
		return *state
	}
	return RuntimeState{AgentID: agentID, Status: "stopped"}
}

func (rt *DockerRuntime) ListRunning() []RuntimeState {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	result := make([]RuntimeState, 0, len(rt.states))
	for _, s := range rt.states {
		result = append(result, *s)
	}
	return result
}

// Observation is a point-in-time view of an agent's managed container, read via
// ContainerInspect on every supervisor tick. It is the source of truth the
// in-memory states map is NOT — states mutates only on Start/Stop, so it cannot
// see a crash, a restart loop, or an unhealthy healthcheck. Health is the raw
// Docker healthcheck status ("", "none", "starting", "healthy", "unhealthy").
type Observation struct {
	Present      bool   // a container named hub-agent-<id> exists (any state)
	Running      bool   // State.Running
	Restarting   bool   // State.Restarting (mid restart-policy bounce)
	Health       string // State.Health.Status ("" when the image declares no healthcheck / not yet reported)
	RestartCount int    // daemon restart counter — grows while a crash loop bounces the container
}

// Observe inspects the agent's managed container by name. A missing container is
// NOT an error — it yields Present:false so the desired-state loop decides to
// Start rather than treating absence as a failure.
func (rt *DockerRuntime) Observe(ctx context.Context, agentID string) (Observation, error) {
	insp, err := rt.docker.ContainerInspect(ctx, containerNameFor(agentID))
	if err != nil {
		if dockerclient.IsErrNotFound(err) {
			return Observation{Present: false}, nil
		}
		return Observation{}, err
	}
	obs := Observation{Present: true, RestartCount: insp.RestartCount}
	if insp.State != nil {
		obs.Running = insp.State.Running
		obs.Restarting = insp.State.Restarting
		if insp.State.Health != nil {
			obs.Health = insp.State.Health.Status
		}
	}
	return obs, nil
}

// ManagedRef identifies a hub-managed container by its agent id — what the
// supervisor diffs against the Agent-DB agent set to find orphans.
type ManagedRef struct {
	AgentID     string
	ContainerID string
	Running     bool
}

// ListManaged returns every container carrying the hub.managed label (including
// exited ones). The supervisor subtracts the live Agent-DB agent set to find
// orphans — managed containers whose identity row is gone (e.g. a deleted agent).
func (rt *DockerRuntime) ListManaged(ctx context.Context) ([]ManagedRef, error) {
	containers, err := rt.docker.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", "hub.managed=true")),
	})
	if err != nil {
		return nil, err
	}
	refs := make([]ManagedRef, 0, len(containers))
	for _, c := range containers {
		id := c.Labels["hub.agent-id"]
		if id == "" {
			continue
		}
		refs = append(refs, ManagedRef{AgentID: id, ContainerID: c.ID, Running: c.State == "running"})
	}
	return refs, nil
}

// Remove force-removes the agent's container (by name) and drops its in-memory
// state. Used to evict an orphan (no Agent-DB row) and to clear a stuck/unhealthy
// container before a fresh recreate (Start's running-guard would otherwise no-op
// on a still-"running" but unhealthy container). Idempotent — a missing
// container is not an error.
func (rt *DockerRuntime) Remove(ctx context.Context, agentID string) error {
	rt.mu.Lock()
	delete(rt.states, agentID)
	rt.mu.Unlock()
	if err := rt.docker.ContainerRemove(ctx, containerNameFor(agentID), container.RemoveOptions{Force: true}); err != nil {
		if dockerclient.IsErrNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}

// containerNameFor is the single derivation of an agent's container name — also
// its DNS host on the agent network. agent_id is validated at create_agent
// (^[a-z0-9][a-z0-9-]{0,40}$) so this is always a safe Docker name / DNS label.
func containerNameFor(agentID string) string {
	return "hub-agent-" + agentID
}

// Logs returns the tail of the agent container's combined stdout+stderr.
func (rt *DockerRuntime) Logs(ctx context.Context, agentID string, tail int) (string, error) {
	if tail <= 0 {
		tail = 200
	}
	rc, err := rt.docker.ContainerLogs(ctx, containerNameFor(agentID), container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       strconv.Itoa(tail),
	})
	if err != nil {
		return "", fmt.Errorf("container logs %q: %w", agentID, err)
	}
	defer rc.Close()
	// Non-TTY container logs are multiplexed (8-byte stream headers per chunk);
	// demux stdout+stderr into one plain-text buffer.
	var buf bytes.Buffer
	if _, err := stdcopy.StdCopy(&buf, &buf, rc); err != nil && err != io.EOF {
		return buf.String(), fmt.Errorf("read container logs %q: %w", agentID, err)
	}
	return buf.String(), nil
}

// APIBaseURL resolves the dial target for the agent's HTTP API from the local
// state cache: the published host port when the container has one
// (HUB_AGENT_PUBLISH_API — the hub runs on the host), else the container-name
// DNS on the agent network (compose/prod — the hub is a container on the same
// network; a dev host without the publish flag gets a connection error the
// gateway maps downstream). A stopped container still resolves — the dial
// failure carries the real signal.
func (rt *DockerRuntime) APIBaseURL(agentID string) (string, error) {
	rt.mu.RLock()
	st, ok := rt.states[agentID]
	rt.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("agent %q: no container", agentID)
	}
	if st.HostPort != "" {
		return "http://127.0.0.1:" + st.HostPort, nil
	}
	return "http://" + containerNameFor(agentID) + ":" + nat.Port(agentAPIPort).Port(), nil
}

// firstHostPort returns the first host-side port bound to the given container
// port, or "" if none is published yet.
func firstHostPort(ports nat.PortMap, port nat.Port) string {
	for _, b := range ports[port] {
		if b.HostPort != "" {
			return b.HostPort
		}
	}
	return ""
}
