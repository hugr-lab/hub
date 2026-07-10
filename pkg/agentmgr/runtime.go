package agentmgr

import (
	"context"
	"encoding/json"
	"time"
)

// Orchestration holds the container-shaping fields the hub writes into the
// hugen-owned agent_type.config under `config.orchestration` — the image plus
// optional resource caps. A runaway agent must not starve the host, so the
// spawner applies these when present (spec-agent-orchestration §3); zero means
// "unset → fall back to the runtime default / unlimited".
type Orchestration struct {
	Image       string
	MemoryBytes int64 // Memory limit in bytes (container.Resources.Memory)
	NanoCPUs    int64 // CPU quota in 1e-9 CPUs (container.Resources.NanoCPUs)
	PidsLimit   int64 // Max PIDs (container.Resources.PidsLimit)
}

// OrchestrationFromConfig extracts the orchestration block from an
// agent_type.config JSON blob. Missing / malformed config yields the zero
// Orchestration (empty image, no caps) rather than an error — the caller
// surfaces "no image" downstream.
func OrchestrationFromConfig(config json.RawMessage) Orchestration {
	if len(config) == 0 {
		return Orchestration{}
	}
	var c struct {
		Orchestration struct {
			Image       string `json:"image"`
			MemoryBytes int64  `json:"memory_bytes"`
			NanoCPUs    int64  `json:"nano_cpus"`
			PidsLimit   int64  `json:"pids_limit"`
		} `json:"orchestration"`
	}
	if err := json.Unmarshal(config, &c); err != nil {
		return Orchestration{}
	}
	return Orchestration{
		Image:       c.Orchestration.Image,
		MemoryBytes: c.Orchestration.MemoryBytes,
		NanoCPUs:    c.Orchestration.NanoCPUs,
		PidsLimit:   c.Orchestration.PidsLimit,
	}
}

// ImageFromConfig returns just the container image from an agent_type.config
// blob (thin wrapper over OrchestrationFromConfig — kept for existing callers).
func ImageFromConfig(config json.RawMessage) string {
	return OrchestrationFromConfig(config).Image
}

// SecretMinter mints a fresh one-shot bootstrap secret for an agent at spawn
// time and returns the plaintext + its expiry. The hub implementation
// invalidates the agent's prior unconsumed secrets first, so a recreate orphans
// the credential baked into the previous container's env. Injected at wiring
// (via DockerRuntime.SetSecretMinter) so agentmgr never imports hubapp.
type SecretMinter func(ctx context.Context, agentID string) (secret string, expiresAt time.Time, err error)

// RuntimeConfig carries the spawn knobs the DockerRuntime bakes into every
// agent container — the container network, the persistent storage root, and the
// remote-mode env hugen boots against (spec-agent-orchestration §3).
type RuntimeConfig struct {
	Network     string // user-defined docker network the agent joins (container-name DNS)
	StoragePath string // HOST path root; ${StoragePath}/agents/<id> binds to /data

	// Container-facing remote-mode env:
	HugrURL    string // → HUGR_URL — hugr BASE url as seen from the agent network (NO /ipc; hugen appends it)
	HugrIssuer string // → HUGR_ISSUER — user-token issuer; hugen serve fails closed without it (boot-fatal)
	TokenURL   string // → HUGR_TOKEN_URL — the /agent/token endpoint the container redeems its secret at
	LogLevel   string // → HUGEN_LOG_LEVEL — optional passthrough

	// PublishAPI publishes the container API port (10200) on an ephemeral host
	// port for out-of-network access (HUB_AGENT_PUBLISH_API dev flag;
	// prod-forbidden). The assignment is learned via ContainerInspect and
	// surfaced on RuntimeState.HostPort.
	PublishAPI bool

	// Resource-cap fallbacks applied when agent_type.config.orchestration omits
	// them (0 = unlimited).
	DefaultMemoryBytes int64
	DefaultNanoCPUs    int64
	DefaultPidsLimit   int64
}

// AgentRuntime manages agent container lifecycle.
// Runtime state (container_id, status) lives in memory.
// Identity (display_name, hugr_*) comes from DB (agents table).
type AgentRuntime interface {
	// Start creates and starts a container for the agent.
	Start(ctx context.Context, agent AgentIdentity) error

	// Stop stops and removes the agent's container.
	Stop(ctx context.Context, agentID string) error

	// Status returns the current runtime state for an agent.
	Status(agentID string) RuntimeState

	// ListRunning returns runtime state for all currently running agents.
	ListRunning() []RuntimeState
}

// AgentIdentity contains persistent agent data from the Agent DB
// (hub.agent.db.agents). DisplayName/HugrRole map to the store's name/role; the
// Hugr principal is the agent itself (HugrUserID/HugrUserName == the agent id/
// name, D8). Image + resource caps come from agent_type.config.orchestration.
type AgentIdentity struct {
	ID           string
	AgentTypeID  string
	DisplayName  string
	HugrUserID   string
	HugrUserName string
	HugrRole     string
	Image        string // agent_type.config.orchestration.image
	MemoryBytes  int64  // orchestration.memory_bytes (0 = runtime default)
	NanoCPUs     int64  // orchestration.nano_cpus   (0 = runtime default)
	PidsLimit    int64  // orchestration.pids_limit  (0 = runtime default)
	// Manual marks a 'manual' desired-state agent (spec §4): the container is
	// created with restart-policy 'no' so a crash stays down until an explicit
	// start_agent relaunches it (the supervisor never auto-revives it).
	Manual bool
}

// RuntimeState contains ephemeral state for a running agent.
type RuntimeState struct {
	AgentID     string    `json:"agent_id"`
	DisplayName string    `json:"display_name"`
	AgentTypeID string    `json:"agent_type_id"`
	ContainerID string    `json:"container_id"`
	HostPort    string    `json:"host_port,omitempty"` // published API host port (HUB_AGENT_PUBLISH_API only)
	Status      string    `json:"status"`              // starting, running, unhealthy, error, stopped
	StartedAt   time.Time `json:"started_at"`
}
