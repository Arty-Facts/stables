// Package docker wraps the Docker CLI. All container lifecycle operations go
// through the `docker` binary; stab never touches the Docker socket inside a
// container and never exposes it to containers.
package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

// Client shells out to the docker CLI.
type Client struct {
	// Bin is the docker binary path; defaults to "docker".
	Bin string
}

func (c *Client) bin() string {
	if c.Bin == "" {
		return "docker"
	}
	return c.Bin
}

// Available reports whether the docker binary is on PATH.
func (c *Client) Available() bool {
	_, err := exec.LookPath(c.bin())
	return err == nil
}

// Version returns the docker client version string.
func (c *Client) Version(ctx context.Context) (string, error) {
	out, err := c.output(ctx, "version", "--format", "{{.Client.Version}}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// GPUsAvailable reports whether an NVIDIA GPU is usable via nvidia-smi. It is
// used to gate --gpus so a GPU request never fails a container launch when no
// GPU (or no NVIDIA container runtime) is present.
func (c *Client) GPUsAvailable() bool {
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		return false
	}
	cmd := exec.Command("nvidia-smi")
	return cmd.Run() == nil
}

func (c *Client) output(ctx context.Context, args ...string) (string, error) {
	full := append([]string{}, args...)
	cmd := exec.CommandContext(ctx, c.bin(), full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (c *Client) run(ctx context.Context, args ...string) error {
	_, err := c.output(ctx, args...)
	return err
}

// ImageExists reports whether an image is present locally.
func (c *Client) ImageExists(ctx context.Context, image string) (bool, error) {
	out, err := c.output(ctx, "image", "inspect", image)
	if err != nil {
		if strings.Contains(out, "No such image") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// PullImage pulls an image.
func (c *Client) PullImage(ctx context.Context, image string) error {
	return c.run(ctx, "image", "pull", image)
}

// BuildImageArgs renders docker build args. fresh adds --pull and --no-cache
// for major refreshes such as `stab update`.
func BuildImageArgs(tag, contextDir, dockerfile string, fresh bool) []string {
	args := []string{"build"}
	if fresh {
		args = append(args, "--pull", "--no-cache")
	}
	args = append(args, "-t", tag)
	if dockerfile != "" {
		args = append(args, "-f", filepath.Join(contextDir, dockerfile))
	}
	return append(args, contextDir)
}

// BuildImage builds an image from a local context directory and streams the
// build output to stdout/stderr so the user sees progress. dockerfile names the
// Dockerfile inside the context dir ("" = default Dockerfile).
func (c *Client) BuildImage(ctx context.Context, tag, contextDir, dockerfile string, stdout, stderr io.Writer) error {
	return c.buildImage(ctx, tag, contextDir, dockerfile, false, stdout, stderr)
}

// BuildImageFresh builds with --pull and --no-cache so base images, install
// scripts, npm, and package-manager layers are refreshed.
func (c *Client) BuildImageFresh(ctx context.Context, tag, contextDir, dockerfile string, stdout, stderr io.Writer) error {
	return c.buildImage(ctx, tag, contextDir, dockerfile, true, stdout, stderr)
}

func (c *Client) buildImage(ctx context.Context, tag, contextDir, dockerfile string, fresh bool, stdout, stderr io.Writer) error {
	args := BuildImageArgs(tag, contextDir, dockerfile, fresh)
	cmd := exec.CommandContext(ctx, c.bin(), args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// ContainerExists reports whether a container exists (any state).
func (c *Client) ContainerExists(ctx context.Context, name string) (bool, error) {
	out, err := c.output(ctx, "ps", "-a", "--filter", "name=^"+name+"$", "--format", "{{.Names}}")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == name {
			return true, nil
		}
	}
	return false, nil
}

// ContainerStatus returns the container status ("running", "exited", ...) or
// empty if it does not exist.
func (c *Client) ContainerStatus(ctx context.Context, name string) (string, error) {
	out, err := c.output(ctx, "ps", "-a", "--filter", "name=^"+name+"$", "--format", "{{.State}}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// ContainerLabels returns the container's labels, or nil if it does not exist.
func (c *Client) ContainerLabels(ctx context.Context, name string) (map[string]string, error) {
	out, err := c.output(ctx, "inspect", "--format", "{{json .Config.Labels}}", name)
	if err != nil {
		if strings.Contains(err.Error(), "No such") {
			return nil, nil
		}
		return nil, err
	}
	var labels map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &labels); err != nil {
		return nil, fmt.Errorf("parse labels for %s: %w", name, err)
	}
	return labels, nil
}

// RunContainer starts a detached container.
func (c *Client) RunContainer(ctx context.Context, opts RunOptions) error {
	args, err := BuildRunArgs(opts)
	if err != nil {
		return err
	}
	return c.run(ctx, args...)
}

// StartContainer starts an existing stopped container.
func (c *Client) StartContainer(ctx context.Context, name string) error {
	return c.run(ctx, "start", name)
}

// StopContainer stops a running container.
func (c *Client) StopContainer(ctx context.Context, name string) error {
	return c.run(ctx, "stop", name)
}

// RemoveContainer removes a stopped container.
func (c *Client) RemoveContainer(ctx context.Context, name string) error {
	return c.run(ctx, "rm", "-f", name)
}

// Exec runs a non-interactive command inside a container and returns its
// combined output.
func (c *Client) Exec(ctx context.Context, container string, args ...string) (string, error) {
	full := append([]string{"exec"}, container)
	full = append(full, args...)
	return c.output(ctx, full...)
}

// ExecAs runs a non-interactive command inside a container as a specific user.
func (c *Client) ExecAs(ctx context.Context, container, user string, args ...string) (string, error) {
	full := []string{"exec"}
	if user != "" {
		full = append(full, "--user", user)
	}
	full = append(full, container)
	full = append(full, args...)
	return c.output(ctx, full...)
}

// ExecCommand builds an *exec.Cmd for an interactive command (stdin/stdout are
// wired to the caller's terminal). Callers must set cmd.Stdin/Stdout/Stderr.
func (c *Client) ExecCommand(ctx context.Context, container string, args ...string) *exec.Cmd {
	return c.ExecCommandAs(ctx, container, "", args...)
}

// ExecCommandAs builds an interactive *exec.Cmd running as a specific user.
func (c *Client) ExecCommandAs(ctx context.Context, container, user string, args ...string) *exec.Cmd {
	full := []string{"exec"}
	if user != "" {
		full = append(full, "--user", user)
	}
	full = append(full, "-it", container)
	full = append(full, args...)
	return exec.CommandContext(ctx, c.bin(), full...)
}

// ExecDetachedCommand builds an *exec.Cmd for a detached docker exec.
func (c *Client) ExecDetachedCommand(ctx context.Context, container string, args ...string) *exec.Cmd {
	full := append([]string{"exec", "-d"}, container)
	full = append(full, args...)
	return exec.CommandContext(ctx, c.bin(), full...)
}
