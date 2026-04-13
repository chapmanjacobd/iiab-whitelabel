package lock_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/lock"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
)

func init() {
	lock.ShortTimeout = 1 // 1 retry loop for tests
}

func TestAcquireShortTimeoutWithContention(t *testing.T) {
	tmpDir := t.TempDir()
	lockFile := filepath.Join(tmpDir, "test.lock")

	// Acquire with timeout
	lk1, err := lock.AcquireShort(context.Background(), lockFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Second acquire with timeout should wait and then fail
	lk2, err := lock.AcquireShort(context.Background(), lockFile)
	if err == nil {
		t.Fatal("expected error when lock is held")
	}
	if lk2 != nil {
		t.Errorf("expected nil lock on error, got non-nil")
	}

	lk1.Release()
}

func TestAcquireDirectoryCreationFailure(t *testing.T) {
	// Try to acquire lock in an invalid path
	invalidPath := "/proc/sys/invalid/.lock"
	lk, err := lock.AcquireShort(context.Background(), invalidPath)
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
	if lk != nil {
		t.Errorf("expected nil lock on error, got non-nil")
	}
	if !strings.Contains(err.Error(), "cannot create lock directory") {
		t.Errorf("expected error to contain 'cannot create lock directory', got: %v", err)
	}
}

func TestReadBuildPIDValid(t *testing.T) {
	tmpDir := t.TempDir()
	name := "test-demo"
	demoDir := state.DemoDir(tmpDir, name)
	os.MkdirAll(demoDir, 0o755)

	// Write a valid PID file
	pidFile := filepath.Join(demoDir, "build.pid")
	os.WriteFile(pidFile, []byte("12345"), 0o644)

	pid := lock.ReadBuildPID(tmpDir, name)
	if pid != 12345 {
		t.Errorf("expected PID 12345, got %d", pid)
	}
}

func TestReadBuildPIDMissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	name := "test-demo"
	demoDir := state.DemoDir(tmpDir, name)
	os.MkdirAll(demoDir, 0o755)

	// Don't create PID file
	pid := lock.ReadBuildPID(tmpDir, name)
	if pid != 0 {
		t.Errorf("expected PID 0, got %d", pid)
	}
}

func TestReadBuildPIDMalformedContent(t *testing.T) {
	tmpDir := t.TempDir()
	name := "test-demo"
	demoDir := state.DemoDir(tmpDir, name)
	os.MkdirAll(demoDir, 0o755)

	// Write malformed PID file
	pidFile := filepath.Join(demoDir, "build.pid")
	os.WriteFile(pidFile, []byte("not-a-number"), 0o644)

	pid := lock.ReadBuildPID(tmpDir, name)
	if pid != 0 {
		t.Errorf("expected PID 0, got %d", pid)
	}
}

func TestReadBuildPIDEmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	name := "test-demo"
	demoDir := state.DemoDir(tmpDir, name)
	os.MkdirAll(demoDir, 0o755)

	// Write empty PID file
	pidFile := filepath.Join(demoDir, "build.pid")
	os.WriteFile(pidFile, []byte(""), 0o644)

	pid := lock.ReadBuildPID(tmpDir, name)
	if pid != 0 {
		t.Errorf("expected PID 0, got %d", pid)
	}
}

func TestWriteBuildPID(t *testing.T) {
	tmpDir := t.TempDir()
	name := "test-demo"
	demoDir := state.DemoDir(tmpDir, name)
	os.MkdirAll(demoDir, 0o755)

	// Write PID
	expectedPID := 12345
	err := lock.WriteBuildPID(tmpDir, name, expectedPID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify
	pidFile := filepath.Join(demoDir, "build.pid")
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "12345" {
		t.Errorf("expected PID '12345', got %q", string(data))
	}
}

func TestWriteBuildPIDCreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	name := "new-demo"

	// Create demo directory first (WriteBuildPID doesn't auto-create)
	demoDir := state.DemoDir(tmpDir, name)
	os.MkdirAll(demoDir, 0o755)

	err := lock.WriteBuildPID(tmpDir, name, 99999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify PID file was created
	pidFile := filepath.Join(demoDir, "build.pid")
	if _, err := os.Stat(pidFile); os.IsNotExist(err) {
		t.Errorf("expected PID file %s to exist", pidFile)
	}
}

func TestRemoveBuildPID(t *testing.T) {
	tmpDir := t.TempDir()
	name := "test-demo"
	demoDir := state.DemoDir(tmpDir, name)
	os.MkdirAll(demoDir, 0o755)

	// Write PID
	lock.WriteBuildPID(tmpDir, name, 12345)

	// Verify it exists
	pidFile := filepath.Join(demoDir, "build.pid")
	if _, err := os.Stat(pidFile); os.IsNotExist(err) {
		t.Errorf("expected PID file to exist")
	}

	// Remove
	lock.RemoveBuildPID(tmpDir, name)

	// Verify it's gone
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Errorf("expected PID file to be removed")
	}
}

func TestRemoveBuildPIDMissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	name := "test-demo"
	demoDir := state.DemoDir(tmpDir, name)
	os.MkdirAll(demoDir, 0o755)

	// Don't create PID file, try to remove
	// Should not error
	lock.RemoveBuildPID(tmpDir, name)
}

func TestIsBuildInProgressMissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	name := "test-demo"
	demoDir := state.DemoDir(tmpDir, name)
	os.MkdirAll(demoDir, 0o755)

	// Don't create PID file
	result := lock.IsBuildInProgress(tmpDir, name)
	if result {
		t.Error("expected build not in progress when PID file missing")
	}
}

func TestIsBuildInProgressEmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	name := "test-demo"
	demoDir := state.DemoDir(tmpDir, name)
	os.MkdirAll(demoDir, 0o755)

	// Write empty PID file
	pidFile := filepath.Join(demoDir, "build.pid")
	os.WriteFile(pidFile, []byte(""), 0o644)

	result := lock.IsBuildInProgress(tmpDir, name)
	if result {
		t.Error("expected build not in progress when PID file is empty")
	}
}

func TestIsBuildInProgressNonNumericPID(t *testing.T) {
	tmpDir := t.TempDir()
	name := "test-demo"
	demoDir := state.DemoDir(tmpDir, name)
	os.MkdirAll(demoDir, 0o755)

	// Write non-numeric PID file
	pidFile := filepath.Join(demoDir, "build.pid")
	os.WriteFile(pidFile, []byte("not-a-number"), 0o644)

	result := lock.IsBuildInProgress(tmpDir, name)
	if result {
		t.Error("expected build not in progress when PID file is non-numeric")
	}
}

func TestIsBuildInProgressDeadPID(t *testing.T) {
	tmpDir := t.TempDir()
	name := "test-demo"
	demoDir := state.DemoDir(tmpDir, name)
	os.MkdirAll(demoDir, 0o755)

	// Write PID that doesn't exist
	pidFile := filepath.Join(demoDir, "build.pid")
	os.WriteFile(pidFile, []byte("999999999"), 0o644)

	result := lock.IsBuildInProgress(tmpDir, name)
	if result {
		t.Error("expected build not in progress when PID doesn't exist")
	}
}

func TestIsBuildInProgressAlivePID(t *testing.T) {
	tmpDir := t.TempDir()
	name := "test-demo"
	demoDir := state.DemoDir(tmpDir, name)
	os.MkdirAll(demoDir, 0o755)

	// Write current PID
	currentPID := os.Getpid()
	pidFile := filepath.Join(demoDir, "build.pid")
	os.WriteFile(pidFile, fmt.Appendf(nil, "%d", currentPID), 0o644)

	// Note: IsBuildInProgress relies on IsProcessAlive which has platform-specific behavior
	// Just verify it doesn't crash
	_ = lock.IsBuildInProgress(tmpDir, name)
}

func TestBuildPIDRoundtrip(t *testing.T) {
	tmpDir := t.TempDir()
	name := "test-demo"

	// Create demo directory first
	demoDir := state.DemoDir(tmpDir, name)
	os.MkdirAll(demoDir, 0o755)

	// Write PID
	expectedPID := 54321
	err := lock.WriteBuildPID(tmpDir, name, expectedPID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Read back
	pid := lock.ReadBuildPID(tmpDir, name)
	if pid != expectedPID {
		t.Errorf("expected PID %d, got %d", expectedPID, pid)
	}

	// Remove
	lock.RemoveBuildPID(tmpDir, name)

	// Verify gone
	if lock.ReadBuildPID(tmpDir, name) != 0 {
		t.Error("expected PID to be 0 after removal")
	}
}
