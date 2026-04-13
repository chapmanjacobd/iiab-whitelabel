package network

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/command"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/config"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
)

const (
	BridgeName = state.IIABBridge
	Gateway    = state.IIABGateway
	SubnetCIDR = state.IIABDemoSubnet
)

// SetupBridge creates and configures the iiab-br0 bridge via systemd-networkd.
func SetupBridge(ctx context.Context, sys *config.System) error {
	// Create .netdev file
	netdevContent := `[NetDev]
Name=` + BridgeName + `
Kind=bridge
`
	if err := os.WriteFile(
		filepath.Join(sys.SystemdNetworkDir, "10-"+BridgeName+".netdev"),
		[]byte(netdevContent),
		0o644,
	); err != nil {
		return fmt.Errorf("cannot create netdev file: %w", err)
	}

	// Create .network file
	networkContent := `[Match]
Name=` + BridgeName + `

[Network]
Address=` + Gateway + `/24
IPForward=yes
`
	if err := os.WriteFile(
		filepath.Join(sys.SystemdNetworkDir, "11-"+BridgeName+".network"),
		[]byte(networkContent),
		0o644,
	); err != nil {
		return fmt.Errorf("cannot create network file: %w", err)
	}

	// Enable systemd-networkd
	if err := command.Run(ctx, "systemctl", "enable", "--now", "systemd-networkd"); err != nil {
		return err
	}

	// Ensure bridge device exists (ip link add is more immediate than systemd-networkd)
	if _, err := net.InterfaceByName(BridgeName); err != nil {
		slog.InfoContext(ctx, "Creating bridge device", "name", BridgeName)
		if err := command.Run(ctx, "ip", "link", "add", "name", BridgeName, "type", "bridge"); err != nil {
			slog.WarnContext(ctx, "Could not create bridge via ip link (might already exist)", "error", err)
		}
		if err := command.Run(ctx, "ip", "link", "set", BridgeName, "up"); err != nil {
			return fmt.Errorf("cannot bring bridge up: %w", err)
		}
	}

	// Assign IP to bridge (if not already done by systemd-networkd)
	if err := assignBridgeIP(ctx); err != nil {
		return err
	}

	slog.InfoContext(ctx, "Bridge configured", "name", BridgeName, "gateway", Gateway)
	return nil
}

// DetectExternalInterface finds the external network interface.
func DetectExternalInterface(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// Try default route
	out, err := exec.CommandContext(ctx, "ip", "route", "show", "default").Output()
	if err == nil {
		fields := strings.Fields(string(out))
		for i, f := range fields {
			if f == "dev" && i+1 < len(fields) {
				return fields[i+1], nil
			}
		}
	}

	// Fallback: first non-lo interface
	out, err = exec.CommandContext(ctx, "ip", "-o", "link", "show").Output()
	if err != nil {
		return "", fmt.Errorf("cannot detect external interface: %w", err)
	}

	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.Contains(line, "lo:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			iface := strings.TrimSuffix(fields[1], ":")
			return iface, nil
		}
	}

	return "", errors.New("no external interface found")
}

func assignBridgeIP(ctx context.Context) error {
	// Check if already assigned
	addrs, err := net.InterfaceAddrs()
	if err == nil {
		for _, addr := range addrs {
			if strings.Contains(addr.String(), Gateway) {
				return nil // already has IP
			}
		}
	}

	// Assign via ip command
	return command.Run(ctx, "ip", "addr", "add", Gateway+"/24", "dev", BridgeName)
}

// CheckGhostIPs checks for IPs that are allocated but no container is actually using them.
func CheckGhostIPs(ctx context.Context, stateDir string, names []string) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	for _, name := range names {
		demo, err := config.Read(ctx, stateDir, name)
		if err != nil {
			continue
		}
		if demo.IP == "" {
			continue
		}
		// Check if the container is actually using that IP
		cmd := exec.CommandContext(ctx, "ip", "addr", "show", "dev", "ve-"+name)
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		if !strings.Contains(string(out), demo.IP) {
			slog.WarnContext(ctx, "Ghost IP", "demo", name, "ip", demo.IP)
		}
	}
}
