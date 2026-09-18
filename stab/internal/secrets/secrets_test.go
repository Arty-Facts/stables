package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "secrets.env")
	content := `# comment
export EXAMPLE_TOKEN="test-token-quoted"
OLLAMA=ollama
OTHER_TOKEN='test-token-single-quoted'
`
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := LoadEnvFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if m["EXAMPLE_TOKEN"] != "test-token-quoted" {
		t.Fatalf("bad quoted value: %q", m["EXAMPLE_TOKEN"])
	}
	if m["OLLAMA"] != "ollama" {
		t.Fatalf("bad unquoted value: %q", m["OLLAMA"])
	}
	if m["OTHER_TOKEN"] != "test-token-single-quoted" {
		t.Fatalf("bad single-quoted value: %q", m["OTHER_TOKEN"])
	}
}

func TestExtractRefs(t *testing.T) {
	s := `{"apiKey": "$EXAMPLE_API_KEY", "x": "${FOO_BAR}", "y": "$EXAMPLE_API_KEY"}`
	got := ExtractRefs(s)
	if len(got) != 2 {
		t.Fatalf("expected 2 unique refs, got %v", got)
	}
	if got[0] != "EXAMPLE_API_KEY" || got[1] != "FOO_BAR" {
		t.Fatalf("unexpected refs: %v", got)
	}
}
