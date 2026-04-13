package build

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/command"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/network"
)

// Pre-compiled regexes for build output parsing.
var (
	reBuildFailed   = regexp.MustCompile(`failed=[1-9][0-9]*`)
	reBuildExitCode = regexp.MustCompile(`BUILD_EXIT_CODE:([0-9]+)`)
	reExitCode      = regexp.MustCompile(`EXIT_CODE:([0-9]+)`)
	rePrompt        = regexp.MustCompile(`(?m)#\s*$`) // (?m) makes $ match end-of-line, not end-of-string
)

// Timeout used for individual expect operations.
const stepTimeout = 7200 * time.Second

// runIIABInstaller runs the IIAB installer inside the container using a buffered
// PTY expect loop for automated interaction with systemd-nspawn.
func runIIABInstaller(buildCtx context.Context, buildSubvol string, cfg Config) error {
	// Verify networking config exists
	networkFile := filepath.Join(buildSubvol, "etc/systemd/network/99-iiab-host0.network")
	if _, err := os.Stat(networkFile); err != nil {
		return fmt.Errorf("container network config not found at %s: %w", networkFile, err)
	}
	slog.InfoContext(buildCtx, "Verified container network config exists", "path", networkFile)

	// Setup bridge networking
	if err := network.SetupBridge(buildCtx, cfg.System); err != nil {
		return fmt.Errorf("bridge setup failed: %w", err)
	}

	// Enable IPv4 forwarding
	if err := command.Run(
		buildCtx,
		"sysctl",
		"-w",
		"net.ipv4.ip_forward=1",
	); err != nil {
		return fmt.Errorf("failed to enable IP forwarding: %w", err)
	}

	// Setup NAT via nftables
	if err := network.SetupNAT(buildCtx, cfg.System); err != nil {
		return fmt.Errorf("NAT setup failed: %w", err)
	}

	// Ensure /root directory exists
	rootDir := filepath.Join(buildSubvol, "root")
	if err := os.MkdirAll(rootDir, 0o700); err != nil {
		return fmt.Errorf("cannot create /root in container: %w", err)
	}

	// Run systemd-nspawn with buffered expect loop for automation
	return runNspawnWithPTY(buildCtx, cfg.Name, buildSubvol, cfg)
}

// runNspawnWithPTY starts systemd-nspawn and automates the installation via a
// buffered PTY expect loop (single read goroutine, pattern-scan-once-per-check).
func runNspawnWithPTY(buildCtx context.Context, name, buildSubvol string, cfg Config) error {
	// Create the buffered PTY loop
	el, err := NewPTYLoop(PTYLoopConfig{Stdout: cfg.Stdout})
	if err != nil {
		return fmt.Errorf("failed to create pty loop: %w", err)
	}
	defer el.Close()

	// Start systemd-nspawn with boot, wired to the PTY slave side
	cmd := exec.CommandContext(
		buildCtx,
		"systemd-nspawn",
		"-q",
		"--network-bridge="+network.BridgeName,
		"-D", buildSubvol,
		"-M", name,
		"--boot",
	)
	if err := el.StartCommand(cmd); err != nil {
		return fmt.Errorf("failed to start nspawn: %w", err)
	}

	// Run the expect automation in a goroutine, report errors via channel
	errCh := make(chan error, 1)
	go func() {
		defer close(errCh)
		if err := runExpectAutomation(buildCtx, el, buildSubvol, cfg); err != nil {
			errCh <- err
		}
	}()

	// Wait for process exit
	slog.DebugContext(buildCtx, "Waiting for nspawn process to exit")
	cmdErr := cmd.Wait()
	slog.DebugContext(buildCtx, "nspawn process exited", "error", cmdErr)

	// Always wait for expect goroutine to finish to avoid goroutine leak
	slog.DebugContext(buildCtx, "Waiting for expect goroutine to finish")
	expectErr := <-errCh
	slog.DebugContext(buildCtx, "expect goroutine finished", "error", expectErr)

	if cmdErr != nil {
		return fmt.Errorf("nspawn process exited with error: %w", cmdErr)
	}
	if expectErr != nil {
		return expectErr
	}

	slog.InfoContext(buildCtx, "Container shutdown complete")
	if !cfg.SkipInstall {
		slog.InfoContext(buildCtx, "IIAB install complete")
	} else {
		slog.InfoContext(buildCtx, "Skip-install boot complete")
	}
	return nil
}

// runExpectAutomation handles all the interactive expect/send logic.
func runExpectAutomation(ctx context.Context, el *PTYLoop, buildSubvol string, cfg Config) error {
	slog.InfoContext(ctx, "Starting interactive build automation")

	if err := loginAndPrepare(el, cfg.SkipInstall); err != nil {
		return err
	}

	if cfg.SkipInstall {
		return finalizeBuild(ctx, el)
	}

	// Detect incremental vs fresh build from the host.
	// A build is incremental if:
	// 1. The iiab-complete flag exists (previous install completed), OR
	// 2. STAGE=9 exists in iiab.env (base image already completed all stages).
	// In both cases, we need to reset STAGE to allow iiab-configure to run.
	isIncremental := false
	if _, err := os.Stat(filepath.Join(buildSubvol, "etc/iiab/install-flags/iiab-complete")); err == nil {
		isIncremental = true
	} else if stageEnv, err := os.ReadFile(filepath.Join(buildSubvol, "etc/iiab/iiab.env")); err == nil {
		if strings.Contains(string(stageEnv), "STAGE=9") {
			isIncremental = true
		}
	}

	// Run system updates one by one
	if err := runStep(el, "apt update", "failed to run apt update"); err != nil {
		return err
	}
	if err := runStep(
		el,
		"apt dist-upgrade -y -o Dpkg::Progress-Fancy=0 -o Dpkg::Options::=\"--force-confdef\" -o Dpkg::Options::=\"--force-confold\" -qq",
		"failed to run apt upgrade",
	); err != nil {
		return err
	}

	buildType := "fresh"
	var finalInstallCmd string
	if isIncremental {
		buildType = "incremental"
		slog.InfoContext(ctx, "Incremental build detected from host")

		if err := runStep(
			el,
			"rm -f /etc/iiab/install-flags/iiab-complete",
			"failed to remove complete flag",
		); err != nil {
			return err
		}
		if err := runStep(
			el,
			"sed -i 's/STAGE=.*/STAGE=3/' /etc/iiab/iiab.env 2>/dev/null",
			"failed to update STAGE in iiab.env",
		); err != nil {
			return err
		}
		if err := runStep(el, "cd /opt/iiab/iiab", "failed to change directory to /opt/iiab/iiab"); err != nil {
			return err
		}
		finalInstallCmd = "./iiab-configure"
	} else {
		slog.InfoContext(ctx, "Fresh build detected from host")

		if err := runStep(
			el,
			"curl -fLo /usr/sbin/iiab https://raw.githubusercontent.com/iiab/iiab-factory/master/iiab",
			"failed to download iiab installer",
		); err != nil {
			return err
		}
		if err := runStep(
			el,
			"chmod 0755 /usr/sbin/iiab",
			"failed to set execute permissions on iiab installer",
		); err != nil {
			return err
		}
		finalInstallCmd = "/usr/sbin/iiab --risky"
	}

	// Run final installer command
	if err := el.SendLine(finalInstallCmd + "; echo \"BUILD_EXIT_CODE:$?\""); err != nil {
		return fmt.Errorf("failed to send installer command: %w", err)
	}

	sawPhotographed, err := monitorBuild(ctx, el, buildType)
	if err != nil {
		return err
	}

	// Fresh installs (iiab-install) trigger a system reboot after "photographed".
	// Incremental builds (iiab-configure) do not reboot.
	if sawPhotographed {
		if _, err := el.WaitForString("login: ", stepTimeout); err != nil {
			return fmt.Errorf("timeout waiting for reboot login prompt: %w", err)
		}
		if err := el.SendLine("root"); err != nil {
			return fmt.Errorf("failed to login after reboot: %w", err)
		}
	}

	return finalizeBuild(ctx, el)
}

// runStep sends a command, waits for the prompt, and verifies the exit code.
func runStep(el *PTYLoop, cmd, errMsg string) error {
	if err := el.SendLine(cmd + "; echo \"EXIT_CODE:$?\""); err != nil {
		return fmt.Errorf("%s: failed to send command: %w", errMsg, err)
	}

	match, _, err := el.WaitForAny([]*regexp.Regexp{reExitCode}, stepTimeout)
	if err != nil {
		return fmt.Errorf("%s: timeout waiting for exit code: %w", errMsg, err)
	}

	// Extract exit code
	matches := reExitCode.FindStringSubmatch(match)
	if len(matches) < 2 {
		return fmt.Errorf("%s: failed to parse exit code from output", errMsg)
	}

	if matches[1] != "0" {
		return fmt.Errorf("%s: command exited with code %s", errMsg, matches[1])
	}

	// Wait for the following prompt to ensure PTY is ready for next command
	if _, _, err := el.WaitForAny([]*regexp.Regexp{rePrompt}, stepTimeout); err != nil {
		return fmt.Errorf("%s: timeout waiting for prompt after command: %w", errMsg, err)
	}

	return nil
}

func loginAndPrepare(el *PTYLoop, skipInstall bool) error {
	// Wait for login prompt
	if _, err := el.WaitForString("login: ", stepTimeout); err != nil {
		return fmt.Errorf("timeout waiting for login prompt: %w", err)
	}

	// Login as root
	if err := el.SendLine("root"); err != nil {
		return fmt.Errorf("failed to send login: %w", err)
	}

	// Wait for root prompt
	if _, _, err := el.WaitForAny([]*regexp.Regexp{rePrompt}, stepTimeout); err != nil {
		return fmt.Errorf("timeout waiting for root prompt: %w", err)
	}

	// Set environment variables to disable terminal noise and interactive prompts
	if err := el.SendLine(
		"export PAGER=cat SYSTEMD_PAGER=cat TERM=dumb GIT_PROGRESS_DELAY=0 GIT_TERMINAL_PROMPT=0 ANSIBLE_NOCOLOR=1 DEBIAN_FRONTEND=noninteractive",
	); err != nil {
		return fmt.Errorf("failed to set environment variables: %w", err)
	}
	if _, _, err := el.WaitForAny([]*regexp.Regexp{rePrompt}, stepTimeout); err != nil {
		return fmt.Errorf("timeout after environment set: %w", err)
	}

	// Install git if not present, then silence it
	if err := el.SendLine(
		"command -v git >/dev/null 2>&1 || { apt-get update -qq && apt-get install -y -qq git >/dev/null 2>&1; }",
	); err != nil {
		return fmt.Errorf("failed to send git installation check: %w", err)
	}
	if _, _, err := el.WaitForAny([]*regexp.Regexp{rePrompt}, stepTimeout); err != nil {
		return fmt.Errorf("timeout after git installation: %w", err)
	}

	// Silence git (ignore errors -- these are best-effort)
	_ = el.SendLine("git config --global core.pager cat 2>/dev/null")
	_ = el.AwaitPrompt(stepTimeout)
	_ = el.SendLine("git config --global core.progress false 2>/dev/null")
	_ = el.AwaitPrompt(stepTimeout)
	_ = el.SendLine("git config --global advice.detachedHead false 2>/dev/null")
	_ = el.AwaitPrompt(stepTimeout)
	_ = el.SendLine("git config --global report.status false")
	_ = el.AwaitPrompt(stepTimeout)
	_ = el.SendLine("git config --global fetch.showForcedUpdates false")
	_ = el.AwaitPrompt(stepTimeout)
	_ = el.SendLine("git config --global core.checkStat minimal")
	_ = el.AwaitPrompt(stepTimeout)

	if skipInstall {
		return nil
	}

	// Generate SSH keys
	if err := el.SendLine("ssh-keygen -A"); err != nil {
		return fmt.Errorf("failed to send ssh-keygen: %w", err)
	}
	if _, _, err := el.WaitForAny([]*regexp.Regexp{rePrompt}, stepTimeout); err != nil {
		return fmt.Errorf("timeout after ssh-keygen: %w", err)
	}

	// Wait for network readiness (poll for default route)
	if err := el.SendLine(
		"for i in $(seq 1 30); do ip route | grep -q default && break; sleep 1; done",
	); err != nil {
		return fmt.Errorf("failed to send network check: %w", err)
	}
	if _, _, err := el.WaitForAny([]*regexp.Regexp{rePrompt}, stepTimeout); err != nil {
		return fmt.Errorf("timeout waiting for network check: %w", err)
	}

	// Verify network is functional
	if err := el.SendLine(
		"ip route | grep -q default || { echo 'ERROR: No default route' >&2; exit 1; }",
	); err != nil {
		return fmt.Errorf("failed to send network verify: %w", err)
	}
	if _, _, err := el.WaitForAny([]*regexp.Regexp{rePrompt}, stepTimeout); err != nil {
		return fmt.Errorf("timeout waiting for network verify: %w", err)
	}

	return nil
}

func monitorBuild(ctx context.Context, el *PTYLoop, buildType string) (bool, error) {
	// Single expect loop (like TCL's `expect { ... exp_continue }`).
	// One read goroutine fills the buffer; we scan all patterns against the
	// accumulated buffer on each iteration -- no per-call PTY reads.
	state := &buildState{buildType: buildType, el: el}
	patterns := state.initialPatterns()
	patternsWithFailed := state.failedPatterns()

	for {
		current := patterns
		if state.sawPlayRecap {
			current = patternsWithFailed
		}

		match, idx, err := el.WaitForAny(current, stepTimeout)
		if err != nil {
			return state.handleEOF(err)
		}

		cont, err := state.handleMatch(ctx, match, idx)
		if err != nil {
			return false, err
		}
		if !cont {
			return state.sawPhotographed, nil
		}
	}
}

// buildState tracks the parsing state for monitorBuild.
type buildState struct {
	sawPlayRecap    bool
	sawPhotographed bool
	exitCode        string
	buildType       string
	el              *PTYLoop
}

func (s *buildState) initialPatterns() []*regexp.Regexp {
	return []*regexp.Regexp{
		regexp.MustCompile(`photographed`),
		regexp.MustCompile(`PLAY RECAP`),
		regexp.MustCompile(`BUILD_EXIT_CODE:([0-9]+)`),
		rePrompt,
	}
}

func (s *buildState) failedPatterns() []*regexp.Regexp {
	return []*regexp.Regexp{
		regexp.MustCompile(`photographed`),
		regexp.MustCompile(`PLAY RECAP`),
		regexp.MustCompile(`failed=0`),
		reBuildFailed,
		regexp.MustCompile(`BUILD_EXIT_CODE:([0-9]+)`),
		rePrompt,
	}
}

func (s *buildState) handleEOF(err error) (bool, error) {
	if errors.Is(err, io.EOF) {
		if s.exitCode != "" && s.exitCode == "0" {
			return s.sawPhotographed, nil
		}
		return false, fmt.Errorf("PTY closed before build completed: %w", err)
	}
	return false, fmt.Errorf("timeout during build: %w", err)
}

// handleMatch processes a pattern match.
// Returns (continueLoop, error).
func (s *buildState) handleMatch(ctx context.Context, match string, idx int) (continueLoop bool, _ error) {
	switch idx {
	case 0: // photographed
		if s.buildType == "fresh" {
			s.sawPhotographed = true
			_ = s.el.SendLine("\r")
			return false, nil
		}

	case 1: // PLAY RECAP
		s.sawPlayRecap = true
		slog.InfoContext(ctx, "Ansible PLAY RECAP section reached")

	case 2: // failed=0 or BUILD_EXIT_CODE
		if s.sawPlayRecap && strings.Contains(match, "failed=0") {
			slog.InfoContext(ctx, "PLAY RECAP shows failed=0 (success)")
			return false, nil
		}
		if m := reBuildExitCode.FindStringSubmatch(match); m != nil {
			s.exitCode = m[1]
			if s.exitCode != "0" {
				return false, fmt.Errorf("IIAB build script failed with exit code: %s", s.exitCode)
			}
			slog.InfoContext(ctx, "IIAB build script completed", "exit_code", s.exitCode)
		}

	case 3: // reBuildFailed or rePrompt
		if s.sawPlayRecap && reBuildFailed.MatchString(match) {
			return false, errors.New("IIAB PLAY RECAP shows failures (failed>0)")
		}
		if s.exitCode == "" {
			return true, nil // still waiting for BUILD_EXIT_CODE
		}
		return false, nil // build complete
	}

	return true, nil
}

func finalizeBuild(ctx context.Context, el *PTYLoop) error {
	// Elicit a fresh prompt -- the caller may have already consumed the previous one
	// (this is the case in --skip-install mode where loginAndPrepare returns at a prompt).
	if err := el.SendLine(""); err != nil {
		return fmt.Errorf("failed to send carriage return: %w", err)
	}

	// Wait for root prompt
	if _, _, err := el.WaitForAny([]*regexp.Regexp{rePrompt}, stepTimeout); err != nil {
		return fmt.Errorf("timeout waiting for final prompt: %w", err)
	}

	// Lock root account
	if err := el.SendLine("usermod --lock --expiredate=1 root"); err != nil {
		return fmt.Errorf("failed to lock root: %w", err)
	}
	if _, _, err := el.WaitForAny([]*regexp.Regexp{rePrompt}, stepTimeout); err != nil {
		return fmt.Errorf("timeout after locking root: %w", err)
	}

	// Shutdown the container
	slog.DebugContext(ctx, "Sending shutdown command")
	if err := el.SendLine("shutdown now"); err != nil {
		return fmt.Errorf("failed to shutdown: %w", err)
	}

	// Wait for EOF (container shutdown).
	// The container may terminate the PTY before a clean EOF can be sent,
	// resulting in an I/O error. This is expected behavior and should be tolerated.
	slog.DebugContext(ctx, "Waiting for container EOF")
	_ = el.WaitEOF(stepTimeout)
	slog.DebugContext(ctx, "Container EOF received")

	return nil
}
