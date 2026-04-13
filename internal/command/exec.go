// Package command provides centralized command execution helpers.
package command

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"time"
)

const defaultTimeout = 30 * time.Minute

// Run runs a command with a 30-minute timeout and logs its output.
// Success (exit code 0): stderr -> [slog.Info], stdout -> [slog.Debug].
// Failure (exit code > 0): stderr -> [slog.Error], stdout -> [slog.Warn].
func Run(ctx context.Context, name string, args ...string) error {
	return RunWithTimeout(ctx, defaultTimeout, name, args...)
}

// RunWithTimeout runs a command with a custom timeout and logs its output.
func RunWithTimeout(ctx context.Context, timeout time.Duration, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	stdoutStr := stdout.String()
	stderrStr := stderr.String()

	if err != nil {
		if stderrStr != "" {
			slog.ErrorContext(ctx, "Command stderr", "command", name, "args", args, "output", stderrStr, "error", err)
		} else {
			slog.ErrorContext(ctx, "Command failed", "command", name, "args", args, "error", err)
		}
		if stdoutStr != "" {
			slog.WarnContext(ctx, "Command stdout", "command", name, "args", args, "output", stdoutStr)
		}
		return fmt.Errorf("command %s failed: %w", name, err)
	}

	if stderrStr != "" {
		slog.InfoContext(ctx, "Command stderr", "command", name, "args", args, "output", stderrStr)
	}
	if stdoutStr != "" {
		slog.DebugContext(ctx, "Command stdout", "command", name, "args", args, "output", stdoutStr)
	}

	return nil
}

// Output runs a command and returns its stdout as a string.
func Output(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// QuietRun runs a command with a 30-minute timeout without logging any output.
// Use this for commands where stderr output is expected and not an error condition.
func QuietRun(ctx context.Context, name string, args ...string) error {
	return QuietRunWithTimeout(ctx, defaultTimeout, name, args...)
}

// QuietRunWithTimeout runs a command with a custom timeout without logging any output.
func QuietRunWithTimeout(ctx context.Context, timeout time.Duration, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("command %s failed: %w", name, err)
	}
	return nil
}
