package hubapp

import (
	"context"
	"io"
	"os"
	"testing"
)

// TestIntegration_SkillsSeedAndCatalog runs the SK1 seeder end-to-end against a
// live Agent DB + a real FS byte-store (guarded: needs HUGR_URL +
// HUGR_SECRET_KEY). When HUB_STORAGE_PATH is set it seeds into the service's
// own byte-store so a running hub-service serves the result immediately.
func TestIntegration_SkillsSeedAndCatalog(t *testing.T) {
	skipIfNoHugr(t)
	app, _ := testClient(t)
	ctx := context.Background()

	storeDir := os.Getenv("HUB_STORAGE_PATH")
	if storeDir != "" {
		storeDir += "/system/skills"
	} else {
		storeDir = t.TempDir()
	}
	bs, err := NewFSBundleStore(storeDir)
	if err != nil {
		t.Fatalf("byte-store: %v", err)
	}
	app.bundleStore = bs
	t.Logf("byte-store: %s", storeDir)

	// Empty catalog must read as zero skills, not an error (ErrNoData → nil) —
	// this is what unblocks the seeder's readiness probe.
	if _, err := app.querySharedSkills(ctx); err != nil {
		t.Fatalf("pre-seed read errored (should be empty, not error): %v", err)
	}

	app.seedBundledSkills(ctx)

	rows, err := app.querySharedSkills(ctx)
	if err != nil {
		t.Fatalf("post-seed read: %v", err)
	}
	t.Logf("seeded shared skills: %d", len(rows))
	if len(rows) == 0 {
		t.Fatal("seeder produced no shared skills")
	}
	for _, r := range rows {
		h := r.ContentHash
		if len(h) > 26 {
			h = h[:26]
		}
		t.Logf("  %-28s v=%-16s %s", r.Name, r.Version, h)
	}

	// The first skill's bundle must be downloadable from the byte-store.
	one := rows[0]
	rc, err := app.bundleStore.Get(ctx, one.Name, one.Version)
	if err != nil {
		t.Fatalf("bundle get %s/%s: %v", one.Name, one.Version, err)
	}
	data, _ := io.ReadAll(rc)
	_ = rc.Close()
	t.Logf("bundle %s: %d bytes (gzip)", one.Name, len(data))
	if len(data) < 32 {
		t.Fatalf("bundle %s too small: %d bytes", one.Name, len(data))
	}
	if data[0] != 0x1f || data[1] != 0x8b {
		t.Fatalf("bundle %s not gzip (magic %x %x)", one.Name, data[0], data[1])
	}
}
