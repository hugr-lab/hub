package hubapp

// BundleStore is the marketplace byte-store abstraction (spec-skills-
// distribution REVISION 2026-07-13 B). Skill bundle bytes (a .tar.gz per
// name+version) live behind this interface so the backend can swap — v1 is a
// filesystem mount; future backends are S3 / S3-FUSE and git (where a publish
// opens a branch/PR for review-before-merge). The catalog metadata lives in
// the Agent DB; only the bytes live here.
//
// Two design invariants the interface must preserve for the future backends:
//   - Put takes the publisher Identity as a first-class argument — it must
//     reach the store's logic point (a git backend needs it as commit/PR
//     author), not only the skills provenance row.
//   - Put returns a Status: the FS backend is always Published (immediately
//     visible), but a git backend may return PendingReview. Callers must not
//     assume a published outcome.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// PublishStatus is the visibility outcome of a Put.
type PublishStatus string

const (
	// StatusPublished — the bundle is live and downloadable now (FS backend).
	StatusPublished PublishStatus = "published"
	// StatusPendingReview — the bundle awaits review before it becomes
	// downloadable (git backend: a branch/PR). Reserved; no v1 producer.
	StatusPendingReview PublishStatus = "pending_review"
)

// PublisherIdentity is the resolved caller threaded into Put. Populated from
// the verified bearer at the HTTP boundary (hugr auth.me), never trusted from
// the request body.
type PublisherIdentity struct {
	AgentID string // set when the caller is an agent
	UserID  string // set when the caller is an end user
	Role    string // the caller's hugr role
}

// PutResult is the outcome of storing a bundle.
type PutResult struct {
	Status   PublishStatus // published (FS) | pending_review (git)
	Location string        // backend-internal locator, persisted to skills.bundle_path
}

// ErrBundleNotFound is returned by Get/Exists when no bundle exists for the
// requested name+version.
var ErrBundleNotFound = errors.New("bundle not found")

// BundleStore stores and serves skill bundle bytes (.tar.gz per name+version).
type BundleStore interface {
	// Get opens the bundle tar.gz for (name, version); the caller closes it.
	// Returns ErrBundleNotFound if absent.
	Get(ctx context.Context, name, version string) (io.ReadCloser, error)
	// Put stores the tar.gz read from r under (name, version), stamping pub as
	// the publisher. It returns where the bytes landed and their visibility.
	Put(ctx context.Context, name, version string, r io.Reader, pub PublisherIdentity) (PutResult, error)
	// Exists reports whether a bundle for (name, version) is stored.
	Exists(ctx context.Context, name, version string) (bool, error)
}

// FSBundleStore is the v1 filesystem BundleStore: bytes at
// <root>/<name>/<version>.tar.gz on a hub-service mount. A Put is immediately
// visible (StatusPublished). It ignores the publisher Identity for placement
// (the provenance is recorded on the catalog row); the arg exists so the
// interface fits the git backend.
type FSBundleStore struct {
	root string
}

// NewFSBundleStore roots an FS byte-store at dir (created if absent).
func NewFSBundleStore(dir string) (*FSBundleStore, error) {
	if dir == "" {
		return nil, errors.New("bundlestore: empty root dir")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("bundlestore: mkdir root: %w", err)
	}
	return &FSBundleStore{root: dir}, nil
}

// path resolves the on-disk tar path for (name, version), rejecting any value
// that could escape the root (defense in depth on top of the handler-level
// kebab validation).
func (s *FSBundleStore) path(name, version string) (string, error) {
	if err := validBundleName(name); err != nil {
		return "", err
	}
	if err := validBundleVersion(version); err != nil {
		return "", err
	}
	return filepath.Join(s.root, name, version+".tar.gz"), nil
}

// Get opens the bundle tar.gz.
func (s *FSBundleStore) Get(_ context.Context, name, version string) (io.ReadCloser, error) {
	p, err := s.path(name, version)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrBundleNotFound
		}
		return nil, err
	}
	return f, nil
}

// Put writes the bundle tar.gz atomically (temp + rename).
func (s *FSBundleStore) Put(_ context.Context, name, version string, r io.Reader, _ PublisherIdentity) (PutResult, error) {
	p, err := s.path(name, version)
	if err != nil {
		return PutResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return PutResult{}, fmt.Errorf("bundlestore: mkdir bundle dir: %w", err)
	}
	tmp := p + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return PutResult{}, fmt.Errorf("bundlestore: create temp: %w", err)
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return PutResult{}, fmt.Errorf("bundlestore: write: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return PutResult{}, fmt.Errorf("bundlestore: close: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return PutResult{}, fmt.Errorf("bundlestore: rename: %w", err)
	}
	return PutResult{Status: StatusPublished, Location: filepath.Join(name, version+".tar.gz")}, nil
}

// Exists reports whether the bundle tar is present.
func (s *FSBundleStore) Exists(_ context.Context, name, version string) (bool, error) {
	p, err := s.path(name, version)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(p); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// validBundleVersion permits a conservative version token: letters, digits,
// dot, dash, underscore — enough for semver and hash-derived versions, and no
// path separators or "..".
func validBundleVersion(v string) error {
	if v == "" {
		return errors.New("bundlestore: empty version")
	}
	if v == "." || v == ".." || strings.ContainsAny(v, "/\\") {
		return fmt.Errorf("bundlestore: unsafe version %q", v)
	}
	for _, r := range v {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_'
		if !ok {
			return fmt.Errorf("bundlestore: invalid version char %q in %q", r, v)
		}
	}
	return nil
}
