package runtime

import (
	"strings"
	"testing"
)

func TestDockerfileEmbedded(t *testing.T) {
	d := Dockerfile()
	if !strings.Contains(d, "FROM codercom/code-server") {
		t.Fatalf("Dockerfile not embedded correctly")
	}
	if !strings.Contains(d, "pi.dev/install.sh") {
		t.Fatalf("harness install missing from Dockerfile")
	}
	if !strings.Contains(d, "tmux") {
		t.Fatalf("tmux missing from Dockerfile")
	}
	if !strings.Contains(d, "fd-find") || !strings.Contains(d, "/usr/local/bin/fd") {
		t.Fatalf("fd missing from Dockerfile")
	}
	if !strings.Contains(d, "docker-cli") || !strings.Contains(d, "docker-compose") {
		t.Fatalf("Docker CLI/Compose missing from Dockerfile")
	}
}

func TestDockerfileGPUEmbedded(t *testing.T) {
	d := DockerfileGPU()
	if !strings.Contains(d, "FROM codercom/code-server") {
		t.Fatalf("GPU Dockerfile not embedded correctly")
	}
	if !strings.Contains(d, "developer.download.nvidia.com") {
		t.Fatalf("GPU Dockerfile should install the CUDA toolkit")
	}
	if !strings.Contains(d, "pi.dev/install.sh") {
		t.Fatalf("harness install missing from GPU Dockerfile")
	}
	if !strings.Contains(d, "fd-find") || !strings.Contains(d, "/usr/local/bin/fd") {
		t.Fatalf("fd missing from GPU Dockerfile")
	}
	if !strings.Contains(d, "docker-cli") || !strings.Contains(d, "docker-compose") {
		t.Fatalf("Docker CLI/Compose missing from GPU Dockerfile")
	}
}

func TestEntrypointEmbedded(t *testing.T) {
	e := Entrypoint()
	if !strings.Contains(e, "code-server") {
		t.Fatalf("entrypoint not embedded correctly")
	}
	for _, want := range []string{"/var/run/docker.sock", "dockerhost", "usermod -aG"} {
		if !strings.Contains(e, want) {
			t.Fatalf("entrypoint missing Docker socket setup %q", want)
		}
	}
}

func TestTmuxConfEmbedded(t *testing.T) {
	c := TmuxConf()
	for _, want := range []string{"bind -n F2", "bind -n F3", "bind -n F4"} {
		if !strings.Contains(c, want) {
			t.Errorf("tmux.conf missing %q", want)
		}
	}
	// Truecolor must be enabled so the Pi harness renders 24-bit RGB instead
	// of falling back to a 256-color approximation.
	for _, want := range []string{"terminal-features", ",xterm*:RGB", "COLORTERM"} {
		if !strings.Contains(c, want) {
			t.Errorf("tmux.conf missing truecolor config %q", want)
		}
	}
}
