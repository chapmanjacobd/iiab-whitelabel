// Package sys provides system-level checks and abstractions.
package sys

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/config"
)

// CheckDependencies verifies that all required external tools are installed.
func CheckDependencies(ctx context.Context) error {
	required := []string{
		"sudo",
		"btrfs",
		"nft",
		"iptables",
		"ip",
		"systemctl",
		"machinectl",
		"mount",
		"df",
		"nginx",
		"certbot",
		"systemd-dissect",
		"curl",
		"tar",
		"xz",
		"losetup",
		"fallocate",
		"truncate",
	}

	var missing []string
	for _, tool := range required {
		if _, err := exec.LookPath(tool); err != nil {
			missing = append(missing, tool)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf(
			"missing required system dependencies: %v, please install them before running democtl",
			missing,
		)
	}

	return nil
}

// IsProcessAlive checks if a process with the given PID is still running.
func IsProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Linux, FindProcess always succeeds, so we signal 0 to check
	return process.Signal(os.Signal(nil)) == nil
}

// SetBackgroundProcess configures a command to run in a new session (detached).
func SetBackgroundProcess(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}

// Exec replaces the current process with a new one.
// This is a wrapper around [syscall.Exec].
func Exec(path string, args, env []string) error {
	return syscall.Exec(path, args, env)
}

// Mountpoint checks if a path is a mount point.
func Mountpoint(ctx context.Context, path string) bool {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "mountpoint", "-q", path)
	return cmd.Run() == nil
}

// FallbackDiskTotalMB is the default disk total when resource file and df both fail.
const FallbackDiskTotalMB = 100 * 1024

// GetDiskTotalMB returns the total disk size in MB, reading from resource file or fallback.
func GetDiskTotalMB(ctx context.Context, stateDir, machinesDir string) int {
	if r, err := config.ReadResources(stateDir); err == nil {
		if r.DiskTotalMB > 0 {
			return r.DiskTotalMB
		}
	}
	// Fallback: df on machines directory
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	// Use --output=size to get only the size column and skip header
	cmd := exec.CommandContext(ctx, "df", "-m", "--output=size", machinesDir)
	out, err := cmd.Output()
	if err != nil {
		return FallbackDiskTotalMB
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) >= 2 {
		var val int
		if _, err := fmt.Sscanf(lines[1], "%d", &val); err == nil {
			return val
		}
	}
	return FallbackDiskTotalMB
}

// GetAvailableMemoryMB returns the available memory in MB from /proc/meminfo.
func GetAvailableMemoryMB() (int, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.HasPrefix(line, "MemAvailable:") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			kb, err := strconv.Atoi(fields[1])
			if err != nil {
				return 0, err
			}
			return kb / 1024, nil
		}
	}

	return 0, errors.New("MemAvailable not found in /proc/meminfo")
}

// GetMountSizeMB returns the size of a mount point in MB using df.
func GetMountSizeMB(ctx context.Context, path string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "df", "-m", "--output=size", path)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) >= 2 {
		var val int
		if _, err := fmt.Sscanf(lines[1], "%d", &val); err == nil {
			return val, nil
		}
	}
	return 0, fmt.Errorf("could not parse df output for %s", path)
}

// FormatSizeMB formats a size in MB to a human-readable string with 1 decimal place.
// Examples: 1519 MB -> "1.5 GB", 512 MB -> "512.0 MB", 10240 MB -> "10.0 GB"
func FormatSizeMB(mb int) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1f GB", float64(mb)/1024.0)
	}
	return fmt.Sprintf("%.1f MB", float64(mb))
}

// FormatBytes formats a size in bytes to a human-readable string with 1 decimal place.
// Examples: 221121 bytes -> "216.0 KB", 1048576 bytes -> "1.0 MB"
func FormatBytes(bytes int64) string {
	const (
		kb = 1024
		mb = 1024 * 1024
		gb = 1024 * 1024 * 1024
	)
	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
