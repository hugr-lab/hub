package hubapp

import (
	"context"
	"encoding/json"
	"net/http"
)

// ensureUser creates user if not exists (for conversation FK).
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
		var users []struct{ ID string `json:"id"` }
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

type userLoginRequest struct {
	UserID   string `json:"user_id"`
	UserName string `json:"user_name"`
	Role     string `json:"role"`
	Email    string `json:"email"`
}

func (a *HubApp) handleUserLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req userLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.UserID == "" {
		http.Error(w, "user_id required", http.StatusBadRequest)
		return
	}

	// Check if user exists
	checkRes, err := a.client.Query(r.Context(),
		`query($id: String!) { hub { db { users(filter: { id: { eq: $id } }) { id } } } }`,
		map[string]any{"id": req.UserID},
	)
	if err != nil {
		a.logger.Error("failed to check user", "user_id", req.UserID, "error", err)
		http.Error(w, "failed to sync user", http.StatusInternalServerError)
		return
	}
	defer checkRes.Close()
	if checkRes.Err() != nil {
		a.logger.Error("failed to check user", "user_id", req.UserID, "error", checkRes.Err())
		http.Error(w, "failed to sync user", http.StatusInternalServerError)
		return
	}

	var existing []struct{ ID string `json:"id"` }
	_ = checkRes.ScanData("hub.db.users", &existing)

	if len(existing) > 0 {
		// Update
		res, err := a.client.Query(r.Context(),
			`mutation($id: String!, $name: String!, $role: String!, $email: String!) {
				hub { db { update_users(
					filter: { id: { eq: $id } }
					data: { display_name: $name, hugr_role: $role, email: $email }
				) { affected_rows } } }
			}`,
			map[string]any{"id": req.UserID, "name": req.UserName, "role": req.Role, "email": req.Email},
		)
		if err != nil {
			a.logger.Error("failed to update user", "user_id", req.UserID, "error", err)
			http.Error(w, "failed to sync user", http.StatusInternalServerError)
			return
		}
		defer res.Close()
		if res.Err() != nil {
			a.logger.Error("failed to update user", "user_id", req.UserID, "error", res.Err())
			http.Error(w, "failed to sync user", http.StatusInternalServerError)
			return
		}
	} else {
		// Insert
		res, err := a.client.Query(r.Context(),
			`mutation($id: String!, $name: String!, $role: String!, $email: String!) {
				hub { db { insert_users(
					data: { id: $id, display_name: $name, hugr_role: $role, email: $email }
				) { id } } }
			}`,
			map[string]any{"id": req.UserID, "name": req.UserName, "role": req.Role, "email": req.Email},
		)
		if err != nil {
			a.logger.Error("failed to insert user", "user_id", req.UserID, "error", err)
			http.Error(w, "failed to sync user", http.StatusInternalServerError)
			return
		}
		defer res.Close()
		if res.Err() != nil {
			a.logger.Error("failed to insert user", "user_id", req.UserID, "error", res.Err())
			http.Error(w, "failed to sync user", http.StatusInternalServerError)
			return
		}
	}

	a.logger.Info("user synced", "user_id", req.UserID, "role", req.Role)
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
