package hubapp

import (
	"encoding/json"
	"io"
	"net/http"
)

// handleGraphQLProxy forwards GraphQL queries to Hugr with management auth.
// Admin panel uses this instead of connection_service proxy.
func (a *HubApp) handleGraphQLProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	var req struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	res, err := a.client.Query(r.Context(), req.Query, req.Variables)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]any{
			"errors": []map[string]string{{"message": err.Error()}},
		})
		return
	}
	defer res.Close()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"data":   res.Data,
		"errors": res.Errors,
	})
}
