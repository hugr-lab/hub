package agentmgr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	dockerclient "github.com/docker/docker/client"
)

// DockerRuntime manages agent containers via Docker API.
// Runtime state lives in memory maps, reconstructed on startup.
type DockerRuntime struct {
	mu          sync.RWMutex
	states      map[string]*RuntimeState // agentID → state
	tokenIndex  map[string]string        // token → agentID

	docker      *dockerclient.Client
	network     string
	storagePath string
	hubURL      string // Hub Service internal URL
	logger      *slog.Logger
}

func NewDockerRuntime(network, storagePath, hubURL string, logger *slog.Logger) (*DockerRuntime, error) {
	cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	rt := &DockerRuntime{
		states:      make(map[string]*RuntimeState),
		tokenIndex:  make(map[string]string),
		docker:      cli,
		network:     network,
		storagePath: storagePath,
		hubURL:      hubURL,
		logger:      logger,
	}
	return rt, nil
}

// Reconstruct scans existing containers with hub.managed=true label on startup.
func (rt *DockerRuntime) Reconstruct(ctx context.Context) {
	containers, err := rt.docker.ContainerList(ctx, container.ListOptions{
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
		token := c.Labels["hub.agent-token"]
		state := &RuntimeState{
			AgentID:     agentID,
			DisplayName: c.Labels["hub.agent-name"],
			AgentTypeID: c.Labels["hub.agent-type"],
			ContainerID: c.ID,
			AuthToken:   token,
			Status:      "running",
			StartedAt:   time.Unix(c.Created, 0),
		}
		rt.states[agentID] = state
		if token != "" {
			rt.tokenIndex[token] = agentID
		}
		rt.logger.Info("reconstructed agent state", "agent", agentID, "container", c.ID[:12])
	}
}

func (rt *DockerRuntime) Start(ctx context.Context, agent AgentIdentity) error {
	rt.mu.Lock()
	if existing, ok := rt.states[agent.ID]; ok && existing.Status == "running" {
		rt.mu.Unlock()
		return nil // already running, idempotent
	}
	rt.mu.Unlock()

	token, err := generateToken(32)
	if err != nil {
		return fmt.Errorf("generate token: %w", err)
	}

	// Ensure storage directories exist
	if rt.storagePath != "" {
		os.MkdirAll(filepath.Join(rt.storagePath, "agents", agent.ID), 0755)
		os.MkdirAll(filepath.Join(rt.storagePath, "shared", agent.ID), 0755)
	}

	containerName := fmt.Sprintf("hub-agent-%s", agent.ID)
	// Remove stale container with same name
	_ = rt.docker.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true})

	agentWSURL := strings.Replace(rt.hubURL, "https://", "wss://", 1)
	agentWSURL = strings.Replace(agentWSURL, "http://", "ws://", 1)

	env := []string{
		fmt.Sprintf("AGENT_TOKEN=%s", token),
		fmt.Sprintf("AGENT_ROLE=%s", agent.HugrRole),
		fmt.Sprintf("AGENT_INSTANCE_ID=%s", agent.ID),
		fmt.Sprintf("HUB_SERVICE_MCP_URL=%s/mcp/%s", rt.hubURL, agent.HugrUserID),
		fmt.Sprintf("HUB_SERVICE_AGENT_WS=%s", agentWSURL),
	}

	var mounts []mount.Mount
	if rt.storagePath != "" {
		mounts = append(mounts,
			mount.Mount{
				Type:   mount.TypeBind,
				Source: filepath.Join(rt.storagePath, "agents", agent.ID),
				Target: "/home/agent",
			},
			mount.Mount{
				Type:   mount.TypeBind,
				Source: filepath.Join(rt.storagePath, "shared", agent.ID),
				Target: "/shared",
			},
		)
	}

	resp, err := rt.docker.ContainerCreate(ctx,
		&container.Config{
			Image: agent.Image,
			Env:   env,
			Labels: map[string]string{
				"hub.managed":         "true",
				"hub.agent-id":        agent.ID,
				"hub.agent-token":     token,
				"hub.agent-type":      agent.AgentTypeID,
				"hub.agent-name":      agent.DisplayName,
			},
		},
		&container.HostConfig{
			Mounts:      mounts,
			NetworkMode: container.NetworkMode(rt.network),
		},
		nil, nil, containerName,
	)
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}

	if err := rt.docker.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = rt.docker.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return fmt.Errorf("start container: %w", err)
	}

	state := &RuntimeState{
		AgentID:     agent.ID,
		DisplayName: agent.DisplayName,
		AgentTypeID: agent.AgentTypeID,
		ContainerID: resp.ID,
		AuthToken:   token,
		Status:      "running",
		StartedAt:   time.Now(),
	}

	rt.mu.Lock()
	rt.states[agent.ID] = state
	rt.tokenIndex[token] = agent.ID
	rt.mu.Unlock()

	rt.logger.Info("agent started", "agent", agent.ID, "container", resp.ID[:12])
	return nil
}

func (rt *DockerRuntime) Stop(ctx context.Context, agentID string) error {
	rt.mu.Lock()
	state, ok := rt.states[agentID]
	if !ok {
		rt.mu.Unlock()
		return nil // not running
	}
	delete(rt.states, agentID)
	if state.AuthToken != "" {
		delete(rt.tokenIndex, state.AuthToken)
	}
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

func generateToken(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
