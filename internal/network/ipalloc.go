// Package network handles IP allocation, bridge setup, and nftables rules.
package network

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
)

const (
	SubnetBase = state.IIABSubnetBase // "10.0.3"

	// IP allocation range constants
	ipStartOffset   = 2   // First usable IP in subnet
	ipEndOffset     = 254 // Last usable IP in subnet
	ipPoolSize      = 253 // Total usable addresses
	ipWarnThreshold = 10  // Warn when remaining IPs below this
)

// AllocateNextIP finds the next available IP in the subnet.
// Must be called under lock.
func AllocateNextIP(stateDir string) (string, error) {
	activeDir := state.ActiveDir(stateDir)

	usedIPs := make(map[string]bool)
	entries, _ := os.ReadDir(activeDir)

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ipFile := filepath.Join(activeDir, e.Name(), "ip")
		if data, err := os.ReadFile(ipFile); err == nil {
			ip := string(data)
			if ip != "" {
				usedIPs[ip] = true
			}
		}
	}

	usedCount := len(usedIPs)
	if usedCount > 0 && (ipPoolSize-usedCount) < ipWarnThreshold {
		// Warning only -- don't block
		fmt.Fprintf(os.Stderr, "Warning: Subnet pool running low -- %d/%d IPs used, %d remaining\n",
			usedCount, ipPoolSize, ipPoolSize-usedCount)
	}

	for i := ipStartOffset; i <= ipEndOffset; i++ {
		candidate := fmt.Sprintf("%s.%d", SubnetBase, i)
		if !usedIPs[candidate] {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("no available IPs in %s (all %d addresses in use)", state.IIABDemoSubnet, ipPoolSize)
}
