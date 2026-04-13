package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/config"
)

// CleanupResources removes container, veth, and subvolume resources.
// Returns a combined error if any cleanup step fails.
func CleanupResources(ctx context.Context, name, subdomain string, sys *config.System) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	var errs []error

	// Force-terminate nspawn machine
	if err := exec.CommandContext(ctx, "machinectl", "terminate", name).Run(); err != nil {
		slog.WarnContext(ctx, "Failed to terminate machine", "name", name, "error", err)
		errs = append(errs, fmt.Errorf("machinectl terminate: %w", err))
	}

	// Clean up veth interfaces (use sanitized subdomain since that's what was used to create them)
	for _, prefix := range []string{"ve-", "vb-"} {
		iface := prefix + subdomain
		if _, err := net.InterfaceByName(iface); err == nil {
			if err := exec.CommandContext(ctx, "ip", "link", "delete", iface).Run(); err != nil {
				slog.WarnContext(ctx, "Failed to delete veth interface", "name", iface, "error", err)
				errs = append(errs, fmt.Errorf("delete veth %s: %w", iface, err))
			}
		}
	}

	// Delete btrfs subvolumes across all backends
	for _, root := range FindStorageRoots(ctx) {
		if err := DeleteSubvolumeWithRetry(ctx, filepath.Join(root, "builds", name)); err != nil {
			errs = append(errs, err)
		}
	}

	// Remove image symlink
	if err := os.Remove(filepath.Join(sys.MachinesDir, name)); err != nil && !os.IsNotExist(err) {
		slog.WarnContext(ctx, "Failed to remove image symlink", "name", name, "error", err)
		errs = append(errs, fmt.Errorf("remove image symlink: %w", err))
	}

	// Remove .nspawn config
	if err := os.Remove(filepath.Join(sys.NspawnDir, name+".nspawn")); err != nil && !os.IsNotExist(err) {
		slog.WarnContext(ctx, "Failed to remove nspawn config", "name", name, "error", err)
		errs = append(errs, fmt.Errorf("remove nspawn config: %w", err))
	}

	// Remove service override
	if err := os.RemoveAll("/etc/systemd/system/systemd-nspawn@" + name + ".service.d"); err != nil {
		slog.WarnContext(ctx, "Failed to remove service override", "name", name, "error", err)
		errs = append(errs, fmt.Errorf("remove service override: %w", err))
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
