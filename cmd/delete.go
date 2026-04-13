package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/config"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/lock"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/nginx"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/storage"
)

// DeleteCmd stops and deletes demo(s).
type DeleteCmd struct {
	Names []string `help:"Demo name(s) to delete" arg:"" optional:""`
	All   bool     `help:"Delete all demos"                          default:"false"`
}

// Run executes the delete command.
func (c *DeleteCmd) Run(ctx context.Context, globals *GlobalOptions) error {
	if err := ensureRoot(); err != nil {
		return err
	}

	names := c.Names
	if c.All {
		var err error
		names, err = config.List(globals.StateDir)
		if err != nil {
			return err
		}
	}

	if len(names) == 0 {
		return errors.New("no demos specified. Use demo name(s) or --all")
	}

	for _, name := range names {
		if err := deleteDemo(ctx, globals, name); err != nil {
			slog.ErrorContext(ctx, "Delete failed", "demo", name, "error", err)
		} else {
			slog.InfoContext(ctx, "Deleted", "demo", name)
		}
	}
	return nil
}

func deleteDemo(ctx context.Context, globals *GlobalOptions, name string) error {
	// Stop if running (ignore error since container might not be running)
	if err := stopDemo(ctx, globals, name); err != nil {
		slog.WarnContext(ctx, "Stop during delete failed", "demo", name, "error", err)
	}

	demoDir := state.DemoDir(globals.StateDir, name)

	// Read config BEFORE removing demo directory (needed for cert cleanup)
	demo, readErr := config.Read(ctx, globals.StateDir, name)
	if readErr != nil {
		slog.WarnContext(
			ctx,
			"Failed to read demo config (cert cleanup may use fallback subdomain)",
			"demo",
			name,
			"error",
			readErr,
		)
	}
	subdomain := state.SanitizeSubdomain(name)
	if demo != nil && demo.Subdomain != "" {
		subdomain = demo.Subdomain
	}

	// Remove build PID
	lock.RemoveBuildPID(globals.StateDir, name)
	os.Remove(demoDir + "/build.watchdog")

	// Clean up resources (container, veth, subvolume)
	if err := storage.CleanupResources(ctx, name, subdomain, globals.System); err != nil {
		slog.WarnContext(ctx, "Resource cleanup had errors (continuing with delete)", "demo", name, "error", err)
	}

	// Remove state
	if err := os.RemoveAll(demoDir); err != nil {
		return fmt.Errorf("cannot remove demo directory: %w", err)
	}

	// Clean up orphaned certs using subdomain from config
	certDir := fmt.Sprintf("/etc/letsencrypt/live/%s.iiab.io", subdomain)
	os.RemoveAll(certDir)

	// Reload nginx
	return nginx.Generate(ctx, globals.StateDir)
}

// cleanupFailedBuild removes a failed build's resources.
func cleanupFailedBuild(ctx context.Context, globals *GlobalOptions, name string) error {
	demoDir := state.DemoDir(globals.StateDir, name)

	// Kill build processes
	lock.RemoveBuildPID(globals.StateDir, name)

	// Clean up watchdog if present
	os.Remove(filepath.Join(demoDir, "build.watchdog"))

	// Remove demo directory (including IP file)
	if err := os.RemoveAll(demoDir); err != nil {
		return fmt.Errorf("cannot remove demo directory: %w", err)
	}

	// Clean up btrfs lock files
	buildsDirs := []string{"/run/iiab-demos/storage/builds", "/var/iiab-demos/storage/builds"}
	for _, bdir := range buildsDirs {
		if state.FileExists(bdir) {
			os.Remove(filepath.Join(bdir, ".#"+name+".lck"))
			os.Remove(filepath.Join(bdir, "."+name+".lck"))
			os.Remove(filepath.Join(bdir, name+".lck"))
		}
	}

	slog.InfoContext(ctx, "Cleanup complete (directory and IP slot reclaimed)", "demo", name)
	return nil
}
