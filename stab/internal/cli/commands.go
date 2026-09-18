package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/stables/stab/internal/project"
	"github.com/stables/stab/internal/redact"
	"github.com/stables/stab/internal/store"
	"github.com/stables/stab/internal/tmux"
	"github.com/stables/stab/internal/version"
)

// cmdStartOrAttach implements `stab .`.
func (a *App) cmdStartOrAttach(ctx context.Context, _ string) int {
	root, id, err := project.ResolveCwd()
	if err != nil {
		fmt.Fprintln(a.ErrOut, "stab:", err)
		return 1
	}
	res, err := a.Projects.StartOrAttach(ctx, root, id)
	if err != nil {
		fmt.Fprintln(a.ErrOut, "stab:", redact.Redact(err.Error()))
		return 1
	}
	fmt.Fprintf(a.ErrOut, "stab: project %s -> %s (%s)\n", res.ProjectID, res.ContainerName, res.ObservedState)
	if res.WebURL != "" {
		fmt.Fprintf(a.ErrOut, "stab: code-server: %s\n", res.WebURL)
	}
	return a.attachInteractive(ctx, res.ContainerName, "")
}

// cmdBash implements `stab bash`.
func (a *App) cmdBash(ctx context.Context) int {
	root, id, err := project.ResolveCwd()
	if err != nil {
		fmt.Fprintln(a.ErrOut, "stab:", err)
		return 1
	}
	res, err := a.Projects.StartOrAttach(ctx, root, id)
	if err != nil {
		fmt.Fprintln(a.ErrOut, "stab:", redact.Redact(err.Error()))
		return 1
	}
	return a.attachInteractive(ctx, res.ContainerName, tmux.WindowShell)
}

// cmdAttach implements `stab attach [project] [window]`.
func (a *App) cmdAttach(ctx context.Context, projectArg, window string) int {
	if projectArg == "" {
		return a.cmdStartOrAttach(ctx, ".")
	}
	dep, err := a.deploymentForProject(projectArg)
	if err != nil {
		fmt.Fprintln(a.ErrOut, "stab:", err)
		return 1
	}
	return a.attachInteractive(ctx, dep.ContainerName, window)
}

// attachInteractive wires the terminal to the tmux attach command inside the
// container. It runs on the host (docker exec), never the user's shell.
func (a *App) attachInteractive(ctx context.Context, container, window string) int {
	cmd := a.Projects.AttachCommand(ctx, container, window)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(a.ErrOut, "stab: attach:", redact.Redact(err.Error()))
		return 1
	}
	return 0
}

// deploymentForProject finds a local deployment by project ID or name.
func (a *App) deploymentForProject(ref string) (store.Deployment, error) {
	p, err := a.Store.GetProject(ref)
	if err == nil {
		return a.Store.GetDeployment(p.ID + ":local")
	}
	projects, _ := a.Store.ListProjects()
	for _, p := range projects {
		if p.Name == ref {
			return a.Store.GetDeployment(p.ID + ":local")
		}
	}
	return store.Deployment{}, fmt.Errorf("no deployment for project %q", ref)
}

// cmdList implements `stab list`.
func (a *App) cmdList(ctx context.Context) int {
	projects, err := a.Store.ListProjects()
	if err != nil {
		fmt.Fprintln(a.ErrOut, "stab:", err)
		return 1
	}
	if len(projects) == 0 {
		fmt.Fprintln(a.Out, "stab: no projects found")
		return 0
	}

	tw := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PROJECT\tSTATUS\tCONTAINER\tIMAGE\tPATH")
	for _, p := range projects {
		dep, err := a.Store.GetDeployment(p.ID + ":local")
		container := "-"
		image := p.Image
		status := "unknown"
		if err == nil {
			if dep.ContainerName != "" {
				container = dep.ContainerName
			}
			if dep.Image != "" {
				image = dep.Image
			}
			if dep.ObservedState != "" {
				status = dep.ObservedState
			}
		}
		if a.Projects != nil && container != "-" {
			live, err := a.Projects.Status(ctx, p.ID)
			if err == nil && live != "" {
				status = live
			}
		}
		if image == "" {
			image = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.Name, status, container, image, p.RootPath)
	}
	if err := tw.Flush(); err != nil {
		fmt.Fprintln(a.ErrOut, "stab:", err)
		return 1
	}
	return 0
}

// cmdStatus implements `stab status [project]`.
func (a *App) cmdStatus(ctx context.Context, ref string) int {
	if ref == "" {
		deps, err := a.Store.ListDeployments("")
		if err != nil {
			fmt.Fprintln(a.ErrOut, "stab:", err)
			return 1
		}
		if len(deps) == 0 {
			fmt.Fprintln(a.Out, "no deployments")
			return 0
		}
		fmt.Fprintf(a.Out, "%-12s %-24s %-8s %s\n", "PROJECT", "CONTAINER", "STATE", "SESSION")
		for _, d := range deps {
			fmt.Fprintf(a.Out, "%-12s %-24s %-8s %s\n", d.ProjectID, d.ContainerName, d.ObservedState, d.TmuxSession)
		}
		return 0
	}
	dep, err := a.deploymentForProject(ref)
	if err != nil {
		fmt.Fprintln(a.ErrOut, "stab:", err)
		return 1
	}
	status, err := a.Projects.Status(ctx, dep.ProjectID)
	if err != nil {
		status = "unknown"
	}
	fmt.Fprintf(a.Out, "project=%s container=%s state=%s tmux=%s\n",
		dep.ProjectID, dep.ContainerName, status, dep.TmuxSession)
	return 0
}

// cmdKill implements `stab kill [all]`.
func (a *App) cmdKill(ctx context.Context, mode string) int {
	if mode == "all" {
		deps, err := a.Store.ListDeployments("")
		if err != nil {
			fmt.Fprintln(a.ErrOut, "stab:", err)
			return 1
		}
		for _, d := range deps {
			_ = a.Projects.Kill(ctx, d.ProjectID)
		}
		fmt.Fprintln(a.Out, "killed all local deployments")
		return 0
	}
	_, id, err := project.ResolveCwd()
	if err != nil {
		fmt.Fprintln(a.ErrOut, "stab:", err)
		return 1
	}
	if err := a.Projects.Kill(ctx, id); err != nil {
		fmt.Fprintln(a.ErrOut, "stab:", err)
		return 1
	}
	fmt.Fprintf(a.Out, "killed %s\n", id)
	return 0
}

// cmdUpdate implements `stab update`: re-seed defaults if the template
// changed, rebuild the current project's image, and recreate the container so
// the changes take effect.
// cmdFetch implements `stab fetch`: refresh the tunnel models in the canonical
// ~/.stables/models.json and copy it into the current project — the fast
// alternative to `stab update` when only the model list changed (no image rebuild).
func (a *App) cmdFetch(ctx context.Context) int {
	if err := a.refreshTunnelModels(); err != nil {
		fmt.Fprintf(a.ErrOut, "stab: refresh tunnel models: %v\n", err)
	}
	if s, err := readTunnelState(a.Home); err == nil && s != nil {
		fmt.Fprintf(a.Out, "stab: tunnel %s port %d\n", s.Mode, s.Port)
	}
	// Copy the refreshed canonical file into the current project, then stop any
	// running container so the next `stab .` re-runs the models merge with the
	// fresh list (a running harness does not hot-reload models.json).
	root, id, err := project.ResolveCwd()
	if err == nil {
		configDir := project.ConfigDir(root)
		if err := os.MkdirAll(configDir, 0o700); err == nil {
			if err := a.syncProjectModels(configDir); err != nil {
				fmt.Fprintf(a.ErrOut, "stab: sync project models: %v\n", err)
			}
		}
		if a.Docker != nil && a.Docker.Available() {
			name := project.ContainerName(id)
			if exists, _ := a.Docker.ContainerExists(ctx, name); exists {
				_ = a.Docker.StopContainer(ctx, name)
				_ = a.Docker.RemoveContainer(ctx, name)
				fmt.Fprintf(a.Out, "stab: stopped %s — run stab . to load the refreshed models\n", name)
			}
		}
	}
	fmt.Fprintln(a.Out, "stab: models.json refreshed")
	return 0
}

func (a *App) cmdUpdate(ctx context.Context) int {
	root, id, err := project.ResolveCwd()
	if err != nil {
		fmt.Fprintln(a.ErrOut, "stab:", err)
		return 1
	}
	if !a.Docker.Available() {
		fmt.Fprintln(a.ErrOut, "stab: docker is not available")
		return 1
	}
	image, dockerfile, _ := a.Projects.ResolveImage(id)
	configDir, err := a.ensureProjectConfig(root)
	if err != nil {
		fmt.Fprintln(a.ErrOut, "stab:", err)
		return 1
	}
	// Pull in the latest canonical models.json (managed by provider sync) so
	// the workspace reflects current providers and secret references.
	if err := a.syncProjectModels(configDir); err != nil {
		fmt.Fprintln(a.ErrOut, "stab:", err)
		return 1
	}
	fmt.Fprintf(a.Out, "stab %s: rebuilding image %s from %s (%s) with --pull --no-cache...\n", version.String(), image, configDir, dockerfile)
	if err := a.buildProjectImageFresh(ctx, image, configDir, dockerfile); err != nil {
		fmt.Fprintln(a.ErrOut, "stab:", redact.Redact(err.Error()))
		return 1
	}

	// Recreate the container so it picks up the new image.
	name := project.ContainerName(id)
	if exists, _ := a.Docker.ContainerExists(ctx, name); exists {
		_ = a.Docker.StopContainer(ctx, name)
		_ = a.Docker.RemoveContainer(ctx, name)
		fmt.Fprintf(a.Out, "recreated container %s\n", name)
	}
	res, err := a.Projects.StartOrAttach(ctx, root, id)
	if err != nil {
		fmt.Fprintln(a.ErrOut, "stab:", redact.Redact(err.Error()))
		return 1
	}
	fmt.Fprintf(a.Out, "workspace updated (%s)\n", res.ContainerName)
	if res.WebURL != "" {
		fmt.Fprintf(a.Out, "code-server: %s\n", res.WebURL)
	}
	return 0
}

// cmdCheck implements `stab check`.
//
//	stab check [--repair] [--fs]
//
// Reports (and with --repair, safely self-heals) the install prerequisites:
// Docker availability and the stab-managed home state dirs. --fs limits
// the check to the state dirs (used by stables on hosts where Docker is
// optional for a pi-only setup).
func (a *App) cmdCheck(ctx context.Context, flags []string) int {
	fsOnly := false
	repair := false
	for _, f := range flags {
		switch f {
		case "--repair":
			repair = true
		case "--fs":
			fsOnly = true
		default:
			fmt.Fprintf(a.ErrOut, "stab: unknown flag %q for check\n", f)
			return 2
		}
	}

	results := ensureHomeDirs(a.Home, repair)
	for _, r := range results {
		line := fmt.Sprintf("  %-16s %s\n", r.Path, r.Status)
		if r.Status == "fail" {
			fmt.Fprint(a.ErrOut, line)
			fmt.Fprint(a.ErrOut, indentLines(r.Detail, "    "))
		} else if r.Detail != "" {
			fmt.Fprintf(a.Out, "  %-16s %s \u2014 %s\n", r.Path, r.Status, firstLine(r.Detail))
		} else {
			fmt.Fprint(a.Out, line)
		}
	}

	fsOK := true
	for _, r := range results {
		if r.Status == "fail" {
			fsOK = false
		}
	}

	if !fsOnly {
		if a.Docker.Available() {
			fmt.Fprintln(a.Out, "  docker             ok")
		} else {
			dockerLine := "  docker             MISSING — install Docker before running stab .\n"
			fmt.Fprint(a.ErrOut, dockerLine)
			return 1
		}
	}

	// models.json / secrets.env consistency. A referenced secret with no value
	// expands to an empty apiKey in the container, and Pi rejects the whole
	// models.json when any provider has one — the entrypoint drops those
	// providers instead. Report it here (non-fatal: the agent still starts with
	// the remaining models) so `stables install` and `stab check` surface the
	// fix before the agent hits the schema error.
	secretsPath := filepath.Join(a.Config.StateDirPath(a.Home), "secrets.env")
	modelsPaths := []string{}
	seenModels := map[string]bool{}
	addModels := func(p string) {
		p = filepath.Clean(p)
		if !seenModels[p] {
			seenModels[p] = true
			modelsPaths = append(modelsPaths, p)
		}
	}
	addModels(filepath.Join(a.Config.StateDirPath(a.Home), "models.json"))
	if wd, err := os.Getwd(); err == nil {
		addModels(filepath.Join(project.ConfigDir(wd), "models.json"))
	}
	modelsFound := false
	for _, mp := range modelsPaths {
		if _, err := os.Stat(mp); err != nil {
			continue
		}
		modelsFound = true
		chk, err := checkModelsSecrets(mp, secretsPath)
		if err != nil {
			fmt.Fprintln(a.ErrOut, "stab:", err)
			return 1
		}
		if chk.OK() {
			fmt.Fprintln(a.Out, "  models             ok")
			continue
		}
		fmt.Fprintf(a.Out, "  models             %d missing secret(s)\n", len(chk.Missing))
		chk.report(a.ErrOut, fixHint(mp, secretsPath))
	}
	if !modelsFound {
		fmt.Fprintln(a.Out, "  models             none")
	}

	if !fsOK {
		fmt.Fprintln(a.ErrOut, "stab: home state dirs are not usable; fix the paths above and re-run (with --repair to let stab self-heal them)")
		return 1
	}
	fmt.Fprintln(a.Out, "stab: prerequisites present")
	return 0
}

func indentLines(s, indent string) string {
	out := ""
	for _, ln := range splitLines(s) {
		out += indent + ln + "\n"
	}
	return out
}

func firstLine(s string) string {
	lns := splitLines(s)
	if len(lns) == 0 {
		return ""
	}
	return lns[0]
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, trimSpace(cur))
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, trimSpace(cur))
	}
	return out
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
