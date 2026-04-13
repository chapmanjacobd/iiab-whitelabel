package build

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/command"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
)

// prepareRootfs sets up the container filesystem for IIAB.
func prepareRootfs(ctx context.Context, buildSubvol string, cfg Config, repo string) error {
	// Clone IIAB repo
	iiabPath := filepath.Join(buildSubvol, "opt/iiab/iiab")
	if state.FileExists(iiabPath) {
		slog.InfoContext(ctx, "Removing inherited /opt/iiab/iiab from base snapshot")
		if err := os.RemoveAll(iiabPath); err != nil {
			return err
		}
	}

	if err := os.MkdirAll(filepath.Join(buildSubvol, "opt/iiab"), 0o755); err != nil {
		return err
	}

	if state.FileExists(repo) {
		slog.InfoContext(ctx, "Copying local IIAB repository", "path", repo)
		if err := command.Run(ctx, "cp", "-a", repo+"/.", iiabPath+"/"); err != nil {
			return fmt.Errorf("cannot copy local IIAB repo: %w", err)
		}
	} else {
		slog.InfoContext(ctx, "Cloning IIAB repository", "repo", repo, "branch", cfg.Branch)
		if err := cloneIIABRepo(ctx, repo, cfg.Branch, iiabPath); err != nil {
			return fmt.Errorf("cannot clone IIAB repo: %w", err)
		}
	}

	// Resolve and copy local_vars
	if err := setupLocalVars(ctx, buildSubvol, cfg.LocalVars, iiabPath, cfg.Name); err != nil {
		return fmt.Errorf("cannot setup local_vars: %w", err)
	}

	// Set hostname
	if err := os.WriteFile(filepath.Join(buildSubvol, "etc/hostname"), []byte(cfg.Name), 0o644); err != nil {
		return err
	}

	// Set default target to multi-user
	systemdDir := filepath.Join(buildSubvol, "etc/systemd/system")
	if err := os.MkdirAll(systemdDir, 0o755); err != nil {
		return err
	}
	targetLink := filepath.Join(systemdDir, "default.target")
	os.Remove(targetLink)
	if err := os.Symlink("/usr/lib/systemd/system/multi-user.target", targetLink); err != nil {
		return err
	}

	// Configure container networking
	if err := setupContainerNetworking(buildSubvol, cfg.IP); err != nil {
		return fmt.Errorf("cannot setup networking: %w", err)
	}

	// Pre-configure systemd first-boot to suppress interactive wizard.
	// This must happen BEFORE the container boots with --boot to prevent
	// the "Press any key to proceed" prompt from hanging in CI.
	if err := command.Run(ctx, "systemd-firstboot",
		"--root="+buildSubvol,
		"--timezone=UTC",
		"--hostname="+cfg.Name,
		"--delete-root-password",
		"--force",
	); err != nil {
		return fmt.Errorf("systemd-firstboot failed: %w", err)
	}

	// Write resolv.conf
	resolvPath := filepath.Join(buildSubvol, "etc/resolv.conf")
	os.Remove(resolvPath)
	resolvContent := "nameserver 8.8.8.8\nnameserver 1.1.1.1\n"
	if err := os.WriteFile(resolvPath, []byte(resolvContent), 0o644); err != nil {
		return err
	}

	// Append container-specific overrides
	localVarsFile := filepath.Join(buildSubvol, "etc/iiab/local_vars.yml")
	f, err := os.OpenFile(localVarsFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	containerOverrides := `is_container: True
iiab_admin_user_install: False
sshd_install: False
sshd_enabled: False
tailscale_install: False
tailscale_enabled: False
remoteit_install: False
remoteit_enabled: False
transmission_install: False
transmission_enabled: False
`
	_, err = f.WriteString(containerOverrides)
	return err
}

// cloneIIABRepo clones the IIAB repository at the specified branch.
func cloneIIABRepo(ctx context.Context, repo, branch, dest string) error {
	if strings.HasPrefix(branch, "refs/pull/") {
		// Clone and fetch PR head
		if err := command.Run(ctx, "git", "clone", "--depth", "1", repo, dest); err != nil {
			return err
		}
		if err := command.Run(ctx, "git", "-C", dest, "fetch", "--depth", "1", repo, branch); err != nil {
			return err
		}
		return command.Run(ctx, "git", "-C", dest, "checkout", "FETCH_HEAD")
	}
	return command.Run(ctx, "git", "clone", "--depth", "1", "--branch", branch, repo, dest)
}

// setupLocalVars handles the three-source resolution for local_vars.
func setupLocalVars(ctx context.Context, buildSubvol, localVars, iiabPath, name string) error {
	iiabVarsPath := "vars/local_vars_" + name + ".yml"
	destDir := filepath.Join(buildSubvol, "etc/iiab")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}

	if localVars == "" {
		// Default: look in IIAB repo
		srcPath := filepath.Join(iiabPath, iiabVarsPath)
		if !state.FileExists(srcPath) {
			return fmt.Errorf("local_vars not found: %s", srcPath)
		}
		return command.Run(ctx, "cp", "--preserve=mode,timestamps", srcPath, filepath.Join(destDir, "local_vars.yml"))
	}

	if strings.HasPrefix(localVars, "/") {
		// Absolute host path
		if !state.FileExists(localVars) {
			return fmt.Errorf("local-vars file not found: %s", localVars)
		}
		return command.Run(ctx, "cp", "--preserve=mode,timestamps", localVars, filepath.Join(destDir, "local_vars.yml"))
	}

	// Relative path: check on host first
	if state.FileExists(localVars) {
		relDir := filepath.Dir(localVars)
		if err := os.MkdirAll(filepath.Join(iiabPath, relDir), 0o755); err != nil {
			return err
		}
		if err := command.Run(
			ctx,
			"cp",
			"--preserve=mode,timestamps",
			localVars,
			filepath.Join(iiabPath, localVars),
		); err != nil {
			return err
		}
		// Also copy to container
		return command.Run(ctx, "cp", "--preserve=mode,timestamps", localVars, filepath.Join(destDir, "local_vars.yml"))
	}

	// Not on host -- look inside IIAB repo
	srcPath := filepath.Join(iiabPath, localVars)
	if !state.FileExists(srcPath) {
		return fmt.Errorf("local_vars not found at %s in IIAB repo", localVars)
	}
	return command.Run(ctx, "cp", "--preserve=mode,timestamps", srcPath, filepath.Join(destDir, "local_vars.yml"))
}

// setupContainerNetworking writes systemd-networkd config inside the container.
func setupContainerNetworking(buildSubvol, ip string) error {
	networkDir := filepath.Join(buildSubvol, "etc/systemd/network")
	if err := os.MkdirAll(networkDir, 0o755); err != nil {
		return err
	}

	// Mask default container network files from /usr/lib/systemd/network/
	// so they don't interfere with our custom configuration.
	// Systemd-networkd picks the first matching file by sort order, and
	// 80-container-host0.network would match before our 99-iiab-host0.network.
	for _, defaultFile := range []string{
		"80-container-host0.network",
		"80-container-vb.network",
	} {
		maskPath := filepath.Join(networkDir, defaultFile)
		os.Remove(maskPath) // Remove if it exists (could be a broken symlink)
		if err := os.Symlink("/dev/null", maskPath); err != nil {
			return fmt.Errorf("failed to mask %s: %w", defaultFile, err)
		}
		slog.Info("Masked default network file", "path", maskPath)
	}

	networkContent := fmt.Sprintf(`[Match]
Kind=veth
Name=host0

[Network]
Address=%s/24
Gateway=%s
DHCP=no
DNS=8.8.8.8
DNS=1.1.1.1
`, ip, Gateway)

	networkFile := filepath.Join(networkDir, "99-iiab-host0.network")
	if err := os.WriteFile(networkFile, []byte(networkContent), 0o644); err != nil {
		return err
	}
	slog.Info("Wrote container network configuration", "path", networkFile, "ip", ip, "gateway", Gateway)

	// Write fallback one-shot service
	serviceDir := filepath.Join(buildSubvol, "etc/systemd/system")
	serviceContent := fmt.Sprintf(`[Unit]
Description=Configure IIAB container network (fallback)
After=systemd-networkd.service
Before=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/sbin/ip addr add %s/24 dev host0
ExecStart=/usr/sbin/ip link set host0 up
ExecStart=/usr/sbin/ip route add default via %s
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
`, ip, Gateway)

	serviceFile := filepath.Join(serviceDir, "iiab-network-setup.service")
	if err := os.WriteFile(serviceFile, []byte(serviceContent), 0o644); err != nil {
		return err
	}

	wantsDir := filepath.Join(serviceDir, "multi-user.target.wants")
	if err := os.MkdirAll(wantsDir, 0o755); err != nil {
		return err
	}
	wantsLink := filepath.Join(wantsDir, "iiab-network-setup.service")
	os.Remove(wantsLink)
	return os.Symlink("/etc/systemd/system/iiab-network-setup.service", wantsLink)
}
