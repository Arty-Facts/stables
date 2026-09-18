package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultNetworkIsBridge(t *testing.T) {
	cfg := Default()
	if cfg.Profile.Network != NetworkBridge {
		t.Fatalf("default network should be bridge (code-server web access), got %q", cfg.Profile.Network)
	}
	if cfg.CodeServerPort != 8080 {
		t.Fatalf("default code-server port should be 8080, got %d", cfg.CodeServerPort)
	}
	if cfg.HarnessCommand != "pi" {
		t.Fatalf("default harness should be pi, got %q", cfg.HarnessCommand)
	}
	if cfg.Profile.ReadOnlyRoot {
		t.Fatalf("default rootfs should be writable for code-server")
	}
	if len(cfg.Profile.CapDrop) != 0 {
		t.Fatalf("default should not drop capabilities (code-server entrypoint needs them)")
	}
}

func TestLoadCreatesVersionOneConfig(t *testing.T) {
	home := t.TempDir()
	cfg, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != 1 {
		t.Fatalf("expected first public config version 1, got %d", cfg.Version)
	}
	if _, err := os.Stat(Path(home, DefaultStateDir)); err != nil {
		t.Fatalf("config file should be created: %v", err)
	}
}

func TestLoadImportsPrivateEraVersionThreeConfig(t *testing.T) {
	home := t.TempDir()
	p := Path(home, DefaultStateDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{
  "version": 3,
  "state_dir": ".stables",
  "default_image": "stab/runtime:latest",
  "harness_command": "pi",
  "code_server_port": 8080,
  "profile": {
    "name": "default",
    "network": "bridge",
    "privileged": false,
    "host_network": false,
    "read_only_root": false,
    "read_only_project": false
  }
}`)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != ConfigVersion {
		t.Fatalf("expected imported config version %d, got %d", ConfigVersion, cfg.Version)
	}
	loaded, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(loaded), `"version": 3`) {
		t.Fatalf("expected normalized config persisted, got:\n%s", loaded)
	}
}

func TestValidateRejectsBadNetwork(t *testing.T) {
	cfg := Default()
	cfg.Profile.Network = "bogus"
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected validation error for bad network")
	}
}
