package cli

import (
	"context"
	"os"
	"testing"
)

func TestInstallRejectsAFlagThatDoesNotExist(t *testing.T) {
	// --no-preload was advertised in the usage text and read by nothing, so it
	// looked accepted and did nothing. An unknown flag must be an error.
	if code := cmdInstall(context.Background(), t.TempDir(), []string{"tts", "--no-preload"}); code != 2 {
		t.Errorf("exit %d, want 2 for an unknown flag", code)
	}
	if code := cmdInstall(context.Background(), t.TempDir(), []string{"tts", "--pretend"}); code != 2 {
		t.Errorf("exit %d, want 2 for an unknown flag", code)
	}
	// And a real one must still be accepted, all the way to the component.
	if err := rejectUnknownFlags([]string{"t t s", "--preload", "--no-models"}, knownInstallFlags); err != nil {
		t.Errorf("a known flag was rejected: %v", err)
	}
	if err := rejectUnknownFlags([]string{"--preload=1"}, knownInstallFlags); err != nil {
		t.Errorf("--flag=value form must parse: %v", err)
	}
}

func TestComponentArgSkipsFlags(t *testing.T) {
	if got := componentArg([]string{"--remotes"}, "all"); got != "all" {
		t.Fatalf("expected default, got %q", got)
	}
	if got := componentArg([]string{"--gpu", "auto", "ollama"}, "all"); got != "ollama" {
		t.Fatalf("expected component, got %q", got)
	}
}

func TestNonFlagArgsSkipsFlagValues(t *testing.T) {
	got := nonFlagArgs([]string{"qwen3:8b", "--ollama-port", "1234", "nomic-embed-text"})
	if len(got) != 2 || got[0] != "qwen3:8b" || got[1] != "nomic-embed-text" {
		t.Fatalf("unexpected args: %#v", got)
	}
}

func TestDockerRemoveListUsesHostHomePaths(t *testing.T) {
	got := dockerRemoveList([]string{".stables", ".pi/agent"})
	want := "/host_home/.stables /host_home/.pi/agent"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestComponentArgSkipsFlagValues(t *testing.T) {
	if got := componentArg([]string{"--port", "17600", "webui"}, "all"); got != "webui" {
		t.Fatalf("expected webui, got %q", got)
	}
	if got := componentArg([]string{"--port", "8080", "webui"}, "all"); got != "webui" {
		t.Fatalf("expected webui, got %q", got)
	}
}

func TestCheckComponentAcceptsKnownAndAll(t *testing.T) {
	for _, name := range append(append([]string{}, knownComponents...), "all") {
		if !checkComponent(name) {
			t.Fatalf("%q must be accepted", name)
		}
	}
}

func TestCheckComponentRejectsTypo(t *testing.T) {
	for _, name := range []string{"voisebox", "webuis", "stb", "workhorse", ""} {
		if checkComponent(name) {
			t.Fatalf("%q must be rejected", name)
		}
	}
}

// A typo used to fall through every `component ==` branch and print
// "install complete" without installing anything.
func TestCmdInstallRejectsTypoWithoutTouchingTheHome(t *testing.T) {
	home := t.TempDir()
	if code := cmdInstall(context.Background(), home, []string{"ttss"}); code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("a rejected component must not create anything, got %v", names)
	}
}

func TestCmdRemoveRejectsTypo(t *testing.T) {
	home := t.TempDir()
	if code := cmdRemove(context.Background(), home, []string{"voisebox"}); code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
}

func TestCmdUpdateRejectsTypo(t *testing.T) {
	home := t.TempDir()
	if code := cmdUpdate(context.Background(), home, []string{"voisebox"}); code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
}

func TestInstallWithNothingNamedAsksWhatToInstall(t *testing.T) {
	// It used to default to every component: several gigabytes chosen on the user's
	// behalf, when the components do not depend on each other.
	if code := cmdInstall(context.Background(), t.TempDir(), []string{}); code != 2 {
		t.Errorf("exit %d, want 2 when no component is named", code)
	}
}
