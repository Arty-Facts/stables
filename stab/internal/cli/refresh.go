package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type ollamaTags struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

type tunnelState struct {
	Mode   string `json:"mode"`
	Target string `json:"target"`
	Port   int    `json:"port"`
	PID    int    `json:"pid"`
}

// queryOllamaTags is a package var so tests can substitute a fake. It returns
// the model tag names served by an Ollama on 127.0.0.1:<port>, or nil when no
// Ollama is reachable (connection refused, timeout, empty list).
var queryOllamaTags = func(port int) []string {
	url := "http://127.0.0.1:" + strconv.Itoa(port) + "/api/tags"
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	var tags ollamaTags
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil
	}
	out := make([]string, 0, len(tags.Models))
	for _, m := range tags.Models {
		out = append(out, m.Name)
	}
	return out
}

// refreshTunnelModels rewrites only the stables-tunnel-* providers in
// ~/.stables/models.json from the Ollama reachable on the local inference
// port. Every other provider is preserved (harness logins, company providers).
// It never fails because Ollama is down: unreachable Ollama just removes the
// stables-tunnel-* keys.
func (a *App) refreshTunnelModels() error {
	port := 11434
	host := hostname()
	if s, err := readTunnelState(a.Home); err == nil && s != nil {
		if s.Port != 0 {
			port = s.Port
		}
		if s.Mode == "remote" && s.Target != "" {
			host = s.Target
		}
	}

	models := queryOllamaTags(port)
	path := filepath.Join(a.Config.StateDirPath(a.Home), "models.json")

	doc, err := loadProviders(path)
	if err != nil {
		return err
	}
	removed := false
	for k := range doc {
		if strings.HasPrefix(k, "stables-tunnel-") {
			delete(doc, k)
			removed = true
		}
	}
	if len(models) > 0 {
		doc["stables-tunnel-"+sanitizeHost(host)] = map[string]any{
			"baseUrl": "http://host.docker.internal:" + strconv.Itoa(port) + "/v1",
			"api":     "openai-completions",
			// Literal dummy key: Ollama needs no auth, and Pi rejects an empty
			// apiKey. Using a $VAR would expand to "" on hosts without
			// OLLAMA_API_KEY in secrets.env.
			"apiKey": "ollama",
			"compat": map[string]any{
				"supportsDeveloperRole":    false,
				"supportsReasoningEffort":  false,
				"supportsUsageInStreaming": false,
				"supportsStrictMode":       false,
			},
			"models": modelEntries(models),
		}
	}
	// Nothing to change: no ollama models and no stale tunnel keys. Do not
	// create or rewrite models.json (stab must never seed one).
	if len(models) == 0 && !removed {
		return nil
	}
	return writeProviders(path, doc)
}

func readTunnelState(home string) (*tunnelState, error) {
	data, err := os.ReadFile(filepath.Join(home, ".stables", "tunnels", "current"))
	if err != nil {
		return nil, err
	}
	var s tunnelState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func modelEntries(models []string) []map[string]any {
	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		out = append(out, map[string]any{"id": m, "name": m, "contextWindow": 131072, "maxTokens": 16384})
	}
	return out
}

// loadProviders reads models.json and returns its providers map.
func loadProviders(path string) (map[string]map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]map[string]any{}, nil
		}
		return nil, err
	}
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

func writeProviders(path string, doc map[string]map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(map[string]any{"providers": doc}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func hostname() string {
	h, _ := os.Hostname()
	if h == "" {
		return "local"
	}
	return h
}

func sanitizeHost(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "local"
	}
	return out
}
