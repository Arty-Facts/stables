// Package tts installs the local text-to-speech stack: the Python backend in
// a container, and the TUI as a binary on the host.
//
// There is no published image for this component, so the backend source and the
// TUI binary are baked into the stables binary as assets and written out at
// install time, the way the stab component works. Everything the user's own
// machine should keep — cloned voices — lives outside the container.
package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/stables/stables/internal/assets"
	"github.com/stables/stables/internal/state"
)

// Options are the install-time choices. Everything has a default: the point is
// that `stables install tts` works with no flags.
type Options struct {
	Home        string
	Listen      string // bind address; default 127.0.0.1
	Port        int    // host port for the API
	GPU         string // auto | on | off
	Name        string // container/stack name prefix
	NoContainer bool   // stage the source and binary, do not start Docker
	NoModels    bool   // do not fetch the model weights
	// Preload fetches the cloning engine's weights at install time. It also puts
	// the engine in the image, because fetching weights for an engine that is not
	// installed would achieve nothing.
	Preload bool
}

// Data is what the compose template renders from.
type Data struct {
	Name        string
	Port        int
	ListenHost  string
	GPUEnabled  bool
	QwenEnabled bool
	ModelsDir   string
	VoicesDir   string
	HFCacheDir  string
}

const (
	// Kokoro is the always-available engine, so its weights are fetched at
	// install time rather than baked into the image: the image stays small and
	// the user never waits for a download mid-sentence.
	kokoroModelURL  = "https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.0/kokoro-v1.0.onnx"
	kokoroVoicesURL = "https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.0/voices-v1.0.bin"
	// Piper voices are fetched by the backend on first use; this one is fetched
	// here so a Swedish voice works immediately.
	piperVoiceURL  = "https://huggingface.co/rhasspy/piper-voices/resolve/main/sv/sv_SE/lisa/medium/sv_SE-lisa-medium.onnx"
	piperConfigURL = piperVoiceURL + ".json"

	// 5000 is not a safe default: it is the first port a dev server reaches for,
	// and on the machine this was written for it is another speech server. This
	// number is high, memorable, and outside the ranges everything else grabs.
	// Used for the container's own port as well, so a mapping reads as one port.
	defaultPort = 17493
	//: Where the TUI is installed. Not /usr/local/bin: this is a user install
	//: and may run without root.
	tuiInstallDir = ".local/bin"
	tuiBinaryName = "ratts-cli"
)

// DefaultData fills the template with the paths and ports an ordinary install
// uses.
func DefaultData(home string) Data {
	base := filepath.Join(home, ".stables", "tts")
	return Data{
		Name:        "stables",
		Port:        defaultPort,
		ListenHost:  "127.0.0.1",
		QwenEnabled: false,
		ModelsDir:   filepath.Join(base, "models"),
		VoicesDir:   filepath.Join(home, ".stables", "voices"),
		HFCacheDir:  filepath.Join(base, "hf"),
	}
}

// RenderCompose renders the compose file for d.
func RenderCompose(d Data) ([]byte, error) {
	t, err := template.New("compose").Parse(string(assets.MustRead("tts/docker-compose.yml.tmpl")))
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, d); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Install stages the stack, fetches the models, starts the container and
// records the install. Cloned voices in VoicesDir are never touched.
func Install(ctx context.Context, opts Options) error {
	d := DefaultData(opts.Home)
	if opts.Name != "" {
		d.Name = opts.Name
	}
	if opts.Port != 0 {
		d.Port = opts.Port
	}
	if opts.Listen != "" {
		d.ListenHost = opts.Listen
	}
	if d.ListenHost != "127.0.0.1" && d.ListenHost != "localhost" {
		fmt.Fprintf(os.Stderr,
			"stables: tts: warning: %s exposes an unauthenticated API; voice profiles are biometric data\n",
			d.ListenHost)
	}
	d.GPUEnabled = gpuEnabled(opts.GPU)
	d.QwenEnabled = opts.Preload

	base := filepath.Join(opts.Home, ".stables", "tts")
	if err := stage(base); err != nil {
		return err
	}
	tuiPath, err := installTUI(opts.Home, base)
	if err != nil {
		return err
	}
	// Point the client at the port this backend is actually published on, before
	// anything is started, so a port other than the default still works.
	if err := writeTuiPrefs(opts.Home, d.Port); err != nil {
		return err
	}
	if !opts.NoModels {
		if err := fetchModels(d); err != nil {
			return err
		}
	}
	if err := ensureDirs(d); err != nil {
		return err
	}
	if err := requireModels(d); err != nil {
		return err
	}

	compose, err := RenderCompose(d)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(base, "docker-compose.yml"), compose, 0o644); err != nil {
		return err
	}

	m, err := state.Load(opts.Home)
	if err != nil {
		return err
	}
	// Recorded before the container is started: once the stack exists, the
	// manifest says so, so an interrupted install can still be removed.
	m.Installations["tts"] = state.Component{
		Status:     "installed",
		ComposeDir: base,
		TTSPort:    d.Port,
		GPU:        d.GPUEnabled,
		BinaryPath: tuiPath,
		Containers: map[string]string{"tts": d.Name + "-tts"},
	}
	if err := m.Save(opts.Home); err != nil {
		return err
	}
	if opts.NoContainer {
		fmt.Printf("stables: tts: staged in %s (container not started)\n", base)
		return nil
	}

	if portInUse(d.ListenHost, d.Port) {
		return fmt.Errorf(
			"port %d is already in use on %s, so the container cannot publish there\n"+
				"  choose another one, for example: stables install tts --port %d",
			d.Port, probeHost(d.ListenHost), d.Port+1)
	}
	if err := composeRun(ctx, base, "up", "-d", "--build"); err != nil {
		return err
	}
	if err := waitHealthy(d, 5*time.Minute); err != nil {
		return err
	}
	if err := verifyVoices(d); err != nil {
		return err
	}
	if opts.Preload {
		if err := preloadQwen(ctx, base, d); err != nil {
			return err
		}
	}
	for _, line := range readyLines(d, tuiPath) {
		fmt.Println(line)
	}
	return nil
}

// Remove tears the stack down. The voices directory is deliberately kept: those
// are the user's own recordings, and re-creating a clone is not possible.
func Remove(ctx context.Context, dir string, hard bool) error {
	args := []string{"down"}
	if hard {
		args = append(args, "-v")
	}
	return composeRun(ctx, dir, args...)
}

// ClientPaths are the places the terminal client may have been written: the copy on
// PATH, the fallback inside the install directory, and wherever the installer says it
// landed.
//
// All three, because the installer prefers ~/.local/bin and falls back when it
// cannot write there. A removal that knew about one of them would leave a client on
// the PATH talking to a backend that no longer exists — which is the failure this
// exists to prevent.
func ClientPaths(home, base, installed string) []string {
	paths := []string{
		filepath.Join(home, ".local", "bin", tuiBinaryName),
		filepath.Join(base, "bin", tuiBinaryName),
	}
	if installed != "" {
		paths = append(paths, installed)
	}
	return paths
}

// RemoveClient deletes the terminal client, and reports which files it removed.
//
// A file that is not there is not an error: what matters is that it is absent
// afterwards. A file that cannot be removed is skipped rather than stopping the rest,
// because one stubborn path must not leave the others behind.
func RemoveClient(paths ...string) []string {
	var removed []string
	for _, path := range paths {
		if path == "" {
			continue
		}
		if err := os.Remove(path); err == nil {
			removed = append(removed, path)
		}
	}
	return removed
}

// stage writes the baked backend source out under base, replacing whatever was
// there so an upgrade cannot leave a stale module behind.
func stage(base string) error {
	target := filepath.Join(base, "backend")
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}

	files, err := assets.List("tts/backend")
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no backend source baked into this binary; rebuild with `make assets-common`")
	}
	for _, name := range files {
		rel := strings.TrimPrefix(name, "tts/backend/")
		dest := filepath.Join(target, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, assets.MustRead(name), 0o644); err != nil {
			return err
		}
	}

	compose, err := assets.Read("tts/docker-compose.yml.tmpl")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(base, "docker-compose.yml.tmpl"), compose, 0o644)
}

// installTUI puts the client on the user's PATH and returns where it landed.
// installTUI writes the client out and returns where it landed.
//
// ~/.local/bin is the conventional place, and where a client is expected to be.
// When it cannot be created — an unwritable home, a container running as another
// uid — the binary goes into the install directory instead and the user is told
// where it is, because a client that exists somewhere useful beats an install
// that refuses to finish over the convenience of the path.
func installTUI(home, base string) (string, error) {
	bin, err := assets.Read("tts/" + tuiBinaryName)
	if err != nil {
		return "", fmt.Errorf("the %s binary is not baked into this stables build: %w", tuiBinaryName, err)
	}
	dir := filepath.Join(home, tuiInstallDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		dir = filepath.Join(base, "bin")
		if fallbackErr := os.MkdirAll(dir, 0o755); fallbackErr != nil {
			return "", fmt.Errorf("cannot install %s to %s (%v) or to %s (%w)",
				tuiBinaryName, filepath.Join(home, tuiInstallDir), err, dir, fallbackErr)
		}
		fmt.Fprintf(os.Stderr,
			"stables: tts: cannot write %s (%v); installing %s to %s instead\n",
			filepath.Join(home, tuiInstallDir), err, tuiBinaryName, dir)
	}
	dest := filepath.Join(dir, tuiBinaryName)
	if err := os.WriteFile(dest, bin, 0o755); err != nil {
		return "", err
	}
	if _, err := exec.LookPath(tuiBinaryName); err != nil {
		fmt.Fprintf(os.Stderr, "stables: tts: %s is not on PATH; add %s to it\n", tuiBinaryName, dir)
	}
	return dest, nil
}

// portInUse reports whether something already listens on host:port.
//
// Checked before starting the container because Docker's own failure for this is
// "failed to set up container networking ... port is already allocated", which
// arrives after a build and says nothing about what to do.
func portInUse(host string, port int) bool {
	conn, err := net.DialTimeout("tcp",
		net.JoinHostPort(probeHost(host), strconv.Itoa(port)), time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// writeTuiPrefs points the client at the port the backend was published on.
//
// The client and the backend are installed together, so leaving the client on its
// built-in default would have it talk to whatever else holds that port — another
// service's API — and the failure would look like a broken install.
//
// The preferences file is TOML written by the client; this replaces the one
// server_url line and leaves every other setting alone, so re-installing does not
// discard the user's voices, speeds or favourites.
func writeTuiPrefs(home string, port int) error {
	path := filepath.Join(home, ".stables", "tts", "config.toml")
	want := fmt.Sprintf("http://127.0.0.1:%d", port)

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var lines []string
	if trimmed := strings.TrimRight(string(data), "\n"); trimmed != "" {
		lines = strings.Split(trimmed, "\n")
	}
	replaced := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "server_url") {
			lines[i] = fmt.Sprintf("server_url = %q", want)
			replaced = true
		}
	}
	if !replaced {
		lines = append(lines, fmt.Sprintf("server_url = %q", want))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

// requireModels fails before the stack starts when the directory the container
// mounts holds no weights.
//
// The weights are a bind mount now rather than part of the image, so an empty
// directory is the likeliest reason a stack that worked yesterday does not work
// today. Without this check the failure arrives as a five-minute wait for
// /health and then "connection refused", because the backend cannot load a voice
// and never starts serving — which says nothing about the actual cause.
func requireModels(d Data) error {
	if _, err := os.Stat(filepath.Join(d.ModelsDir, "kokoro-v1.0.onnx")); err != nil {
		return fmt.Errorf("no weights in %s, so the container would mount an empty directory\n"+
			"  fetch them with: stables install tts   (without --no-models)", d.ModelsDir)
	}
	return nil
}

// ensureDirs creates the directories the container mounts.
//
// ModelsDir is created even with --no-models, because it is the bind source for
// /app/models: a directory that exists and is empty fails the install with a message
// naming it, whereas one that does not exist is created by Docker, owned by root,
// and the failure arrives later as an unexplained 404.
func ensureDirs(d Data) error {
	for _, dir := range []string{d.ModelsDir, d.VoicesDir, d.HFCacheDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// fetchModels downloads anything missing into ModelsDir, skipping what is
// already there so a reinstall is cheap.
//
// This runs before the build because the weights are baked into the image: the
// directory is the build context's models/, so it has to exist first.
func fetchModels(d Data) error {
	if err := os.MkdirAll(filepath.Join(d.ModelsDir, "piper"), 0o755); err != nil {
		return err
	}
	assetsToFetch := []struct{ url, dest string }{
		{kokoroModelURL, filepath.Join(d.ModelsDir, "kokoro-v1.0.onnx")},
		{kokoroVoicesURL, filepath.Join(d.ModelsDir, "voices-v1.0.bin")},
		{piperVoiceURL, filepath.Join(d.ModelsDir, "piper", "sv_SE-lisa-medium.onnx")},
		{piperConfigURL, filepath.Join(d.ModelsDir, "piper", "sv_SE-lisa-medium.onnx.json")},
	}
	for _, item := range assetsToFetch {
		if err := fetchIfMissing(item.url, item.dest); err != nil {
			return err
		}
	}
	return nil
}

// fetchIfMissing downloads url to dest when dest does not already exist.
//
// The download goes to a temporary file first and is renamed into place only
// when complete, so an interrupted install cannot leave a truncated model that
// the backend would then fail to load.
func fetchIfMissing(url, dest string) error {
	if info, err := os.Stat(dest); err == nil && info.Size() > 0 {
		return nil
	}
	fmt.Printf("stables: tts: fetching %s\n", filepath.Base(dest))

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: HTTP %s", url, resp.Status)
	}

	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("fetch %s: %w", url, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

// waitHealthy polls the backend until it answers, so the caller does not have to
// guess how long loading a 325 MB model takes.
func waitHealthy(d Data, timeout time.Duration) error {
	url := fmt.Sprintf("http://%s:%d/health", probeHost(d.ListenHost), d.Port)
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		client := http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(url)
		if err == nil {
			var body struct {
				Status  string   `json:"status"`
				Engines []string `json:"engines"`
			}
			if json.NewDecoder(resp.Body).Decode(&body) == nil && body.Status == "healthy" {
				resp.Body.Close()
				fmt.Printf("stables: tts: engines available: %s\n", strings.Join(body.Engines, ", "))
				return nil
			}
			resp.Body.Close()
			last = "not healthy yet"
		} else {
			last = err.Error()
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("backend did not become healthy at %s within %s (last: %s); check `docker logs %s-tts`",
		url, timeout, last, d.Name)
}

// preloadQwen fetches the cloning engine's weights now, into the mounted cache.
//
// On demand means the first request for a cloned voice waits for a gigabyte or
// three; this is the same download, done at a moment when waiting is expected. It
// runs in the image's own engine, through the same mounts the server uses, so what
// it fetches is exactly what the server will find.
//
// It does not load them: the server starts cold either way, and holding a
// checkpoint in a container that is about to exit would only slow the exit.
func preloadQwen(ctx context.Context, base string, d Data) error {
	fmt.Println("stables: tts: fetching the cloning engine's weights (gigabytes)")
	script := strings.Join([]string{
		"from backend.engines.qwen import QwenEngine",
		"engine = QwenEngine()",
		"print('preloaded', engine.preload(), 'into', __import__('os').environ.get('HF_HOME', 'the default cache'))",
	}, "; ")
	args := []string{"run", "--rm", "--no-deps", "--entrypoint", "python3", "tts", "-c", script}
	if err := composeRun(ctx, base, args...); err != nil {
		return fmt.Errorf("preloading the cloning engine failed: %w", err)
	}
	return nil
}

// readyLines is what the user needs in order to use what was just installed.
//
// Both ways in are named: `stables tts` is the wrapper, and `ratts-cli` is the
// client itself, which is worth saying because that is the name that shows up in
// the file system and in a process list. When the client is not on PATH the path
// is given instead, since the bare name would not run.
func readyLines(d Data, tuiPath string) []string {
	address := fmt.Sprintf("http://%s:%d", probeHost(d.ListenHost), d.Port)

	start := "stables: tts: start the terminal client with `stables tts`, or run `ratts-cli` directly"
	if _, err := exec.LookPath(tuiBinaryName); err != nil {
		start = fmt.Sprintf(
			"stables: tts: start the terminal client with `stables tts`, or run %s (not on PATH)",
			tuiPath)
	}
	return []string{
		fmt.Sprintf("stables: tts: ready on %s (%s)", address, gpuLabel(d.GPUEnabled)),
		start,
	}
}

// verifyVoices fails the install when the backend can see no voices at all.
//
// A container whose models directory is empty still answers /health with
// "healthy" and reports zero engines, and every request after that fails with
// "unknown voice". Reporting a successful install into that state tells the user
// nothing about what went wrong, so the one thing worth checking is checked here.
func verifyVoices(d Data) error {
	url := fmt.Sprintf("http://%s:%d/v1/voices", probeHost(d.ListenHost), d.Port)
	client := http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("could not read the voice list from %s: %w", url, err)
	}
	defer resp.Body.Close()

	var body struct {
		Voices []struct {
			ID string `json:"id"`
		} `json:"voices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("could not read the voice list from %s: %w", url, err)
	}
	if len(body.Voices) == 0 {
		return fmt.Errorf("the backend is running but sees no voices, so nothing can be spoken\n"+
			"  it looks for weights in %s, which must exist on the machine running Docker\n"+
			"  check with: docker exec %s-tts ls /app/models",
			d.ModelsDir, d.Name)
	}
	fmt.Printf("stables: tts: %d voices available\n", len(body.Voices))
	return nil
}

// probeHost turns a bind address into something dialable: 0.0.0.0 is not.
func probeHost(listen string) string {
	if listen == "0.0.0.0" || listen == "" || listen == "::" {
		return "127.0.0.1"
	}
	return listen
}

func composeRun(ctx context.Context, dir string, args ...string) error {
	var cmd *exec.Cmd
	if _, err := exec.LookPath("docker"); err == nil {
		cmd = exec.CommandContext(ctx, "docker", append([]string{"compose"}, args...)...)
	} else {
		cmd = exec.CommandContext(ctx, "docker-compose", args...)
	}
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func gpuEnabled(mode string) bool {
	switch mode {
	case "on":
		return true
	case "off":
		return false
	default:
		_, err := exec.LookPath("nvidia-smi")
		return err == nil
	}
}

func gpuLabel(enabled bool) string {
	if enabled {
		return "GPU"
	}
	return "CPU"
}

// Reachable reports whether the API answers, used by `stables list`.
func Reachable(d Data) bool {
	addr := net.JoinHostPort(probeHost(d.ListenHost), strconv.Itoa(d.Port))
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
