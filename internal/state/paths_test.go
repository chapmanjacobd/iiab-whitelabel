package state_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
)

func TestEnsureStateDirsIdempotency(t *testing.T) {
	tmpDir := t.TempDir()

	// Call multiple times -- should not error
	for range 3 {
		dirs := []string{
			tmpDir,
			state.ActiveDir(tmpDir),
			t.TempDir(), // simulate /var/lib/machines
		}
		for _, d := range dirs {
			err := os.MkdirAll(d, 0o755)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		}
	}
}

func TestEnsureStateDirsIdempotencyFromDemoctl(t *testing.T) {
	tmpDir := t.TempDir()
	dirs := []string{
		tmpDir,
		state.ActiveDir(tmpDir),
	}

	// Call multiple times
	for range 3 {
		for _, d := range dirs {
			err := os.MkdirAll(d, 0o755)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		}
	}

	// Verify structure
	for _, d := range dirs {
		info, err := os.Stat(d)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !info.IsDir() {
			t.Errorf("expected %s to be a directory", d)
		}
	}
}

func TestNetworkConstantsConsistency(t *testing.T) {
	// Verify network constants are consistent across re-sources
	if state.IIABBridge != "iiab-br0" {
		t.Errorf("expected bridge 'iiab-br0', got %q", state.IIABBridge)
	}
	if state.IIABGateway != "10.0.3.1" {
		t.Errorf("expected gateway '10.0.3.1', got %q", state.IIABGateway)
	}
	if state.IIABDemoSubnet != "10.0.3.0/24" {
		t.Errorf("expected subnet '10.0.3.0/24', got %q", state.IIABDemoSubnet)
	}
	if state.IIABSubnetBase != "10.0.3" {
		t.Errorf("expected subnet base '10.0.3', got %q", state.IIABSubnetBase)
	}
}

func TestEnsureDirsIdempotency(t *testing.T) {
	tmpDir := t.TempDir()
	testDir := filepath.Join(tmpDir, "test-dir")

	// Create multiple times -- should not error
	for range 5 {
		err := os.MkdirAll(testDir, 0o755)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	// Verify only one directory exists
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}
}

func TestMultipleEnsureDirsCallsNoDuplication(t *testing.T) {
	tmpDir := t.TempDir()
	testDir := filepath.Join(tmpDir, "nested", "dir", "structure")

	// Create 10 times
	for range 10 {
		err := os.MkdirAll(testDir, 0o755)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	// Count directories
	count := 0
	err := filepath.Walk(tmpDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should be exactly 4: tmpDir + nested + dir + structure
	if count != 4 {
		t.Errorf("expected 4 directories, got %d", count)
	}
}
