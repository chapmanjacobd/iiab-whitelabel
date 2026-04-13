// Package build handles IIAB container image building.
package build

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/config"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/storage"
)

// Config holds the parameters for a build.
type Config struct {
	Name         string
	Repo         string
	Branch       string
	ImageSizeMB  int
	VolatileMode string
	IP           string
	LocalVars    string
	BuildOnDisk  bool
	SkipInstall  bool
	BaseName     string
	Timeout      time.Duration // Optional timeout for the build (0 = use default 2h+10m)
	Stdout       io.Writer     // Optional: also write build output to this writer
	System       *config.System
	StateDir     string
}

// Run executes the full IIAB container build.
func Run(ctx context.Context, cfg Config) error {
	repo := cfg.Repo

	// Step 1: Setup storage
	info, err := storage.SetupStorage(ctx, cfg.BuildOnDisk)
	if err != nil {
		return fmt.Errorf("cannot setup storage: %w", err)
	}

	// Grow storage if needed
	if err := storage.GrowStorage(ctx, cfg.BuildOnDisk, cfg.ImageSizeMB); err != nil {
		return fmt.Errorf("cannot grow storage: %w", err)
	}

	buildsDir := filepath.Join(info.Mount, "builds")
	if err := os.MkdirAll(buildsDir, 0o755); err != nil {
		return err
	}

	// Step 2: Prepare base subvolume
	if err := ensureBaseSubvolume(ctx, info, cfg.BuildOnDisk, cfg.BaseName); err != nil {
		return fmt.Errorf("cannot prepare base subvolume: %w", err)
	}

	// Step 3: Create CoW snapshot for this build
	buildSubvol := filepath.Join(buildsDir, cfg.Name)
	if storage.SubvolumeExists(ctx, info.Mount, "builds/"+cfg.Name) {
		return fmt.Errorf("build subvolume %s already exists", cfg.Name)
	}

	baseSubvol := "base-debian"
	if cfg.BaseName != "" {
		baseSubvol = cfg.BaseName
	}

	basePath := filepath.Join(info.Mount, "builds", baseSubvol)
	if !state.FileExists(basePath) {
		// Fallback to storage root
		basePath = filepath.Join(info.Mount, baseSubvol)
	}

	if err := storage.Snapshot(ctx, basePath, buildSubvol); err != nil {
		return fmt.Errorf("cannot create snapshot: %w", err)
	}

	// Create early VM symlink so build errors are visible in machinectl list
	if err := createVMSymlink(cfg.Name, buildSubvol, cfg.System.MachinesDir); err != nil {
		slog.WarnContext(ctx, "Failed to create early VM symlink (non-fatal)", "demo", cfg.Name, "error", err)
	}

	// Step 4: Prepare container rootfs
	if err := prepareRootfs(ctx, buildSubvol, cfg, repo); err != nil {
		return fmt.Errorf("cannot prepare rootfs: %w", err)
	}

	// Step 5: Run IIAB installer in the build subvolume
	// Create context from timeout (default 2h+10m if not specified)
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = expectTimeout + 10*time.Minute
	}
	buildCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := runIIABInstaller(buildCtx, buildSubvol, cfg); err != nil {
		return fmt.Errorf("IIAB installer failed: %w", err)
	}

	// Step 6: Clean up and finalize
	if err := finalizeImage(ctx, buildSubvol, cfg); err != nil {
		return fmt.Errorf("cannot finalize image: %w", err)
	}

	// Step 7: Register the image
	if err := createVMSymlink(cfg.Name, buildSubvol, cfg.System.MachinesDir); err != nil {
		return fmt.Errorf("cannot register image: %w", err)
	}

	// Generate nspawn service files
	if err := GenerateNspawn(ctx, cfg.Name, cfg.IP, cfg.VolatileMode, cfg.System); err != nil {
		return fmt.Errorf("cannot generate nspawn files: %w", err)
	}

	slog.InfoContext(ctx, "Build complete", "name", cfg.Name, "subvolume", buildSubvol)
	return nil
}
