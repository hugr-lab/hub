package hubapp

import (
	"context"
	"fmt"
	"time"
)

// ensurePersonalAgent creates the personal agent + user_agents grant if not exists.
// Called lazily when a workspace agent first connects or when the user opens chat.
// Idempotent — safe to call on every request (checks existence first).
func (a *HubApp) ensurePersonalAgent(ctx context.Context, userID, userName, role string) {
	agentID := fmt.Sprintf("agent-personal-%s", userID)

	// Check if agent already exists
	res, err := a.client.Query(ctx,
		`query($id: String!) { hub { db { agents(filter: { id: { eq: $id } } limit: 1) { id } } } }`,
		map[string]any{"id": agentID},
	)
	if err != nil {
		a.logger.Warn("ensurePersonalAgent check failed", "agent", agentID, "error", err)
		return
	}
	defer res.Close()
	if res.Err() != nil {
		a.logger.Warn("ensurePersonalAgent check error", "agent", agentID, "error", res.Err())
		return
	}
	var agents []struct{ ID string `json:"id"` }
	if err := res.ScanData("hub.db.agents", &agents); err == nil && len(agents) > 0 {
		return // already exists
	}

	// Ensure user row exists (FK)
	a.ensureUser(ctx, userID, role)

	// Create agent
	agentRes, err := a.client.Query(ctx,
		`mutation($id: String!, $uid: String!, $uname: String!, $role: String!) {
			hub { db { insert_agents(data: {
				id: $id,
				agent_type_id: "personal-assistant",
				display_name: "Personal Assistant",
				hugr_user_id: $uid,
				hugr_user_name: $uname,
				hugr_role: $role
			}) { id } } }
		}`,
		map[string]any{"id": agentID, "uid": userID, "uname": userName, "role": role},
	)
	if err != nil {
		a.logger.Warn("ensurePersonalAgent create failed", "agent", agentID, "error", err)
		return
	}
	defer agentRes.Close()
	if agentRes.Err() != nil {
		a.logger.Warn("ensurePersonalAgent create error", "agent", agentID, "error", agentRes.Err())
		return
	}

	// Grant owner access
	grantID := fmt.Sprintf("grant-%d", time.Now().UnixNano())
	_ = grantID
	grantRes, err := a.client.Query(ctx,
		`mutation($uid: String!, $aid: String!) {
			hub { db { insert_user_agents(data: {
				user_id: $uid, agent_id: $aid, role: "owner"
			}) { user_id } } }
		}`,
		map[string]any{"uid": userID, "aid": agentID},
	)
	if err != nil {
		a.logger.Warn("ensurePersonalAgent grant failed", "agent", agentID, "error", err)
		return
	}
	defer grantRes.Close()

	a.logger.Info("personal agent created", "agent", agentID, "user", userID)
}
