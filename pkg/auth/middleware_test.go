package auth

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testMiddleware(cfg AuthConfig) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
			return
		}
		user, ok := UserFromContext(r.Context())
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("no user in context"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(user.ID + "|" + user.Role + "|" + user.AuthType))
	})
	return Middleware(inner, cfg)
}

func TestMiddleware_HealthSkipped(t *testing.T) {
	h := testMiddleware(AuthConfig{SecretKey: "secret"})
	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestMiddleware_SecretKey(t *testing.T) {
	h := testMiddleware(AuthConfig{SecretKey: "my-secret", Logger: slog.Default()})

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("X-Hugr-Secret-Key", "my-secret")
	req.Header.Set("X-Hugr-User-Id", "admin-user")
	req.Header.Set("X-Hugr-Role", "admin")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); body != "admin-user|admin|management" {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestMiddleware_SecretKeyDefaults(t *testing.T) {
	h := testMiddleware(AuthConfig{SecretKey: "s"})

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("X-Hugr-Secret-Key", "s")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if body := rr.Body.String(); body != "admin|admin|management" {
		t.Fatalf("expected default admin, got: %s", body)
	}
}

func TestMiddleware_WrongSecretKey(t *testing.T) {
	h := testMiddleware(AuthConfig{SecretKey: "correct"})

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("X-Hugr-Secret-Key", "wrong")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestMiddleware_NoAuth(t *testing.T) {
	h := testMiddleware(AuthConfig{SecretKey: "secret"})

	req := httptest.NewRequest("GET", "/api/test", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestMiddleware_InvalidBearer(t *testing.T) {
	h := testMiddleware(AuthConfig{SecretKey: "secret", Logger: slog.Default()})

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Authorization", "Bearer invalid-token-no-dots")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// No agent validator, so should be 401
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestUserTransport(t *testing.T) {
	var capturedHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	transport := &UserTransport{Base: http.DefaultTransport}
	client := &http.Client{Transport: transport}

	req, _ := http.NewRequest("GET", srv.URL, nil)
	req = req.WithContext(ContextWithUser(req.Context(), UserInfo{
		ID: "user-1", Name: "Test User", Role: "analyst",
	}))

	_, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	if capturedHeaders.Get("X-Hugr-User-Id") != "user-1" {
		t.Fatalf("expected user-id header, got: %v", capturedHeaders)
	}
	if capturedHeaders.Get("X-Hugr-Role") != "analyst" {
		t.Fatalf("expected role header, got: %v", capturedHeaders)
	}
}

func TestUserContext(t *testing.T) {
	ctx := ContextWithUser(t.Context(), UserInfo{ID: "u1", Role: "admin", AuthType: "jwt"})
	user, ok := UserFromContext(ctx)
	if !ok {
		t.Fatal("user not found in context")
	}
	if user.ID != "u1" || user.Role != "admin" || user.AuthType != "jwt" {
		t.Fatalf("unexpected user: %+v", user)
	}

	_, ok = UserFromContext(t.Context())
	if ok {
		t.Fatal("should not find user in empty context")
	}
}

func TestIsJWT(t *testing.T) {
	tests := []struct {
		token string
		isJWT bool
	}{
		{"abc.def.ghi", true},
		{"header.payload.signature", true},
		{"no-dots-here", false},
		{"one.dot", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsJWT(tt.token); got != tt.isJWT {
			t.Errorf("IsJWT(%q) = %v, want %v", tt.token, got, tt.isJWT)
		}
	}
}
