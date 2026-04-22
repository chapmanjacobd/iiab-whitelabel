package tests_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/config"
)

func TestBuildCommand(t *testing.T) {
	requireRoot(t)

	stateDir := setupStateDir(t)
	name := uniqueDemoName("test-demo")

	// Cleanup storage on exit
	defer runDemoctl(t, stateDir, "delete", name)

	// 1. Initial build command
	stdout, stderr, err := runDemoctl(t, stateDir, "build", name, "--skip-install", "--size", "12000")
	if err != nil {
		t.Fatalf("build command failed: %s %s", stdout, stderr)
	}

	combined := stdout + stderr
	if !strings.Contains(combined, "Starting build") {
		t.Errorf("expected output to contain 'Starting build', got: %s", combined)
	}
	if !strings.Contains(combined, "Build completed successfully") {
		t.Errorf("expected output to contain 'Build completed successfully', got: %s", combined)
	}

	// 2. Verify state files exist
	demoDir := filepath.Join(stateDir, "active", name)
	if _, statErr := os.Stat(demoDir); os.IsNotExist(statErr) {
		t.Errorf("expected demo dir %s to exist", demoDir)
	}
	if _, statErr := os.Stat(filepath.Join(demoDir, "config")); os.IsNotExist(statErr) {
		t.Errorf("expected config file to exist")
	}
	if _, statErr := os.Stat(filepath.Join(demoDir, "status")); os.IsNotExist(statErr) {
		t.Errorf("expected status file to exist")
	}
	if _, statErr := os.Stat(filepath.Join(demoDir, "ip")); os.IsNotExist(statErr) {
		t.Errorf("expected ip file to exist")
	}

	// 3. Verify file contents
	status, err := os.ReadFile(filepath.Join(demoDir, "status"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(status) != "stopped" {
		t.Errorf("expected status 'stopped', got %q", string(status))
	}

	ip, err := os.ReadFile(filepath.Join(demoDir, "ip"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ip) == 0 {
		t.Errorf("expected non-empty IP")
	}

	// 4. Parse config and verify values
	demo, err := config.Read(t.Context(), stateDir, name)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if demo.Name != name {
		t.Errorf("expected name %q, got %q", name, demo.Name)
	}
	// image_size_mb is updated to actual used space after build (should be between 500MB and 1GB for skip-install)
	if demo.ImageSizeMB < 500 {
		t.Errorf("expected image_size_mb >= 500, got %d", demo.ImageSizeMB)
	}
	if demo.ImageSizeMB > 1024 {
		t.Errorf("expected image_size_mb <= 1024, got %d", demo.ImageSizeMB)
	}
	// unique_size_mb should always be smaller than image_size_mb
	if demo.UniqueSizeMB <= 0 {
		t.Errorf("expected unique_size_mb > 0, got %d", demo.UniqueSizeMB)
	}
	if demo.UniqueSizeMB >= demo.ImageSizeMB {
		t.Errorf("expected unique_size_mb < image_size_mb, got %d >= %d", demo.UniqueSizeMB, demo.ImageSizeMB)
	}
}

func TestBuildCommandInvalidName(t *testing.T) {
	stateDir := setupStateDir(t)
	name := "invalid name!"

	_, stderr, err := runDemoctl(t, stateDir, "build", name)
	if err == nil {
		t.Fatalf("expected error for invalid name")
	}
	if !strings.Contains(stderr, "contains invalid characters") {
		t.Errorf("expected stderr to contain 'contains invalid characters', got: %s", stderr)
	}
}
