// Package cli implements the stab command-line interface.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/stables/stab/internal/config"
	"github.com/stables/stab/internal/docker"
	"github.com/stables/stab/internal/project"
	"github.com/stables/stab/internal/secrets"
	"github.com/stables/stab/internal/store"
	"github.com/stables/stab/internal/version"
	stabruntime "github.com/stables/stab/runtime"
)

type App struct {
	Home     string
	Config   config.Config
	Store    *store.Store
	Docker   *docker.Client
	Projects *project.Controller
	Super    bool
	In       io.Reader
	Out      io.Writer
	ErrOut   io.Writer
}

// New builds an App with the default state under home.
func New(home string, in io.Reader, out, errOut io.Writer) (*App, error) {
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		home = h
	}
	cfg, err := config.Load(home)
	if err != nil {
		return nil, err
	}
	stateRoot := cfg.StateDirPath(home)
	if err := os.MkdirAll(stateRoot, 0o700); err != nil {
		return nil, err
	}
	st, err := store.Open(filepath.Join(stateRoot, "stab.db"))
	if err != nil {
		return nil, err
	}
	dc := &docker.Client{}
	pc := &project.Controller{
		Docker:    dc,
		Store:     st,
		Config:    cfg,
		StateRoot: stateRoot,
		ErrOut:    errOut,
	}
	app := &App{
		Home:     home,
		Config:   cfg,
		Store:    st,
		Docker:   dc,
		Projects: pc,
		In:       in,
		Out:      out,
		ErrOut:   errOut,
	}
	// Seed the in-project config dir, build images locally from it, and map a
	// free host port for code-server.
	pc.ConfigDir = app.ensureProjectConfig
	pc.BuildImage = app.buildProjectImage
	pc.NextPort = app.findFreePort
	pc.EnvInject = app.envInjectForProject
	pc.TemplateVersion = stabruntime.TemplateVersionString()
	// Mount the host's global Pi extensions and skills into the container so
	// they are shared across workspaces.
	pc.PiAgentDir = func() string {
		dir := filepath.Join(home, ".pi", "agent")
		os.MkdirAll(filepath.Join(dir, "extensions"), 0o755)
		os.MkdirAll(filepath.Join(dir, "skills"), 0o755)
		// Package dirs are created (not just conditionally mounted) so `pi
		// install`'d packages always have a shared, mounted home.
		os.MkdirAll(filepath.Join(dir, "npm"), 0o755)
		os.MkdirAll(filepath.Join(dir, "git"), 0o755)
		return dir
	}
	return app, nil
}

// Close releases resources.
func (a *App) Close() { a.Store.Close() }

// ensureProjectConfig seeds the in-project config directory (root/.stables) with
// the embedded Dockerfile, entrypoint, and tmux.conf on first use. Because it
// lives under the project root it is automatically mapped into the container.
// A template-version marker tracks which defaults were seeded: when the binary
// ships new defaults (a template version bump), the files are re-seeded. Within
// the same template version, existing files are never overwritten.
func (a *App) ensureProjectConfig(root string) (string, error) {
	dir := project.ConfigDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	files := map[string]string{
		"Dockerfile":     stabruntime.Dockerfile(),
		"Dockerfile.gpu": stabruntime.DockerfileGPU(),
		"entrypoint.sh":  stabruntime.Entrypoint(),
		"tmux.conf":      stabruntime.TmuxConf(),
	}
	marker := filepath.Join(dir, ".stables-template-version")
	current := stabruntime.TemplateVersionString()

	// Re-seed if the marker is missing or stale (defaults changed upstream).
	data, err := os.ReadFile(marker)
	if err != nil || strings.TrimSpace(string(data)) != current {
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				return "", a.configWriteError(filepath.Join(dir, name), err)
			}
		}
		if err := os.WriteFile(marker, []byte(current), 0o644); err != nil {
			return "", a.configWriteError(marker, err)
		}
	} else {
		// Same template version: only fill in files the user deleted.
		for name, content := range files {
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); os.IsNotExist(err) {
				if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
					return "", a.configWriteError(p, err)
				}
			}
		}
	}

	// Refresh the stables-tunnel-* providers from whatever Ollama is reachable
	// on the local inference port before seeding/copying the canonical file.
	// Best-effort: a down Ollama just removes the tunnel keys, never fails stab.
	if err := a.refreshTunnelModels(); err != nil {
		fmt.Fprintf(a.ErrOut, "stab: refresh tunnel models: %v\n", err)
	}

	// models.json is sourced (never embedded): copy the canonical host file
	// into the workspace if the workspace does not have one yet.
	if err := a.seedProjectModels(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// seedProjectModels copies the canonical ~/.stables/models.json into the
// project's .stables/ dir if it is not already present. The canonical file is
// managed by `make install` and provider sync, never embedded in the binary.
func (a *App) seedProjectModels(dir string) error {
	projectModels := filepath.Join(dir, "models.json")
	if _, err := os.Stat(projectModels); err == nil {
		return nil
	}
	canonical := filepath.Join(a.Config.StateDirPath(a.Home), "models.json")
	data, err := os.ReadFile(canonical)
	if err != nil {
		return nil // no canonical models yet; provider sync will add it later
	}
	return os.WriteFile(projectModels, data, 0o644)
}

// syncProjectModels overwrites the workspace models.json with the canonical
// host copy. Called by `stab update` so provider changes propagate.
func (a *App) syncProjectModels(dir string) error {
	canonical := filepath.Join(a.Config.StateDirPath(a.Home), "models.json")
	data, err := os.ReadFile(canonical)
	if err != nil {
		return nil
	}
	return os.WriteFile(filepath.Join(dir, "models.json"), data, 0o644)
}

// envInjectForProject resolves the secret references in the project's
// models.json against ~/.stables/secrets.env and returns only the referenced
// values, so unrelated secrets never enter the container.
func (a *App) envInjectForProject(configDir string) (map[string]string, error) {
	modelsPath := filepath.Join(configDir, "models.json")
	secretsPath := filepath.Join(a.Config.StateDirPath(a.Home), "secrets.env")
	chk, err := checkModelsSecrets(modelsPath, secretsPath)
	if err != nil {
		return nil, err
	}
	chk.report(a.ErrOut, fixHint(modelsPath, secretsPath))
	if len(chk.Refs) == 0 {
		return nil, nil
	}
	sec, err := secrets.LoadEnvFile(secretsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	env := map[string]string{}
	for _, ref := range chk.Refs {
		if v, ok := sec[ref]; ok && v != "" {
			env[ref] = v
		}
	}
	return env, nil
}

// configWriteError returns an actionable write error for a foreign-owned
// .stables directory. Prefers no-sudo recovery when the caller owns the
// parent.
func (a *App) configWriteError(path string, err error) error {
	if errors.Is(err, os.ErrPermission) {
		dir := filepath.Dir(path)
		return fmt.Errorf(
			"cannot write %s: %v — %s is likely owned by another user from a prior install\n"+
				"  fix without sudo (if you own the parent):  rm -rf '%s'  then re-run the command\n"+
				"  otherwise have an admin run:  chown -R $(id -u):$(id -g) '%s'  (or: rm -rf '%s')",
			path, err, dir, dir, dir, dir)
	}
	return fmt.Errorf("write %s: %w", path, err)
}

// buildProjectImage builds a per-project image from its config directory,
// streaming the build output to the terminal.
func (a *App) buildProjectImage(ctx context.Context, image, contextDir, dockerfile string) error {
	return a.buildProjectImageMode(ctx, image, contextDir, dockerfile, false)
}

func (a *App) buildProjectImageFresh(ctx context.Context, image, contextDir, dockerfile string) error {
	return a.buildProjectImageMode(ctx, image, contextDir, dockerfile, true)
}

func (a *App) buildProjectImageMode(ctx context.Context, image, contextDir, dockerfile string, fresh bool) error {
	if fresh {
		fmt.Fprintf(a.ErrOut, "stab: building image %s from %s (%s) with --pull --no-cache...\n", image, contextDir, dockerfile)
		if err := a.Docker.BuildImageFresh(ctx, image, contextDir, dockerfile, a.Out, a.ErrOut); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(a.ErrOut, "stab: building image %s from %s (%s)...\n", image, contextDir, dockerfile)
		if err := a.Docker.BuildImage(ctx, image, contextDir, dockerfile, a.Out, a.ErrOut); err != nil {
			return err
		}
	}
	// Record which template version built this image so `stab .` can rebuild it
	// automatically when the runtime template moves forward.
	return project.RecordImageTemplate(a.Config.StateDirPath(a.Home), image, stabruntime.TemplateVersionString())
}

// findFreePort returns a free loopback port starting from base.
func (a *App) findFreePort(base int) (int, error) {
	if base <= 0 {
		base = 8080
	}
	for port := base; port < base+1000; port++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			ln.Close()
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free port found starting at %d", base)
}

// Run dispatches the command line and returns the process exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	super := false
	filtered := args[:0]
	for _, arg := range args {
		if arg == "--super" {
			super = true
			continue
		}
		filtered = append(filtered, arg)
	}
	args = filtered
	a.Super = super
	a.Projects.Super = super

	if len(args) == 0 {
		return a.cmdStartOrAttach(ctx, ".")
	}
	switch args[0] {
	case ".", "start":
		return a.cmdStartOrAttach(ctx, ".")
	case "bash", "shell":
		return a.cmdBash(ctx)
	case "attach":
		project := ""
		window := ""
		if len(args) > 1 {
			project = args[1]
		}
		if len(args) > 2 {
			window = args[2]
		}
		return a.cmdAttach(ctx, project, window)
	case "list", "ls":
		return a.cmdList(ctx)
	case "status":
		project := ""
		if len(args) > 1 {
			project = args[1]
		}
		return a.cmdStatus(ctx, project)
	case "kill":
		mode := ""
		if len(args) > 1 {
			mode = args[1]
		}
		return a.cmdKill(ctx, mode)
	case "settings":
		return a.cmdSettings(ctx)
	case "update":
		return a.cmdUpdate(ctx)
	case "fetch":
		return a.cmdFetch(ctx)
	case "install":
		name := "stab"
		force := false
		for i := 1; i+1 < len(args); i++ {
			if args[i] == "--name" {
				name = sanitizeBinaryName(args[i+1])
			}
		}
		for i := 1; i < len(args); i++ {
			if args[i] == "--force" {
				force = true
			}
		}
		return a.cmdInstall(ctx, name, force)
	case "version", "--version", "-v":
		fmt.Fprintln(a.Out, "stab", version.String())
		return 0
	case "check":
		var flags []string
		for i := 1; i < len(args); i++ {
			flags = append(flags, args[i])
		}
		return a.cmdCheck(ctx, flags)
	case "help", "--help", "-h":
		a.printUsage()
		return 0
	default:
		fmt.Fprintf(a.ErrOut, "unknown command %q\n\n", args[0])
		a.printUsage()
		return 2
	}
}

func (a *App) printUsage() {
	fmt.Fprint(a.ErrOut, `usage:
  stab .                 start or attach to the current project (harness in tmux)
  stab . --super         start with docker socket mounted (sub-container spawning)
  stab bash              attach to the current project's shell window
  stab attach [project] [window]
  stab list              list known projects and containers
  stab status [project]
  stab kill [all]
  stab settings          show and edit local configuration
  stab install [--force]  install to ~/.local/bin, set PATH, merge embedded config
  stab update            rebuild the current project's image from its Dockerfile
  stab fetch             refresh models.json from the active Ollama (fast, no rebuild)
  stab check             verify prerequisites (--repair to self-heal home state dirs, --fs for fs-only)
  stab version
`)
}

// prompt asks a yes/no question with a default.
func (a *App) promptYesNo(question string, def bool) (bool, error) {
	suffix := " [y/N] "
	if def {
		suffix = " [Y/n] "
	}
	fmt.Fprint(a.Out, question+suffix)
	line, err := a.readLine()
	if err != nil {
		return def, err
	}
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return def, nil
	}
	return line == "y" || line == "yes", nil
}

func (a *App) prompt(question string) (string, error) {
	fmt.Fprint(a.Out, question+" ")
	return a.readLine()
}

func (a *App) readLine() (string, error) {
	r := bufio.NewReader(a.In)
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
