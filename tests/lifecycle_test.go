package tests_test

import (
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
)

func TestLifecycleCommand(t *testing.T) {
	stateDir := setupStateDir(t)
	name := "demo-lifecycle"

	// 1. Build
	stdout, stderr, err := runDemoctl(t, stateDir, "build", name, "--skip-install")
	if err != nil {
		t.Fatalf("build failed: %s %s", stdout, stderr)
	}

	// Wait for background build to finish
	err = waitDemoSettled(t, stateDir, name, 120*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Cleanup storage on exit (btrfs subvolumes are global, not under state-dir)
	defer runDemoctl(t, stateDir, "delete", name)

	// 2. Status check
	stdout, _, err = runDemoctl(t, stateDir, "status", name, "--toml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var status statusOutput
	if unmarshalErr := toml.Unmarshal([]byte(stdout), &status); unmarshalErr != nil {
		t.Fatalf("unexpected error: %v", unmarshalErr)
	}
	if status.Name != name {
		t.Errorf("expected name %q, got %q", name, status.Name)
	}

	// 3. Start
	stdout, stderr, err = runDemoctl(t, stateDir, "start", name)
	if err != nil {
		t.Fatalf("start failed: %s %s", stdout, stderr)
	}

	// Wait for start to reflect in status
	time.Sleep(1 * time.Second)

	// 4. Status check
	stdout, _, err = runDemoctl(t, stateDir, "status", name, "--toml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if unmarshalErr := toml.Unmarshal([]byte(stdout), &status); unmarshalErr != nil {
		t.Fatalf("unexpected error: %v", unmarshalErr)
	}
	if status.Status != "running" {
		t.Errorf("expected status 'running', got %q", status.Status)
	}

	// 5. Restart
	stdout, stderr, err = runDemoctl(t, stateDir, "restart", name)
	if err != nil {
		t.Fatalf("restart failed: %s %s", stdout, stderr)
	}

	// Wait for restart to reflect
	time.Sleep(1 * time.Second)

	// 6. Stop
	stdout, stderr, err = runDemoctl(t, stateDir, "stop", name)
	if err != nil {
		t.Fatalf("stop failed: %s %s", stdout, stderr)
	}

	// Wait for stop to reflect
	time.Sleep(1 * time.Second)

	// 7. Status check
	stdout, _, err = runDemoctl(t, stateDir, "status", name, "--toml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if unmarshalErr := toml.Unmarshal([]byte(stdout), &status); unmarshalErr != nil {
		t.Fatalf("unexpected error: %v", unmarshalErr)
	}
	if status.Status != "stopped" {
		t.Errorf("expected status 'stopped', got %q", status.Status)
	}

	// 8. Delete
	stdout, stderr, err = runDemoctl(t, stateDir, "delete", name)
	if err != nil {
		t.Fatalf("delete failed: %s %s", stdout, stderr)
	}

	// 9. List
	stdout, _, err = runDemoctl(t, stateDir, "list", "--toml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var list listOutput
	if unmarshalErr := toml.Unmarshal([]byte(stdout), &list); unmarshalErr != nil {
		t.Fatalf("unexpected error: %v", unmarshalErr)
	}
	for _, d := range list.Demos {
		if d.Name == name {
			t.Errorf("expected demo %q to be deleted", name)
		}
	}

	// 10. Status check (should fail)
	_, _, err = runDemoctl(t, stateDir, "status", name)
	if err == nil {
		t.Errorf("expected error for status of deleted demo")
	}
}
