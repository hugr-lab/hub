package console

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler_ServesShellAndFallsBack(t *testing.T) {
	h, err := Handler("/console/")
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	// Happy path: the embedded index.html shell is served at the mount root.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/console/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("root: got status %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Hugr Hub Console") {
		t.Fatalf("root: body missing shell marker: %q", rec.Body.String())
	}

	// Edge: an unknown deep-link path is not an embedded asset → SPA fallback
	// returns the same shell (200) so the client-side router owns it.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/console/agents/nope", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("fallback: got status %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Hugr Hub Console") {
		t.Fatalf("fallback: body missing shell marker")
	}
}
