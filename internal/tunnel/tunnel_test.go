package tunnel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCurrentDefaultLocal(t *testing.T) {
	home := t.TempDir()
	s, err := Current(home)
	if err != nil {
		t.Fatal(err)
	}
	if s.Mode != "local" || s.Port != DefaultPort {
		t.Fatalf("expected local default, got %+v", s)
	}
}

func TestUpLocalThenDown(t *testing.T) {
	home := t.TempDir()
	if err := Up(home, State{Kind: KindOllama, Mode: "local", Port: DefaultPort}); err != nil {
		t.Fatal(err)
	}
	s, err := Get(home, KindOllama)
	if err != nil {
		t.Fatal(err)
	}
	if s.Mode != "local" {
		t.Fatalf("expected local, got %+v", s)
	}
	if err := Down(home, KindOllama); err != nil {
		t.Fatal(err)
	}
	s, err = Get(home, KindOllama)
	if err != nil {
		t.Fatal(err)
	}
	if s.Mode != "local" || s.PID != 0 {
		t.Fatalf("expected cleared state, got %+v", s)
	}
}

func TestUpRemoteUnreachableFails(t *testing.T) {
	home := t.TempDir()
	err := Up(home, State{Kind: KindOllama, Mode: "remote", Target: "user@host.invalid", Port: DefaultPort})
	if err == nil {
		t.Fatal("expected the reachability check to fail")
	}
	// And nothing claiming a tunnel was left behind.
	s, _ := Get(home, KindOllama)
	if s.Mode == "remote" {
		t.Fatalf("a failed tunnel must not be recorded as up: %+v", s)
	}
}

func TestHostOnly(t *testing.T) {
	if got := hostOnly("user@example.com"); got != "example.com" {
		t.Fatalf("got %q", got)
	}
	if got := hostOnly("example.com"); got != "example.com" {
		t.Fatalf("got %q", got)
	}
}

func TestSSHForwardBindsAllInterfaces(t *testing.T) {
	// The stab container reaches the host through the bridge gateway, so a
	// loopback-only forward would be unreachable from inside it.
	cmd := sshForward("user@example.com", 11434)
	joined := ""
	for _, a := range cmd.Args {
		joined += a + " "
	}
	if !strings.Contains(joined, "0.0.0.0:11434:localhost:11434") {
		t.Fatalf("forward must bind all interfaces: %s", joined)
	}
}

// ── kinds ────────────────────────────────────────────────────────────────────

func TestEachKindHasItsOwnPort(t *testing.T) {
	if Port(KindOllama) != DefaultPort {
		t.Errorf("ollama port = %d", Port(KindOllama))
	}
	// The voice backend does not listen on ollama's port, and a tunnel that
	// forwarded the wrong one would look like it worked.
	if Port(KindTTS) != TTSPort || TTSPort == DefaultPort {
		t.Errorf("tts port = %d, ollama = %d", Port(KindTTS), DefaultPort)
	}
}

func TestKindsAreIndependent(t *testing.T) {
	home := t.TempDir()
	if err := Up(home, State{Kind: KindOllama, Mode: "local", Port: Port(KindOllama)}); err != nil {
		t.Fatal(err)
	}
	if err := Up(home, State{Kind: KindTTS, Mode: "local", Port: Port(KindTTS)}); err != nil {
		t.Fatal(err)
	}
	if err := Down(home, KindTTS); err != nil {
		t.Fatal(err)
	}
	// Closing the voice tunnel must not touch the model server's.
	if _, err := os.Stat(kindPath(home, KindOllama)); err != nil {
		t.Errorf("the ollama tunnel should still have state: %v", err)
	}
	if _, err := os.Stat(kindPath(home, KindTTS)); !os.IsNotExist(err) {
		t.Errorf("the tts tunnel state should be gone, got %v", err)
	}
}

func TestDownAllClosesBoth(t *testing.T) {
	home := t.TempDir()
	for _, kind := range Kinds {
		if err := Up(home, State{Kind: kind, Mode: "local", Port: Port(kind)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := DownAll(home); err != nil {
		t.Fatal(err)
	}
	for _, kind := range Kinds {
		if _, err := os.Stat(kindPath(home, kind)); !os.IsNotExist(err) {
			t.Errorf("%s state should be gone, got %v", kind, err)
		}
	}
}

func TestALocalStopIsRemembered(t *testing.T) {
	// It is the only record of why the local stack is not running, and `down` needs
	// it to put things back.
	home := t.TempDir()
	if err := Up(home, State{Kind: KindTTS, Mode: "remote", Target: "server", Port: TTSPort, LocalStopped: true}); err != nil {
		// A remote tunnel needs ssh; write the state directly for this check.
		if err := save(home, State{Kind: KindTTS, Mode: "remote", Target: "server", Port: TTSPort, LocalStopped: true}); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Get(home, KindTTS)
	if err != nil {
		t.Fatal(err)
	}
	if !s.LocalStopped {
		t.Fatalf("expected the stopped local stack to be recorded: %+v", s)
	}
}

func TestThePreKindTunnelFileIsStillRead(t *testing.T) {
	// An upgrade must not forget a tunnel that is already up.
	home := t.TempDir()
	if err := os.MkdirAll(dir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := `{"mode":"remote","target":"server","port":11434,"pid":4321}`
	if err := os.WriteFile(currentPath(home), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Current(home)
	if err != nil {
		t.Fatal(err)
	}
	if s.Mode != "remote" || s.Target != "server" || s.PID != 4321 {
		t.Fatalf("the old file should still describe a tunnel: %+v", s)
	}
	if s.Kind != KindOllama {
		t.Errorf("the single old tunnel was the ollama one, got %q", s.Kind)
	}
	// Clearing it clears the old file too, or the tunnel would reappear.
	if err := Down(home, KindOllama); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir(home), "current")); !os.IsNotExist(err) {
		t.Errorf("the legacy file should be gone, got %v", err)
	}
}
