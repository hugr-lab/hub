package auth

import (
	"context"
	"fmt"

	"github.com/hugr-lab/query-engine/client"
)

// AgentTokenValidator validates agent tokens by looking up agent_instances in hub DB.
type AgentTokenValidator struct {
	hugrClient *client.Client
}

func NewAgentTokenValidator(hugrClient *client.Client) *AgentTokenValidator {
	return &AgentTokenValidator{hugrClient: hugrClient}
}

// Validate checks an agent token against running instances.
func (v *AgentTokenValidator) Validate(ctx context.Context, token string) (*UserInfo, error) {
	gql := `query($token: String!) {
		hub { db { agent_instances(
			filter: { auth_token: { eq: $token }, status: { eq: "running" } }
			limit: 1
		) { user_id agent_type_id } } }
	}`

	res, err := v.hugrClient.Query(ctx, gql, map[string]any{"token": token})
	if err != nil {
		return nil, fmt.Errorf("query agent token: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return nil, res.Err()
	}

	var instances []struct {
		UserID      string `json:"user_id"`
		AgentTypeID string `json:"agent_type_id"`
	}
	if err := res.ScanData("hub.db.agent_instances", &instances); err != nil || len(instances) == 0 {
		return nil, fmt.Errorf("invalid agent token")
	}

	return &UserInfo{
		ID:       instances[0].UserID,
		Name:     "agent:" + instances[0].AgentTypeID,
		Role:     "agent",
		AuthType: "agent",
	}, nil
}
