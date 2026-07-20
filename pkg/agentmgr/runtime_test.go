package agentmgr

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/docker/go-connections/nat"
)

func TestOrchestrationFromConfig(t *testing.T) {
	tests := []struct {
		name   string
		config string
		want   Orchestration
	}{
		{
			name:   "full block",
			config: `{"orchestration":{"image":"hugen:m3","memory_bytes":2147483648,"nano_cpus":2000000000,"pids_limit":512}}`,
			want:   Orchestration{Image: "hugen:m3", MemoryBytes: 2147483648, NanoCPUs: 2000000000, PidsLimit: 512},
		},
		{
			name:   "image only",
			config: `{"orchestration":{"image":"hugen:m3"}}`,
			want:   Orchestration{Image: "hugen:m3"},
		},
		{
			name:   "no orchestration key",
			config: `{"models":{"router":"x"}}`,
			want:   Orchestration{},
		},
		{
			name:   "env block",
			config: `{"orchestration":{"image":"hugen:m3","env":{"HUGEN_LOG_LEVEL":"debug","FOO":"bar"}}}`,
			want:   Orchestration{Image: "hugen:m3", Env: map[string]string{"HUGEN_LOG_LEVEL": "debug", "FOO": "bar"}},
		},
		{name: "empty", config: "", want: Orchestration{}},
		{name: "malformed", config: `{not json`, want: Orchestration{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := OrchestrationFromConfig(json.RawMessage(tt.config))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("OrchestrationFromConfig(%q) = %+v, want %+v", tt.config, got, tt.want)
			}
			// ImageFromConfig is the thin wrapper — must agree on the image.
			if img := ImageFromConfig(json.RawMessage(tt.config)); img != tt.want.Image {
				t.Fatalf("ImageFromConfig(%q) = %q, want %q", tt.config, img, tt.want.Image)
			}
		})
	}
}

func TestResourceLimits(t *testing.T) {
	rt := &DockerRuntime{cfg: RuntimeConfig{
		DefaultMemoryBytes: 100,
		DefaultNanoCPUs:    200,
		DefaultPidsLimit:   300,
	}}

	t.Run("agent values win over defaults", func(t *testing.T) {
		res := rt.resourceLimits(AgentIdentity{MemoryBytes: 1, NanoCPUs: 2, PidsLimit: 3})
		if res.Memory != 1 || res.NanoCPUs != 2 {
			t.Fatalf("mem/cpu = %d/%d, want 1/2", res.Memory, res.NanoCPUs)
		}
		if res.PidsLimit == nil || *res.PidsLimit != 3 {
			t.Fatalf("pids = %v, want 3", res.PidsLimit)
		}
	})

	t.Run("fall back to runtime defaults when agent zero", func(t *testing.T) {
		res := rt.resourceLimits(AgentIdentity{})
		if res.Memory != 100 || res.NanoCPUs != 200 {
			t.Fatalf("mem/cpu = %d/%d, want 100/200", res.Memory, res.NanoCPUs)
		}
		if res.PidsLimit == nil || *res.PidsLimit != 300 {
			t.Fatalf("pids = %v, want 300", res.PidsLimit)
		}
	})

	t.Run("unlimited when both zero leaves pids nil", func(t *testing.T) {
		bare := &DockerRuntime{cfg: RuntimeConfig{}}
		res := bare.resourceLimits(AgentIdentity{})
		if res.Memory != 0 || res.NanoCPUs != 0 {
			t.Fatalf("mem/cpu = %d/%d, want 0/0", res.Memory, res.NanoCPUs)
		}
		if res.PidsLimit != nil {
			t.Fatalf("pids = %v, want nil (unlimited)", *res.PidsLimit)
		}
	})
}

func TestFirstHostPort(t *testing.T) {
	port := nat.Port(agentAPIPort)

	t.Run("returns bound host port", func(t *testing.T) {
		ports := nat.PortMap{port: []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: "49153"}}}
		if got := firstHostPort(ports, port); got != "49153" {
			t.Fatalf("firstHostPort = %q, want 49153", got)
		}
	})

	t.Run("no binding yields empty", func(t *testing.T) {
		if got := firstHostPort(nat.PortMap{}, port); got != "" {
			t.Fatalf("firstHostPort = %q, want empty", got)
		}
	})

	t.Run("skips empty host port", func(t *testing.T) {
		ports := nat.PortMap{port: []nat.PortBinding{{HostPort: ""}, {HostPort: "5000"}}}
		if got := firstHostPort(ports, port); got != "5000" {
			t.Fatalf("firstHostPort = %q, want 5000", got)
		}
	})
}

func TestAPIBaseURL(t *testing.T) {
	rt := &DockerRuntime{states: map[string]*RuntimeState{
		"pub":  {AgentID: "pub", HostPort: "49153"},
		"net":  {AgentID: "net"},
		"down": {AgentID: "down", Status: "stopped"},
	}}

	t.Run("published host port wins (dev publish)", func(t *testing.T) {
		got, err := rt.APIBaseURL("pub")
		if err != nil {
			t.Fatalf("APIBaseURL(pub): %v", err)
		}
		if got != "http://127.0.0.1:49153" {
			t.Fatalf("APIBaseURL(pub) = %q, want http://127.0.0.1:49153", got)
		}
	})

	t.Run("falls back to container DNS on the agent network", func(t *testing.T) {
		got, err := rt.APIBaseURL("net")
		if err != nil {
			t.Fatalf("APIBaseURL(net): %v", err)
		}
		if got != "http://hub-agent-net:10200" {
			t.Fatalf("APIBaseURL(net) = %q, want http://hub-agent-net:10200", got)
		}
	})

	t.Run("stopped container still resolves (dial carries the signal)", func(t *testing.T) {
		if _, err := rt.APIBaseURL("down"); err != nil {
			t.Fatalf("APIBaseURL(down): %v", err)
		}
	})

	t.Run("unknown agent errors", func(t *testing.T) {
		if _, err := rt.APIBaseURL("ghost"); err == nil {
			t.Fatal("APIBaseURL(ghost) = nil error, want error")
		}
	})
}
