package hubapp

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/hugr-lab/hugen/pkg/store/schema"
)

//go:embed schema/init.sql
var hubDBSchema string

//go:embed schema/hub.graphql
var hubGraphQLSchema string

// hubGraphExtSchema is the HB-EXT cross-source relation graph (platform DB ↔
// Agent DB) registered as an `extension` data source. It declares no tables of
// its own — only cross-source @join fields on hub_db_* and hub_agent_db_* types.
//
//go:embed schema/graph.graphql
var hubGraphExtSchema string

// Schema version map. hub provisions TWO independent schemas into TWO SEPARATE
// physical databases (D11); each has its OWN _hugr_app_meta version stream,
// keyed by app_name "hub" but in its own DB, so the two versions never collide
// and are NOT comparable to each other:
//
//	platform DB  (hub.db)       → appVersion (below)  — hub owns this stream;
//	                                                     migrations in schema/migrations/
//	agent store  (hub.agent.db) → schema.Version      — hugen owns this stream;
//	                                                     migrations in hugen's pkg/store/schema
//
// appVersion is declared in app.go (it also stamps the app itself). The agent
// schema version is pulled from the imported hugen library — it changes when
// go.mod bumps hugen. agentSchemaVersion pins the value THIS hub release was
// built and reviewed against; a guard test asserts it equals schema.Version, so
// a transitive hugen bump can't silently migrate the agent store under a hub
// release — upgrading it is always a deliberate, reviewed bump here.
const agentSchemaVersion = "0.0.9"

// migrationsFS holds the platform-DB migration chain, mirroring hugen's layout:
// schema/migrations/<target-version>/<N-slug>.sql, where the directory name is
// the schema version the DB reaches after applying every file under it. Adding a
// migration is drop-in — create schema/migrations/<version>/<N-slug>.sql and
// bump appVersion; no Go change is needed.
//
//go:embed all:schema/migrations
var migrationsFS embed.FS

const migrationsRoot = "schema/migrations"

// migrationSQL returns the SQL to migrate the platform DB from fromVersion up to
// appVersion: every migration file whose version directory is newer than
// fromVersion and not newer than appVersion, concatenated in (version, filename)
// order. ok is false when nothing applies (fromVersion is already current/ahead).
// Version comparison reuses hugen's schema.CompareVersions so both migration
// streams order versions identically.
func migrationSQL(fromVersion string) (sql string, ok bool, err error) {
	type script struct {
		version, path, file string
	}
	var scripts []script
	walkErr := fs.WalkDir(migrationsFS, migrationsRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".sql") {
			return nil
		}
		rel := strings.TrimPrefix(p, migrationsRoot+"/")
		parts := strings.SplitN(rel, "/", 2)
		if len(parts) < 2 {
			return fmt.Errorf("migration %q must live under a <version>/ directory", p)
		}
		version := parts[0]
		// (fromVersion, appVersion] — newer than the DB, not past the target.
		if schema.CompareVersions(version, fromVersion) <= 0 || schema.CompareVersions(version, appVersion) > 0 {
			return nil
		}
		scripts = append(scripts, script{version, p, path.Base(p)})
		return nil
	})
	if walkErr != nil {
		return "", false, fmt.Errorf("walk migrations: %w", walkErr)
	}
	if len(scripts) == 0 {
		return "", false, nil
	}
	sort.Slice(scripts, func(i, j int) bool {
		if c := schema.CompareVersions(scripts[i].version, scripts[j].version); c != 0 {
			return c < 0
		}
		return scripts[i].file < scripts[j].file
	})
	var b strings.Builder
	for _, s := range scripts {
		data, rerr := migrationsFS.ReadFile(s.path)
		if rerr != nil {
			return "", false, fmt.Errorf("read migration %s: %w", s.path, rerr)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String(), true, nil
}
