package network

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/command"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/config"
)

// SetupNAT sets up nftables NAT masquerade for containers.
// Idempotent: uses individual CLI commands that skip already-existing structures.
func SetupNAT(ctx context.Context, sys *config.System) error {
	extIF, err := DetectExternalInterface(ctx)
	if err != nil {
		return err
	}

	// Apply NAT rules idempotently via CLI commands
	// Create table (idempotent: silently ignores if exists)
	_ = command.Run(ctx, "nft", "add", "table", "inet", "iiab")

	// Create chain only if it doesn't exist (idempotent)
	if !chainExists(ctx, "inet", "postrouting") {
		if err := command.Run(ctx, "nft", "add", "chain", "inet", "iiab", "postrouting",
			"{ type nat hook postrouting priority srcnat; policy accept; }"); err != nil {
			return fmt.Errorf("cannot create postrouting chain: %w", err)
		}
	}

	// Flush existing rules to start clean
	_ = command.Run(ctx, "nft", "flush", "chain", "inet", "iiab", "postrouting")

	// Add masquerade rule
	if err := command.Run(ctx, "nft", "add", "rule", "inet", "iiab", "postrouting",
		"oifname", extIF, "ip", "saddr", SubnetCIDR, "masquerade"); err != nil {
		return fmt.Errorf("cannot apply nftables NAT masquerade: %w", err)
	}

	// Apply container isolation
	if err := AddContainerIsolation(ctx, extIF); err != nil {
		return err
	}

	// Save rules to persist across reboots safely
	if err := persistRules(ctx, sys); err != nil {
		slog.WarnContext(ctx, "Failed to persist nftables rules", "error", err)
	}

	slog.InfoContext(ctx, "NAT and isolation rules applied")
	return nil
}

// chainExists checks if a specific chain exists in the nftables table.
func chainExists(ctx context.Context, family, chain string) bool {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "nft", "list", "table", family, "iiab").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "chain "+chain)
}

// persistRules saves the iiab table to a dedicated include file and ensures it is included.
func persistRules(ctx context.Context, sys *config.System) error {
	includeDir := sys.NftablesDir
	iiabConf := filepath.Join(includeDir, "iiab.conf")
	mainConf := sys.NftablesConf

	if err := os.MkdirAll(includeDir, 0o755); err != nil {
		return err
	}

	// Get only the iiab table rules
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "nft", "list", "table", "inet", "iiab").Output()
	if err != nil {
		return fmt.Errorf("cannot list iiab table: %w", err)
	}

	if writeErr := os.WriteFile(iiabConf, out, 0o644); writeErr != nil {
		return fmt.Errorf("cannot write iiab nft config: %w", writeErr)
	}

	// Ensure include directive exists in main config
	mainData, err := os.ReadFile(mainConf)
	if err != nil {
		if os.IsNotExist(err) {
			// Create a basic nftables.conf if it doesn't exist
			return os.WriteFile(mainConf, []byte("include \""+includeDir+"/*.conf\"\n"), 0o644)
		}
		return err
	}

	if !strings.Contains(string(mainData), "include \""+includeDir+"/*.conf\"") {
		f, err := os.OpenFile(mainConf, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		_, _ = f.WriteString("\ninclude \"" + includeDir + "/*.conf\"\n")
	}

	return nil
}

// AddContainerIsolation applies per-container network isolation rules.
// Rules:
//  1. Block container-to-container traffic (L3 via inet forward, L2 via bridge forward)
//  2. Allow container-to-host gateway (DNS, Nginx proxy)
//  3. Allow container-to-internet (NAT'd traffic)
//  4. Allow host-to-container (for reverse proxy and health checks)
//  5. Allow established/related connections
//
// Idempotent: checks if rules already exist before recreating.
func AddContainerIsolation(ctx context.Context, extIF string) error {
	// Reset iptables FORWARD policy (avoid Docker interference)
	// This is done on every call to ensure it is not lost to other network management tools.
	_ = command.Run(ctx, "iptables", "-P", "FORWARD", "ACCEPT")

	if isolationRulesActive(ctx, extIF) {
		slog.InfoContext(ctx, "Container isolation rules already active")
		return nil
	}

	slog.InfoContext(ctx, "Applying container isolation rules...")

	// === INET TABLE (L3 filtering) ===
	_ = command.Run(ctx, "nft", "add", "table", "inet", "iiab")

	// Create forward chain only if it doesn't exist
	if !chainExists(ctx, "inet", "forward") {
		if err := command.Run(ctx, "nft", "add", "chain", "inet", "iiab", "forward",
			"{ type filter hook forward priority filter - 1; policy accept; }"); err != nil {
			return fmt.Errorf("cannot create inet iiab forward chain: %w", err)
		}
	}
	// Flush existing rules
	_ = command.Run(ctx, "nft", "flush", "chain", "inet", "iiab", "forward")

	// A. Allow established/related traffic
	if err := command.Run(ctx, "nft", "add", "rule", "inet", "iiab", "forward",
		"ct", "state", "established,related", "accept"); err != nil {
		return fmt.Errorf("cannot add established/related rule: %w", err)
	}

	// B. Allow container -> Host gateway (DNS, Nginx proxy)
	if err := command.Run(ctx, "nft", "add", "rule", "inet", "iiab", "forward",
		"iifname", "{ ve-*, vb-* }", "ip", "daddr", Gateway, "accept"); err != nil {
		return fmt.Errorf("cannot add container->gateway rule: %w", err)
	}

	// C. Allow container -> Internet (NAT'd traffic)
	if err := command.Run(ctx, "nft", "add", "rule", "inet", "iiab", "forward",
		"iifname", "{ ve-*, vb-* }", "oifname", extIF, "accept"); err != nil {
		return fmt.Errorf("cannot add container->internet rule: %w", err)
	}

	// D. Allow host -> container (for reverse proxy)
	if err := command.Run(ctx, "nft", "add", "rule", "inet", "iiab", "forward",
		"oifname", "{ "+BridgeName+", ve-*, vb-* }", "ip", "daddr", SubnetCIDR, "accept"); err != nil {
		return fmt.Errorf("cannot add host->container rule: %w", err)
	}

	// Create input chain only if it doesn't exist
	if !chainExists(ctx, "inet", "input") {
		if err := command.Run(ctx, "nft", "add", "chain", "inet", "iiab", "input",
			"{ type filter hook input priority filter - 1; policy accept; }"); err != nil {
			return fmt.Errorf("cannot create inet iiab input chain: %w", err)
		}
	}
	_ = command.Run(ctx, "nft", "flush", "chain", "inet", "iiab", "input")

	if err := command.Run(ctx, "nft", "add", "rule", "inet", "iiab", "input",
		"iifname", "{ ve-*, vb-* }", "accept"); err != nil {
		return fmt.Errorf("cannot add input ve/vb accept rule: %w", err)
	}
	if err := command.Run(ctx, "nft", "add", "rule", "inet", "iiab", "input",
		"ct", "state", "established,related", "accept"); err != nil {
		return fmt.Errorf("cannot add input established/related rule: %w", err)
	}

	// === BRIDGE TABLE (L2 isolation) ===
	// Delete and recreate to start clean (L2 tables are simpler to recreate)
	_ = command.Run(ctx, "nft", "delete", "table", "bridge", "iiab")
	_ = command.Run(ctx, "nft", "add", "table", "bridge", "iiab")
	if err := command.Run(ctx, "nft", "add", "chain", "bridge", "iiab", "forward",
		"{ type filter hook forward priority 0; policy accept; }"); err != nil {
		return fmt.Errorf("cannot create bridge iiab forward chain: %w", err)
	}

	// Drop all intra-bridge container-to-container traffic
	dropCombos := []struct{ from, to string }{
		{"ve-*", "ve-*"},
		{"vb-*", "vb-*"},
		{"ve-*", "vb-*"},
		{"vb-*", "ve-*"},
	}
	for _, combo := range dropCombos {
		if err := command.Run(ctx, "nft", "add", "rule", "bridge", "iiab", "forward",
			"iifname", combo.from, "oifname", combo.to, "drop"); err != nil {
			return fmt.Errorf("cannot add bridge drop rule %s->%s: %w", combo.from, combo.to, err)
		}
	}

	slog.InfoContext(ctx, "Container isolation rules added")
	return nil
}

// RemoveContainerIsolation removes nftables isolation rules.
func RemoveContainerIsolation(ctx context.Context) error {
	if tableExists(ctx, "inet") {
		_ = command.Run(ctx, "nft", "delete", "table", "inet", "iiab")
	}
	if tableExists(ctx, "bridge") {
		_ = command.Run(ctx, "nft", "delete", "table", "bridge", "iiab")
	}
	return nil
}

// tableExists checks if an nftables table exists.
func tableExists(ctx context.Context, family string) bool {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "nft", "list", "table", family, "iiab")
	return cmd.Run() == nil
}

// isolationRulesActive checks if isolation rules are already in place.
func isolationRulesActive(ctx context.Context, extIF string) bool {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "nft", "list", "ruleset").Output()
	if err != nil {
		return false
	}
	output := string(out)

	// Check inet iiab table and chains exist
	if !strings.Contains(output, "table inet iiab") {
		return false
	}
	if !strings.Contains(output, "type filter hook forward priority filter - 1") {
		return false
	}
	if !strings.Contains(output, "type filter hook input priority filter - 1") {
		return false
	}

	// Check bridge iiab table with drop rules
	if !strings.Contains(output, "table bridge iiab") {
		return false
	}
	if !strings.Contains(output, `iifname "ve-*" oifname "ve-*" drop`) {
		return false
	}

	// Check key inet forward rules
	if !strings.Contains(output, "ct state established,related accept") {
		return false
	}
	if !strings.Contains(output, "ip daddr "+Gateway) {
		return false
	}
	if extIF != "" && !strings.Contains(output, "oifname \""+extIF+"\"") {
		return false
	}

	return true
}
