package tests_test

import (
	"strings"
	"testing"
)

func TestCrossStorageCopy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	stateDir := setupStateDir(t)
	ramDemo := "ram-base"
	diskDemo := "disk-derived"

	// Ensure clean slate for storage
	_, stderr, err := runDemoctl(t, stateDir, "cleanup", "--all")
	if err != nil {
		t.Fatalf("initial cleanup failed: %s", stderr)
	}

	// 1. Build in RAM (default) with skip-install
	t.Log("Building base demo in RAM...")
	stdout, stderr, err := runDemoctl(t, stateDir, "build", ramDemo, "--skip-install")
	if err != nil {
		t.Fatalf("RAM build failed: %s %s", stdout, stderr)
	}

	// Cleanup storage on exit (btrfs subvolumes are global, not under state-dir)
	defer runDemoctl(t, stateDir, "delete", diskDemo)
	defer runDemoctl(t, stateDir, "delete", ramDemo)

	// 2. Build on Disk using RAM demo as base
	t.Log("Building derived demo on DISK...")
	stdout, stderr, err = runDemoctl(
		t,
		stateDir,
		"build",
		diskDemo,
		// Test base debian
		"--base",
		ramDemo,
		"--disk",
		"--skip-install",
	)
	if err != nil {
		t.Fatalf("Disk build failed: %s %s", stdout, stderr)
	}

	// 3. Verify it was copied from alternate storage
	if !strings.Contains(stdout+stderr, "Copying subvolume from alternate storage") {
		t.Errorf("expected output to contain 'Copying subvolume from alternate storage', got: %s%s", stdout, stderr)
	}
	if strings.Contains(stdout+stderr, "Downloading and extracting Debian 13 genericcloud image") {
		t.Errorf(
			"expected output to NOT contain 'Downloading and extracting Debian 13 genericcloud image', got: %s%s",
			stdout,
			stderr,
		)
	}
}
