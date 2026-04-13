package agent

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

const testSkillContent = `---
name: test-skill
description: A test skill for data analysis
context: any
mcp_backend: http
tools:
  - name: test-tool
---

# Test Skill

You are a test assistant.
`

const testSkillV2Content = `---
name: test-skill
description: Updated test skill v2
context: local
mcp_backend: stdio
mcp_executable: test-mcp
tools:
  - name: tool-a
  - name: tool-b
---

# Test Skill v2

You are an updated test assistant.
`

func writeSkill(t *testing.T, dir, skillID, version, content string) {
	t.Helper()
	skillDir := filepath.Join(dir, skillID, version)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSkillCatalog_Load(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "test-skill", "1.0", testSkillContent)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	catalog := NewSkillCatalog(dir, logger)
	catalog.Load()

	all := catalog.All()
	if len(all) != 1 {
		t.Fatalf("catalog.All() length = %d, want 1", len(all))
	}

	skill := all[0]
	if skill.ID != "test-skill" {
		t.Errorf("ID = %q, want %q", skill.ID, "test-skill")
	}
	if skill.Name != "test-skill" {
		t.Errorf("Name = %q, want %q", skill.Name, "test-skill")
	}
	if skill.Context != "any" {
		t.Errorf("Context = %q, want %q", skill.Context, "any")
	}
	if len(skill.Tools) != 1 {
		t.Fatalf("Tools length = %d, want 1", len(skill.Tools))
	}
	if skill.Tools[0].Name != "test-tool" {
		t.Errorf("Tools[0].Name = %q, want %q", skill.Tools[0].Name, "test-tool")
	}
}

func TestSkillCatalog_VersionSelection(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "test-skill", "1.0", testSkillContent)
	writeSkill(t, dir, "test-skill", "2.0", testSkillV2Content)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	catalog := NewSkillCatalog(dir, logger)
	catalog.Load()

	all := catalog.All()
	if len(all) != 1 {
		t.Fatalf("catalog.All() length = %d, want 1 (latest version only)", len(all))
	}

	skill := all[0]
	if skill.Version != "2.0" {
		t.Errorf("Version = %q, want %q (latest)", skill.Version, "2.0")
	}
	if skill.Description != "Updated test skill v2" {
		t.Errorf("Description = %q, want v2 description", skill.Description)
	}
	if len(skill.Tools) != 2 {
		t.Errorf("Tools length = %d, want 2 (from v2)", len(skill.Tools))
	}
}

func TestSkillCatalog_LoadFull(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "test-skill", "1.0", testSkillContent)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	catalog := NewSkillCatalog(dir, logger)
	catalog.Load()

	full, err := catalog.LoadFull("test-skill")
	if err != nil {
		t.Fatalf("LoadFull error: %v", err)
	}

	if full.SystemPrompt == "" {
		t.Error("SystemPrompt is empty")
	}
	if full.SystemPrompt != "# Test Skill\n\nYou are a test assistant." {
		t.Errorf("SystemPrompt = %q", full.SystemPrompt)
	}
}

func TestSkillCatalog_Filter(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "skill-any", "1.0", `---
name: skill-any
description: available everywhere
context: any
tools: []
---
Prompt.
`)
	writeSkill(t, dir, "skill-local", "1.0", `---
name: skill-local
description: workspace only
context: local
tools: []
---
Prompt.
`)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	catalog := NewSkillCatalog(dir, logger)
	catalog.Load()

	// Filter by context "local" — should include "any" and "local"
	filtered := catalog.Filter("local", nil)
	if len(filtered) != 2 {
		t.Errorf("Filter(local, nil) length = %d, want 2", len(filtered))
	}

	// Filter by context "remote" — should include only "any"
	filtered = catalog.Filter("remote", nil)
	if len(filtered) != 1 {
		t.Fatalf("Filter(remote, nil) length = %d, want 1", len(filtered))
	}
	if filtered[0].ID != "skill-any" {
		t.Errorf("filtered[0].ID = %q, want %q", filtered[0].ID, "skill-any")
	}

	// Filter by allowed IDs (context="local" so skill-local matches)
	filtered = catalog.Filter("local", []string{"skill-local"})
	if len(filtered) != 1 {
		t.Fatalf("Filter(local, [skill-local]) length = %d, want 1", len(filtered))
	}
	if filtered[0].ID != "skill-local" {
		t.Errorf("filtered[0].ID = %q, want %q", filtered[0].ID, "skill-local")
	}
}

func TestParseFrontmatter(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr bool
		wantCtx string
	}{
		{
			name:    "valid",
			content: "---\nname: test\ndescription: desc\ncontext: local\ntools: []\n---\nBody.",
			wantCtx: "local",
		},
		{
			name:    "defaults",
			content: "---\nname: test\ndescription: desc\n---\nBody.",
			wantCtx: "any", // default
		},
		{
			name:    "no frontmatter",
			content: "Just a body.",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "SKILL.md")
			os.WriteFile(path, []byte(tt.content), 0o644)

			sm, err := ParseSkillMeta(path)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sm.Context != tt.wantCtx {
				t.Errorf("Context = %q, want %q", sm.Context, tt.wantCtx)
			}
		})
	}
}
