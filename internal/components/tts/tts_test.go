package tts

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stables/stables/internal/assets"
)

// skipWithoutAssets skips when the generated runtime assets are not staged,
// which is the case on a fresh clone that has not run `make assets-common`.
func skipWithoutAssets(t *testing.T) {
	t.Helper()
	if !assets.Has("tts/docker-compose.yml.tmpl") {
		t.Skip("runtime assets not staged: run `make assets-common`")
	}
}

func TestRenderComposeLoopbackByDefaultAndCappedLog(t *testing.T) {
	skipWithoutAssets(t)
	d := DefaultData("/home/alice")
	b, err := RenderCompose(d)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		// The API is unauthenticated and a voice profile is biometric data.
		`"127.0.0.1:17493:17493"`,
		`/home/alice/.stables/voices:/data/voices`,
		`STABLES_VOICES_DIR: /data/voices`,
		// Without this a recreate re-downloads gigabytes of Qwen weights.
		`HF_HOME: /data/hf`,
		// Docker's json-file driver does not rotate on its own.
		`max-size: "10m"`,
		`max-file: "3"`,
		`restart: unless-stopped`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("compose missing %q:\n%s", want, s)
		}
	}
}

func TestRenderComposeNamesTheProjectSoItCannotCollide(t *testing.T) {
	skipWithoutAssets(t)
	b, err := RenderCompose(DefaultData("/home/alice"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	// Without this, Compose takes the project name from the install directory
	// (~/.stables/tts), which is the same project name as any other checkout
	// called tts on the machine.
	if !strings.Contains(s, "name: stables-tts\n") {
		t.Errorf("project name must be explicit:\n%s", s)
	}
	if !strings.Contains(s, "image: stables-tts:local") {
		t.Errorf("image tag must not depend on the directory:\n%s", s)
	}
}

func TestReadyLinesNameBothWaysIn(t *testing.T) {
	lines := readyLines(DefaultData("/home/alice"), "/home/alice/.local/bin/ratts-cli")
	joined := strings.Join(lines, "\n")

	// The wrapper and the client itself: ratts-cli is the name that appears in a
	// process list, so it is worth naming.
	for _, want := range []string{"stables tts", "ratts-cli", "http://127.0.0.1:17493"} {
		if !strings.Contains(joined, want) {
			t.Errorf("ready message should mention %q:\n%s", want, joined)
		}
	}
	// The label follows the install, not the machine: DefaultData has no GPU.
	if !strings.Contains(joined, "CPU") {
		t.Errorf("a CPU install should say so:\n%s", joined)
	}

	gpu := DefaultData("/home/alice")
	gpu.GPUEnabled = true
	if !strings.Contains(strings.Join(readyLines(gpu, "/bin/ratts-cli"), "\n"), "GPU") {
		t.Error("a GPU install should say so")
	}
}

func TestReadyLinesGiveThePathWhenItIsNotOnPath(t *testing.T) {
	// `ratts-cli` alone would not run in that case, so the path is what helps.
	lines := readyLines(DefaultData("/home/alice"), "/home/alice/.stables/tts/bin/ratts-cli")
	if !strings.Contains(strings.Join(lines, "\n"), "/home/alice/.stables/tts/bin/ratts-cli") {
		t.Errorf("the path must be given when the client is not on PATH:\n%s", strings.Join(lines, "\n"))
	}
}

func TestRenderComposeTellsTheBuildWhetherThereIsAGPU(t *testing.T) {
	skipWithoutAssets(t)
	// A CPU host must not be handed CUDA wheels: for one thing they are gigabytes
	// it cannot use, and for another the two images would share a cache key.
	for gpu, want := range map[bool]string{true: `USE_GPU: "1"`, false: `USE_GPU: "0"`} {
		d := DefaultData("/home/alice")
		d.GPUEnabled = gpu
		b, err := RenderCompose(d)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), want) {
			t.Errorf("gpu=%v: compose must pass %s:\n%s", gpu, want, b)
		}
	}
}

func TestRenderComposeTellsTheBuildWhetherToIncludeCloning(t *testing.T) {
	skipWithoutAssets(t)
	// Only when preloading: fetching weights for an engine the image does not
	// contain would achieve nothing.
	for qwen, want := range map[bool]string{true: `WITH_QWEN: "1"`, false: `WITH_QWEN: "0"`} {
		d := DefaultData("/home/alice")
		d.QwenEnabled = qwen
		b, err := RenderCompose(d)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), want) {
			t.Errorf("qwen=%v: compose must pass %s:\n%s", qwen, want, b)
		}
	}
}

func TestDockerfileInstallsCloningBeforeOnnxruntimeGPU(t *testing.T) {
	skipWithoutAssets(t)
	dockerfile := string(assets.MustRead("tts/backend/Dockerfile"))

	qwen := strings.Index(dockerfile, "-r /tmp/requirements-qwen.txt")
	// The install is quoted and pinned, so match the package spec rather than the
	// whole command.
	ortGPU := strings.Index(dockerfile, "onnxruntime-gpu==")
	base := strings.Index(dockerfile, "requirements-docker.txt")
	if qwen < 0 || ortGPU < 0 || base < 0 {
		t.Fatalf("expected all three install steps:\n%s", dockerfile)
	}
	if qwen < base {
		t.Error("the cloning engine goes after the base requirements")
	}
	// qwen-tts depends on plain onnxruntime. Installing it after onnxruntime-gpu
	// would replace the GPU build with the CPU one, costing the ~10x that the
	// pinned onnxruntime-gpu exists to provide.
	if qwen > ortGPU {
		t.Error("the cloning engine must be installed before onnxruntime-gpu")
	}
	if !strings.Contains(dockerfile[:qwen], `[ "$WITH_QWEN" = "1" ]`) {
		t.Error("the cloning install must be behind the WITH_QWEN check")
	}
}

func TestPreloadIsOffUnlessAskedFor(t *testing.T) {
	// The default install must not drag in gigabytes of weights or the extra
	// image layers for an engine most installs will never use.
	d := DefaultData("/home/alice")
	if d.QwenEnabled {
		t.Error("cloning must not be enabled by default")
	}
}

func TestDockerfileInstallsOnnxruntimeGPUOnlyWithAGPU(t *testing.T) {
	skipWithoutAssets(t)
	dockerfile := string(assets.MustRead("tts/backend/Dockerfile"))

	if !strings.Contains(dockerfile, "ARG USE_GPU") {
		t.Fatal("the build needs to know whether there is a GPU")
	}
	// onnxruntime-gpu replaces the CPU onnxruntime kokoro-onnx pulls in, so it has
	// to be installed after the requirements, and only when there is a GPU.
	// Match the package spec, not the whole command: the install is quoted and
	// pinned, so the bare command text does not appear.
	requirements := strings.Index(dockerfile, "requirements-docker.txt")
	ortGPU := strings.Index(dockerfile, "onnxruntime-gpu==")
	guard := -1
	if ortGPU >= 0 {
		guard = strings.LastIndex(dockerfile[:ortGPU], `[ "$USE_GPU" = "1" ]`)
	}
	if requirements < 0 || ortGPU < 0 || guard < 0 {
		t.Fatalf("expected a guarded onnxruntime-gpu install:\n%s", dockerfile)
	}
	if ortGPU < requirements {
		t.Error("onnxruntime-gpu must be installed after the other requirements")
	}
	if guard < requirements {
		t.Error("onnxruntime-gpu must be installed last, behind the GPU check")
	}
}

func TestRenderComposeGPUUsesExplicitReservation(t *testing.T) {
	skipWithoutAssets(t)
	d := DefaultData("/home/alice")
	d.GPUEnabled = true
	b, err := RenderCompose(d)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)

	// The short-form GPU key is silently dropped by Compose 2.26.1, which leaves
	// the container on CPU while appearing to have a GPU. Check for it as a YAML
	// key rather than as a substring, so a comment may still name it.
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "gpus:") {
			t.Errorf("must not use the short-form GPU key:\n%s", s)
		}
	}
	for _, want := range []string{"deploy:", "reservations:", "capabilities: [gpu]", "driver: nvidia"} {
		if !strings.Contains(s, want) {
			t.Fatalf("compose missing %q:\n%s", want, s)
		}
	}
}

func TestRenderComposeWithoutGPUHasNoReservation(t *testing.T) {
	skipWithoutAssets(t)
	d := DefaultData("/home/alice")
	d.GPUEnabled = false
	b, err := RenderCompose(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "nvidia") {
		t.Errorf("CPU install must not reserve a device:\n%s", b)
	}
}

func TestDefaultPortAvoidsOverloadedPorts(t *testing.T) {
	// 5000 is what every dev server reaches for first, and on a machine running
	// another speech stack it is already taken; the install then fails to publish.
	d := DefaultData("/home/alice")
	if d.Port == 5000 {
		t.Error("the default port must not be 5000")
	}
	if d.Port != defaultPort {
		t.Errorf("DefaultData should use defaultPort: %d", d.Port)
	}
}

func TestWriteTuiPrefsPointsTheClientAtThePort(t *testing.T) {
	home := t.TempDir()
	if err := writeTuiPrefs(home, 17493); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(home, ".stables", "tts", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `server_url = "http://127.0.0.1:17493"`) {
		t.Errorf("client must be pointed at the published port:\n%s", body)
	}
}

func TestWriteTuiPrefsKeepsTheUsersSettings(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".stables", "tts", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// Re-installing must not discard voices, speeds or favourites.
	existing := "server_url = \"http://127.0.0.1:5000\"\nprimary_voice = \"af_heart\"\n\n[voice_speeds]\n\"af_heart\" = 1.2\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeTuiPrefs(home, 18000); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	if !strings.Contains(s, `server_url = "http://127.0.0.1:18000"`) {
		t.Errorf("server_url must be replaced:\n%s", s)
	}
	if strings.Contains(s, ":5000") {
		t.Errorf("the old port must be gone:\n%s", s)
	}
	for _, keep := range []string{`primary_voice = "af_heart"`, `"af_heart" = 1.2`} {
		if !strings.Contains(s, keep) {
			t.Errorf("install must preserve %s:\n%s", keep, s)
		}
	}
}

func TestPortInUseDetectsAListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if !portInUse("127.0.0.1", port) {
		t.Error("a bound port must be reported as in use")
	}
	ln.Close()
	if portInUse("127.0.0.1", port) {
		t.Error("a closed port must not be reported as in use")
	}
}

func TestDefaultDataKeepsVoicesOutOfTheInstallDir(t *testing.T) {
	d := DefaultData("/home/alice")
	// Cloned voices are the user's recordings: removing the component must not
	// touch them, so they cannot live under .stables/tts/.
	if strings.Contains(d.VoicesDir, ".stables/tts/") {
		t.Errorf("voices dir must outlive the component: %s", d.VoicesDir)
	}
	if d.VoicesDir != "/home/alice/.stables/voices" {
		t.Errorf("unexpected voices dir: %s", d.VoicesDir)
	}
}

func TestModelURLs(t *testing.T) {
	for _, url := range []string{kokoroModelURL, kokoroVoicesURL, piperVoiceURL, piperConfigURL} {
		if !strings.HasPrefix(url, "https://") {
			t.Errorf("model URLs must be https: %s", url)
		}
	}
	if !strings.HasSuffix(piperConfigURL, ".onnx.json") {
		t.Errorf("piper config must accompany the model: %s", piperConfigURL)
	}
}

func TestProbeHostTurnsWildcardsIntoLoopback(t *testing.T) {
	// 0.0.0.0 is a bind address, not something to dial.
	for listen, want := range map[string]string{
		"0.0.0.0": "127.0.0.1", "": "127.0.0.1", "::": "127.0.0.1",
		"127.0.0.1": "127.0.0.1", "192.168.1.5": "192.168.1.5",
	} {
		if got := probeHost(listen); got != want {
			t.Errorf("probeHost(%q) = %q, want %q", listen, got, want)
		}
	}
}

func TestReachableIsFalseWhenNothingListens(t *testing.T) {
	// Grab a port, close it, and confirm we report not-listening rather than
	// claiming the backend is up.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	d := DefaultData("/home/alice")
	d.Port = ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	if Reachable(d) {
		t.Error("nothing is listening on that port")
	}
}

func TestReachableIsTrueWhenSomethingListens(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	d := DefaultData("/home/alice")
	d.ListenHost = "127.0.0.1"
	d.Port = ln.Addr().(*net.TCPAddr).Port
	if !Reachable(d) {
		t.Error("a listener is bound to that port")
	}
}

func TestVerifyVoicesRejectsAnEmptyVoiceList(t *testing.T) {
	// The container answers /health while seeing no models; that must not be
	// reported as a successful install.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"voices": []}`)
	}))
	defer srv.Close()

	d := DefaultData("/home/alice")
	d.ListenHost, d.Port = hostPort(t, srv.URL)
	if err := verifyVoices(d); err == nil {
		t.Fatal("an empty voice list must fail the install")
	} else if !strings.Contains(err.Error(), "no voices") {
		t.Errorf("error should say what is wrong, got: %v", err)
	}
}

func TestVerifyVoicesAcceptsAVoiceList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"voices": [{"id": "af_heart"}]}`)
	}))
	defer srv.Close()

	d := DefaultData("/home/alice")
	d.ListenHost, d.Port = hostPort(t, srv.URL)
	if err := verifyVoices(d); err != nil {
		t.Fatalf("a populated voice list should pass: %v", err)
	}
}

// hostPort splits a test server URL into the host and port the Data wants.
func hostPort(t *testing.T, raw string) (string, int) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return u.Hostname(), port
}

func TestComposeMountsTheWeightsTheInstallerDownloads(t *testing.T) {
	skipWithoutAssets(t)
	// The bind source and the download target have to be the same directory. If
	// they drift apart the installer fills one and the container mounts the other,
	// which is the empty-directory failure this design is most likely to hit.
	d := DefaultData("/home/alice")
	d.Name = "stables"
	body, err := RenderCompose(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), d.ModelsDir+":/app/models:ro") {
		t.Errorf("the weights must be mounted read-only from %s:\n%s", d.ModelsDir, body)
	}
	if !strings.Contains(string(body), "STABLES_VOICE_MODELS: /app/models") {
		t.Error("the backend must be told where the weights are mounted")
	}
}

func TestDockerfileDoesNotCarryTheWeights(t *testing.T) {
	skipWithoutAssets(t)
	dockerfile := string(assets.MustRead("tts/backend/Dockerfile"))
	if strings.Contains(dockerfile, "COPY models/") {
		t.Error("weights are mounted at runtime: copying them in means the image carries third-party weights and every rebuild re-downloads them")
	}
	for _, line := range strings.Split(dockerfile, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "VOLUME") {
			t.Error("a VOLUME over the weights would hide the mount and leave anonymous volumes behind on every recreate")
		}
	}
}

func TestTheBuildContextExcludesTheWeights(t *testing.T) {
	skipWithoutAssets(t)
	ignore := string(assets.MustRead("tts/.dockerignore"))
	if !strings.Contains(ignore, "models/") {
		t.Error("the weights are mounted, not built in: without a .dockerignore the daemon receives ~460 MB it discards")
	}
}

func TestTheMountSourceIsCreatedEvenWithoutModels(t *testing.T) {
	// --no-models skips the download, not the directory: the bind source has to
	// exist as the user's own directory, or Docker substitutes an empty one owned
	// by root and the failure arrives as an unexplained 404 instead of a message.
	home := t.TempDir()
	d := DefaultData(home)
	if err := ensureDirs(d); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{d.ModelsDir, d.VoicesDir, d.HFCacheDir} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("%s must exist: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s must be a directory", dir)
		}
	}
}

func TestInstallRefusesAModelsDirectoryWithNoWeights(t *testing.T) {
	// The failure this replaces is a five-minute wait for /health followed by
	// "connection refused", which names nothing.
	home := t.TempDir()
	d := DefaultData(home)
	if err := ensureDirs(d); err != nil {
		t.Fatal(err)
	}
	err := requireModels(d)
	if err == nil {
		t.Fatal("an empty models directory must fail the install")
	}
	if !strings.Contains(err.Error(), d.ModelsDir) {
		t.Errorf("the error must name the directory to look in: %v", err)
	}
	if !strings.Contains(err.Error(), "install tts") {
		t.Errorf("the error must say how to fix it: %v", err)
	}

	// And once a weight is there, it passes.
	if err := os.WriteFile(filepath.Join(d.ModelsDir, "kokoro-v1.0.onnx"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := requireModels(d); err != nil {
		t.Errorf("weights present, so the install should proceed: %v", err)
	}
}

func TestRemovingTheStackRemovesTheClientItInstalled(t *testing.T) {
	home := t.TempDir()
	base := filepath.Join(home, ".stables", "tts")
	// Every place the installer can write it: on PATH, the fallback inside the
	// install directory, and wherever it recorded.
	onPath := filepath.Join(home, ".local", "bin", "ratts-cli")
	fallback := filepath.Join(base, "bin", "ratts-cli")
	recorded := filepath.Join(home, "elsewhere", "ratts-cli")
	for _, path := range []string{onPath, fallback, recorded} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	removed := RemoveClient(ClientPaths(home, base, recorded)...)
	if len(removed) != 3 {
		t.Errorf("every copy must go, removed %v", removed)
	}
	for _, path := range []string{onPath, fallback, recorded} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s should be gone, got %v", path, err)
		}
	}
}

func TestRemovingAClientThatIsNotThereIsNotAnError(t *testing.T) {
	// Removal runs on machines where the install partially failed, and failing there
	// would leave the rest of the removal undone.
	home := t.TempDir()
	removed := RemoveClient(ClientPaths(home, filepath.Join(home, ".stables", "tts"), "")...)
	if len(removed) != 0 {
		t.Errorf("nothing was there, so nothing should be reported: %v", removed)
	}
}

func TestTheClientPathsCoverWhereTheInstallerWrites(t *testing.T) {
	home := "/home/alice"
	paths := ClientPaths(home, "/home/alice/.stables/tts", "/home/alice/.local/bin/ratts-cli")
	want := []string{
		"/home/alice/.local/bin/ratts-cli",
		"/home/alice/.stables/tts/bin/ratts-cli",
	}
	for _, w := range want {
		found := false
		for _, got := range paths {
			if got == w {
				found = true
			}
		}
		if !found {
			t.Errorf("the installer can write %s, so removal must look there: %v", w, paths)
		}
	}
	// And the recorded path, when it is somewhere else entirely.
	found := false
	for _, got := range paths {
		if got == "/home/alice/.local/bin/ratts-cli" {
			found = true
		}
	}
	if !found {
		t.Errorf("the recorded path must be included: %v", paths)
	}
}
