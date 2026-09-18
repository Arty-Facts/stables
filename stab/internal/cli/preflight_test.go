package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testHome returns a scratch home dir; STAB_TEST_HOME lets a root helper
// pre-create foreign-owned state for ownership tests.
func testHome(t *testing.T) string {
	t.Helper()
	if h := os.Getenv("STAB_TEST_HOME"); h != "" {
		return h
	}
	d := t.TempDir()
	_ = os.MkdirAll(d, 0o755)
	return d
}
func statusFor(results []fsResult, dir string) *fsResult {
	for i := range results {
		if results[i].Path == dir {
			return &results[i]
		}
	}
	return nil
}

func TestEnsureHomeDirsAllWritable(t *testing.T) {
	home := t.TempDir()
	results := ensureHomeDirs(home, false)
	if len(results) != len(homeStateDirs(home)) {
		t.Fatalf("got %d results, want %d", len(results), len(homeStateDirs(home)))
	}
	for _, r := range results {
		if r.Status != "ok" {
			t.Fatalf("expected ok for %s, got %s: %s", r.Path, r.Status, r.Detail)
		}
	}
}

func TestEnsureHomeDirsResetsBlockedAgentDir(t *testing.T) {
	home := t.TempDir()
	agent := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(agent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agent, "stale.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(agent, 0o500); err != nil { // owner without write bit
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(agent, 0o755) })

	if got := ensureHomeDirs(home, false); statusFor(got, agent).Status != "fail" {
		t.Fatalf("expected fail without repair, got %+v", got)
	}
	got := ensureHomeDirs(home, true)
	r := statusFor(got, agent)
	if r.Status != "reset" {
		t.Fatalf("expected reset, got %s: %s", r.Status, r.Detail)
	}
	if !strings.Contains(r.Detail, "repaired mode") {
		t.Fatalf("owned-but-locked dir should have its mode repaired, got: %s", r.Detail)
	}
	// The dir must be writable again and the owned content must survive.
	if err := os.WriteFile(filepath.Join(agent, "ok.txt"), []byte("y"), 0o600); err != nil {
		t.Fatalf("repaired agent dir not writable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(agent, "stale.txt")); err != nil {
		t.Fatalf("owned content must survive a mode repair, stat err=%v", err)
	}
}

func TestEnsureHomeDirsBlockedWithoutRepairReportsFix(t *testing.T) {
	home := t.TempDir()
	stab := filepath.Join(home, ".stables")
	if err := os.MkdirAll(stab, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(stab, 0o755) })

	got := ensureHomeDirs(home, false)
	r := statusFor(got, stab)
	if r.Status != "fail" {
		t.Fatalf("expected fail, got %s", r.Status)
	}
	if !strings.Contains(r.Detail, "chmod u+w") {
		t.Fatalf("owned-but-locked failure should suggest the no-sudo chmod, got: %s", r.Detail)
	}
}

func TestEnsureHomeDirsBinRepairKeepsBinaries(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"other.bin", "stab"} {
		if err := os.WriteFile(filepath.Join(bin, f), []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(bin, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(bin, 0o755) })

	got := ensureHomeDirs(home, true)
	r := statusFor(got, bin)
	if r.Status != "reset" {
		t.Fatalf("expected reset, got %s: %s", r.Status, r.Detail)
	}
	// A mode repair must preserve every binary in the user's bin dir.
	for _, f := range []string{"other.bin", "stab"} {
		if _, err := os.Stat(filepath.Join(bin, f)); err != nil {
			t.Fatalf("binary %s lost during repair: %v", f, err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "probe"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("repaired bin not writable: %v", err)
	}
}

func TestForeignOwnedDirHint(t *testing.T) {
	if os.Getenv("STAB_TEST_HOME") == "" {
		t.Skip("foreign-ownership simulation needs STAB_TEST_HOME prepared by root")
	}
	home := testHome(t)
	agent := filepath.Join(home, ".pi", "agent")
	got := ensureHomeDirs(home, false) // report only; must not touch foreign state
	r := statusFor(got, agent)
	if r == nil || r.Status != "fail" {
		t.Fatalf("expected fail for foreign-owned %s, got %+v", agent, r)
	}
	if os.Geteuid() != 0 && !strings.Contains(r.Detail, "uid 0:0") {
		t.Fatalf("hint should name the foreign owner, got: %s", r.Detail)
	}
	if !strings.Contains(r.Detail, "rm -rf") {
		t.Fatalf("hint should give the no-sudo fix, got: %s", r.Detail)
	}
}

func TestForeignOwnedDirReset(t *testing.T) {
	if os.Getenv("STAB_TEST_HOME") == "" {
		t.Skip("foreign-ownership simulation needs STAB_TEST_HOME prepared by root")
	}
	home := testHome(t)
	got := ensureHomeDirs(home, true)
	checked := 0
	for _, r := range got {
		base := filepath.Base(r.Path)
		if base != "stab" && base != "agent" {
			continue
		}
		if r.Status == "ok" {
			continue // foreign preparation not present for this path
		}
		checked++
		if r.Status != "reset" {
			t.Fatalf("expected reset for %s, got %s: %s", r.Path, r.Status, r.Detail)
		}
		if err := os.WriteFile(filepath.Join(r.Path, "probe"), []byte("ok"), 0o600); err != nil {
			t.Fatalf("reset dir %s not writable: %v", r.Path, err)
		}
	}
	if checked == 0 {
		t.Skip("no foreign-owned state dir found to reset")
	}
}
