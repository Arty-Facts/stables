// Package runtime embeds the default Docker image files so stab can seed and
// build project workspaces without a source checkout.
package runtime

import (
	_ "embed"
	"strconv"
)

// TemplateVersion is bumped whenever the embedded default files change, so
// existing projects are re-seeded with the new defaults.
const TemplateVersion = 7

// TemplateVersionString returns the template version as a string.
func TemplateVersionString() string { return strconv.Itoa(TemplateVersion) }

//go:embed Dockerfile
var dockerfile string

//go:embed Dockerfile.gpu
var dockerfileGPU string

//go:embed entrypoint.sh
var entrypoint string

//go:embed tmux.conf
var tmuxConf string

// Dockerfile returns the embedded runtime Dockerfile.
func Dockerfile() string { return dockerfile }

// DockerfileGPU returns the embedded GPU runtime Dockerfile.
func DockerfileGPU() string { return dockerfileGPU }

// Entrypoint returns the embedded container entrypoint script.
func Entrypoint() string { return entrypoint }

// TmuxConf returns the embedded tmux configuration.
func TmuxConf() string { return tmuxConf }
