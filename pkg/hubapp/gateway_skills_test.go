package hubapp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hugr-lab/hub/pkg/auth"
)

// TestAgentProxy_BlocksSkills: the generic /hugen/ passthrough refuses
// /v1/skills/* — skills go exclusively through the dedicated
// /api/v1/agents/{id}/skills endpoints (owner-gated), so a member cannot reach
// /v1/skills/install via the raw proxy.
func TestAgentProxy_BlocksSkills(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer downstream.Close()
	rt := &fakeRuntime{base: map[string]string{"a1": downstream.URL}}
	mux := gatewayTestApp(rt, allowAll)

	blocked := []struct{ method, path string }{
		{"POST", "/api/v1/agents/a1/hugen/v1/skills/install"},
		{"GET", "/api/v1/agents/a1/hugen/v1/skills/hugr-data/export"},
		{"GET", "/api/v1/agents/a1/hugen/v1/skills"},
	}
	for _, b := range blocked {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, asUser(httptest.NewRequest(b.method, b.path, strings.NewReader("x")), "u1"))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: status %d, want 404 (skills blocked on raw proxy)", b.method, b.path, rec.Code)
		}
	}

	// A non-skills path still proxies through.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, asUser(httptest.NewRequest("GET", "/api/v1/agents/a1/hugen/v1/agent", nil), "u1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("non-skills path: status %d, want 200", rec.Code)
	}
}

// TestAgentSkillsEndpoints exercises the hub-native dedicated skills endpoints
// (the console's path): list is member+, export/install are owner/admin.
func TestAgentSkillsEndpoints(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer downstream.Close()
	rt := &fakeRuntime{base: map[string]string{"a1": downstream.URL}}

	app := func(owner func(context.Context, auth.UserInfo, string) error) *http.ServeMux {
		a := &HubApp{logger: slog.Default(), agentRuntime: rt, accessCheck: allowAll, ownerCheck: owner}
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/v1/agents/{id}/skills", a.agentSkillsListHandler)
		mux.HandleFunc("GET /api/v1/agents/{id}/skills/{name}/export", a.agentSkillExportHandler)
		mux.HandleFunc("POST /api/v1/agents/{id}/skills/install", a.agentSkillInstallHandler)
		return mux
	}
	denyOwner := func(context.Context, auth.UserInfo, string) error {
		return errors.New("forbidden: owner access required")
	}

	cases := []struct {
		name       string
		req        *http.Request
		owner      func(context.Context, auth.UserInfo, string) error
		wantStatus int
		wantBody   string
	}{
		{"list member ok", httptest.NewRequest("GET", "/api/v1/agents/a1/skills", nil), denyOwner, http.StatusOK, ""},
		{"install member denied", httptest.NewRequest("POST", "/api/v1/agents/a1/skills/install", strings.NewReader("x")), denyOwner, http.StatusForbidden, "owner_required"},
		{"install owner ok", httptest.NewRequest("POST", "/api/v1/agents/a1/skills/install", strings.NewReader("x")), allowAll, http.StatusOK, ""},
		{"export member denied", httptest.NewRequest("GET", "/api/v1/agents/a1/skills/hugr-data/export", nil), denyOwner, http.StatusForbidden, "owner_required"},
		{"export owner ok", httptest.NewRequest("GET", "/api/v1/agents/a1/skills/hugr-data/export", nil), allowAll, http.StatusOK, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			app(c.owner).ServeHTTP(rec, asUser(c.req, "u1"))
			if rec.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, c.wantStatus, rec.Body.String())
			}
			if c.wantBody != "" && !strings.Contains(rec.Body.String(), c.wantBody) {
				t.Fatalf("body = %s, want contains %q", rec.Body.String(), c.wantBody)
			}
		})
	}
}
