package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ttscmp "github.com/stables/stables/internal/components/tts"
	"github.com/stables/stables/internal/state"
	"github.com/stables/stables/internal/tunnel"
)

// install records a component in a temporary home with its own compose directory.
func install(t *testing.T, home, kind string, port int) string {
	t.Helper()
	dir := filepath.Join(home, ".stables", kind)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m, err := state.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	m.Installations[kind] = state.Component{ComposeDir: dir, TTSPort: port}
	if err := m.Save(home); err != nil {
		t.Fatal(err)
	}
	return dir
}

// stubDocker replaces the two seams and returns the recorded docker calls.
func stubDocker(t *testing.T, running bool) *[]string {
	t.Helper()
	var calls []string
	oldRun, oldRunning := runDocker, localStackRunning
	runDocker = func(ctx context.Context, dir string, args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}
	localStackRunning = func(ctx context.Context, dir string) bool { return running }
	t.Cleanup(func() { runDocker, localStackRunning = oldRun, oldRunning })
	return &calls
}

// writeTunnelState puts a tunnel on disk, which is what a previous `tunnel` left.
func writeTunnelState(t *testing.T, home, kind, mode string, port int, stopped bool) string {
	t.Helper()
	path := filepath.Join(home, ".stables", "tunnels", kind)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"kind": kind, "mode": mode, "target": "server", "port": port,
		"pid": 999999, "local_stopped": stopped,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEachComponentTunnelsItsOwnPort(t *testing.T) {
	// The point of per-component tunnels: forwarding voices is not a way to forward
	// the model server on a different port. Binding these to the components' real
	// ports means a changed port cannot leave a tunnel pointing at nothing.
	if got, want := tunnel.Port(tunnel.KindTTS), ttscmp.DefaultData(t.TempDir()).Port; got != want {
		t.Errorf("the voice tunnel forwards port %d, the backend listens on %d", got, want)
	}
	if got := tunnel.Port(tunnel.KindOllama); got != tunnel.DefaultPort {
		t.Errorf("the ollama tunnel forwards port %d", got)
	}
}

func TestTunnellingStopsTheLocalStack(t *testing.T) {
	home := t.TempDir()
	install(t, home, tunnel.KindTTS, tunnel.TTSPort)
	calls := stubDocker(t, true)

	// A host that cannot resolve, so the tunnel fails — which is the case that must
	// not leave the user with neither the remote service nor the local one.
	code := cmdComponentTunnel(context.Background(), home, tunnel.KindTTS, []string{"user@host.invalid"})
	if code == 0 {
		t.Error("a tunnel that did not start must not report success")
	}
	joined := strings.Join(*calls, " | ")
	if !strings.Contains(joined, "compose stop") {
		t.Errorf("the local stack holds the port and must be stopped: %s", joined)
	}
	if !strings.Contains(joined, "compose up -d") {
		t.Errorf("a tunnel that failed must restart what it stopped: %s", joined)
	}
}

func TestTunnellingLeavesAStoppedStackAlone(t *testing.T) {
	home := t.TempDir()
	install(t, home, tunnel.KindTTS, tunnel.TTSPort)
	calls := stubDocker(t, false)

	cmdComponentTunnel(context.Background(), home, tunnel.KindTTS, []string{"user@host.invalid"})
	if len(*calls) != 0 {
		t.Errorf("nothing was running, so nothing should be stopped or started: %v", *calls)
	}
}

func TestTunnelDownRestartsALocalStackTheTunnelStopped(t *testing.T) {
	home := t.TempDir()
	install(t, home, tunnel.KindTTS, tunnel.TTSPort)
	path := writeTunnelState(t, home, tunnel.KindTTS, "remote", tunnel.TTSPort, true)
	calls := stubDocker(t, false)

	if code := cmdComponentTunnel(context.Background(), home, tunnel.KindTTS, []string{"down"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(strings.Join(*calls, " "), "compose up -d") {
		t.Errorf("down must start the stack the tunnel stopped: %v", *calls)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the tunnel state should be cleared")
	}
}

func TestTunnelDownLeavesAnUntouchedStackAlone(t *testing.T) {
	home := t.TempDir()
	install(t, home, tunnel.KindTTS, tunnel.TTSPort)
	writeTunnelState(t, home, tunnel.KindTTS, "remote", tunnel.TTSPort, false)
	calls := stubDocker(t, false)

	if code := cmdComponentTunnel(context.Background(), home, tunnel.KindTTS, []string{"down"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(*calls) != 0 {
		t.Errorf("the tunnel did not stop anything, so nothing should be started: %v", *calls)
	}
}

func TestTTSUsageIsNotPassedToTheTui(t *testing.T) {
	// `stables tts up` must be a lifecycle command, not text handed to the TUI to
	// read aloud. Without an install it fails before reaching the TUI, which is the
	// assertion: exit 1 with the install hint, never a TUI launch.
	// up/down/list need an install and fail with the hint (1). `tunnel` with no
	// target prints its usage (2) instead, because reaching a *remote* voice stack is
	// the point of a tunnel and must not require a local install.
	for verb, want := range map[string]int{"up": 1, "down": 1, "list": 1, "tunnel": 2} {
		if code := cmdTTSControl(context.Background(), t.TempDir(), []string{verb}); code != want {
			t.Errorf("tts %s with no install: exit %d, want %d", verb, code, want)
		}
	}
}
