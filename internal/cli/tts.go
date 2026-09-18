package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	ttscmp "github.com/stables/stables/internal/components/tts"
	"github.com/stables/stables/internal/state"
	"github.com/stables/stables/internal/tunnel"
)

// cmdTTS launches the terminal client. `stables tts` is the whole interface
// for day-to-day use: the client talks to the backend over HTTP, so this only
// has to find the binary and hand over the terminal.
func cmdTTS(ctx context.Context, home string, args []string) int {
	// Lifecycle and tunnelling are handled here rather than passed to the TUI, which
	// would take "up" as text to read aloud.
	if len(args) > 0 {
		switch args[0] {
		case "up", "down", "list", "tunnel":
			return cmdTTSControl(ctx, home, args)
		}
	}
	m, err := state.Load(home)
	if err != nil {
		fmt.Fprintln(os.Stderr, "stables:", err)
		return 1
	}
	component, ok := m.Installations["tts"]
	if !ok {
		fmt.Fprintln(os.Stderr, "stables: voice is not installed — run: stables install tts")
		return 1
	}

	binary := component.BinaryPath
	if binary == "" {
		binary = filepath.Join(home, ".local", "bin", "ratts-cli")
	}
	if _, err := os.Stat(binary); err != nil {
		fmt.Fprintf(os.Stderr, "stables: %s is missing — run: stables install tts\n", binary)
		return 1
	}

	// The backend may have been stopped (`docker compose down`) while the client
	// stayed installed. Say which it is, rather than reporting a bare connection
	// error from the TUI.
	if !ttscmp.Reachable(ttscmp.DefaultData(home)) {
		fmt.Fprintf(os.Stderr,
			"stables: voice: nothing is answering on port %d; start it with: stables install tts\n",
			component.TTSPort)
	}

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "stables: voice:", err)
		return 1
	}
	return 0
}

// cmdTTSControl starts and stops the voice stack, and points its port at a remote
// one. The container is disposable — the weights, the voices and the cache are all
// mounted — so stopping it costs nothing but the container.
func cmdTTSControl(ctx context.Context, home string, args []string) int {
	// Before the install check: a tunnel exists to reach a *remote* voice stack, so
	// closing one, or being told how, must not require a local install.
	if args[0] == "tunnel" {
		return cmdComponentTunnel(ctx, home, tunnel.KindTTS, args[1:])
	}
	m, _ := state.Load(home)
	component, ok := m.Installations["tts"]
	if !ok || component.ComposeDir == "" {
		fmt.Fprintln(os.Stderr, "stables: no local voice install; run: stables install tts")
		return 1
	}
	switch args[0] {
	case "up":
		if err := runDocker(ctx, component.ComposeDir, "compose", "up", "-d"); err != nil {
			fmt.Fprintln(os.Stderr, "stables: tts up:", err)
			return 1
		}
		fmt.Println("stables: tts started")
		return 0
	case "down":
		if err := runDocker(ctx, component.ComposeDir, "compose", "down"); err != nil {
			fmt.Fprintln(os.Stderr, "stables: tts down:", err)
			return 1
		}
		fmt.Println("stables: tts stopped")
		return 0
	case "list":
		return cmdTTSList(home, component)
	}
	return 2
}

// cmdTTSList answers the three questions worth asking about a voice install: where
// it is, whether it is answering, and whether it is answering for itself.
func cmdTTSList(home string, component state.Component) int {
	d := ttscmp.DefaultData(home)
	if component.TTSPort != 0 {
		d.Port = component.TTSPort
	}
	answering := "not answering"
	if ttscmp.Reachable(d) {
		answering = "answering"
	}
	fmt.Printf("install:  %s\n", component.ComposeDir)
	fmt.Printf("port:     %d (%s)\n", d.Port, answering)
	if s, err := tunnel.Get(home, tunnel.KindTTS); err == nil && s.Mode == "remote" {
		fmt.Printf("tunnel:   remote %s\n", s.Target)
	} else {
		fmt.Println("tunnel:   none")
	}
	return 0
}
