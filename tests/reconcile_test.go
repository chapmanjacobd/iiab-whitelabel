package tests_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/config"
)

func TestReconcileCommand(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatal("this test must be run as root (use sudo go test ./tests/...)")
	}
	stateDir := setupStateDir(t)
	name := "reconcile-demo"

	// 1. Initialize a demo with 15000 MB size
	_, _, err := runDemoctl(t, stateDir, "build", name, "--skip-install", "--size", "15000")
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

	// 2. Clear resource usage file or set to incorrect values
	resourceFile := filepath.Join(stateDir, "resources")
	err = os.WriteFile(resourceFile, []byte("disk_total_mb = 100000\ndisk_allocated_mb = 0\n"), 0o644)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 3. Run reconcile
	stdout, stderr, err := runDemoctl(t, stateDir, "reconcile")
	if err != nil {
		t.Fatalf("reconcile failed: %s %s", stdout, stderr)
	}
	if !strings.Contains(stdout, "Reconciled resource counters") {
		t.Errorf("expected stdout to contain 'Reconciled resource counters', got: %s", stdout)
	}

	// 4. Verify resource file is updated (disk_allocated_mb = actual used space)
	resources, err := config.ReadResources(stateDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resources.DiskAllocatedMB < 500 {
		t.Errorf("expected disk_allocated_mb >= 500, got: %d", resources.DiskAllocatedMB)
	}
	if resources.DiskAllocatedMB > 1024 {
		t.Errorf("expected disk_allocated_mb <= 1024, got: %d", resources.DiskAllocatedMB)
	}
}
