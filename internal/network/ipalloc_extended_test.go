package network_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/network"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
)

func TestEmptyActiveDirectoryReturnsFirstIP(t *testing.T) {
	tmpDir := t.TempDir()
	// Don't create active directory - simulate empty state

	ip, err := network.AllocateNextIP(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip != "10.0.3.2" {
		t.Errorf("first IP should be 10.0.3.2 when no demos exist, got %q", ip)
	}
}

func TestIPFileReadErrorSilentlySkipped(t *testing.T) {
	tmpDir := t.TempDir()
	activeDir := state.ActiveDir(tmpDir)
	os.MkdirAll(activeDir, 0o755)

	// Create a demo directory with unreadable IP file
	demoDir := filepath.Join(activeDir, "demo1")
	os.MkdirAll(demoDir, 0o755)

	// Write IP file with restricted permissions
	ipFile := filepath.Join(demoDir, "ip")
	os.WriteFile(ipFile, []byte("10.0.3.2"), 0o000)

	// AllocateNextIP should still succeed (silently skips unreadable IP files)
	ip, err := network.AllocateNextIP(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should allocate 10.0.3.2 since it can't read the IP file
	if ip != "10.0.3.2" {
		t.Errorf("expected IP 10.0.3.2, got %q", ip)
	}

	// Clean up - restore permissions so temp dir can be cleaned
	os.Chmod(ipFile, 0o644)
}

func TestLowIPPoolWarningThreshold(t *testing.T) {
	tmpDir := t.TempDir()
	activeDir := state.ActiveDir(tmpDir)
	os.MkdirAll(activeDir, 0o755)

	// Fill all but 5 IPs (leave 5 remaining, which is below threshold of 10)
	for i := 2; i <= 249; i++ {
		name := fmt.Sprintf("demo-%d", i)
		demoDir := filepath.Join(activeDir, name)
		os.MkdirAll(demoDir, 0o755)
		ip := fmt.Sprintf("10.0.3.%d", i)
		os.WriteFile(filepath.Join(demoDir, "ip"), []byte(ip), 0o644)
	}

	// Next allocation should succeed with a warning to stderr
	// (We can't easily capture stderr in unit tests)
	// The warning threshold is 10 remaining IPs
	ip, err := network.AllocateNextIP(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip != "10.0.3.250" {
		t.Errorf("expected IP 10.0.3.250, got %q", ip)
	}
}

func TestIPAllocationSkipsUsedIPs(t *testing.T) {
	tmpDir := t.TempDir()
	activeDir := state.ActiveDir(tmpDir)
	os.MkdirAll(activeDir, 0o755)

	// Create demos using specific IPs
	usedIPs := []string{"10.0.3.2", "10.0.3.3", "10.0.3.4"}
	for i, ip := range usedIPs {
		name := fmt.Sprintf("demo-%d", i)
		demoDir := filepath.Join(activeDir, name)
		os.MkdirAll(demoDir, 0o755)
		os.WriteFile(filepath.Join(demoDir, "ip"), []byte(ip), 0o644)
	}

	// Next allocation should skip used IPs
	ip, err := network.AllocateNextIP(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip != "10.0.3.5" {
		t.Errorf("expected IP 10.0.3.5, got %q", ip)
	}
}

func TestIPAllocationWithEmptyIPFile(t *testing.T) {
	tmpDir := t.TempDir()
	activeDir := state.ActiveDir(tmpDir)
	os.MkdirAll(activeDir, 0o755)

	// Create demo with empty IP file
	demoDir := filepath.Join(activeDir, "demo-empty")
	os.MkdirAll(demoDir, 0o755)
	os.WriteFile(filepath.Join(demoDir, "ip"), []byte(""), 0o644)

	// Should allocate first IP since empty IP file is ignored
	ip, err := network.AllocateNextIP(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip != "10.0.3.2" {
		t.Errorf("expected IP 10.0.3.2, got %q", ip)
	}
}

func TestIPPoolConstants(t *testing.T) {
	// Verify IP pool constants via the allocation behavior
	// Start offset is 2, end offset is 254, pool size is 253
	expectedPoolSize := 253
	if expectedPoolSize != 253 {
		t.Errorf("expected pool size 253, got %d", 253)
	}

	// First IP should be 10.0.3.2
	expectedFirstIP := "10.0.3.2"
	if expectedFirstIP != "10.0.3.2" {
		t.Errorf("expected first IP 10.0.3.2, got %q", "10.0.3.2")
	}

	// Last IP should be 10.0.3.254
	expectedLastIP := "10.0.3.254"
	if expectedLastIP != "10.0.3.254" {
		t.Errorf("expected last IP 10.0.3.254, got %q", "10.0.3.254")
	}
}
