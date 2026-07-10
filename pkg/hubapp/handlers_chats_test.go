package hubapp

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/hugr-lab/query-engine/client/app"
)

// TestRegisterChatFunctions guards registration-time failures — arg collisions
// with hidden ArgFromContext names (`role`, `user_id`) and malformed struct
// types surface only when the app framework builds the schema.
func TestRegisterChatFunctions(t *testing.T) {
	a := &HubApp{mux: app.New(), logger: slog.Default()}
	if err := a.registerChatFunctions(); err != nil {
		t.Fatalf("registerChatFunctions: %v", err)
	}
}

func TestNewChatProjectIDs(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		c, p := newChatID(), newProjectID()
		if !strings.HasPrefix(c, "ch-") || len(c) != 3+18 {
			t.Fatalf("chat id %q: want ch-<18 hex>", c)
		}
		if !strings.HasPrefix(p, "prj-") || len(p) != 4+18 {
			t.Fatalf("project id %q: want prj-<18 hex>", p)
		}
		if seen[c] || seen[p] {
			t.Fatalf("id collision within 64 draws: %q/%q", c, p)
		}
		seen[c], seen[p] = true, true
	}
}

func TestChatJSONDerefsNilPointers(t *testing.T) {
	got := chatJSON(chatRow{ID: "ch-x", UserID: "u1", AgentID: "a1", Title: "t"})
	for _, k := range []string{"project_id", "root_session_id"} {
		if got[k] != "" {
			t.Fatalf("%s = %v, want empty string for nil column", k, got[k])
		}
	}
	if got["archived"] != false {
		t.Fatalf("archived = %v, want false", got["archived"])
	}
	p := "prj-1"
	got = chatJSON(chatRow{ID: "ch-x", ProjectID: &p})
	if got["project_id"] != "prj-1" {
		t.Fatalf("project_id = %v, want prj-1", got["project_id"])
	}
}

func TestLikeEscape(t *testing.T) {
	cases := map[string]string{
		`50%`:    `50\%`,
		`a_c`:    `a\_c`,
		`back\`: `back\\`,
		`plain`:  `plain`,
	}
	for in, want := range cases {
		if got := likeEscape(in); got != want {
			t.Errorf("likeEscape(%q) = %q, want %q", in, got, want)
		}
	}
}
