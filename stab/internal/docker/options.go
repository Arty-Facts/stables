package docker

import (
	"fmt"
	"sort"
	"strconv"
)

// Network policy values (mirrored from config to avoid an import cycle).
const (
	NetworkDeny      = "deny"
	NetworkBridge    = "bridge"
	NetworkAllowlist = "allowlist"
	NetworkInternet  = "internet"
)

// RunOptions describes a container launch.
type RunOptions struct {
	Name              string            // container name
	Image             string            // image reference
	ProjectMount      string            // host path to the project (source of truth)
	ContainerProject  string            // target path inside the container
	StateDir          string            // host state dir for persistence (optional)
	ContainerStateDir string            // target path for state inside container
	Network           string            // deny | allowlist | internet
	AllowedEndpoints  []string          // host[:port] add-host entries for allowlist
	CapDrop           []string          // linux capabilities to drop
	Privileged        bool              // must default to false
	HostNetwork       bool              // must default to false
	ReadOnlyRoot      bool              // mount rootfs read-only
	ReadOnlyProject   bool              // mount project read-only
	CPULimit          string            // e.g. "1.0"
	MemoryLimit       string            // e.g. "2g"
	PidsLimit         int               // 0 = unlimited
	Env               map[string]string // environment variables (secrets only as refs)
	User              string            // uid:gid to run as
	Labels            map[string]string // docker labels
	WorkingDir        string            // default working dir inside container
	ExtraVolumes      map[string]string // host:container additional mounts (already approved)
	PortBindings      []string          // -p bindings, e.g. "127.0.0.1:8080:8080"
	ExtraHosts        []string          // --add-host entries, e.g. "host.docker.internal:host-gateway"
	Entrypoint        string            // override entrypoint ("" = image default)
	Command           []string          // override command
	RemoveAfterStop   bool              // --rm
	AutoRestart       string            // restart policy, e.g. "unless-stopped"
	TmpfsSize         string            // tmpfs for /tmp when read-only root
	GPUs              string            // "all", "0", "0,1" — empty means no GPU
}

// Container log rotation. Docker's default is an unbounded json-file log, which
// grows for as long as the container lives.
const (
	logDriver  = "json-file"
	logMaxSize = "10m"
	logMaxFile = "3"
)

// BuildRunArgs renders a `docker run -d ...` argument list. It is a pure
// function so tests can assert the security flags without touching Docker.
func BuildRunArgs(opts RunOptions) ([]string, error) {
	if opts.Name == "" {
		return nil, fmt.Errorf("docker: container name required")
	}
	if opts.Image == "" {
		return nil, fmt.Errorf("docker: image required")
	}

	args := []string{"run", "-d", "--name", opts.Name}

	// Cap the container's log on disk. The agent is chatty and Docker's default
	// json-file driver never rotates, so a long-lived project would grow the
	// host's log until the disk fills. Rotation is not configurable: an
	// unbounded log is never what anyone wants.
	args = append(args,
		"--log-driver", logDriver,
		"--log-opt", "max-size="+logMaxSize,
		"--log-opt", "max-file="+logMaxFile,
	)

	if opts.HostNetwork {
		args = append(args, "--network", "host")
	} else {
		switch opts.Network {
		case "", NetworkDeny:
			args = append(args, "--network", "none")
		case NetworkBridge, NetworkAllowlist, NetworkInternet:
			args = append(args, "--network", "bridge")
		default:
			return nil, fmt.Errorf("docker: invalid network policy %q", opts.Network)
		}
	}

	if opts.Privileged {
		args = append(args, "--privileged")
	} else {
		// sudo (setuid) is intentionally left enabled so the agent can install
		// packages etc. inside the container; `no-new-privileges` would block it.
		// Capabilities are only dropped when the profile explicitly requests it;
		// the code-server entrypoint needs the default set to align uid/gid and
		// drop to a non-root user.
		for _, c := range opts.CapDrop {
			args = append(args, "--cap-drop", c)
		}
	}

	if opts.ReadOnlyRoot {
		args = append(args, "--read-only")
		args = append(args, "--tmpfs", "/tmp:rw,size="+tmpfsSize(opts))
		args = append(args, "--tmpfs", "/run:rw,size=64m")
	}

	if opts.ReadOnlyProject {
		args = append(args, "-v", fmt.Sprintf("%s:%s:ro", opts.ProjectMount, opts.ContainerProject))
	} else {
		args = append(args, "-v", fmt.Sprintf("%s:%s", opts.ProjectMount, opts.ContainerProject))
	}

	if opts.StateDir != "" && opts.ContainerStateDir != "" {
		args = append(args, "-v", fmt.Sprintf("%s:%s", opts.StateDir, opts.ContainerStateDir))
	}

	// Additional volumes are sorted for deterministic output.
	extraKeys := make([]string, 0, len(opts.ExtraVolumes))
	for k := range opts.ExtraVolumes {
		extraKeys = append(extraKeys, k)
	}
	sort.Strings(extraKeys)
	for _, host := range extraKeys {
		args = append(args, "-v", fmt.Sprintf("%s:%s", host, opts.ExtraVolumes[host]))
	}

	if opts.Network == NetworkAllowlist {
		// Approximation: publish no ports and add explicit host mappings only.
		// Full endpoint allowlisting is enforced by the runtime image's
		// egress rules in a later milestone.
		eps := append([]string{}, opts.AllowedEndpoints...)
		sort.Strings(eps)
		for _, ep := range eps {
			args = append(args, "--add-host", ep)
		}
	}

	// Port bindings and extra host entries are sorted for determinism.
	ports := append([]string{}, opts.PortBindings...)
	sort.Strings(ports)
	for _, p := range ports {
		args = append(args, "-p", p)
	}
	hosts := append([]string{}, opts.ExtraHosts...)
	sort.Strings(hosts)
	for _, h := range hosts {
		args = append(args, "--add-host", h)
	}
	if opts.GPUs != "" {
		args = append(args, "--gpus", opts.GPUs)
	}

	if opts.CPULimit != "" {
		args = append(args, "--cpus", opts.CPULimit)
	}
	if opts.MemoryLimit != "" {
		args = append(args, "--memory", opts.MemoryLimit)
	}
	if opts.PidsLimit > 0 {
		args = append(args, "--pids-limit", strconv.Itoa(opts.PidsLimit))
	}
	if opts.User != "" {
		args = append(args, "--user", opts.User)
	}
	if opts.WorkingDir != "" {
		args = append(args, "--workdir", opts.WorkingDir)
	}
	if opts.Entrypoint != "" {
		args = append(args, "--entrypoint", opts.Entrypoint)
	}
	if opts.AutoRestart != "" {
		args = append(args, "--restart", opts.AutoRestart)
	}
	if opts.RemoveAfterStop {
		args = append(args, "--rm")
	}

	// Environment is sorted for determinism.
	envKeys := make([]string, 0, len(opts.Env))
	for k := range opts.Env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	for _, k := range envKeys {
		args = append(args, "-e", k+"="+opts.Env[k])
	}

	// Labels sorted for determinism.
	labelKeys := make([]string, 0, len(opts.Labels))
	for k := range opts.Labels {
		labelKeys = append(labelKeys, k)
	}
	sort.Strings(labelKeys)
	for _, k := range labelKeys {
		args = append(args, "--label", k+"="+opts.Labels[k])
	}

	args = append(args, opts.Image)
	args = append(args, opts.Command...)
	return args, nil
}

func tmpfsSize(opts RunOptions) string {
	if opts.TmpfsSize != "" {
		return opts.TmpfsSize
	}
	return "256m"
}
