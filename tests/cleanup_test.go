package tests_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCleanupCommand(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatal("this test must be run as root (use sudo go test ./tests/...)")
	}
	stateDir := setupStateDir(t)
	name := "failed-demo"

	// 1. Initialize a demo
	_, _, err := runDemoctl(t, stateDir, "build", name, "--skip-install")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Wait for background build to finish
	err = waitDemoSettled(t, stateDir, name, 120*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Cleanup storage on exit (btrfs subvolumes are global, not under state-dir)
	defer runDemoctl(t, stateDir, "delete", name)

	// 2. Manually set status to failed
	statusFile := filepath.Join(stateDir, "active", name, "status")
	err = os.WriteFile(statusFile, []byte("failed"), 0o644)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 3. Verify it exists in list
	stdout, _, err := runDemoctl(t, stateDir, "list")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, name) {
		t.Errorf("expected list to contain %q, got: %s", name, stdout)
	}

	// 4. Run cleanup
	stdout, stderr, err := runDemoctl(t, stateDir, "cleanup")
	if err != nil {
		t.Fatalf("cleanup failed: %s %s", stdout, stderr)
	}
	if !strings.Contains(stdout+stderr, "Cleaned up failed build") {
		t.Errorf("expected output to contain 'Cleaned up failed build', got: %s%s", stdout, stderr)
	}

	// 5. Verify it's gone
	stdout, _, err = runDemoctl(t, stateDir, "list")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(stdout, name) {
		t.Errorf("expected list to NOT contain %q, got: %s", name, stdout)
	}

	demoDir := filepath.Join(stateDir, "active", name)
	if _, err := os.Stat(demoDir); !os.IsNotExist(err) {
		t.Errorf("expected demo dir %s to not exist", demoDir)
	}
}
