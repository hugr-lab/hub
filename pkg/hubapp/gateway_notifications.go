package hubapp

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Stage 2 (console notifications): a per-chat read cursor + a cheap activity
// aggregate. The bell + unread badges poll /chats/activity (~20s); read state is
// advanced by the frontend via /chats/{id}/read. No per-chat SSE — the hub load
// is O(users), not O(chats)·O(events). See design memory
// project_console_notifications_arch.

// chatArchiveHandler archives a chat and CANCELS its session's in-flight work
// (cascade — stops the turn + kills any mission subtree) without terminating the
// root. The session stays resumable, so restore revives it fully (continue +
// schedules). POST /api/v1/chats/{id}/archive. Reversible.
func (a *HubApp) chatArchiveHandler(w http.ResponseWriter, r *http.Request) {
	a.archiveChat(w, r, false)
}

// chatDropHandler archives a chat and TERMINATES its session (graceful
// CloseSession — /end, keeps history but the session can't be revived). Restore
// then shows read-only history. POST /api/v1/chats/{id}/drop. Not reversible.
func (a *HubApp) chatDropHandler(w http.ResponseWriter, r *http.Request) {
	a.archiveChat(w, r, true)
}

// archiveChat is the shared archive path: owner-check, act on the bound session
// (cancel for archive / terminate for drop), then flip archived=true. Nothing is
// deleted either way. A stopped agent has no live session to act on — skip.
func (a *HubApp) archiveChat(w http.ResponseWriter, r *http.Request, drop bool) {
	_, chat, ok := a.chatContext(w, r)
	if !ok {
		return
	}
	if sid := deref(chat.RootSessionID); sid != "" && a.agentRuntime != nil {
		if base, err := a.agentRuntime.APIBaseURL(chat.AgentID); err == nil {
			bearer := r.Header.Get("Authorization")
			if drop {
				a.closeAgentSession(r.Context(), base, bearer, sid) // /end — irreversible
			} else {
				a.cancelAgentSession(r.Context(), base, bearer, sid) // cancel turn — resumable
			}
		}
	}
	if err := a.setChatArchived(r.Context(), chat.ID, true); err != nil {
		a.logger.Error("chat archive", "chat", chat.ID, "drop", drop, "err", err)
		gatewayError(w, http.StatusInternalServerError, "archive_failed", "could not archive the chat")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// setChatArchived flips a chat's archived flag (owner already verified by the
// caller). Used by the close endpoint; restore goes through the update_chat
// table function on the console side.
func (a *HubApp) setChatArchived(ctx context.Context, chatID string, archived bool) error {
	res, err := a.client.Query(ctx,
		`mutation($id: String!, $ar: Boolean!) {
			hub { db { update_chats(filter: { id: { eq: $id } }, data: { archived: $ar }) { affected_rows } } } }`,
		map[string]any{"id": chatID, "ar": archived})
	if err != nil {
		return err
	}
	defer res.Close()
	return res.Err()
}

// chatReadHandler advances a chat's read cursor. POST /api/v1/chats/{id}/read
// with {"seq": N}. CAS-style (the filter only matches a strictly-lower cursor)
// so a stale / out-of-order request can never un-read newer messages. Owner-
// scoped via chatContext.
func (a *HubApp) chatReadHandler(w http.ResponseWriter, r *http.Request) {
	_, chat, ok := a.chatContext(w, r)
	if !ok {
		return
	}
	var body struct {
		Seq int `json:"seq"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	res, err := a.client.Query(r.Context(),
		`mutation($id: String!, $seq: Int!) {
			hub { db { update_chats(
				filter: { id: { eq: $id }, last_read_seq: { lt: $seq } }
				data: { last_read_seq: $seq }
			) { affected_rows } } } }`,
		map[string]any{"id": chat.ID, "seq": body.Seq})
	if err == nil {
		defer res.Close()
		err = res.Err()
	}
	if err != nil {
		a.logger.Error("chat read-cursor update", "chat", chat.ID, "err", err)
		gatewayError(w, http.StatusInternalServerError, "read_update_failed", "could not update read cursor")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// chatActivity is one chat's unread summary — enough to drive a list badge and a
// bell line (title + count + last-event kind), never message content.
type chatActivity struct {
	ChatID    string          `json:"chat_id"`
	Title     string          `json:"title"`
	AgentID   string          `json:"agent_id"`
	LastSeq   int             `json:"last_seq"`
	Unread    int             `json:"unread"`
	LastEvent *lastEventBrief `json:"last_event,omitempty"`
}

type lastEventBrief struct {
	Kind string `json:"kind"` // event_type of the highest-seq event
	At   string `json:"at"`   // its created_at
}

// chatsActivityHandler returns unread activity for the caller's active chats.
// GET /api/v1/chats/activity. Two queries total regardless of chat count:
// (1) the caller's non-archived chats + read cursors from hub.db, (2) the latest
// event (seq/kind/at) per bound session from hub.agent.db via one nested
// subquery. unread = last_seq − last_read_seq. Remote-mode agents only — a
// local-mode agent writes its own host DB, invisible to this aggregate.
func (a *HubApp) chatsActivityHandler(w http.ResponseWriter, r *http.Request) {
	u, ok := a.gatewayCaller(w, r)
	if !ok {
		return
	}
	chats, err := a.userActiveChats(r.Context(), u.ID)
	if err != nil {
		a.logger.Error("activity: list chats", "user", u.ID, "err", err)
		gatewayError(w, http.StatusInternalServerError, "activity_failed", "could not load chat activity")
		return
	}
	// Only surface unread for chats whose agent is currently running — a
	// paused/stopped agent produces no live activity, so its stale unread would
	// just be noise. When no runtime is wired (non-container dev), don't filter.
	if a.agentRuntime != nil {
		kept := chats[:0]
		for _, c := range chats {
			if a.agentRuntime.Status(c.AgentID).Status == "running" {
				kept = append(kept, c)
			}
		}
		chats = kept
	}
	// Collect bound session ids to resolve their latest event in one query.
	ids := make([]string, 0, len(chats))
	for _, c := range chats {
		if sid := deref(c.RootSessionID); sid != "" {
			ids = append(ids, sid)
		}
	}
	notable, err := a.notableEventsBySession(r.Context(), ids)
	if err != nil {
		a.logger.Error("activity: notable events", "user", u.ID, "err", err)
		gatewayError(w, http.StatusInternalServerError, "activity_failed", "could not load chat activity")
		return
	}

	out := make([]chatActivity, 0, len(chats))
	for _, c := range chats {
		act := chatActivity{ChatID: c.ID, Title: c.Title, AgentID: c.AgentID}
		if evs := notable[deref(c.RootSessionID)]; len(evs) > 0 {
			act.LastSeq = evs[0].Seq // newest notable event's seq
			for _, e := range evs {
				if e.Seq > c.LastReadSeq {
					act.Unread++
				}
			}
			act.LastEvent = &lastEventBrief{Kind: evs[0].EventType, At: evs[0].CreatedAt.Format(time.RFC3339)}
		}
		out = append(out, act)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"chats": out})
}

// activityChatCap bounds how many recent active chats one /activity call scans.
// The unread of an older-than-cap chat simply isn't surfaced until it resurfaces
// (a new event bumps its last_active_at back into the window).
const activityChatCap = 100

// userActiveChats returns the caller's non-archived chats (id, root_session_id,
// last_read_seq …), newest-active first, capped.
func (a *HubApp) userActiveChats(ctx context.Context, userID string) ([]chatRow, error) {
	res, err := a.client.Query(ctx,
		`query($filter: hub_db_chats_filter, $limit: Int!) {
			hub { db { chats(
				filter: $filter,
				order_by: [{field: "last_active_at", direction: DESC}, {field: "id", direction: DESC}],
				limit: $limit
			) { `+chatProjection+` } } } }`,
		map[string]any{
			"filter": map[string]any{
				"user_id":  map[string]any{"eq": userID},
				"archived": map[string]any{"eq": false},
			},
			"limit": activityChatCap,
		})
	if err != nil {
		return nil, err
	}
	defer res.Close()
	if res.Err() != nil {
		return nil, res.Err()
	}
	var rows []chatRow
	if err := res.ScanData("hub.db.chats", &rows); err != nil && !isNoData(err) {
		return nil, err
	}
	return rows, nil
}

// sessionEvent is one notable session event — seq for the unread cursor, kind/at
// for the bell line.
type sessionEvent struct {
	Seq       int
	EventType string
	CreatedAt time.Time
}

// notableEventTypes are the session_events kinds that count as user-facing
// notifications: an agent reply (also how an async schedule-fire result lands)
// and a pending approval. NOT internal noise (session_status / reasoning /
// tool_* dominate the log). Artifacts (extension_frame + an op inside metadata)
// and errors are a later refinement — they need op-level filtering.
var notableEventTypes = []string{"agent_message", "inquiry_request"}

// notablePerSessionCap bounds the notable events fetched per session; unread
// counts within it, so a session with more than this many unread reports the
// cap (a "N+"-style approximation the UI can render).
const notablePerSessionCap = 50

// notableEventsBySession returns the recent notable events per session (seq DESC)
// in ONE nested query. The handler counts those past each chat's read cursor for
// the unread badge and takes the newest for the bell line. Empty ids → no
// round-trip.
func (a *HubApp) notableEventsBySession(ctx context.Context, ids []string) (map[string][]sessionEvent, error) {
	out := map[string][]sessionEvent{}
	if len(ids) == 0 {
		return out, nil
	}
	res, err := a.client.Query(ctx,
		`query($ids: [String!], $ef: hub_agent_db_session_events_filter, $n: Int) {
			hub { agent { db { sessions(filter: { id: { in: $ids } }) {
				id
				events(filter: $ef, nested_order_by: [{field: "seq", direction: DESC}], nested_limit: $n) {
					seq event_type created_at
				}
			} } } }
		}`,
		map[string]any{
			"ids": ids,
			"ef":  map[string]any{"event_type": map[string]any{"in": notableEventTypes}},
			"n":   notablePerSessionCap,
		})
	if err != nil {
		return nil, err
	}
	defer res.Close()
	if res.Err() != nil {
		return nil, res.Err()
	}
	var rows []struct {
		ID     string `json:"id"`
		Events []struct {
			Seq       int       `json:"seq"`
			EventType string    `json:"event_type"`
			CreatedAt time.Time `json:"created_at"`
		} `json:"events"`
	}
	if err := res.ScanData("hub.agent.db.sessions", &rows); err != nil && !isNoData(err) {
		return nil, err
	}
	for _, row := range rows {
		evs := make([]sessionEvent, 0, len(row.Events))
		for _, e := range row.Events {
			evs = append(evs, sessionEvent{Seq: e.Seq, EventType: e.EventType, CreatedAt: e.CreatedAt})
		}
		out[row.ID] = evs
	}
	return out, nil
}
