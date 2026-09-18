package project

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/stables/stab/internal/config"
	"github.com/stables/stab/internal/docker"
	"github.com/stables/stab/internal/mounts"
	"github.com/stables/stab/internal/store"
	"github.com/stables/stab/internal/tmux"
)

// Docker is the subset of the docker client the controller needs. *docker.Client
// satisfies it, and tests can substitute a fake.
type Docker interface {
	Available() bool
	Version(ctx context.Context) (string, error)
	GPUsAvailable() bool
	ImageExists(ctx context.Context, image string) (bool, error)
	PullImage(ctx context.Context, image string) error
	ContainerExists(ctx context.Context, name string) (bool, error)
	ContainerStatus(ctx context.Context, name string) (string, error)
	ContainerLabels(ctx context.Context, name string) (map[string]string, error)
	RunContainer(ctx context.Context, opts docker.RunOptions) error
	StartContainer(ctx context.Context, name string) error
	StopContainer(ctx context.Context, name string) error
	RemoveContainer(ctx context.Context, name string) error
	Exec(ctx context.Context, container string, args ...string) (string, error)
	ExecAs(ctx context.Context, container, user string, args ...string) (string, error)
	ExecCommandAs(ctx context.Context, container, user string, args ...string) *exec.Cmd
}

// ContainerUser is the user that tmux and the harness run as inside the
// container, matching the code-server runtime (the entrypoint aligns this user
// to the host uid/gid).
const ContainerUser = "coder"

var (
	agentProbeAttempts = 40
	agentProbeDelay    = 250 * time.Millisecond
	dockerSocketPath   = "/var/run/docker.sock"
)

// Controller drives local deployments.
type Controller struct {
	Docker    Docker
	Store     *store.Store
	Config    config.Config
	Super     bool
	StateRoot string
	ErrOut    io.Writer

	// ConfigDir returns the seeded in-project config directory (root/.stables)
	// containing the project's Dockerfile and tmux.conf. Wired by the CLI.
	ConfigDir func(root string) (string, error)
	// BuildImage builds an image from a local context directory using the named
	// Dockerfile inside it. Wired by the CLI to the embedded runtime builder.
	BuildImage func(ctx context.Context, image, contextDir, dockerfile string) error
	// TemplateVersion is the embedded runtime template the current binary ships.
	// An image known to predate it is rebuilt automatically on start.
	TemplateVersion string
	// NextPort finds a free host port starting from the given base. Wired by
	// the CLI; nil disables port mapping.
	NextPort func(base int) (int, error)
	// EnvInject returns secret env vars to pass to the container, resolved from
	// the project's models.json references. Wired by the CLI; nil injects none.
	EnvInject func(configDir string) (map[string]string, error)
	// PiAgentDir returns the host global Pi agent directory (~/.pi/agent) whose
	// extensions and skills are mounted into the container. Wired by the CLI.
	PiAgentDir func() string
}

// StartResult describes an attached or started deployment.
type StartResult struct {
	ProjectID     string `json:"project_id"`
	Root          string `json:"root"`
	DeploymentID  string `json:"deployment_id"`
	ContainerName string `json:"container_name"`
	TmuxSession   string `json:"tmux_session"`
	Image         string `json:"image"`
	Kind          string `json:"kind"`
	ObservedState string `json:"observed_state"`
	WebPort       int    `json:"web_port,omitempty"`
	WebURL        string `json:"web_url,omitempty"`
}

// StartOrAttach reconciles the local deployment for a project: it seeds the
// per-project config, builds the image if missing, starts the container, and
// creates the tmux session (harness in the main window). It is idempotent.
func (c *Controller) StartOrAttach(ctx context.Context, root, projectID string) (StartResult, error) {
	res := StartResult{ProjectID: projectID, Root: root, Kind: store.KindLocal, TmuxSession: tmux.SessionName}

	if !c.Docker.Available() {
		return res, fmt.Errorf("docker is not installed or not on PATH; install Docker before running stab")
	}
	if _, err := mounts.ValidateRoot(root, nil); err != nil {
		return res, err
	}

	image, dockerfile, gpu := c.ResolveImage(projectID)
	res.Image = image
	res.ContainerName = ContainerName(projectID)
	name := Name(root)

	// Seed (or reuse) the in-project config dir (root/.stables): Dockerfile +
	// tmux.conf. It is already inside the project mount, so no extra mount is
	// needed.
	configDir := ""
	if c.ConfigDir != nil {
		var err error
		configDir, err = c.ConfigDir(root)
		if err != nil {
			return res, err
		}
	}

	exists, err := c.Docker.ContainerExists(ctx, res.ContainerName)
	if err != nil {
		return res, err
	}

	if !exists {
		ok, err := c.Docker.ImageExists(ctx, image)
		if err != nil {
			return res, err
		}
		stale := false
		if ok {
			if builtWith, known := ImageTemplateVersion(c.StateRoot, image); known && c.TemplateVersion != "" && builtWith != c.TemplateVersion {
				stale = true
				fmt.Fprintf(os.Stderr, "stab: runtime image %s was built with template %s (current: %s); rebuilding\n", image, builtWith, c.TemplateVersion)
			} else if !known && c.TemplateVersion != "" {
				fmt.Fprintf(os.Stderr, "stab: image %s predates template tracking; if its runtime is outdated run: stab update\n", image)
			}
		}
		if !ok || stale {
			if c.BuildImage != nil {
				if err := c.BuildImage(ctx, image, configDir, dockerfile); err != nil {
					return res, fmt.Errorf("build image %s: %w", image, err)
				}
				// Image meta is recorded by the CLI's build hook (single choke point).
			} else if err := c.Docker.PullImage(ctx, image); err != nil {
				return res, fmt.Errorf("pull image %s: %w", image, err)
			}
		}
		stateDir := StateDirForProject(c.StateRoot, projectID)
		if err := os.MkdirAll(stateDir, 0o700); err != nil {
			return res, fmt.Errorf("create project state dir %s: %w", stateDir, err)
		}
		if err := c.Docker.RunContainer(ctx, c.runOptions(root, projectID, name, image, res.ContainerName, configDir, gpu, &res)); err != nil {
			return res, err
		}
	} else {
		labels, err := c.Docker.ContainerLabels(ctx, res.ContainerName)
		if err != nil {
			return res, err
		}
		if labelSuper(labels) != c.Super {
			c.logf("stab: container %s super mode changed (%t -> %t); recreating\n", res.ContainerName, labelSuper(labels), c.Super)
			if err := c.Docker.RemoveContainer(ctx, res.ContainerName); err != nil {
				return res, err
			}
			if err := c.Docker.RunContainer(ctx, c.runOptions(root, projectID, name, image, res.ContainerName, configDir, gpu, &res)); err != nil {
				return res, err
			}
		} else {
			status, err := c.Docker.ContainerStatus(ctx, res.ContainerName)
			if err != nil {
				return res, err
			}
			if status != "running" {
				if err := c.Docker.StartContainer(ctx, res.ContainerName); err != nil {
					return res, err
				}
			}
		}
	}

	// Wait until the container is running, verify the container user can write
	// under its home, then create the tmux session (harness in the main window). The
	// code-server entrypoint takes a moment to start.
	if err := c.waitRunning(ctx, res.ContainerName, 30*time.Second); err != nil {
		return res, err
	}
	if err := c.probeAgentDir(ctx, res.ContainerName); err != nil {
		return res, err
	}
	if err := c.prewarmPiNPM(ctx, res.ContainerName); err != nil {
		return res, err
	}
	if err := c.ensureTmuxSession(ctx, res.ContainerName, name); err != nil {
		return res, err
	}

	dep := store.Deployment{
		ID:            deploymentID(projectID),
		ProjectID:     projectID,
		Kind:          store.KindLocal,
		ContainerName: res.ContainerName,
		TmuxSession:   tmux.SessionName,
		Image:         image,
		DesiredState:  "running",
		ObservedState: "running",
	}
	if _, err := c.Store.UpsertDeployment(dep); err != nil {
		return res, err
	}
	res.DeploymentID = dep.ID
	res.ObservedState = "running"

	c.Store.UpsertContainer(store.Container{
		ID: "ctr-" + projectID, DeploymentID: dep.ID, Name: res.ContainerName, Status: "running",
	})
	c.Store.UpsertTmuxSession(store.TmuxSession{
		ID: "tmux-" + projectID, DeploymentID: dep.ID, Name: tmux.SessionName,
	})
	if _, err := c.Store.UpsertProject(store.Project{
		ID: projectID, Name: Name(root), RootPath: root, Image: image,
	}); err != nil {
		return res, err
	}
	c.Store.RecordAudit("user", "project.started", "project", projectID, map[string]string{"root": root, "image": image})
	return res, nil
}

func (c *Controller) logf(format string, args ...any) {
	w := c.ErrOut
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, format, args...)
}

// imageFor returns the per-project image tag. A custom image recorded on the
// project takes precedence; otherwise each project gets its own image built
// from its config dir (the GPU variant when gpu is true).
func (c *Controller) imageFor(projectID string, gpu bool) string {
	if p, err := c.Store.GetProject(projectID); err == nil && p.Image != "" {
		return p.Image
	}
	if gpu {
		return ImageTagGPU(projectID)
	}
	return ImageTag(projectID)
}

// gpuSpec resolves the GPU setting for a deployment. An empty profile value
// means "auto": use all GPUs when nvidia-smi is available (nvidia-smi by
// default on NVIDIA hosts). "off" forces CPU; any other value ("all", "0,1")
// is used verbatim when a GPU is available.
func (c *Controller) gpuSpec() string {
	gpu := c.Config.Profile.GPU
	if gpu == "off" {
		return ""
	}
	if gpu == "" {
		if c.Docker.GPUsAvailable() {
			return "all"
		}
		return ""
	}
	if c.Docker.GPUsAvailable() {
		return gpu
	}
	fmt.Fprintf(os.Stderr, "stab: GPU requested (%q) but nvidia-smi is not available; launching without GPU\n", gpu)
	return ""
}

// ResolveImage returns the image tag, Dockerfile, and GPU spec for a project,
// honoring the profile's GPU setting and any custom image on the project.
func (c *Controller) ResolveImage(projectID string) (image, dockerfile, gpu string) {
	gpu = c.gpuSpec()
	image = c.imageFor(projectID, gpu != "")
	if gpu != "" {
		dockerfile = "Dockerfile.gpu"
	} else {
		dockerfile = "Dockerfile"
	}
	return image, dockerfile, gpu
}

// piAgentVolumes returns the mounts for the host's global Pi agent directory so
// the same skills, extensions, and installed packages are available inside the
// container on launch. Optional paths (npm/git package dirs, settings.json) are
// only mounted when they exist, so Docker never creates an empty placeholder (a
// single-file mount of a missing source becomes a directory).
func (c *Controller) piAgentVolumes() map[string]string {
	if c.PiAgentDir == nil {
		return nil
	}
	dir := c.PiAgentDir()
	if dir == "" {
		return nil
	}
	// Mount the whole agent dir (not just skills/extensions) so Pi's credential
	// store (auth.json) and models.json persist across container recreation —
	// a Pi login must survive `stab update` without re-authenticating.
	return map[string]string{dir: "/home/coder/.pi/agent"}
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func (c *Controller) defaultExtraVolumes() map[string]string {
	vols := c.piAgentVolumes()
	if vols == nil {
		vols = map[string]string{}
	}
	if c.Super {
		if fi, err := os.Stat(dockerSocketPath); err == nil && fi.Mode()&os.ModeSocket != 0 {
			vols[dockerSocketPath] = "/var/run/docker.sock"
		}
	}
	return vols
}

func labelSuper(labels map[string]string) bool { return labels["stab.super"] == "1" }

func (c *Controller) runOptions(root, projectID, name, image, containerName, configDir, gpu string, res *StartResult) docker.RunOptions {
	prof := c.Config.Profile
	containerProject := ContainerProjectDir(name)

	env := map[string]string{
		"STAB_PROJECT_ID":       projectID,
		"STAB_PROJECT_NAME":     name,
		"STAB_PROJECT_DIR":      containerProject,
		"STAB_HOST_PROJECT_DIR": root,
		// Persist Pi sessions inside the project's .stables dir (which is
		// bind-mounted and survives `stab kill`) so conversations can be
		// resumed after the container is recreated.
		"PI_CODING_AGENT_SESSION_DIR": filepath.Join(containerProject, ".stables", "sessions"),
		"PUID":                        strconv.Itoa(os.Getuid()),
		"PGID":                        strconv.Itoa(os.Getgid()),
	}
	labels := map[string]string{
		"stab.project_id": projectID,
		"stab.kind":       store.KindLocal,
		"stab.super":      "0",
	}
	if c.Super {
		env["STAB_DOCKER_SOCK"] = dockerSocketPath
		env["DOCKER_HOST"] = "unix:///var/run/docker.sock"
		labels["stab.super"] = "1"
	}

	opts := docker.RunOptions{
		Name:              containerName,
		Image:             image,
		ProjectMount:      root,
		ContainerProject:  containerProject,
		StateDir:          StateDirForProject(c.StateRoot, projectID),
		ContainerStateDir: ContainerStateDir,
		Network:           prof.Network,
		AllowedEndpoints:  prof.AllowedEndpoints,
		CapDrop:           prof.CapDrop,
		Privileged:        prof.Privileged,
		HostNetwork:       prof.HostNetwork,
		ReadOnlyRoot:      prof.ReadOnlyRoot,
		ReadOnlyProject:   prof.ReadOnlyProject,
		CPULimit:          prof.CPULimit,
		MemoryLimit:       prof.MemoryLimit,
		PidsLimit:         prof.PidsLimit,
		WorkingDir:        containerProject,
		AutoRestart:       "unless-stopped",
		Env:               env,
		Labels:            labels,
		ExtraVolumes:      c.defaultExtraVolumes(),
		// Open code-server on the per-project path so its title/Explorer shows
		// the real project name, not a generic "workspace".
		Command: []string{"--bind-addr", "0.0.0.0:8080", containerProject},
	}

	// GPU: gpu was resolved by gpuSpec (auto-detected when the profile leaves it
	// unset), so nvidia-smi is available by default on NVIDIA hosts.
	if gpu != "" {
		opts.GPUs = gpu
	}

	// Inject only the secrets referenced by the project's models.json.
	if c.EnvInject != nil && configDir != "" {
		if env, err := c.EnvInject(configDir); err == nil {
			for k, v := range env {
				opts.Env[k] = v
			}
		}
	}

	// Expose code-server on a localhost-only dynamic port (unless networking is
	// disabled entirely).
	if prof.Network != docker.NetworkDeny && c.NextPort != nil {
		hostPort, err := c.NextPort(c.Config.CodeServerPort)
		if err == nil && hostPort > 0 {
			opts.PortBindings = []string{fmt.Sprintf("127.0.0.1:%d:%d", hostPort, c.Config.CodeServerPort)}
			res.WebPort = hostPort
			res.WebURL = fmt.Sprintf("http://localhost:%d", hostPort)
		}
	}

	// Allow the harness to reach host services such as Ollama.
	if prof.Network != docker.NetworkDeny {
		opts.ExtraHosts = []string{"host.docker.internal:host-gateway"}
	}

	return opts
}

// waitRunning polls until the container is running.
func (c *Controller) waitRunning(ctx context.Context, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, err := c.Docker.ContainerStatus(ctx, name)
		if err == nil && status == "running" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("container %s did not become running within %s", name, timeout)
}

// probeAgentDir verifies the container user can create and write Pi's agent
// dir. Without this probe, a uid/gid mismatch or stale home ownership surfaces
// later as opaque Pi EACCES (fd download, models.json load, availability
// refresh) instead of an actionable deployment error.
func (c *Controller) probeAgentDir(ctx context.Context, container string) error {
	script := `mkdir -p "$HOME/.pi/agent" && touch "$HOME/.pi/agent/.stab-probe" && rm -f "$HOME/.pi/agent/.stab-probe"`
	var out string
	var err error
	for attempt := 0; attempt < agentProbeAttempts; attempt++ {
		out, err = c.Docker.ExecAs(ctx, container, ContainerUser, "bash", "-c", script)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(agentProbeDelay):
		}
	}
	return fmt.Errorf("%s cannot write $HOME/.pi/agent inside %s after %d attempts: %s. Verify the runtime template is current (stab update) and that PUID/PGID match the host user",
		ContainerUser, container, agentProbeAttempts, probeDetails(out, err))
}

func probeDetails(out string, err error) string {
	out = strings.TrimSpace(out)
	if out == "" || out == "ok" {
		return err.Error()
	}
	return out
}

// prewarmPiNPM installs shared Pi npm package dependencies before the harness
// starts. Stables deploys package metadata/settings but skips node_modules for
// portability, so the first `pi` run otherwise spends minutes doing npm work in
// the tmux main window.
func (c *Controller) prewarmPiNPM(ctx context.Context, container string) error {
	needsScript := `dir="$HOME/.pi/agent/npm"; test -f "$dir/package.json" && test ! -d "$dir/node_modules"`
	if _, err := c.Docker.ExecAs(ctx, container, ContainerUser, "bash", "-c", needsScript); err != nil {
		return nil
	}

	c.logf("stab: setting up Pi packages inside %s; downloading dependencies (this can take a couple minutes)...\n", container)
	installScript := `set -e
	dir="$HOME/.pi/agent/npm"
	cd "$dir"
	npm install`
	out, err := c.Docker.ExecAs(ctx, container, ContainerUser, "bash", "-c", installScript)
	if err != nil {
		return fmt.Errorf("prewarm Pi npm packages inside %s: %s", container, probeDetails(out, err))
	}
	c.logf("stab: Pi npm packages ready inside %s\n", container)
	return nil
}

// ensureTmuxSession creates the tmux session inside the container (as the
// container user) if it does not already exist, loading the project's tmux.conf
// from its in-project .stables dir. It retries briefly while the container is
// still starting.
func (c *Controller) ensureTmuxSession(ctx context.Context, container, name string) error {
	containerProject := ContainerProjectDir(name)
	configFile := ContainerConfigDir(name) + "/tmux.conf"
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		if _, err := c.Docker.ExecAs(ctx, container, ContainerUser, tmux.HasSession(tmux.SessionName)...); err == nil {
			return nil
		}
		_, lastErr = c.Docker.ExecAs(ctx, container, ContainerUser, tmux.NewSessionConfig(tmux.SessionName, tmux.DefaultLayout(c.Config.HarnessCommand, ContainerConfigDir(name)), containerProject, configFile)...)
		if lastErr == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return lastErr
}

// AttachCommand returns the interactive command that attaches to the workspace
// as the container user. It uses a grouped session so multiple terminals can
// attach to the same workspace while navigating independently.
func (c *Controller) AttachCommand(ctx context.Context, container, window string) *exec.Cmd {
	script := tmux.AttachGroupedScript(tmux.SessionName, window)
	return c.Docker.ExecCommandAs(ctx, container, ContainerUser, "bash", "-c", script)
}

// Kill stops and removes a local deployment.
func (c *Controller) Kill(ctx context.Context, projectID string) error {
	container := ContainerName(projectID)
	status, err := c.Docker.ContainerStatus(ctx, container)
	if err == nil && status != "" {
		_ = c.Docker.StopContainer(ctx, container)
		_ = c.Docker.RemoveContainer(ctx, container)
	}
	dep, err := c.Store.GetDeployment(deploymentID(projectID))
	if err == nil {
		dep.ObservedState = "stopped"
		dep.DesiredState = "stopped"
		_, _ = c.Store.UpsertDeployment(dep)
	}
	c.Store.RecordAudit("user", "project.stopped", "project", projectID, nil)
	return nil
}

// Status returns the observed state of a local deployment, or empty if none.
func (c *Controller) Status(ctx context.Context, projectID string) (string, error) {
	container := ContainerName(projectID)
	status, err := c.Docker.ContainerStatus(ctx, container)
	if err != nil {
		return "", err
	}
	if status == "" {
		return "not_created", nil
	}
	return status, nil
}

// deploymentID derives a stable deployment ID for a project.
func deploymentID(projectID string) string {
	return projectID + ":local"
}
