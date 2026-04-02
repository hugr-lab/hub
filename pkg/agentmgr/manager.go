package agentmgr

import (
	"context"
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

// StartAgent creates and starts an agent container for a user.
func (m *Manager) StartAgent(ctx context.Context, userID, agentTypeID string) (string, error) {
	// 1. Lookup agent type
	agentType, err := m.getAgentType(ctx, agentTypeID)
	if err != nil {
		return "", fmt.Errorf("get agent type: %w", err)
	}

	// 2. Create container
	mcpURL := fmt.Sprintf("%s/mcp/%s", m.baseURL, userID)
	containerID, err := m.backend.Create(ctx, AgentConfig{
		UserID:      userID,
		AgentTypeID: agentTypeID,
		Image:       agentType.Image,
		MCPURL:      mcpURL,
		Mounts: []Mount{
			{Source: fmt.Sprintf("hub-user-%s-shared", userID), Target: "/shared"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}

	// 3. Record instance in hub DB
	res, err := m.hugrClient.Query(ctx,
		`mutation($uid: String!, $tid: String!, $cid: String!) {
			hub { hub { insert_agent_instances(data: {
				user_id: $uid
				agent_type_id: $tid
				container_id: $cid
				status: "running"
			}) { id } } }
		}`,
		map[string]any{"uid": userID, "tid": agentTypeID, "cid": containerID},
	)
	if err != nil {
		m.logger.Warn("failed to record instance", "error", err)
	}
	if res != nil {
		defer res.Close()
	}

	// 4. Start container
	if err := m.backend.Start(ctx, containerID); err != nil {
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
		fmt.Sprintf(`mutation { hub { hub { update_agent_instances(
			filter: { id: { eq: "%s" } }
			data: { status: "stopped" }
		) { success } } } }`, instance.ID),
		nil,
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
		fmt.Sprintf(`{ hub { hub { agent_types(filter: { id: { eq: "%s" } }) { image } } } }`, typeID),
		nil,
	)
	if err != nil {
		return agentTypeInfo{}, err
	}
	defer res.Close()
	if res.Err() != nil {
		return agentTypeInfo{}, res.Err()
	}
	var types []agentTypeInfo
	if err := res.ScanData("hub.hub.agent_types", &types); err != nil || len(types) == 0 {
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
		fmt.Sprintf(`{ hub { hub { agent_instances(
			filter: { user_id: { eq: "%s" }, status: { eq: "running" } }
			limit: 1
		) { id container_id } } } }`, userID),
		nil,
	)
	if err != nil {
		return instanceInfo{}, err
	}
	defer res.Close()
	if res.Err() != nil {
		return instanceInfo{}, res.Err()
	}
	var instances []instanceInfo
	if err := res.ScanData("hub.hub.agent_instances", &instances); err != nil || len(instances) == 0 {
		return instanceInfo{}, fmt.Errorf("no running agent for user %q", userID)
	}
	return instances[0], nil
}
