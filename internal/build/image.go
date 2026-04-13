package build

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/command"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/config"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/storage"
)

// ensureBaseSubvolume creates or copies the base-debian subvolume.
func ensureBaseSubvolume(ctx context.Context, info *storage.StorageInfo, onDisk bool, baseName string) error {
	// Determine what base subvolume we need
	baseSubvol := "base-debian"
	if baseName != "" {
		baseSubvol = baseName
	}

	// Check if it exists locally
	if storage.SubvolumeExists(ctx, info.Mount, baseSubvol) ||
		storage.SubvolumeExists(ctx, info.Mount, "builds/"+baseSubvol) {

		return nil
	}

	// Not local - try to copy from alternate storage
	altStorage, altMount := storage.GetAlternateStoragePath(onDisk)
	if state.FileExists(altStorage) {
		slog.InfoContext(ctx, "Base subvolume not in current storage, checking alternate", "subvolume", baseSubvol)
		err := storage.CopySubvolumeFromAlternate(ctx, baseSubvol, altStorage, altMount, info.Mount)
		if err == nil {
			return nil
		}
		slog.WarnContext(ctx, "Could not copy from alternate storage", "error", err)
		if baseName != "" {
			return fmt.Errorf("base subvolume '%s' not found in current or alternate storage", baseSubvol)
		}
		slog.InfoContext(ctx, "Will download base subvolume from cloud")
	} else if baseName != "" {
		return fmt.Errorf("base subvolume '%s' not found and no alternate storage available", baseSubvol)
	}

	// Download and extract Debian genericcloud image
	slog.InfoContext(ctx, "Downloading and extracting Debian 13 genericcloud image")

	// Download tarball into a temp dir
	tmpdir, mkdirErr := os.MkdirTemp(info.Mount, "debian-base.*")
	if mkdirErr != nil {
		return mkdirErr
	}
	defer os.RemoveAll(tmpdir)

	// Download tarball
	tarFile := filepath.Join(tmpdir, "debian.tar.xz")
	if err := command.Run(ctx, "curl", "-fL", "-o", tarFile, DebianTarURL); err != nil {
		return fmt.Errorf("cannot download Debian: %w", err)
	}

	// Extract tar
	if err := command.Run(ctx, "tar", "-xJf", tarFile, "-C", tmpdir); err != nil {
		return fmt.Errorf("cannot extract Debian tar: %w", err)
	}

	// Find the .raw disk image
	rawImage, err := findFile(tmpdir, "*.raw")
	if err != nil {
		// Fallback to .qcow2
		rawImage, err = findFile(tmpdir, "*.qcow2")
		if err != nil {
			return fmt.Errorf("no .raw or .qcow2 image found in tarball: %w", err)
		}
	}

	slog.InfoContext(ctx, "Found disk image", "path", rawImage)

	// Use systemd-dissect to mount and extract root filesystem
	extractDir, err := os.MkdirTemp(info.Mount, "debian-extract.*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(extractDir)

	if err := command.Run(ctx, "systemd-dissect", "--mount", "--mkdir", rawImage, extractDir); err != nil {
		return fmt.Errorf("systemd-dissect mount failed: %w", err)
	}
	defer func() { _ = command.Run(ctx, "systemd-dissect", "--umount", extractDir) }()

	// Create base subvolume and copy
	if err := storage.CreateSubvolume(ctx, info.Mount, "base-debian"); err != nil {
		return err
	}

	basePath := filepath.Join(info.Mount, "base-debian")
	if err := command.Run(ctx, "cp", "-a", "--reflink=auto", extractDir+"/.", basePath+"/"); err != nil {
		return err
	}

	// Clean up machine-id and hostname
	os.Remove(filepath.Join(basePath, "etc/machine-id"))
	os.Remove(filepath.Join(basePath, "etc/hostname"))

	// Mark read-only
	if err := storage.SetReadOnly(ctx, basePath); err != nil {
		return err
	}

	slog.InfoContext(ctx, "Base subvolume ready", "path", basePath)
	return nil
}

// finalizeImage cleans up the container, measures its size, updates config, and marks it as read-only.
func finalizeImage(ctx context.Context, buildSubvol string, cfg Config) error {
	// Check if this is an incremental build (base had iiab-complete flag)
	isIncremental := checkIncrementalBuild(buildSubvol)

	buildType := "fresh"
	if isIncremental {
		buildType = "incremental"
	}

	// Clean up per-build artifacts
	if err := os.WriteFile(filepath.Join(buildSubvol, "etc/machine-id"), []byte("uninitialized\n"), 0o644); err != nil {
		return err
	}
	os.Remove(filepath.Join(buildSubvol, "etc/iiab/uuid"))
	os.Remove(filepath.Join(buildSubvol, "var/swap"))

	// Measure final size
	usedMB := 0
	if out, err := command.Output(ctx, "du", "-sm", buildSubvol); err == nil {
		if _, err := fmt.Sscanf(out, "%d", &usedMB); err != nil {
			slog.DebugContext(ctx, "failed to parse du output", "output", out, "error", err)
		}
	}

	// Measure unique size via btrfs filesystem du
	uniqueMB := usedMB
	if out, err := command.Output(ctx, "btrfs", "filesystem", "du", "-s", "--raw", buildSubvol); err == nil {
		lines := strings.Split(strings.TrimSpace(out), "\n")
		// The output has a header line, and the data line is typically the second.
		// Fallback to the last line.
		if len(lines) >= 2 {
			lastLine := lines[len(lines)-1]
			fields := strings.Fields(lastLine)
			// fields should be: Total Exclusive SetShared Filename
			if len(fields) >= 2 {
				var exclusiveBytes int64
				if _, err := fmt.Sscanf(fields[1], "%d", &exclusiveBytes); err == nil {
					uniqueMB = int(exclusiveBytes / (1024 * 1024))
				}
			}
		}
	}

	slog.InfoContext(ctx, "Final image size calculated", "used_mb", usedMB, "unique_mb", uniqueMB)

	// Update config with actual sizes
	if cfg.StateDir != "" {
		if demo, err := config.Read(ctx, cfg.StateDir, cfg.Name); err == nil {
			demo.ImageSizeMB = usedMB
			demo.UniqueSizeMB = uniqueMB
			_ = demo.Write(cfg.StateDir)
		}
	}

	// Add metadata
	metadata := fmt.Sprintf(
		"%s\nBuild date: %s\nBranch: %s\nRepo: %s\nBuild type: %s\nVolatile: %s\nSize: %dMB\nUnique: %dMB\n",
		cfg.Name,
		time.Now().UTC().Format(time.RFC3339),
		cfg.Branch,
		cfg.Repo,
		buildType,
		cfg.VolatileMode,
		usedMB,
		uniqueMB,
	)
	metadataFile := filepath.Join(buildSubvol, ".iiab-image")
	f, err := os.OpenFile(metadataFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(metadata)
	if err != nil {
		return err
	}

	// Reset machine-id and other per-build artifacts
	if err := os.WriteFile(filepath.Join(buildSubvol, "etc/machine-id"), []byte("uninitialized\n"), 0o644); err != nil {
		return err
	}
	os.Remove(filepath.Join(buildSubvol, "etc/iiab/uuid"))
	os.Remove(filepath.Join(buildSubvol, "var/swap"))

	// Mark as read-only
	if err := storage.SetReadOnly(ctx, buildSubvol); err != nil {
		return err
	}

	slog.InfoContext(ctx, "Image finalized and marked read-only")
	return nil
}

// checkIncrementalBuild checks if the base image had iiab-complete flag.
func checkIncrementalBuild(buildSubvol string) bool {
	// Check if iiab-complete flag exists (was copied from base)
	flagPath := filepath.Join(buildSubvol, "etc/iiab/install-flags/iiab-complete")
	return state.FileExists(flagPath)
}

// createVMSymlink creates a symlink from machinesDir/<name> to the build subvolume.
// This is called early in the build process so that build errors are visible in machinectl list.
func createVMSymlink(name, buildSubvol, machinesDir string) error {
	if err := os.MkdirAll(machinesDir, 0o755); err != nil {
		return err
	}

	symlink := filepath.Join(machinesDir, name)
	os.Remove(symlink)
	return os.Symlink(buildSubvol, symlink)
}

// findFile searches for a file matching the glob pattern in dir (recursive).
func findFile(dir, pattern string) (string, error) {
	var found string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		matched, _ := filepath.Match(pattern, filepath.Base(path))
		if matched {
			found = path
			return filepath.SkipDir
		}
		return nil
	})
	if found == "" {
		return "", fmt.Errorf("no file matching %s found in %s", pattern, dir)
	}
	return found, nil
}
