package webui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFetchEndpointsResolvesKeysAndSkipsTunnel(t *testing.T) {
	home := t.TempDir()
	stab := filepath.Join(home, ".stables")
	if err := os.MkdirAll(stab, 0o700); err != nil {
		t.Fatal(err)
	}
	models := `{
  "providers": {
    "stables-tunnel-wood": {"baseUrl": "http://host.docker.internal:11434/v1", "apiKey": "ollama"},
    "acme": {"baseUrl": "https://ai.example/openai", "apiKey": "$CV_KEY"},
    "openai": {"baseUrl": "https://api.openai.com/v1", "apiKey": "test-key-literal"},
    "nokey": {"baseUrl": "https://nokey.example/v1"}
  }
}`
	if err := os.WriteFile(filepath.Join(stab, "models.json"), []byte(models), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stab, "secrets.env"), []byte("CV_KEY=test-key-resolved\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ends, err := fetchEndpoints(home)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range ends {
		got[e.BaseURL] = e.Key
	}
	if _, ok := got["http://host.docker.internal:11434/v1"]; ok {
		t.Fatal("tunnel provider must be skipped")
	}
	if got["https://ai.example/openai"] != "test-key-resolved" {
		t.Fatalf("$VAR key not resolved: %v", got)
	}
	if got["https://api.openai.com/v1"] != "test-key-literal" {
		t.Fatalf("literal key wrong: %v", got)
	}
	if _, ok := got["https://nokey.example/v1"]; ok {
		t.Fatal("provider without key must be skipped")
	}
}

func TestFetchEndpointsMissingModels(t *testing.T) {
	ends, err := fetchEndpoints(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(ends) != 0 {
		t.Fatalf("expected no endpoints, got %v", ends)
	}
}
