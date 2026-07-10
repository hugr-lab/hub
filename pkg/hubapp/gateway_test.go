package hubapp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hugr-lab/hub/pkg/agentmgr"
	"github.com/hugr-lab/hub/pkg/auth"
)

// fakeRuntime satisfies agentmgr.AgentRuntime for gateway tests; only
// APIBaseURL carries behavior.
type fakeRuntime struct {
	base map[string]string // agentID → base URL; missing → error
}

func (f *fakeRuntime) Start(context.Context, agentmgr.AgentIdentity) error { return nil }
func (f *fakeRuntime) Stop(context.Context, string) error                  { return nil }
func (f *fakeRuntime) Remove(context.Context, string) error                { return nil }
func (f *fakeRuntime) Status(string) agentmgr.RuntimeState                 { return agentmgr.RuntimeState{} }
func (f *fakeRuntime) ListRunning() []agentmgr.RuntimeState                { return nil }
func (f *fakeRuntime) Observe(context.Context, string) (agentmgr.Observation, error) {
	return agentmgr.Observation{}, nil
}
func (f *fakeRuntime) ListManaged(context.Context) ([]agentmgr.ManagedRef, error) { return nil, nil }
func (f *fakeRuntime) SetSecretMinter(agentmgr.SecretMinter)                      {}
func (f *fakeRuntime) Reconstruct(context.Context)                                {}
func (f *fakeRuntime) APIBaseURL(agentID string) (string, error) {
	if b, ok := f.base[agentID]; ok {
		return b, nil
	}
	return "", fmt.Errorf("agent %q: no container", agentID)
}

// gatewayTestApp wires a HubApp with the fake runtime + a permissive access
// check, routed exactly like Init does.
func gatewayTestApp(rt agentmgr.AgentRuntime, access func(ctx context.Context, u auth.UserInfo, agentID string) error) *http.ServeMux {
	a := &HubApp{logger: slog.Default(), agentRuntime: rt, accessCheck: access}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/agents/{id}/hugen/{path...}", a.agentProxyHandler)
	return mux
}

func allowAll(context.Context, auth.UserInfo, string) error { return nil }

// asUser stamps an authenticated identity + bearer on a request, as the auth
// middleware would.
func asUser(r *http.Request, id string) *http.Request {
	r.Header.Set("Authorization", "Bearer user-token-"+id)
	return r.WithContext(auth.ContextWithUser(r.Context(), auth.UserInfo{ID: id, Name: id, Role: "user", AuthType: "jwt"}))
}

func TestAgentProxy_ForwardsPathQueryAndBearer(t *testing.T) {
	var got *http.Request
	var gotBody string
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer downstream.Close()

	mux := gatewayTestApp(&fakeRuntime{base: map[string]string{"a1": downstream.URL}}, allowAll)

	req := asUser(httptest.NewRequest("POST", "/api/v1/agents/a1/hugen/v1/sessions/s1/messages?x=1", strings.NewReader(`{"text":"hi"}`)), "u1")
	// A dev/ops caller authenticates to the HUB with these — they must stop here.
	req.Header.Set("X-Hugr-Secret-Key", "super-secret")
	req.Header.Set("X-Hugr-User-Id", "u1")
	req.Header.Set("X-Hugr-Impersonated-User-Id", "someone")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}
	if got.URL.Path != "/v1/sessions/s1/messages" {
		t.Fatalf("downstream path = %q, want /v1/sessions/s1/messages", got.URL.Path)
	}
	if got.URL.RawQuery != "x=1" {
		t.Fatalf("downstream query = %q, want x=1", got.URL.RawQuery)
	}
	if h := got.Header.Get("Authorization"); h != "Bearer user-token-u1" {
		t.Fatalf("downstream Authorization = %q — the user bearer must pass verbatim", h)
	}
	for _, k := range []string{"X-Hugr-Secret-Key", "X-Hugr-User-Id", "X-Hugr-Impersonated-User-Id"} {
		if v := got.Header.Get(k); v != "" {
			t.Fatalf("downstream %s = %q — hub credentials must never reach the container", k, v)
		}
	}
	if got.Header.Get("X-Forwarded-For") == "" {
		t.Fatal("downstream X-Forwarded-For missing")
	}
	if gotBody != `{"text":"hi"}` {
		t.Fatalf("downstream body = %q", gotBody)
	}
}

func TestAgentProxy_SSEStreamsIncrementally(t *testing.T) {
	release := make(chan struct{})
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprintf(w, "id: 1\ndata: {\"seq\":1}\n\n")
		fl.Flush()
		<-release // hold the stream open: the first event must arrive anyway
		fmt.Fprintf(w, "id: 2\ndata: {\"seq\":2}\n\n")
	}))
	defer downstream.Close()
	defer close(release)

	mux := gatewayTestApp(&fakeRuntime{base: map[string]string{"a1": downstream.URL}}, allowAll)
	// A real server (not a Recorder) so the response streams over a socket; the
	// shim stamps the identity the auth middleware would.
	shim := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, asUser(r, "u1"))
	}))
	defer shim.Close()

	req, _ := http.NewRequest("GET", shim.URL+"/api/v1/agents/a1/hugen/v1/sessions/s1/stream", nil)
	req.Header.Set("Last-Event-ID", "0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stream request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d", resp.StatusCode)
	}
	// The first event must be readable while the downstream handler is still
	// blocked — proves flush-through, not whole-response buffering.
	r := bufio.NewReader(resp.Body)
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("read first SSE line: %v", err)
	}
	if !strings.HasPrefix(line, "id: 1") {
		t.Fatalf("first SSE line = %q, want id: 1", line)
	}
}

func TestAgentProxy_ErrorMapping(t *testing.T) {
	t.Run("no container -> 503 agent_not_running", func(t *testing.T) {
		// agentForToken refinement needs a hugr client — the fake path errors on
		// lookup, so the generic active-agent code is used. (agentForToken with a
		// nil client would panic; keep the runtime error path client-free by
		// pointing at an agent the fake knows nothing about via accessCheck ok.)
		mux := gatewayTestApp(&fakeRuntime{}, allowAll)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, asUser(httptest.NewRequest("GET", "/api/v1/agents/ghost/hugen/v1/agent", nil), "u1"))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "agent_not_running") {
			t.Fatalf("body = %s, want agent_not_running", rec.Body.String())
		}
		if rec.Header().Get("Retry-After") == "" {
			t.Fatal("503 must carry Retry-After")
		}
	})

	t.Run("dead endpoint -> 502 agent_unreachable", func(t *testing.T) {
		mux := gatewayTestApp(&fakeRuntime{base: map[string]string{"a1": "http://127.0.0.1:1"}}, allowAll)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, asUser(httptest.NewRequest("GET", "/api/v1/agents/a1/hugen/v1/agent", nil), "u1"))
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "agent_unreachable") {
			t.Fatalf("body = %s, want agent_unreachable", rec.Body.String())
		}
	})

	t.Run("no runtime -> 503", func(t *testing.T) {
		mux := gatewayTestApp(nil, allowAll)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, asUser(httptest.NewRequest("GET", "/api/v1/agents/a1/hugen/v1/agent", nil), "u1"))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rec.Code)
		}
	})
}

func TestAgentProxy_Authz(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer downstream.Close()
	rt := &fakeRuntime{base: map[string]string{"a1": downstream.URL}}

	t.Run("denied grant -> 403 no_agent_access", func(t *testing.T) {
		mux := gatewayTestApp(rt, func(context.Context, auth.UserInfo, string) error {
			return errors.New("forbidden: no access to agent a1")
		})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, asUser(httptest.NewRequest("GET", "/api/v1/agents/a1/hugen/v1/agent", nil), "u2"))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "no_agent_access") {
			t.Fatalf("body = %s, want no_agent_access", rec.Body.String())
		}
	})

	t.Run("no identity -> 401", func(t *testing.T) {
		mux := gatewayTestApp(rt, allowAll)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/agents/a1/hugen/v1/agent", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("secret-key caller without bearer -> 403 user_token_required", func(t *testing.T) {
		mux := gatewayTestApp(rt, allowAll)
		req := httptest.NewRequest("GET", "/api/v1/agents/a1/hugen/v1/agent", nil)
		// Management auth: identity present, no Authorization header.
		req = req.WithContext(auth.ContextWithUser(req.Context(), auth.UserInfo{ID: "ops", AuthType: "management"}))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "user_token_required") {
			t.Fatalf("body = %s, want user_token_required", rec.Body.String())
		}
	})
}
