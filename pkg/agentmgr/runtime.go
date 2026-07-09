package agentmgr

import (
	"context"
	"encoding/json"
	"time"
)

// ImageFromConfig extracts the container image from an agent_type.config JSON
// blob — orchestration fields live under `config.orchestration` (image, cpu,
// mem, mounts), hub-written into the hugen-owned config. Returns "" if absent.
func ImageFromConfig(config json.RawMessage) string {
	if len(config) == 0 {
		return ""
	}
	var c struct {
		Orchestration struct {
			Image string `json:"image"`
		} `json:"orchestration"`
	}
	if err := json.Unmarshal(config, &c); err != nil {
		return ""
	}
	return c.Orchestration.Image
}

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

// AgentIdentity contains persistent agent data from the Agent DB
// (hub.agent.db.agents). DisplayName/HugrRole map to the store's name/role; the
// Hugr principal is the agent itself (HugrUserID/HugrUserName == the agent id/
// name, D8).
type AgentIdentity struct {
	ID           string
	AgentTypeID  string
	DisplayName  string
	HugrUserID   string
	HugrUserName string
	HugrRole     string
	Image        string // agent_type.config.orchestration.image
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
