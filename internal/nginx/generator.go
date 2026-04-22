// Package nginx generates dynamic nginx configuration from active demos.
package nginx

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/config"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/lock"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/network"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
)

const (
	sitesAvailablePath = "/etc/nginx/sites-available/iiab-demo.conf"
	sitesEnabledPath   = "/etc/nginx/sites-enabled/iiab-demo.conf"
	confDPath          = "/etc/nginx/conf.d/iiab-demo.conf"
)

// GetNginxPaths returns the config and enabled paths for the iiab-demo nginx config.
// It detects the nginx config style (sites-available vs conf.d) based on filesystem state.
func GetNginxPaths() (configPath, enabledPath string) {
	if p := os.Getenv("IIAB_NGINX_CONF"); p != "" {
		return p, p + ".enabled"
	}

	if state.FileExists("/etc/nginx/sites-available") {
		return sitesAvailablePath, sitesEnabledPath
	}

	// Default to conf.d style (no symlink needed usually, so enabledPath is same as configPath)
	return confDPath, confDPath
}

// DemoEntry represents a demo for nginx templating.
type DemoEntry struct {
	Name      string
	Subdomain string
	IP        string
	Port      string
	HasSSL    bool
	Wildcard  bool
}

// Generate regenerates the nginx config from all active demos.
func Generate(ctx context.Context, stateDir string) error {
	// Use flock to prevent concurrent generation.
	// 30s timeout balances between waiting for in-progress generation and not hanging indefinitely.
	lockFile := filepath.Join(stateDir, ".nginx-gen.lock")
	l, err := lock.AcquireShort(ctx, lockFile)
	if err != nil {
		return fmt.Errorf("cannot acquire nginx generation lock: %w", err)
	}
	defer l.Release() //nolint:errcheck // unlock is best-effort for this advisory lock

	names, listErr := config.List(stateDir)
	if listErr != nil {
		return listErr
	}

	entries, wildcardFound := collectDemoEntries(ctx, stateDir, names)
	renderedConfig, err := renderConfig(entries, wildcardFound)
	if err != nil {
		return err
	}

	return writeAndReloadConfig(ctx, renderedConfig)
}

func hasValidCert(subdomain string) bool {
	// Check the full chain (symlink in live/ → actual file in archive/)
	fullchain := fmt.Sprintf("/etc/letsencrypt/live/%s.iiab.io/fullchain.pem", subdomain)
	privkey := fmt.Sprintf("/etc/letsencrypt/live/%s.iiab.io/privkey.pem", subdomain)

	// Both must exist
	if !state.FileExists(fullchain) || !state.FileExists(privkey) {
		return false
	}

	// Symlink might exist but point to missing archive entry; verify we can actually read them
	fullData, err1 := os.ReadFile(fullchain)
	_, err2 := os.ReadFile(privkey)
	if err1 != nil || err2 != nil {
		return false
	}

	// Must contain actual PEM content (not an empty file)
	return len(fullData) > 0
}

func collectDemoEntries(ctx context.Context, stateDir string, names []string) ([]DemoEntry, *DemoEntry) {
	var entries []DemoEntry
	var wildcardFound *DemoEntry

	for _, name := range names {
		demo, err := config.Read(ctx, stateDir, name)
		if err != nil {
			continue
		}

		if demo.IsTransient() {
			continue
		}

		subdomain := demo.Subdomain
		if subdomain == "" {
			subdomain = state.SanitizeSubdomain(name)
		}
		entry := DemoEntry{
			Name:      name,
			Subdomain: subdomain,
			IP:        demo.IP,
			Port:      "80",
			Wildcard:  demo.Wildcard,
		}

		if hasValidCert(subdomain) {
			entry.HasSSL = true
		}

		entries = append(entries, entry)
		if demo.Wildcard {
			wildcardFound = &entry
		}
	}
	return entries, wildcardFound
}

func renderConfig(entries []DemoEntry, wildcard *DemoEntry) (string, error) {
	tmplStr := MainConfig
	if len(entries) == 0 {
		tmplStr = FallbackConfig
	}

	tmpl, err := template.New("nginx").Parse(tmplStr)
	if err != nil {
		return "", err
	}

	var buf strings.Builder
	if e := tmpl.Execute(&buf, struct {
		Demos      []DemoEntry
		Wildcard   *DemoEntry
		BridgeName string
		Gateway    string
	}{
		Demos:      entries,
		Wildcard:   wildcard,
		BridgeName: network.BridgeName,
		Gateway:    network.Gateway,
	}); e != nil {
		return "", e
	}

	return buf.String(), nil
}

func writeAndReloadConfig(ctx context.Context, renderedConfig string) error {
	configPath, enabledPath := GetNginxPaths()

	if e := os.WriteFile(configPath, []byte(renderedConfig), 0o644); e != nil {
		return fmt.Errorf("cannot write nginx config: %w", e)
	}

	if configPath != enabledPath {
		os.Remove(enabledPath)
		if e := os.Symlink(configPath, enabledPath); e != nil {
			return e
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	testCmd := exec.CommandContext(ctx, "nginx", "-t")
	testOut, err := testCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nginx config test failed: %s", strings.TrimSpace(string(testOut)))
	}

	if err := exec.CommandContext(ctx, "systemctl", "reload", "nginx").Run(); err != nil {
		slog.WarnContext(ctx, "Failed to reload nginx (might not be active yet)", "error", err)
	}

	fmt.Println("Nginx config regenerated and reloaded")
	return nil
}

// FallbackConfig is the nginx config used when there are no active demos.
const FallbackConfig = `# No active demos - fallback 404
server {
    listen 80 default_server;
    listen [::]:80 default_server;
    server_name _;

    location /.well-known/acme-challenge/ {
        root /var/www/certbot;
    }

    location / {
        return 404;
    }
}
`

// MainConfig is the main nginx config template for active demos.
const MainConfig = `# Auto-generated by democtl reload
{{range .Demos}}
upstream {{.Subdomain}} {
    server {{.IP}}:80;
    keepalive 32;
}
{{end}}

# HTTP fallback catch-all
server {
    listen 80 default_server;
    listen [::]:80 default_server;
    server_name _;
    {{if and .Wildcard .Wildcard.HasSSL}}
    return 302 https://{{.Wildcard.Subdomain}}.iiab.io$request_uri;
    {{else}}
    return 404;
    {{end}}
}

{{range .Demos}}
# {{.Name}} - HTTP
server {
    listen 80;
    listen [::]:80;
    server_name {{.Subdomain}}.iiab.io;
    {{if .HasSSL}}
    return 301 https://$host$request_uri;
    {{else}}
    location / {
        proxy_pass http://{{.Subdomain}};
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
    {{end}}
}
{{end}}

{{range .Demos}}
{{if .HasSSL}}
# {{.Name}} - HTTPS
server {
    listen 443 ssl;
    listen [::]:443 ssl;
    server_name {{.Subdomain}}.iiab.io;

    ssl_certificate /etc/letsencrypt/live/{{.Subdomain}}.iiab.io/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/{{.Subdomain}}.iiab.io/privkey.pem;

    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;
    ssl_prefer_server_ciphers on;

    location / {
        proxy_pass http://{{.Subdomain}};
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Host $host;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
{{end}}
{{end}}

# HTTPS fallback catch-all
{{if and .Wildcard .Wildcard.HasSSL}}
server {
    listen 443 ssl default_server;
    listen [::]:443 ssl default_server;
    server_name _;
    ssl_certificate /etc/letsencrypt/live/{{.Wildcard.Subdomain}}.iiab.io/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/{{.Wildcard.Subdomain}}.iiab.io/privkey.pem;
    return 302 https://{{.Wildcard.Subdomain}}.iiab.io$request_uri;
}
{{end}}
`
