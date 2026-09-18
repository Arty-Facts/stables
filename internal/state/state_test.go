package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSaveRoundtrip(t *testing.T) {
	home := t.TempDir()
	m := Manifest{Installations: map[string]Component{"ollama": {Status: "installed", OllamaPort: 11434}}}
	if err := m.Save(home); err != nil {
		t.Fatal(err)
	}
	got, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Installed("ollama") {
		t.Fatalf("expected ollama installed, got %+v", got)
	}
	if got.Installations["ollama"].OllamaPort != 11434 {
		t.Fatalf("port mismatch: %+v", got)
	}
}

func TestLoadMissing(t *testing.T) {
	home := filepath.Join(t.TempDir(), "nope")
	m, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if m.Installations == nil {
		t.Fatal("expected non-nil map")
	}
	_ = os.RemoveAll(home)
}

func TestInstalledFalseWhenRemoved(t *testing.T) {
	m := Manifest{Installations: map[string]Component{"webui": {Status: "removed"}}}
	if m.Installed("webui") {
		t.Fatal("removed component must not report installed")
	}
}

func TestMigrateWorkhorseToStab(t *testing.T) {
	home := t.TempDir()
	m := Manifest{Installations: map[string]Component{
		"workhorse": {Status: "installed"},
		"ollama":    {Status: "installed", OllamaPort: 11434},
	}}
	if err := m.Save(home); err != nil {
		t.Fatal(err)
	}
	got, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Installations["workhorse"]; ok {
		t.Fatalf("workhorse key not migrated away: %+v", got.Installations)
	}
	if !got.Installed("stab") {
		t.Fatalf("expected stab migrated from workhorse: %+v", got.Installations)
	}
	if !got.Installed("ollama") {
		t.Fatalf("ollama lost during migration: %+v", got.Installations)
	}
	// persisted: a second load must not see workhorse either
	again, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := again.Installations["workhorse"]; ok {
		t.Fatalf("workhorse key survived migration persist: %+v", again.Installations)
	}
}
