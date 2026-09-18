// Package embedded carries opaque deployment configuration baked into the
// binary at build time. stab is provider-agnostic: it does not know or
// care what providers, models, or vendors are described — it only ships two
// opaque blobs (a models.json and a secrets.env) that the top-level build
// injects via -ldflags.
package embedded

import "encoding/base64"

// modelsJSONB64 is the base64-encoded models.json content injected at build
// time with:
//
//	-X github.com/stables/stab/internal/embedded.modelsJSONB64=<base64>
var modelsJSONB64 string

// secretsB64 is the base64-encoded secrets.env content injected at build time.
var secretsB64 string

const defaultModels = "{\n  \"providers\": {}\n}\n"

// ModelsJSON returns the embedded models.json content, or a generic empty
// providers document when none was injected.
func ModelsJSON() []byte {
	if modelsJSONB64 == "" {
		return []byte(defaultModels)
	}
	data, err := base64.StdEncoding.DecodeString(modelsJSONB64)
	if err != nil {
		return []byte(defaultModels)
	}
	return data
}

// SecretsEnv returns the embedded secrets.env content, or nil when none was
// injected.
func SecretsEnv() []byte {
	if secretsB64 == "" {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(secretsB64)
	if err != nil {
		return nil
	}
	return data
}
