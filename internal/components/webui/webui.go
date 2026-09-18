// Package webui installs Open WebUI as its own stack, pointed at any Ollama
// endpoint. It is decoupled from the ollama component so Ollama can run remote
// while WebUI runs local (both converge on the local inference port).
package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/stables/stables/internal/assets"
	"github.com/stables/stables/internal/state"
)

type Options struct {
	Home          string
	OllamaURL     string
	WebUIPort     int
	Listen        string
	Name          string
	NetworkSubnet string
}

type data struct {
	OllamaURL     string
	WebUIName     string
	WebUIPort     int
	ListenHost    string
	Name          string
	NetworkSubnet string
}

func defaults(opts Options) data {
	d := data{
		OllamaURL:  "http://host.docker.internal:11434",
		WebUIName:  "Stable LLM",
		WebUIPort:  3000,
		ListenHost: "127.0.0.1",
		Name:       "stables",
	}
	if opts.OllamaURL != "" {
		d.OllamaURL = opts.OllamaURL
	}
	if opts.WebUIPort != 0 {
		d.WebUIPort = opts.WebUIPort
	}
	if opts.Listen != "" {
		d.ListenHost = opts.Listen
	}
	if opts.Name != "" {
		d.Name = opts.Name
	}
	d.NetworkSubnet = opts.NetworkSubnet
	return d
}

// Render produces the compose file for the given data.
func Render(d data) ([]byte, error) {
	t, err := template.New("webui").Parse(string(assets.MustRead("webui/docker-compose.yml.tmpl")))
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, d); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Install renders the webui compose stack, starts it, and records the install
// in the local manifest.
func Install(ctx context.Context, opts Options) error {
	d := defaults(opts)
	if d.NetworkSubnet != "" {
		if _, _, err := net.ParseCIDR(d.NetworkSubnet); err != nil {
			return fmt.Errorf("invalid network subnet %q", d.NetworkSubnet)
		}
	}
	base := filepath.Join(opts.Home, ".stables", "webui")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}
	compose, err := Render(d)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(base, "docker-compose.yml"), compose, 0o644); err != nil {
		return err
	}
	// Populate OpenAI connections from models.json before the first up, so a
	// fresh install already lists every known endpoint (same logic as fetch).
	ends, err := writeConnectionsEnv(opts.Home, base)
	if err != nil {
		return err
	}
	if err := run(ctx, base, "up", "-d"); err != nil {
		return err
	}
	fmt.Printf("stables: Open WebUI: http://localhost:%d  (Ollama: %s)\n", d.WebUIPort, d.OllamaURL)
	fmt.Printf("stables: webui connections: %d (from ~/.stables/models.json)\n", len(ends))
	m, err := state.Load(opts.Home)
	if err != nil {
		return err
	}
	m.Installations["webui"] = state.Component{
		Status:     "installed",
		ComposeDir: base,
		OllamaURL:  d.OllamaURL,
		WebUIPort:  d.WebUIPort,
		Containers: map[string]string{"open-webui": d.Name + "-open-webui"},
		Network:    d.Name + "-webui",
	}
	return m.Save(opts.Home)
}

// Remove tears down the webui stack. soft (hard=false) keeps the data volume
// (chat logs, DB) with `down`; hard=true adds `-v` to wipe it. The volume must
// go on a hard remove: Open WebUI seeds its OpenAI connections from
// OPENAI_API_BASE_URLS into the DB only on first run, so a stale volume would
// keep serving old (empty) connections forever.
func Remove(ctx context.Context, dir string, hard bool) error {
	args := []string{"down"}
	if hard {
		args = append(args, "-v")
	}
	return run(ctx, dir, args...)
}

func run(ctx context.Context, dir string, args ...string) error {
	bin := "docker"
	full := append([]string{"compose"}, args...)
	if _, err := exec.LookPath("docker"); err != nil {
		bin = "docker-compose"
		full = args
	}
	cmd := exec.CommandContext(ctx, bin, full...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// endpoint is one OpenAI-compatible connection to expose to Open WebUI.
type endpoint struct {
	BaseURL string
	Key     string
}

// Fetch refreshes the WebUI model list: it reads ~/.stables/models.json,
// extracts every non-tunnel provider endpoint (resolving $VAR api keys from
// ~/.stables/secrets.env), writes them into the compose .env as Open WebUI
// OpenAI connections, and restarts the container so the new connections and
// models are picked up.
func Fetch(ctx context.Context, home, composeDir string) error {
	ends, err := writeConnectionsEnv(home, composeDir)
	if err != nil {
		return err
	}
	fmt.Printf("stables: webui connections: %d (from ~/.stables/models.json)\n", len(ends))
	for _, e := range ends {
		fmt.Printf("  %s\n", e.BaseURL)
	}
	// `up -d` recreates the container when its env changed (restart would keep
	// the old env, so the new connections would never be applied).
	if err := run(ctx, composeDir, "up", "-d", "open-webui"); err != nil {
		return err
	}
	fmt.Println("stables: webui models refreshed")
	return nil
}

// writeConnectionsEnv extracts non-tunnel providers from models.json and writes
// them to the compose .env as Open WebUI OpenAI connections.
func writeConnectionsEnv(home, composeDir string) ([]endpoint, error) {
	ends, err := fetchEndpoints(home)
	if err != nil {
		return nil, err
	}
	baseURLs := make([]string, 0, len(ends))
	keys := make([]string, 0, len(ends))
	for _, e := range ends {
		baseURLs = append(baseURLs, e.BaseURL)
		keys = append(keys, e.Key)
	}
	env := fmt.Sprintf("OPENAI_API_BASE_URLS=%s\nOPENAI_API_KEYS=%s\n",
		strings.Join(baseURLs, ";"), strings.Join(keys, ";"))
	if err := os.WriteFile(filepath.Join(composeDir, ".env"), []byte(env), 0o600); err != nil {
		return nil, err
	}
	return ends, nil
}

// fetchEndpoints reads models.json and returns the non-tunnel provider
// endpoints with resolved api keys.
func fetchEndpoints(home string) ([]endpoint, error) {
	data, err := os.ReadFile(filepath.Join(home, ".stables", "models.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var doc struct {
		Providers map[string]map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse models.json: %w", err)
	}
	secrets := loadEnvFile(filepath.Join(home, ".stables", "secrets.env"))

	var out []endpoint
	for name, p := range doc.Providers {
		// The tunnel provider duplicates the OLLAMA_BASE_URL connection; skip it.
		if strings.HasPrefix(name, "stables-tunnel-") {
			continue
		}
		base, _ := p["baseUrl"].(string)
		if base == "" {
			continue
		}
		key := resolveKey(p["apiKey"], secrets)
		if key == "" {
			continue
		}
		out = append(out, endpoint{BaseURL: base, Key: key})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BaseURL < out[j].BaseURL })
	return out, nil
}

// resolveKey turns a models.json apiKey value into a literal key, expanding
// $VAR references against secrets.
func resolveKey(v any, secrets map[string]string) string {
	s, _ := v.(string)
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "$") {
		name := strings.TrimPrefix(s, "$")
		name = strings.TrimPrefix(name, "{")
		name = strings.TrimSuffix(name, "}")
		return secrets[name]
	}
	return s
}

// loadEnvFile parses KEY=value content (comments and "export " allowed).
func loadEnvFile(path string) map[string]string {
	m := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return m
	}
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
		k := strings.TrimSpace(line[:eq])
		v := strings.TrimSpace(line[eq+1:])
		v = strings.Trim(v, `"'`)
		if k != "" {
			m[k] = v
		}
	}
	return m
}
