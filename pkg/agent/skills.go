package agent

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SkillCatalog scans a directory tree for SKILL.md files and provides
// lightweight SkillMeta at startup with on-demand SkillFull loading.
//
// Directory layout:
//
//	{root}/
//	  {skill_id}/
//	    {version}/
//	      SKILL.md
//
// The catalog picks the latest version directory per skill (lexicographic).
type SkillCatalog struct {
	root   string
	skills map[string]SkillMeta // id → SkillMeta (latest version)
	paths  map[string]string    // id → absolute path to SKILL.md
	logger *slog.Logger
}

func NewSkillCatalog(root string, logger *slog.Logger) *SkillCatalog {
	return &SkillCatalog{
		root:   root,
		skills: make(map[string]SkillMeta),
		paths:  make(map[string]string),
		logger: logger,
	}
}

// Load scans the catalog directory and parses all SKILL.md frontmatters.
func (c *SkillCatalog) Load() {
	entries, err := os.ReadDir(c.root)
	if err != nil {
		c.logger.Warn("skill catalog directory not found", "root", c.root, "error", err)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillID := entry.Name()
		versDir := filepath.Join(c.root, skillID)
		versions, err := os.ReadDir(versDir)
		if err != nil {
			continue
		}

		// Pick latest version (lexicographic sort, last wins)
		var versionNames []string
		for _, v := range versions {
			if v.IsDir() {
				versionNames = append(versionNames, v.Name())
			}
		}
		if len(versionNames) == 0 {
			continue
		}
		sort.Strings(versionNames)
		latestVersion := versionNames[len(versionNames)-1]

		skillPath := filepath.Join(versDir, latestVersion, "SKILL.md")
		meta, err := ParseSkillMeta(skillPath)
		if err != nil {
			c.logger.Warn("failed to parse skill", "path", skillPath, "error", err)
			continue
		}
		meta.ID = skillID
		meta.Version = latestVersion
		c.skills[skillID] = meta
		c.paths[skillID] = skillPath
		c.logger.Info("skill loaded", "id", skillID, "version", latestVersion, "tools", len(meta.Tools))
	}
	c.logger.Info("skill catalog loaded", "count", len(c.skills))
}

// All returns all loaded skill metadata.
func (c *SkillCatalog) All() []SkillMeta {
	result := make([]SkillMeta, 0, len(c.skills))
	for _, m := range c.skills {
		result = append(result, m)
	}
	return result
}

// Get returns a single skill's metadata by ID.
func (c *SkillCatalog) Get(id string) (SkillMeta, bool) {
	m, ok := c.skills[id]
	return m, ok
}

// LoadFull loads the full skill content for on-demand injection into a turn.
func (c *SkillCatalog) LoadFull(id string) (SkillFull, error) {
	path, ok := c.paths[id]
	if !ok {
		return SkillFull{}, fmt.Errorf("skill %q not found in catalog", id)
	}
	full, err := LoadSkillFull(path)
	if err != nil {
		return SkillFull{}, err
	}
	full.ID = id
	full.Version = c.skills[id].Version
	return full, nil
}

// SystemPrompt builds a combined system prompt from all loaded skills.
// This is a backward-compatible method — the skill router (Phase 3) will
// replace this with per-turn prompt assembly.
func (c *SkillCatalog) SystemPrompt() string {
	if len(c.skills) == 0 {
		return "You are a data analysis assistant. Help users explore and query data."
	}
	var parts []string
	for id := range c.skills {
		full, err := c.LoadFull(id)
		if err != nil {
			continue
		}
		if full.SystemPrompt != "" {
			parts = append(parts, full.SystemPrompt)
		}
	}
	if len(parts) == 0 {
		return "You are a data analysis assistant. Help users explore and query data."
	}
	return strings.Join(parts, "\n\n")
}

// Filter returns only skills matching the given context and allowed IDs.
// If allowedIDs is empty, all skills are returned (no filter).
func (c *SkillCatalog) Filter(context string, allowedIDs []string) []SkillMeta {
	allowed := make(map[string]bool)
	for _, id := range allowedIDs {
		allowed[id] = true
	}

	var result []SkillMeta
	for _, m := range c.skills {
		// Context filter: "any" matches everything, otherwise must match
		if m.Context != "any" && m.Context != context {
			continue
		}
		// Allowed filter: empty = all allowed
		if len(allowed) > 0 && !allowed[m.ID] {
			continue
		}
		result = append(result, m)
	}
	return result
}
