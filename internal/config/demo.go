// Package config handles demo configuration file I/O.
package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
)

// Demo represents a demo's configuration as stored in its config file.
type Demo struct {
	Name          string `toml:"demo_name"`
	Repo          string `toml:"iiab_repo"`
	Branch        string `toml:"iiab_branch"`
	ImageSizeMB   int    `toml:"image_size_mb"`
	UniqueSizeMB  int    `toml:"unique_size_mb"`
	VolatileMode  string `toml:"volatile_mode"`
	BuildOnDisk   bool   `toml:"build_on_disk"`
	SkipInstall   bool   `toml:"skip_install"`
	CleanupFailed bool   `toml:"cleanup_failed"`
	LocalVars     string `toml:"local_vars"`
	Wildcard      bool   `toml:"wildcard"`
	Description   string `toml:"description"`
	BaseName      string `toml:"base_name"`
	Subdomain     string `toml:"subdomain"`
	Status        string `toml:"-"`
	IP            string `toml:"-"`
}

// Resources represents the system resource tracking file.
type Resources struct {
	DiskTotalMB     int `toml:"disk_total_mb"`
	DiskAllocatedMB int `toml:"disk_allocated_mb"`
}

// ReadResources reads the system resource tracking file.
func ReadResources(stateDir string) (*Resources, error) {
	resourceFile := state.ResourceFile(stateDir)
	data, err := os.ReadFile(resourceFile)
	if err != nil {
		return nil, err
	}

	r := &Resources{}
	if err := toml.Unmarshal(data, r); err != nil {
		return nil, fmt.Errorf("cannot unmarshal resources: %w", err)
	}
	return r, nil
}

// WriteResources writes the system resource tracking file as TOML.
func WriteResources(stateDir string, r *Resources) error {
	resourceFile := state.ResourceFile(stateDir)
	data, err := toml.Marshal(r)
	if err != nil {
		return err
	}
	return os.WriteFile(resourceFile, data, 0o644)
}

// Read reads a demo's config from its state directory using go-toml.
func Read(ctx context.Context, stateDir, name string) (*Demo, error) {
	demoDir := state.DemoDir(stateDir, name)
	configPath := filepath.Join(demoDir, "config")

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read config: %w", err)
	}

	d := &Demo{Name: name}
	if err := toml.Unmarshal(data, d); err != nil {
		return nil, fmt.Errorf("cannot unmarshal config: %w", err)
	}

	// Read IP and status from separate files
	ipPath := filepath.Join(demoDir, "ip")
	if ipData, err := os.ReadFile(ipPath); err == nil {
		d.IP = strings.TrimSpace(string(ipData))
	}

	statusPath := filepath.Join(demoDir, "status")
	if statusData, err := os.ReadFile(statusPath); err == nil {
		d.Status = strings.TrimSpace(string(statusData))
	}

	return d, nil
}

// MarshalTOML marshals any value to TOML bytes.
func MarshalTOML(v any) ([]byte, error) {
	return toml.Marshal(v)
}

// Write writes the demo config to the demo's state directory as TOML.
func (d *Demo) Write(stateDir string) error {
	demoDir := state.DemoDir(stateDir, d.Name)
	if err := os.MkdirAll(demoDir, 0o755); err != nil {
		return err
	}
	configPath := filepath.Join(demoDir, "config")

	data, err := toml.Marshal(d)
	if err != nil {
		return fmt.Errorf("cannot marshal config to TOML: %w", err)
	}

	return os.WriteFile(configPath, data, 0o644)
}

// IsTransient returns true if the demo is in a transitional state (building, starting, etc.)
func (d *Demo) IsTransient() bool {
	switch d.Status {
	case "building", "pending", "starting", "stopping":
		return true
	}
	return false
}

// WriteStatus writes the demo status file.
func WriteStatus(stateDir, name, status string) error {
	demoDir := state.DemoDir(stateDir, name)
	return os.WriteFile(filepath.Join(demoDir, "status"), []byte(status), 0o644)
}

// ValidateName checks a demo name for valid characters and length.
func ValidateName(name string) error {
	if name == "" {
		return errors.New("demo name is required")
	}
	if len(name) > 64 {
		return fmt.Errorf("demo name must be 64 characters or less (got %d)", len(name))
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return fmt.Errorf(
				"demo name '%s' contains invalid characters (only letters, numbers, hyphens, underscores allowed)",
				name,
			)
		}
	}
	return nil
}

// List returns all demo names found in the active directory.
func List(stateDir string) ([]string, error) {
	activeDir := state.ActiveDir(stateDir)
	entries, err := os.ReadDir(activeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// GetDemoStatus reads the status of a demo from its status file.
func GetDemoStatus(stateDir, name string) (string, error) {
	statusPath := filepath.Join(state.DemoDir(stateDir, name), "status")
	data, err := os.ReadFile(statusPath)
	if err != nil {
		return "unknown", err
	}
	return strings.TrimSpace(string(data)), nil
}
