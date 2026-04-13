package config_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/config"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
)

func TestReadMalformedConfigLines(t *testing.T) {
	tmpDir := t.TempDir()
	activeDir := state.ActiveDir(tmpDir)
	name := "malformed-test"
	demoDir := filepath.Join(activeDir, name)
	os.MkdirAll(demoDir, 0o755)

	// Valid TOML with comments and empty lines
	configContent := `# This is a comment
demo_name = "malformed-test"

iiab_repo = "https://github.com/test/iiab.git"
# this line is a comment now
iiab_branch = "master"

# Another comment
image_size_mb = 10000
`
	os.WriteFile(filepath.Join(demoDir, "config"), []byte(configContent), 0o644)

	demo, err := config.Read(context.Background(), tmpDir, name)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if demo.Name != "malformed-test" {
		t.Errorf("expected name 'malformed-test', got %q", demo.Name)
	}
	if demo.Repo != "https://github.com/test/iiab.git" {
		t.Errorf("expected repo 'https://github.com/test/iiab.git', got %q", demo.Repo)
	}
	if demo.Branch != "master" {
		t.Errorf("expected branch 'master', got %q", demo.Branch)
	}
	if demo.ImageSizeMB != 10000 {
		t.Errorf("expected image size 10000, got %d", demo.ImageSizeMB)
	}
}

func TestReadEmptyConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	activeDir := state.ActiveDir(tmpDir)
	name := "empty-config"
	demoDir := filepath.Join(activeDir, name)
	os.MkdirAll(demoDir, 0o755)

	// Empty config file
	os.WriteFile(filepath.Join(demoDir, "config"), []byte(""), 0o644)

	demo, err := config.Read(context.Background(), tmpDir, name)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if demo.Name != "empty-config" {
		t.Errorf("expected name 'empty-config', got %q", demo.Name)
	}
	if demo.Repo != "" {
		t.Errorf("expected empty repo, got %q", demo.Repo)
	}
	if demo.ImageSizeMB != 0 {
		t.Errorf("expected image size 0, got %d", demo.ImageSizeMB)
	}
}

func TestReadCommentsOnlyConfig(t *testing.T) {
	tmpDir := t.TempDir()
	activeDir := state.ActiveDir(tmpDir)
	name := "comments-only"
	demoDir := filepath.Join(activeDir, name)
	os.MkdirAll(demoDir, 0o755)

	// Config with only comments
	configContent := `# Comment 1
# Comment 2
# Comment 3
`
	os.WriteFile(filepath.Join(demoDir, "config"), []byte(configContent), 0o644)

	demo, err := config.Read(context.Background(), tmpDir, name)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if demo.Name != "comments-only" {
		t.Errorf("expected name 'comments-only', got %q", demo.Name)
	}
}

func TestReadMissingIPAndStatusFiles(t *testing.T) {
	tmpDir := t.TempDir()
	activeDir := state.ActiveDir(tmpDir)
	name := "missing-files"
	demoDir := filepath.Join(activeDir, name)
	os.MkdirAll(demoDir, 0o755)

	// Config without IP and status files (TOML)
	configContent := `demo_name = "missing-files"
iiab_repo = ""
iiab_branch = ""
image_size_mb = 10000
volatile_mode = ""
build_on_disk = false
skip_install = false
cleanup_failed = false
local_vars = ""
wildcard = false
description = ""
base_name = ""
subdomain = "missing-files"
`
	os.WriteFile(filepath.Join(demoDir, "config"), []byte(configContent), 0o644)

	demo, err := config.Read(context.Background(), tmpDir, name)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if demo.Name != "missing-files" {
		t.Errorf("expected name 'missing-files', got %q", demo.Name)
	}
	if demo.IP != "" {
		t.Errorf("expected empty IP, got %q", demo.IP)
	}
	if demo.Status != "" {
		t.Errorf("expected empty Status, got %q", demo.Status)
	}
}

func TestGetDemoStatusMissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	activeDir := state.ActiveDir(tmpDir)
	name := "missing-status"
	demoDir := filepath.Join(activeDir, name)
	os.MkdirAll(demoDir, 0o755)

	// Create config but no status file (TOML)
	configContent := `demo_name = "missing-status"
iiab_repo = ""
iiab_branch = ""
image_size_mb = 10000
volatile_mode = ""
build_on_disk = false
skip_install = false
cleanup_failed = false
local_vars = ""
wildcard = false
description = ""
base_name = ""
subdomain = "missing-status"
`
	os.WriteFile(filepath.Join(demoDir, "config"), []byte(configContent), 0o644)

	// GetDemoStatus should return "unknown" when status file is missing
	status, err := config.GetDemoStatus(tmpDir, name)
	if err == nil {
		t.Fatal("expected error when status file is missing")
	}
	if status != "unknown" {
		t.Errorf("expected status 'unknown', got %q", status)
	}
}

func TestEscapeUnescapeRoundtrip(t *testing.T) {
	// escapeVal/unescapeVal are unexported, but we can test them
	// indirectly through the public Write/Read API.
	values := []string{
		``,
		`hello`,
		`hello"world`,
		`hello\world`,
		`hello\"world`,
		`"test"`,
		`\test\`,
		`test with "quotes" and \backslash`,
	}

	for _, input := range values {
		t.Run(input, func(t *testing.T) {
			tmpDir := t.TempDir()
			activeDir := state.ActiveDir(tmpDir)
			name := "escape-test"
			demoDir := filepath.Join(activeDir, name)
			os.MkdirAll(demoDir, 0o755)

			// Write a demo with the input value as description
			demo := &config.Demo{
				Name:        name,
				Description: input,
				Subdomain:   name,
			}
			err := demo.Write(tmpDir)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Read it back and verify the roundtrip preserves the value
			readDemo, err := config.Read(context.Background(), tmpDir, name)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if readDemo.Description != input {
				t.Errorf(
					"escape/unescape roundtrip should preserve value: expected %q, got %q",
					input,
					readDemo.Description,
				)
			}
		})
	}
}

func TestValidateNameExact64CharBoundary(t *testing.T) {
	// Exactly 64 characters - should pass
	name64 := strings.Repeat("a", 64)
	if err := config.ValidateName(name64); err != nil {
		t.Errorf("64 char name should be valid: %v", err)
	}

	// 65 characters - should fail
	name65 := strings.Repeat("a", 65)
	if err := config.ValidateName(name65); err == nil {
		t.Error("65 char name should be invalid")
	}
}

func TestValidateNameOnlySpecialChars(t *testing.T) {
	// Names with only special chars should still be valid if they contain valid chars
	// "---" is valid (hyphens are allowed)
	if err := config.ValidateName("---"); err != nil {
		t.Errorf("'---' should be valid: %v", err)
	}
	if err := config.ValidateName("___"); err != nil {
		t.Errorf("'___' should be valid: %v", err)
	}
	if err := config.ValidateName("a-b_c"); err != nil {
		t.Errorf("'a-b_c' should be valid: %v", err)
	}
}

func TestValidateNameMixedCaseAndSpecialChars(t *testing.T) {
	tests := []struct {
		name    string
		isValid bool
	}{
		{"My-Demo_123", true},
		{"TEST-DEMO", true},
		{"test_demo", true},
		{"test-demo-123", true},
		{"Test123", true},
		{"", false},          // Empty name
		{"test demo", false}, // Space not allowed
		{"test.demo", false}, // Dot not allowed
		{"test@demo", false}, // @ not allowed
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := config.ValidateName(tc.name)
			if tc.isValid {
				if err != nil {
					t.Errorf("name %q should be valid: %v", tc.name, err)
				}
			} else {
				if err == nil {
					t.Errorf("name %q should be invalid", tc.name)
				}
			}
		})
	}
}

func TestReadRoundtripWithSpecialChars(t *testing.T) {
	tmpDir := t.TempDir()
	activeDir := state.ActiveDir(tmpDir)
	name := "special-chars"
	demoDir := filepath.Join(activeDir, name)
	os.MkdirAll(demoDir, 0o755)

	// Config with special characters in values (TOML)
	configContent := `demo_name = "special-chars"
iiab_repo = "https://github.com/test/iiab.git"
iiab_branch = "feature/test-branch"
image_size_mb = 10000
volatile_mode = ""
build_on_disk = false
skip_install = true
cleanup_failed = false
local_vars = ""
wildcard = false
description = "Test with \"quotes\" and \\backslash"
base_name = ""
subdomain = "special-chars"
`
	os.WriteFile(filepath.Join(demoDir, "config"), []byte(configContent), 0o644)

	demo, err := config.Read(context.Background(), tmpDir, name)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if demo.Name != "special-chars" {
		t.Errorf("expected name 'special-chars', got %q", demo.Name)
	}
	if demo.Repo != "https://github.com/test/iiab.git" {
		t.Errorf("expected repo 'https://github.com/test/iiab.git', got %q", demo.Repo)
	}
	if demo.Branch != "feature/test-branch" {
		t.Errorf("expected branch 'feature/test-branch', got %q", demo.Branch)
	}
	if !demo.SkipInstall {
		t.Error("expected SkipInstall to be true")
	}
	if demo.Description != "Test with \"quotes\" and \\backslash" {
		t.Errorf("expected description 'Test with \"quotes\" and \\backslash', got %q", demo.Description)
	}
}
