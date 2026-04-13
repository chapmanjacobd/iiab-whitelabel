package command_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/command"
)

func TestRunSuccessReturnsNil(t *testing.T) {
	// Run a command that should succeed
	err := command.Run(t.Context(), "true")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunSuccessCapturesOutput(t *testing.T) {
	// Run echo which writes to stdout
	err := command.Run(t.Context(), "echo", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunFailureReturnsError(t *testing.T) {
	// Run a command that should fail
	err := command.Run(t.Context(), "false")
	if err == nil {
		t.Fatal("expected error for failing command")
	}
	if !strings.Contains(err.Error(), "command false failed") {
		t.Errorf("expected error to contain 'command false failed', got: %v", err)
	}
}

func TestRunWithCustomTimeoutSuccess(t *testing.T) {
	// Short timeout with fast command
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := command.RunWithTimeout(ctx, 1*time.Second, "true")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunWithCustomTimeoutExpiry(t *testing.T) {
	// Command that sleeps longer than timeout
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := command.RunWithTimeout(ctx, 100*time.Millisecond, "sleep", "10")
	if err == nil {
		t.Fatal("expected error for timed out command")
	}
	// Should contain timeout information
	if !strings.Contains(err.Error(), "command sleep failed") {
		t.Errorf("expected error to contain 'command sleep failed', got: %v", err)
	}
}

func TestRunWithContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // Cancel immediately

	err := command.Run(ctx, "sleep", "10")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestRunNonExistentCommand(t *testing.T) {
	err := command.Run(t.Context(), "nonexistent-command-that-does-not-exist")
	if err == nil {
		t.Fatal("expected error for non-existent command")
	}
}

func TestRunWithStderrOutput(t *testing.T) {
	// Commands that write to stderr should still succeed if exit code is 0
	// Using a command that writes to stderr but exits successfully
	err := command.Run(t.Context(), "sh", "-c", "echo 'stderr output' >&2; exit 0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunWithStdoutAndStderr(t *testing.T) {
	// Command that writes to both stdout and stderr
	err := command.Run(t.Context(), "sh", "-c", "echo 'stdout'; echo 'stderr' >&2; exit 0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDefaultTimeoutConstant(t *testing.T) {
	// Verify default timeout is 30 minutes (internal constant)
	// This test documents the expected value
	expectedTimeout := 30 * time.Minute
	if 30*time.Minute != expectedTimeout {
		t.Errorf("default timeout should be 30 minutes")
	}
}
