package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeModelsFileAddsNewProviders(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "models.json")
	if err := os.WriteFile(dst, []byte("{\"providers\":{\"mine\":{\"baseUrl\":\"x\"}}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	baked := []byte("{\"providers\":{\"mine\":{\"baseUrl\":\"new\"},\"theirs\":{\"baseUrl\":\"y\"}}}\n")
	if err := mergeModelsFile(dst, baked, false); err != nil {
		t.Fatal(err)
	}
	p, err := loadProviders(dst)
	if err != nil {
		t.Fatal(err)
	}
	if p["mine"]["baseUrl"] != "x" {
		t.Fatalf("existing provider was overwritten: %v", p["mine"])
	}
	if _, ok := p["theirs"]; !ok {
		t.Fatalf("new provider not added: %v", p)
	}
}

func TestMergeModelsFileForceReplaces(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "models.json")
	if err := os.WriteFile(dst, []byte("{\"providers\":{\"mine\":{},\"old\":{}}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	baked := []byte("{\"providers\":{\"theirs\":{}}}\n")
	if err := mergeModelsFile(dst, baked, true); err != nil {
		t.Fatal(err)
	}
	p, _ := loadProviders(dst)
	if _, ok := p["mine"]; ok {
		t.Fatalf("force did not replace: %v", p)
	}
	if _, ok := p["theirs"]; !ok {
		t.Fatalf("force missing baked provider: %v", p)
	}
}

func TestMergeSecretsFileCollisionKeepsOld(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "secrets.env")
	if err := os.WriteFile(dst, []byte("# comment\nFOO=old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var warned []string
	baked := []byte("FOO=new\nBAR=value\n")
	if err := mergeSecretsFile(dst, baked, false, func(m string) { warned = append(warned, m) }); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(dst)
	s := string(data)
	if !strings.Contains(s, "FOO=old") {
		t.Fatalf("existing secret was overwritten: %q", s)
	}
	if !strings.Contains(s, "BAR=value") {
		t.Fatalf("new secret not appended: %q", s)
	}
	if !strings.Contains(s, "# comment") {
		t.Fatalf("existing comments lost: %q", s)
	}
	if len(warned) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warned), warned)
	}
}

func TestMergeSecretsFileForceReplaces(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "secrets.env")
	if err := os.WriteFile(dst, []byte("FOO=old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	baked := []byte("FOO=new\nBAR=value\n")
	if err := mergeSecretsFile(dst, baked, true, nil); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(dst)
	s := string(data)
	if !strings.Contains(s, "FOO=new") {
		t.Fatalf("force did not replace: %q", s)
	}
	if !strings.Contains(s, "BAR=value") {
		t.Fatalf("force missing key: %q", s)
	}
}

func TestExtractConfigSeedsEmptyTemplates(t *testing.T) {
	a := newTestApp(t)
	if err := a.extractConfig(false); err != nil {
		t.Fatal(err)
	}
	state := a.Config.StateDirPath(a.Home)
	models, err := os.ReadFile(filepath.Join(state, "models.json"))
	if err != nil {
		t.Fatalf("models.json not seeded: %v", err)
	}
	if !strings.Contains(string(models), `"providers"`) {
		t.Fatalf("models.json empty template wrong: %q", models)
	}
	if _, err := os.Stat(filepath.Join(state, "secrets.env")); err != nil {
		t.Fatalf("secrets.env not seeded: %v", err)
	}
}
