package hubapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hugr-lab/hub/pkg/auth"
)

// chatTestApp wires a HubApp with fake runtime/access/chat seams, routed like
// Init does.
func chatTestApp(rt *fakeRuntime, chats map[string]chatRow) *http.ServeMux {
	a := &HubApp{
		logger:       slog.Default(),
		agentRuntime: rt,
		accessCheck:  allowAll,
		chatLookup: func(_ context.Context, id string) (chatRow, error) {
			c, ok := chats[id]
			if !ok {
				return chatRow{}, errChatNotFound
			}
			return c, nil
		},
	}
	mux := http.NewServeMux()
	registerChatTransport(mux, a)
	return mux
}

func strPtr(s string) *string { return &s }

func TestChatVerbs_ResolveChatToSession(t *testing.T) {
	var gotPath, gotAuth string
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		fmt.Fprint(w, `[]`)
	}))
	defer downstream.Close()

	mux := chatTestApp(
		&fakeRuntime{base: map[string]string{"a1": downstream.URL}},
		map[string]chatRow{"ch-1": {ID: "ch-1", UserID: "u1", AgentID: "a1", RootSessionID: strPtr("ses-9")}},
	)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, asUser(httptest.NewRequest("GET", "/api/v1/chats/ch-1/events?min_seq=5", nil), "u1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("events status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if gotPath != "/v1/sessions/ses-9/events" {
		t.Fatalf("downstream path = %q, want /v1/sessions/ses-9/events", gotPath)
	}
	if gotAuth != "Bearer user-token-u1" {
		t.Fatalf("downstream Authorization = %q — bearer must pass verbatim", gotAuth)
	}
}

func TestChatVerbs_UnboundChatConflicts(t *testing.T) {
	mux := chatTestApp(
		&fakeRuntime{base: map[string]string{"a1": "http://127.0.0.1:1"}},
		map[string]chatRow{"ch-1": {ID: "ch-1", UserID: "u1", AgentID: "a1"}}, // no session yet
	)
	for _, path := range []string{"/api/v1/chats/ch-1/stream", "/api/v1/chats/ch-1/events"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, asUser(httptest.NewRequest("GET", path, nil), "u1"))
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s status = %d, want 409", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "chat_not_bound") {
			t.Fatalf("%s body = %s, want chat_not_bound", path, rec.Body.String())
		}
	}
}

func TestChatVerbs_ForeignAndMissingChatsAre404(t *testing.T) {
	mux := chatTestApp(
		&fakeRuntime{base: map[string]string{"a1": "http://127.0.0.1:1"}},
		map[string]chatRow{"ch-1": {ID: "ch-1", UserID: "OWNER", AgentID: "a1", RootSessionID: strPtr("s")}},
	)
	for _, id := range []string{"ch-1" /* foreign */, "ch-ghost" /* missing */} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, asUser(httptest.NewRequest("GET", "/api/v1/chats/"+id+"/events", nil), "u1"))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("chat %s status = %d, want 404 (never leak foreign chats)", id, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "chat_not_found") {
			t.Fatalf("chat %s body = %s, want chat_not_found", id, rec.Body.String())
		}
	}
}

func TestChatVerbs_RevokedGrantIs403(t *testing.T) {
	a := &HubApp{
		logger:       slog.Default(),
		agentRuntime: &fakeRuntime{base: map[string]string{"a1": "http://127.0.0.1:1"}},
		accessCheck: func(context.Context, auth.UserInfo, string) error {
			return errors.New("forbidden: no access to agent a1")
		},
		chatLookup: func(context.Context, string) (chatRow, error) {
			return chatRow{ID: "ch-1", UserID: "u1", AgentID: "a1", RootSessionID: strPtr("s")}, nil
		},
	}
	mux := http.NewServeMux()
	registerChatTransport(mux, a)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, asUser(httptest.NewRequest("GET", "/api/v1/chats/ch-1/events", nil), "u1"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (revocation must bite on the next call)", rec.Code)
	}
}
