package tests_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCommand(t *testing.T) {
	stateDir := setupStateDir(t)

	// Run init
	stdout, stderr, err := runDemoctl(t, stateDir, "init")
	if err != nil {
		t.Fatalf("init failed: %s %s", stdout, stderr)
	}
	if !strings.Contains(stdout+stderr, "Setting up IIAB Demo Server Host") {
		t.Errorf("expected output to contain 'Setting up IIAB Demo Server Host', got: %s%s", stdout, stderr)
	}

	// Verify state dirs were created
	if _, err := os.Stat(stateDir); os.IsNotExist(err) {
		t.Errorf("expected state dir %s to exist", stateDir)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "active")); os.IsNotExist(err) {
		t.Errorf("expected active dir %s to exist", filepath.Join(stateDir, "active"))
	}
}
