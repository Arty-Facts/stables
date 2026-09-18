// Command embedgen bakes the build machine's Pi agent resources (skills,
// extensions, installed npm/git packages, and the remote package list) into
// internal/assets/files/piagent/ so a single stables binary carries them to
// every remote install. Regenerable/heavy content (node_modules, caches,
// bytecode) is skipped. Nothing is committed: the checked-in files are empty
// placeholders that `make assets` overwrites.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/stables/stables/internal/sealbox"
)

func main() {
	var skills, extensions, npmDir, gitDir, settings, models, secrets, out, homeout string
	flag.StringVar(&skills, "skills", "", "path to ~/.pi/agent/skills")
	flag.StringVar(&extensions, "extensions", "", "path to ~/.pi/agent/extensions")
	flag.StringVar(&npmDir, "npm", "", "path to ~/.pi/agent/npm (installed npm packages)")
	flag.StringVar(&gitDir, "git", "", "path to ~/.pi/agent/git (installed git packages)")
	flag.StringVar(&settings, "settings", "", "path to ~/.pi/agent/settings.json (packages list)")
	flag.StringVar(&models, "models", "", "path to ~/.stables/models.json")
	flag.StringVar(&secrets, "secrets", "", "path to ~/.stables/secrets.env")
	flag.StringVar(&out, "out", "internal/assets/files/piagent", "output dir for skills/extensions/packages")
	flag.StringVar(&homeout, "homeout", "internal/assets/files/home", "output dir for models.json/secrets.env")
	flag.Parse()

	if err := os.MkdirAll(out, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "embedgen:", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(homeout, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "embedgen:", err)
		os.Exit(1)
	}
	write(out, "skills.tgz", tgzRoots([]string{skills}))
	write(out, "extensions.tgz", tgzRoots([]string{extensions}))
	write(out, "npm.tgz", tgzRoots([]string{npmDir}))
	write(out, "git.tgz", tgzRoots([]string{gitDir}))
	write(out, "settings.json", remotePackages(settings))

	// models.json + secrets.env go to the separate `home` dir, embedded behind
	// the `embedsecrets` tag (full build). build-extensions bakes only piagent.
	// A non-empty STABLES_PASSWORD seals both files so the installer must supply
	// the same password to unlock them.
	modelsData := read(models)
	if len(modelsData) == 0 {
		modelsData = []byte("{\n  \"providers\": {}\n}\n")
	}
	secretsData := read(secrets)
	if pw := os.Getenv("STABLES_PASSWORD"); pw != "" {
		sealed, err := sealbox.Encrypt(pw, modelsData)
		if err != nil {
			fmt.Fprintln(os.Stderr, "embedgen: seal models.json:", err)
			os.Exit(1)
		}
		modelsData = sealed
		if len(secretsData) > 0 {
			sealed, err := sealbox.Encrypt(pw, secretsData)
			if err != nil {
				fmt.Fprintln(os.Stderr, "embedgen: seal secrets.env:", err)
				os.Exit(1)
			}
			secretsData = sealed
		}
		fmt.Println("embedgen: home assets sealed with installer password")
	}
	write(homeout, "models.json", modelsData)
	write(homeout, "secrets.env", secretsData)
	fmt.Println("embedgen: wrote piagent assets to", out, "and home assets to", homeout)
}

func write(dir, name string, data []byte) {
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "embedgen:", err)
		os.Exit(1)
	}
}

func read(path string) []byte {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return b
}

// tgzRoots archives every root into a single gzipped tar in memory. Entries are
// stored relative to their own root, so several roots merge into one directory
// tree on extraction. Regenerable/heavy content (node_modules, caches,
// bytecode) is skipped.
func tgzRoots(roots []string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for _, dir := range roots {
		if dir == "" {
			continue
		}
		dir = filepath.Clean(dir)
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil {
				return nil
			}
			rel, err := filepath.Rel(dir, path)
			if err != nil || rel == "." {
				return nil
			}
			rel = filepath.ToSlash(rel)
			if skipPath(rel, info.IsDir()) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			hdr, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return nil
			}
			hdr.Name = rel
			if err := tw.WriteHeader(hdr); err != nil {
				return nil
			}
			if info.Mode().IsRegular() {
				f, err := os.Open(path)
				if err != nil {
					return nil
				}
				_, _ = io.Copy(tw, f)
				f.Close()
			}
			return nil
		})
	}

	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// remotePackages returns the "packages" array from settings.json, keeping only
// remote (npm:/git:/URL) entries. Local-path package entries are not portable
// to another machine and are dropped.
func remotePackages(path string) []byte {
	data := read(path)
	if len(data) == 0 {
		return []byte("[]")
	}
	var doc struct {
		Packages []json.RawMessage `json:"packages"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return []byte("[]")
	}
	kept := []json.RawMessage{}
	for _, p := range doc.Packages {
		if isRemotePackage(p) {
			kept = append(kept, p)
		}
	}
	if kept == nil {
		kept = []json.RawMessage{}
	}
	b, err := json.Marshal(kept)
	if err != nil {
		return []byte("[]")
	}
	return b
}

func isRemotePackage(raw json.RawMessage) bool {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return isRemoteSource(s)
	}
	var obj struct {
		Source string `json:"source"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Source != "" {
		return isRemoteSource(obj.Source)
	}
	return false
}

// isRemoteSource reports whether a package source is portable to another
// machine. Filesystem paths (/, ./, ../, ~) are not; npm/git/URL sources are.
func isRemoteSource(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, ".") || strings.HasPrefix(s, "~") {
		return false
	}
	return true
}

func skipPath(rel string, isDir bool) bool {
	parts := strings.Split(rel, "/")
	for _, p := range parts {
		// `.git` is skipped deliberately: archiving it would bake private repo
		// names, remote URLs, author identities, and full commit history into
		// every shipped binary. The directory's contents, not its history, are
		// what the agent needs.
		if p == "node_modules" || p == "__pycache__" || p == ".DS_Store" || p == ".git" {
			return true
		}
	}
	if !isDir && strings.HasSuffix(rel, ".pyc") {
		return true
	}
	return false
}
