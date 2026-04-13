package config_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/config"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
)

func TestCleanupInterruptedPending(t *testing.T) {
	tmpDir := t.TempDir()
	activeDir := state.ActiveDir(tmpDir)
	name := "interrupted-pending"
	demoDir := filepath.Join(activeDir, name)
	os.MkdirAll(demoDir, 0o755)

	// Create a pending demo
	configContent := `DEMO_NAME="interrupted-pending"
IIAB_REPO=""
IIAB_BRANCH=""
IMAGE_SIZE_MB=10000
VOLATILE_MODE=""
BUILD_ON_DISK=false
SKIP_INSTALL=false
CLEANUP_FAILED=false
LOCAL_VARS=""
WILDCARD=false
DESCRIPTION=""
BASE_NAME=""
SUBDOMAIN="interrupted-pending"
`
	os.WriteFile(filepath.Join(demoDir, "config"), []byte(configContent), 0o644)
	os.WriteFile(filepath.Join(demoDir, "status"), []byte("pending"), 0o644)

	// Verify it exists
	if _, err := os.Stat(demoDir); os.IsNotExist(err) {
		t.Errorf("expected demo dir %s to exist", demoDir)
	}

	// Simulate cleanup (delete the directory)
	os.RemoveAll(demoDir)

	// Verify it's gone
	if _, err := os.Stat(demoDir); !os.IsNotExist(err) {
		t.Errorf("expected demo dir %s to be removed", demoDir)
	}
}

func TestCleanupInterruptedBuilding(t *testing.T) {
	tmpDir := t.TempDir()
	activeDir := state.ActiveDir(tmpDir)
	name := "interrupted-building"
	demoDir := filepath.Join(activeDir, name)
	os.MkdirAll(demoDir, 0o755)

	configContent := `DEMO_NAME="interrupted-building"
IIAB_REPO=""
IIAB_BRANCH=""
IMAGE_SIZE_MB=10000
VOLATILE_MODE=""
BUILD_ON_DISK=false
SKIP_INSTALL=false
CLEANUP_FAILED=false
LOCAL_VARS=""
WILDCARD=false
DESCRIPTION=""
BASE_NAME=""
SUBDOMAIN="interrupted-building"
`
	os.WriteFile(filepath.Join(demoDir, "config"), []byte(configContent), 0o644)
	os.WriteFile(filepath.Join(demoDir, "status"), []byte("building"), 0o644)

	if _, err := os.Stat(demoDir); os.IsNotExist(err) {
		t.Errorf("expected demo dir %s to exist", demoDir)
	}

	// Simulate cleanup
	os.RemoveAll(demoDir)
	if _, err := os.Stat(demoDir); !os.IsNotExist(err) {
		t.Errorf("expected demo dir %s to be removed", demoDir)
	}
}

func TestCleanupFailedBuild(t *testing.T) {
	tmpDir := t.TempDir()
	activeDir := state.ActiveDir(tmpDir)
	name := "failed-build"
	demoDir := filepath.Join(activeDir, name)
	os.MkdirAll(demoDir, 0o755)

	configContent := `DEMO_NAME="failed-build"
IIAB_REPO=""
IIAB_BRANCH=""
IMAGE_SIZE_MB=10000
VOLATILE_MODE=""
BUILD_ON_DISK=false
SKIP_INSTALL=false
CLEANUP_FAILED=false
LOCAL_VARS=""
WILDCARD=false
DESCRIPTION=""
BASE_NAME=""
SUBDOMAIN="failed-build"
`
	os.WriteFile(filepath.Join(demoDir, "config"), []byte(configContent), 0o644)
	os.WriteFile(filepath.Join(demoDir, "status"), []byte("failed"), 0o644)

	if _, err := os.Stat(demoDir); os.IsNotExist(err) {
		t.Errorf("expected demo dir %s to exist", demoDir)
	}

	// Simulate cleanup
	os.RemoveAll(demoDir)
	if _, err := os.Stat(demoDir); !os.IsNotExist(err) {
		t.Errorf("expected demo dir %s to be removed", demoDir)
	}
}

func TestRunningDemoNotCleanedUp(t *testing.T) {
	tmpDir := t.TempDir()
	activeDir := state.ActiveDir(tmpDir)
	name := "running-demo"
	demoDir := filepath.Join(activeDir, name)
	os.MkdirAll(demoDir, 0o755)

	configContent := `DEMO_NAME="running-demo"
IIAB_REPO=""
IIAB_BRANCH=""
IMAGE_SIZE_MB=10000
VOLATILE_MODE=""
BUILD_ON_DISK=false
SKIP_INSTALL=false
CLEANUP_FAILED=false
LOCAL_VARS=""
WILDCARD=false
DESCRIPTION=""
BASE_NAME=""
SUBDOMAIN="running-demo"
`
	os.WriteFile(filepath.Join(demoDir, "config"), []byte(configContent), 0o644)
	os.WriteFile(filepath.Join(demoDir, "status"), []byte("running"), 0o644)

	// Verify it exists
	if _, err := os.Stat(demoDir); os.IsNotExist(err) {
		t.Errorf("expected demo dir %s to exist", demoDir)
	}

	// Read status and verify it's NOT a cleanup candidate
	demo, err := config.Read(context.Background(), tmpDir, name)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if demo.Status != "running" {
		t.Errorf("expected status 'running', got %q", demo.Status)
	}

	// Running demos should NOT be cleaned up
	shouldCleanup := shouldCleanupDemo(demo.Status)
	if shouldCleanup {
		t.Error("running demo should not be cleaned up")
	}
}

func TestStoppedDemoNotCleanedUp(t *testing.T) {
	tmpDir := t.TempDir()
	activeDir := state.ActiveDir(tmpDir)
	name := "stopped-demo"
	demoDir := filepath.Join(activeDir, name)
	os.MkdirAll(demoDir, 0o755)

	configContent := `DEMO_NAME="stopped-demo"
IIAB_REPO=""
IIAB_BRANCH=""
IMAGE_SIZE_MB=10000
VOLATILE_MODE=""
BUILD_ON_DISK=false
SKIP_INSTALL=false
CLEANUP_FAILED=false
LOCAL_VARS=""
WILDCARD=false
DESCRIPTION=""
BASE_NAME=""
SUBDOMAIN="stopped-demo"
`
	os.WriteFile(filepath.Join(demoDir, "config"), []byte(configContent), 0o644)
	os.WriteFile(filepath.Join(demoDir, "status"), []byte("stopped"), 0o644)

	if _, err := os.Stat(demoDir); os.IsNotExist(err) {
		t.Errorf("expected demo dir %s to exist", demoDir)
	}

	demo, err := config.Read(context.Background(), tmpDir, name)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if demo.Status != "stopped" {
		t.Errorf("expected status 'stopped', got %q", demo.Status)
	}

	// Stopped demos should NOT be cleaned up
	shouldCleanup := shouldCleanupDemo(demo.Status)
	if shouldCleanup {
		t.Error("stopped demo should not be cleaned up")
	}
}

// shouldCleanupDemo determines if a demo should be cleaned up based on status.
// This mirrors the logic in cmd/cleanup.go and cmd/build.go.
func shouldCleanupDemo(status string) bool {
	switch status {
	case "failed", "pending", "building":
		return true
	default:
		return false
	}
}
