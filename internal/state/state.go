// Package state owns the stateless local-install manifest at
// ~/.stables/installs.json. It replaces the cross-host SQLite registry: stables
// remembers nothing about remote servers, but each local install records its
// own compose dir, ports, and containers so `stables list`, `ollama pull/list`,
// and `remove` can find them.
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Component records one local install (stab, ollama, webui).
type Component struct {
	Status     string            `json:"status"` // installed | removed
	ComposeDir string            `json:"compose_dir,omitempty"`
	OllamaURL  string            `json:"ollama_url,omitempty"`
	OllamaPort int               `json:"ollama_port,omitempty"`
	WebUIPort  int               `json:"webui_port,omitempty"`
	TTSPort    int               `json:"voice_port,omitempty"`
	BinaryPath string            `json:"binary_path,omitempty"`
	GPU        bool              `json:"gpu,omitempty"`
	Containers map[string]string `json:"containers,omitempty"`
	Network    string            `json:"network,omitempty"`
}

type Manifest struct {
	Installations map[string]Component `json:"installations"`
}

func Path(home string) string { return filepath.Join(home, ".stables", "installs.json") }

// Load returns the manifest, or an empty one when it does not exist.
func Load(home string) (Manifest, error) {
	m := Manifest{Installations: map[string]Component{}}
	data, err := os.ReadFile(Path(home))
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	if m.Installations == nil {
		m.Installations = map[string]Component{}
	}
	if migrateLegacyNames(&m) {
		_ = m.Save(home) // best-effort persist the one-time rename migration
	}
	return m, nil
}

// migrateLegacyNames renames legacy component names (the pre-rename
// "workhorse" and the intermediate "stb") to "stab" and removes the old keys.
// Reports whether the manifest changed.
func migrateLegacyNames(m *Manifest) bool {
	changed := false
	for _, legacy := range []string{"workhorse", "stb"} {
		c, ok := m.Installations[legacy]
		if !ok {
			continue
		}
		if _, exists := m.Installations["stab"]; !exists {
			m.Installations["stab"] = c
		}
		delete(m.Installations, legacy)
		changed = true
	}
	return changed
}

func (m Manifest) Save(home string) error {
	if err := os.MkdirAll(filepath.Dir(Path(home)), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(home), append(data, '\n'), 0o644)
}

// Installed reports whether a component is recorded as installed.
func (m Manifest) Installed(component string) bool {
	c, ok := m.Installations[component]
	return ok && c.Status == "installed"
}
