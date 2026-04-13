// Package cmd defines all democtl CLI commands using Kong.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/alecthomas/kong"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/config"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/lock"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/logging"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/network"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/nginx"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/sys"
)

// CLI is the root Kong command structure.
type CLI struct {
	GlobalOptions

	Init      InitCmd      `help:"Initialize host for IIAB demos"                 cmd:""`
	Build     BuildCmd     `help:"Build a new demo"                               cmd:""`
	Delete    DeleteCmd    `help:"Stop and delete demo(s)"                        cmd:""`
	List      ListCmd      `help:"Show all demos and resource usage"              cmd:""`
	Status    StatusCmd    `help:"Detailed status of a demo"                      cmd:""`
	Start     StartCmd     `help:"Start stopped demo(s)"                          cmd:""`
	Stop      StopCmd      `help:"Stop a running demo(s)"                         cmd:""`
	Restart   RestartCmd   `help:"Restart running demo(s)"                        cmd:""`
	Settle    SettleCmd    `help:"Wait until all demos reach a settled state"     cmd:""`
	Logs      LogsCmd      `help:"Show build log or container journal"            cmd:""`
	Shell     ShellCmd     `help:"Open a shell in a running container"            cmd:""`
	Cleanup   CleanupCmd   `help:"Clean up failed builds and orphaned subvolumes" cmd:""`
	Rebuild   RebuildCmd   `help:"Delete and re-build demo(s)"                    cmd:""`
	Reload    ReloadCmd    `help:"Regenerate nginx config from active demos"      cmd:""`
	Reconcile ReconcileCmd `help:"Fix resource counter drift and check ghost IPs" cmd:""`
	Certs     CertsCmd     `help:"Manage TLS certificates for demos"              cmd:""`
}

// GlobalOptions holds the parsed Kong context and shared state.
type GlobalOptions struct {
	StateDir string         `help:"Override state directory" default:"/var/lib/iiab-demos"`
	System   *config.System `                                                              kong:"-"`
}

// Run parses the CLI and executes the command. Returns exit code.
func Run(ctx context.Context) int {
	// Configure logging based on environment variable for test verbosity
	configureLogging()

	cli := CLI{
		GlobalOptions: GlobalOptions{
			System: config.NewSystem(),
		},
	}

	parser := kong.Must(&cli,
		kong.Name("democtl"),
		kong.Description("Manage IIAB whitelabel demo containers."),
		kong.UsageOnError(),
		kong.ConfigureHelp(kong.HelpOptions{
			Compact: true,
			Summary: true,
		}),
		kong.BindTo(ctx, (*context.Context)(nil)),
	)

	kongCtx, err := parser.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	if err := kongCtx.Run(ctx, &cli.GlobalOptions); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

// configureLogging sets up slog based on DEMOCTL_VERBOSE environment variable.
// Levels: "debug" or "1" for DEBUG, "2" for TRACE-level detail.
func configureLogging() {
	verbose := os.Getenv("DEMOCTL_VERBOSE")
	if verbose == "" {
		verbose = os.Getenv("IIAB_VERBOSE")
	}
	if verbose == "" {
		if os.Getenv("CI") != "" {
			verbose = "2"
		}
	}

	if verbose == "" {
		// Default: INFO level with compact plain output
		slog.SetDefault(slog.New(logging.NewPlainHandler(os.Stderr, slog.LevelInfo)))
		return
	}

	// Verbose mode: DEBUG with source
	level := slog.LevelDebug
	if verbose == "2" {
		level = slog.Level(-8) // trace-level
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level:     level,
		AddSource: verbose == "2",
	})))
}

// ensureRoot re-executes the current binary via sudo if not running as root.
// It uses [syscall.Exec] to replace the current process, similar to os.execv().
// Resolves the binary path to handle relative paths and $PATH lookups correctly under sudo.
func ensureRoot() error {
	if os.Geteuid() == 0 {
		return nil
	}
	binaryPath, err := exec.LookPath(os.Args[0])
	if err != nil {
		binaryPath = os.Args[0] // Fallback to original if lookup fails
	}
	sudoPath, err := exec.LookPath("sudo")
	if err != nil {
		return fmt.Errorf("sudo command not found in $PATH: %w", err)
	}
	// Use sys.Exec to replace the current process with the sudo process.
	// This is equivalent to os.execv() in Python - the current process image
	// is replaced, so we never return here on success.
	args := append([]string{"sudo", "-E", binaryPath}, os.Args[1:]...)
	return sys.Exec(sudoPath, args, os.Environ())
}

// acquireLongLock acquires the global flock with a long timeout (for critical sections).
func acquireLongLock(ctx context.Context, globals *GlobalOptions) (*lock.Lock, error) {
	lockFile := filepath.Join(globals.StateDir, ".democtl.lock")
	return lock.AcquireLong(ctx, lockFile)
}

// ensureStateDirs creates the required state directory structure.
func ensureStateDirs(globals *GlobalOptions) error {
	dirs := []string{
		globals.StateDir,
		state.ActiveDir(globals.StateDir),
		globals.System.MachinesDir,
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("cannot create directory %s: %w", d, err)
		}
	}
	return nil
}

func startDemo(ctx context.Context, globals *GlobalOptions, name string) error {
	slog.InfoContext(ctx, "Starting...", "demo", name)

	// Ensure bridge exists (might have been cleaned up or never created)
	if err := network.SetupBridge(ctx, globals.System); err != nil {
		slog.WarnContext(ctx, "Bridge setup had issues (may already exist)", "demo", name, "error", err)
	}

	tctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// Reload systemd to pick up any new or changed service files
	if err := exec.CommandContext(tctx, "systemctl", "daemon-reload").Run(); err != nil {
		slog.WarnContext(ctx, "daemon-reload failed", "demo", name, "error", err)
	}

	// Enable and start systemd-nspawn service
	cmd := exec.CommandContext(tctx, "systemctl", "enable", "--now", fmt.Sprintf("systemd-nspawn@%s.service", name))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cannot start container: %w", err)
	}

	// Wait for container to reach running state
	if err := waitForContainerRunning(ctx, name); err != nil {
		// Log service state for debugging
		slog.ErrorContext(ctx, "Container failed to start", "demo", name)
		_ = exec.CommandContext(ctx, "systemctl", "show",
			fmt.Sprintf("systemd-nspawn@%s.service", name),
			"--property=ActiveState,SubState,ExecMainStatus").Run()
		return fmt.Errorf("container did not become active: %w", err)
	}

	// Regenerate nginx config
	if err := nginx.Generate(ctx, globals.StateDir); err != nil {
		return err
	}

	// Re-apply isolation rules
	extIF, err := network.DetectExternalInterface(ctx)
	if err != nil {
		slog.WarnContext(ctx, "Could not detect external interface for isolation", "demo", name, "error", err)
	} else if err := network.AddContainerIsolation(ctx, extIF); err != nil {
		slog.WarnContext(ctx, "Isolation setup failed", "demo", name, "error", err)
	}

	return config.WriteStatus(globals.StateDir, name, "running")
}

// waitForContainerRunning polls the systemd service until it reaches "running" substate.
// Returns an error if the container fails to start within 60 seconds or enters a failure state.
func waitForContainerRunning(ctx context.Context, name string) error {
	serviceName := fmt.Sprintf("systemd-nspawn@%s.service", name)
	pollMax := 60
	pollCount := 0
	nonRunningCount := 0

	// Give the container a moment to initialize
	time.Sleep(3 * time.Second)

	for pollCount < pollMax {
		tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		out, err := exec.CommandContext(tctx, "systemctl", "show",
			serviceName, "--property=SubState", "--value").Output()
		cancel()
		if err != nil {
			pollCount++
			time.Sleep(1 * time.Second)
			continue
		}

		substate := strings.TrimSpace(string(out))
		if substate == "running" {
			return nil
		}

		nonRunningCount++
		if nonRunningCount >= 3 && (substate == "auto-restart" || substate == "failed") {
			return fmt.Errorf("container entered %s state", substate)
		}

		pollCount++
		time.Sleep(1 * time.Second)
	}

	return errors.New("timeout waiting for container to reach running state")
}

func stopDemo(ctx context.Context, globals *GlobalOptions, name string) error {
	slog.InfoContext(ctx, "Stopping...", "demo", name)
	tctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(tctx, "systemctl", "stop", fmt.Sprintf("systemd-nspawn@%s.service", name))
	if err := cmd.Run(); err != nil {
		exitCode := 0
		exitErr := &exec.ExitError{}
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		// Exit code 5 means unit not found, which is ok (container never existed or already removed)
		if exitCode != 5 {
			return fmt.Errorf("systemctl stop failed: %w", err)
		}
		slog.WarnContext(ctx, "Container not found, might already be stopped", "demo", name)
	}
	return config.WriteStatus(globals.StateDir, name, "stopped")
}
