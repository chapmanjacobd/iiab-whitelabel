package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/config"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/network"
)

// GenerateNspawn creates the .nspawn settings file for a container.
func GenerateNspawn(ctx context.Context, name, ip, volatileMode string, sys *config.System) error {
	if err := os.MkdirAll(sys.NspawnDir, 0o755); err != nil {
		return err
	}

	content := `[Exec]
Boot=true
PrivateUsers=false
NoNewPrivileges=false

[Files]
`
	if volatileMode != "" && volatileMode != "no" {
		content += fmt.Sprintf("Volatile=%s\n", volatileMode)
	}

	content += `
[Network]
Bridge=` + network.BridgeName + `
VirtualEthernet=true
`

	nspawnFile := filepath.Join(sys.NspawnDir, name+".nspawn")
	if err := os.WriteFile(nspawnFile, []byte(content), 0o644); err != nil {
		return fmt.Errorf("cannot write .nspawn file: %w", err)
	}

	// Generate service override
	return generateServiceOverride(name)
}

// generateServiceOverride creates a systemd drop-in to harden the nspawn service.
func generateServiceOverride(name string) error {
	overrideDir := fmt.Sprintf("/etc/systemd/system/systemd-nspawn@%s.service.d", name)
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		return err
	}

	content := `[Service]
ProtectSystem=full
ProtectHome=yes
ProtectKernelTunables=yes
ProcSubset=pid
DevicePolicy=closed
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK
SystemCallArchitectures=native
MemoryDenyWriteExecute=yes
`

	overrideFile := filepath.Join(overrideDir, "override.conf")
	if err := os.WriteFile(overrideFile, []byte(content), 0o644); err != nil {
		return fmt.Errorf("cannot write service override: %w", err)
	}

	return nil
}
