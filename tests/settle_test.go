package tests_test

import (
	"strings"
	"testing"
)

func TestSettleCommand(t *testing.T) {
	stateDir := setupStateDir(t)

	// 1. No demos
	stdout, _, err := runDemoctl(t, stateDir, "settle", "--timeout", "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "No demos to settle") {
		t.Errorf("expected stdout to contain 'No demos to settle', got: %s", stdout)
	}

	// 2. Queue a demo
	_, _, err = runDemoctl(t, stateDir, "build", "settle-demo", "--skip-install")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Cleanup storage on exit (btrfs subvolumes are global, not under state-dir)
	defer runDemoctl(t, stateDir, "delete", "settle-demo")

	// 3. Settle
	stdout, stderr, err := runDemoctl(t, stateDir, "settle", "--timeout", "10")
	if err != nil {
		t.Fatalf("settle failed: %s %s", stdout, stderr)
	}
	if !strings.Contains(stdout, "All demos settled") {
		t.Errorf("expected stdout to contain 'All demos settled', got: %s", stdout)
	}
}
