package agentmgr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"

	"github.com/hugr-lab/query-engine/client"
)

// Manager orchestrates agent container lifecycle and tracks instances in hub DB.
type Manager struct {
	backend    Backend
	hugrClient *client.Client
	baseURL    string // Hub Service base URL for MCP endpoints
	logger     *slog.Logger
}

func NewManager(backend Backend, hugrClient *client.Client, baseURL string, logger *slog.Logger) *Manager {
	return &Manager{
		backend:    backend,
		hugrClient: hugrClient,
		baseURL:    baseURL,
		logger:     logger,
	}
}

// StartAgent creates and starts an agent container.
// userID is optional — empty for autonomous agents.
// role is the Hugr role for the agent (default: from agent type or "agent").
func (m *Manager) StartAgent(ctx context.Context, userID, agentTypeID, role string) (string, error) {
	// 1. Lookup agent type
	agentType, err := m.getAgentType(ctx, agentTypeID)
	if err != nil {
		return "", fmt.Errorf("get agent type: %w", err)
	}

	// 2. Generate agent auth token
	authToken, err := generateToken(32)
	if err != nil {
		return "", fmt.Errorf("generate auth token: %w", err)
	}

	// 3. Determine agent identity
	agentID := userID
	if agentID == "" {
		short, _ := generateToken(4)
		agentID = "agent-" + agentTypeID + "-" + short
	}
	if role == "" {
		role = "agent"
	}

	// 4. Ensure user exists in hub DB (for FK)
	if err := m.ensureUser(ctx, agentID, role); err != nil {
		return "", fmt.Errorf("ensure user: %w", err)
	}

	// 6. Create container
	mcpURL := fmt.Sprintf("%s/mcp/%s", m.baseURL, agentID)
	containerID, err := m.backend.Create(ctx, AgentConfig{
		UserID:      agentID,
		AgentTypeID: agentTypeID,
		Image:       agentType.Image,
		MCPURL:      mcpURL,
		Env: map[string]string{
			"AGENT_TOKEN": authToken,
			"AGENT_ROLE":  role,
		},
		Mounts: []Mount{
			{Source: fmt.Sprintf("hub-agent-%s-shared", agentID), Target: "/shared"},
			{Source: "hub-agent-tools", Target: "/tools"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}

	// 7. Record instance in hub DB
	instanceID, _ := generateToken(16) // UUID-like hex ID
	res, err := m.hugrClient.Query(ctx,
		`mutation($id: String!, $uid: String!, $tid: String!, $cid: String!, $token: String!) {
			hub { db { insert_agent_instances(data: {
				id: $id
				user_id: $uid
				agent_type_id: $tid
				container_id: $cid
				auth_token: $token
				status: "running"
			}) { id } } }
		}`,
		map[string]any{"id": instanceID, "uid": agentID, "tid": agentTypeID, "cid": containerID, "token": authToken},
	)
	if err != nil {
		return "", fmt.Errorf("record instance: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return "", fmt.Errorf("record instance: %w", res.Err())
	}

	// 8. Start container
	if err := m.backend.Start(ctx, containerID); err != nil {
		_ = m.backend.Remove(ctx, containerID)
		return "", fmt.Errorf("start container: %w", err)
	}

	m.logger.Info("agent started", "user", userID, "type", agentTypeID, "container", containerID[:12])
	return containerID, nil
}

// StopAgent stops and removes an agent container.
func (m *Manager) StopAgent(ctx context.Context, userID string) error {
	instance, err := m.getRunningInstance(ctx, userID)
	if err != nil {
		return err
	}

	if err := m.backend.Stop(ctx, instance.ContainerID); err != nil {
		m.logger.Warn("stop failed, force removing", "error", err)
	}
	if err := m.backend.Remove(ctx, instance.ContainerID); err != nil {
		return fmt.Errorf("remove container: %w", err)
	}

	// Update status in DB
	res, err := m.hugrClient.Query(ctx,
		`mutation($id: String!) { hub { db { update_agent_instances(
			filter: { id: { eq: $id } }
			data: { status: "stopped" }
		) { affected_rows } } } }`,
		map[string]any{"id": instance.ID},
	)
	if err != nil {
		m.logger.Warn("failed to update instance status", "error", err)
	}
	if res != nil {
		defer res.Close()
	}

	m.logger.Info("agent stopped", "user", userID, "container", instance.ContainerID[:12])
	return nil
}

// AgentStatus returns the status of a user's running agent.
func (m *Manager) AgentStatus(ctx context.Context, userID string) (AgentStatus, error) {
	instance, err := m.getRunningInstance(ctx, userID)
	if err != nil {
		return AgentStatus{Status: "stopped"}, nil
	}
	return m.backend.Status(ctx, instance.ContainerID)
}

type agentTypeInfo struct {
	Image string `json:"image"`
}

func (m *Manager) getAgentType(ctx context.Context, typeID string) (agentTypeInfo, error) {
	res, err := m.hugrClient.Query(ctx,
		`query($id: String!) { hub { db { agent_types(filter: { id: { eq: $id } }) { image } } } }`,
		map[string]any{"id": typeID},
	)
	if err != nil {
		return agentTypeInfo{}, err
	}
	defer res.Close()
	if res.Err() != nil {
		return agentTypeInfo{}, res.Err()
	}
	var types []agentTypeInfo
	if err := res.ScanData("hub.db.agent_types", &types); err != nil || len(types) == 0 {
		return agentTypeInfo{}, fmt.Errorf("agent type %q not found", typeID)
	}
	return types[0], nil
}

type instanceInfo struct {
	ID          string `json:"id"`
	ContainerID string `json:"container_id"`
}

func (m *Manager) getRunningInstance(ctx context.Context, userID string) (instanceInfo, error) {
	res, err := m.hugrClient.Query(ctx,
		`query($uid: String!) { hub { db { agent_instances(
			filter: { user_id: { eq: $uid }, status: { eq: "running" } }
			limit: 1
		) { id container_id } } } }`,
		map[string]any{"uid": userID},
	)
	if err != nil {
		return instanceInfo{}, err
	}
	defer res.Close()
	if res.Err() != nil {
		return instanceInfo{}, res.Err()
	}
	var instances []instanceInfo
	if err := res.ScanData("hub.db.agent_instances", &instances); err != nil || len(instances) == 0 {
		return instanceInfo{}, fmt.Errorf("no running agent for user %q", userID)
	}
	return instances[0], nil
}

// ensureUser creates a user record if it doesn't exist (for agent identity).
func (m *Manager) ensureUser(ctx context.Context, userID, role string) error {
	// Check if user exists
	res, err := m.hugrClient.Query(ctx,
		`query($id: String!) { hub { db { users(filter: { id: { eq: $id } }, limit: 1) { id } } } }`,
		map[string]any{"id": userID},
	)
	if err == nil {
		defer res.Close()
		var users []struct{ ID string `json:"id"` }
		if err := res.ScanData("hub.db.users", &users); err == nil && len(users) > 0 {
			return nil // already exists
		}
	}

	// Create user
	res2, err := m.hugrClient.Query(ctx,
		`mutation($id: String!, $name: String!, $role: String!) {
			hub { db { insert_users(data: {
				id: $id, display_name: $name, hugr_role: $role
			}) { id } } }
		}`,
		map[string]any{"id": userID, "name": userID, "role": role},
	)
	if err != nil {
		return fmt.Errorf("create user %s: %w", userID, err)
	}
	defer res2.Close()
	if res2.Err() != nil {
		return fmt.Errorf("create user %s: %w", userID, res2.Err())
	}
	m.logger.Info("created agent user", "id", userID, "role", role)
	return nil
}

func generateToken(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
