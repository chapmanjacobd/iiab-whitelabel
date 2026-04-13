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
	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/storage"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/sys"
)

// ListCmd shows all demos and resource usage.
type ListCmd struct {
	TOML bool `help:"Output as TOML" short:"t"`
}

// Run executes the list command.
func (c *ListCmd) Run(ctx context.Context, globals *GlobalOptions) error {
	if err := ensureRoot(); err != nil {
		return err
	}

	names, err := config.List(globals.StateDir)
	if err != nil {
		return err
	}

	if len(names) == 0 {
		if c.TOML {
			fmt.Println("demos = []")
			return nil
		}
		fmt.Println("No active demos found.")
		return nil
	}

	var demos []*config.Demo
	totalAllocated := 0
	for _, name := range names {
		var demo *config.Demo
		demo, err = config.Read(ctx, globals.StateDir, name)
		if err != nil {
			if !c.TOML {
				fmt.Printf("%-20s %-10s %-15s %-12s %-12s %s\n", name, "error", "unknown", "", "", err.Error())
			}
			continue
		}
		demos = append(demos, demo)
		totalAllocated += demo.ImageSizeMB
	}

	if c.TOML {
		type listOutput struct {
			Demos []*config.Demo `toml:"demos"`
		}
		var data []byte
		data, err = config.MarshalTOML(listOutput{Demos: demos})
		if err != nil {
			return err
		}
		fmt.Print(string(data))
		return nil
	}

	fmt.Printf("%-20s %-10s %-15s %-12s %-12s %s\n", "NAME", "STATUS", "IP", "SIZE", "UNIQUE", "DESCRIPTION")
	fmt.Println(strings.Repeat("-", 90))

	for _, demo := range demos {
		fmt.Printf(
			"%-20s %-10s %-15s %-12s %-12s %s\n",
			demo.Name,
			demo.Status,
			demo.IP,
			sys.FormatSizeMB(demo.ImageSizeMB),
			sys.FormatSizeMB(demo.UniqueSizeMB),
			demo.Description,
		)
	}

	// Show resource usage
	diskTotal := sys.GetDiskTotalMB(ctx, globals.StateDir, globals.System.MachinesDir)
	if r, err := config.ReadResources(globals.StateDir); err == nil {
		if r.DiskTotalMB > 0 {
			diskTotal = r.DiskTotalMB
		}
	}

	percent := 0.0
	if diskTotal > 0 {
		percent = float64(totalAllocated) / float64(diskTotal) * 100
	}

	fmt.Println(strings.Repeat("-", 90))
	fmt.Printf(
		"Total Allocated: %s / %s (%.1f%%)\n",
		sys.FormatSizeMB(totalAllocated),
		sys.FormatSizeMB(diskTotal),
		percent,
	)

	return nil
}

// StatusCmd shows detailed status of a demo.
type StatusCmd struct {
	Name string `help:"Demo name"      arg:""`
	TOML bool   `help:"Output as TOML"        short:"t"`
}

// Run executes the status command.
func (c *StatusCmd) Run(ctx context.Context, globals *GlobalOptions) error {
	if err := ensureRoot(); err != nil {
		return err
	}

	demo, err := config.Read(ctx, globals.StateDir, c.Name)
	if err != nil {
		return fmt.Errorf("cannot read demo %s: %w", c.Name, err)
	}

	if c.TOML {
		// We want to include IP and Status in the TOML output even though they have toml:"-"
		// so we'll use a temporary struct or just manually add them if needed.
		// For now, let's just marshal the demo struct.
		// Wait, the user might want IP and Status too.
		type tomlOutput struct {
			*config.Demo

			Status string `toml:"status"`
			IP     string `toml:"ip"`
		}
		var data []byte
		data, err = config.MarshalTOML(tomlOutput{
			Demo:   demo,
			Status: demo.Status,
			IP:     demo.IP,
		})
		if err != nil {
			return err
		}
		fmt.Print(string(data))
		return nil
	}

	fmt.Printf("Demo:        %s\n", demo.Name)
	fmt.Printf("Status:      %s\n", demo.Status)
	fmt.Printf("IP:          %s\n", demo.IP)
	fmt.Printf("Subdomain:   %s\n", demo.Subdomain)
	fmt.Printf("Description: %s\n", demo.Description)
	fmt.Printf("Repo:        %s\n", demo.Repo)
	fmt.Printf("Branch:      %s\n", demo.Branch)
	fmt.Printf("Size:        %s\n", sys.FormatSizeMB(demo.ImageSizeMB))
	if demo.UniqueSizeMB > 0 {
		fmt.Printf("Unique:      %s\n", sys.FormatSizeMB(demo.UniqueSizeMB))
	}
	fmt.Printf("Volatile:    %s\n", demo.VolatileMode)
	fmt.Printf("On Disk:     %v\n", demo.BuildOnDisk)
	fmt.Printf("Wildcard:    %v\n", demo.Wildcard)

	// Live storage detection
	st := storage.DetectStorageInfo(ctx)
	if st.Mounted {
		fmt.Printf("Storage:     %s", st.Backend)
		if st.FSType != "" {
			fmt.Printf(" (btrfs on %s)", st.FSType)
		}
		if st.LoopDev != "" {
			fmt.Printf(", loop: %s", st.LoopDev)
		}
		if st.FileSize != "" {
			fmt.Printf(", file: %s", st.FileSize)
		}
		fmt.Printf("\n")
	}

	if demo.BaseName != "" {
		fmt.Printf("Base:        %s\n", demo.BaseName)
	}

	// Check build log
	logFile := filepath.Join(state.DemoDir(globals.StateDir, c.Name), "build.log")
	if info, statErr := os.Stat(logFile); statErr == nil {
		fmt.Printf("Build log:   %s\n", sys.FormatBytes(info.Size()))
	}

	// Check if container is running via machinectl
	out, err := exec.CommandContext(ctx, "machinectl", "status", c.Name).Output()
	if err == nil {
		fmt.Println("\nContainer Status (machinectl):")
		fmt.Println(string(out))
	}

	return nil
}

// LogsCmd shows build log or container journal.
type LogsCmd struct {
	Name  string `help:"Demo name (omit for all)"                    arg:"" optional:""`
	Build bool   `help:"Show build log instead of container journal"                    short:"b"`
}

// Run executes the logs command.
func (c *LogsCmd) Run(ctx context.Context, globals *GlobalOptions) error {
	if err := ensureRoot(); err != nil {
		return err
	}
	if c.Name != "" {
		return showLogs(ctx, globals, c.Name, c.Build)
	}

	names, err := config.List(globals.StateDir)
	if err != nil {
		return err
	}

	for i, name := range names {
		if i > 0 {
			slog.InfoContext(ctx, "", "separator", "---")
		}
		slog.InfoContext(ctx, "Showing logs", "name", name)
		if err := showLogs(ctx, globals, name, c.Build); err != nil {
			slog.ErrorContext(ctx, "Logs failed", "demo", name, "error", err)
		}
	}
	return nil
}

func showLogs(ctx context.Context, globals *GlobalOptions, name string, forceBuild bool) error {
	status, err := config.GetDemoStatus(globals.StateDir, name)
	if err != nil {
		return err
	}

	if !forceBuild && (status == "running" || status == "stopped") {
		tctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()

		// Show journalctl for running container
		cmd := exec.CommandContext(
			tctx,
			"journalctl",
			"-u",
			fmt.Sprintf("systemd-nspawn@%s.service", name),
			"--no-pager",
			"-n",
			"100",
		)

		// Don't return error if journalctl fails (container might not have logs yet)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if runErr := cmd.Run(); runErr == nil {
			return nil
		}
	}

	// Show build log
	logFile := filepath.Join(state.DemoDir(globals.StateDir, name), "build.log")
	data, err := os.ReadFile(logFile)
	if err != nil {
		return fmt.Errorf("no logs found (journalctl failed and no build.log exists): %w", err)
	}

	os.Stdout.Write(data)
	return nil
}

// ShellCmd opens a shell in a running container.
type ShellCmd struct {
	Name string   `help:"Demo name"                           arg:""`
	Args []string `help:"Command to run inside the container" arg:"" optional:""`
}

// Run executes the shell command.
func (c *ShellCmd) Run(ctx context.Context, globals *GlobalOptions) error {
	if err := ensureRoot(); err != nil {
		return err
	}

	// Try systemd-run -t first. It provides a proper TTY with job control,
	// bypasses PAM authentication (unlike machinectl shell), but still
	// integrates with systemd-machined.
	runArgs := []string{"-M", c.Name, "-t", "--quiet", "--collect"}
	if len(c.Args) > 0 {
		runArgs = append(runArgs, c.Args...)
	} else {
		runArgs = append(runArgs, "/bin/bash")
	}

	cmd := exec.CommandContext(ctx, "systemd-run", runArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err == nil {
		return nil
	}

	slog.WarnContext(ctx, "systemd-run failed, falling back to nsenter", "demo", c.Name)

	// Fallback to nsenter if systemd-run fails (e.g., no system bus in container)
	pid, err := getContainerPID(ctx, c.Name)
	if err != nil || pid == "" {
		return fmt.Errorf("container %s is not running or PID not found: %w", c.Name, err)
	}

	return c.runNsenter(ctx, pid)
}

func getContainerPID(ctx context.Context, name string) (string, error) {
	out, err := exec.CommandContext(ctx, "machinectl", "show", name, "-p", "Leader", "--value").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (c *ShellCmd) runNsenter(ctx context.Context, pid string) error {
	// Determine command to run
	exe := "/bin/bash"
	args := []string{}
	if len(c.Args) > 0 {
		exe = c.Args[0]
		if len(c.Args) > 1 {
			args = c.Args[1:]
		}
	}

	// nsenter into all namespaces of the leader PID
	nsenterArgs := make([]string, 0, 8+len(args))
	nsenterArgs = append(nsenterArgs, "-t", pid, "-m", "-u", "-i", "-n", "-p", exe)
	nsenterArgs = append(nsenterArgs, args...)
	nsCmd := exec.CommandContext(ctx, "nsenter", nsenterArgs...)
	nsCmd.Stdin = os.Stdin
	nsCmd.Stdout = os.Stdout
	nsCmd.Stderr = os.Stderr

	// Set a basic PATH and TERM if not present, as nsenter inherits host environment
	// but might need some defaults for a better interactive experience.
	env := os.Environ()
	hasTerm := false
	hasPath := false
	for _, e := range env {
		if strings.HasPrefix(e, "TERM=") {
			hasTerm = true
		}
		if strings.HasPrefix(e, "PATH=") {
			hasPath = true
		}
	}
	if !hasTerm {
		env = append(env, "TERM=xterm-256color")
	}
	if !hasPath {
		env = append(env, "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
	}
	nsCmd.Env = env

	return nsCmd.Run()
}
