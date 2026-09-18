// Package redact removes secret material from strings before they are logged
// or shipped across a process boundary. It is intentionally conservative and
// operates on explicit secret values plus a small set of well-known patterns.
package redact

import (
	"regexp"
	"strings"
)

var keyValuePattern = regexp.MustCompile(`(?i)\b(api[_-]?key|token|secret|password|passwd|authorization|credential)\b\s*[:=]\s*"?[^"\s,;]+"?`)

var bearerPattern = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`)

const mask = "[REDACTED]"

// Redact scrubs a string, replacing known secret patterns and any explicit
// secret values it contains.
func Redact(s string, secrets ...string) string {
	// Bearer tokens are scrubbed first so the generic key=value rule cannot
	// consume the "Bearer" keyword before the token is removed.
	out := bearerPattern.ReplaceAllString(s, "${1}"+mask)
	out = keyValuePattern.ReplaceAllStringFunc(out, func(m string) string {
		return mask
	})
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if len(secret) < 4 {
			continue
		}
		out = strings.ReplaceAll(out, secret, mask)
	}
	return out
}

// String reports whether any redaction was applied and returns the scrubbed
// value.
func String(s string, secrets ...string) (string, bool) {
	out := Redact(s, secrets...)
	return out, out != s
}

// RedactSlice scrubs every element of a string slice in place.
func RedactSlice(ss []string, secrets ...string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = Redact(s, secrets...)
	}
	return out
}
