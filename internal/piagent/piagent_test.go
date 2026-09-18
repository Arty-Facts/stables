package piagent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func tgz(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Typeflag: tar.TypeReg, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestExtractTGZ(t *testing.T) {
	dir := t.TempDir()
	data := tgz(t, map[string]string{"a/SKILL.md": "hello", "b.txt": "world"})
	if err := extractTGZ(data, dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "a", "SKILL.md"))
	if err != nil || string(b) != "hello" {
		t.Fatalf("skill not extracted: %v %q", err, b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "b.txt")); string(b) != "world" {
		t.Fatalf("b.txt mismatch: %q", b)
	}
}

func TestExtractTGZEmptyIsNoop(t *testing.T) {
	if err := extractTGZ(nil, t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestExtractTGZPreservesGit(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/main"), 0o644)
	data := tgz(t, map[string]string{".git/HEAD": "corrupt", "x.txt": "ok"})
	if err := extractTGZ(data, dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".git", "HEAD")); string(b) != "ref: refs/heads/main" {
		t.Fatalf(".git was overwritten: %q", b)
	}
}

func TestMergeSettingsPackagesDedup(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(p, []byte(`{"packages":["npm:foo"]}`), 0o644)
	if err := mergeSettingsPackages(p, []byte(`["npm:foo","git:https://x/y"]`)); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	for _, want := range []string{"npm:foo", "git:https://x/y"} {
		if !bytes.Contains(data, []byte(want)) {
			t.Fatalf("settings missing %q: %s", want, data)
		}
	}
	if bytes.Count(data, []byte("npm:foo")) != 1 {
		t.Fatalf("npm:foo duplicated: %s", data)
	}
}
