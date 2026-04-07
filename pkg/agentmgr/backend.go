package agentmgr

import "context"

// AgentConfig describes the container to create.
type AgentConfig struct {
	UserID      string
	AgentTypeID string
	Image       string
	MCPURL      string            // Hub Service MCP endpoint for this user
	Env         map[string]string // extra env vars
	Mounts      []Mount
}

// Mount describes a volume mount for the agent container.
type Mount struct {
	Source string
	Target string
}

// AgentStatus describes a running agent instance.
type AgentStatus struct {
	ID          string
	ContainerID string
	Status      string // creating, running, idle, stopping, stopped
}

// Backend is the interface for agent container lifecycle management.
type Backend interface {
	Create(ctx context.Context, cfg AgentConfig) (containerID string, err error)
	Start(ctx context.Context, containerID string) error
	Stop(ctx context.Context, containerID string) error
	Remove(ctx context.Context, containerID string) error
	Status(ctx context.Context, containerID string) (AgentStatus, error)
}
