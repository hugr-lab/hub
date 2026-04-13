package agent

import (
	"sort"
	"strings"
)

// SkillRouter selects skills for a conversation turn based on the user's
// message content. It scores each available skill by keyword overlap with
// the skill's description and intent hints.
type SkillRouter struct {
	// AlwaysInclude is a set of skill IDs that are always selected
	// regardless of routing (e.g. "constitution").
	AlwaysInclude map[string]bool
	// MaxSkills is the maximum number of skills to select per turn
	// (excluding always-included ones). Default: 3.
	MaxSkills int
}

// NewSkillRouter creates a router with default settings.
func NewSkillRouter() *SkillRouter {
	return &SkillRouter{
		AlwaysInclude: map[string]bool{"constitution": true, "context": true},
		MaxSkills:     3,
	}
}

// Route selects the best-matching skills for the given user message.
// Returns skill IDs in relevance order (always-included first, then scored).
func (r *SkillRouter) Route(message string, available []SkillMeta) []string {
	maxSkills := r.MaxSkills
	if maxSkills <= 0 {
		maxSkills = 3
	}

	msgLower := strings.ToLower(message)
	words := strings.Fields(msgLower)

	type scored struct {
		id    string
		score int
	}
	var candidates []scored

	var alwaysIDs []string
	for _, skill := range available {
		if r.AlwaysInclude[skill.ID] {
			alwaysIDs = append(alwaysIDs, skill.ID)
			continue
		}
		score := scoreSkill(skill, msgLower, words)
		if score > 0 {
			candidates = append(candidates, scored{skill.ID, score})
		}
	}

	// Sort by score descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	// Build result: always-included first, then top-N scored
	result := make([]string, 0, len(alwaysIDs)+maxSkills)
	result = append(result, alwaysIDs...)
	for i, c := range candidates {
		if i >= maxSkills {
			break
		}
		result = append(result, c.id)
	}

	// If no skills matched, rely on always-included skills only (constitution,
	// context). Including ALL skills as fallback pollutes the system prompt and
	// causes wrong behavior for simple messages like greetings.

	return result
}

// scoreSkill scores a skill against the user message.
// Higher score = better match.
func scoreSkill(skill SkillMeta, msgLower string, words []string) int {
	score := 0

	// Check description keywords
	descLower := strings.ToLower(skill.Description)
	for _, w := range words {
		if len(w) < 3 {
			continue
		}
		if strings.Contains(descLower, w) {
			score += 2
		}
	}

	// Check intent hints (stronger signal)
	for _, hint := range extractHints(skill.Description) {
		if strings.Contains(msgLower, hint) {
			score += 5
		}
	}

	// Check tool names
	for _, t := range skill.Tools {
		nameParts := strings.Split(strings.ToLower(t.Name), "-")
		for _, part := range nameParts {
			if len(part) >= 3 && strings.Contains(msgLower, part) {
				score++
			}
		}
	}

	return score
}

// extractHints pulls trigger phrases from description text.
// Looks for quoted phrases and key terms.
func extractHints(description string) []string {
	lower := strings.ToLower(description)
	// Split on common delimiters to get meaningful phrases
	parts := strings.FieldsFunc(lower, func(r rune) bool {
		return r == ',' || r == '.' || r == ';' || r == '/' || r == '|'
	})
	var hints []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if len(p) >= 4 && len(p) <= 40 {
			hints = append(hints, p)
		}
	}
	return hints
}
