package hubapp

// Shared helpers used by the airport-go mutating / reading functions:
//   - ensureUser: seed a hub.db.users row for a caller on first use (FK dependency)

import (
	"context"
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
