package hubapp

import (
	"strings"
	"testing"

	"github.com/hugr-lab/hugen/pkg/store/schema"
)

// TestAgentSchemaVersion_MatchesImportedHugen is the version-map guard: hub
// declares the agent-store schema version it provisions (agentSchemaVersion),
// and it MUST equal the imported hugen schema.Version. If a go.mod bump drifts
// hugen's schema, this fails — forcing a deliberate, reviewed bump here rather
// than silently migrating every agent store under an unchanged hub release.
func TestAgentSchemaVersion_MatchesImportedHugen(t *testing.T) {
	if agentSchemaVersion != schema.Version {
		t.Fatalf("agent schema drift: hub declares agentSchemaVersion=%q but imported hugen schema.Version=%q.\n"+
			"A hugen bump changed the agent-store schema. Review its migration and set agentSchemaVersion=%q.",
			agentSchemaVersion, schema.Version, schema.Version)
	}
}

func TestMigrationSQL_Chain(t *testing.T) {
	// From the last shipped version, only the newest step applies and it lands
	// the HB6 platform tables.
	sql, ok, err := migrationSQL("0.3.1")
	if err != nil || !ok {
		t.Fatalf("migrationSQL(0.3.1): ok=%v err=%v", ok, err)
	}
	for _, want := range []string{"CREATE TABLE IF NOT EXISTS projects", "CREATE TABLE IF NOT EXISTS chats"} {
		if !strings.Contains(sql, want) {
			t.Errorf("0.3.1 migration missing %q", want)
		}
	}

	// From the oldest version the whole chain applies, in ascending order —
	// the earliest step's body precedes the HB6 tables.
	full, ok, err := migrationSQL("0.1.0")
	if err != nil || !ok {
		t.Fatalf("migrationSQL(0.1.0): ok=%v err=%v", ok, err)
	}
	if i := strings.Index(full, "CREATE TABLE IF NOT EXISTS chats"); i < 0 {
		t.Error("full chain must include the HB6 chats table")
	}

	// At the current target there is nothing left to apply.
	if _, ok, err := migrationSQL(appVersion); err != nil || ok {
		t.Errorf("migrationSQL(appVersion) should be a no-op: ok=%v err=%v", ok, err)
	}

	// A version past the target also yields nothing (no accidental downgrade SQL).
	if _, ok, err := migrationSQL("9.9.9"); err != nil || ok {
		t.Errorf("migrationSQL(future) should be a no-op: ok=%v err=%v", ok, err)
	}
}
