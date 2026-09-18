// Package version holds the stab build version. It is overridable at build
// time via -ldflags "-X github.com/stables/stab/internal/version.Version=...".
package version

// Version is the semantic version of this build.
var Version = "0.2.0"

// String returns the version.
func String() string { return Version }
