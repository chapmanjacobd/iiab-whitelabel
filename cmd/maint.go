package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/config"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/network"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/nginx"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/storage"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/sys"
)

// CleanupCmd cleans up failed builds and orphaned subvolumes.
type CleanupCmd struct {
	All    bool `help:"Clean up all failed builds and orphans" default:"false"`
	DryRun bool `help:"Preview what would be cleaned up"       default:"false" name:"dry-run"`
}

// Run executes the cleanup command.
func (c *CleanupCmd) Run(ctx context.Context, globals *GlobalOptions) error {
	if err := ensureRoot(); err != nil {
		return err
	}

	lk, err := acquireLongLock(ctx, globals)
	if err != nil {
		return err
	}
	defer func() { _ = lk.Release() }()

	// Clean failed demos
	names, err := config.List(globals.StateDir)
	if err != nil {
		return err
	}

	for _, name := range names {
		status, _ := config.GetDemoStatus(globals.StateDir, name)
		if status != "failed" && status != "pending" && status != "building" {
			continue
		}

		if c.DryRun {
			slog.InfoContext(ctx, "Would cleanup failed demo", "name", name, "status", status)
			continue
		}

		if err := cleanupFailedBuild(ctx, globals, name); err != nil {
			slog.ErrorContext(ctx, "Cleanup failed", "demo", name, "error", err)
		} else {
			slog.InfoContext(ctx, "Cleaned up failed build", "demo", name)
		}
	}

	// Clean orphaned subvolumes in btrfs storage
	cleanupOrphanedSubvolumes(ctx, globals, c.DryRun)

	// Clean orphaned machines in machined
	cleanupOrphanedMachines(ctx, globals, c.DryRun)

	if c.All {
		if c.DryRun {
			slog.InfoContext(ctx, "Would perform aggressive cleanup (unmount and delete storage files)")
		} else {
			if err := cleanupAggressive(ctx, globals.StateDir, globals.System); err != nil {
				return err
			}
		}
	}

	return nil
}

// cleanupAggressive unmounts all iiab storage and deletes the backing btrfs files.
// It refuses to run if any demo is currently running.
func cleanupAggressive(ctx context.Context, stateDir string, sys *config.System) error {
	slog.InfoContext(ctx, "Performing aggressive cleanup...")

	// Refuse if any demo is running
	names, err := config.List(stateDir)
	if err != nil {
		return fmt.Errorf("cannot list demos: %w", err)
	}
	for _, name := range names {
		status, _ := config.GetDemoStatus(stateDir, name)
		if status == "running" {
			return fmt.Errorf("cannot perform aggressive cleanup: demo '%s' is running (stop all demos first)", name)
		}
	}

	terminateAllMachines(ctx)
	unmountAllStorage(ctx)
	detachAllLoopDevices(ctx)
	removeBridge(ctx)
	cleanOrphanedMachineSymlinks(sys)
	cleanupEmptyDirs()

	slog.InfoContext(ctx, "Aggressive cleanup complete")
	return nil
}

func terminateAllMachines(ctx context.Context) {
	tctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	out, err := exec.CommandContext(tctx, "machinectl", "list", "--no-legend").Output()
	cancel()

	if err != nil {
		slog.WarnContext(ctx, "Failed to list machines", "error", err)
		return
	}

	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			name := fields[0]
			slog.InfoContext(ctx, "Terminating machine for aggressive cleanup", "name", name)
			tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			_ = exec.CommandContext(tctx, "machinectl", "terminate", name).Run()
			cancel()
		}
	}
}

func unmountAllStorage(ctx context.Context) {
	mounts := []string{
		"/run/iiab-demos/storage",
		"/var/iiab-demos/storage",
		"/run/iiab-demos/alt-ram-storage",
		"/var/iiab-demos/alt-disk-storage",
	}
	for _, mount := range mounts {
		for range 3 {
			if !sys.Mountpoint(ctx, mount) {
				break
			}
			slog.InfoContext(ctx, "Unmounting storage", "mount", mount)
			tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			_ = exec.CommandContext(tctx, "umount", "-l", mount).Run()
			cancel()
			time.Sleep(500 * time.Millisecond)
		}
	}
}

func detachAllLoopDevices(ctx context.Context) {
	files := []string{
		"/run/iiab-demos/storage.btrfs",
		"/var/iiab-demos/storage.btrfs",
	}
	for _, file := range files {
		slog.InfoContext(ctx, "Detaching loop devices for file", "file", file)
		tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		out, losetupErr := exec.CommandContext(tctx, "losetup", "-j", file).Output()
		cancel()
		if losetupErr == nil {
			for line := range strings.SplitSeq(string(out), "\n") {
				if fields := strings.Split(line, ":"); len(fields) > 0 {
					loopDev := fields[0]
					slog.InfoContext(ctx, "Detaching loop device", "device", loopDev)
					tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
					_ = exec.CommandContext(tctx, "losetup", "-d", loopDev).Run()
					cancel()
				}
			}
		}

		if state.FileExists(file) {
			slog.InfoContext(ctx, "Deleting storage file", "file", file)
			if removeErr := os.Remove(file); removeErr != nil {
				slog.WarnContext(ctx, "Failed to delete storage file", "file", file, "error", removeErr)
			}
		}
	}
}

func removeBridge(ctx context.Context) {
	slog.InfoContext(ctx, "Removing iiab bridge")
	bctx, bcancel := context.WithTimeout(ctx, 30*time.Second)
	_ = exec.CommandContext(bctx, "ip", "link", "set", "iiab-br0", "down").Run()
	_ = exec.CommandContext(bctx, "ip", "link", "delete", "iiab-br0").Run()
	bcancel()
}

func cleanOrphanedMachineSymlinks(sys *config.System) {
	entries, err := os.ReadDir(sys.MachinesDir)
	if err != nil {
		slog.Warn("Failed to read machines directory", "path", sys.MachinesDir, "error", err)
		return
	}
	for _, e := range entries {
		path := filepath.Join(sys.MachinesDir, e.Name())
		if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			target, readErr := os.Readlink(path)
			if readErr == nil &&
				(strings.HasPrefix(target, "/run/iiab-demos") || strings.HasPrefix(target, "/var/iiab-demos")) {

				slog.Info("Removing orphaned machine symlink", "path", path, "target", target)
				os.Remove(path)
			}
		}
	}
}

// cleanupEmptyDirs removes empty directories left over from storage operations.
// These are created during storage setup but should be removed if empty
// after aggressive cleanup.
func cleanupEmptyDirs() {
	emptyDirs := []string{
		"/run/iiab-demos/storage",
		"/run/iiab-demos/alt-ram-storage",
		"/var/iiab-demos/storage",
		"/var/iiab-demos/alt-disk-storage",
	}
	for _, dir := range emptyDirs {
		if state.FileExists(dir) {
			if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
				slog.Info("Removing empty storage directory", "path", dir)
				if err := os.Remove(dir); err != nil {
					slog.Warn("Failed to remove empty storage directory", "path", dir, "error", err)
				}
			}
		}
	}

	// Remove top-level empty directories
	for _, dir := range []string{"/run/iiab-demos", "/var/iiab-demos"} {
		if state.FileExists(dir) {
			if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
				slog.Info("Removing empty state directory", "path", dir)
				_ = os.Remove(dir)
			}
		}
	}
}

// cleanupOrphanedMachines terminates and cleans up machines without corresponding demo config.
func cleanupOrphanedMachines(ctx context.Context, globals *GlobalOptions, dryRun bool) {
	tctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	out, err := exec.CommandContext(tctx, "machinectl", "list", "--no-legend").Output()
	if err != nil {
		return
	}

	activeNames, _ := config.List(globals.StateDir)
	activeSet := make(map[string]bool)
	for _, n := range activeNames {
		activeSet[n] = true
	}

	lines := strings.SplitSeq(string(out), "\n")
	for line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 1 {
			continue
		}
		name := fields[0]
		if activeSet[name] {
			continue
		}

		if dryRun {
			slog.InfoContext(ctx, "Would cleanup orphaned machine", "name", name)
			continue
		}

		slog.InfoContext(ctx, "Cleaning up orphaned machine", "name", name)
		// We use name as subdomain fallback here
		if err := storage.CleanupResources(ctx, name, name, globals.System); err != nil {
			slog.WarnContext(ctx, "Failed to cleanup orphaned machine resources", "name", name, "error", err)
		}
	}
}

// cleanupOrphanedSubvolumes removes subvolumes without corresponding demo config.
func cleanupOrphanedSubvolumes(ctx context.Context, globals *GlobalOptions, dryRun bool) {
	for _, storageRoot := range storage.FindStorageRoots(ctx) {
		buildsDir := filepath.Join(storageRoot, "builds")
		entries, err := os.ReadDir(buildsDir)
		if err != nil {
			continue
		}

		activeNames, _ := config.List(globals.StateDir)
		activeSet := make(map[string]bool)
		for _, n := range activeNames {
			activeSet[n] = true
		}

		for _, e := range entries {
			if !e.IsDir() || activeSet[e.Name()] {
				continue
			}

			if dryRun {
				slog.InfoContext(ctx, "Would remove orphaned subvolume", "storage", storageRoot, "name", e.Name())
				continue
			}

			subvol := filepath.Join(buildsDir, e.Name())
			if err := storage.DeleteSubvolumeWithRetry(ctx, subvol); err != nil {
				slog.WarnContext(
					ctx,
					"Failed to remove orphaned subvolume",
					"storage",
					storageRoot,
					"name",
					e.Name(),
					"error",
					err,
				)
			} else {
				slog.InfoContext(ctx, "Removed orphaned subvolume", "storage", storageRoot, "name", e.Name())
			}
		}
	}
}

// ReloadCmd regenerates nginx config.
type ReloadCmd struct{}

// Run executes the reload command.
func (c *ReloadCmd) Run(ctx context.Context, globals *GlobalOptions) error {
	if err := ensureRoot(); err != nil {
		return err
	}
	return nginx.Generate(ctx, globals.StateDir)
}

// ReconcileCmd fixes resource counter drift and checks ghost IPs.
type ReconcileCmd struct{}

// Run executes the reconcile command.
func (c *ReconcileCmd) Run(ctx context.Context, globals *GlobalOptions) error {
	if err := ensureRoot(); err != nil {
		return err
	}

	lk, err := acquireLongLock(ctx, globals)
	if err != nil {
		return err
	}
	defer func() { _ = lk.Release() }()

	names, err := config.List(globals.StateDir)
	if err != nil {
		return err
	}

	totalAllocated := 0
	for _, name := range names {
		demo, err := config.Read(ctx, globals.StateDir, name)
		if err != nil {
			continue
		}
		totalAllocated += demo.ImageSizeMB
	}

	// Update resource file
	resources := &config.Resources{
		DiskTotalMB:     sys.GetDiskTotalMB(ctx, globals.StateDir, globals.System.MachinesDir),
		DiskAllocatedMB: totalAllocated,
	}
	if err := config.WriteResources(globals.StateDir, resources); err != nil {
		return err
	}

	fmt.Printf("Reconciled resource counters: %d demos, %d MB allocated\n", len(names), totalAllocated)

	// Check for ghost IPs
	network.CheckGhostIPs(ctx, globals.StateDir, names)

	return nil
}
