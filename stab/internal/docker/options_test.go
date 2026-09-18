package docker

import (
	"strings"
	"testing"
)

func TestBuildRunArgsSecurityDefaults(t *testing.T) {
	opts := RunOptions{
		Name:             "stab-abc",
		Image:            "stab/runtime:latest",
		ProjectMount:     "/home/coder/project",
		ContainerProject: "/workspace",
		Network:          NetworkDeny,
	}
	args, err := BuildRunArgs(opts)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--network none") {
		t.Errorf("deny network should use --network none: %q", joined)
	}
	if strings.Contains(joined, "--cap-drop") {
		t.Errorf("no cap-drop should be applied by default (empty CapDrop): %q", joined)
	}
	if strings.Contains(joined, "no-new-privileges") {
		t.Errorf("no-new-privileges must not be set (agents need sudo): %q", joined)
	}
	if strings.Contains(joined, "--privileged") {
		t.Errorf("privileged must not be set by default: %q", joined)
	}
	if strings.Contains(joined, "--network host") {
		t.Errorf("host networking must not be default: %q", joined)
	}
	if !strings.Contains(joined, "/home/coder/project:/workspace") {
		t.Errorf("project mount missing: %q", joined)
	}
}

func TestBuildRunArgsCapsContainerLog(t *testing.T) {
	opts := RunOptions{
		Name: "stab-abc", Image: "stab/runtime:latest",
		ProjectMount: "/home/coder/project", ContainerProject: "/workspace",
		Network: NetworkDeny,
	}
	args, err := BuildRunArgs(opts)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	// Docker's json-file driver has no default rotation, so a long-lived project
	// would grow the host's log without limit.
	for _, want := range []string{
		"--log-driver json-file",
		"--log-opt max-size=10m",
		"--log-opt max-file=3",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q in run args: %q", want, joined)
		}
	}
}

func TestBuildRunArgsExplicitCapDrop(t *testing.T) {
	opts := RunOptions{
		Name: "c", Image: "i", ProjectMount: "/p", ContainerProject: "/workspace",
		Network: NetworkBridge, CapDrop: []string{"ALL"},
	}
	args, err := BuildRunArgs(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(args, " "), "--cap-drop ALL") {
		t.Errorf("explicit CapDrop should render --cap-drop ALL")
	}
}

func TestBuildRunArgsReadOnlyProject(t *testing.T) {
	opts := RunOptions{
		Name: "c", Image: "i", ProjectMount: "/p", ContainerProject: "/workspace",
		ReadOnlyProject: true, Network: NetworkDeny,
	}
	args, err := BuildRunArgs(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(args, " "), "/p:/workspace:ro") {
		t.Errorf("read-only project mount expected")
	}
}

func TestBuildRunArgsRequiresNameAndImage(t *testing.T) {
	if _, err := BuildRunArgs(RunOptions{Name: "", Image: "i"}); err == nil {
		t.Fatal("expected error for missing name")
	}
	if _, err := BuildRunArgs(RunOptions{Name: "n"}); err == nil {
		t.Fatal("expected error for missing image")
	}
}

func TestBuildRunArgsDeterministic(t *testing.T) {
	opts := RunOptions{
		Name: "c", Image: "i", ProjectMount: "/p", ContainerProject: "/workspace",
		Network: NetworkAllowlist,
		Env:     map[string]string{"B": "2", "A": "1"},
		Labels:  map[string]string{"z": "z", "a": "a"},
	}
	a, _ := BuildRunArgs(opts)
	b, _ := BuildRunArgs(opts)
	if strings.Join(a, " ") != strings.Join(b, " ") {
		t.Fatalf("args not deterministic")
	}
}

func TestBuildRunArgsBridgePortsAndHosts(t *testing.T) {
	opts := RunOptions{
		Name: "c", Image: "i", ProjectMount: "/p", ContainerProject: "/workspace",
		Network:      NetworkBridge,
		PortBindings: []string{"127.0.0.1:8081:8080"},
		ExtraHosts:   []string{"host.docker.internal:host-gateway"},
	}
	args, err := BuildRunArgs(opts)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--network bridge") {
		t.Errorf("expected bridge network: %q", joined)
	}
	if !strings.Contains(joined, "-p 127.0.0.1:8081:8080") {
		t.Errorf("expected port binding: %q", joined)
	}
	if !strings.Contains(joined, "--add-host host.docker.internal:host-gateway") {
		t.Errorf("expected extra host: %q", joined)
	}
}

func TestBuildImageArgsCachedByDefault(t *testing.T) {
	args := BuildImageArgs("tag", "/ctx", "Dockerfile", false)
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--pull") || strings.Contains(joined, "--no-cache") {
		t.Fatalf("cached build should not force pull/no-cache: %q", joined)
	}
	if !strings.Contains(joined, "-t tag") || !strings.Contains(joined, "-f /ctx/Dockerfile") || args[len(args)-1] != "/ctx" {
		t.Fatalf("unexpected build args: %#v", args)
	}
}

func TestBuildImageArgsFreshPullsAndDisablesCache(t *testing.T) {
	args := BuildImageArgs("tag", "/ctx", "Dockerfile.gpu", true)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--pull") || !strings.Contains(joined, "--no-cache") {
		t.Fatalf("fresh build should force pull/no-cache: %q", joined)
	}
	if strings.Index(joined, "--pull") > strings.Index(joined, "-t tag") {
		t.Fatalf("fresh flags should appear before tag: %q", joined)
	}
}

func TestBuildRunArgsGPUs(t *testing.T) {
	opts := RunOptions{
		Name: "c", Image: "i", ProjectMount: "/p", ContainerProject: "/workspace",
		Network: NetworkBridge, GPUs: "all",
	}
	args, err := BuildRunArgs(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(args, " "), "--gpus all") {
		t.Errorf("expected --gpus all: %v", args)
	}

	off := RunOptions{Name: "c", Image: "i", ProjectMount: "/p", ContainerProject: "/workspace", Network: NetworkBridge}
	args2, _ := BuildRunArgs(off)
	if strings.Contains(strings.Join(args2, " "), "--gpus") {
		t.Errorf("no --gpus when GPU unset: %v", args2)
	}
}
