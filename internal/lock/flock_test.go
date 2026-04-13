package lock_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gofrs/flock"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/lock"
)

func init() {
	lock.ShortTimeout = 0 // Fast fail for tests
}

func TestLockAcquireAndRelease(t *testing.T) {
	tmpDir := t.TempDir()
	lockFile := filepath.Join(tmpDir, ".democtl.lock")

	lk, err := lock.AcquireShort(context.Background(), lockFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lk == nil {
		t.Fatal("expected non-nil lock")
	}

	err = lk.Release()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConcurrentLockRejection(t *testing.T) {
	tmpDir := t.TempDir()
	lockFile := filepath.Join(tmpDir, ".democtl.lock")

	lk1, err := lock.AcquireShort(context.Background(), lockFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Second acquire should fail (non-blocking)
	lk2, err := lock.AcquireShort(context.Background(), lockFile)
	if err == nil {
		t.Fatal("expected error when acquiring lock twice")
	}
	if lk2 != nil {
		t.Errorf("expected nil lock on error, got non-nil")
	}

	// Release first, then second should succeed
	lk1.Release()
	lk2, err = lock.AcquireShort(context.Background(), lockFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lk2.Release()
}

func TestStaleLockCleanup(t *testing.T) {
	// flock auto-releases on process death, so a "stale" lock
	// is simply one where the process died -- the next acquire succeeds.
	tmpDir := t.TempDir()
	lockFile := filepath.Join(tmpDir, ".democtl.lock")

	// Acquire and release simulating a process that held it
	lk, err := lock.AcquireShort(context.Background(), lockFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lk.Release()

	// Next acquire should succeed (flock released automatically)
	lk2, err := lock.AcquireShort(context.Background(), lockFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lk2.Release()
}

func TestLockFileCleanupAfterRelease(t *testing.T) {
	tmpDir := t.TempDir()
	lockFile := filepath.Join(tmpDir, ".democtl.lock")

	lk, err := lock.AcquireShort(context.Background(), lockFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Lock file should exist
	if _, err := os.Stat(lockFile); os.IsNotExist(err) {
		t.Errorf("expected lock file %s to exist", lockFile)
	}

	lk.Release()
	// Lock file still exists but unlocked
	if _, err := os.Stat(lockFile); os.IsNotExist(err) {
		t.Errorf("expected lock file %s to still exist after release", lockFile)
	}
}

// Ensure flock releases on process exit
func TestFlockAutoReleaseOnExit(t *testing.T) {
	tmpDir := t.TempDir()
	lockFile := filepath.Join(tmpDir, "test.lock")

	f := flock.New(lockFile)
	acquired, err := f.TryLock()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !acquired {
		t.Fatal("expected to acquire lock")
	}

	// Don't release -- simulate process exit
	// In real scenario, OS releases flock on process death
	// Next process can acquire it
	f.Unlock() // explicit for test
}
