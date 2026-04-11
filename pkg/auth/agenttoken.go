package auth

import (
	"context"
	"fmt"

	"github.com/hugr-lab/query-engine/client"
)

// AgentTokenValidator validates agent tokens via AgentRuntime (in-memory, O(1))
// and looks up agent identity from Hugr DB for impersonation fields.
type AgentTokenValidator struct {
	hugrClient   *client.Client
	validateFunc func(token string) (agentID string, ok bool) // from AgentRuntime.ValidateToken
}

func NewAgentTokenValidator(hugrClient *client.Client, validateFunc func(string) (string, bool)) *AgentTokenValidator {
	return &AgentTokenValidator{
		hugrClient:   hugrClient,
		validateFunc: validateFunc,
	}
}

// Validate checks an agent token via AgentRuntime and looks up identity from DB.
func (v *AgentTokenValidator) Validate(ctx context.Context, token string) (*UserInfo, error) {
	agentID, ok := v.validateFunc(token)
	if !ok {
		return nil, fmt.Errorf("invalid agent token")
	}

	agent, err := v.getAgentIdentity(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("lookup agent %s: %w", agentID, err)
	}

	return &UserInfo{
		ID:       agent.HugrUserID,
		Name:     agent.HugrUserName,
		Role:     agent.HugrRole,
		AuthType: "agent",
	}, nil
}

type agentInfo struct {
	HugrUserID   string `json:"hugr_user_id"`
	HugrUserName string `json:"hugr_user_name"`
	HugrRole     string `json:"hugr_role"`
}

func (v *AgentTokenValidator) getAgentIdentity(ctx context.Context, agentID string) (agentInfo, error) {
	res, err := v.hugrClient.Query(ctx,
		`query($id: String!) { hub { db { agents(
			filter: { id: { eq: $id } } limit: 1
		) { hugr_user_id hugr_user_name hugr_role } } } }`,
		map[string]any{"id": agentID},
	)
	if err != nil {
		return agentInfo{}, err
	}
	defer res.Close()
	if res.Err() != nil {
		return agentInfo{}, res.Err()
	}

	var agents []agentInfo
	if err := res.ScanData("hub.db.agents", &agents); err != nil || len(agents) == 0 {
		return agentInfo{}, fmt.Errorf("agent %q not found", agentID)
	}
	return agents[0], nil
}
