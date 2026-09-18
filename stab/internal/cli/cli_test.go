package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stables/stab/internal/config"
	"github.com/stables/stab/internal/docker"
	"github.com/stables/stab/internal/project"
	"github.com/stables/stab/internal/store"
	stabruntime "github.com/stables/stab/runtime"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	return &App{
		Home:   t.TempDir(),
		Config: config.Default(),
		In:     strings.NewReader(""),
		Out:    io.Discard,
		ErrOut: io.Discard,
	}
}

func TestEnsureProjectConfigSeedsAndWritesMarker(t *testing.T) {
	a := newTestApp(t)
	root := t.TempDir()
	dir, err := a.ensureProjectConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(root, ".stables") {
		t.Fatalf("config dir should be root/.stables, got %q", dir)
	}
	for _, name := range []string{"Dockerfile", "entrypoint.sh", "tmux.conf"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("missing seeded file %s: %v", name, err)
		}
	}
	marker, err := os.ReadFile(filepath.Join(dir, ".stables-template-version"))
	if err != nil {
		t.Fatalf("missing marker: %v", err)
	}
	if strings.TrimSpace(string(marker)) != stabruntime.TemplateVersionString() {
		t.Fatalf("marker mismatch: %q", marker)
	}
}

func TestEnsureProjectConfigReseedsWhenMarkerMissing(t *testing.T) {
	a := newTestApp(t)
	root := t.TempDir()
	dir, err := a.ensureProjectConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate stale seeded defaults: outdated Dockerfile and no marker.
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM debian:bookworm-slim"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(dir, ".stables-template-version"))

	if _, err := a.ensureProjectConfig(root); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "FROM codercom/code-server") {
		t.Fatalf("expected re-seeded code-server Dockerfile, got:\n%s", data)
	}
}

func TestEnsureProjectConfigPreservesUserEdits(t *testing.T) {
	a := newTestApp(t)
	root := t.TempDir()
	dir, err := a.ensureProjectConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	custom := "FROM my-custom-base\n"
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	// Same template version -> user edit must survive.
	if _, err := a.ensureProjectConfig(root); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if string(data) != custom {
		t.Fatalf("user edit was overwritten: %q", data)
	}
}

func TestEnsureProjectConfigSourcesModelsJSON(t *testing.T) {
	a := newTestApp(t)
	canonical := filepath.Join(a.Home, ".stables", "models.json")
	if err := os.MkdirAll(filepath.Dir(canonical), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canonical, []byte(`{"providers":{"x":{"baseUrl":"http://x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dir, err := a.ensureProjectConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "models.json"))
	if err != nil {
		t.Fatalf("models.json should be sourced from canonical: %v", err)
	}
	if !strings.Contains(string(data), `"x"`) {
		t.Fatalf("unexpected models.json content: %s", data)
	}
}

func TestEnsureProjectConfigDoesNotEmbedModels(t *testing.T) {
	a := newTestApp(t)
	root := t.TempDir()
	dir, err := a.ensureProjectConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "models.json")); !os.IsNotExist(err) {
		t.Fatal("models.json must be sourced from ~/.stables, not embedded")
	}
}

func TestEnvInjectForProjectOnlyReferencedSecrets(t *testing.T) {
	a := newTestApp(t)
	secDir := filepath.Join(a.Home, ".stables")
	if err := os.MkdirAll(secDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secDir, "secrets.env"),
		[]byte("EXAMPLE_API_KEY=sk-cv\nUNUSED_SECRET=sk-unused\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "models.json"),
		[]byte(`{"providers":{"cv":{"apiKey":"$EXAMPLE_API_KEY"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	env, err := a.envInjectForProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if env["EXAMPLE_API_KEY"] != "sk-cv" {
		t.Fatalf("referenced secret not injected: %v", env)
	}
	if _, ok := env["UNUSED_SECRET"]; ok {
		t.Fatalf("unreferenced secret leaked into container: %v", env)
	}
}

func TestExtractConfigDoesNotOverwrite(t *testing.T) {
	a := newTestApp(t)
	state := a.Config.StateDirPath(a.Home)
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	existing := "{\"providers\":{\"mine\":{}}}\n"
	if err := os.WriteFile(filepath.Join(state, "models.json"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.extractConfig(false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(state, "models.json"))
	if string(data) != existing {
		t.Fatalf("models.json was overwritten: %q", data)
	}
}

type listFakeDocker struct {
	statuses map[string]string
}

func (f listFakeDocker) Available() bool                                   { return true }
func (f listFakeDocker) Version(context.Context) (string, error)           { return "fake", nil }
func (f listFakeDocker) GPUsAvailable() bool                               { return false }
func (f listFakeDocker) ImageExists(context.Context, string) (bool, error) { return true, nil }
func (f listFakeDocker) PullImage(context.Context, string) error           { return nil }
func (f listFakeDocker) ContainerExists(_ context.Context, name string) (bool, error) {
	_, ok := f.statuses[name]
	return ok, nil
}
func (f listFakeDocker) ContainerStatus(_ context.Context, name string) (string, error) {
	return f.statuses[name], nil
}
func (f listFakeDocker) ContainerLabels(context.Context, string) (map[string]string, error) {
	return nil, nil
}
func (f listFakeDocker) RunContainer(context.Context, docker.RunOptions) error   { return nil }
func (f listFakeDocker) StartContainer(context.Context, string) error            { return nil }
func (f listFakeDocker) StopContainer(context.Context, string) error             { return nil }
func (f listFakeDocker) RemoveContainer(context.Context, string) error           { return nil }
func (f listFakeDocker) Exec(context.Context, string, ...string) (string, error) { return "", nil }
func (f listFakeDocker) ExecAs(context.Context, string, string, ...string) (string, error) {
	return "", nil
}
func (f listFakeDocker) ExecCommandAs(context.Context, string, string, ...string) *exec.Cmd {
	return exec.Command("echo")
}

func newListTestApp(t *testing.T) (*App, *bytes.Buffer) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "stab.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	out := &bytes.Buffer{}
	fd := listFakeDocker{statuses: map[string]string{}}
	return &App{
		Home:   t.TempDir(),
		Config: config.Default(),
		Store:  st,
		Projects: &project.Controller{
			Docker: fd,
			Store:  st,
			Config: config.Default(),
		},
		In:     strings.NewReader(""),
		Out:    out,
		ErrOut: io.Discard,
	}, out
}

func TestListEmptyState(t *testing.T) {
	a, out := newListTestApp(t)
	if code := a.Run(context.Background(), []string{"list"}); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if strings.TrimSpace(out.String()) != "stab: no projects found" {
		t.Fatalf("unexpected output: %q", out.String())
	}
}

func TestListShowsProjectsAndLiveContainerStatus(t *testing.T) {
	a, out := newListTestApp(t)
	root := filepath.Join(t.TempDir(), "lindsim")
	id := "22eaaf6fc0dc"
	if _, err := a.Store.UpsertProject(store.Project{ID: id, Name: "lindsim", RootPath: root, Image: "stab-project-22eaaf6fc0dc:gpu"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.UpsertDeployment(store.Deployment{ID: id + ":local", ProjectID: id, Kind: store.KindLocal, ContainerName: "stab-22eaaf6fc0dc", Image: "stab-project-22eaaf6fc0dc:gpu", ObservedState: "exited"}); err != nil {
		t.Fatal(err)
	}
	a.Projects.Docker = listFakeDocker{statuses: map[string]string{"stab-22eaaf6fc0dc": "running"}}
	if code := a.Run(context.Background(), []string{"list"}); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	got := out.String()
	for _, want := range []string{"PROJECT", "STATUS", "CONTAINER", "IMAGE", "PATH", "lindsim", "running", "stab-22eaaf6fc0dc", "stab-project-22eaaf6fc0dc:gpu", root} {
		if !strings.Contains(got, want) {
			t.Fatalf("list output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "exited") {
		t.Fatalf("list should prefer live Docker status over stale DB status:\n%s", got)
	}
}
