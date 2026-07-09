package hubapp

import (
	"log/slog"
	"testing"

	"github.com/hugr-lab/query-engine/client/app"
)

// TestRegisterProvisioningMutations guards against registration-time failures
// (e.g. an input arg colliding with a hidden ArgFromContext name like `role`),
// which surface only when the app framework builds the schema — not at compile
// time. Registering against a fresh mux exercises exactly that path.
func TestRegisterProvisioningMutations(t *testing.T) {
	a := &HubApp{mux: app.New(), logger: slog.Default()}
	if err := a.registerProvisioningMutations(); err != nil {
		t.Fatalf("registerProvisioningMutations: %v", err)
	}
}

// shortIDFromAgentID derives the NOT-NULL short_id alias from a caller-supplied
// agent id. The DB-coupled provisioning handlers are exercised at the live M2
// gate; this pins the one bit of pure logic they depend on.
func TestShortIDFromAgentID(t *testing.T) {
	isHex := func(s string) bool {
		for _, c := range s {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				return false
			}
		}
		return true
	}

	cases := []struct {
		name string
		id   string
		want string // "" → non-deterministic (padded), assert length + prefix instead
		pfx  string
	}{
		{"first four alphanumerics", "data-agent-1", "data", ""},
		{"lower-cases", "MyAgent", "myag", ""},
		{"uuid keeps leading run", "c9370a30-1234-5678", "c937", ""},
		{"digits count", "12345", "1234", ""},
		{"skips punctuation between", "a.b.c.d.e", "abcd", ""},
		{"leading dashes then chars", "--ab", "", "ab"}, // pads: "ab" + hex
		{"all non-alnum → padded hex", "----", "", ""},
		{"empty → padded hex", "", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shortIDFromAgentID(tc.id)
			if len(got) != 4 {
				t.Fatalf("shortIDFromAgentID(%q) = %q, want length 4", tc.id, got)
			}
			switch {
			case tc.want != "":
				if got != tc.want {
					t.Errorf("shortIDFromAgentID(%q) = %q, want %q", tc.id, got, tc.want)
				}
			case tc.pfx != "":
				if got[:len(tc.pfx)] != tc.pfx {
					t.Errorf("shortIDFromAgentID(%q) = %q, want prefix %q", tc.id, got, tc.pfx)
				}
				if !isHex(got[len(tc.pfx):]) {
					t.Errorf("shortIDFromAgentID(%q) = %q, padding %q is not hex", tc.id, got, got[len(tc.pfx):])
				}
			default:
				// Fully padded — every char must be hex.
				if !isHex(got) {
					t.Errorf("shortIDFromAgentID(%q) = %q, want all-hex padding", tc.id, got)
				}
			}
		})
	}
}
