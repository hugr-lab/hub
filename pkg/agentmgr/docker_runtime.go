package agentmgr

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

// agentAPIPort is the in-container hugen API port (HUGEN_API_PORT, baked in the
// image). Health probing + the optional dev publish flag reference it.
const agentAPIPort = "10200/tcp"

// DockerRuntime manages agent containers via Docker API.
// Runtime state lives in memory maps, reconstructed on startup.
type DockerRuntime struct {
	mu         sync.RWMutex
	states     map[string]*RuntimeState // agentID → state
	tokenIndex map[string]string        // token → agentID (legacy ADK path — dies in O3)

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
		states:     make(map[string]*RuntimeState),
		tokenIndex: make(map[string]string),
		docker:     cli,
		cfg:        cfg,
		logger:     logger,
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
		state := &RuntimeState{
			AgentID:     agentID,
			DisplayName: c.Labels["hub.agent-name"],
			AgentTypeID: c.Labels["hub.agent-type"],
			ContainerID: c.ID,
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

	containerName := fmt.Sprintf("hub-agent-%s", agent.ID)
	// Remove any stale container with the same name (a prior crashed spawn).
	_ = rt.docker.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true})

	env := []string{
		"HUGR_URL=" + rt.cfg.HugrURL,
		"HUGR_ISSUER=" + rt.cfg.HugrIssuer,
		"HUGR_ACCESS_TOKEN=" + secret,
		"HUGR_TOKEN_URL=" + rt.cfg.TokenURL,
	}
	if rt.cfg.LogLevel != "" {
		env = append(env, "HUGEN_LOG_LEVEL="+rt.cfg.LogLevel)
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

	hostCfg := &container.HostConfig{
		NetworkMode:   container.NetworkMode(rt.cfg.Network),
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
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

// ValidateToken is the legacy ADK in-memory token check; tokenIndex is no longer
// populated (agents authenticate via the user-token/JWT flow). Kept until the O3
// legacy sweep removes the auth-middleware wiring that references it.
func (rt *DockerRuntime) ValidateToken(token string) (string, bool) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	agentID, ok := rt.tokenIndex[token]
	return agentID, ok
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
