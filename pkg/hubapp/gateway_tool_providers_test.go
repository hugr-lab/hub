package hubapp

import "testing"

func stdio(name string) map[string]any { return map[string]any{"name": name, "command": "./" + name} }
func httpP(name, ep string) map[string]any {
	return map[string]any{"name": name, "type": "mcp", "transport": "http", "endpoint": ep}
}

func names(list []any) []string {
	out := make([]string, 0, len(list))
	for _, p := range list {
		out = append(out, providerName(p))
	}
	return out
}

func TestEditProviderList(t *testing.T) {
	base := []any{stdio("bash-mcp"), stdio("hugr-query"), httpP("weather", "https://a/mcp")}
	newSpec := httpP("weather", "https://b/mcp")

	t.Run("add new preserves all", func(t *testing.T) {
		next, found := editProviderList(base, "search", httpP("search", "https://s/mcp"), false)
		if found {
			t.Error("found should be false for a new provider")
		}
		if got := names(next); len(got) != 4 || got[3] != "search" {
			t.Errorf("names = %v, want bash-mcp,hugr-query,weather,search", got)
		}
	})

	t.Run("upsert existing replaces, keeps stdio", func(t *testing.T) {
		next, found := editProviderList(base, "weather", newSpec, false)
		if !found {
			t.Error("found should be true when replacing")
		}
		if got := names(next); len(got) != 3 {
			t.Fatalf("names = %v, want 3", got)
		}
		// the replaced entry carries the new endpoint
		for _, p := range next {
			if providerName(p) == "weather" {
				if ep := p.(map[string]any)["endpoint"]; ep != "https://b/mcp" {
					t.Errorf("weather endpoint = %v, want https://b/mcp", ep)
				}
			}
		}
	})

	t.Run("delete removes only the named, keeps stdio", func(t *testing.T) {
		next, found := editProviderList(base, "weather", nil, true)
		if !found {
			t.Error("found should be true when deleting an existing provider")
		}
		if got := names(next); len(got) != 2 || got[0] != "bash-mcp" || got[1] != "hugr-query" {
			t.Errorf("names = %v, want bash-mcp,hugr-query", got)
		}
	})

	t.Run("delete missing → found=false", func(t *testing.T) {
		_, found := editProviderList(base, "nope", nil, true)
		if found {
			t.Error("found should be false when deleting an absent provider")
		}
	})
}

func TestIsHTTPProviderEntry(t *testing.T) {
	if !isHTTPProviderEntry(httpP("x", "https://h/mcp")) {
		t.Error("http entry should be an HTTP provider")
	}
	if isHTTPProviderEntry(stdio("bash-mcp")) {
		t.Error("stdio entry (no transport) should NOT be an HTTP provider")
	}
	if !isHTTPProviderEntry(map[string]any{"name": "x", "transport": "SSE"}) {
		t.Error("case-insensitive transport should be recognized")
	}
}

func TestIsHTTPTransportLabel(t *testing.T) {
	for _, ok := range []string{"http", "streamable-http", "sse"} {
		if !isHTTPTransportLabel(ok) {
			t.Errorf("%q should be a valid HTTP transport", ok)
		}
	}
	for _, bad := range []string{"stdio", "", "grpc"} {
		if isHTTPTransportLabel(bad) {
			t.Errorf("%q should not be a valid HTTP transport", bad)
		}
	}
}
