package hubapp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestFSBundleStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := NewFSBundleStore(dir)
	if err != nil {
		t.Fatalf("NewFSBundleStore: %v", err)
	}
	ctx := context.Background()
	payload := []byte("fake-tar-gz-bytes")

	// Absent before Put.
	if ok, err := st.Exists(ctx, "hugr-data", "0.0.1"); err != nil || ok {
		t.Fatalf("Exists before Put = (%v,%v), want (false,nil)", ok, err)
	}
	if _, err := st.Get(ctx, "hugr-data", "0.0.1"); !errors.Is(err, ErrBundleNotFound) {
		t.Fatalf("Get before Put = %v, want ErrBundleNotFound", err)
	}

	res, err := st.Put(ctx, "hugr-data", "0.0.1", bytes.NewReader(payload), PublisherIdentity{AgentID: "gw-test-1", Role: "agent:gw-test-1"})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if res.Status != StatusPublished {
		t.Fatalf("Put status = %q, want published", res.Status)
	}
	if res.Location != filepath.Join("hugr-data", "0.0.1.tar.gz") {
		t.Fatalf("Put location = %q", res.Location)
	}

	// Present after Put; round-trips byte-exact.
	if ok, err := st.Exists(ctx, "hugr-data", "0.0.1"); err != nil || !ok {
		t.Fatalf("Exists after Put = (%v,%v), want (true,nil)", ok, err)
	}
	rc, err := st.Get(ctx, "hugr-data", "0.0.1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !bytes.Equal(got, payload) {
		t.Fatalf("Get bytes = %q, want %q", got, payload)
	}

	// No temp file left behind (atomic rename).
	entries, _ := os.ReadDir(filepath.Join(dir, "hugr-data"))
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("stray temp file: %s", e.Name())
		}
	}
}

func TestFSBundleStore_RejectsTraversal(t *testing.T) {
	st, _ := NewFSBundleStore(t.TempDir())
	ctx := context.Background()
	cases := []struct{ name, version string }{
		{"../escape", "0.0.1"},
		{"ok", "../escape"},
		{"ok", ".."},
		{"has/slash", "0.0.1"},
		{"ok", "0.0.1/../../etc"},
		{"UPPER", "0.0.1"},
	}
	for _, c := range cases {
		if _, err := st.Put(ctx, c.name, c.version, bytes.NewReader([]byte("x")), PublisherIdentity{}); err == nil {
			t.Errorf("Put(%q,%q) succeeded, want rejection", c.name, c.version)
		}
		if _, err := st.Get(ctx, c.name, c.version); err == nil {
			t.Errorf("Get(%q,%q) succeeded, want rejection", c.name, c.version)
		}
	}
}
