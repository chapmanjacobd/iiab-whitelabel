package tests_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLogsCommand(t *testing.T) {
	requireRoot(t)
	stateDir := setupStateDir(t)
	name := uniqueDemoName("logs-demo")

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

	// 2. Manually write some text to build.log
	logFile := filepath.Join(stateDir, "active", name, "build.log")
	logContent := "Testing log output\nSome more lines\n"
	err = os.WriteFile(logFile, []byte(logContent), 0o644)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 3. Run logs command with --build
	stdout, stderr, err := runDemoctl(t, stateDir, "logs", name, "--build")
	if err != nil {
		t.Fatalf("logs failed: %s %s", stdout, stderr)
	}
	if !strings.Contains(stdout, "Testing log output") {
		t.Errorf("expected stdout to contain 'Testing log output', got: %s", stdout)
	}
}
