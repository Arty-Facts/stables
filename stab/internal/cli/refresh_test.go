package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stables/stab/internal/config"
)

func writeModels(t *testing.T, home, content string) string {
	t.Helper()
	dir := filepath.Join(home, ".stables")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "models.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRefreshTunnelModelsPreservesOtherProviders(t *testing.T) {
	home := t.TempDir()
	p := writeModels(t, home, `{"providers":{"x":{"baseUrl":"http://x"},"stables-tunnel-old":{"baseUrl":"http://old"}}}`)

	old := queryOllamaTags
	queryOllamaTags = func(port int) []string { return []string{"qwen2.5:0.5b"} }
	defer func() { queryOllamaTags = old }()

	a := &App{Home: home, Config: config.Config{StateDir: ".stables"}}
	if err := a.refreshTunnelModels(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	var doc struct {
		Providers map[string]map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.Providers["x"]; !ok {
		t.Fatal("other provider x was dropped")
	}
	if _, ok := doc.Providers["stables-tunnel-old"]; ok {
		t.Fatal("stale stables-tunnel-old not removed")
	}
	found := ""
	for k := range doc.Providers {
		if strings.HasPrefix(k, "stables-tunnel-") {
			found = k
		}
	}
	if found == "" {
		t.Fatal("no stables-tunnel-<host> provider added")
	}
	if got := doc.Providers[found]["baseUrl"]; got != "http://host.docker.internal:11434/v1" {
		t.Fatalf("baseUrl = %v", got)
	}
	if got := doc.Providers[found]["apiKey"]; got != "ollama" {
		t.Fatalf("apiKey = %v, want literal ollama (non-empty, no env needed)", got)
	}
}

func TestRefreshTunnelModelsRemovesWhenNoOllama(t *testing.T) {
	home := t.TempDir()
	p := writeModels(t, home, `{"providers":{"x":{"baseUrl":"http://x"},"stables-tunnel-old":{"baseUrl":"http://old"}}}`)

	old := queryOllamaTags
	queryOllamaTags = func(port int) []string { return nil }
	defer func() { queryOllamaTags = old }()

	a := &App{Home: home, Config: config.Config{StateDir: ".stables"}}
	if err := a.refreshTunnelModels(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	var doc struct {
		Providers map[string]map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.Providers["x"]; !ok {
		t.Fatal("other provider x was dropped")
	}
	for k := range doc.Providers {
		if strings.HasPrefix(k, "stables-tunnel-") {
			t.Fatalf("tunnel provider %q must be removed when no ollama", k)
		}
	}
}
