package agentmgr

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	dockerclient "github.com/docker/docker/client"
)

// DockerBackend manages agent containers via Docker API.
type DockerBackend struct {
	client  *dockerclient.Client
	network string
}

func NewDockerBackend(network string) (*DockerBackend, error) {
	cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &DockerBackend{client: cli, network: network}, nil
}

func (b *DockerBackend) Create(ctx context.Context, cfg AgentConfig) (string, error) {
	env := []string{
		fmt.Sprintf("HUB_SERVICE_MCP_URL=%s", cfg.MCPURL),
	}
	for k, v := range cfg.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	var mounts []mount.Mount
	for _, m := range cfg.Mounts {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeVolume,
			Source: m.Source,
			Target: m.Target,
		})
	}

	// Remove existing container with same name (stale from previous run)
	containerName := fmt.Sprintf("hub-agent-%s", cfg.UserID)
	_ = b.client.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true})

	resp, err := b.client.ContainerCreate(ctx,
		&container.Config{
			Image: cfg.Image,
			Env:   env,
			Labels: map[string]string{
				"hub.user_id":       cfg.UserID,
				"hub.agent_type_id": cfg.AgentTypeID,
				"hub.managed":       "true",
			},
		},
		&container.HostConfig{
			Mounts:      mounts,
			NetworkMode: container.NetworkMode(b.network),
		},
		nil, nil,
		containerName,
	)
	if err != nil {
		return "", fmt.Errorf("container create: %w", err)
	}

	return resp.ID, nil
}

func (b *DockerBackend) Start(ctx context.Context, containerID string) error {
	return b.client.ContainerStart(ctx, containerID, container.StartOptions{})
}

func (b *DockerBackend) Stop(ctx context.Context, containerID string) error {
	timeout := 10
	return b.client.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout})
}

func (b *DockerBackend) Remove(ctx context.Context, containerID string) error {
	return b.client.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
}

func (b *DockerBackend) Status(ctx context.Context, containerID string) (AgentStatus, error) {
	info, err := b.client.ContainerInspect(ctx, containerID)
	if err != nil {
		return AgentStatus{}, fmt.Errorf("inspect: %w", err)
	}

	status := "stopped"
	if info.State.Running {
		status = "running"
	} else if info.State.Restarting {
		status = "creating"
	}

	return AgentStatus{
		ContainerID: containerID,
		Status:      status,
	}, nil
}
