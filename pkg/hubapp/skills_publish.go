package hubapp

// SK3 — the skills marketplace publish path (spec-skills-distribution §4/§6):
//
//	POST /skills/publish   (body = bundle tar.gz) → stores + shared row
//
// The trust boundary. A compromised agent process bypasses nothing: the
// publish gate lives in hugr (a positive `(role,"hugen:skill","publish")`
// permission row, read privileged under the caller's identity), and the hub
// stamps provenance from the verified token — never from the upload. v1
// hardening (owner 2026-07-09): reserved embed-seed names, first-publisher
// pin per name, publisher-must-hold-declared-caps. Bytes are re-tarred
// canonically (dotfile-excluding, matching the seeder) so any downloader's
// re-hash equals the catalog content_hash.

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/hugr-lab/hugen/pkg/skill"
	"github.com/hugr-lab/query-engine/types"
)

const (
	// skillPublishNamespace / skillPublishField name the hugr permission that
	// gates publishing: a positive `(role,"hugen:skill","publish")` row.
	skillPublishNamespace = "hugen:skill"
	skillPublishField     = "publish"
	// maxPublishBytes / maxPublishFiles cap an uploaded bundle (defense
	// against a decompression bomb / runaway upload).
	maxPublishBytes = 32 << 20 // 32 MiB
	maxPublishFiles = 4096
)

// publishResponse is the POST /skills/publish success envelope.
type publishResponse struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	ContentHash string `json:"content_hash"`
	Status      string `json:"status"`
}

// handleSkillsPublish serves POST /skills/publish.
func (a *HubApp) handleSkillsPublish(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.skillsAuthOrFail(w, r)
	if !ok {
		return
	}
	if a.bundleStore == nil {
		skillsError(w, http.StatusServiceUnavailable, "no_store", "byte-store unavailable")
		return
	}

	// Publish gate (§4): a positive hugr permission row on the caller's role.
	granted, err := a.callerCanPublish(r.Context(), caller)
	if err != nil {
		a.logger.Warn("skills publish gate", "caller", caller.ID, "error", err)
		skillsError(w, http.StatusForbidden, "forbidden", "publish permission check failed")
		return
	}
	if !granted {
		skillsError(w, http.StatusForbidden, "forbidden", "role lacks hugen:skill.publish")
		return
	}

	// Read the uploaded bundle (capped).
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPublishBytes))
	if err != nil {
		skillsError(w, http.StatusRequestEntityTooLarge, "too_large", "bundle exceeds size cap")
		return
	}

	// Extract to a temp dir safely, then validate + canonicalise from disk.
	tmp, err := os.MkdirTemp("", "skill-publish-")
	if err != nil {
		skillsError(w, http.StatusInternalServerError, "internal", "temp dir")
		return
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := safeExtractTarGz(strings.NewReader(string(body)), tmp, maxPublishBytes, maxPublishFiles); err != nil {
		a.logger.Info("skills publish extract", "caller", caller.ID, "error", err)
		skillsError(w, http.StatusBadRequest, "bad_bundle", "bundle could not be extracted safely")
		return
	}

	mdBytes, err := os.ReadFile(filepath.Join(tmp, "SKILL.md"))
	if err != nil {
		skillsError(w, http.StatusBadRequest, "no_manifest", "bundle has no SKILL.md")
		return
	}
	man, err := skill.Parse(mdBytes)
	if err != nil {
		skillsError(w, http.StatusBadRequest, "bad_manifest", "SKILL.md did not parse")
		return
	}
	name := man.Name
	if err := validBundleName(name); err != nil {
		skillsError(w, http.StatusBadRequest, "invalid_name", "skill name is not a valid kebab identifier")
		return
	}

	// Hardening §4: reserved embed-seed names + first-publisher pin.
	owners, err := a.sharedSkillOwners(r.Context(), name)
	if err != nil {
		a.logger.Warn("skills publish owners lookup", "skill", name, "error", err)
		skillsError(w, http.StatusBadGateway, "catalog_unavailable", "could not check the catalog")
		return
	}
	for _, o := range owners {
		if o.AgentID == marketplaceSeedAgentID {
			skillsError(w, http.StatusConflict, "reserved_name", "name is reserved by a bundled skill")
			return
		}
		if o.AgentID != caller.ID {
			skillsError(w, http.StatusConflict, "name_taken", "name is already published by another publisher")
			return
		}
	}

	// Hardening §4: the publisher's role must itself hold every capability the
	// manifest declares — blocks laundering a privileged cap into the public
	// set.
	if req := requiredCapsFromMetadata(man.Metadata); len(req) > 0 {
		holds, err := a.callerHasCaps(r.Context(), caller, req)
		if err != nil {
			a.logger.Warn("skills publish cap check", "skill", name, "error", err)
			skillsError(w, http.StatusForbidden, "forbidden", "capability check failed")
			return
		}
		if !holds {
			skillsError(w, http.StatusForbidden, "forbidden", "publisher role lacks a declared capability")
			return
		}
	}

	// Canonicalise: hash + re-tar from the validated on-disk tree so the
	// stored bytes match the catalog content_hash byte-for-byte.
	sub := os.DirFS(tmp)
	hash, err := skill.BundleHash(sub)
	if err != nil {
		skillsError(w, http.StatusInternalServerError, "internal", "hash bundle")
		return
	}
	tarball, err := tarGzBundle(sub)
	if err != nil {
		skillsError(w, http.StatusInternalServerError, "internal", "re-tar bundle")
		return
	}
	version := shortVersion(hash)

	res, err := a.bundleStore.Put(r.Context(), name, version, strings.NewReader(string(tarball)),
		PublisherIdentity{AgentID: caller.ID, UserID: caller.ID, Role: caller.Role})
	if err != nil {
		a.logger.Warn("skills publish store", "skill", name, "error", err)
		skillsError(w, http.StatusBadGateway, "store_failed", "could not store the bundle")
		return
	}

	md := man.Metadata
	if md == nil {
		md = map[string]any{}
	}
	if err := a.upsertSkillRow(r.Context(), caller.ID, marketplaceSeedSource, seededSkillRow{
		Name:            name,
		Description:     man.Description,
		Version:         version,
		ContentHash:     hash,
		BundleLocation:  res.Location,
		Metadata:        md,
		TierCompat:      man.Hugen.TierCompatibility,
		TaskEligible:    man.Hugen.Task.Eligible,
		HasInputsSchema: len(man.Hugen.Task.InputsSchema) > 0,
	}); err != nil {
		a.logger.Warn("skills publish row", "skill", name, "error", err)
		skillsError(w, http.StatusBadGateway, "catalog_write_failed", "could not write the catalog row")
		return
	}

	status := "published"
	if res.Status == StatusPendingReview {
		status = "pending_review"
	}
	a.logger.Info("skill published", "skill", name, "version", version, "publisher", caller.ID, "status", status)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(publishResponse{
		Name:        name,
		Version:     version,
		ContentHash: hash,
		Status:      status,
	})
}

// callerCanPublish reports whether the caller's role holds a positive
// `(role,"hugen:skill","publish")` permission — evaluated by hugr check_access
// under the caller's own identity (never hub-service's).
func (a *HubApp) callerCanPublish(ctx context.Context, caller skillsCaller) (bool, error) {
	q := withIdentity(ctx, callerUserInfo(caller))
	res, err := a.client.Query(q,
		`query($ns: String!, $f: String!) {
			function { core { auth { check_access(
				type_name: $ns, fields: $f
			) { field enabled } } } }
		}`,
		map[string]any{"ns": skillPublishNamespace, "f": skillPublishField},
	)
	if err != nil {
		return false, err
	}
	defer res.Close()
	if res.Err() != nil {
		return false, res.Err()
	}
	var entries []struct {
		Field   string `json:"field"`
		Enabled bool   `json:"enabled"`
	}
	if err := res.ScanData("function.core.auth.check_access", &entries); err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.Field == skillPublishField {
			return e.Enabled, nil
		}
	}
	return false, nil
}

// skillOwnerRow is the (agent_id, source) of a shared catalog row — enough for
// the reserved-name + first-publisher-pin checks.
type skillOwnerRow struct {
	AgentID string `json:"agent_id"`
	Source  string `json:"source"`
}

// sharedSkillOwners privileged-reads every shared row for a name, returning
// who published each. Empty result → nil (name is free).
func (a *HubApp) sharedSkillOwners(ctx context.Context, name string) ([]skillOwnerRow, error) {
	res, err := a.client.Query(ctx,
		`query($n: String!) { hub { agent { db { skills(
			filter: { shared: { eq: true }, name: { eq: $n } }
		) { agent_id source } } } } }`,
		map[string]any{"n": name},
	)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	if res.Err() != nil {
		return nil, res.Err()
	}
	var rows []skillOwnerRow
	if err := res.ScanData("hub.agent.db.skills", &rows); err != nil {
		if errors.Is(err, types.ErrNoData) || errors.Is(err, types.ErrWrongDataPath) {
			return nil, nil
		}
		return nil, err
	}
	return rows, nil
}

// safeExtractTarGz extracts a gzip-compressed tar into dst, rejecting any
// entry that would escape dst (absolute, "..", symlink/hardlink), with a
// cumulative byte cap and a file-count cap. Only regular files and
// directories are materialised. (Hub-local copy of the reconciler's extractor
// — the same security contract on the ingest side.)
func safeExtractTarGz(r io.Reader, dst string, maxBytes int64, maxFiles int) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)

	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("mkdir dst: %w", err)
	}
	root, err := filepath.Abs(dst)
	if err != nil {
		return err
	}

	var total int64
	var files int
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeDir {
			return fmt.Errorf("unsafe entry type %d in %q", hdr.Typeflag, hdr.Name)
		}
		clean := filepath.Clean(hdr.Name)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe path %q", hdr.Name)
		}
		target := filepath.Join(root, clean)
		if target != root && !strings.HasPrefix(target, root+string(filepath.Separator)) {
			return fmt.Errorf("path escapes bundle root: %q", hdr.Name)
		}
		// Count EVERY materialised entry — directories included — against the
		// cap. A gzip of repetitive tar directory headers otherwise amplifies
		// into millions of MkdirAll calls / inodes with neither the byte cap
		// (dir entries carry no content) nor a file cap tripping.
		files++
		if files > maxFiles {
			return fmt.Errorf("too many entries (> %d)", maxFiles)
		}
		if hdr.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("mkdir %q: %w", target, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("mkdir parent %q: %w", target, err)
		}
		remaining := maxBytes - total
		if remaining <= 0 {
			return fmt.Errorf("bundle exceeds size cap (%d bytes)", maxBytes)
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return fmt.Errorf("create %q: %w", target, err)
		}
		n, err := io.Copy(f, io.LimitReader(tr, remaining+1))
		_ = f.Close()
		if err != nil {
			return fmt.Errorf("write %q: %w", target, err)
		}
		total += n
		if total > maxBytes {
			return fmt.Errorf("bundle exceeds size cap (%d bytes)", maxBytes)
		}
	}
	return nil
}
