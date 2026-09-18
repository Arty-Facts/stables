// Package piagent extracts the build machine's baked Pi agent resources
// (skills, extensions, installed packages) into ~/.pi/agent during install, so
// a remote `stables install` carries the same resources the build machine had.
package piagent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/stables/stables/internal/assets"
)

// Install creates the agent resource dirs and extracts the baked skills,
// extensions, npm/git packages, and remote package list. Empty archives are a
// no-op (minimal builds bake nothing).
func Install(home string) error {
	dir := filepath.Join(home, ".pi", "agent")
	for _, sub := range []string{"skills", "extensions", "npm", "git"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return err
		}
	}
	if err := extractTGZ(assets.PiAgentFile("skills.tgz"), filepath.Join(dir, "skills")); err != nil {
		return fmt.Errorf("skills: %w", err)
	}
	if err := extractTGZ(assets.PiAgentFile("extensions.tgz"), filepath.Join(dir, "extensions")); err != nil {
		return fmt.Errorf("extensions: %w", err)
	}
	if err := extractTGZ(assets.PiAgentFile("npm.tgz"), filepath.Join(dir, "npm")); err != nil {
		return fmt.Errorf("npm packages: %w", err)
	}
	if err := extractTGZ(assets.PiAgentFile("git.tgz"), filepath.Join(dir, "git")); err != nil {
		return fmt.Errorf("git packages: %w", err)
	}
	return mergeSettingsPackages(filepath.Join(dir, "settings.json"), assets.PiAgentFile("settings.json"))
}

// extractTGZ extracts a gzipped tar archive into dir as an update: files are
// overwritten and new files added, but nothing is deleted. An existing .git is
// preserved so the directory stays a working git clone across re-installs.
func extractTGZ(data []byte, dir string) error {
	if len(data) == 0 {
		return nil
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		// Preserve an existing .git on update installs so remotes, branches and
		// local commits are not reset by re-deploying stables.
		if isGitPath(hdr.Name) {
			if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
				continue
			}
		}

		target := filepath.Join(dir, hdr.Name)
		// Defense in depth: the archive is self-generated, but never write
		// outside dir.
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(dir)+string(os.PathSeparator)) && filepath.Clean(target) != filepath.Clean(dir) {
			return fmt.Errorf("archive entry escapes destination: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeStream(target, tr, os.FileMode(hdr.Mode)&0o777); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		case tar.TypeLink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Link(filepath.Join(dir, hdr.Linkname), target); err != nil {
				return err
			}
		case tar.TypeXHeader, tar.TypeXGlobalHeader:
			// PAX metadata headers — skip, they apply to the next entry.
			continue
		}
	}
}

func isGitPath(name string) bool {
	n := strings.TrimPrefix(name, "./")
	n = strings.TrimPrefix(n, "/")
	return n == ".git" || strings.HasPrefix(n, ".git/")
}

func writeStream(path string, r io.Reader, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

// mergeSettingsPackages adds the embedded remote packages to settings.json's
// "packages" array, deduped by source. Existing entries are preserved.
func mergeSettingsPackages(settingsPath string, embedded []byte) error {
	var incoming []json.RawMessage
	if err := json.Unmarshal(embedded, &incoming); err != nil || len(incoming) == 0 {
		return nil
	}

	doc := map[string]json.RawMessage{}
	if data, err := os.ReadFile(settingsPath); err == nil {
		_ = json.Unmarshal(data, &doc)
	}

	current := []json.RawMessage{}
	if raw, ok := doc["packages"]; ok {
		_ = json.Unmarshal(raw, &current)
	}

	have := map[string]bool{}
	for _, p := range current {
		have[packageSourceKey(p)] = true
	}
	changed := false
	for _, p := range incoming {
		key := packageSourceKey(p)
		if !have[key] {
			current = append(current, p)
			have[key] = true
			changed = true
		}
	}
	if !changed {
		return nil
	}

	b, err := json.Marshal(current)
	if err != nil {
		return err
	}
	doc["packages"] = b
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return writeStream(settingsPath, bytes.NewReader(out), 0o644)
}

// packageSourceKey returns a stable identity for a settings.json package entry
// (a source string, or the "source" field of an object entry).
func packageSourceKey(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var obj struct {
		Source string `json:"source"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Source != "" {
		return strings.TrimSpace(obj.Source)
	}
	return string(raw)
}
