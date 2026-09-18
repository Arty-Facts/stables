// Package mounts owns local filesystem mount policy. The validation logic here
// is the security boundary between "an approved project path" and "an
// arbitrary host directory": every project root is canonicalized and checked
// against an allowlist and a set of forbidden locations before Docker mounts it.
package mounts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrRejected is returned when a path fails validation. The message is safe to
// show to a user and never contains secret material.
type ErrRejected struct{ Reason string }

func (e *ErrRejected) Error() string { return e.Reason }

func rejectf(format string, args ...any) *ErrRejected {
	return &ErrRejected{Reason: fmt.Sprintf(format, args...)}
}

// Forbidden path components: anything under these names is never mountable or
// visible inside a deployment, regardless of allowlist.
var forbiddenComponents = []string{
	".ssh",
	".gnupg",
	".aws",
	".azure",
	".config/gcloud",
	".docker",
	".pi",
}

// ValidateRoot canonicalizes path and verifies that it:
//
//  1. is an absolute, existing directory,
//  2. is inside one of the allowed roots (or allowedRoots is empty, meaning
//     "any path" is still subject to the forbidden checks below),
//  3. does not traverse a symlink to escape its lexical path,
//  4. contains no forbidden component,
//  5. is not an unrestricted home directory,
//  6. is not a Docker socket or other device.
//
// It returns the canonical (symlink-resolved) path on success.
func ValidateRoot(path string, allowedRoots []string) (string, error) {
	if path == "" {
		return "", rejectf("empty path")
	}
	if !filepath.IsAbs(path) {
		return "", rejectf("path must be absolute: %q", path)
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", rejectf("cannot stat %q: %v", path, err)
	}
	if !info.IsDir() {
		return "", rejectf("not a directory: %q", path)
	}
	if info.Mode()&os.ModeDevice != 0 {
		return "", rejectf("device files cannot be mounted: %q", path)
	}

	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", rejectf("cannot resolve symlinks in %q: %v", path, err)
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return "", rejectf("cannot absolutize %q: %v", path, err)
	}

	if err := checkForbidden(canonical); err != nil {
		return "", err
	}
	if err := checkNotHome(canonical); err != nil {
		return "", err
	}

	if len(allowedRoots) > 0 {
		if !withinAny(canonical, allowedRoots) {
			return "", rejectf("path %q is outside the approved roots", path)
		}
	}

	return canonical, nil
}

// checkForbidden rejects paths that contain a forbidden component.
func checkForbidden(path string) error {
	clean := filepath.Clean(path)
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		lower := strings.ToLower(part)
		for _, fb := range forbiddenComponents {
			if lower == fb {
				return rejectf("path %q contains forbidden component %q", path, fb)
			}
		}
	}
	// Reject Docker sockets explicitly even if named otherwise.
	if strings.Contains(lowerBase(path), "docker.sock") {
		return rejectf("Docker socket paths cannot be mounted: %q", path)
	}
	return nil
}

func lowerBase(p string) string {
	return strings.ToLower(filepath.Base(p))
}

// checkNotHome rejects bare home-directory mounts, which would otherwise
// sweep in credential directories by accident.
func checkNotHome(path string) error {
	home, err := os.UserHomeDir()
	if err == nil {
		home, _ = filepath.EvalSymlinks(home)
		if canonicalPath(path) == canonicalPath(home) {
			return rejectf("mounting an unrestricted home directory is not allowed: %q", path)
		}
	}
	// Also reject the empty-string home marker if resolution failed.
	if home == "" {
		if strings.HasSuffix(filepath.Clean(path), string(filepath.Separator)+".") {
			return rejectf("unresolved home directory path rejected: %q", path)
		}
	}
	return nil
}

func canonicalPath(p string) string {
	c, err := filepath.EvalSymlinks(p)
	if err != nil {
		c = p
	}
	return filepath.Clean(c)
}

// withinAny reports whether path is equal to or nested under any allowed root.
func withinAny(path string, roots []string) bool {
	for _, root := range roots {
		rc := canonicalPath(root)
		pc := canonicalPath(path)
		if pc == rc {
			return true
		}
		rel, err := filepath.Rel(rc, pc)
		if err != nil {
			continue
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
