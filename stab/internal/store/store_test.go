package store

import (
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "stab.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrationsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stab.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
}

func TestProjectAndDeployment(t *testing.T) {
	s := openTestStore(t)
	p, err := s.UpsertProject(Project{ID: "p1", Name: "x", RootPath: "/tmp/x", Image: "stab/runtime:latest"})
	if err != nil {
		t.Fatal(err)
	}
	if p.CreatedAt == "" {
		t.Fatal("created_at not set")
	}
	got, err := s.GetProject("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "p1" || got.Image != "stab/runtime:latest" {
		t.Fatalf("wrong project %+v", got)
	}

	d, err := s.UpsertDeployment(Deployment{ID: "p1:local", ProjectID: "p1", Kind: KindLocal, ContainerName: "stab-abc"})
	if err != nil {
		t.Fatal(err)
	}
	if d.ObservedState == "" {
		t.Fatal("observed_state not defaulted")
	}
	deps, err := s.ListDeployments("p1")
	if err != nil || len(deps) != 1 {
		t.Fatalf("expected 1 deployment, got %d (err=%v)", len(deps), err)
	}

	// Container and tmux session upserts.
	if _, err := s.UpsertContainer(Container{ID: "c1", DeploymentID: "p1:local", Name: "stab-abc", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertTmuxSession(TmuxSession{ID: "t1", DeploymentID: "p1:local", Name: "stab"}); err != nil {
		t.Fatal(err)
	}
}
