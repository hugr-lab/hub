package agent

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// SkillMeta is the lightweight metadata parsed from SKILL.md frontmatter.
// Loaded at agent startup for all available skills. Used for per-turn routing.
type SkillMeta struct {
	ID            string     `yaml:"-" json:"id"`             // derived from directory name
	Version       string     `yaml:"-" json:"version"`        // derived from directory name
	Name          string     `yaml:"name" json:"name"`
	Description   string     `yaml:"description" json:"description"`
	Context       string     `yaml:"context" json:"context"`             // "any", "local", "remote"
	MCPBackend    string     `yaml:"mcp_backend" json:"mcp_backend"`     // "http", "stdio", "builtin"
	MCPUrl        string     `yaml:"mcp_url" json:"mcp_url"`             // for http backend
	MCPExecutable string     `yaml:"mcp_executable" json:"mcp_executable"` // for stdio backend
	Tools         []ToolMeta `yaml:"tools" json:"tools"`
	RequiresPlan  bool       `yaml:"requires_plan" json:"requires_plan"`
}

// ToolMeta is a lightweight tool reference in skill metadata.
type ToolMeta struct {
	Name string `yaml:"name" json:"name"`
}

// SkillFull extends SkillMeta with the full prompt body loaded on demand.
type SkillFull struct {
	SkillMeta
	SystemPrompt string `json:"system_prompt"` // markdown body after frontmatter
}

// ParseSkillMeta parses YAML frontmatter from a SKILL.md file.
// Returns the metadata without loading the full body.
func ParseSkillMeta(path string) (SkillMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SkillMeta{}, fmt.Errorf("read skill: %w", err)
	}
	meta, _, err := splitFrontmatter(data)
	if err != nil {
		return SkillMeta{}, fmt.Errorf("parse skill %s: %w", path, err)
	}
	var sm SkillMeta
	if err := yaml.Unmarshal(meta, &sm); err != nil {
		return SkillMeta{}, fmt.Errorf("parse skill frontmatter %s: %w", path, err)
	}
	if sm.Context == "" {
		sm.Context = "any"
	}
	if sm.MCPBackend == "" {
		sm.MCPBackend = "http"
	}
	return sm, nil
}

// LoadSkillFull reads a SKILL.md file and returns both metadata and full body.
func LoadSkillFull(path string) (SkillFull, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SkillFull{}, fmt.Errorf("read skill: %w", err)
	}
	meta, body, err := splitFrontmatter(data)
	if err != nil {
		return SkillFull{}, fmt.Errorf("parse skill %s: %w", path, err)
	}
	var sm SkillMeta
	if err := yaml.Unmarshal(meta, &sm); err != nil {
		return SkillFull{}, fmt.Errorf("parse skill frontmatter %s: %w", path, err)
	}
	if sm.Context == "" {
		sm.Context = "any"
	}
	if sm.MCPBackend == "" {
		sm.MCPBackend = "http"
	}
	return SkillFull{
		SkillMeta:    sm,
		SystemPrompt: strings.TrimSpace(body),
	}, nil
}

// splitFrontmatter splits a SKILL.md into YAML frontmatter and markdown body.
// Frontmatter is delimited by --- lines.
func splitFrontmatter(data []byte) (frontmatter []byte, body string, err error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	inFrontmatter := false
	var fmBuf bytes.Buffer
	var bodyBuf bytes.Buffer
	pastFrontmatter := false

	for scanner.Scan() {
		line := scanner.Text()
		if !inFrontmatter && !pastFrontmatter && strings.TrimSpace(line) == "---" {
			inFrontmatter = true
			continue
		}
		if inFrontmatter && strings.TrimSpace(line) == "---" {
			inFrontmatter = false
			pastFrontmatter = true
			continue
		}
		if inFrontmatter {
			fmBuf.WriteString(line)
			fmBuf.WriteByte('\n')
		} else if pastFrontmatter {
			bodyBuf.WriteString(line)
			bodyBuf.WriteByte('\n')
		}
	}
	if fmBuf.Len() == 0 {
		return nil, "", fmt.Errorf("no YAML frontmatter found (expected --- delimiters)")
	}
	return fmBuf.Bytes(), bodyBuf.String(), nil
}
