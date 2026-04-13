package network_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/lock"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/network"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
)

func TestIPAllocationUniqueness(t *testing.T) {
	tmpDir := t.TempDir()
	activeDir := state.ActiveDir(tmpDir)
	os.MkdirAll(activeDir, 0o755)

	allocatedIPs := make(map[string]bool)
	for i := range 10 {
		name := t.Name() + string(rune('a'+i))
		demoDir := filepath.Join(activeDir, name)
		os.MkdirAll(demoDir, 0o755)

		ip, err := network.AllocateNextIP(tmpDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ip == "" {
			t.Fatal("expected non-empty IP")
		}
		if allocatedIPs[ip] {
			t.Errorf("IP %s allocated twice", ip)
		}
		allocatedIPs[ip] = true

		// Write IP file so next allocation skips it
		os.WriteFile(filepath.Join(demoDir, "ip"), []byte(ip), 0o644)
	}
	if len(allocatedIPs) != 10 {
		t.Errorf("expected 10 allocated IPs, got %d", len(allocatedIPs))
	}
}

func TestIPPoolExhaustion(t *testing.T) {
	tmpDir := t.TempDir()
	activeDir := state.ActiveDir(tmpDir)
	os.MkdirAll(activeDir, 0o755)

	// Fill all 253 IPs (2-254)
	for i := 2; i <= 254; i++ {
		name := fmt.Sprintf("exhaust-%d", i)
		demoDir := filepath.Join(activeDir, name)
		os.MkdirAll(demoDir, 0o755)

		ip, err := network.AllocateNextIP(tmpDir)
		if err != nil {
			t.Fatalf("unexpected error at iteration %d: %v", i, err)
		}
		os.WriteFile(filepath.Join(demoDir, "ip"), []byte(ip), 0o644)
	}

	// Now try one more -- should fail
	extraDir := filepath.Join(activeDir, "exhaust-extra")
	os.MkdirAll(extraDir, 0o755)
	_, err := network.AllocateNextIP(tmpDir)
	if err == nil {
		t.Fatal("expected error when IP pool is exhausted")
	}
}

func TestConcurrentIPAllocationUnderLock(t *testing.T) {
	tmpDir := t.TempDir()
	activeDir := state.ActiveDir(tmpDir)
	os.MkdirAll(activeDir, 0o755)
	lockFile := filepath.Join(tmpDir, ".democtl.lock")

	lk, err := lock.AcquireShort(context.Background(), lockFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer lk.Release()

	// Allocate 5 IPs under lock
	for i := range 5 {
		name := "concurrent" + string(rune('a'+i))
		demoDir := filepath.Join(activeDir, name)
		os.MkdirAll(demoDir, 0o755)

		ip, err := network.AllocateNextIP(tmpDir)
		if err != nil {
			t.Fatalf("unexpected error at iteration %d: %v", i, err)
		}
		os.WriteFile(filepath.Join(demoDir, "ip"), []byte(ip), 0o644)
	}
}
