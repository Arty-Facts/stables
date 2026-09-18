package project

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stables/stab/internal/config"
	"github.com/stables/stab/internal/docker"
	"github.com/stables/stab/internal/store"
)

// fakeDocker records lifecycle calls and simulates a tmux session inside a
// container without needing a real Docker daemon.
type fakeDocker struct {
	containers      map[string]string // name -> status
	labels          map[string]string // name -> "stab.super" value
	runCalls        int
	stopCalls       int
	removeCalls     int
	tmuxSessions    map[string]bool
	execArgs        [][]string
	imageExists     bool
	lastRunOpts     docker.RunOptions
	gpusAvailable   bool
	probeFails      bool
	probeFailures   int
	npmNeedsInstall bool
	npmInstallFails bool
}

func newFakeDocker() *fakeDocker {
	return &fakeDocker{containers: map[string]string{}, labels: map[string]string{}, tmuxSessions: map[string]bool{}, imageExists: true}
}

func (f *fakeDocker) setImageExists(b bool) { f.imageExists = b }

func (f *fakeDocker) Available() bool                                   { return true }
func (f *fakeDocker) Version(context.Context) (string, error)           { return "fake", nil }
func (f *fakeDocker) GPUsAvailable() bool                               { return f.gpusAvailable }
func (f *fakeDocker) ImageExists(context.Context, string) (bool, error) { return f.imageExists, nil }
func (f *fakeDocker) PullImage(context.Context, string) error           { return nil }
func (f *fakeDocker) ContainerExists(_ context.Context, name string) (bool, error) {
	_, ok := f.containers[name]
	return ok, nil
}
func (f *fakeDocker) ContainerStatus(_ context.Context, name string) (string, error) {
	if s, ok := f.containers[name]; ok {
		return s, nil
	}
	return "", nil
}
func (f *fakeDocker) ContainerLabels(_ context.Context, name string) (map[string]string, error) {
	if v, ok := f.labels[name]; ok {
		return map[string]string{"stab.super": v}, nil
	}
	return nil, nil
}
func (f *fakeDocker) RunContainer(_ context.Context, opts docker.RunOptions) error {
	f.containers[opts.Name] = "running"
	f.labels[opts.Name] = opts.Labels["stab.super"]
	f.runCalls++
	f.lastRunOpts = opts
	return nil
}
func (f *fakeDocker) StartContainer(_ context.Context, name string) error {
	f.containers[name] = "running"
	return nil
}
func (f *fakeDocker) StopContainer(_ context.Context, name string) error {
	if _, ok := f.containers[name]; ok {
		f.containers[name] = "exited"
	}
	f.stopCalls++
	return nil
}
func (f *fakeDocker) RemoveContainer(_ context.Context, name string) error {
	delete(f.containers, name)
	f.removeCalls++
	return nil
}
func (f *fakeDocker) Exec(_ context.Context, container string, args ...string) (string, error) {
	f.execArgs = append(f.execArgs, append([]string{container}, args...))
	return "", nil
}
func (f *fakeDocker) ExecAs(_ context.Context, container, user string, args ...string) (string, error) {
	f.execArgs = append(f.execArgs, append([]string{container, user}, args...))
	// Simulate the controller's $HOME/.pi/agent writability probe failing.
	if len(args) >= 3 && args[0] == "bash" && strings.Contains(args[2], ".stab-probe") {
		if f.probeFailures > 0 {
			f.probeFailures--
			return "mkdir: cannot create directory '/home/coder': Permission denied", os.ErrPermission
		}
		if f.probeFails {
			return "EACCES: permission denied", os.ErrPermission
		}
	}
	if len(args) >= 3 && args[0] == "bash" && strings.Contains(args[2], "npm install") {
		if f.npmInstallFails {
			return "npm failed", os.ErrPermission
		}
		return "added 152 packages", nil
	}
	if len(args) >= 3 && args[0] == "bash" && strings.Contains(args[2], "package.json") {
		if f.npmNeedsInstall {
			return "", nil
		}
		return "", exec.ErrNotFound
	}
	// Simulate tmux has-session / new-session.
	if len(args) >= 2 && args[0] == "tmux" && args[1] == "has-session" {
		if f.tmuxSessions[args[3]] {
			return "", nil
		}
		return "", exec.ErrNotFound
	}
	if sliceContains(args, "new-session") {
		// new-session args: [-f <conf>] -d -s <session> ...
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "-s" {
				f.tmuxSessions[args[i+1]] = true
			}
		}
	}
	return "", nil
}
func (f *fakeDocker) ExecCommandAs(_ context.Context, _ string, _ string, _ ...string) *exec.Cmd {
	return exec.Command("echo", "fake")
}

func testController(t *testing.T) (*Controller, *fakeDocker, *store.Store, string) {
	t.Helper()
	dc := newFakeDocker()
	st, err := store.Open(filepath.Join(t.TempDir(), "stab.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c := &Controller{
		Docker:    dc,
		Store:     st,
		Config:    config.Default(),
		StateRoot: t.TempDir(),
	}
	return c, dc, st, t.TempDir()
}

func TestStartOrAttachIsIdempotent(t *testing.T) {
	c, dc, _, root := testController(t)
	id, err := IDForPath(root)
	if err != nil {
		t.Fatal(err)
	}

	first, err := c.StartOrAttach(context.Background(), root, id)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.StartOrAttach(context.Background(), root, id)
	if err != nil {
		t.Fatal(err)
	}

	if dc.runCalls != 1 {
		t.Fatalf("expected exactly one docker run, got %d", dc.runCalls)
	}
	if first.ContainerName != second.ContainerName {
		t.Fatalf("container name changed between calls")
	}
	if !dc.tmuxSessions["stab"] {
		t.Fatal("tmux session was not created")
	}
}

func TestRunOptionsPersistSessionsInStab(t *testing.T) {
	c, dc, _, root := testController(t)
	id, _ := IDForPath(root)
	if _, err := c.StartOrAttach(context.Background(), root, id); err != nil {
		t.Fatal(err)
	}

	want := filepath.Join("/workspace", SanitizeName(filepath.Base(root)), ".stables", "sessions")
	if got := dc.lastRunOpts.Env["PI_CODING_AGENT_SESSION_DIR"]; got != want {
		t.Fatalf("session dir env = %q, want %q", got, want)
	}
	if got := dc.lastRunOpts.Env["STAB_HOST_PROJECT_DIR"]; got != root {
		t.Fatalf("host project dir env = %q, want %q", got, root)
	}
}

func TestRunOptionsNoDockerSocketByDefault(t *testing.T) {
	old := dockerSocketPath
	sock := filepath.Join(t.TempDir(), "docker.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	dockerSocketPath = sock
	t.Cleanup(func() { dockerSocketPath = old })

	c, dc, _, root := testController(t)
	id, _ := IDForPath(root)
	if _, err := c.StartOrAttach(context.Background(), root, id); err != nil {
		t.Fatal(err)
	}
	if _, ok := dc.lastRunOpts.ExtraVolumes[sock]; ok {
		t.Fatal("docker socket must not be mounted by default")
	}
	if _, ok := dc.lastRunOpts.Env["DOCKER_HOST"]; ok {
		t.Fatal("DOCKER_HOST must not be set by default")
	}
	if got := dc.lastRunOpts.Labels["stab.super"]; got != "0" {
		t.Fatalf("stab.super label = %q, want 0", got)
	}
}

func TestRunOptionsSuperMountsDockerSocket(t *testing.T) {
	old := dockerSocketPath
	sock := filepath.Join(t.TempDir(), "docker.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	dockerSocketPath = sock
	t.Cleanup(func() { dockerSocketPath = old })

	c, dc, _, root := testController(t)
	c.Super = true
	id, _ := IDForPath(root)
	if _, err := c.StartOrAttach(context.Background(), root, id); err != nil {
		t.Fatal(err)
	}
	if got := dc.lastRunOpts.ExtraVolumes[sock]; got != "/var/run/docker.sock" {
		t.Fatalf("docker socket volume = %q", got)
	}
	if got := dc.lastRunOpts.Env["DOCKER_HOST"]; got != "unix:///var/run/docker.sock" {
		t.Fatalf("DOCKER_HOST = %q", got)
	}
	if got := dc.lastRunOpts.Labels["stab.super"]; got != "1" {
		t.Fatalf("stab.super label = %q, want 1", got)
	}
}

func TestStartOrAttachCreatesHarnessWindow(t *testing.T) {
	c, dc, _, root := testController(t)
	id, _ := IDForPath(root)
	if _, err := c.StartOrAttach(context.Background(), root, id); err != nil {
		t.Fatal(err)
	}

	var newSession []string
	for _, args := range dc.execArgs {
		if sliceContains(args, "new-session") {
			newSession = args
		}
	}
	if newSession == nil {
		t.Fatal("no tmux new-session executed")
	}
	joined := strings.Join(newSession, " ")
	if !strings.Contains(joined, "-n main pi") {
		t.Errorf("expected harness window running pi: %q", joined)
	}
	if !strings.Contains(joined, "-n shell") {
		t.Errorf("expected shell window: %q", joined)
	}
}

func TestKillStopsAndRemoves(t *testing.T) {
	c, dc, _, root := testController(t)
	id, _ := IDForPath(root)
	if _, err := c.StartOrAttach(context.Background(), root, id); err != nil {
		t.Fatal(err)
	}
	if err := c.Kill(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if dc.stopCalls != 1 || dc.removeCalls != 1 {
		t.Fatalf("expected stop+remove, got stop=%d remove=%d", dc.stopCalls, dc.removeCalls)
	}
}

func TestStartOrAttachBuildsWhenImageMissing(t *testing.T) {
	c, dc, _, root := testController(t)
	id, _ := IDForPath(root)
	dc.setImageExists(false)

	built := ""
	c.BuildImage = func(_ context.Context, image, contextDir, dockerfile string) error {
		built = image + "|" + contextDir + "|" + dockerfile
		return nil
	}
	if _, err := c.StartOrAttach(context.Background(), root, id); err != nil {
		t.Fatal(err)
	}
	if built == "" {
		t.Fatal("expected BuildImage to be called when image is missing")
	}
}

func TestStartOrAttachBuildsGPUImageOnNvidiaHost(t *testing.T) {
	c, dc, _, root := testController(t)
	id, _ := IDForPath(root)
	dc.setImageExists(false)
	dc.gpusAvailable = true

	built := ""
	c.BuildImage = func(_ context.Context, image, contextDir, dockerfile string) error {
		built = image + "|" + dockerfile
		return nil
	}
	if _, err := c.StartOrAttach(context.Background(), root, id); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(built, ImageTagGPU(id)+"|"+"Dockerfile.gpu") {
		t.Fatalf("expected GPU image build, got %q", built)
	}
	if dc.lastRunOpts.GPUs != "all" {
		t.Fatalf("expected --gpus all on NVIDIA host, got %q", dc.lastRunOpts.GPUs)
	}
}

func TestStartOrAttachBuildsCPUImageWithoutGPU(t *testing.T) {
	c, dc, _, root := testController(t)
	id, _ := IDForPath(root)
	dc.setImageExists(false)
	dc.gpusAvailable = false

	built := ""
	c.BuildImage = func(_ context.Context, image, contextDir, dockerfile string) error {
		built = image + "|" + dockerfile
		return nil
	}
	if _, err := c.StartOrAttach(context.Background(), root, id); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(built, ImageTag(id)+"|"+"Dockerfile") {
		t.Fatalf("expected CPU image build, got %q", built)
	}
	if dc.lastRunOpts.GPUs != "" {
		t.Fatalf("expected no GPU on CPU host, got %q", dc.lastRunOpts.GPUs)
	}
}

func sliceContains(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}

func TestPiAgentVolumes(t *testing.T) {
	c := &Controller{PiAgentDir: func() string { return "/home/u/.pi/agent" }}
	vols := c.piAgentVolumes()
	// The whole agent dir is mounted (not just skills/extensions) so Pi's
	// auth.json and models.json persist across container recreation.
	if vols["/home/u/.pi/agent"] != "/home/coder/.pi/agent" {
		t.Fatalf("whole agent dir must be mounted: %v", vols)
	}
	if len(vols) != 1 {
		t.Fatalf("expected exactly one mount, got %v", vols)
	}
}

func TestPiAgentVolumesNil(t *testing.T) {
	c := &Controller{}
	if vols := c.piAgentVolumes(); vols != nil {
		t.Fatalf("expected nil volumes when PiAgentDir unset, got %v", vols)
	}
}

// --- runtime template pinning and home-probe tests -------------------------

func testControllerWithTemplate(t *testing.T) (*Controller, *fakeDocker, *store.Store, string) {
	c, dc, st, root := testController(t)
	c.TemplateVersion = "26"
	return c, dc, st, root
}

func TestImageMetaRebuildsStaleTemplate(t *testing.T) {
	c, dc, _, root := testControllerWithTemplate(t)
	id, err := IDForPath(root)
	if err != nil {
		t.Fatal(err)
	}
	dc.setImageExists(true)
	image, _, _ := c.ResolveImage(id)
	if err := RecordImageTemplate(c.StateRoot, image, "25"); err != nil {
		t.Fatal(err)
	}
	built := false
	c.BuildImage = func(_ context.Context, img, dir, dockerfile string) error {
		built = true
		if img != image {
			t.Errorf("built image %q, want %q", img, image)
		}
		return nil
	}
	if _, err := c.StartOrAttach(context.Background(), root, id); err != nil {
		t.Fatal(err)
	}
	if !built {
		t.Fatal("expected stale-template image to be rebuilt")
	}
}

func TestImageMetaKeepsFreshTemplate(t *testing.T) {
	c, dc, _, root := testControllerWithTemplate(t)
	id, err := IDForPath(root)
	if err != nil {
		t.Fatal(err)
	}
	dc.setImageExists(true)
	image, _, _ := c.ResolveImage(id)
	if err := RecordImageTemplate(c.StateRoot, image, "26"); err != nil {
		t.Fatal(err)
	}
	c.BuildImage = func(context.Context, string, string, string) error {
		t.Fatal("fresh template image must not be rebuilt")
		return nil
	}
	if _, err := c.StartOrAttach(context.Background(), root, id); err != nil {
		t.Fatal(err)
	}
}

func TestImageMetaUnknownTemplateDoesNotRebuild(t *testing.T) {
	// No meta record (image predates tracking or is user-managed): keep it,
	// the caller sees a "stab update" hint on stderr instead of a rebuild.
	c, dc, _, root := testControllerWithTemplate(t)
	id, err := IDForPath(root)
	if err != nil {
		t.Fatal(err)
	}
	dc.setImageExists(true)
	c.BuildImage = func(context.Context, string, string, string) error {
		t.Fatal("untracked image must not be rebuilt automatically")
		return nil
	}
	if _, err := c.StartOrAttach(context.Background(), root, id); err != nil {
		t.Fatal(err)
	}
}

func TestStartOrAttachFailsWhenAgentDirNotWritable(t *testing.T) {
	oldAttempts, oldDelay := agentProbeAttempts, agentProbeDelay
	agentProbeAttempts, agentProbeDelay = 2, 0
	t.Cleanup(func() { agentProbeAttempts, agentProbeDelay = oldAttempts, oldDelay })

	c, dc, _, root := testControllerWithTemplate(t)
	id, err := IDForPath(root)
	if err != nil {
		t.Fatal(err)
	}
	dc.probeFails = true
	_, err = c.StartOrAttach(context.Background(), root, id)
	if err == nil {
		t.Fatal("expected agent-dir probe failure")
	}
	if !strings.Contains(err.Error(), "cannot write $HOME/.pi/agent") {
		t.Fatalf("error should name the agent dir, got: %v", err)
	}
}

func TestStartOrAttachRetriesTransientAgentDirProbe(t *testing.T) {
	oldAttempts, oldDelay := agentProbeAttempts, agentProbeDelay
	agentProbeAttempts, agentProbeDelay = 3, 0
	t.Cleanup(func() { agentProbeAttempts, agentProbeDelay = oldAttempts, oldDelay })

	c, dc, _, root := testControllerWithTemplate(t)
	id, err := IDForPath(root)
	if err != nil {
		t.Fatal(err)
	}
	dc.probeFailures = 2
	if _, err := c.StartOrAttach(context.Background(), root, id); err != nil {
		t.Fatalf("transient agent-dir probe failure should be retried, got: %v", err)
	}
	probeCalls := 0
	for _, args := range dc.execArgs {
		if len(args) >= 5 && args[2] == "bash" && strings.Contains(args[4], ".stab-probe") {
			probeCalls++
		}
	}
	if probeCalls != 3 {
		t.Fatalf("probe calls = %d, want 3", probeCalls)
	}
}

func TestStartOrAttachCreatesHostStateDirBeforeDockerRun(t *testing.T) {
	c, dc, _, root := testControllerWithTemplate(t)
	id, err := IDForPath(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.StartOrAttach(context.Background(), root, id); err != nil {
		t.Fatal(err)
	}
	if dc.lastRunOpts.StateDir == "" {
		t.Fatal("docker run state dir was empty")
	}
	fi, err := os.Stat(dc.lastRunOpts.StateDir)
	if err != nil {
		t.Fatalf("state dir should exist before docker run: %v", err)
	}
	if !fi.IsDir() {
		t.Fatalf("state dir should be a directory: %s", dc.lastRunOpts.StateDir)
	}
}

func TestStartOrAttachPrewarmsPiNPMBeforeTmux(t *testing.T) {
	c, dc, _, root := testControllerWithTemplate(t)
	log := &strings.Builder{}
	c.ErrOut = log
	id, err := IDForPath(root)
	if err != nil {
		t.Fatal(err)
	}
	dc.npmNeedsInstall = true
	if _, err := c.StartOrAttach(context.Background(), root, id); err != nil {
		t.Fatal(err)
	}
	npmIndex := -1
	tmuxIndex := -1
	var npmScript string
	for i, args := range dc.execArgs {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "npm ") && npmIndex == -1 {
			npmIndex = i
			if len(args) >= 5 {
				npmScript = args[4]
			}
		}
		if strings.Contains(joined, "new-session") && tmuxIndex == -1 {
			tmuxIndex = i
		}
	}
	if npmIndex == -1 {
		t.Fatalf("expected npm prewarm before tmux, exec calls: %#v", dc.execArgs)
	}
	if strings.Contains(npmScript, "npm ci") {
		t.Fatalf("prewarm must use npm install, not npm ci, because embedded package-lock may be stale:\n%s", npmScript)
	}
	if !strings.Contains(npmScript, "npm install") {
		t.Fatalf("prewarm should run npm install:\n%s", npmScript)
	}
	if tmuxIndex == -1 {
		t.Fatalf("expected tmux session creation, exec calls: %#v", dc.execArgs)
	}
	if npmIndex > tmuxIndex {
		t.Fatalf("npm prewarm should run before tmux: npm=%d tmux=%d calls=%#v", npmIndex, tmuxIndex, dc.execArgs)
	}
	for _, want := range []string{
		"stab: setting up Pi packages inside stab-",
		"downloading dependencies (this can take a couple minutes)",
		"stab: Pi npm packages ready inside stab-",
	} {
		if !strings.Contains(log.String(), want) {
			t.Fatalf("prewarm log missing %q:\n%s", want, log.String())
		}
	}
}
