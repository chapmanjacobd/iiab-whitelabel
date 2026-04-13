package config_test

import (
	"strings"
	"testing"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/config"
)

func TestDemoNameValidation(t *testing.T) {
	validNames := []string{"small", "test-123", "PR_456", "a"}
	for _, name := range validNames {
		if err := config.ValidateName(name); err != nil {
			t.Errorf("name %q should be valid: %v", name, err)
		}
	}

	invalidNames := []string{"test demo", "test@demo", "test.demo", ""}
	for _, name := range invalidNames {
		if err := config.ValidateName(name); err == nil {
			t.Errorf("name %q should be invalid", name)
		}
	}

	// Length limit
	longName := ""
	var longNameSb125 strings.Builder
	for range 65 {
		longNameSb125.WriteString("a")
	}
	longName += longNameSb125.String()
	if err := config.ValidateName(longName); err == nil {
		t.Error("expected error for 65 char name")
	}
}
