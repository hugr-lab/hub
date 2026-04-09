package hubapp

import (
	"encoding/json"
	"net/http"

	"github.com/hugr-lab/hub/pkg/agentconn"
	"github.com/hugr-lab/hub/pkg/agentmgr"
	"github.com/hugr-lab/hub/pkg/auth"
)

// handleAgentInstances returns running agent instances with connected status.
func (a *HubApp) handleAgentInstances(connMgr *agentconn.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		// Query running instances from DB
		res, err := a.client.Query(r.Context(),
			`query($uid: String!) { hub { db { agent_instances(
				filter: { user_id: { eq: $uid }, status: { eq: "running" } }
			) { id user_id agent_type_id status started_at } } } }`,
			map[string]any{"uid": user.ID},
		)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]any{})
			return
		}
		defer res.Close()
		if res.Err() != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]any{})
			return
		}

		var instances []struct {
			ID          string `json:"id"`
			UserID      string `json:"user_id"`
			AgentTypeID string `json:"agent_type_id"`
			Status      string `json:"status"`
			StartedAt   string `json:"started_at"`
		}
		if err := res.ScanData("hub.db.agent_instances", &instances); err != nil || instances == nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]any{})
			return
		}

		// Enrich with connected status
		type instanceWithConn struct {
			ID          string `json:"id"`
			UserID      string `json:"user_id"`
			AgentTypeID string `json:"agent_type_id"`
			Status      string `json:"status"`
			Connected   bool   `json:"connected"`
			StartedAt   string `json:"started_at"`
		}

		result := make([]instanceWithConn, len(instances))
		for i, inst := range instances {
			result[i] = instanceWithConn{
				ID:          inst.ID,
				UserID:      inst.UserID,
				AgentTypeID: inst.AgentTypeID,
				Status:      inst.Status,
				Connected:   connMgr.IsConnected(inst.ID),
				StartedAt:   inst.StartedAt,
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

func (a *HubApp) handleAgentStart(mgr *agentmgr.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			UserID      string `json:"user_id"`
			AgentTypeID string `json:"agent_type_id"`
			Role        string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		if req.AgentTypeID == "" {
			http.Error(w, "agent_type_id required", http.StatusBadRequest)
			return
		}

		containerID, err := mgr.StartAgent(r.Context(), req.UserID, req.AgentTypeID, req.Role)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(map[string]string{"status": "started", "container_id": containerID})
	}
}

func (a *HubApp) handleAgentStop(mgr *agentmgr.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			UserID string `json:"user_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}

		if err := mgr.StopAgent(r.Context(), req.UserID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
	}
}

func (a *HubApp) handleAgentStatus(mgr *agentmgr.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := r.URL.Query().Get("user_id")
		if userID == "" {
			http.Error(w, "user_id required", http.StatusBadRequest)
			return
		}

		status, err := mgr.AgentStatus(r.Context(), userID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(status)
	}
}
