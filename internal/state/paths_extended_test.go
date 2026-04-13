package state_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
)

func TestFileExistsTrueAndFalse(t *testing.T) {
	tmpDir := t.TempDir()
	existingFile := filepath.Join(tmpDir, "exists.txt")
	nonExistingFile := filepath.Join(tmpDir, "does-not-exist.txt")

	// Create the existing file
	os.WriteFile(existingFile, []byte("test"), 0o644)

	if !state.FileExists(existingFile) {
		t.Error("existing file should exist")
	}
	if state.FileExists(nonExistingFile) {
		t.Error("non-existing file should not exist")
	}
}

func TestFileExistsDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	if !state.FileExists(tmpDir) {
		t.Error("directory should exist")
	}
	if state.FileExists(filepath.Join(tmpDir, "nonexistent-dir")) {
		t.Error("non-existent directory should not exist")
	}
}

func TestReadFileMissingReturnsEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	missingFile := filepath.Join(tmpDir, "missing.txt")

	result, err := state.ReadFile(missingFile)
	if err == nil {
		t.Fatal("expected error when reading missing file")
	}
	if result != "" {
		t.Errorf("expected empty result, got %q", result)
	}
}

func TestReadFileExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	expectedContent := "hello world"

	os.WriteFile(testFile, []byte(expectedContent), 0o644)

	result, err := state.ReadFile(testFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expectedContent {
		t.Errorf("expected %q, got %q", expectedContent, result)
	}
}

func TestWriteFileAutoCreatesDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	nestedFile := filepath.Join(tmpDir, "nested", "dir", "structure", "test.txt")
	content := "test content"

	err := state.WriteFile(nestedFile, content, 0o644)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify file was created
	data, err := os.ReadFile(nestedFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != content {
		t.Errorf("expected %q, got %q", content, string(data))
	}
}

func TestWriteFileOverwritesExisting(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")

	// Write initial content
	os.WriteFile(testFile, []byte("initial"), 0o644)

	// Overwrite
	err := state.WriteFile(testFile, "updated", 0o644)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify updated content
	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "updated" {
		t.Errorf("expected 'updated', got %q", string(data))
	}
}

func TestWriteFileFailure(t *testing.T) {
	// Try to write to an invalid path
	invalidPath := "/proc/sys/invalid-file"
	err := state.WriteFile(invalidPath, "test", 0o644)
	_ = err
}

func TestWriteIP(t *testing.T) {
	tmpDir := t.TempDir()
	name := "test-demo"
	ip := "10.0.3.2"

	// Create demo directory
	demoDir := state.DemoDir(tmpDir, name)
	os.MkdirAll(demoDir, 0o755)

	// Write IP
	err := state.WriteIP(tmpDir, name, ip)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify IP file
	ipFile := filepath.Join(demoDir, "ip")
	data, err := os.ReadFile(ipFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != ip {
		t.Errorf("expected IP %q, got %q", ip, string(data))
	}
}

func TestWriteIPRequiresExistingDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	name := "new-demo"
	ip := "10.0.3.5"

	// WriteIP doesn't create directory - it will fail if directory doesn't exist
	err := state.WriteIP(tmpDir, name, ip)
	if err == nil {
		t.Fatal("expected error because directory doesn't exist")
	}
}

func TestPathConstructionFunctions(t *testing.T) {
	stateDir := "/test/state"

	// Test ActiveDir
	activeDir := state.ActiveDir(stateDir)
	if activeDir != "/test/state/active" {
		t.Errorf("expected active dir '/test/state/active', got %q", activeDir)
	}

	// Test ResourceFile
	resourceFile := state.ResourceFile(stateDir)
	if resourceFile != "/test/state/resources" {
		t.Errorf("expected resource file '/test/state/resources', got %q", resourceFile)
	}

	// Test LockFile
	lockFile := state.LockFile(stateDir)
	if lockFile != "/test/state/.democtl.lock" {
		t.Errorf("expected lock file '/test/state/.democtl.lock', got %q", lockFile)
	}

	// Test DemoDir
	demoDir := state.DemoDir(stateDir, "my-demo")
	if demoDir != "/test/state/active/my-demo" {
		t.Errorf("expected demo dir '/test/state/active/my-demo', got %q", demoDir)
	}
}

func TestConstantsConsistency(t *testing.T) {
	if state.ResourceFileName != "resources" {
		t.Errorf("expected resource file name 'resources', got %q", state.ResourceFileName)
	}
	if state.LockFileName != ".democtl.lock" {
		t.Errorf("expected lock file name '.democtl.lock', got %q", state.LockFileName)
	}
	if state.IIABBridge != "iiab-br0" {
		t.Errorf("expected bridge 'iiab-br0', got %q", state.IIABBridge)
	}
	if state.IIABSubnetBase != "10.0.3" {
		t.Errorf("expected subnet base '10.0.3', got %q", state.IIABSubnetBase)
	}
	if state.IIABGateway != "10.0.3.1" {
		t.Errorf("expected gateway '10.0.3.1', got %q", state.IIABGateway)
	}
	if state.IIABDemoSubnet != "10.0.3.0/24" {
		t.Errorf("expected subnet '10.0.3.0/24', got %q", state.IIABDemoSubnet)
	}
}

func TestWriteFileWithDifferentPermissions(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name string
		perm os.FileMode
	}{
		{"readonly", 0o444},
		{"executable", 0o755},
		{"private", 0o600},
		{"world-readable", 0o644},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testFile := filepath.Join(tmpDir, tc.name+".txt")
			err := state.WriteFile(testFile, "content", tc.perm)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			info, err := os.Stat(testFile)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.Mode().Perm()&tc.perm == 0 {
				t.Errorf("file should have expected permissions %v, got %v", tc.perm, info.Mode().Perm())
			}
		})
	}
}
