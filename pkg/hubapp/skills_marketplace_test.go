package hubapp

import (
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestRequiredCapsFromMetadata(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want []string
	}{
		{"nil", nil, nil},
		{"empty", map[string]any{}, nil},
		{"no hugen", map[string]any{"other": 1}, nil},
		{"hugen not a map", map[string]any{"hugen": "x"}, nil},
		{"no caps", map[string]any{"hugen": map[string]any{"autoload": false}}, nil},
		{"caps not a list", map[string]any{"hugen": map[string]any{"required_capabilities": "pii"}}, nil},
		{"one cap", map[string]any{"hugen": map[string]any{"required_capabilities": []any{"pii"}}}, []string{"pii"}},
		{"multi + blank pruned", map[string]any{"hugen": map[string]any{"required_capabilities": []any{"a", " ", "b"}}}, []string{"a", "b"}},
		{"non-string entries skipped", map[string]any{"hugen": map[string]any{"required_capabilities": []any{"a", 1, "b"}}}, []string{"a", "b"}},
	}
	for _, c := range cases {
		got := requiredCapsFromMetadata(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: requiredCapsFromMetadata(%#v) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}

func TestShortVersion(t *testing.T) {
	if got := shortVersion("sha256:0123456789abcdef00"); got != "h-0123456789ab" {
		t.Errorf("shortVersion = %q, want h-0123456789ab", got)
	}
	// A different hash yields a different version (so a changed bundle relocates).
	if shortVersion("sha256:aaaaaaaaaaaa") == shortVersion("sha256:bbbbbbbbbbbb") {
		t.Error("distinct hashes collapsed to the same version")
	}
}

func TestExtractBearerHeader(t *testing.T) {
	cases := map[string]string{
		"Bearer abc":  "abc",
		"bearer xyz":  "xyz",
		"BEARER k":    "k",
		"Basic zzz":   "",
		"":            "",
		"Bearer  p ":  "p",
	}
	for h, want := range cases {
		r := httptest.NewRequest("GET", "/skills/catalog", nil)
		if h != "" {
			r.Header.Set("Authorization", h)
		}
		if got := extractBearerHeader(r); got != want {
			t.Errorf("extractBearerHeader(%q) = %q, want %q", h, got, want)
		}
	}
}
