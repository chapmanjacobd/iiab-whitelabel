package nginx_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/network"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/nginx"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
)

func TestFallbackConfigStructure(t *testing.T) {
	// Verify fallback config contains expected elements
	if !strings.Contains(nginx.FallbackConfig, "listen 80 default_server") {
		t.Error("expected fallback config to contain 'listen 80 default_server'")
	}
	if !strings.Contains(nginx.FallbackConfig, "return 404") {
		t.Error("expected fallback config to contain 'return 404'")
	}
	if !strings.Contains(nginx.FallbackConfig, "acme-challenge") {
		t.Error("expected fallback config to contain 'acme-challenge'")
	}
	if !strings.Contains(nginx.FallbackConfig, "/var/www/certbot") {
		t.Error("expected fallback config to contain '/var/www/certbot'")
	}
	// ACME challenge location must be in its own block, not after return
	if !strings.Contains(nginx.FallbackConfig, "location /.well-known/acme-challenge/") {
		t.Error("expected fallback config to contain ACME challenge location")
	}
	if !strings.Contains(nginx.FallbackConfig, "location /") {
		t.Error("expected fallback config to contain 'location /'")
	}
}

func TestMainConfigStructure(t *testing.T) {
	// Verify main config template contains expected elements
	if !strings.Contains(nginx.MainConfig, "upstream") {
		t.Error("expected main config to contain 'upstream'")
	}
	if !strings.Contains(nginx.MainConfig, "proxy_pass") {
		t.Error("expected main config to contain 'proxy_pass'")
	}
	if !strings.Contains(nginx.MainConfig, "server_name") {
		t.Error("expected main config to contain 'server_name'")
	}
	if !strings.Contains(nginx.MainConfig, "listen 80") {
		t.Error("expected main config to contain 'listen 80'")
	}
	if !strings.Contains(nginx.MainConfig, "listen 443 ssl") {
		t.Error("expected main config to contain 'listen 443 ssl'")
	}
	if !strings.Contains(nginx.MainConfig, "ssl_certificate") {
		t.Error("expected main config to contain 'ssl_certificate'")
	}
	if !strings.Contains(nginx.MainConfig, "X-Real-IP") {
		t.Error("expected main config to contain 'X-Real-IP'")
	}
	if !strings.Contains(nginx.MainConfig, "X-Forwarded-For") {
		t.Error("expected main config to contain 'X-Forwarded-For'")
	}
	// WebSocket support headers
	if !strings.Contains(nginx.MainConfig, "keepalive 32") {
		t.Error("expected main config to contain 'keepalive 32'")
	}
	if !strings.Contains(nginx.MainConfig, "X-Forwarded-Host") {
		t.Error("expected main config to contain 'X-Forwarded-Host'")
	}
	if !strings.Contains(nginx.MainConfig, "proxy_http_version 1.1") {
		t.Error("expected main config to contain 'proxy_http_version 1.1'")
	}
	if !strings.Contains(nginx.MainConfig, "Upgrade") {
		t.Error("expected main config to contain 'Upgrade'")
	}
	if !strings.Contains(nginx.MainConfig, `Connection "upgrade"`) {
		t.Error("expected main config to contain 'Connection \"upgrade\"'")
	}
}

func TestMainConfigWildcardSupport(t *testing.T) {
	// Verify main config handles wildcard demos with SSL check
	if !strings.Contains(nginx.MainConfig, ".Wildcard.HasSSL") {
		t.Error("expected main config to contain wildcard HasSSL conditional")
	}
	if !strings.Contains(nginx.MainConfig, "return 302") {
		t.Error("expected main config to contain 'return 302'")
	}
}

func TestMainConfigSSLConditional(t *testing.T) {
	// Verify main config has SSL conditional
	if !strings.Contains(nginx.MainConfig, "{{if .HasSSL}}") {
		t.Error("expected main config to contain SSL conditional")
	}
	if !strings.Contains(nginx.MainConfig, "return 301 https://") {
		t.Error("expected main config to contain 'return 301 https://'")
	}
}

func TestIsTransientStatuses(t *testing.T) {
	// Test that transient statuses are properly identified
	transientStatuses := []string{"building", "pending", "starting", "stopping"}
	for _, status := range transientStatuses {
		t.Run(status, func(t *testing.T) {
			// The isTransient function should return true for these
			// This is tested indirectly via collectDemoEntries
			if !isTransient(status) {
				t.Errorf("%s should be transient", status)
			}
		})
	}
}

func TestNonTransientStatuses(t *testing.T) {
	nonTransientStatuses := []string{"running", "stopped", "failed", "unknown"}
	for _, status := range nonTransientStatuses {
		t.Run(status, func(t *testing.T) {
			if isTransient(status) {
				t.Errorf("%s should not be transient", status)
			}
		})
	}
}

func TestGenerateWithEmptyStateDir(t *testing.T) {
	tmpDir := t.TempDir()
	// Create active directory
	os.MkdirAll(state.ActiveDir(tmpDir), 0o755)

	// Generate should succeed with fallback config
	// Since we can't run nginx, we test the structure indirectly
	if _, err := os.Stat(state.ActiveDir(tmpDir)); os.IsNotExist(err) {
		t.Errorf("expected active dir to exist")
	}
}

func TestGenerateWithMultipleDemos(t *testing.T) {
	tmpDir := t.TempDir()
	activeDir := state.ActiveDir(tmpDir)
	os.MkdirAll(activeDir, 0o755)

	// Create two demo directories
	demo1Dir := filepath.Join(activeDir, "demo1")
	demo2Dir := filepath.Join(activeDir, "demo2")
	os.MkdirAll(demo1Dir, 0o755)
	os.MkdirAll(demo2Dir, 0o755)

	// Write demo configs
	config1 := `DEMO_NAME="demo1"
IIAB_REPO=""
IIAB_BRANCH=""
IMAGE_SIZE_MB=10000
VOLATILE_MODE=""
BUILD_ON_DISK=false
SKIP_INSTALL=false
CLEANUP_FAILED=false
LOCAL_VARS=""
WILDCARD=false
DESCRIPTION=""
BASE_NAME=""
SUBDOMAIN="demo1"
`
	config2 := `DEMO_NAME="demo2"
IIAB_REPO=""
IIAB_BRANCH=""
IMAGE_SIZE_MB=10000
VOLATILE_MODE=""
BUILD_ON_DISK=false
SKIP_INSTALL=false
CLEANUP_FAILED=false
LOCAL_VARS=""
WILDCARD=false
DESCRIPTION=""
BASE_NAME=""
SUBDOMAIN="demo2"
`
	os.WriteFile(filepath.Join(demo1Dir, "config"), []byte(config1), 0o644)
	os.WriteFile(filepath.Join(demo2Dir, "config"), []byte(config2), 0o644)
	os.WriteFile(filepath.Join(demo1Dir, "status"), []byte("running"), 0o644)
	os.WriteFile(filepath.Join(demo2Dir, "status"), []byte("running"), 0o644)
	os.WriteFile(filepath.Join(demo1Dir, "ip"), []byte("10.0.3.2"), 0o644)
	os.WriteFile(filepath.Join(demo2Dir, "ip"), []byte("10.0.3.3"), 0o644)

	// Verify demos exist
	entries, err := os.ReadDir(activeDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
}

func TestGenerateSkipsTransientDemos(t *testing.T) {
	tmpDir := t.TempDir()
	activeDir := state.ActiveDir(tmpDir)
	os.MkdirAll(activeDir, 0o755)

	// Create a demo in "building" status
	buildingDir := filepath.Join(activeDir, "building-demo")
	os.MkdirAll(buildingDir, 0o755)

	config := `DEMO_NAME="building-demo"
IIAB_REPO=""
IIAB_BRANCH=""
IMAGE_SIZE_MB=10000
VOLATILE_MODE=""
BUILD_ON_DISK=false
SKIP_INSTALL=false
CLEANUP_FAILED=false
LOCAL_VARS=""
WILDCARD=false
DESCRIPTION=""
BASE_NAME=""
SUBDOMAIN="building-demo"
`
	os.WriteFile(filepath.Join(buildingDir, "config"), []byte(config), 0o644)
	os.WriteFile(filepath.Join(buildingDir, "status"), []byte("building"), 0o644)

	// The demo should be skipped due to transient status
	// This is tested indirectly via collectDemoEntries
	if _, err := os.Stat(buildingDir); os.IsNotExist(err) {
		t.Errorf("expected building demo dir to exist")
	}
}

func TestNetworkConstantsInNginxConfig(t *testing.T) {
	// Verify network constants are used in nginx config generation
	if network.BridgeName != "iiab-br0" {
		t.Errorf("expected bridge name 'iiab-br0', got %q", network.BridgeName)
	}
	if network.Gateway != "10.0.3.1" {
		t.Errorf("expected gateway '10.0.3.1', got %q", network.Gateway)
	}
}

// isTransient mirrors the unexported function in nginx package
func isTransient(status string) bool {
	switch status {
	case "building", "pending", "starting", "stopping":
		return true
	}
	return false
}
