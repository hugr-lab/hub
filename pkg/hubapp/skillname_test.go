package hubapp

import (
	"errors"
	"testing"
)

func TestValidBundleName(t *testing.T) {
	ok := []string{"hugr-data", "analyst", "report-builder", "a", "a1", "data-tables-rows-count", "x9-y9"}
	for _, n := range ok {
		if err := validBundleName(n); err != nil {
			t.Errorf("validBundleName(%q) = %v, want nil", n, err)
		}
	}
	bad := []string{
		"", "Analyst", "hugr_data", "-lead", "trail-", "double--dash",
		"has space", "path/sep", "..", "up.dir", "UPPER", "café",
	}
	for _, n := range bad {
		if err := validBundleName(n); !errors.Is(err, ErrInvalidSkillName) {
			t.Errorf("validBundleName(%q) = %v, want ErrInvalidSkillName", n, err)
		}
	}
}
