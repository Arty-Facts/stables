//go:build !embedassets

package assets

// PiAgentFile returns nil in the minimal build (no `embedassets` tag): no
// personal skills, extensions, models.json, or secrets.env are baked in.
func PiAgentFile(name string) []byte { return nil }
