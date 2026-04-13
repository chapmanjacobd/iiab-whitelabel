// Package tls handles certbot integration for SSL certificate management.
package tls

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/config"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/nginx"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
)

const (
	adminEmail  = "admin@iiab.io"
	certbotRoot = "/var/www/certbot"
)

// SetupCerts obtains Let's Encrypt certificates for all active demos.
func SetupCerts(ctx context.Context, stateDir string) error {
	if err := ensureRoot(); err != nil {
		return err
	}

	if os.Getenv("IIAB_MOCK_CERTS") == "true" {
		slog.InfoContext(ctx, "MOCK: Setting up TLS certificates for active demos")
		return nginx.Generate(ctx, stateDir)
	}

	// Create required directories
	if err := os.MkdirAll(certbotRoot, 0o755); err != nil {
		return fmt.Errorf("cannot create certbot root %s: %w", certbotRoot, err)
	}
	if err := os.MkdirAll("/var/log/nginx", 0o755); err != nil {
		return fmt.Errorf("cannot create nginx log dir: %w", err)
	}

	names, err := config.List(stateDir)
	if err != nil {
		return err
	}

	for _, name := range names {
		demo, err := config.Read(ctx, stateDir, name)
		if err != nil {
			continue
		}

		// Skip transient states
		switch demo.Status {
		case "building", "pending", "starting", "stopping":
			continue
		}

		subdomain := demo.Subdomain
		if subdomain == "" {
			subdomain = state.SanitizeSubdomain(name)
		}
		domain := subdomain + ".iiab.io"

		// Check if cert already exists and is valid for >30 days
		if certValidFor(ctx, domain, 30) {
			slog.InfoContext(ctx, "Certificate valid, skipping", "domain", domain)
			continue
		}

		// Obtain certificate via webroot
		slog.InfoContext(ctx, "Obtaining certificate", "domain", domain)
		tctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		cmd := exec.CommandContext(tctx, "certbot", "certonly", "--webroot",
			"-w", certbotRoot,
			"-d", domain,
			"--email", adminEmail,
			"--agree-tos",
			"--non-interactive",
			"--keep-until-expiring")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			slog.WarnContext(ctx, "Failed to obtain cert", "domain", domain, "error", err)
			cancel()
			continue
		}
		cancel()
	}

	// Enable certbot timer
	tctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	_ = exec.CommandContext(tctx, "systemctl", "enable", "--now", "certbot.timer").Run()

	// Create post-renewal hook
	if err := setupPostRenewalHook(); err != nil {
		slog.WarnContext(ctx, "Failed to setup post-renewal hook", "error", err)
	}

	// Regenerate nginx config with SSL
	return nginx.Generate(ctx, stateDir)
}

// certValidFor checks if a certificate is valid for at least the given number of days.
func certValidFor(ctx context.Context, domain string, days int) bool {
	certFile := fmt.Sprintf("/etc/letsencrypt/live/%s/fullchain.pem", domain)
	if !state.FileExists(certFile) {
		return false
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	// Force C locale so month names are always in English, regardless of system locale.
	cmd := exec.CommandContext(ctx, "openssl", "x509", "-enddate", "-noout", "-in", certFile)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return false
	}

	// Parse "notAfter=Apr 10 12:00:00 2026 GMT"
	line := strings.TrimSpace(string(out))
	if !strings.HasPrefix(line, "notAfter=") {
		return false
	}

	dateStr := strings.TrimPrefix(line, "notAfter=")
	expiry, err := time.Parse("Jan 2 15:04:05 2006 MST", dateStr)
	if err != nil {
		return false
	}

	return time.Until(expiry) > time.Duration(days)*24*time.Hour
}

// setupPostRenewalHook creates the certbot post-renewal hook for nginx reload.
func setupPostRenewalHook() error {
	hookPath := "/etc/letsencrypt/renewal-hooks/post/reload-nginx.sh"
	content := `#!/usr/bin/env bash
nginx -t && systemctl reload nginx
`
	return os.WriteFile(hookPath, []byte(content), 0o755)
}

func ensureRoot() error {
	if os.Geteuid() == 0 {
		return nil
	}
	return errors.New("must run as root")
}
