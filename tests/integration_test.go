package tests_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
)

func checkDemoListEmpty(t *testing.T, stateDir string) {
	t.Helper()
	stdout, _, err := runDemoctl(t, stateDir, "list", "--toml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var list listOutput
	if unmarshalErr := toml.Unmarshal([]byte(stdout), &list); unmarshalErr != nil {
		t.Fatalf("unexpected error: %v", unmarshalErr)
	}
	if len(list.Demos) != 0 {
		t.Errorf("expected empty demo list, got %d demos", len(list.Demos))
	}
}

func checkDemoListHelp(t *testing.T) {
	t.Helper()
	stdout, _, err := runDemoctl(t, "", "list", "--help")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "Show all demos and resource usage") {
		t.Errorf("expected stdout to contain 'Show all demos and resource usage', got: %s", stdout)
	}
}

func checkDemoStatus(t *testing.T, stateDir, name string, expectedStatuses []string) statusOutput {
	t.Helper()
	stdout, _, err := runDemoctl(t, stateDir, "status", name, "--toml")
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	var status statusOutput
	if unmarshalErr := toml.Unmarshal([]byte(stdout), &status); unmarshalErr != nil {
		t.Fatalf("unexpected error: %v", unmarshalErr)
	}
	if status.Name != name {
		t.Errorf("expected name %q, got %q", name, status.Name)
	}
	if slices.Contains(expectedStatuses, status.Status) {
		return status
	}
	t.Errorf("expected status to be one of %v, got %q", expectedStatuses, status.Status)
	return status
}

func checkDemoStatusNotFound(t *testing.T, stateDir string) {
	t.Helper()
	_, stderr, err := runDemoctl(t, stateDir, "status", "nonexistent")
	if err == nil {
		t.Fatalf("expected error for nonexistent demo")
	}
	if !strings.Contains(stderr, "cannot read config") {
		t.Errorf("expected stderr to contain 'cannot read config', got: %s", stderr)
	}
}

func checkDemoListHasDemo(t *testing.T, stateDir, name string) {
	t.Helper()
	stdout, _, err := runDemoctl(t, stateDir, "list", "--toml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var list listOutput
	if unmarshalErr := toml.Unmarshal([]byte(stdout), &list); unmarshalErr != nil {
		t.Fatalf("unexpected error: %v", unmarshalErr)
	}
	if len(list.Demos) == 0 {
		t.Errorf("expected non-empty demo list")
	}
	if len(list.Demos) > 0 && list.Demos[0].Name != name {
		t.Errorf("expected first demo to be %q, got %q", name, list.Demos[0].Name)
	}
}

func checkNginxConfig(t *testing.T, stateDir, name string) {
	t.Helper()
	configPath := filepath.Join(stateDir, "nginx.conf")
	if _, statErr := os.Stat(configPath); os.IsNotExist(statErr) {
		t.Errorf("expected config file %s to exist", configPath)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(data), "upstream "+name) {
		t.Errorf("expected nginx config to contain 'upstream %s', got: %s", name, string(data))
	}
}

func TestIntegration_BuildStatusListReload(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatal("this test must be run as root (use sudo go test ./tests/...)")
	}
	stateDir := setupStateDir(t)

	// --- TestList: empty list ---
	checkDemoListEmpty(t, stateDir)

	// --- TestListHelp ---
	checkDemoListHelp(t)

	// --- Build a shared demo ---
	name := "integration-demo"
	_, _, err := runDemoctl(t, stateDir, "build", name, "--skip-install")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Cleanup on exit
	defer runDemoctl(t, stateDir, "delete", name)

	// Wait for build to settle
	if settleErr := waitDemoSettled(t, stateDir, name, 120*time.Second); settleErr != nil {
		t.Fatalf("unexpected error: %v", settleErr)
	}

	// --- TestStatus ---
	checkDemoStatus(t, stateDir, name, []string{"pending", "building", "stopped"})

	// --- TestStatusNotFound ---
	checkDemoStatusNotFound(t, stateDir)

	// --- TestList: demo present ---
	checkDemoListHasDemo(t, stateDir, name)

	// --- TestReload ---
	_, _, err = runDemoctl(t, stateDir, "start", name)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, _, err = runDemoctl(t, stateDir, "reload")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	checkNginxConfig(t, stateDir, name)

	// --- TestRebuild ---
	stdout, stderr, err := runDemoctl(t, stateDir, "rebuild", name)
	if err != nil {
		t.Fatalf("rebuild failed: %s %s", stdout, stderr)
	}
	if !strings.Contains(stdout+stderr, "Rebuilt successfully") {
		t.Errorf("expected output to contain 'Rebuilt successfully', got: %s%s", stdout, stderr)
	}

	if settleErr := waitDemoSettled(t, stateDir, name, 120*time.Second); settleErr != nil {
		t.Fatalf("unexpected error: %v", settleErr)
	}

	checkDemoStatus(t, stateDir, name, []string{"stopped"})
}
