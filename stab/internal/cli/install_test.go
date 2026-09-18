package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCopyFileOverRunningExecutable reproduces the `stab install` self-update
// bug: the binary being copied is the currently-running executable, and
// writing directly to it fails with ETXTBSY ("text file busy"). copyFile must
// write to a temp file and rename it into place instead.
func TestCopyFileOverRunningExecutable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ETXTBSY semantics differ outside Linux")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}

	// The test binary is running right now; overwriting it in place would fail.
	if err := copyFile(exe, exe, 0o755); err != nil {
		t.Fatalf("copyFile over running executable failed: %v", err)
	}

	after, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("binary content changed across the atomic rewrite")
	}

	entries, err := os.ReadDir(filepath.Dir(exe))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".stab-install-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestCopyFilePreservesMode(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(src, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "dst")
	if err := copyFile(src, dst, 0o700); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %o, want 700", fi.Mode().Perm())
	}
}

func TestSanitizeBinaryName(t *testing.T) {
	tests := map[string]string{
		"test1":           "test1",
		"test-one_2":      "test-one_2",
		"bad/../name wow": "bad____name_wow",
		"":                "stab",
	}
	for input, want := range tests {
		if got := sanitizeBinaryName(input); got != want {
			t.Fatalf("sanitizeBinaryName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestEnsurePathInBashrc(t *testing.T) {
	home := t.TempDir()
	bindir := filepath.Join(home, ".local", "bin")
	if err := ensurePathInBashrc(home, bindir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".bashrc"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), bindir) {
		t.Fatalf(".bashrc does not contain %q: %s", bindir, data)
	}
	if err := ensurePathInBashrc(home, bindir); err != nil {
		t.Fatal(err)
	}
	dataAgain, err := os.ReadFile(filepath.Join(home, ".bashrc"))
	if err != nil {
		t.Fatal(err)
	}
	if string(dataAgain) != string(data) {
		t.Fatal("repeating PATH setup should not duplicate the entry")
	}
}
