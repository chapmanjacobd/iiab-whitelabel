package config

import (
	"os"
)

// System holds system-wide paths and configuration.
type System struct {
	// SystemdNetworkDir is the directory where systemd-networkd configuration is stored.
	SystemdNetworkDir string
	// NftablesConf is the path to the main nftables configuration file.
	NftablesConf string
	// NftablesDir is the directory where nftables include files are stored.
	NftablesDir string
	// MachinesDir is the directory where systemd-nspawn containers are stored.
	MachinesDir string
	// NspawnDir is the directory where systemd-nspawn .nspawn configuration files are stored.
	NspawnDir string
}

// NewSystem returns a System configuration with defaults that can be overridden by environment variables.
func NewSystem() *System {
	s := &System{
		SystemdNetworkDir: "/etc/systemd/network",
		NftablesConf:      "/etc/nftables.conf",
		NftablesDir:       "/etc/nftables.d/",
		MachinesDir:       "/var/lib/machines",
		NspawnDir:         "/etc/systemd/nspawn",
	}

	if val := os.Getenv("DEMOCTL_SYSTEMD_NETWORK_DIR"); val != "" {
		s.SystemdNetworkDir = val
	}
	if val := os.Getenv("DEMOCTL_NFTABLES_CONF"); val != "" {
		s.NftablesConf = val
	}
	if val := os.Getenv("DEMOCTL_NFTABLES_DIR"); val != "" {
		s.NftablesDir = val
	}
	if val := os.Getenv("DEMOCTL_MACHINES_DIR"); val != "" {
		s.MachinesDir = val
	}
	if val := os.Getenv("DEMOCTL_NSPAWN_DIR"); val != "" {
		s.NspawnDir = val
	}

	return s
}
