package agent

import (
	"encoding/json"
	"os"
)

// AgentConfig defines the agent's runtime configuration.
type AgentConfig struct {
	MCPServers []MCPServerConfig `json:"mcp_servers"`
	MaxTurns   int               `json:"max_turns"`
}

// MCPServerConfig defines a local MCP server to start.
type MCPServerConfig struct {
	Name      string            `json:"name"`
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	AutoStart bool              `json:"auto_start"`
}

// LoadConfig reads agent config from a JSON file.
func LoadConfig(path string) (*AgentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultConfig(), nil
	}
	var cfg AgentConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.MaxTurns <= 0 {
		cfg.MaxTurns = 20
	}
	return &cfg, nil
}

// DefaultConfig returns the default agent configuration.
func DefaultConfig() *AgentConfig {
	return &AgentConfig{
		MCPServers: []MCPServerConfig{
			{Name: "sandbox", Command: "/tools/sandbox-mcp", AutoStart: true},
		},
		MaxTurns: 20,
	}
}
