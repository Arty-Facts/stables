// Package config owns the versioned, typed controller configuration stored at
// ~/.stables/config.json. Projects, deployments, and events live in the
// SQLite store; this file holds global settings and the default profile.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ConfigVersion is the public config schema version.
const ConfigVersion = 1

// DefaultStateDir is the directory under the user's home dir holding all stab
// runtime state (SQLite DB, keys, logs).
const DefaultStateDir = ".stables"

// Network policy values.
const (
	NetworkDeny      = "deny"      // no network access at all
	NetworkBridge    = "bridge"    // default Docker bridge (needed for code-server web access)
	NetworkAllowlist = "allowlist" // only explicitly allowed endpoints
	NetworkInternet  = "internet"  // full outbound access (explicit opt-in)
)

// Profile is the isolation and resource contract applied to a deployment.
type Profile struct {
	Name             string   `json:"name"`
	Network          string   `json:"network"`
	AllowedEndpoints []string `json:"allowed_endpoints,omitempty"`
	CapDrop          []string `json:"cap_drop,omitempty"`
	Privileged       bool     `json:"privileged"`
	HostNetwork      bool     `json:"host_network"`
	ReadOnlyRoot     bool     `json:"read_only_root"`
	ReadOnlyProject  bool     `json:"read_only_project"`
	CPULimit         string   `json:"cpu_limit,omitempty"`
	MemoryLimit      string   `json:"memory_limit,omitempty"`
	PidsLimit        int      `json:"pids_limit,omitempty"`
	GPU              string   `json:"gpu,omitempty"` // "" = auto (GPU if available), "off" = force CPU, "all" = all GPUs, "0,1" = specific devices
	AllowedImages    []string `json:"allowed_images,omitempty"`
}

// Config is the root configuration document.
type Config struct {
	Version        int               `json:"version"`
	StateDir       string            `json:"state_dir"`
	DefaultImage   string            `json:"default_image"`
	HarnessCommand string            `json:"harness_command"`
	CodeServerPort int               `json:"code_server_port"` // port code-server listens on inside the container
	Profile        Profile           `json:"profile"`
	SecretRefs     map[string]string `json:"secret_refs,omitempty"` // name -> reference, never a value
}

// Default returns the locked-down default configuration.
func Default() Config {
	return Config{
		Version:        ConfigVersion,
		StateDir:       DefaultStateDir,
		DefaultImage:   "stab/runtime:latest",
		HarnessCommand: "pi",
		CodeServerPort: 8080,
		Profile: Profile{
			Name:            "default",
			Network:         NetworkBridge,
			CapDrop:         nil,
			Privileged:      false,
			HostNetwork:     false,
			ReadOnlyRoot:    false,
			ReadOnlyProject: false,
		},
		SecretRefs: map[string]string{},
	}
}

// Path returns the config file path inside the state dir's parent (home).
func Path(home, stateDir string) string {
	if stateDir == "" {
		stateDir = DefaultStateDir
	}
	return filepath.Join(home, stateDir, "config.json")
}

// Load reads and validates the config file, creating it with defaults if it
// does not exist.
func Load(home string) (Config, error) {
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return Config{}, fmt.Errorf("config: resolve home: %w", err)
		}
		home = h
	}
	cfg := Default()
	p := Path(home, cfg.StateDir)

	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			if err := save(p, cfg); err != nil {
				return Config{}, err
			}
			return cfg, nil
		}
		return Config{}, fmt.Errorf("config: read %s: %w", p, err)
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("config: parse %s: %w", p, err)
	}
	normalized := false
	if cfg.Version > ConfigVersion {
		if !isImportablePrivateEraVersion(cfg.Version) {
			return Config{}, fmt.Errorf("config: unsupported version %d", cfg.Version)
		}
		cfg.Version = ConfigVersion
		normalized = true
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	if normalized {
		if err := save(p, cfg); err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

func isImportablePrivateEraVersion(v int) bool {
	return v == 2 || v == 3
}

// Save writes the config back to disk.
func (c Config) Save(home string) error {
	c.Version = ConfigVersion
	if c.StateDir == "" {
		c.StateDir = DefaultStateDir
	}
	if err := c.Validate(); err != nil {
		return err
	}
	p := Path(home, c.StateDir)
	return save(p, c)
}

func save(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("config: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

// Validate checks the config for internal consistency.
func (c Config) Validate() error {
	if c.Version != 0 && c.Version > ConfigVersion {
		return fmt.Errorf("config: unsupported version %d", c.Version)
	}
	if c.DefaultImage == "" {
		return fmt.Errorf("config: default_image must not be empty")
	}
	switch c.Profile.Network {
	case NetworkDeny, NetworkBridge, NetworkAllowlist, NetworkInternet, "":
	default:
		return fmt.Errorf("config: invalid network policy %q", c.Profile.Network)
	}
	if c.CodeServerPort == 0 {
		return fmt.Errorf("config: code_server_port must not be zero")
	}
	return nil
}

// StateDirPath resolves the absolute state directory for the given home.
func (c Config) StateDirPath(home string) string {
	if c.StateDir == "" {
		c.StateDir = DefaultStateDir
	}
	if filepath.IsAbs(c.StateDir) {
		return c.StateDir
	}
	return filepath.Join(home, c.StateDir)
}
