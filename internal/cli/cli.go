package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	ollamacmp "github.com/stables/stables/internal/components/ollama"
	stabcmp "github.com/stables/stables/internal/components/stab"
	ttscmp "github.com/stables/stables/internal/components/tts"
	webuicmp "github.com/stables/stables/internal/components/webui"
	"github.com/stables/stables/internal/remote"
	"github.com/stables/stables/internal/state"
	"github.com/stables/stables/internal/tunnel"
)

const Version = "0.2.0"

func Run(args []string) int {
	if len(args) < 2 {
		usage()
		return 2
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "stables:", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	switch args[1] {
	case "version", "--version", "-v":
		fmt.Println("stables", Version)
		return 0
	case "install":
		return cmdInstall(ctx, home, args[2:])
	case "update":
		return cmdUpdate(ctx, home, args[2:])
	case "remove":
		return cmdRemove(ctx, home, args[2:])
	case "clean-install":
		return cmdCleanInstall(ctx, home, args[2:])
	case "reinstall":
		return cmdReinstall(ctx, home, args[2:])
	case "remote":
		return cmdRemote(ctx, home, args[2:])
	case "ollama":
		return cmdOllama(ctx, home, args[2:])
	case "tunnel":
		return cmdTunnel(ctx, home, args[2:])
	case "tts":
		return cmdTTS(ctx, home, args[2:])
	case "tts-mcp-ping":
		return cmdTTSPing(home, args[2:])
	case "fetch":
		return cmdFetch(ctx, home, args[2:])
	case "list", "status":
		return cmdList(home)
	default:
		usage()
		return 2
	}
}

// knownComponents lists the installable components, in help order.
var knownComponents = []string{"stab", "ollama", "webui", "tts"}

// checkComponent reports whether a name is installable. Without this, a typo
// ("stables install voisebox") falls through every `component ==` branch and
// the command reports a successful install of nothing.
func checkComponent(component string) bool {
	if component == "all" {
		return true
	}
	for _, c := range knownComponents {
		if c == component {
			return true
		}
	}
	return false
}

// unknownComponent prints the valid names and returns the usage exit code.
func unknownComponent(name string) int {
	fmt.Fprintf(os.Stderr, "stables: unknown component %q; want %s, or all\n",
		name, strings.Join(knownComponents, ", "))
	return 2
}

// knownInstallFlags are the flags `stables install` accepts.
//
// Spelled out so an unknown one is reported instead of ignored. Flags are found by
// scanning rather than by a parser, so `install tts --no-preload` used to look
// accepted and do nothing at all — and the usage text advertised that flag, which is
// how the ghost was found.
var knownInstallFlags = []string{
	"--force", "--password", "--gpu", "--data-dir", "--listen", "--port",
	"--preload", "--no-container", "--no-models", "--ollama-url", "--ollama-port",
	"--webui-port", "--name", "--network-subnet",
}

// rejectUnknownFlags returns the first flag that is not in *known*.
func rejectUnknownFlags(args []string, known []string) error {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "--") {
			continue
		}
		name, _, _ := strings.Cut(arg, "=")
		if !slicesContains(known, name) {
			return fmt.Errorf("unknown flag %s", name)
		}
	}
	return nil
}

func cmdInstall(ctx context.Context, home string, args []string) int {
	if err := rejectUnknownFlags(args, knownInstallFlags); err != nil {
		fmt.Fprintf(os.Stderr, "stables: install: %v\n", err)
		usage()
		return 2
	}
	// Nothing named means the request is a question, not an install: answer it with
	// what can be installed rather than choosing several gigabytes on the user's
	// behalf. "all" is still available, but only when asked for by name.
	component := componentArg(args, "")
	if component == "" {
		return usageInstall()
	}
	// Validate before anything touches the filesystem, so a typo cannot even
	// self-install the binary.
	if !checkComponent(component) {
		return unknownComponent(component)
	}
	force := hasFlag(args, "--force")
	password := flag(args, "--password", os.Getenv("STABLES_PASSWORD"))
	if password == "" && stabcmp.Locked() {
		password = promptPassword()
	}

	// Self-install stables to ~/.local/bin so a remote host (which only
	// received the scp'd binary) can run `stables tunnel` / `stables list`.
	//
	// A failure here is a warning, not a reason to stop. This only puts the
	// binary on PATH; it says nothing about whether the components can be
	// installed, and a home directory whose ~/.local/bin cannot be created
	// (read-only, someone else's uid, a container) would otherwise make every
	// install impossible with an error about the convenience copy.
	if err := selfInstall(home); err != nil {
		fmt.Fprintf(os.Stderr,
			"stables: warning: could not self-install to ~/.local/bin (%v); stables will not be on PATH\n", err)
	}

	if component == "all" || component == "stab" {
		if err := stabcmp.Install(ctx, home, force, password); err != nil {
			fmt.Fprintln(os.Stderr, "stables: stab:", err)
			return 1
		}
	}
	if component == "all" || component == "ollama" {
		opts := ollamacmp.Options{
			Home:          home,
			GPU:           flag(args, "--gpu", "auto"),
			Listen:        flag(args, "--listen", "0.0.0.0"),
			DataDir:       flag(args, "--data-dir", ""),
			Name:          flag(args, "--name", ""),
			OllamaPort:    intFlag(args, "--ollama-port", 0),
			NetworkSubnet: flag(args, "--network-subnet", ""),
		}
		if err := ollamacmp.Install(ctx, opts); err != nil {
			fmt.Fprintln(os.Stderr, "stables: ollama:", err)
			return 1
		}
	}
	if component == "all" || component == "webui" {
		opts := webuicmp.Options{
			Home:          home,
			OllamaURL:     flag(args, "--ollama-url", ""),
			WebUIPort:     intFlag(args, "--webui-port", 0),
			Listen:        flag(args, "--listen", "127.0.0.1"),
			Name:          flag(args, "--name", ""),
			NetworkSubnet: flag(args, "--network-subnet", ""),
		}
		if err := webuicmp.Install(ctx, opts); err != nil {
			fmt.Fprintln(os.Stderr, "stables: webui:", err)
			return 1
		}
	}
	if component == "all" || component == "tts" {
		opts := ttscmp.Options{
			Home:        home,
			Listen:      flag(args, "--listen", "127.0.0.1"),
			GPU:         flag(args, "--gpu", "auto"),
			Port:        intFlag(args, "--port", 0),
			Name:        flag(args, "--name", ""),
			NoContainer: hasFlag(args, "--no-container"),
			NoModels:    hasFlag(args, "--no-models"),
			Preload:     hasFlag(args, "--preload"),
		}
		if err := ttscmp.Install(ctx, opts); err != nil {
			fmt.Fprintln(os.Stderr, "stables: tts:", err)
			return 1
		}
	}
	fmt.Println("stables: install complete")
	return 0
}

func cmdUpdate(ctx context.Context, home string, args []string) int {
	// Nothing named means the request is a question, not an install: answer it with
	// what can be installed rather than choosing several gigabytes on the user's
	// behalf. "all" is still available, but only when asked for by name.
	component := componentArg(args, "all")
	if !checkComponent(component) {
		return unknownComponent(component)
	}
	m, _ := state.Load(home)
	if component == "all" || component == "stab" {
		if err := stabcmp.Update(ctx, home); err != nil {
			fmt.Fprintln(os.Stderr, "stables: stab:", err)
			return 1
		}
	}
	if component == "all" || component == "ollama" {
		if c, ok := m.Installations["ollama"]; ok && c.ComposeDir != "" {
			_ = runDocker(ctx, c.ComposeDir, "compose", "pull")
			_ = runDocker(ctx, c.ComposeDir, "compose", "up", "-d")
		}
	}
	if component == "all" || component == "tts" {
		// The image is built from the staged source, so updating means
		// rebuilding rather than pulling.
		if c, ok := m.Installations["tts"]; ok && c.ComposeDir != "" {
			_ = runDocker(ctx, c.ComposeDir, "compose", "up", "-d", "--build")
		}
	}
	if component == "all" || component == "webui" {
		if c, ok := m.Installations["webui"]; ok && c.ComposeDir != "" {
			_ = runDocker(ctx, c.ComposeDir, "compose", "pull")
			_ = runDocker(ctx, c.ComposeDir, "compose", "up", "-d")
		}
	}
	return 0
}

func cmdRemove(ctx context.Context, home string, args []string) int {
	component := componentArg(args, "")
	if component == "" || !checkComponent(component) {
		if component != "" {
			return unknownComponent(component)
		}
		usage()
		return 2
	}
	// soft remove: keep Ollama models + WebUI logs (data volumes)
	removeLocal(ctx, home, component, false)
	return 0
}

func cmdCleanInstall(ctx context.Context, home string, args []string) int {
	component := componentArg(args, "")
	if component == "" || !checkComponent(component) {
		if component != "" {
			return unknownComponent(component)
		}
		usage()
		return 2
	}
	// hard remove: wipe data volumes, then fresh install
	removeLocal(ctx, home, component, true)
	return cmdInstall(ctx, home, []string{component})
}

// cmdReinstall is a soft reinstall by default (down + up, keeping data
// volumes); `--clean` wipes them first for a fresh system.
func cmdReinstall(ctx context.Context, home string, args []string) int {
	component := componentArg(args, "")
	if component == "" || !checkComponent(component) {
		if component != "" {
			return unknownComponent(component)
		}
		usage()
		return 2
	}
	removeLocal(ctx, home, component, hasFlag(args, "--clean"))
	return cmdInstall(ctx, home, []string{component})
}

func removeLocal(ctx context.Context, home, component string, hard bool) {
	m, _ := state.Load(home)
	if component == "ollama" || component == "all" {
		if c, ok := m.Installations["ollama"]; ok && c.ComposeDir != "" {
			_ = ollamacmp.Remove(ctx, c.ComposeDir, hard)
		}
		delete(m.Installations, "ollama")
	}
	if component == "webui" || component == "all" {
		if c, ok := m.Installations["webui"]; ok && c.ComposeDir != "" {
			_ = webuicmp.Remove(ctx, c.ComposeDir, hard)
		}
		delete(m.Installations, "webui")
	}
	if component == "tts" || component == "all" {
		base := filepath.Join(home, ".stables", "tts")
		if c, ok := m.Installations["tts"]; ok {
			if c.ComposeDir != "" {
				base = c.ComposeDir
				// Only if there is something to take down: a half-removed install has
				// no compose file, and asking docker about it prints an error that
				// says nothing about the state the user is actually in.
				if _, err := os.Stat(filepath.Join(c.ComposeDir, "docker-compose.yml")); err == nil {
					_ = ttscmp.Remove(ctx, c.ComposeDir, hard)
				}
			}
			// The client is a binary outside the compose project, so compose knows
			// nothing about it: without this, `stables remove tts` left ratts-cli on
			// the PATH pointing at a backend that had gone.
			for _, path := range ttscmp.RemoveClient(ttscmp.ClientPaths(home, base, c.BinaryPath)...) {
				fmt.Printf("stables: removed %s\n", path)
			}
		}
		if hard {
			// The weights and the Qwen cache live here. They are re-downloadable, and
			// --clean means clean; the cloned voices are elsewhere and are kept.
			if err := os.RemoveAll(base); err != nil {
				fmt.Fprintf(os.Stderr, "stables: tts: could not remove %s: %v\n", base, err)
			} else {
				fmt.Printf("stables: removed %s\n", base)
			}
		}
		// Cloned voices are the user's own recordings and are never removed.
		delete(m.Installations, "tts")
	}
	if component == "stab" || component == "all" {
		_ = stabcmp.Remove(home)
		if hard {
			cleanupHomeArtifacts(ctx, home, []string{".stables", filepath.Join(".pi", "agent")})
		}
		delete(m.Installations, "stab")
	}
	_ = m.Save(home)
}

// cmdRemote scp's the current binary to the target and runs the given action
// there. Stateless: nothing about the remote is remembered locally.
func cmdRemote(ctx context.Context, home string, args []string) int {
	if len(args) < 3 {
		usage()
		return 2
	}
	action, component, targetArg := args[0], args[1], args[2]
	t := remote.Parse(targetArg)
	if action == "install" || action == "clean-install" {
		if err := remote.CopySelf(t); err != nil {
			fmt.Fprintln(os.Stderr, "stables: scp:", err)
			return 1
		}
	}
	remoteArgs := append([]string{action, component}, args[3:]...)
	if err := remote.Run(t, remoteArgs...); err != nil {
		fmt.Fprintln(os.Stderr, "stables: remote:", err)
		return 1
	}
	return 0
}

func cmdOllama(ctx context.Context, home string, args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	// Before the install check, for the same reason as the voice stack: the point of
	// a tunnel is to reach a service that is not here.
	if args[0] == "tunnel" {
		return cmdComponentTunnel(ctx, home, tunnel.KindOllama, args[1:])
	}
	m, _ := state.Load(home)
	c, ok := m.Installations["ollama"]
	if !ok || c.ComposeDir == "" {
		fmt.Fprintln(os.Stderr, "stables: no local ollama install; run: stables install ollama")
		return 1
	}
	switch args[0] {
	case "list":
		if err := ollamacmp.List(ctx, c.ComposeDir); err != nil {
			fmt.Fprintln(os.Stderr, "stables: ollama list:", err)
			return 1
		}
		return 0
	case "pull":
		models := nonFlagArgs(args[1:])
		if len(models) == 0 {
			fmt.Fprintln(os.Stderr, "stables: ollama pull requires at least one model name")
			return 2
		}
		if err := ollamacmp.Pull(ctx, c.ComposeDir, models); err != nil {
			fmt.Fprintln(os.Stderr, "stables: ollama pull:", err)
			return 1
		}
		return 0
	case "up":
		if err := runDocker(ctx, c.ComposeDir, "compose", "up", "-d"); err != nil {
			fmt.Fprintln(os.Stderr, "stables: ollama up:", err)
			return 1
		}
		fmt.Fprintln(os.Stdout, "stables: ollama started")
		return 0
	case "down":
		if err := runDocker(ctx, c.ComposeDir, "compose", "down"); err != nil {
			fmt.Fprintln(os.Stderr, "stables: ollama down:", err)
			return 1
		}
		fmt.Fprintln(os.Stdout, "stables: ollama stopped")
		return 0
	default:
		usage()
		return 2
	}
}

func cmdTunnel(ctx context.Context, home string, args []string) int {
	if len(args) == 0 {
		return usageTunnel()
	}
	switch args[0] {
	case "down":
		// Close everything, restoring any local stack a tunnel had stopped.
		up, err := tunnel.Active(home)
		if err != nil {
			fmt.Fprintln(os.Stderr, "stables: tunnel:", err)
			return 1
		}
		for _, s := range up {
			if code := cmdComponentTunnel(ctx, home, s.Kind, []string{"down"}); code != 0 {
				return code
			}
		}
		if len(up) == 0 {
			if err := tunnel.DownAll(home); err != nil {
				fmt.Fprintln(os.Stderr, "stables: tunnel:", err)
				return 1
			}
			fmt.Println("stables: no tunnels were up")
		}
		return 0
	case "list":
		line, err := tunnel.List(home)
		if err != nil {
			fmt.Fprintln(os.Stderr, "stables: tunnel:", err)
			return 1
		}
		fmt.Println(line)
		return 0
	default:
		return usageTunnel()
	}
}

// cmdComponentTunnel points one component's port at a remote server.
//
// A forward binds the same local port the component listens on, so the two cannot
// both be up. The local stack is stopped rather than the user being told to stop it,
// and that is recorded, so `down` puts back what it took away. A tunnel that fails to
// start restores the local stack immediately: leaving the user with neither the
// remote service nor the local one is the one outcome worth going out of the way to
// avoid.
func cmdComponentTunnel(ctx context.Context, home, kind string, args []string) int {
	m, _ := state.Load(home)
	component, installed := m.Installations[kind]
	port := tunnel.Port(kind)

	if len(args) > 0 && args[0] == "down" {
		before, err := tunnel.Get(home, kind)
		if err != nil {
			fmt.Fprintln(os.Stderr, "stables: tunnel:", err)
			return 1
		}
		if err := tunnel.Down(home, kind); err != nil {
			fmt.Fprintln(os.Stderr, "stables: tunnel:", err)
			return 1
		}
		fmt.Printf("stables: %s tunnel down\n", kind)
		if before.LocalStopped {
			if err := restoreLocal(ctx, kind, component); err != nil {
				fmt.Fprintf(os.Stderr, "stables: %s: the tunnel closed but the local stack did not restart: %v\n", kind, err)
				return 1
			}
			fmt.Printf("stables: %s: local stack restarted (the tunnel had stopped it)\n", kind)
		}
		return 0
	}

	if len(args) == 0 {
		return usageTunnel()
	}
	target := args[0]
	if !installed || component.ComposeDir == "" {
		// Tunnelling is still useful without a local install, but there is nothing
		// to stop, and saying so avoids a confusing "no local install" refusal.
		fmt.Fprintf(os.Stderr, "stables: %s: no local install, so nothing is holding port %d\n", kind, port)
	}

	stopped, err := stopLocalForPort(ctx, kind, component, installed)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stables: %s tunnel: %v\n", kind, err)
		return 1
	}
	if stopped {
		fmt.Printf("stables: %s: local stack stopped to free port %d\n", kind, port)
	}

	err = tunnel.Up(home, tunnel.State{
		Kind:         kind,
		Mode:         "remote",
		Target:       target,
		Port:         port,
		LocalStopped: stopped,
	})
	if err != nil {
		if stopped {
			if rerr := restoreLocal(ctx, kind, component); rerr != nil {
				fmt.Fprintf(os.Stderr, "stables: %s: and the local stack did not restart: %v\n", kind, rerr)
			} else {
				fmt.Printf("stables: %s: local stack restarted (the tunnel did not start)\n", kind)
			}
		}
		fmt.Fprintln(os.Stderr, "stables: tunnel:", err)
		return 1
	}
	fmt.Printf("stables: %s tunnel to %s on port %d\n", kind, target, port)
	return 0
}

// localStackRunning reports whether the component's container is up. A variable so a
// test can answer the question without a Docker daemon.
var localStackRunning = func(ctx context.Context, dir string) bool {
	cmd := exec.CommandContext(ctx, "docker", "compose", "ps", "-q")
	cmd.Dir = dir
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// stopLocalForPort frees the port the tunnel needs, and reports whether it had to.
func stopLocalForPort(ctx context.Context, kind string, component state.Component, installed bool) (bool, error) {
	if !installed || component.ComposeDir == "" {
		return false, nil
	}
	if !localStackRunning(ctx, component.ComposeDir) {
		return false, nil
	}
	fmt.Printf("stables: %s: stopping the local stack so the tunnel can hold port %d\n",
		kind, tunnel.Port(kind))
	if err := runDocker(ctx, component.ComposeDir, "compose", "stop"); err != nil {
		return false, fmt.Errorf("stopping the local stack: %w", err)
	}
	return true, nil
}

// restoreLocal starts a component's stack again, after a tunnel stopped it.
func restoreLocal(ctx context.Context, kind string, component state.Component) error {
	if component.ComposeDir == "" {
		return fmt.Errorf("no install directory recorded for %s", kind)
	}
	return runDocker(ctx, component.ComposeDir, "compose", "up", "-d")
}

func cmdFetch(ctx context.Context, home string, args []string) int {
	component := componentArg(args, "")
	m, _ := state.Load(home)
	switch component {
	case "stab":
		stab := filepath.Join(home, ".local", "bin", "stab")
		cmd := exec.CommandContext(ctx, stab, "fetch")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		cmd.Env = append(os.Environ(), "HOME="+home)
		if err := cmd.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "stables: stab fetch:", err)
			return 1
		}
		return 0
	case "webui":
		c, ok := m.Installations["webui"]
		if !ok || c.ComposeDir == "" {
			fmt.Fprintln(os.Stderr, "stables: no local webui install; run: stables install webui")
			return 1
		}
		if err := webuicmp.Fetch(ctx, home, c.ComposeDir); err != nil {
			fmt.Fprintln(os.Stderr, "stables: webui fetch:", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintln(os.Stderr, "usage: stables fetch [stab|webui]")
		return 2
	}
}

func cmdList(home string) int {
	m, err := state.Load(home)
	if err != nil {
		fmt.Fprintln(os.Stderr, "stables:", err)
		return 1
	}
	fmt.Printf("%-12s %-13s %s\n", "COMPONENT", "STATUS", "DETAIL")
	known := append([]string{}, knownComponents...)
	shown := map[string]bool{}
	for _, name := range known {
		c, ok := m.Installations[name]
		if !ok {
			fmt.Printf("%-12s %-13s\n", name, "not installed")
			continue
		}
		shown[name] = true
		fmt.Printf("%-12s %-13s %s\n", name, c.Status, componentDetail(c))
	}
	// any extra/unknown components still in the manifest
	var extra []string
	for name := range m.Installations {
		if !shown[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	for _, name := range extra {
		c := m.Installations[name]
		fmt.Printf("%-12s %-13s %s\n", name, c.Status, componentDetail(c))
	}
	return 0
}

func componentDetail(c state.Component) string {
	if c.OllamaPort != 0 {
		return fmt.Sprintf("ollama port %d", c.OllamaPort)
	}
	if c.TTSPort != 0 {
		return fmt.Sprintf("tts http://localhost:%d (TUI %s)", c.TTSPort, c.BinaryPath)
	}
	if c.WebUIPort != 0 {
		return fmt.Sprintf("Open WebUI http://localhost:%d (ollama %s)", c.WebUIPort, c.OllamaURL)
	}
	return c.ComposeDir
}

func componentArg(args []string, def string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if flagTakesValue(a) {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a
	}
	return def
}

func flagTakesValue(a string) bool {
	return a == "--gpu" || a == "--listen" || a == "--data-dir" || a == "--name" ||
		a == "--ollama-port" || a == "--webui-port" || a == "--network-subnet" || a == "--ollama-url" ||
		a == "--port" || a == "--password"
}

func nonFlagArgs(args []string) []string {
	out := []string{}
	for i := 0; i < len(args); i++ {
		if flagTakesValue(args[i]) {
			i++
			continue
		}
		if strings.HasPrefix(args[i], "-") {
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func flag(args []string, name, def string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, name+"=") {
			return strings.TrimPrefix(a, name+"=")
		}
	}
	return def
}

func intFlag(args []string, name string, def int) int {
	v := flag(args, name, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name || strings.HasPrefix(a, name+"=") {
			return true
		}
	}
	return false
}

func cleanupHomeArtifacts(ctx context.Context, home string, rels []string) {
	for _, rel := range rels {
		_ = os.RemoveAll(filepath.Join(home, rel))
	}
	left := []string{}
	for _, rel := range rels {
		if _, err := os.Stat(filepath.Join(home, rel)); err == nil {
			left = append(left, rel)
		}
	}
	if len(left) == 0 {
		return
	}
	args := []string{"run", "--rm", "-v", home + ":/host_home", "alpine", "sh", "-c", "rm -rf " + dockerRemoveList(left)}
	_ = runDocker(ctx, "", args...)
}

func dockerRemoveList(rels []string) string {
	parts := make([]string, 0, len(rels))
	for _, rel := range rels {
		parts = append(parts, "/host_home/"+strings.TrimPrefix(filepath.ToSlash(rel), "/"))
	}
	return strings.Join(parts, " ")
}

var runDocker = func(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// selfInstall copies the running stables binary to ~/.local/bin/stables so it
// is on PATH (idempotent). Atomic write avoids ETXTBSY when overwriting a
// running binary.
func selfInstall(home string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	return writeFileAtomic(exe, filepath.Join(bin, "stables"))
}

func writeFileAtomic(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".stables-install-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op after successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, dst)
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  stables install <component> [--force] [--password <pw>] [stab|ollama|webui|tts|all]
        [--gpu auto|cuda|cpu|rocm] [--data-dir PATH] [--listen auto|0.0.0.0]
        [--port N] [--preload] [--no-container] [--no-models]
        [--ollama-url URL] [--ollama-port N] [--webui-port N] [--name N]
        [--network-subnet CIDR]
  stables remote install [stab|ollama] user@host [--force]
  stables remote clean-install [stab|ollama] user@host
  stables update [--build] [stab|ollama|webui|tts|all]
  stables remove [stab|ollama|webui|tts|all]
  stables reinstall [--clean] [stab|ollama|webui|tts|all]
  stables clean-install [stab|ollama|webui|tts|all]
  stables ollama list
  stables ollama pull [model...]
  stables ollama up
  stables ollama down
  stables ollama tunnel user@host        forward the model server
  stables ollama tunnel down
  stables tts tunnel user@host           forward the voice backend
  stables tts tunnel down
  stables tts up | down | list
  stables tunnel list                    every tunnel that is up
  stables tunnel down                    close them all and restore what they stopped
  stables fetch [stab]
  stables tts
  stables tts-mcp-ping "text" [--kind ping|say|summary|question] [--source NAME]
  stables list
  stables version`)
}

// usageInstall lists the components, because `stables install` on its own is a
// question about what can be installed.
func usageInstall() int {
	fmt.Fprintln(os.Stderr, `usage: stables install <component> [options]

Components, each independent of the others:
  stab     the agent runtime: a container per project, tmux, Pi
  ollama   a local model server
  webui    Open WebUI, talking to ollama
  tts      the voice stack: text to speech, cloned voices, announcements
  all      every component above (several gigabytes)

Options:
  --force  --password <pw>  --gpu auto|cuda|cpu|rocm  --data-dir PATH
  --listen auto|0.0.0.0  --port N  --preload  --no-container  --no-models
  --ollama-url URL  --ollama-port N  --webui-port N  --name N
  --network-subnet CIDR

Install one component at a time; the components do not depend on each other.`)
	return 2
}

func usageTunnel() int {
	fmt.Fprintln(os.Stderr, `usage: stables tunnel <command>

Commands:
  list                   every tunnel that is up
  down                   close them all, restoring any local stack they stopped

Per component, next to the other verbs:
  stables ollama tunnel user@host | down     the model server
  stables tts tunnel user@host | down        the voice backend

A tunnel forwards the component's own port, so it takes the place of a local stack:
tunnelling stops the local one, and closing the tunnel starts it again.`)
	return 2
}
