package hubapp

import (
	"log/slog"
	"testing"
)

func TestAgentTokenURL(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "explicit wins",
			cfg:  Config{AgentTokenURL: "http://custom:1234/agent/token", InternalURL: "http://hub-service:8082"},
			want: "http://custom:1234/agent/token",
		},
		{
			name: "default from InternalURL, no dedicated listener",
			cfg:  Config{InternalURL: "http://hub-service:8082"},
			want: "http://hub-service:8082/agent/token",
		},
		{
			name: "trailing slash trimmed",
			cfg:  Config{InternalURL: "http://hub-service:8082/"},
			want: "http://hub-service:8082/agent/token",
		},
		{
			name: "dedicated listener swaps the port, keeps InternalURL host",
			cfg:  Config{InternalURL: "http://hub-service:8082", AgentTokenListen: ":9090"},
			want: "http://hub-service:9090/agent/token",
		},
		{
			name: "dedicated listener with host:port form",
			cfg:  Config{InternalURL: "http://hub-service:8082", AgentTokenListen: "0.0.0.0:9091"},
			want: "http://hub-service:9091/agent/token",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &HubApp{config: tt.cfg, logger: slog.Default()}
			if got := a.agentTokenURL(); got != tt.want {
				t.Fatalf("agentTokenURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEnvBool(t *testing.T) {
	truthy := []string{"1", "t", "true", "TRUE", "Yes", "on", " on "}
	for _, v := range truthy {
		t.Setenv("HUB_TEST_BOOL", v)
		if !envBool("HUB_TEST_BOOL", false) {
			t.Errorf("envBool(%q) = false, want true", v)
		}
	}
	falsy := []string{"0", "false", "no", "off", "garbage"}
	for _, v := range falsy {
		t.Setenv("HUB_TEST_BOOL", v)
		if envBool("HUB_TEST_BOOL", true) {
			t.Errorf("envBool(%q) = true, want false", v)
		}
	}
	// Unset → default.
	if !envBool("HUB_TEST_BOOL_UNSET", true) {
		t.Error("envBool(unset) = false, want default true")
	}
	if envBool("HUB_TEST_BOOL_UNSET", false) {
		t.Error("envBool(unset) = true, want default false")
	}
}

func TestEnvInt64(t *testing.T) {
	cases := []struct {
		val  string
		def  int64
		want int64
	}{
		{"2147483648", 0, 2147483648}, // >2^31, must not truncate
		{"0", 5, 0},                   // explicit zero is valid
		{"-5", 7, 7},                  // negative → default
		{"abc", 9, 9},                 // unparseable → default
	}
	for _, c := range cases {
		t.Setenv("HUB_TEST_INT64", c.val)
		if got := envInt64("HUB_TEST_INT64", c.def); got != c.want {
			t.Errorf("envInt64(%q, %d) = %d, want %d", c.val, c.def, got, c.want)
		}
	}
	if got := envInt64("HUB_TEST_INT64_UNSET", 42); got != 42 {
		t.Errorf("envInt64(unset) = %d, want 42", got)
	}
}
