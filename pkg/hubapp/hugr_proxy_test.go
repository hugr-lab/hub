package hubapp

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hugr-lab/hub/pkg/auth"
)

// TestHugrProxy_ForwardsCallerBearer pins the /hugr identity contract: a
// JWT-authenticated caller's own bearer goes to hugr verbatim (native jwt
// auth — mutating functions execute), NOT downgraded to secret-key
// impersonation (under which hugr silently no-ops mutating functions,
// query-engine ask #7).
func TestHugrProxy_ForwardsCallerBearer(t *testing.T) {
	var got http.Header
	hugr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Write([]byte(`{"data":{}}`))
	}))
	defer hugr.Close()

	a := &HubApp{config: Config{HugrURL: hugr.URL + "/ipc", HugrSecretKey: "sk"}, logger: slog.Default()}
	h := a.hugrProxyHandler()

	req := httptest.NewRequest("POST", "/hugr", strings.NewReader(`{"query":"{ __typename }"}`))
	req.Header.Set("Authorization", "Bearer user-jwt")
	req = req.WithContext(auth.ContextWithUser(req.Context(), auth.UserInfo{ID: "u1", Role: "user", AuthType: "jwt"}))
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if v := got.Get("Authorization"); v != "Bearer user-jwt" {
		t.Fatalf("hugr Authorization = %q — the caller's bearer must pass verbatim", v)
	}
	for _, k := range []string{"X-Hugr-Secret-Key", "X-Hugr-Impersonated-User-Id", "X-Hugr-Impersonated-Role"} {
		if v := got.Get(k); v != "" {
			t.Fatalf("hugr %s = %q — a bearer caller must not be downgraded to impersonation", k, v)
		}
	}
}

// TestHugrProxy_ManagementUsesImpersonation pins the secret-key path: no
// bearer to forward, so the hub authenticates with its management key and
// carries the (possibly header-chosen) identity via impersonation.
func TestHugrProxy_ManagementUsesImpersonation(t *testing.T) {
	var got http.Header
	hugr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Write([]byte(`{"data":{}}`))
	}))
	defer hugr.Close()

	a := &HubApp{config: Config{HugrURL: hugr.URL, HugrSecretKey: "sk"}, logger: slog.Default()}
	h := a.hugrProxyHandler()

	req := httptest.NewRequest("POST", "/hugr", strings.NewReader(`{"query":"{ __typename }"}`))
	req = req.WithContext(auth.ContextWithUser(req.Context(), auth.UserInfo{ID: "ops", Name: "ops", Role: "admin", AuthType: "management"}))
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got.Get("X-Hugr-Secret-Key") != "sk" {
		t.Fatal("management caller must authenticate with the secret key")
	}
	if got.Get("X-Hugr-Impersonated-User-Id") != "ops" || got.Get("X-Hugr-Impersonated-Role") != "admin" {
		t.Fatalf("impersonation headers missing: %v", got)
	}
	if got.Get("Authorization") != "" {
		t.Fatal("management caller has no bearer to forward")
	}
}

// TestHugrProxy_ManagementBearerNotForwarded pins the security gate: a
// secret-key caller that ALSO carries a stray Authorization header must NOT
// have that bearer forwarded to hugr — a management identity is never
// downgraded to (or upgraded from) a caller-supplied token. The proxy
// authenticates with its own secret and impersonates, ignoring the bearer.
func TestHugrProxy_ManagementBearerNotForwarded(t *testing.T) {
	var got http.Header
	hugr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Write([]byte(`{"data":{}}`))
	}))
	defer hugr.Close()

	a := &HubApp{config: Config{HugrURL: hugr.URL, HugrSecretKey: "sk"}, logger: slog.Default()}
	h := a.hugrProxyHandler()

	req := httptest.NewRequest("POST", "/hugr", strings.NewReader(`{"query":"{ __typename }"}`))
	req.Header.Set("Authorization", "Bearer stray-token")
	req = req.WithContext(auth.ContextWithUser(req.Context(), auth.UserInfo{ID: "ops", Name: "ops", Role: "admin", AuthType: "management"}))
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got.Get("Authorization") == "Bearer stray-token" {
		t.Fatal("management caller's stray bearer must not be forwarded to hugr")
	}
	if got.Get("X-Hugr-Secret-Key") != "sk" {
		t.Fatal("management caller must authenticate with the secret key regardless of a stray bearer")
	}
	if got.Get("X-Hugr-Impersonated-User-Id") != "ops" {
		t.Fatalf("impersonation headers missing: %v", got)
	}
}
