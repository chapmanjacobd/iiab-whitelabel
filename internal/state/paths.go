// Package state provides constants and helpers for managing demo state directories.
package state

import (
	"os"
	"path/filepath"
)

const (
	// ResourceFileName is the name of the resource tracking file.
	ResourceFileName = "resources"

	// LockFileName is the default lock file name.
	LockFileName = ".democtl.lock"

	// IIABBridge is the bridge name for IIAB demos.
	IIABBridge     = "iiab-br0"
	IIABSubnetBase = "10.0.3"
	IIABGateway    = "10.0.3.1"
	IIABDemoSubnet = "10.0.3.0/24"
)

// ActiveDir returns the path to the active demos directory.
func ActiveDir(stateDir string) string {
	return filepath.Join(stateDir, "active")
}

// ResourceFile returns the path to the resource tracking file.
func ResourceFile(stateDir string) string {
	return filepath.Join(stateDir, ResourceFileName)
}

// LockFile returns the path to the lock file.
func LockFile(stateDir string) string {
	return filepath.Join(stateDir, LockFileName)
}

// DemoDir returns the path to a specific demo's directory.
func DemoDir(stateDir, name string) string {
	return filepath.Join(ActiveDir(stateDir), name)
}

// FileExists checks if a file exists.
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ReadFile reads a file's contents, returning empty string if not found.
func ReadFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WriteFile writes content to a file, creating directories as needed.
func WriteFile(path, content string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), perm)
}

// WriteIP writes the demo IP file.
func WriteIP(stateDir, name, ip string) error {
	demoDir := DemoDir(stateDir, name)
	return os.WriteFile(filepath.Join(demoDir, "ip"), []byte(ip), 0o644)
}
