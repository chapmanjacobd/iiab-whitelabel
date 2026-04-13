package state_test

import (
	"testing"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
)

func TestSubdomainSanitization(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"small", "small"},
		{"Test-Demo", "test-demo"},
		{"PR_123", "pr123"},
		{"---test---", "test"},
		{"", "demo"},
		{"TEST@DEMO!", "testdemo"},
	}
	for _, tc := range tests {
		result := state.SanitizeSubdomain(tc.input)
		if result != tc.expected {
			t.Errorf("input: %q: expected %q, got %q", tc.input, tc.expected, result)
		}
	}
}

func TestSanitizeSubdomainIdempotency(t *testing.T) {
	inputs := []string{"Test-Demo", "PR_123", "---test---", "UPPERCASE"}
	for _, input := range inputs {
		first := state.SanitizeSubdomain(input)
		second := state.SanitizeSubdomain(first)
		if first != second {
			t.Errorf("sanitizeSubdomain not idempotent for %q: %q -> %q", input, first, second)
		}
	}
}
