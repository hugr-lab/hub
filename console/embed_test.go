package console

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandler_ServesShellAndFallsBack(t *testing.T) {
	h, fromDisk, err := Handler("/console/", "")
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if fromDisk {
		t.Fatalf("empty dir: fromDisk = true, want embedded")
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

// A valid HUB_CONSOLE_DIR serves the on-disk build; SPA-fallback still applies.
func TestHandler_ServesFromDisk(t *testing.T) {
	dir := t.TempDir()
	const marker = "DISK CONSOLE BUILD"
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>"+marker+"</html>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}

	h, fromDisk, err := Handler("/console/", dir)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if !fromDisk {
		t.Fatalf("valid dir: fromDisk = false, want disk")
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/console/roles/x", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("disk fallback: got status %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), marker) {
		t.Fatalf("disk fallback: body missing disk marker: %q", rec.Body.String())
	}
}

// A HUB_CONSOLE_DIR without an index.html is treated as misconfigured and falls
// back to the embedded build rather than serving a blank console.
func TestHandler_FallsBackWhenDirHasNoIndex(t *testing.T) {
	h, fromDisk, err := Handler("/console/", t.TempDir())
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if fromDisk {
		t.Fatalf("dir without index.html: fromDisk = true, want embedded fallback")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/console/", nil))
	if !strings.Contains(rec.Body.String(), "Hugr Hub Console") {
		t.Fatalf("fallback: body missing embedded shell marker")
	}
}
