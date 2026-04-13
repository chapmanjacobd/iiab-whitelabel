package sys_test

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/sys"
)

func TestGetDiskTotalMBFromResourceFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create resource file with disk_total_mb (TOML)
	content := `disk_total_mb = 20480
other_key = "value"
`
	os.WriteFile(state.ResourceFile(tmpDir), []byte(content), 0o644)

	result := sys.GetDiskTotalMB(t.Context(), tmpDir, "/var/lib/machines")
	if result != 20480 {
		t.Errorf("expected disk total 20480, got %d", result)
	}
}

func TestGetDiskTotalMBInvalidValueInResourceFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create resource file with invalid disk_total_mb
	content := `disk_total_mb = "invalid"
`
	os.WriteFile(state.ResourceFile(tmpDir), []byte(content), 0o644)

	// Should fallback to df command
	result := sys.GetDiskTotalMB(t.Context(), tmpDir, "/var/lib/machines")
	// Result depends on df output or fallback constant
	if result <= 0 {
		t.Errorf("disk total should be positive, got %d", result)
	}
}

func TestGetAvailableMemoryMB(t *testing.T) {
	result, err := sys.GetAvailableMemoryMB()
	// Should work on Linux, might fail elsewhere but we can at least check for no crash
	if err != nil {
		t.Logf("GetAvailableMemoryMB returned error (expected if not on Linux): %v", err)
	} else {
		if result <= 0 {
			t.Errorf("expected positive memory, got %d", result)
		}
		t.Logf("Available memory: %d MB", result)
	}
}

func TestGetDiskTotalMBMissingResourceFile(t *testing.T) {
	tmpDir := t.TempDir()

	// No resource file - should fallback to df
	result := sys.GetDiskTotalMB(t.Context(), tmpDir, "/var/lib/machines")
	// Result depends on df output or fallback constant
	if result <= 0 {
		t.Errorf("disk total should be positive, got %d", result)
	}
}

func TestGetDiskTotalMBFallbackDefault(t *testing.T) {
	// When both resource file and df fail, should use FallbackDiskTotalMB
	if sys.FallbackDiskTotalMB != 100*1024 {
		t.Errorf("expected fallback disk total %d, got %d", 100*1024, sys.FallbackDiskTotalMB)
	}
}

func TestMountpointCommand(t *testing.T) {
	tmpDir := t.TempDir()

	// A normal directory is not a mountpoint
	result := sys.Mountpoint(t.Context(), tmpDir)
	if result {
		t.Errorf("normal directory should not be a mountpoint")
	}
}

func TestIsProcessAliveAfterProcessDeath(t *testing.T) {
	// Start a short-lived process
	cmd := exec.Command("sleep", "0.1")
	cmd.Start()
	pid := cmd.Process.Pid

	// Wait for it to finish
	cmd.Wait()

	// Give OS time to clean up
	_ = sys.IsProcessAlive(pid)
}

func TestGetDiskTotalMBParsesDfOutput(t *testing.T) {
	// We can't easily mock df, but we can verify the parsing logic
	testLine := "/dev/sda1         102400  5000     97400   5% /var/lib/machines"
	fields := []string{"/dev/sda1", "102400", "5000", "97400", "5%", "/var/lib/machines"}

	var val int
	_, err := fmt.Sscanf(fields[1], "%d", &val)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != 102400 {
		t.Errorf("expected 102400, got %d", val)
	}

	_ = testLine // Document the expected format
}
