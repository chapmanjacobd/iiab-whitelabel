package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/command"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/network"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/nginx"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
)

// InitCmd initializes the host for IIAB demos.
type InitCmd struct{}

// Run executes the init command.
func (c *InitCmd) Run(ctx context.Context, globals *GlobalOptions) error {
	if err := ensureRoot(); err != nil {
		return err
	}

	slog.InfoContext(ctx, "Setting up IIAB Demo Server Host")

	// Step 1: Install required packages
	if err := installRequiredPackages(ctx); err != nil {
		return fmt.Errorf("package installation failed: %w", err)
	}

	// Step 2: Enable and start systemd services
	if err := enableSystemdServices(ctx); err != nil {
		return fmt.Errorf("failed to enable systemd services: %w", err)
	}

	// Step 3: Enable IPv4 forwarding
	if err := enableIPv4Forwarding(ctx); err != nil {
		return fmt.Errorf("failed to enable IPv4 forwarding: %w", err)
	}

	// Step 4: Create container bridge network
	if err := network.SetupBridge(ctx, globals.System); err != nil {
		return fmt.Errorf("failed to setup bridge: %w", err)
	}

	// Step 5: Deploy nginx configuration
	if err := deployNginxConfig(ctx); err != nil {
		return fmt.Errorf("failed to deploy nginx config: %w", err)
	}

	// Step 6: Configure nftables for container NAT and isolation
	if err := network.SetupNAT(ctx, globals.System); err != nil {
		return fmt.Errorf("failed to setup nftables: %w", err)
	}

	// Step 7: Create required directories
	if err := ensureStateDirs(globals); err != nil {
		return fmt.Errorf("failed to create state dirs: %w", err)
	}

	// Step 8: Test nginx configuration
	if err := testNginxConfig(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: nginx config test failed: %v\n", err)
	}

	slog.InfoContext(ctx, "Host setup complete", "next_steps", []string{
		"democtl build small --size 12000",
		"democtl build medium --base small --size 8000",
		"democtl build large --base medium --wildcard",
	})
	return nil
}

// installRequiredPackages installs all required packages.
func installRequiredPackages(ctx context.Context) error {
	pm := detectPackageManager()
	if pm == "" {
		return errors.New("no supported package manager found (apt-get, dnf, pacman, zypper)")
	}

	packages := []string{
		"nginx",
		"systemd-container",
		"nftables",
		"curl",
		"git",
		"certbot",
		"btrfs-progs",
		"iptables",
		"tar",
		"util-linux",
	}

	// Add package-manager specific overrides
	switch pm {
	case "pacman":
		packages = append(packages, "certbot-nginx", "xz", "iproute2")
	case "apt-get":
		packages = append(packages, "python3-certbot-nginx", "xz-utils", "iproute2")
	case "dnf", "zypper":
		packages = append(packages, "python3-certbot-nginx", "xz", "iproute")
	}

	// Check which packages are missing
	var missing []string
	for _, pkg := range packages {
		if !isPackageInstalled(ctx, pm, pkg) {
			missing = append(missing, pkg)
		}
	}

	if len(missing) == 0 {
		slog.InfoContext(ctx, "All required packages already installed")
		return nil
	}

	slog.InfoContext(ctx, "Installing packages via "+pm, "packages", missing)

	switch pm {
	case "dnf":
		args := append([]string{"install", "-y"}, missing...)
		if err := command.Run(ctx, "dnf", args...); err != nil {
			return fmt.Errorf("dnf install failed: %w", err)
		}
	case "apt-get":
		if err := command.Run(ctx, "apt-get", "update", "-qq"); err != nil {
			return fmt.Errorf("apt-get update failed: %w", err)
		}
		args := append([]string{"install", "-y"}, missing...)
		if err := command.Run(ctx, "apt-get", args...); err != nil {
			return fmt.Errorf("apt-get install failed: %w", err)
		}
	case "pacman":
		args := append([]string{"-S", "--noconfirm", "--needed"}, missing...)
		if err := command.Run(ctx, "pacman", args...); err != nil {
			return fmt.Errorf("pacman install failed: %w", err)
		}
	case "zypper":
		args := append([]string{"install", "-y"}, missing...)
		if err := command.Run(ctx, "zypper", args...); err != nil {
			return fmt.Errorf("zypper install failed: %w", err)
		}
	}

	return nil
}

func detectPackageManager() string {
	for _, pm := range []string{"apt-get", "dnf", "pacman", "zypper"} {
		if _, err := exec.LookPath(pm); err == nil {
			return pm
		}
	}
	return ""
}

// isPackageInstalled checks if a package is installed.
func isPackageInstalled(ctx context.Context, pm, name string) bool {
	tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	switch pm {
	case "dnf", "zypper":
		cmd = exec.CommandContext(tctx, "rpm", "-q", name)
	case "apt-get":
		cmd = exec.CommandContext(tctx, "dpkg", "-l", name)
	case "pacman":
		cmd = exec.CommandContext(tctx, "pacman", "-Qq", name)
	default:
		return false
	}

	output, err := cmd.Output()
	if err != nil {
		return false
	}

	if pm == "apt-get" {
		// dpkg -l shows 'ii' for installed
		return strings.Contains(string(output), "ii  "+name) || strings.Contains(string(output), "ii "+name+" ")
	}

	return true // for rpm and pacman, exit code 0 is enough
}

// enableSystemdServices enables and starts systemd-networkd and systemd-resolved.
func enableSystemdServices(ctx context.Context) error {
	services := []string{"systemd-networkd", "systemd-resolved"}

	for _, svc := range services {
		if isServiceActive(ctx, svc) {
			slog.InfoContext(ctx, "Service already running", "service", svc)
			continue
		}

		slog.InfoContext(ctx, "Starting service", "service", svc)
		if err := command.Run(ctx, "systemctl", "enable", "--now", svc); err != nil {
			return fmt.Errorf("failed to enable %s: %w", svc, err)
		}
	}

	return nil
}

// isServiceActive checks if a systemd service is active.
func isServiceActive(ctx context.Context, name string) bool {
	tctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(tctx, "systemctl", "is-active", "--quiet", name)
	return cmd.Run() == nil
}

// enableIPv4Forwarding enables IPv4 forwarding persistently.
func enableIPv4Forwarding(ctx context.Context) error {
	// Check if already enabled
	data, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err == nil && strings.TrimSpace(string(data)) == "1" {
		slog.InfoContext(ctx, "IPv4 forwarding already enabled")
		return nil
	}

	slog.InfoContext(ctx, "Enabling IPv4 forwarding")
	if sysctlErr := command.Run(ctx, "sysctl", "-w", "net.ipv4.ip_forward=1"); sysctlErr != nil {
		return fmt.Errorf("sysctl failed: %w", sysctlErr)
	}

	// Make it persistent
	sysctlConf := "/etc/sysctl.conf"
	content, err := os.ReadFile(sysctlConf)
	if err != nil {
		content = []byte{}
	}

	if !strings.Contains(string(content), "net.ipv4.ip_forward=1") {
		f, err := os.OpenFile(sysctlConf, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("cannot open sysctl.conf: %w", err)
		}
		defer f.Close()

		if _, err := f.WriteString("\nnet.ipv4.ip_forward=1\n"); err != nil {
			return fmt.Errorf("cannot write to sysctl.conf: %w", err)
		}
		slog.InfoContext(ctx, "Made IPv4 forwarding persistent")
	}

	return nil
}

// deployNginxConfig deploys the initial nginx configuration.
func deployNginxConfig(ctx context.Context) error {
	slog.InfoContext(ctx, "Configuring nginx")

	configPath, enabledPath := nginx.GetNginxPaths()

	// Create directories for the detected config style
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("cannot create %s: %w", filepath.Dir(configPath), err)
	}
	if configPath != enabledPath {
		if err := os.MkdirAll(filepath.Dir(enabledPath), 0o755); err != nil {
			return fmt.Errorf("cannot create %s: %w", filepath.Dir(enabledPath), err)
		}
	}

	if err := os.MkdirAll("/var/www/certbot", 0o755); err != nil {
		return fmt.Errorf("cannot create /var/www/certbot: %w", err)
	}

	if !state.FileExists(configPath) {
		slog.InfoContext(ctx, "Creating initial nginx config")
		config := `# Auto-generated by democtl init
# This will be regenerated by democtl reload

server {
    listen 80 default_server;
    listen [::]:80 default_server;
    server_name _;

    location / {
        return 404;
    }
}
`
		if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
			return fmt.Errorf("cannot write nginx config: %w", err)
		}
	} else {
		slog.InfoContext(ctx, "Nginx config already exists")
	}

	if configPath != enabledPath {
		if err := enableNginxSite(ctx, configPath, enabledPath); err != nil {
			return err
		}
	}

	// Enable and start nginx (idempotent)
	if isServiceActive(ctx, "nginx") {
		slog.InfoContext(ctx, "nginx already running")
	} else {
		slog.InfoContext(ctx, "Starting nginx")
		if err := command.Run(ctx, "systemctl", "enable", "--now", "nginx"); err != nil {
			return fmt.Errorf("failed to start nginx: %w", err)
		}
	}

	return nil
}

// enableNginxSite enables the nginx site by symlinking it.
func enableNginxSite(ctx context.Context, configPath, enabledPath string) error {
	// Enable site (idempotent)
	if !state.FileExists(enabledPath) {
		slog.InfoContext(ctx, "Enabling nginx site")
		if err := os.Symlink(configPath, enabledPath); err != nil {
			return fmt.Errorf("cannot symlink nginx site: %w", err)
		}
	} else {
		slog.InfoContext(ctx, "Nginx site already enabled")
	}

	// Remove default site (idempotent)
	defaultSite := filepath.Join(filepath.Dir(enabledPath), "default")
	if state.FileExists(defaultSite) {
		slog.InfoContext(ctx, "Removing default nginx site")
		if err := os.Remove(defaultSite); err != nil {
			slog.WarnContext(ctx, "Cannot remove default site", "error", err)
		}
	} else {
		slog.InfoContext(ctx, "Default nginx site already removed")
	}

	return nil
}

// testNginxConfig tests the nginx configuration.
func testNginxConfig(ctx context.Context) error {
	return command.Run(ctx, "nginx", "-t")
}
