// Package tunnel manages the SSH tunnels that decide where work is computed.
//
// One tunnel per kind. Forwarding ollama says nothing about the voice stack, and
// they are separate services on separate ports, so each keeps its own state and
// they can be up at once.
//
// A forward binds the same local port the component listens on, because that is
// what makes the remote service a drop-in: the client keeps talking to
// 127.0.0.1 and nothing downstream needs reconfiguring. It is also why a local
// stack and a forward cannot both be up, which is what `State.LocalStopped`
// records — the caller stops the local stack to free the port, and a later
// `down` can put it back.
package tunnel

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	// KindOllama forwards the local model server.
	KindOllama = "ollama"
	// KindTTS forwards the voice backend.
	KindTTS = "tts"

	// DefaultPort is ollama's port, and the port of a tunnel with no kind.
	DefaultPort = 11434
	// TTSPort is the voice backend's port.
	TTSPort = 17493
)

// Kinds are the tunnels that can be up, in the order they are listed.
var Kinds = []string{KindOllama, KindTTS}

// Port is the local port a kind is forwarded on: the same port the component
// listens on, so the client needs no reconfiguring and the local stack has to be
// stopped first.
func Port(kind string) int {
	if kind == KindTTS {
		return TTSPort
	}
	return DefaultPort
}

type State struct {
	Kind   string `json:"kind"`
	Mode   string `json:"mode"`   // local | remote
	Target string `json:"target"` // host part only (mode=remote)
	Port   int    `json:"port"`
	PID    int    `json:"pid"`
	// LocalStopped records that the local stack was stopped to free the port, so
	// `down` can start it again rather than leaving the user with neither the
	// remote service nor the local one.
	LocalStopped bool `json:"local_stopped,omitempty"`
}

func dir(home string) string            { return filepath.Join(home, ".stables", "tunnels") }
func kindPath(home, kind string) string { return filepath.Join(dir(home), kind) }

// currentPath is the file the single pre-kind tunnel used. It is still read, so an
// upgrade does not silently forget a tunnel that is already up.
func currentPath(home string) string { return filepath.Join(dir(home), "current") }

// Current is the ollama tunnel: the one that existed before kinds did.
func Current(home string) (State, error) { return Get(home, KindOllama) }

// Get returns the tunnel for *kind*, or a local default when none is up.
func Get(home, kind string) (State, error) {
	if kind == "" {
		kind = KindOllama
	}
	data, err := os.ReadFile(kindPath(home, kind))
	if err != nil {
		if !os.IsNotExist(err) {
			return State{}, err
		}
		if kind == KindOllama {
			if legacy, lerr := os.ReadFile(currentPath(home)); lerr == nil {
				var s State
				if json.Unmarshal(legacy, &s) == nil {
					return normalise(s, kind), nil
				}
			}
		}
		return State{Kind: kind, Mode: "local", Port: Port(kind)}, nil
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, err
	}
	return normalise(s, kind), nil
}

func normalise(s State, kind string) State {
	if s.Kind == "" {
		s.Kind = kind
	}
	if s.Port == 0 {
		s.Port = Port(s.Kind)
	}
	if s.Mode == "" {
		s.Mode = "local"
	}
	return s
}

// Active returns every tunnel that is up, in Kinds order.
func Active(home string) ([]State, error) {
	var up []State
	for _, kind := range Kinds {
		s, err := Get(home, kind)
		if err != nil {
			return nil, err
		}
		if s.Mode == "remote" {
			up = append(up, s)
		}
	}
	return up, nil
}

func save(home string, s State) error {
	if err := os.MkdirAll(dir(home), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(kindPath(home, s.Kind), append(data, '\n'), 0o600)
}

// Down closes the tunnel for *kind*, killing its process. Idempotent.
func Down(home, kind string) error {
	if kind == "" {
		return DownAll(home)
	}
	s, err := Get(home, kind)
	if err != nil {
		return err
	}
	if s.PID > 0 {
		_ = syscall.Kill(s.PID, syscall.SIGTERM)
	}
	if err := os.Remove(kindPath(home, kind)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if kind == KindOllama {
		// The pre-kind file describes the same tunnel.
		if err := os.Remove(currentPath(home)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// DownAll closes every tunnel. Idempotent.
func DownAll(home string) error {
	var first error
	for _, kind := range Kinds {
		if err := Down(home, kind); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// clearState is Down without touching processes, kept for callers that only want
// the bookkeeping.
func clearState(home string) error { return Down(home, KindOllama) }

// Up starts (or replaces) the tunnel described by *s*. A local mode just clears it.
func Up(home string, s State) error {
	s = normalise(s, s.Kind)
	if err := Down(home, s.Kind); err != nil {
		return err
	}
	if s.Mode != "remote" {
		s.Mode, s.PID, s.Target = "local", 0, ""
		return save(home, s)
	}
	if s.Target == "" {
		return fmt.Errorf("%s tunnel requires a target user@host", s.Kind)
	}
	if err := checkReachable(s.Target); err != nil {
		return err
	}
	cmd := sshForward(s.Target, s.Port)
	if err := cmd.Start(); err != nil {
		return err
	}
	s.Target = hostOnly(s.Target)
	s.PID = cmd.Process.Pid
	return save(home, s)
}

func checkReachable(target string) error {
	c := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", target, "true")
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c.Run()
}

func sshForward(target string, port int) *exec.Cmd {
	args := []string{"-N",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveInterval=30", "-o", "ServerAliveCountMax=3",
		// Bind 0.0.0.0, not the ssh default loopback: the stab container
		// reaches the host via host.docker.internal (bridge gateway), which is
		// not 127.0.0.1, so a loopback-only forward is unreachable from inside
		// the container. 0.0.0.0 matches Ollama's own LAN-exposed default.
		"-L", fmt.Sprintf("0.0.0.0:%d:localhost:%d", port, port), target}
	bin := "ssh"
	if p, err := exec.LookPath("autossh"); err == nil {
		bin = p
		args = append([]string{"-M", "0"}, args...)
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	// Detach into its own session so the tunnel survives `stables` exiting.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}

// List renders every tunnel that is up, one line each.
func List(home string) (string, error) {
	up, err := Active(home)
	if err != nil {
		return "", err
	}
	if len(up) == 0 {
		return "no tunnels", nil
	}
	lines := make([]string, 0, len(up))
	for _, s := range up {
		lines = append(lines, fmt.Sprintf("%s: remote %s port %d pid %d", s.Kind, s.Target, s.Port, s.PID))
	}
	return strings.Join(lines, "\n"), nil
}

// Host returns the hostname stab should name the tunnel provider after.
func (s State) Host(localHostname string) string {
	if s.Mode == "remote" && s.Target != "" {
		return s.Target
	}
	return localHostname
}

// PortString returns the port for URL building.
func (s State) PortString() string { return strconv.Itoa(s.Port) }

func hostOnly(s string) string {
	if i := strings.IndexByte(s, '@'); i >= 0 {
		return s[i+1:]
	}
	return s
}
