package hubapp

import (
	"encoding/json"
	"strings"
	"testing"
)

// The seeded placeholder config must be valid JSON and carry the one hub-only
// key the spawn requires (orchestration.image); models.model is intentionally
// left empty so the console flags it, and the insert must stay idempotent.
func TestDefaultAgentTypeConfig_ValidPlaceholder(t *testing.T) {
	var c struct {
		Orchestration struct {
			Image string `json:"image"`
		} `json:"orchestration"`
		Models struct {
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(defaultAgentTypeConfig), &c); err != nil {
		t.Fatalf("seed config is not valid JSON: %v", err)
	}
	if c.Orchestration.Image == "" {
		t.Fatal("seed config missing orchestration.image")
	}
	if c.Models.Model != "" {
		t.Fatalf("placeholder should leave models.model empty, got %q", c.Models.Model)
	}
	if !strings.Contains(defaultAgentTypeSeedSQL, defaultAgentTypeConfig) {
		t.Fatal("seed SQL must embed defaultAgentTypeConfig")
	}
	if !strings.Contains(defaultAgentTypeSeedSQL, "ON CONFLICT (id) DO NOTHING") {
		t.Fatal("seed SQL must be idempotent")
	}
}
