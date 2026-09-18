package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIDForPathStable(t *testing.T) {
	dir := t.TempDir()
	a, err := IDForPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := IDForPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("ID not stable: %s != %s", a, b)
	}
	if a == "" {
		t.Fatal("empty ID")
	}
}

func TestIDForPathIgnoresBasename(t *testing.T) {
	base := t.TempDir()
	dir1 := filepath.Join(base, "same")
	dir2 := filepath.Join(base, "same")
	if err := os.MkdirAll(dir1, 0o755); err != nil {
		t.Fatal(err)
	}
	// Symlink with a different name resolving to dir1.
	link := filepath.Join(base, "alias")
	if err := os.Symlink(dir1, link); err == nil {
		a, _ := IDForPath(dir1)
		b, _ := IDForPath(link)
		if a != b {
			t.Fatalf("symlink alias should share ID: %s != %s", a, b)
		}
	}
	_ = dir2
}

func TestCanonicalPathResolvesSymlinks(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got, err := CanonicalPath(link)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := CanonicalPath(real)
	if got != want {
		t.Fatalf("canonical mismatch: %s != %s", got, want)
	}
}

func TestContainerName(t *testing.T) {
	if ContainerName("abcdef1234567890") != "stab-abcdef123456" {
		t.Fatalf("unexpected container name %q", ContainerName("abcdef1234567890"))
	}
}
