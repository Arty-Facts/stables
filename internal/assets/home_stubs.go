//go:build !embedsecrets

package assets

// HomeFile returns nil when `embedsecrets` is not set: no models.json or
// secrets.env are baked in (minimal and build-extensions tiers).
func HomeFile(name string) []byte { return nil }
