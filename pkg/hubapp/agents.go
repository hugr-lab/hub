package hubapp

import (
	"encoding/json"
	"net/http"

	"github.com/hugr-lab/hub/pkg/agentmgr"
)

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
