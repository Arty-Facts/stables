package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/stables/stab/internal/embedded"
)

// extractConfig merges the embedded models.json and secrets.env into the
// state directory. Existing providers/secrets win on conflict unless force is
// set (then the baked content replaces them). Provider-agnostic: the blobs
// are opaque to stab.
func (a *App) extractConfig(force bool) error {
	state := a.Config.StateDirPath(a.Home)
	if err := os.MkdirAll(state, 0o700); err != nil {
		return err
	}
	modelsPath := filepath.Join(state, "models.json")
	secretsPath := filepath.Join(state, "secrets.env")
	if err := mergeModelsFile(modelsPath, embedded.ModelsJSON(), force); err != nil {
		return err
	}
	if sec := embedded.SecretsEnv(); len(sec) > 0 {
		if err := mergeSecretsFile(secretsPath, sec, force, func(msg string) {
			fmt.Fprintln(a.ErrOut, "stab:", msg)
		}); err != nil {
			return err
		}
	}
	// Ensure empty templates exist even when nothing was baked (minimal / build
	// tiers), so the user can fill them in themselves.
	if _, err := os.Stat(modelsPath); os.IsNotExist(err) {
		if err := os.WriteFile(modelsPath, []byte("{\n  \"providers\": {}\n}\n"), 0o644); err != nil {
			return err
		}
	}
	if _, err := os.Stat(secretsPath); os.IsNotExist(err) {
		if err := os.WriteFile(secretsPath, []byte("# add your API keys here, one per line: KEY=value\n"), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// mergeModelsFile merges baked into dst. Existing providers win on conflict
// unless force is set (baked replaces the whole file).
func mergeModelsFile(dst string, baked []byte, force bool) error {
	bakedProviders, err := parseProvidersMap(baked)
	if err != nil {
		return err
	}
	if len(bakedProviders) == 0 {
		return nil // nothing baked; never clobber or seed an empty file
	}
	if force {
		return writeProviders(dst, bakedProviders)
	}
	existing, err := loadProviders(dst)
	if err != nil {
		return err
	}
	for k, v := range bakedProviders {
		if _, ok := existing[k]; !ok {
			existing[k] = v
		}
	}
	return writeProviders(dst, existing)
}

// parseProvidersMap parses a models.json body into its providers map.
func parseProvidersMap(data []byte) (map[string]map[string]any, error) {
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

// mergeSecretsFile merges baked into dst, preserving the existing file's
// comments and ordering and appending only new keys. On a same-key collision
// with a different value the existing value stays and warn is called. force
// replaces the whole file with the baked keys.
func mergeSecretsFile(dst string, baked []byte, force bool, warn func(string)) error {
	bakedMap := parseEnvBytes(baked)
	if len(bakedMap) == 0 {
		return nil
	}
	if force {
		return writeEnvSorted(dst, bakedMap)
	}
	existingText, _ := os.ReadFile(dst)
	existing := parseEnvBytes(existingText)
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

// parseEnvBytes parses a KEY=value env-file body (comments, optional "export"
// prefix, optional quotes) into a map.
func parseEnvBytes(data []byte) map[string]string {
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
	return m
}

// writeEnvSorted writes an env file with sorted keys, mode 0600.
func writeEnvSorted(path string, m map[string]string) error {
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
