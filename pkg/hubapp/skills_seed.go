package hubapp

// skills_seed.go — SK1 marketplace seed. On boot (async, AFTER hugr is up —
// like the supervisor, NEVER from Init) the hub publishes the skills embedded
// in the hugen binary (assets.SkillsFS — the current hub-tier bundle) into the
// marketplace: bundle bytes → BundleStore, a shared:true row → the Agent DB
// `skills` catalog. This gives a fresh deployment a non-empty catalog before
// any agent authors + publishes (SK3). The owner's call (2026-07-13): the
// initial catalog content IS the skills hugen ships today.
//
// Idempotent by the canonical content hash (skill.BundleHash): an unchanged
// bundle is skipped; a bundle changed across a hugen release re-publishes.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/hugr-lab/hugen/assets"
	"github.com/hugr-lab/hugen/pkg/skill"
)

const (
	// marketplaceSeedAgentID owns the seeded catalog rows. A reserved,
	// non-agent id so the rows never collide with a real agent's per-agent
	// index and the privileged catalog read (shared=true) always finds them.
	marketplaceSeedAgentID = "system"
	// marketplaceSeedSource tags seeded rows in the skills.source enum.
	marketplaceSeedSource = "hub"
)

// startSkillsSeed launches the async catalog seed. It runs on a background
// context (like the supervisor) and waits for hugr to come up — Init runs as
// hugr's _mount/init, before hugr's HTTP is listening, so the seed's catalog
// writes cannot happen inline. Readiness is probed with the actual catalog
// read; once it succeeds the seed runs once.
func (a *HubApp) startSkillsSeed() {
	if a.bundleStore == nil {
		return
	}
	go func() {
		ctx := context.Background()
		for i := 0; i < 45; i++ { // ~3m of 4s probes
			if _, err := a.querySharedSkills(ctx); err == nil {
				a.seedBundledSkills(ctx)
				return
			}
			time.Sleep(4 * time.Second)
		}
		a.logger.Warn("skills seed: hugr not reachable after retries — catalog not seeded")
	}()
}

// seedBundledSkills seeds the embedded hub-tier bundles into the marketplace
// INSERT-IF-ABSENT (owner decision 2026-07-15): a bundled skill is seeded only
// when its `system` catalog row does not yet exist — a first deployment, or a
// genuinely new bundled skill in a later release. The hub NEVER auto-updates an
// existing `system` row from disk on restart; once the catalog exists, all
// skill changes flow through the running hub's API (publish / admin tooling).
// (Runs async post-readiness, not in Init — hugr connects hubapp as a source
// before it accepts requests.) Best-effort per skill: one failure logs on.
func (a *HubApp) seedBundledSkills(ctx context.Context) {
	if a.bundleStore == nil {
		return
	}
	entries, err := fs.ReadDir(assets.SkillsFS, "skills")
	if err != nil {
		a.logger.Warn("skills seed: read embed", "error", err)
		return
	}
	// One catalog read decides what's already seeded (system-owned shared rows).
	rows, err := a.querySharedSkills(ctx)
	if err != nil {
		a.logger.Warn("skills seed: read catalog", "error", err)
		return
	}
	present := make(map[string]string, len(rows)) // name → system row's version
	for _, r := range rows {
		if r.AgentID == marketplaceSeedAgentID {
			present[r.Name] = r.Version
		}
	}
	var seeded, repaired, skipped, failed int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if err := validBundleName(name); err != nil {
			a.logger.Warn("skills seed: skip non-kebab name", "name", name)
			failed++
			continue
		}
		if version, ok := present[name]; ok {
			// Row already seeded → NEVER update it from disk. But keep downloads
			// working: if its bytes are missing (a fresh / reset byte-store with a
			// persisted catalog), re-put them — a byte repair, not a catalog
			// change. Cheap in steady state (just an Exists check).
			if has, _ := a.bundleStore.Exists(ctx, name, version); has {
				skipped++
				continue
			}
			if err := a.repairBundleBytes(ctx, name, version); err != nil {
				a.logger.Warn("skills seed: byte repair skipped", "skill", name, "error", err)
				failed++
			} else {
				repaired++
			}
			continue
		}
		if err := a.seedOneBundledSkill(ctx, name); err != nil {
			a.logger.Warn("skills seed: skill failed", "skill", name, "error", err)
			failed++
			continue
		}
		seeded++
	}
	a.logger.Info("skills marketplace seeded (insert-if-absent)",
		"seeded", seeded, "bytes_repaired", repaired, "already_present", skipped, "failed", failed)
}

// repairBundleBytes re-puts a seeded skill's bundle bytes into the byte-store
// when they are missing (a fresh/reset byte-store against a persisted catalog),
// WITHOUT touching the catalog row. It only repairs when the embed still hashes
// to the row's version — a diverged row (an older release) can't be repaired
// from the current disk and is left for the admin.
func (a *HubApp) repairBundleBytes(ctx context.Context, name, wantVersion string) error {
	sub, err := fs.Sub(assets.SkillsFS, "skills/"+name)
	if err != nil {
		return fmt.Errorf("sub-fs: %w", err)
	}
	hash, err := skill.BundleHash(sub)
	if err != nil {
		return fmt.Errorf("hash: %w", err)
	}
	if v := shortVersion(hash); v != wantVersion {
		return fmt.Errorf("embed version %s != catalog version %s (diverged — admin must re-seed)", v, wantVersion)
	}
	tarball, err := tarGzBundle(sub)
	if err != nil {
		return fmt.Errorf("tar: %w", err)
	}
	if _, err := a.bundleStore.Put(ctx, name, wantVersion, bytes.NewReader(tarball),
		PublisherIdentity{UserID: marketplaceSeedAgentID, Role: "admin"}); err != nil {
		return fmt.Errorf("byte-store put: %w", err)
	}
	return nil
}

func (a *HubApp) seedOneBundledSkill(ctx context.Context, name string) error {
	sub, err := fs.Sub(assets.SkillsFS, "skills/"+name)
	if err != nil {
		return fmt.Errorf("sub-fs: %w", err)
	}
	mdBytes, err := fs.ReadFile(sub, "SKILL.md")
	if err != nil {
		return fmt.Errorf("read SKILL.md: %w", err)
	}
	man, err := skill.Parse(mdBytes)
	if err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}
	hash, err := skill.BundleHash(sub)
	if err != nil {
		return fmt.Errorf("hash: %w", err)
	}
	version := shortVersion(hash)

	// Called only for a name with no `system` row yet (seedBundledSkills gates on
	// presence), so this always materialises the bundle + inserts the row.
	tarball, err := tarGzBundle(sub)
	if err != nil {
		return fmt.Errorf("tar: %w", err)
	}
	res, err := a.bundleStore.Put(ctx, name, version, bytes.NewReader(tarball),
		PublisherIdentity{UserID: marketplaceSeedAgentID, Role: "admin"})
	if err != nil {
		return fmt.Errorf("byte-store put: %w", err)
	}

	md := man.Metadata
	if md == nil {
		md = map[string]any{} // skills.metadata is NOT NULL
	}
	// Reserved-name enforcement: a bundled name belongs to `system`. The publish
	// gate's reserved-name check only blocks NEW publishes once the system row
	// exists; a third party that squatted this name BEFORE the first seed would
	// otherwise coexist. Evict any non-system row for the name as the seed
	// claims it (best-effort — a leftover squatter never wins a download anyway,
	// since sharedSkillByName prefers the system row).
	if err := a.evictForeignSharedRows(ctx, name); err != nil {
		a.logger.Warn("skills seed: evict foreign rows for reserved name", "skill", name, "error", err)
	}
	if err := a.upsertSkillRow(ctx, marketplaceSeedAgentID, marketplaceSeedSource, seededSkillRow{
		Name:           name,
		Description:    man.Description,
		Version:        version,
		ContentHash:    hash,
		BundleLocation: res.Location,
		Metadata:       md,
		// Keywords: the manifest has no top-level hugen.keywords (MissionBlock
		// carries mission-dispatch keywords, a different concept); leave the
		// catalog column null for the seed. Discovery keywords are an SK-later
		// nicety, not load-bearing for install.
		Keywords:   nil,
		TierCompat: man.Hugen.TierCompatibility,
	}); err != nil {
		return fmt.Errorf("upsert row: %w", err)
	}
	return nil
}

type seededSkillRow struct {
	Name        string
	Description string
	Version     string
	ContentHash string
	// BundleLocation is the byte-store address the BundleStore returned for
	// these bytes. It is BACKEND-specific (an FS path today; a git ref / S3
	// key tomorrow), so it is folded into `metadata.bundle_location` — NOT the
	// `bundle_path` column, which the schema reserves for an "on-disk bundle
	// directory" that a catalog row does not have. The hub itself resolves
	// bytes by (name, version); this is provenance for non-deterministic
	// backends + debugging.
	BundleLocation  string
	Metadata        map[string]any // sent as the $md JSON var (skills.metadata NOT NULL)
	Keywords        []string
	TierCompat      []string
	TaskEligible    bool
	HasInputsSchema bool
}

// evictForeignSharedRows deletes any SHARED catalog row for `name` not owned by
// the reserved seed principal ("system"). Reserved (bundled) names belong to
// system; this removes a row a third party published under the name before the
// seed claimed it (the publish-time reserved-name check only guards once the
// system row exists). Privileged (service principal).
func (a *HubApp) evictForeignSharedRows(ctx context.Context, name string) error {
	res, err := a.client.Query(ctx,
		`mutation($n: String!, $sys: String!) { hub { agent { db { delete_skills(
			filter: { shared: { eq: true }, name: { eq: $n }, _not: { agent_id: { eq: $sys } } }
		) { affected_rows } } } } }`,
		map[string]any{"n": name, "sys": marketplaceSeedAgentID},
	)
	if err != nil {
		return err
	}
	res.Close()
	return res.Err()
}

// upsertSkillRow writes a shared catalog row for (agentID, source, name):
// delete-then-insert keyed by the (agent_id, source, name) unique index so a
// changed bundle replaces cleanly. Privileged (a.client secret-key principal).
// Shared by the bundled-skill seeder (agentID="system") and the publish path
// (agentID=<publisher>).
func (a *HubApp) upsertSkillRow(ctx context.Context, agentID, source string, row seededSkillRow) error {
	del, err := a.client.Query(ctx,
		`mutation($a: String!, $s: String!, $n: String!) { hub { agent { db { delete_skills(
			filter: { agent_id: { eq: $a }, source: { eq: $s }, name: { eq: $n } }
		) { affected_rows } } } } }`,
		map[string]any{"a": agentID, "s": source, "n": row.Name},
	)
	if err != nil {
		return err
	}
	_ = del.Err()
	del.Close()

	// The byte-store address is backend-specific provenance → fold it into the
	// metadata bag rather than the on-disk-only `bundle_path` column.
	md := row.Metadata
	if md == nil {
		md = map[string]any{}
	}
	if row.BundleLocation != "" {
		md["bundle_location"] = row.BundleLocation
	}

	// Build the insert as a $data map so empty array columns can be OMITTED
	// (→ NULL) rather than sent as an untyped `ARRAY[]`, which the Postgres
	// agent-store rejects. All NOT NULL columns (incl. the DEFAULT ones the
	// GraphQL input still marks required) are always present. `bundle_path` is
	// intentionally left NULL for catalog rows (no on-disk directory).
	data := map[string]any{
		"id":                newSkillRowID(),
		"agent_id":          agentID,
		"shared":            true,
		"name":              row.Name,
		"type":              "skill",
		"description":       row.Description,
		"source":            source,
		"version":           row.Version,
		"content_hash":      row.ContentHash,
		"metadata":          md,
		"task_eligible":     row.TaskEligible,
		"has_inputs_schema": row.HasInputsSchema,
		"pin":               false,
	}
	if len(row.Keywords) > 0 {
		data["keywords"] = row.Keywords
	}
	if len(row.TierCompat) > 0 {
		data["tier_compat"] = row.TierCompat
	}
	ins, err := a.client.Query(ctx,
		`mutation($data: hub_agent_db_skills_mut_input_data!) {
			hub { agent { db { insert_skills(data: $data) { id } } } } }`,
		map[string]any{"data": data},
	)
	if err != nil {
		return err
	}
	defer ins.Close()
	return ins.Err()
}

// shortVersion derives a stable content-addressed version from the canonical
// hash (bundled skills carry no explicit version). A changed bundle yields a
// new version → a new byte-store path, so old and new bytes never alias.
func shortVersion(hash string) string {
	h := strings.TrimPrefix(hash, "sha256:")
	if len(h) >= 12 {
		return "h-" + h[:12]
	}
	return "h-" + h
}

// newSkillRowID mints a random skills.id (mirrors hugen's newSkillID shape).
func newSkillRowID() string {
	var b [9]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "skl-seedfallback"
	}
	return "skl-" + hex.EncodeToString(b[:])
}

// tarGzBundle writes fsys as a gzip-compressed tar, excluding dotfiles/dot-dirs
// so the archive covers exactly the files the canonical hash does.
func tarGzBundle(fsys fs.FS) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		if hasDotSegment(p) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		content, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		hdr := &tar.Header{
			Name:    p,
			Mode:    0o644,
			Size:    int64(len(content)),
			ModTime: info.ModTime(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(content); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// hasDotSegment mirrors the exclusion rule in skill.BundleHash (a path segment
// beginning with "."). Kept local so the tar and the hash agree on membership.
func hasDotSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}
