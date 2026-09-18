// Package secrets loads and resolves secret references without ever logging or
// persisting the values in plain text. Secrets live in a host-side env file
// (0600); model files reference them by name ($VAR / ${VAR}).
package secrets

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

// envRef matches $VAR and ${VAR} references.
var envRef = regexp.MustCompile(`\$\{?[A-Za-z_][A-Za-z0-9_]*\}?`)

// LoadEnvFile parses a KEY=value file (the host secrets file). Lines may use
// "#" comments and an optional "export " prefix. Values may be quoted.
func LoadEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	m := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
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
		val := strings.TrimSpace(line[eq+1:])
		val = strings.Trim(val, `"'`)
		if key != "" {
			m[key] = val
		}
	}
	return m, sc.Err()
}

// ExtractRefs returns the unique variable names referenced as $VAR / ${VAR}
// in s, in first-seen order.
func ExtractRefs(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range envRef.FindAllString(s, -1) {
		name := strings.TrimPrefix(m, "$")
		name = strings.TrimPrefix(name, "{")
		name = strings.TrimSuffix(name, "}")
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}
