package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/build"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/config"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/lock"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/logging"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/network"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/storage"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/sys"
)

// RAMSafetyBufferMB is the amount of RAM to keep free for the host system (2GB).
const RAMSafetyBufferMB = 2048

// BuildCmd builds a new demo container.
type BuildCmd struct {
	Name        string `help:"Demo name"                                                              arg:""`
	Branch      string `help:"Git ref (branch, tag, or PR head)"                                             default:"master"`
	Repo        string `help:"Source repository for IIAB"                                                    default:"https://github.com/iiab/iiab.git"`
	Description string `help:"Human-readable description"                                                    default:""`
	LocalVars   string `help:"Path to IIAB configuration variables"                                          default:"vars/local_vars_small.yml"`
	Size        int    `help:"Virtual disk size in MB"                                                       default:"15000"`
	Volatile    string `help:"Volatile mode (no, overlay, state, yes)"                                       default:"overlay"`
	Start       bool   `help:"Start the demo after build succeeds"                                           default:"false"`
	Cleanup     bool   `help:"Delete failed build snapshots immediately on failure"                          default:"false"`
	Base        string `help:"Build on top of an existing base subvolume"                                    default:""`
	Wildcard    bool   `help:"Use as wildcard for unknown subdomains"                                        default:"false"`
	Disk        bool   `help:"Build on disk instead of tmpfs"                                                default:"false"`
	SkipInstall bool   `help:"Skip the IIAB installer (useful for base image creation or testing)"           default:"false"`
	Subdomain   string `help:"Override the subdomain (must be a valid subdomain, not auto-sanitized)"        default:""                                 name:"subdomain"`
}

// Run executes the build command.
func (c *BuildCmd) Run(ctx context.Context, globals *GlobalOptions) error {
	if err := c.validateAndPrepare(globals); err != nil {
		return err
	}

	demoDir := state.DemoDir(globals.StateDir, c.Name)

	// Cap lock timeout to 10 minutes maximum (prevent infinite waits)
	// Acquire lock for check + allocate
	lk, lkErr := acquireLongLock(ctx, globals)
	if lkErr != nil {
		return lkErr
	}

	// Check if demo already exists
	if err := c.checkExisting(ctx, globals, demoDir); err != nil {
		_ = lk.Release()
		return err
	}

	// Allocate IP
	ip, err := network.AllocateNextIP(globals.StateDir)
	if err != nil {
		_ = lk.Release()
		return err
	}

	// Create demo directory and write initial config
	if err := c.setupDemoState(ctx, globals, demoDir, ip); err != nil {
		_ = lk.Release()
		return err
	}

	defer func() { _ = lk.Release() }()
	return runBuild(ctx, globals, c.Name, c.Start, c.Cleanup)
}

func (c *BuildCmd) validateAndPrepare(globals *GlobalOptions) error {
	if err := ensureRoot(); err != nil {
		return err
	}

	if err := config.ValidateName(c.Name); err != nil {
		return err
	}

	if c.Subdomain != "" {
		if state.SanitizeSubdomain(c.Subdomain) != c.Subdomain {
			return fmt.Errorf(
				"subdomain %q is not valid: must contain only lowercase letters, numbers, and hyphens (no leading/trailing hyphens)",
				c.Subdomain,
			)
		}
	}

	if state.FileExists(c.Repo) {
		abs, err := filepath.Abs(c.Repo)
		if err == nil {
			c.Repo = abs
		}
	} else if !strings.HasPrefix(c.Repo, "https://") &&
		!strings.HasPrefix(c.Repo, "http://") &&
		!strings.HasPrefix(c.Repo, "git@") {

		c.Repo = "https://" + c.Repo
	}

	return ensureStateDirs(globals)
}

func (c *BuildCmd) checkExisting(ctx context.Context, globals *GlobalOptions, demoDir string) error {
	if !state.FileExists(demoDir) {
		return nil
	}

	statusPath := filepath.Join(demoDir, "status")
	status := "unknown"
	if data, err := os.ReadFile(statusPath); err == nil {
		status = string(data)
	}

	switch status {
	case "pending", "building":
		if lock.IsBuildInProgress(globals.StateDir, c.Name) {
			return fmt.Errorf("demo '%s' already has an active build in progress (status: %s)", c.Name, status)
		}
		slog.WarnContext(ctx, "Interrupted build, cleaning up...", "demo", c.Name, "status", status)
		return cleanupFailedBuild(ctx, globals, c.Name)
	case "failed":
		slog.WarnContext(ctx, "Previously failed build, cleaning up...", "demo", c.Name)
		return cleanupFailedBuild(ctx, globals, c.Name)
	default:
		return fmt.Errorf(
			"demo '%s' already exists (status: %s). Use 'democtl rebuild' to recreate",
			c.Name,
			status,
		)
	}
}

func (c *BuildCmd) setupDemoState(ctx context.Context, globals *GlobalOptions, demoDir, ip string) error {
	if err := os.MkdirAll(demoDir, 0o755); err != nil {
		return err
	}

	demo := &config.Demo{
		Name:         c.Name,
		Repo:         c.Repo,
		Branch:       c.Branch,
		ImageSizeMB:  c.Size,
		VolatileMode: c.Volatile,
		BuildOnDisk:  c.Disk,
		SkipInstall:  c.SkipInstall,
		LocalVars:    c.LocalVars,
		Wildcard:     c.Wildcard,
		Description:  c.Description,
		BaseName:     c.Base,
		Subdomain:    c.Subdomain,
		Status:       "pending",
		IP:           ip,
	}

	if err := demo.Write(globals.StateDir); err != nil {
		return err
	}
	if err := state.WriteIP(globals.StateDir, c.Name, ip); err != nil {
		return err
	}
	if err := state.WriteFile(filepath.Join(demoDir, "build.log"), "", 0o644); err != nil {
		return err
	}
	if err := config.WriteStatus(globals.StateDir, c.Name, "pending"); err != nil {
		return err
	}

	sub := c.Subdomain
	if sub == "" {
		sub = state.SanitizeSubdomain(c.Name)
	}
	slog.InfoContext(ctx, "Demo queued", "demo", c.Name, "subdomain", sub, "ip", ip)
	return nil
}

// runBuild executes the actual container build.
//
//nolint:contextcheck // buildCtx intentionally uses context.Background() to survive parent cancellation
func runBuild(ctx context.Context, globals *GlobalOptions, name string, startAfter, cleanup bool) error {
	demo, err := config.Read(ctx, globals.StateDir, name)
	if err != nil {
		return fmt.Errorf("cannot read demo config: %w", err)
	}

	buildOnDisk := determineStorageBackend(ctx, demo)

	// Create a context with a 2-hour timeout for the build.
	// Use context.Background() to isolate the build from parent context
	// cancellation (e.g., if the parent CLI process is terminated).
	buildCtx, buildCancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer buildCancel()

	// Update status to building
	if err = config.WriteStatus(globals.StateDir, name, "building"); err != nil {
		return err
	}

	multiWriter, logFile, err := setupBuildLogging(globals.StateDir, name)
	if err != nil {
		return err
	}
	defer logFile.Close()

	slog.InfoContext(
		buildCtx,
		"Starting build",
		"demo",
		name,
		"repo",
		demo.Repo,
		"branch",
		demo.Branch,
		"size_mb",
		demo.ImageSizeMB,
	)

	// Use the Go-based build package with a 2-hour deadline
	buildCfg := build.Config{
		Name:         demo.Name,
		Repo:         demo.Repo,
		Branch:       demo.Branch,
		ImageSizeMB:  demo.ImageSizeMB,
		VolatileMode: demo.VolatileMode,
		IP:           demo.IP,
		LocalVars:    demo.LocalVars,
		BuildOnDisk:  buildOnDisk,
		SkipInstall:  demo.SkipInstall,
		BaseName:     demo.BaseName,
		Timeout:      2 * time.Hour,
		Stdout:       multiWriter,
		System:       globals.System,
		StateDir:     globals.StateDir,
	}

	if err := build.Run(buildCtx, buildCfg); err != nil {
		// Clean up the build PID file
		lock.RemoveBuildPID(globals.StateDir, name)

		if writeErr := config.WriteStatus(globals.StateDir, name, "failed"); writeErr != nil {
			slog.WarnContext(buildCtx, "Failed to update status to 'failed'", "demo", name, "error", writeErr)
		}
		// Clean up failed build snapshots if requested
		if cleanup {
			slog.InfoContext(buildCtx, "Cleaning up failed build snapshots", "demo", name)
			if cleanupErr := cleanupFailedBuild(buildCtx, globals, name); cleanupErr != nil {
				slog.WarnContext(buildCtx, "Failed to cleanup failed build", "demo", name, "error", cleanupErr)
			}
		}
		return fmt.Errorf("build failed: %w", err)
	}

	slog.InfoContext(buildCtx, "Build completed successfully", "demo", name)

	// Clean up the build PID file (it was written during fork)
	lock.RemoveBuildPID(globals.StateDir, name)

	// Start the container if requested
	if startAfter {
		return startDemo(buildCtx, globals, name)
	}

	return config.WriteStatus(globals.StateDir, name, "stopped")
}

func setupBuildLogging(stateDir, name string) (io.Writer, *os.File, error) {
	demoDir := state.DemoDir(stateDir, name)
	logFile, err := os.OpenFile(filepath.Join(demoDir, "build.log"), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot open build.log: %w", err)
	}

	multiWriter := io.MultiWriter(os.Stderr, logFile)

	// Configure logger to write to both
	verbose := os.Getenv("DEMOCTL_VERBOSE")
	if verbose == "" {
		verbose = os.Getenv("IIAB_VERBOSE")
	}
	var handler slog.Handler
	if verbose == "" {
		handler = logging.NewPlainHandler(multiWriter, slog.LevelInfo)
	} else {
		level := slog.LevelDebug
		if verbose == "2" {
			level = slog.Level(-8)
		}
		handler = slog.NewTextHandler(multiWriter, &slog.HandlerOptions{
			Level:     level,
			AddSource: verbose == "2",
		})
	}
	slog.SetDefault(slog.New(handler))
	return multiWriter, logFile, nil
}

func determineStorageBackend(ctx context.Context, demo *config.Demo) bool {
	if demo.BuildOnDisk {
		return true
	}

	// Check if RAMFS is already mounted and what its size is
	ramfsSize := 0
	if sys.Mountpoint(ctx, storage.RAMFSRoot) {
		if size, err := sys.GetMountSizeMB(ctx, storage.RAMFSRoot); err == nil {
			ramfsSize = size
		}
	}

	avail, err := sys.GetAvailableMemoryMB()
	if err != nil {
		slog.WarnContext(ctx, "Failed to check available memory", "error", err)
		return false // Default to RAM if check fails, SetupStorage will handle errors
	}

	needed := demo.ImageSizeMB + RAMSafetyBufferMB
	// If already mounted, we only need to account for the difference if it needs growing
	if ramfsSize > 0 {
		if ramfsSize >= demo.ImageSizeMB {
			return false // Already large enough
		}
		// We need to grow it, so we need more RAM
		needed = (demo.ImageSizeMB - ramfsSize) + RAMSafetyBufferMB
	}

	if avail < needed {
		slog.InfoContext(
			ctx,
			"Insufficient RAM for build, falling back to disk",
			"available_mb",
			avail,
			"needed_mb",
			needed,
		)
		return true
	}

	return false
}

// RebuildCmd deletes and re-builds demo(s).
type RebuildCmd struct {
	Names []string `help:"Demo name(s) to rebuild"        arg:"" optional:""`
	All   bool     `help:"Rebuild all demos from scratch"                    default:"false"`
}

// Run executes the rebuild command.
func (c *RebuildCmd) Run(ctx context.Context, globals *GlobalOptions) error {
	if err := ensureRoot(); err != nil {
		return err
	}

	names := c.Names
	if c.All {
		// Acquire lock for the entire factory reset operation
		lk, lkErr := acquireLongLock(ctx, globals)
		if lkErr != nil {
			return lkErr
		}
		defer func() { _ = lk.Release() }()

		slog.InfoContext(ctx, "Rebuilding all demos from scratch (factory reset)")

		var err error
		names, err = config.List(globals.StateDir)
		if err != nil {
			return err
		}

		// Delete each demo state and stop containers
		for _, name := range names {
			if err := deleteDemo(ctx, globals, name); err != nil {
				slog.ErrorContext(ctx, "Delete failed during factory reset", "demo", name, "error", err)
			}
		}

		// Teardown storage (unmounts and removes btrfs files)
		if err := storage.TeardownStorage(ctx); err != nil {
			slog.WarnContext(ctx, "Storage teardown had issues", "error", err)
		}

		// Reset resource counters
		_ = config.WriteResources(globals.StateDir, &config.Resources{
			DiskTotalMB:     0,
			DiskAllocatedMB: 0,
		})
	}

	if len(names) == 0 {
		return errors.New("no demos specified")
	}

	for _, name := range names {
		demo, err := config.Read(ctx, globals.StateDir, name)
		if err != nil {
			slog.ErrorContext(ctx, "Cannot read config", "demo", name, "error", err)
			continue
		}

		if !c.All {
			if err := deleteDemo(ctx, globals, name); err != nil {
				slog.ErrorContext(ctx, "Delete failed", "demo", name, "error", err)
				continue
			}
		}

		slog.InfoContext(
			ctx,
			"Rebuilding",
			"demo",
			name,
			"repo",
			demo.Repo,
			"branch",
			demo.Branch,
			"size_mb",
			demo.ImageSizeMB,
		)
		// Re-build with same parameters
		buildCmd := BuildCmd{
			Name:        demo.Name,
			Branch:      demo.Branch,
			Repo:        demo.Repo,
			Description: demo.Description,
			LocalVars:   demo.LocalVars,
			Size:        demo.ImageSizeMB,
			Volatile:    demo.VolatileMode,
			Start:       false,
			Cleanup:     demo.CleanupFailed,
			Base:        demo.BaseName,
			Wildcard:    demo.Wildcard,
			Disk:        demo.BuildOnDisk,
			SkipInstall: demo.SkipInstall,
			Subdomain:   demo.Subdomain,
		}
		if err := buildCmd.Run(ctx, globals); err != nil {
			slog.ErrorContext(ctx, "Rebuild failed", "demo", name, "error", err)
		} else {
			slog.InfoContext(ctx, "Rebuilt successfully", "demo", name)
		}
	}
	return nil
}
