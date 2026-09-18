package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stables/stab/internal/dockersetup"
	"github.com/stables/stab/internal/version"
)

// cmdInstall implements `stab install [--name <name>] [--force]`: install the
// binary into ~/.local/bin and merge the embedded (opaque) models.json and
// secrets.env into ~/.stables so the workspace works without cloning the repo.
// --force makes the baked content override existing providers/secrets.
func (a *App) cmdInstall(ctx context.Context, name string, force bool) int {
	// Self-heal stale or mis-owned state dirs first so reinstall works on
	// machines without sudo.
	for _, r := range ensureHomeDirs(a.Home, true) {
		switch r.Status {
		case "reset":
			fmt.Fprintf(a.Out, "  reset  %s: %s\n", r.Path, r.Detail)
		case "fail":
			fmt.Fprintln(a.ErrOut, "stab:")
			fmt.Fprintln(a.ErrOut, indentLines(r.Detail, "  "))
			fmt.Fprintln(a.ErrOut, "fix the path above and re-run: stab install")
			return 1
		}
	}

	// The only hard dependency is Docker. When it is missing, print the exact
	// setup commands (NVIDIA vs regular, detected from nvidia-smi).
	if !a.Docker.Available() {
		fmt.Fprintln(a.ErrOut, dockersetup.InstallInstructions(a.Docker.GPUsAvailable()))
		return 1
	}
	if a.Docker.GPUsAvailable() {
		fmt.Fprintln(a.Out, "  gpu: nvidia-smi available")
	} else {
		fmt.Fprintln(a.Out, "  gpu: nvidia-smi not detected (GPU disabled unless configured)")
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(a.ErrOut, "stab:", err)
		return 1
	}
	bindir := filepath.Join(a.Home, ".local", "bin")
	dest := filepath.Join(bindir, name)
	if err := os.MkdirAll(bindir, 0o755); err != nil {
		fmt.Fprintln(a.ErrOut, "stab:", err)
		return 1
	}
	if err := copyFile(exe, dest, 0o755); err != nil {
		fmt.Fprintln(a.ErrOut, "stab:", err)
		return 1
	}
	if name == "stab" {
		if err := ensurePathInBashrc(a.Home, bindir); err != nil {
			fmt.Fprintln(a.ErrOut, "stab:", err)
			return 1
		}
	}
	if err := a.extractConfig(force); err != nil {
		fmt.Fprintln(a.ErrOut, "stab:", err)
		return 1
	}
	// Pre-create the global Pi skills/extensions dirs so they are ready on launch.
	piAgent := filepath.Join(a.Home, ".pi", "agent")
	_ = os.MkdirAll(filepath.Join(piAgent, "skills"), 0o755)
	_ = os.MkdirAll(filepath.Join(piAgent, "extensions"), 0o755)

	fmt.Fprintln(a.Out, "")
	fmt.Fprintf(a.Out, "stab %s installed to %s\n", version.String(), dest)
	if name == "stab" {
		fmt.Fprintln(a.Out, "  PATH   : added "+bindir+" to ~/.bashrc (if missing)")
	}
	fmt.Fprintln(a.Out, "  config : ~/.stables/models.json + secrets.env")
	fmt.Fprintln(a.Out, "")
	fmt.Fprintln(a.Out, "Open a new terminal, cd into a project, and run:  "+name+" .")
	return 0
}

// sanitizeBinaryName restricts an install name to safe filename characters.
func sanitizeBinaryName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "stab"
	}
	return b.String()
}

func copyFile(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeFileAtomic(dst, data, mode)
}

// writeFileAtomic writes data to a temporary file in dst's directory and then
// renames it into place. Writing directly to dst fails with ETXTBSY ("text
// file busy") when dst is a running executable — which is exactly the case
// when `stab install` copies its own binary over ~/.local/bin/stab — and rename
// also avoids leaving a truncated binary behind if the process dies mid-write.
func writeFileAtomic(dst string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".stab-install-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dst)
}

// ensurePathInBashrc appends the bindir to PATH in ~/.bashrc if it is not
// already referenced there.
func ensurePathInBashrc(home, bindir string) error {
	bashrc := filepath.Join(home, ".bashrc")
	data, _ := os.ReadFile(bashrc)
	if strings.Contains(string(data), bindir) {
		return nil
	}
	line := fmt.Sprintf("\n# stab\nexport PATH=\"%s:$PATH\"\n", bindir)
	f, err := os.OpenFile(bashrc, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line)
	return err
}
