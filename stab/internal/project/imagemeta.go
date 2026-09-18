// Package-local metadata for runtime images: which embedded template version
// each locally built image was produced from. Enables automatic rebuilds when
// a stab update ships new runtime files while a stale image is still
// cached.
package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ImageMetaPath is the state-root file mapping image tag -> template version.
func ImageMetaPath(stateRoot string) string {
	return filepath.Join(stateRoot, "images.json")
}

// readImageMeta loads the tag->template-version map (empty when absent).
func readImageMeta(stateRoot string) map[string]string {
	m := map[string]string{}
	data, err := os.ReadFile(ImageMetaPath(stateRoot))
	if err != nil {
		return m
	}
	_ = json.Unmarshal(data, &m)
	return m
}

// ImageTemplateVersion returns the template version an image was built with,
// and whether that is known. Unknown images are treated conservatively.
func ImageTemplateVersion(stateRoot, image string) (string, bool) {
	v, ok := readImageMeta(stateRoot)[image]
	return v, ok
}

// RecordImageTemplate records that image was (re)built with templateVersion.
func RecordImageTemplate(stateRoot, image, templateVersion string) error {
	if templateVersion == "" {
		return nil
	}
	m := readImageMeta(stateRoot)
	m[image] = templateVersion
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode image meta: %w", err)
	}
	return os.WriteFile(ImageMetaPath(stateRoot), append(b, '\n'), 0o600)
}
