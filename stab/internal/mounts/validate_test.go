package mounts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateRootRejectsSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	inside := filepath.Join(base, "project")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	// A symlink inside the project pointing outside it.
	link := filepath.Join(inside, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// Validate the link path itself: EvalSymlinks resolves it outside base.
	if _, err := ValidateRoot(link, []string{base}); err == nil {
		t.Fatalf("expected symlink escape to be rejected")
	}
}

func TestValidateRootAllowlist(t *testing.T) {
	base := t.TempDir()
	inside := filepath.Join(base, "project")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()

	if _, err := ValidateRoot(inside, []string{base}); err != nil {
		t.Fatalf("inside path should be allowed: %v", err)
	}
	if _, err := ValidateRoot(outside, []string{base}); err == nil {
		t.Fatalf("outside path should be rejected")
	}
}

func TestValidateRootForbiddenComponents(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{".ssh", ".docker", ".gnupg", ".aws"} {
		p := filepath.Join(base, "proj", name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateRoot(p, nil); err == nil {
			t.Fatalf("forbidden component %q should be rejected", name)
		}
	}
}

func TestValidateRootRejectsHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if _, err := ValidateRoot(home, nil); err == nil {
		t.Fatalf("bare home directory should be rejected")
	}
}

func TestValidateRootRejectsNonDir(t *testing.T) {
	base := t.TempDir()
	f := filepath.Join(base, "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRoot(f, nil); err == nil {
		t.Fatalf("non-directory should be rejected")
	}
}
