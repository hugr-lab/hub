package hubapp

// HB5 gateway — chat-scoped transport verbs (spec-hub-gateway §4). Each verb
// resolves chat → (agent_id, root_session_id) and forwards to the container's
// native HTTP API with the caller's bearer. The root session binds LAZILY on
// the first message (spec §3): create_chat is pure-platform, so the bind
// happens here, where the user's bearer is present.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hugr-lab/hub/pkg/auth"
)

// registerChatTransport mounts the chat verbs on the shared mux.
func registerChatTransport(mux *http.ServeMux, a *HubApp) {
	mux.HandleFunc("POST /api/v1/chats/{id}/messages", a.chatMessagesHandler)
	mux.HandleFunc("POST /api/v1/chats/{id}/inquiry", a.chatVerbHandler("inquiry", true))
	mux.HandleFunc("POST /api/v1/chats/{id}/cancel", a.chatVerbHandler("cancel", false))
	mux.HandleFunc("GET /api/v1/chats/{id}/stream", a.chatVerbHandler("stream", false))
	mux.HandleFunc("GET /api/v1/chats/{id}/events", a.chatVerbHandler("events", false))
	// Stage 2 notifications: read-cursor + activity aggregate (see gateway_notifications.go).
	mux.HandleFunc("GET /api/v1/chats/activity", a.chatsActivityHandler)
	mux.HandleFunc("POST /api/v1/chats/{id}/read", a.chatReadHandler)
	mux.HandleFunc("POST /api/v1/chats/{id}/archive", a.chatArchiveHandler)
	mux.HandleFunc("POST /api/v1/chats/{id}/drop", a.chatDropHandler)
	mux.HandleFunc("GET /api/v1/chats/{id}/tasks", a.chatVerbHandler("tasks", false))
	mux.HandleFunc("POST /api/v1/chats/{id}/tasks/{taskId}/cancel", a.chatTaskHandler("/cancel"))
	mux.HandleFunc("POST /api/v1/chats/{id}/tasks/{taskId}/pause", a.chatTaskHandler("/pause"))
	mux.HandleFunc("POST /api/v1/chats/{id}/tasks/{taskId}/resume", a.chatTaskHandler("/resume"))
	mux.HandleFunc("DELETE /api/v1/chats/{id}/tasks/{taskId}", a.chatTaskHandler(""))
	mux.HandleFunc("GET /api/v1/chats/{id}/artifacts", a.chatVerbHandler("artifacts", false))
	mux.HandleFunc("POST /api/v1/chats/{id}/artifacts", a.chatVerbHandler("artifacts", false))
	mux.HandleFunc("GET /api/v1/chats/{id}/artifacts/{aid}", a.chatArtifactHandler)
	mux.HandleFunc("GET /api/v1/agents/{id}/logs", a.agentLogsHandler)
	// Per-agent skills (hub-native; forwards to the agent's hugen /v1/skills).
	mux.HandleFunc("GET /api/v1/agents/{id}/skills", a.agentSkillsListHandler)
	mux.HandleFunc("GET /api/v1/agents/{id}/skills/{name}/export", a.agentSkillExportHandler)
	mux.HandleFunc("POST /api/v1/agents/{id}/skills/install", a.agentSkillInstallHandler)
}

// chatContext authenticates the caller, loads the chat, and enforces the
// platform layer: thread ownership (foreign chat → 404, never leak) + a live
// user_agents grant on the chat's agent (re-checked per call).
func (a *HubApp) chatContext(w http.ResponseWriter, r *http.Request) (auth.UserInfo, chatRow, bool) {
	u, ok := a.gatewayCaller(w, r)
	if !ok {
		return auth.UserInfo{}, chatRow{}, false
	}
	chat, err := a.lookupChat(r.Context(), r.PathValue("id"))
	if err != nil || chat.UserID != u.ID {
		// Missing and foreign chats are indistinguishable by design.
		gatewayError(w, http.StatusNotFound, "chat_not_found", "chat not found")
		return auth.UserInfo{}, chatRow{}, false
	}
	if err := a.checkAccess(r.Context(), u, chat.AgentID); err != nil {
		a.logger.Info("agent access denied", "agent", chat.AgentID, "user", u.ID, "chat", chat.ID, "error", err)
		gatewayError(w, http.StatusForbidden, "no_agent_access", "no access to agent "+chat.AgentID)
		return auth.UserInfo{}, chatRow{}, false
	}
	return u, chat, true
}

// chatVerbHandler proxies a session-scoped verb; verbs other than messages
// require the chat to be bound already (409 chat_not_bound).
func (a *HubApp) chatVerbHandler(verb string, bumpActivity bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, chat, ok := a.chatContext(w, r)
		if !ok {
			return
		}
		sid := deref(chat.RootSessionID)
		if sid == "" {
			gatewayError(w, http.StatusConflict, "chat_not_bound",
				"no session yet — send the first message to bind one")
			return
		}
		base, ok := a.resolveAgentBase(w, r, chat.AgentID)
		if !ok {
			return
		}
		if bumpActivity {
			a.bumpChatActivity(r.Context(), chat.ID)
		}
		a.proxyToAgent(w, r, chat.AgentID, base, "/v1/sessions/"+sid+"/"+verb)
	}
}

// chatTaskHandler proxies a task-lifecycle write to the agent's per-session
// task endpoint. suffix is "/cancel" (POST) or "" (DELETE). The extra {taskId}
// path segment keeps these out of the generic verb table. The request method is
// forwarded as-is by proxyToAgent.
func (a *HubApp) chatTaskHandler(suffix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, chat, ok := a.chatContext(w, r)
		if !ok {
			return
		}
		sid := deref(chat.RootSessionID)
		if sid == "" {
			gatewayError(w, http.StatusConflict, "chat_not_bound",
				"no session yet — send the first message to bind one")
			return
		}
		base, ok := a.resolveAgentBase(w, r, chat.AgentID)
		if !ok {
			return
		}
		a.proxyToAgent(w, r, chat.AgentID, base, "/v1/sessions/"+sid+"/tasks/"+r.PathValue("taskId")+suffix)
	}
}

// chatArtifactHandler proxies the by-ref artifact download (extra {aid} path
// segment keeps it out of the generic verb table).
func (a *HubApp) chatArtifactHandler(w http.ResponseWriter, r *http.Request) {
	_, chat, ok := a.chatContext(w, r)
	if !ok {
		return
	}
	sid := deref(chat.RootSessionID)
	if sid == "" {
		gatewayError(w, http.StatusConflict, "chat_not_bound",
			"no session yet — send the first message to bind one")
		return
	}
	base, ok := a.resolveAgentBase(w, r, chat.AgentID)
	if !ok {
		return
	}
	a.proxyToAgent(w, r, chat.AgentID, base, "/v1/sessions/"+sid+"/artifacts/"+r.PathValue("aid"))
}

// chatMessagesHandler is the write path: binds the root session lazily on the
// first message, then forwards. Responds with the gateway envelope
// `{status, session_id}` so the app can open the stream right after the first
// send (spec §3).
func (a *HubApp) chatMessagesHandler(w http.ResponseWriter, r *http.Request) {
	_, chat, ok := a.chatContext(w, r)
	if !ok {
		return
	}
	base, ok := a.resolveAgentBase(w, r, chat.AgentID)
	if !ok {
		return
	}
	bearer := r.Header.Get("Authorization")

	ctx, cancel := context.WithTimeout(r.Context(), gatewayCallTimeout)
	defer cancel()

	sid := deref(chat.RootSessionID)
	if sid == "" {
		var err error
		sid, err = a.bindChatSession(ctx, chat, base, bearer)
		if err != nil {
			a.logger.Warn("chat session bind failed", "chat", chat.ID, "agent", chat.AgentID, "error", err)
			if errors.Is(err, errChatNotFound) {
				// The chat was deleted mid-bind — the opened session is closed.
				gatewayError(w, http.StatusNotFound, "chat_not_found", "chat not found")
				return
			}
			gatewayError(w, http.StatusBadGateway, "agent_unreachable", "could not open a session on the agent")
			return
		}
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		gatewayError(w, http.StatusBadRequest, "invalid_body", "message body too large or unreadable")
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/sessions/"+sid+"/messages", bytes.NewReader(body))
	if err != nil {
		gatewayError(w, http.StatusBadGateway, "agent_unreachable", "invalid agent endpoint")
		return
	}
	req.Header.Set("Authorization", bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := gatewayClient().Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			gatewayError(w, http.StatusGatewayTimeout, "agent_timeout", "agent did not respond in time")
			return
		}
		gatewayError(w, http.StatusBadGateway, "agent_unreachable", "agent did not respond")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Pass the agent's error through untouched.
		copyResponse(w, resp)
		return
	}

	a.bumpChatActivity(r.Context(), chat.ID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "accepted", "session_id": sid})
}

// bindChatSession opens a root session on the agent AS the caller and stamps
// it onto the chat compare-and-set. A raced loser closes its own session and
// adopts the winner's — every concurrent first message lands in ONE session.
func (a *HubApp) bindChatSession(ctx context.Context, chat chatRow, base, bearer string) (string, error) {
	payload, _ := json.Marshal(map[string]any{
		"name": chat.Title,
		"metadata": map[string]any{
			"chat_id":    chat.ID,
			"project_id": deref(chat.ProjectID),
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/sessions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := gatewayClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return "", fmt.Errorf("open session: agent returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.SessionID == "" {
		return "", fmt.Errorf("open session: bad agent response: %v", err)
	}

	// CAS: the update filter matches ONLY an unbound row, so a raced second
	// binder writes nothing (postgres reports affected_rows 0 on success, so
	// the re-read below is the source of truth either way). On ANY failure
	// past this point the just-opened session must not be orphaned.
	if err := a.bindChatRow(ctx, chat.ID, out.SessionID); err != nil {
		a.closeAgentSession(ctx, base, bearer, out.SessionID)
		return "", err
	}
	after, err := a.fetchChat(ctx, chat.ID)
	if err != nil {
		a.closeAgentSession(ctx, base, bearer, out.SessionID)
		return "", err
	}
	won := deref(after.RootSessionID)
	if won == "" {
		a.closeAgentSession(ctx, base, bearer, out.SessionID)
		return "", fmt.Errorf("bind chat %s: root_session_id still empty after update", chat.ID)
	}
	if won != out.SessionID {
		// Lost the race — drop our session, use the winner's.
		a.closeAgentSession(ctx, base, bearer, out.SessionID)
	}
	return won, nil
}

// bindChatRow stamps the root session onto a STILL-UNBOUND chat row — the
// is_null filter is what makes the bind a compare-and-set.
func (a *HubApp) bindChatRow(ctx context.Context, chatID, sessionID string) error {
	res, err := a.client.Query(ctx,
		`mutation($id: String!, $sid: String!) {
			hub { db { update_chats(
				filter: { id: { eq: $id }, root_session_id: { is_null: true } }
				data: { root_session_id: $sid }
			) { affected_rows } } } }`,
		map[string]any{"id": chatID, "sid": sessionID},
	)
	if err != nil {
		return fmt.Errorf("bind chat: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return fmt.Errorf("bind chat: %w", res.Err())
	}
	return nil
}

// closeAgentSession best-effort deletes a session the bind path orphaned.
// Runs detached from the caller's deadline (cleanup must survive the very
// timeout/cancellation that triggered it), bounded by its own.
func (a *HubApp) closeAgentSession(ctx context.Context, base, bearer, sid string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, base+"/v1/sessions/"+sid, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", bearer)
	if resp, err := gatewayClient().Do(req); err == nil {
		resp.Body.Close()
	}
}

// cancelAgentSession best-effort cancels a session's in-flight work with cascade
// (aborts the root turn + terminates the sub-agent / mission subtree) WITHOUT
// terminating the root. The root stays resumable (status active) so archive is
// reversible — restore revives it and its schedules survive. Contrast
// closeAgentSession (drop), which terminates irreversibly. Detached deadline.
func (a *HubApp) cancelAgentSession(ctx context.Context, base, bearer, sid string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	body := strings.NewReader(`{"cascade":true,"reason":"user_archive: chat archived"}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/sessions/"+sid+"/cancel", body)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", bearer)
	req.Header.Set("Content-Type", "application/json")
	if resp, err := gatewayClient().Do(req); err == nil {
		resp.Body.Close()
	}
}

// bumpChatActivity moves the thread to the top of my_chats — called on
// message/inquiry submits only (organizing is not activity, spec §3).
// Best-effort: an activity miss must not fail the message.
func (a *HubApp) bumpChatActivity(ctx context.Context, chatID string) {
	if err := a.updateChatRow(ctx, chatID, map[string]any{
		"last_active_at": time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		a.logger.Warn("chat activity bump failed", "chat", chatID, "error", err)
	}
}

// lookupChat is the transport plane's chat-read seam — defaults to fetchChat
// (hugr, service principal); tests override chatLookup.
func (a *HubApp) lookupChat(ctx context.Context, id string) (chatRow, error) {
	if a.chatLookup != nil {
		return a.chatLookup(ctx, id)
	}
	return a.fetchChat(ctx, id)
}

// gatewayClient is the direct-call twin of the proxy path (shared transport,
// no global timeout — callers bound their own contexts).
func gatewayClient() *http.Client {
	return &http.Client{Transport: gatewayTransport}
}

// copyResponse relays a downstream response verbatim.
func copyResponse(w http.ResponseWriter, resp *http.Response) {
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
