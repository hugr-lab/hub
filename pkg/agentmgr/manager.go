package agentmgr

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hugr-lab/query-engine/client"
)

// Manager orchestrates agent lifecycle using AgentRuntime for containers
// and Hugr GraphQL for identity/access data.
type Manager struct {
	runtime    AgentRuntime
	hugrClient *client.Client
	logger     *slog.Logger
}

func NewManager(runtime AgentRuntime, hugrClient *client.Client, logger *slog.Logger) *Manager {
	return &Manager{
		runtime:    runtime,
		hugrClient: hugrClient,
		logger:     logger,
	}
}

// StartAgent looks up agent identity from DB and starts a container via AgentRuntime.
func (m *Manager) StartAgent(ctx context.Context, agentID string) error {
	agent, err := m.getAgentIdentity(ctx, agentID)
	if err != nil {
		return fmt.Errorf("get agent: %w", err)
	}
	return m.runtime.Start(ctx, agent)
}

// StopAgent stops the agent container via AgentRuntime.
func (m *Manager) StopAgent(ctx context.Context, agentID string) error {
	return m.runtime.Stop(ctx, agentID)
}

// AgentStatus returns the runtime status of an agent.
func (m *Manager) AgentStatus(agentID string) RuntimeState {
	return m.runtime.Status(agentID)
}

// Runtime returns the underlying AgentRuntime for direct access (e.g. token validation).
func (m *Manager) Runtime() AgentRuntime {
	return m.runtime
}

// CheckAccess verifies a user has access to an agent via user_agents table.
// Admin/management auth types bypass the check.
func (m *Manager) CheckAccess(ctx context.Context, userID, agentID, authType string) (string, error) {
	if authType == "management" {
		return "owner", nil
	}

	res, err := m.hugrClient.Query(ctx,
		`query($uid: String!, $aid: String!) { hub { db { user_agents(
			filter: { user_id: { eq: $uid }, agent_id: { eq: $aid } }
			limit: 1
		) { role } } } }`,
		map[string]any{"uid": userID, "aid": agentID},
	)
	if err != nil {
		return "", fmt.Errorf("check access: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return "", res.Err()
	}

	var access []struct {
		Role string `json:"role"`
	}
	if err := res.ScanData("hub.db.user_agents", &access); err != nil || len(access) == 0 {
		return "", fmt.Errorf("no access to agent %s", agentID)
	}
	return access[0].Role, nil
}

type agentTypeInfo struct {
	Image string `json:"image"`
}

func (m *Manager) getAgentIdentity(ctx context.Context, agentID string) (AgentIdentity, error) {
	res, err := m.hugrClient.Query(ctx,
		`query($id: String!) { hub { db { agents(
			filter: { id: { eq: $id } } limit: 1
		) { id agent_type_id display_name hugr_user_id hugr_user_name hugr_role
		   agent_type { image } } } } }`,
		map[string]any{"id": agentID},
	)
	if err != nil {
		return AgentIdentity{}, err
	}
	defer res.Close()
	if res.Err() != nil {
		return AgentIdentity{}, res.Err()
	}

	var agents []struct {
		ID           string `json:"id"`
		AgentTypeID  string `json:"agent_type_id"`
		DisplayName  string `json:"display_name"`
		HugrUserID   string `json:"hugr_user_id"`
		HugrUserName string `json:"hugr_user_name"`
		HugrRole     string `json:"hugr_role"`
		AgentType    struct {
			Image string `json:"image"`
		} `json:"agent_type"`
	}
	if err := res.ScanData("hub.db.agents", &agents); err != nil || len(agents) == 0 {
		return AgentIdentity{}, fmt.Errorf("agent %q not found", agentID)
	}
	a := agents[0]
	return AgentIdentity{
		ID:           a.ID,
		AgentTypeID:  a.AgentTypeID,
		DisplayName:  a.DisplayName,
		HugrUserID:   a.HugrUserID,
		HugrUserName: a.HugrUserName,
		HugrRole:     a.HugrRole,
		Image:        a.AgentType.Image,
	}, nil
}
