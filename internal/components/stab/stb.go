package stab

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/stables/stables/internal/assets"
	"github.com/stables/stables/internal/piagent"
	"github.com/stables/stables/internal/sealbox"
	"github.com/stables/stables/internal/state"
)

func Install(ctx context.Context, home string, force bool, password string) error {
	bin := assets.MustRead("stab/stab")
	if len(bin) < 1024 {
		return fmt.Errorf("embedded stab binary missing; run make build")
	}
	bindir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bindir, 0o755); err != nil {
		return err
	}
	stab := filepath.Join(bindir, "stab")
	if err := os.WriteFile(stab, bin, 0o755); err != nil {
		return err
	}
	// Write the baked models.json + secrets.env (full build) BEFORE stab install
	// so stab's own merge leaves them intact. Minimal builds bake empty files and
	// this becomes a no-op.
	if err := writeStablesHome(home, force, password); err != nil {
		return err
	}
	installArgs := []string{"install"}
	if force {
		installArgs = append(installArgs, "--force")
	}
	for _, args := range [][]string{{"check", "--fs", "--repair"}, installArgs} {
		cmd := exec.CommandContext(ctx, stab, args...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = append(os.Environ(), "HOME="+home)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("stab %v: %w", args, err)
		}
	}
	// Extract the baked Pi agent resources (skills, extensions, packages) so
	// remote installs carry the same resources as the build machine.
	if err := piagent.Install(home); err != nil {
		return fmt.Errorf("piagent: %w", err)
	}
	m, err := state.Load(home)
	if err != nil {
		return err
	}
	m.Installations["stab"] = state.Component{Status: "installed"}
	return m.Save(home)
}

func Update(ctx context.Context, home string) error {
	stab := filepath.Join(home, ".local", "bin", "stab")
	cmd := exec.CommandContext(ctx, stab, "update")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "HOME="+home)
	return cmd.Run()
}

func Remove(home string) error {
	return os.Remove(filepath.Join(home, ".local", "bin", "stab"))
}

// Locked reports whether the baked models.json/secrets.env are password-sealed
// (build-secrets with STABLES_PASSWORD set).
func Locked() bool {
	return sealbox.IsEncrypted(assets.HomeFile("models.json")) || sealbox.IsEncrypted(assets.HomeFile("secrets.env"))
}

// writeStablesHome writes the baked models.json + secrets.env (full build) into
// ~/.stables/, merging with any existing content. Existing providers/secrets
// win on conflict unless force is set (then the baked content replaces them).
// A sealed (encrypted) payload is only decrypted when password is correct; a
// wrong or missing password skips the secrets entirely (base install).
func writeStablesHome(home string, force bool, password string) error {
	dir := filepath.Join(home, ".stables")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	models := assets.HomeFile("models.json")
	secretsB := assets.HomeFile("secrets.env")
	if sealbox.IsEncrypted(models) || sealbox.IsEncrypted(secretsB) {
		if password == "" {
			fmt.Fprintln(os.Stderr, "stables: installer is locked; no password provided — installing without models/secrets")
			return nil
		}
		dec, err := sealbox.Decrypt(password, models)
		if err != nil {
			fmt.Fprintln(os.Stderr, "stables: incorrect password — installing without models/secrets")
			return nil
		}
		models = dec
		if len(secretsB) > 0 {
			if secretsB, err = sealbox.Decrypt(password, secretsB); err != nil {
				fmt.Fprintln(os.Stderr, "stables: incorrect password — installing without models/secrets")
				return nil
			}
		}
	}
	if len(models) > 0 {
		if err := mergeModelsFile(filepath.Join(dir, "models.json"), models, force); err != nil {
			return err
		}
	}
	if len(secretsB) > 0 {
		if err := mergeSecretsFile(filepath.Join(dir, "secrets.env"), secretsB, force, func(msg string) {
			fmt.Fprintln(os.Stderr, "stables:", msg)
		}); err != nil {
			return err
		}
	}
	return nil
}

// mergeModelsFile merges baked into dst. Existing providers win on conflict
// unless force is set (baked replaces the whole file).
func mergeModelsFile(dst string, baked []byte, force bool) error {
	bakedProviders, err := parseProviders(baked)
	if err != nil {
		return err
	}
	if len(bakedProviders) == 0 {
		return nil // nothing baked; never clobber or seed an empty file
	}
	if force {
		return writeProvidersFile(dst, bakedProviders)
	}
	existing, err := loadProvidersFile(dst)
	if err != nil {
		return err
	}
	for k, v := range bakedProviders {
		if _, ok := existing[k]; !ok {
			existing[k] = v
		}
	}
	return writeProvidersFile(dst, existing)
}

// mergeSecretsFile merges baked into dst, preserving the existing file's
// comments and ordering and appending only new keys. On a same-key collision
// with a different value the existing value stays (and warn, when non-nil, is
// called). force replaces the whole file with the baked keys.
func mergeSecretsFile(dst string, baked []byte, force bool, warn func(string)) error {
	bakedMap, err := parseEnv(baked)
	if err != nil {
		return err
	}
	if len(bakedMap) == 0 {
		return nil
	}
	if force {
		return writeEnvFile(dst, bakedMap)
	}
	existingText, _ := os.ReadFile(dst)
	existing, err := parseEnv(existingText)
	if err != nil {
		return err
	}
	var appended []string
	for k, v := range bakedMap {
		if old, ok := existing[k]; ok {
			if old != v && warn != nil {
				warn(fmt.Sprintf("secret %s already set; keeping the existing value (use --force to override)", k))
			}
			continue
		}
		appended = append(appended, k+"="+v)
	}
	if len(appended) == 0 {
		return nil
	}
	sort.Strings(appended)
	out := string(existingText)
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	out += strings.Join(appended, "\n") + "\n"
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return os.WriteFile(dst, []byte(out), 0o600)
}

// parseProviders parses a models.json body and returns its providers map.
func parseProviders(data []byte) (map[string]map[string]any, error) {
	var doc struct {
		Providers map[string]map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Providers == nil {
		doc.Providers = map[string]map[string]any{}
	}
	return doc.Providers, nil
}

// loadProvidersFile reads and parses a models.json file, returning an empty map
// when the file is absent.
func loadProvidersFile(path string) (map[string]map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]map[string]any{}, nil
		}
		return nil, err
	}
	return parseProviders(data)
}

// writeProvidersFile writes a models.json file containing only a providers map.
func writeProvidersFile(path string, providers map[string]map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(map[string]any{"providers": providers}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// parseEnv parses a KEY=value env-file body (comments, optional "export"
// prefix, optional quotes) into an ordered map.
func parseEnv(data []byte) (map[string]string, error) {
	m := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		line = strings.TrimSpace(line)
		eq := strings.Index(line, "=")
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.Trim(strings.TrimSpace(line[eq+1:]), `"'`)
		if key != "" {
			m[key] = val
		}
	}
	return m, nil
}

// writeEnvFile writes an env file with sorted keys, mode 0600.
func writeEnvFile(path string, m map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(m[k])
		b.WriteString("\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}
