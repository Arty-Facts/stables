package ollama

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/stables/stables/internal/assets"
	"github.com/stables/stables/internal/state"
)

type Options struct {
	Home          string
	Listen        string
	GPU           string // auto | on | off
	DataDir       string
	Name          string
	OllamaPort    int
	NetworkSubnet string
}

type ComposeData struct {
	DataDir           string
	ListenHost        string
	OllamaPort        int
	GPUEnabled        bool
	ContextLength     int
	GPUMemoryFraction string
	NumParallel       int
	KeepAlive         string
	FlashAttention    int
	MaxQueue          int
	Name              string
	NetworkSubnet     string
}

func DefaultData(home string) ComposeData {
	return ComposeData{
		DataDir:           filepath.Join(home, ".stables", "ollama", "data"),
		ListenHost:        "0.0.0.0",
		OllamaPort:        11434,
		ContextLength:     131072,
		GPUMemoryFraction: "0.9",
		NumParallel:       2,
		KeepAlive:         "15m",
		FlashAttention:    1,
		MaxQueue:          512,
		Name:              "stables",
	}
}

func RenderCompose(d ComposeData) ([]byte, error) {
	t, err := template.New("compose").Parse(string(assets.MustRead("ollama/docker-compose.yml.tmpl")))
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, d); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Install renders the ollama stack (Ollama only; WebUI is a separate
// component), starts it, and records the install in the local manifest.
func Install(ctx context.Context, opts Options) error {
	d := DefaultData(opts.Home)
	if opts.Listen != "" {
		d.ListenHost = opts.Listen
	}
	if opts.DataDir != "" {
		d.DataDir = opts.DataDir
	}
	if opts.Name != "" {
		d.Name = opts.Name
	}
	if opts.OllamaPort != 0 {
		d.OllamaPort = opts.OllamaPort
	}
	if opts.NetworkSubnet != "" {
		if _, _, err := net.ParseCIDR(opts.NetworkSubnet); err != nil {
			return fmt.Errorf("invalid network subnet %q", opts.NetworkSubnet)
		}
		d.NetworkSubnet = opts.NetworkSubnet
	}
	if conflicts := subnetConflicts(d.NetworkSubnet); len(conflicts) > 0 {
		return fmt.Errorf("network subnet %s conflicts with: %s; set --network-subnet to an isolated CIDR (e.g. a free 10.x/24)", d.NetworkSubnet, strings.Join(conflicts, ", "))
	}
	d.GPUEnabled = gpuEnabled(opts.GPU)

	base := filepath.Join(opts.Home, ".stables", "ollama")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(d.DataDir, 0o755); err != nil {
		return err
	}
	compose, err := RenderCompose(d)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(base, "docker-compose.yml"), compose, 0o644); err != nil {
		return err
	}
	for _, name := range []string{"setup.sh", "pull-models.sh", "catalog.json"} {
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(base, name), assets.MustRead("ollama/"+name), mode); err != nil {
			return err
		}
	}
	if err := runEnv(ctx, base, []string{
		"OLLAMA_HOST_PORT=" + strconv.Itoa(d.OllamaPort),
		"STACK_NAME=" + d.Name,
	}, "sh", "setup.sh"); err != nil {
		return err
	}

	m, err := state.Load(opts.Home)
	if err != nil {
		return err
	}
	m.Installations["ollama"] = state.Component{
		Status:     "installed",
		ComposeDir: base,
		OllamaPort: d.OllamaPort,
		GPU:        d.GPUEnabled,
		Containers: map[string]string{"ollama": d.Name + "-ollama"},
		Network:    d.Name + "-llm",
	}
	return m.Save(opts.Home)
}

func Pull(ctx context.Context, dir string, models []string) error {
	args := append([]string{"exec", "-T", "ollama", "sh", "-s"}, models...)
	cmd := composeCommand(ctx, args...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(assets.MustRead("ollama/pull-models.sh"))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func List(ctx context.Context, dir string) error {
	cmd := composeCommand(ctx, "exec", "-T", "ollama", "ollama", "list")
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func Remove(ctx context.Context, dir string, hard bool) error {
	args := []string{"down"}
	if hard {
		args = append(args, "-v")
	}
	cmd := composeCommand(ctx, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Stop stops the local ollama container (compose stop), leaving the stack
// defined so `ollama up` can start it again. It does not touch the tunnel.
func Stop(ctx context.Context, dir string) error {
	cmd := composeCommand(ctx, "stop", "ollama")
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Start starts the local ollama container (compose up -d). It does not touch
// the tunnel; if the tunnel holds the ollama port, Docker fails with
// "address already in use" and the caller should run `stables tunnel down` first.
func Start(ctx context.Context, dir string) error {
	cmd := composeCommand(ctx, "up", "-d", "ollama")
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// subnetConflicts checks the requested CIDR against every docker network and
// the host routing table. Docker network creation installs host routes for
// the subnet; if it overlaps a VPN/LAN/remote range those hosts become
// unreachable without an error, so validate before creating.
func subnetConflicts(subnet string) []string {
	var conflicts []string
	_, cand, err := net.ParseCIDR(subnet)
	if err != nil {
		return nil
	}
	if out, err := runOutput("docker", "network", "ls", "--format", "{{.Name}}\t{{.ID}}"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			if out2, err := runOutput("docker", "network", "inspect", fields[1], "--format", "{{range .IPAM.Config}}{{.Subnet}}{{end}}"); err == nil {
				for _, s := range strings.Fields(out2) {
					if _, n, err := net.ParseCIDR(s); err == nil && cidrOverlaps(cand, n) {
						conflicts = append(conflicts, "docker network "+fields[0]+" ("+s+")")
					}
				}
			}
		}
	}
	if out, err := hostRouteLines(); err == nil {
		for _, line := range strings.Split(out, "\n") {
			parts := strings.Fields(line)
			if len(parts) < 2 || !strings.Contains(parts[1], "/") {
				continue
			}
			if _, n, err := net.ParseCIDR(parts[1]); err == nil && cidrOverlaps(cand, n) {
				conflicts = append(conflicts, "host route "+strings.TrimSpace(line))
			}
		}
	}
	return conflicts
}

func cidrOverlaps(a, b *net.IPNet) bool {
	if a4 := a.IP.To4(); a4 != nil && b.IP.To4() != nil {
		loHi := func(n *net.IPNet) (uint32, uint32) {
			ip := binary.BigEndian.Uint32(n.IP.To4())
			mask := binary.BigEndian.Uint32(n.Mask)
			lo := ip & mask
			hi := lo ^ (^mask)
			return lo, hi
		}
		aLo, aHi := loHi(a)
		bLo, bHi := loHi(b)
		return aLo <= bHi && bLo <= aHi
	}
	return a.Contains(b.IP) || b.Contains(a.IP)
}

func hostRouteLines() (string, error) {
	if out, err := exec.Command("ip", "route").Output(); err == nil {
		return string(out), nil
	}
	return "", fmt.Errorf("ip route unavailable")
}

func runOutput(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

func gpuEnabled(mode string) bool {
	switch mode {
	case "on":
		return true
	case "off":
		return false
	default:
		return commandOK("nvidia-smi")
	}
}

func composeCommand(ctx context.Context, args ...string) *exec.Cmd {
	if commandOK("docker") {
		return exec.CommandContext(ctx, "docker", append([]string{"compose"}, args...)...)
	}
	return exec.CommandContext(ctx, "docker-compose", args...)
}

func runEnv(ctx context.Context, dir string, extraEnv []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func commandOK(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
