package agentmgr

import (
	"context"
	"time"
)

// AgentRuntime manages agent container lifecycle.
// Runtime state (container_id, auth_token, status) lives in memory.
// Identity (display_name, hugr_*) comes from DB (agents table).
type AgentRuntime interface {
	// Start creates and starts a container for the agent.
	Start(ctx context.Context, agent AgentIdentity) error

	// Stop stops and removes the agent's container.
	Stop(ctx context.Context, agentID string) error

	// Status returns the current runtime state for an agent.
	Status(agentID string) RuntimeState

	// ValidateToken checks if token belongs to a running agent.
	ValidateToken(token string) (agentID string, ok bool)

	// ListRunning returns runtime state for all currently running agents.
	ListRunning() []RuntimeState
}

// AgentIdentity contains persistent agent data from DB.
type AgentIdentity struct {
	ID           string
	AgentTypeID  string
	DisplayName  string
	HugrUserID   string
	HugrUserName string
	HugrRole     string
	Image        string // from agent_types
}

// RuntimeState contains ephemeral state for a running agent.
type RuntimeState struct {
	AgentID     string    `json:"agent_id"`
	DisplayName string    `json:"display_name"`
	AgentTypeID string    `json:"agent_type_id"`
	ContainerID string    `json:"container_id"`
	AuthToken   string    `json:"-"` // never expose in API
	Status      string    `json:"status"` // starting, running, error, stopped
	StartedAt   time.Time `json:"started_at"`
}
