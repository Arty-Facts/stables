// Package project resolves a working directory to a stable project identity and
// drives the local deployment lifecycle (Docker + tmux). It is the "local
// controller" half of the stab binary.
package project

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ContainerBaseDir is the parent directory inside the container under which a
// project is mounted. Each project mounts at <base>/<project-name> so the
// code-server workspace (its Explorer root and window title) shows the real
// project name instead of a generic "workspace".
const ContainerBaseDir = "/workspace"

// ContainerStateDir is the persistent per-project state directory inside the
// container (config, logs, agent state).
const ContainerStateDir = "/stab-state"

// ContainerProjectDir returns the in-container mount path for a project name.
func ContainerProjectDir(name string) string {
	return filepath.Join(ContainerBaseDir, SanitizeName(name))
}

// ContainerConfigDir returns the in-container .stables path for a project.
func ContainerConfigDir(name string) string {
	return ContainerProjectDir(name) + "/.stables"
}

// SanitizeName returns a container-path-safe version of a project name.
func SanitizeName(name string) string {
	var sb strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			sb.WriteRune(r)
		default:
			sb.WriteRune('_')
		}
	}
	s := sb.String()
	if s == "" || s == "." || s == ".." {
		return "project"
	}
	return s
}

// IDForPath returns a stable project ID for a directory path. The ID is derived
// from the canonical (symlink-resolved, absolute) path, never the basename, so
// two differently-named directories that resolve to the same location share an
// ID and renaming a directory does not orphan its deployments.
func IDForPath(dir string) (string, error) {
	canonical, err := CanonicalPath(dir)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:8]), nil // 64-bit ID, collision-safe for a controller
}

// CanonicalPath resolves dir to an absolute, symlink-free path.
func CanonicalPath(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("project: absolute path: %w", err)
	}
	eval, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("project: resolve %s: %w", abs, err)
	}
	return filepath.Clean(eval), nil
}

// Name returns the human-friendly project name (basename of the root).
func Name(root string) string {
	return filepath.Base(root)
}

// ContainerName returns the deterministic Docker container name for a project
// ID. It is short enough for Docker and stable across invocations.
func ContainerName(projectID string) string {
	if len(projectID) > 12 {
		projectID = projectID[:12]
	}
	return "stab-" + projectID
}

// ImageTag returns the deterministic per-project image tag. Each project builds
// its own image from its own config directory.
func ImageTag(projectID string) string {
	id := projectID
	if len(id) > 12 {
		id = id[:12]
	}
	return "stab-project-" + id + ":latest"
}

// ImageTagGPU returns the GPU variant of the per-project image tag, built from
// the project's Dockerfile.gpu on NVIDIA hosts.
func ImageTagGPU(projectID string) string {
	id := projectID
	if len(id) > 12 {
		id = id[:12]
	}
	return "stab-project-" + id + ":gpu"
}

// TmuxSession is the stable tmux session name inside a deployment container.
const TmuxSession = "stab"

// ConfigDir returns the in-project stab config directory. It lives in the
// project root so it is automatically mapped into the container with the
// project — no symlinks or extra mounts needed.
func ConfigDir(root string) string {
	return filepath.Join(root, ".stables")
}

// StateDirForProject returns the host-side persistent state directory for a
// project ID under the stab state root (kept outside the repo).
func StateDirForProject(stateRoot, projectID string) string {
	return filepath.Join(stateRoot, "projects", projectID, "state")
}

// ResolveCwd resolves the current working directory to a canonical root and ID.
func ResolveCwd() (root string, id string, err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("project: getcwd: %w", err)
	}
	root, err = CanonicalPath(cwd)
	if err != nil {
		return "", "", err
	}
	id, err = IDForPath(root)
	if err != nil {
		return "", "", err
	}
	return root, id, nil
}
