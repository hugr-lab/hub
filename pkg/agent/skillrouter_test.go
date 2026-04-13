package agent

import (
	"testing"
)

func TestSkillRouter_AlwaysIncluded(t *testing.T) {
	r := NewSkillRouter()
	available := []SkillMeta{
		{ID: "constitution", Description: "base identity"},
		{ID: "context", Description: "context management"},
		{ID: "hugr-data", Description: "data query tables schema"},
	}

	result := r.Route("any message", available)

	// constitution and context must always be present
	has := make(map[string]bool)
	for _, id := range result {
		has[id] = true
	}
	if !has["constitution"] {
		t.Error("constitution not in result")
	}
	if !has["context"] {
		t.Error("context not in result")
	}
}

func TestSkillRouter_NoMatch_OnlyAlwaysIncluded(t *testing.T) {
	r := NewSkillRouter()
	available := []SkillMeta{
		{ID: "constitution", Description: "base identity"},
		{ID: "context", Description: "context management"},
		{ID: "hugr-data", Description: "query data tables schema"},
		{ID: "kernel", Description: "execute code python jupyter"},
	}

	// Russian message — no English keyword overlap
	result := r.Route("привет", available)

	// Should contain only always-included, NOT hugr-data or kernel
	for _, id := range result {
		if id == "hugr-data" || id == "kernel" {
			t.Errorf("non-matching skill %q should not be in result for 'привет'", id)
		}
	}
	if len(result) != 2 {
		t.Errorf("result length = %d, want 2 (only always-included)", len(result))
	}
}

func TestSkillRouter_KeywordScoring(t *testing.T) {
	r := NewSkillRouter()
	available := []SkillMeta{
		{ID: "constitution", Description: "base identity"},
		{ID: "context", Description: "context management"},
		{ID: "hugr-data", Description: "query data tables schema exploration"},
		{ID: "kernel", Description: "execute code python jupyter notebook"},
	}

	result := r.Route("show me the data tables", available)

	// hugr-data should be selected (matches "data", "tables")
	has := make(map[string]bool)
	for _, id := range result {
		has[id] = true
	}
	if !has["hugr-data"] {
		t.Error("hugr-data should be selected for 'show me the data tables'")
	}
}

func TestSkillRouter_MaxSkills(t *testing.T) {
	r := NewSkillRouter()
	r.MaxSkills = 1

	available := []SkillMeta{
		{ID: "constitution", Description: "base identity"},
		{ID: "skill-a", Description: "query data analysis"},
		{ID: "skill-b", Description: "query data tables"},
		{ID: "skill-c", Description: "query data schema"},
	}

	result := r.Route("query data", available)

	// Should have 1 always-included (constitution) + 1 scored (MaxSkills=1)
	scored := 0
	for _, id := range result {
		if !r.AlwaysInclude[id] {
			scored++
		}
	}
	if scored > 1 {
		t.Errorf("scored skills = %d, want <= 1 (MaxSkills=1)", scored)
	}
}

func TestSkillRouter_ToolNameMatching(t *testing.T) {
	r := NewSkillRouter()
	available := []SkillMeta{
		{ID: "constitution", Description: "base identity"},
		{ID: "context", Description: "context management"},
		{ID: "hugr-data", Description: "data platform", Tools: []ToolMeta{
			{Name: "discovery-search_modules"},
			{Name: "data-inline_graphql_result"},
		}},
	}

	// Message containing tool name part "discovery"
	result := r.Route("run discovery search", available)

	has := make(map[string]bool)
	for _, id := range result {
		has[id] = true
	}
	if !has["hugr-data"] {
		t.Error("hugr-data should be selected via tool name match 'discovery'")
	}
}
