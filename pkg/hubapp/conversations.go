package hubapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/hugr-lab/hub/pkg/auth"
)

// verifyConversationOwner checks that conversation belongs to user.
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

func (a *HubApp) handleConversationCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Title           string `json:"title"`
		Mode            string `json:"mode"`
		Model           string `json:"model"`
		AgentInstanceID string `json:"agent_instance_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.Mode == "" {
		req.Mode = "tools"
	}
	if req.Title == "" {
		req.Title = "New Chat"
	}

	// Ensure user exists (may not be synced yet after DB recreate)
	a.ensureUser(r.Context(), user.ID, user.Role)

	convID := fmt.Sprintf("conv-%d", time.Now().UnixNano())

	vars := map[string]any{"id": convID, "uid": user.ID, "title": req.Title, "mode": req.Mode}
	dataFields := `id: $id, user_id: $uid, title: $title, mode: $mode`
	varDefs := `$id: String!, $uid: String!, $title: String!, $mode: String!`

	if req.Model != "" {
		varDefs += `, $model: String`
		dataFields += `, model: $model`
		vars["model"] = req.Model
	}
	if req.AgentInstanceID != "" {
		varDefs += `, $aid: String`
		dataFields += `, agent_instance_id: $aid`
		vars["aid"] = req.AgentInstanceID
	}

	gql := fmt.Sprintf(`mutation(%s) {
		hub { db { insert_conversations(data: {
			%s
		}) { id title mode model } } }
	}`, varDefs, dataFields)

	res, err := a.client.Query(r.Context(), gql, vars)
	if err != nil {
		http.Error(w, fmt.Sprintf("create: %v", err), http.StatusInternalServerError)
		return
	}
	defer res.Close()
	if res.Err() != nil {
		http.Error(w, fmt.Sprintf("create: %v", res.Err()), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"id": convID, "title": req.Title, "mode": req.Mode})
}

func (a *HubApp) handleConversationList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	res, err := a.client.Query(r.Context(),
		`query($uid: String!) { hub { db { conversations(
			filter: { user_id: { eq: $uid } }
			order_by: [{field: "updated_at", direction: DESC}]
		) { id title folder mode agent_instance_id model updated_at created_at } } } }`,
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

	var convs []any
	if err := res.ScanData("hub.db.conversations", &convs); err != nil || convs == nil {
		convs = []any{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(convs)
}

func (a *HubApp) handleConversationMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		ID    string `json:"id"`
		Limit int    `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.Limit <= 0 {
		req.Limit = 50
	}

	// Verify conversation belongs to user
	if err := a.verifyConversationOwner(r.Context(), req.ID, user.ID); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	res, err := a.client.Query(r.Context(),
		`query($cid: String!, $limit: Int!) { hub { db { agent_messages(
			filter: { conversation_id: { eq: $cid } }
			order_by: [{field: "created_at", direction: DESC}]
			limit: $limit
		) { id role content tool_calls tool_call_id tokens_used model created_at } } } }`,
		map[string]any{"cid": req.ID, "limit": req.Limit},
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

	var msgs []any
	if err := res.ScanData("hub.db.agent_messages", &msgs); err != nil || msgs == nil {
		msgs = []any{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(msgs)
}

func (a *HubApp) handleConversationDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}

	// Soft delete via Hugr @table(soft_delete) — sets deleted_at = NOW()
	res, err := a.client.Query(r.Context(),
		`mutation($id: String!, $uid: String!) { hub { db { delete_conversations(
			filter: { id: { eq: $id }, user_id: { eq: $uid } }
		) { affected_rows } } } }`,
		map[string]any{"id": req.ID, "uid": user.ID},
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("delete: %v", err), http.StatusInternalServerError)
		return
	}
	defer res.Close()
	if res.Err() != nil {
		http.Error(w, fmt.Sprintf("delete: %v", res.Err()), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"deleted": req.ID})
}

func (a *HubApp) handleConversationMove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		ID     string  `json:"id"`
		Folder *string `json:"folder"` // null to remove from folder
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}

	folder := ""
	if req.Folder != nil {
		folder = *req.Folder
	}

	res, err := a.client.Query(r.Context(),
		`mutation($id: String!, $uid: String!, $folder: String) { hub { db { update_conversations(
			filter: { id: { eq: $id }, user_id: { eq: $uid } }
			data: { folder: $folder }
		) { affected_rows } } } }`,
		map[string]any{"id": req.ID, "uid": user.ID, "folder": folder},
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("move: %v", err), http.StatusInternalServerError)
		return
	}
	defer res.Close()
	if res.Err() != nil {
		http.Error(w, fmt.Sprintf("move: %v", res.Err()), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"moved": req.ID})
}

func (a *HubApp) handleConversationRename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" || req.Title == "" {
		http.Error(w, "id and title required", http.StatusBadRequest)
		return
	}

	// Filter by user_id ensures ownership
	res, err := a.client.Query(r.Context(),
		`mutation($id: String!, $uid: String!, $title: String!) { hub { db { update_conversations(
			filter: { id: { eq: $id }, user_id: { eq: $uid } }
			data: { title: $title }
		) { affected_rows } } } }`,
		map[string]any{"id": req.ID, "uid": user.ID, "title": req.Title},
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("rename: %v", err), http.StatusInternalServerError)
		return
	}
	defer res.Close()
	if res.Err() != nil {
		http.Error(w, fmt.Sprintf("rename: %v", res.Err()), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"renamed": req.ID})
}
