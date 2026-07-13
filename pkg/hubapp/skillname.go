package hubapp

// Skill-name validation for the marketplace surface. The hub is a separate Go
// module from hugen, so it carries its own copy of the kebab-case rule that
// hugen's skill:save / skill:validate enforce (hugen
// pkg/extension/skill.ErrInvalidSkillName). Every marketplace path param
// (catalog filter, bundle download, publish) runs through validBundleName —
// with NO grandfathering (spec-skills-distribution SK1): the marketplace only
// ever holds names authored under the current rule.

import (
	"errors"
	"fmt"
)

// ErrInvalidSkillName marks a name that is not lowercase kebab-case.
var ErrInvalidSkillName = errors.New("invalid skill name")

// validBundleName enforces lowercase kebab-case: one or more segments of
// [a-z0-9] joined by single dashes (e.g. "hugr-data", "report-builder").
// No leading/trailing/double dash, no uppercase, no underscore, no path
// separators — so a name is always a safe single path segment.
func validBundleName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty", ErrInvalidSkillName)
	}
	prevDash := true // treat position before the first char as "just saw a boundary"
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			prevDash = false
		case r == '-':
			if prevDash {
				return fmt.Errorf("%w: %q (dash at position %d not between two segment chars)", ErrInvalidSkillName, name, i)
			}
			prevDash = true
		default:
			return fmt.Errorf("%w: %q (illegal char %q)", ErrInvalidSkillName, name, r)
		}
	}
	if prevDash {
		return fmt.Errorf("%w: %q (trailing dash)", ErrInvalidSkillName, name)
	}
	return nil
}
