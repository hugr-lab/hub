package hubapp

import (
	"encoding/json"
	"net/http"
)

type userLoginRequest struct {
	UserID   string `json:"user_id"`
	UserName string `json:"user_name"`
	Role     string `json:"role"`
	Email    string `json:"email"`
}

// handleUserLogin handles POST /api/user/login from JupyterHub post_auth_hook.
// Upserts user info into hub.users via Hugr GraphQL.
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

	_, err := a.client.Query(r.Context(),
		`mutation($id: String!, $name: String!, $role: String!, $email: String!) {
			hub { hub { insert_users(
				data: {
					id: $id
					display_name: $name
					hugr_role: $role
					email: $email
				}
			) { id } } }
		}`,
		map[string]any{
			"id":    req.UserID,
			"name":  req.UserName,
			"role":  req.Role,
			"email": req.Email,
		},
	)
	if err != nil {
		a.logger.Error("failed to upsert user", "user_id", req.UserID, "error", err)
		http.Error(w, "failed to sync user", http.StatusInternalServerError)
		return
	}

	a.logger.Info("user synced", "user_id", req.UserID, "role", req.Role)
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
