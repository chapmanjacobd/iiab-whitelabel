package tests_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

var democtlPath string

func isVerboseTest() bool {
	// Check for -v or -test.v flags before test framework parses them
	for _, arg := range os.Args {
		if arg == "-v" || arg == "-test.v" || arg == "--v" {
			return true
		}
	}
	return false
}

func TestMain(m *testing.M) {
	// Configure verbose logging for tests when -v flag is passed
	opts := &slog.HandlerOptions{
		Level: slog.LevelInfo, // Default level
	}

	// Enable debug level if -v flag is passed
	if isVerboseTest() {
		opts.Level = slog.LevelDebug
	}

	// Set as the global default logger for the test process
	logger := slog.New(slog.NewTextHandler(os.Stderr, opts))
	slog.SetDefault(logger)

	tmpDir, err := os.MkdirTemp("", "democtl-test-bin-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmpDir)

	binName := "democtl"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	democtlPath = filepath.Join(tmpDir, binName)

	// Get root directory (where go.mod is)
	pc, filename, line, ok := runtime.Caller(0)
	if !ok {
		println("failed to get caller info")
		os.Exit(1)
	}
	_ = pc
	_ = line
	rootDir := filepath.Join(filepath.Dir(filename), "..")

	cmd := exec.Command("go", "build", "-o", democtlPath, ".")
	cmd.Dir = rootDir
	if out, err := cmd.CombinedOutput(); err != nil {
		println("failed to build democtl:", err.Error(), "\nOutput:", string(out))
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// runDemoctl runs the built democtl binary with the given arguments.
// It automatically prepends the --state-dir argument and sets up the environment
// with necessary mocks (like IIAB_MOCK_CERTS) to allow tests to run on host.
func runDemoctl(t *testing.T, stateDir string, args ...string) (stdoutStr, stderrStr string, err error) {
	// Prepend state dir if provided
	fullArgs := args
	if stateDir != "" {
		fullArgs = append([]string{"--state-dir", stateDir}, args...)
	}

	cmd := exec.Command(democtlPath, fullArgs...)
	cmd.Env = append(os.Environ(),
		"IIAB_NGINX_CONF="+filepath.Join(stateDir, "nginx.conf"),
		"IIAB_MOCK_CERTS=true",
		"DEMOCTL_VERBOSE=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	return stdout.String(), stderr.String(), err
}

// newDemoctlCommand returns a configured [exec.Cmd] for the built democtl binary.
func newDemoctlCommand(t *testing.T, stateDir string, args ...string) *exec.Cmd {
	fullArgs := args
	if stateDir != "" {
		fullArgs = append([]string{"--state-dir", stateDir}, args...)
	}

	cmd := exec.Command(democtlPath, fullArgs...)
	cmd.Env = append(os.Environ(),
		"IIAB_NGINX_CONF="+filepath.Join(stateDir, "nginx.conf"),
		"IIAB_MOCK_CERTS=true",
		"DEMOCTL_VERBOSE=1")
	return cmd
}

func setupStateDir(t *testing.T) string {
	return t.TempDir()
}

func requireRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("requires root (run with sudo go test ./tests/...)")
	}
}

func uniqueDemoName(prefix string) string {
	return fmt.Sprintf("%s-%x", prefix, time.Now().UnixNano())
}

type statusOutput struct {
	Name   string `toml:"demo_name"`
	Status string `toml:"status"`
	IP     string `toml:"ip"`
}

type listOutput struct {
	Demos []statusOutput `toml:"demos"`
}

func readBuildLog(t *testing.T, stateDir, name string) string {
	logFile := filepath.Join(stateDir, "active", name, "build.log")
	data, err := os.ReadFile(logFile)
	if err != nil {
		return ""
	}
	return string(data)
}

// waitDemoSettled waits for a demo to reach a settled state (stopped, running, or failed)
// using exponential backoff to reduce flakiness on slow systems.
func waitDemoSettled(t *testing.T, stateDir, name string, timeout time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	delay := 100 * time.Millisecond
	maxDelay := 2 * time.Second

	for time.Now().Before(deadline) {
		statusData, err := os.ReadFile(filepath.Join(stateDir, "active", name, "status"))
		if err == nil {
			status := string(statusData)
			// Settled states
			if status == "stopped" || status == "running" || status == "failed" {
				return nil
			}
		}

		time.Sleep(delay)
		// Exponential backoff with cap
		delay = min(delay*2, maxDelay)
	}

	// Timeout - provide helpful error with build log snippet
	buildLog := readBuildLog(t, stateDir, name)
	logSnippet := buildLog
	if len(logSnippet) > 500 {
		logSnippet = logSnippet[len(logSnippet)-500:] + "...(truncated)"
	}
	return &settledTimeoutError{
		name:     name,
		stateDir: stateDir,
		log:      logSnippet,
	}
}

type settledTimeoutError struct {
	name     string
	stateDir string
	log      string
}

func (e *settledTimeoutError) Error() string {
	return "timeout waiting for demo to settle: " + e.name + ". Last build log snippet:\n" + e.log
}
