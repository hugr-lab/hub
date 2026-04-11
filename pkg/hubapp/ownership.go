package hubapp

// Shared helpers used by the airport-go mutating / reading functions:
//   - ensureUser: seed a hub.db.users row for a caller on first use (FK dependency)
//   - verifyConversationOwner: gate writes/reads on a conversation by ownership
//   - getConversationDepth: enforce the branch-depth-≤-3 rule for branch_conversation

import (
	"context"
	"fmt"
)

// ensureUser creates the user row if not exists, so conversations and other
// user-scoped inserts can satisfy their FK. Called on the first write operation
// for a caller.
func (a *HubApp) ensureUser(ctx context.Context, userID, role string) {
	res, err := a.client.Query(ctx,
		`query($id: String!) { hub { db { users(filter: { id: { eq: $id } }, limit: 1) { id } } } }`,
		map[string]any{"id": userID},
	)
	if err == nil {
		defer res.Close()
		if res.Err() != nil {
			a.logger.Warn("ensureUser check error", "user", userID, "error", res.Err())
			return
		}
		var users []struct {
			ID string `json:"id"`
		}
		if err := res.ScanData("hub.db.users", &users); err == nil && len(users) > 0 {
			return
		}
	}
	if role == "" {
		role = "user"
	}
	insRes, err := a.client.Query(ctx,
		`mutation($id: String!, $name: String!, $role: String!) {
			hub { db { insert_users(data: { id: $id, display_name: $name, hugr_role: $role }) { id } } }
		}`,
		map[string]any{"id": userID, "name": userID, "role": role},
	)
	if err != nil {
		a.logger.Warn("ensureUser failed", "user", userID, "error", err)
		return
	}
	defer insRes.Close()
	if insRes.Err() != nil {
		a.logger.Warn("ensureUser insert error", "user", userID, "error", insRes.Err())
		return
	}
	a.logger.Info("auto-created user", "id", userID, "role", role)
}

// verifyConversationOwner checks that the conversation belongs to the user.
// Returns an error if the conversation does not exist or the user is not the
// owner. Admin callers should bypass this check at the handler level.
func (a *HubApp) verifyConversationOwner(ctx context.Context, convID, userID string) error {
	res, err := a.client.Query(ctx,
		`query($id: String!, $uid: String!) { hub { db { conversations(
			filter: { id: { eq: $id }, user_id: { eq: $uid } }
			limit: 1
		) { id } } } }`,
		map[string]any{"id": convID, "uid": userID},
	)
	if err != nil {
		return err
	}
	defer res.Close()
	if res.Err() != nil {
		return res.Err()
	}
	var convs []any
	if err := res.ScanData("hub.db.conversations", &convs); err != nil || len(convs) == 0 {
		return fmt.Errorf("conversation not found or access denied")
	}
	return nil
}

// getConversationDepth returns the depth of a conversation in the tree.
// depth == 0 means root conversation (no parent).
// The walk stops at depth 4 as a safety limit; callers that need to enforce a
// maximum depth should compare the returned value explicitly.
func (a *HubApp) getConversationDepth(ctx context.Context, convID string) (int, error) {
	depth := 0
	currentID := convID
	for depth < 4 { // safety limit
		res, err := a.client.Query(ctx,
			`query($id: String!) { hub { db { conversations(
				filter: { id: { eq: $id } } limit: 1
			) { parent_id } } } }`,
			map[string]any{"id": currentID},
		)
		if err != nil {
			return 0, err
		}
		if res.Err() != nil {
			res.Close()
			return 0, res.Err()
		}
		var convs []struct {
			ParentID *string `json:"parent_id"`
		}
		if err := res.ScanData("hub.db.conversations", &convs); err != nil || len(convs) == 0 {
			res.Close()
			return depth, nil
		}
		res.Close()
		if convs[0].ParentID == nil || *convs[0].ParentID == "" {
			return depth, nil
		}
		depth++
		currentID = *convs[0].ParentID
	}
	return depth, nil
}
