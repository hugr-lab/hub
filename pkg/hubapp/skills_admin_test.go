package hubapp

import (
	"reflect"
	"testing"
)

// The capability gate is deny-by-default (SK5): only a positive, non-disabled
// hugen:skill:capability row counts as a grant. These pin the pure projection +
// require-all logic that decides visibility/install/publish authorization, and
// the exact role_permissions keys the admin tooling writes.

func TestEnabledCapsFromRows(t *testing.T) {
	rows := []rolePermRow{
		{TypeName: capabilitySkillNamespace, FieldName: "pii", Disabled: false},              // grant
		{TypeName: capabilitySkillNamespace, FieldName: "finance", Disabled: true},           // explicit deny — NOT a grant
		{TypeName: "hugen:skill", FieldName: "publish", Disabled: false},                     // different namespace — ignored
		{TypeName: "data-object:query", FieldName: "hub_agent_db_sessions", Disabled: false}, // floor row — ignored
		{TypeName: capabilitySkillNamespace, FieldName: "hr", Disabled: false},               // grant
	}
	got := enabledCapsFromRows(rows)
	want := map[string]bool{"pii": true, "hr": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("enabledCapsFromRows = %v, want %v", got, want)
	}
	if got["finance"] {
		t.Error("a disabled capability row must not count as a grant (deny-by-default)")
	}
}

func TestEnabledCapsFromRows_Empty(t *testing.T) {
	// No rows at all → no caps (a role with no grants is denied every cap).
	if got := enabledCapsFromRows(nil); len(got) != 0 {
		t.Errorf("enabledCapsFromRows(nil) = %v, want empty", got)
	}
}

func TestHasAllCaps(t *testing.T) {
	granted := map[string]bool{"pii": true, "hr": true}
	cases := []struct {
		name     string
		required []string
		want     bool
	}{
		{"none required", nil, true},
		{"subset", []string{"pii"}, true},
		{"all", []string{"pii", "hr"}, true},
		{"one missing", []string{"pii", "finance"}, false},
		{"all missing", []string{"finance"}, false},
	}
	for _, c := range cases {
		if got := hasAllCaps(granted, c.required); got != c.want {
			t.Errorf("%s: hasAllCaps(%v, %v) = %v, want %v", c.name, granted, c.required, got, c.want)
		}
	}
	// An empty grant set denies any non-empty requirement.
	if hasAllCaps(map[string]bool{}, []string{"pii"}) {
		t.Error("empty grants must deny a required cap")
	}
}

func TestSkillPermKeys(t *testing.T) {
	if got := skillCapabilityKey("pii"); got != (permKey{"hugen:skill:capability", "pii"}) {
		t.Errorf("skillCapabilityKey = %v, want {hugen:skill:capability, pii}", got)
	}
	if got := skillPublishKey(); got != (permKey{"hugen:skill", "publish"}) {
		t.Errorf("skillPublishKey = %v, want {hugen:skill, publish}", got)
	}
}
