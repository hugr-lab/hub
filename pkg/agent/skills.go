package agent

import (
	"archive/zip"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// SkillsEngine loads and manages SKILL.md files from .skill archives.
type SkillsEngine struct {
	dir    string
	skills []Skill
	logger *slog.Logger
}

// Skill represents a loaded skill with its instructions and references.
type Skill struct {
	Name         string
	Description  string
	Instructions string
	References   map[string]string // filename → content
}

func NewSkillsEngine(dir string, logger *slog.Logger) *SkillsEngine {
	return &SkillsEngine{dir: dir, logger: logger}
}

// Load reads all .skill archives from the skills directory.
func (e *SkillsEngine) Load() {
	entries, err := os.ReadDir(e.dir)
	if err != nil {
		e.logger.Warn("skills directory not found", "dir", e.dir, "error", err)
		return
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".skill") {
			continue
		}
		path := filepath.Join(e.dir, entry.Name())
		skill, err := loadSkillArchive(path)
		if err != nil {
			e.logger.Warn("failed to load skill", "path", path, "error", err)
			continue
		}
		e.skills = append(e.skills, skill)
		e.logger.Info("skill loaded", "name", skill.Name, "references", len(skill.References))
	}

	e.logger.Info("skills loaded", "count", len(e.skills))
}

// SystemPrompt builds the system prompt from loaded skills.
func (e *SkillsEngine) SystemPrompt() string {
	if len(e.skills) == 0 {
		return "You are a data analysis assistant. Help users explore and query data."
	}

	var sb strings.Builder
	for _, s := range e.skills {
		sb.WriteString(s.Instructions)
		sb.WriteString("\n\n")
		for name, content := range s.References {
			sb.WriteString(fmt.Sprintf("## Reference: %s\n\n%s\n\n", name, content))
		}
	}
	return sb.String()
}

// loadSkillArchive reads a .skill ZIP archive containing SKILL.md and references/.
func loadSkillArchive(path string) (Skill, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return Skill{}, fmt.Errorf("open archive: %w", err)
	}
	defer r.Close()

	skill := Skill{
		Name:       strings.TrimSuffix(filepath.Base(path), ".skill"),
		References: make(map[string]string),
	}

	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			continue
		}
		data, err := readAll(rc)
		rc.Close()
		if err != nil {
			continue
		}

		name := filepath.Base(f.Name)
		if name == "SKILL.md" {
			skill.Instructions = string(data)
			// Extract description from first line after frontmatter
			lines := strings.Split(skill.Instructions, "\n")
			for _, l := range lines {
				l = strings.TrimSpace(l)
				if l != "" && l != "---" && !strings.HasPrefix(l, "name:") && !strings.HasPrefix(l, "description:") {
					if strings.HasPrefix(l, "description:") {
						skill.Description = strings.TrimPrefix(l, "description:")
						skill.Description = strings.TrimSpace(strings.Trim(skill.Description, "\""))
					}
					break
				}
				if strings.HasPrefix(l, "description:") {
					skill.Description = strings.TrimPrefix(l, "description:")
					skill.Description = strings.TrimSpace(strings.Trim(skill.Description, "\""))
				}
			}
		} else if strings.HasPrefix(f.Name, "references/") {
			skill.References[name] = string(data)
		}
	}

	if skill.Instructions == "" {
		return skill, fmt.Errorf("SKILL.md not found in archive")
	}

	return skill, nil
}

func readAll(rc interface{ Read([]byte) (int, error) }) ([]byte, error) {
	var buf []byte
	tmp := make([]byte, 4096)
	for {
		n, err := rc.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return buf, nil
}
