package tests_test

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/build"
)

func TestShellCommand(t *testing.T) {
	requireRoot(t)
	stateDir := setupStateDir(t)
	name := uniqueDemoName("shell-demo")

	// 0. Cleanup on exit
	defer runDemoctl(t, stateDir, "delete", name)

	// 1. Initial build
	_, _, err := runDemoctl(t, stateDir, "build", name, "--skip-install")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 2. Start
	_, _, err = runDemoctl(t, stateDir, "start", name)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 3. Wait for machine to be registered and active in machined
	deadline := time.Now().Add(60 * time.Second)
	machineReady := false
	for time.Now().Before(deadline) {
		out, _ := exec.Command("machinectl", "list").Output()
		if strings.Contains(string(out), name) {
			machineReady = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !machineReady {
		out, _ := exec.Command("machinectl", "list").CombinedOutput()
		t.Fatalf("Machine %s did not become active. machinectl list: %s", name, string(out))
	}

	// Give the container time to boot systemd and D-Bus inside
	time.Sleep(5 * time.Second)

	// 4. Run shell via PTY expect loop
	el, err := build.NewPTYLoop(build.PTYLoopConfig{Stdout: os.Stdout})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer el.Close()

	cmd := newDemoctlCommand(t, stateDir, "shell", name)
	err = el.StartCommand(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Wait for connection message OR prompt (nsenter fallback doesn't show connection message)
	rePromptOrMsg := regexp.MustCompile(`(Connected to machine|[#$]\s*)`)
	_, _, err = el.WaitForAny([]*regexp.Regexp{rePromptOrMsg}, 30*time.Second)
	if err != nil {
		t.Fatalf("Did not see connection message or prompt: %v", err)
	}

	// Send a carriage return to ensure we have a prompt
	_ = el.Send("\r")
	_, _, err = el.WaitForAny([]*regexp.Regexp{regexp.MustCompile(`[#$]\s*`)}, 10*time.Second)
	if err != nil {
		t.Fatalf("Could not find prompt: %v", err)
	}

	// Disconnect:
	// 1. Send exit (for nsenter/bash)
	// 2. Send ctrl-] (for machinectl shell)
	_ = el.SendLine("exit")
	time.Sleep(100 * time.Millisecond)
	for range 3 {
		_ = el.Send("\x1D")
		time.Sleep(100 * time.Millisecond)
	}

	if err := cmd.Wait(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
