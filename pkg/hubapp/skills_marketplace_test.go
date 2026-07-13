package hubapp

import (
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestRequiredCapsFromMetadata(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", ``, nil},
		{"null", `null`, nil},
		{"no hugen", `{"other":1}`, nil},
		{"no caps", `{"hugen":{"autoload":false}}`, nil},
		{"one cap", `{"hugen":{"required_capabilities":["pii"]}}`, []string{"pii"}},
		{"multi + blank pruned", `{"hugen":{"required_capabilities":["a"," ","b"]}}`, []string{"a", "b"}},
		{"garbage", `{not json`, nil},
	}
	for _, c := range cases {
		got := requiredCapsFromMetadata(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: requiredCapsFromMetadata(%q) = %v, want %v", c.name, c.in, got, c.want)
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
