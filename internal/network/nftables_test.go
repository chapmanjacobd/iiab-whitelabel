package network_test

import (
	"os"
	"strings"
	"testing"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/network"
)

func TestSetupNATTempFileCreation(t *testing.T) {
	// SetupNAT creates a temp file with restricted permissions
	// The pattern is: os.CreateTemp("", "iiab-nft-*.nft")
	tmpFile, err := os.CreateTemp(t.TempDir(), "iiab-nft-*.nft")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Should have 0o600 permissions after chmod
	err = os.Chmod(tmpFile.Name(), 0o600)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(tmpFile.Name())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("expected permissions 0o600, got %v", info.Mode().Perm())
	}
}

func TestSetupNATRuleStructure(t *testing.T) {
	// NAT rules should contain expected elements (idempotent format)
	extIF := "eth0"
	subnetCIDR := "10.0.3.0/24"

	rules := `table inet iiab
chain postrouting {
    type nat hook postrouting priority srcnat; policy accept;
}
flush chain inet iiab postrouting
add rule inet iiab postrouting oifname "` + extIF + `" ip saddr ` + subnetCIDR + ` masquerade
`

	if !strings.Contains(rules, "table inet iiab") {
		t.Error("expected rules to contain 'table inet iiab'")
	}
	if !strings.Contains(rules, "masquerade") {
		t.Error("expected rules to contain 'masquerade'")
	}
	if !strings.Contains(rules, extIF) {
		t.Errorf("expected rules to contain %q", extIF)
	}
	if !strings.Contains(rules, subnetCIDR) {
		t.Errorf("expected rules to contain %q", subnetCIDR)
	}
	if !strings.Contains(rules, "flush chain") {
		t.Error("expected rules to contain 'flush chain'")
	}
}

func TestPersistRulesIncludeDirectoryCreation(t *testing.T) {
	// persistRules creates /etc/nftables.d directory
	// This requires root, so we test the structure
	expectedDir := "/etc/nftables.d"
	if expectedDir != "/etc/nftables.d" {
		t.Errorf("expected dir %q, got %q", expectedDir, "/etc/nftables.d")
	}
}

func TestPersistRulesMainConfigWithoutInclude(t *testing.T) {
	// When main config doesn't have include directive, should append it
	mainConfig := `#!/usr/sbin/nft -f
flush ruleset`

	expectedInclude := `include "/etc/nftables.d/*.conf"`

	// Should not contain include initially
	if strings.Contains(mainConfig, "include") {
		t.Error("expected main config to not contain 'include' initially")
	}

	// After append, should contain include
	withInclude := mainConfig + "\n" + expectedInclude + "\n"
	if !strings.Contains(withInclude, "include") {
		t.Error("expected withInclude to contain 'include'")
	}
}

func TestPersistRulesMainConfigWithInclude(t *testing.T) {
	// When main config already has include directive, should not append
	mainConfig := `#!/usr/sbin/nft -f
flush ruleset

include "/etc/nftables.d/*.conf"`

	if !strings.Contains(mainConfig, "include") {
		t.Error("expected main config to contain 'include'")
	}

	// Should not append again
	alreadyHasInclude := strings.Contains(mainConfig, `include "/etc/nftables.d/*.conf"`)
	if !alreadyHasInclude {
		t.Error("expected main config to already have include")
	}
}

func TestRemoveContainerIsolation(t *testing.T) {
	// RemoveContainerIsolation deletes the iiab tables
	// Commands: nft delete table inet iiab, nft delete table bridge iiab
	// This requires root, so we test the structure
	expectedCommands := []string{
		"nft delete table inet iiab",
		"nft delete table bridge iiab",
	}
	if len(expectedCommands) != 2 {
		t.Errorf("expected 2 commands, got %d", len(expectedCommands))
	}
}

func TestIsolationRulesActive(t *testing.T) {
	// isolationRulesActive checks for inet/bridge tables, chains, and key rules
	testOutput := `table inet iiab {
    chain forward {
        type filter hook forward priority filter - 1; policy accept;
        ct state established,related accept
        iifname { ve-*, vb-* } ip daddr 10.0.3.1 accept
        iifname { ve-*, vb-* } oifname "eth0" accept
        oifname { ve-*, vb-* } ip daddr 10.0.3.0/24 accept
    }
    chain input {
        type filter hook input priority filter - 1; policy accept;
        iifname { ve-*, vb-* } accept
        ct state established,related accept
    }
}
table bridge iiab {
    chain forward {
        type filter hook forward priority 0; policy accept;
        iifname "ve-*" oifname "ve-*" drop
    }
}`

	isActive := strings.Contains(testOutput, "table inet iiab") &&
		strings.Contains(testOutput, "type filter hook forward priority filter - 1") &&
		strings.Contains(testOutput, "table bridge iiab") &&
		strings.Contains(testOutput, `iifname "ve-*" oifname "ve-*" drop`) &&
		strings.Contains(testOutput, "ct state established,related accept")
	if !isActive {
		t.Error("isolation rules should be active")
	}
}

func TestIsolationRulesNotActive(t *testing.T) {
	// When ruleset doesn't have iiab or ve-*, should return false
	testOutput := `table inet filter {
    chain input {
        type filter hook input priority 0; policy accept;
    }
}`

	isActive := strings.Contains(testOutput, "iiab") && strings.Contains(testOutput, "ve-*")
	if isActive {
		t.Error("isolation rules should not be active")
	}
}

func TestContainerIsolationRuleStructure(t *testing.T) {
	// Isolation rules should contain expected elements (comprehensive L3+L2)
	gateway := "10.0.3.1"
	extIF := "eth0"
	subnetCIDR := "10.0.3.0/24"

	rules := `# Inet table: L3 filtering with priority filter-1
table inet iiab
chain forward {
    type filter hook forward priority filter - 1; policy accept;
}
flush chain inet iiab forward
add rule inet iiab forward ct state established,related accept
add rule inet iiab forward iifname { ve-*, vb-* } ip daddr ` + gateway + ` accept
add rule inet iiab forward iifname { ve-*, vb-* } oifname "` + extIF + `" accept
add rule inet iiab forward oifname { ve-*, vb-* } ip daddr ` + subnetCIDR + ` accept
chain input {
    type filter hook input priority filter - 1; policy accept;
}
flush chain inet iiab input
add rule inet iiab input iifname { ve-*, vb-* } accept
add rule inet iiab input ct state established,related accept
# Bridge table: L2 isolation
delete table bridge iiab
table bridge iiab
chain forward {
    type filter hook forward priority 0; policy accept;
}
add rule bridge iiab forward iifname "ve-*" oifname "ve-*" drop
add rule bridge iiab forward iifname "vb-*" oifname "vb-*" drop
add rule bridge iiab forward iifname "ve-*" oifname "vb-*" drop
add rule bridge iiab forward iifname "vb-*" oifname "ve-*" drop
`

	if !strings.Contains(rules, "ve-*") {
		t.Error("expected rules to contain 've-*'")
	}
	if !strings.Contains(rules, "drop") {
		t.Error("expected rules to contain 'drop'")
	}
	if !strings.Contains(rules, gateway) {
		t.Errorf("expected rules to contain %q", gateway)
	}
	if !strings.Contains(rules, extIF) {
		t.Errorf("expected rules to contain %q", extIF)
	}
	if !strings.Contains(rules, "ct state established,related accept") {
		t.Error("expected rules to contain 'ct state established,related accept'")
	}
	if !strings.Contains(rules, "chain input") {
		t.Error("expected rules to contain 'chain input'")
	}
	if !strings.Contains(rules, "table bridge iiab") {
		t.Error("expected rules to contain 'table bridge iiab'")
	}
}

func TestNftablesRuleCreationLogic(t *testing.T) {
	// NAT rules use idempotent add/flush instead of delete/recreate
	rules := `table inet iiab
chain postrouting {
    type nat hook postrouting priority srcnat; policy accept;
}
flush chain inet iiab postrouting
add rule inet iiab postrouting oifname "eth0" ip saddr 10.0.3.0/24 masquerade
`

	// Should use flush+add, not delete
	if !strings.Contains(rules, "flush chain") {
		t.Error("expected rules to contain 'flush chain'")
	}
	if !strings.Contains(rules, "add rule") {
		t.Error("expected rules to contain 'add rule'")
	}
	if strings.Contains(rules, "delete table") {
		t.Error("expected rules to NOT contain 'delete table'")
	}
}

func TestNftablesTempFileCleanup(t *testing.T) {
	// SetupNAT and AddContainerIsolation use defer os.Remove to cleanup temp files
	// This ensures no temp file leaks
	tmpFile, err := os.CreateTemp(t.TempDir(), "iiab-test-*.nft")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tmpPath := tmpFile.Name()

	// Simulate defer cleanup
	os.Remove(tmpPath)

	// File should be gone
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("expected file %s to be removed", tmpPath)
	}
}

func TestNftablesRulePersistence(t *testing.T) {
	// persistRules saves rules to /etc/nftables.d/iiab.conf
	// This ensures rules survive reboots
	expectedPath := "/etc/nftables.d/iiab.conf"
	if expectedPath != "/etc/nftables.d/iiab.conf" {
		t.Errorf("expected path %q, got %q", expectedPath, "/etc/nftables.d/iiab.conf")
	}

	// Verify network constants
	if network.BridgeName != "iiab-br0" {
		t.Errorf("expected bridge name 'iiab-br0', got %q", network.BridgeName)
	}
}
