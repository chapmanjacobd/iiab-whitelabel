// Package lock provides flock-based mutual exclusion for democtl operations.
package lock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/sys"
)

var (
	// ShortTimeout is the timeout in seconds for AcquireShort.
	ShortTimeout = 20 //nolint:gochecknoglobals // Mutated in tests to reduce wait times.
	// LongTimeout is the timeout in seconds for AcquireLong.
	LongTimeout = 10800 //nolint:gochecknoglobals // Mutated in tests to reduce wait times.
)

// Lock wraps a flock file handle.
type Lock struct {
	flock *flock.Flock
}

// AcquireShort acquires an exclusive lock on the given file with a short timeout.
func AcquireShort(ctx context.Context, lockFile string) (*Lock, error) {
	return acquireInternal(ctx, lockFile, ShortTimeout)
}

// AcquireLong acquires an exclusive lock on the given file with a long timeout.
func AcquireLong(ctx context.Context, lockFile string) (*Lock, error) {
	return acquireInternal(ctx, lockFile, LongTimeout)
}

// acquireInternal acquires an exclusive lock on the given file.
// timeout: 0 = non-blocking, >0 = seconds to wait.
func acquireInternal(ctx context.Context, lockFile string, timeout int) (*Lock, error) {
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(lockFile), 0o755); err != nil {
		return nil, fmt.Errorf("cannot create lock directory: %w", err)
	}

	f := flock.New(lockFile)

	var acquired bool
	var err error

	if timeout == 0 {
		acquired, err = f.TryLock()
	} else {
		// Use TryLockContext with a timeout context
		lockCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
		acquired, err = f.TryLockContext(lockCtx, 100*time.Millisecond)
	}

	if err != nil {
		return nil, fmt.Errorf("lock error: %w", err)
	}
	if !acquired {
		return nil, errors.New("another democtl operation is in progress (lock held)")
	}

	return &Lock{flock: f}, nil
}

// Release releases the lock.
func (l *Lock) Release() error {
	if l.flock != nil {
		return l.flock.Unlock()
	}
	return nil
}

// IsBuildInProgress checks if a demo build is actively running.
// Returns true if a PID file exists and the process is alive.
func IsBuildInProgress(stateDir, name string) bool {
	demoDir := state.DemoDir(stateDir, name)
	pidFile := filepath.Join(demoDir, "build.pid")

	data, err := os.ReadFile(pidFile)
	if err != nil {
		return false
	}

	pidStr := strings.TrimSpace(string(data))
	if pidStr == "" {
		return false
	}

	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return false
	}

	return sys.IsProcessAlive(pid)
}

// ReadBuildPID reads the build PID file. Returns 0 if not found.
func ReadBuildPID(stateDir, name string) int {
	demoDir := state.DemoDir(stateDir, name)
	pidFile := filepath.Join(demoDir, "build.pid")

	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0
	}

	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid
}

// WriteBuildPID writes the build PID file.
func WriteBuildPID(stateDir, name string, pid int) error {
	demoDir := state.DemoDir(stateDir, name)
	return os.WriteFile(filepath.Join(demoDir, "build.pid"), []byte(strconv.Itoa(pid)), 0o644)
}

// RemoveBuildPID removes the build PID file.
func RemoveBuildPID(stateDir, name string) {
	demoDir := state.DemoDir(stateDir, name)
	os.Remove(filepath.Join(demoDir, "build.pid"))
}
