package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/stables/stab/internal/secrets"
)

// SecretCheck is the result of validating one models.json against the host
// secrets.env. A models.json may reference $VAR / ${VAR} keys that the
// controller injects as environment variables at container start; a reference
// with no value expands to the empty string, and Pi rejects the WHOLE
// models.json when any provider has "apiKey": "" (schema minLength 1). The
// container entrypoint therefore skips providers with empty apiKeys — this
// check is the host-side hint that tells the user which keys to add.
type SecretCheck struct {
	ModelsPath  string
	SecretsPath string
	Refs        []string // every $VAR referenced by models.json
	Missing     []string // refs with no value in secrets.env
}

// OK reports whether every referenced secret has a value.
func (c SecretCheck) OK() bool { return len(c.Missing) == 0 }

// checkModelsSecrets validates modelsPath against secretsPath. A missing
// models.json yields an empty, OK result (nothing to validate).
func checkModelsSecrets(modelsPath, secretsPath string) (SecretCheck, error) {
	res := SecretCheck{ModelsPath: modelsPath, SecretsPath: secretsPath}
	data, err := os.ReadFile(modelsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return res, nil
		}
		return res, err
	}
	res.Refs = secrets.ExtractRefs(string(data))
	if len(res.Refs) == 0 {
		return res, nil
	}
	sec, err := secrets.LoadEnvFile(secretsPath)
	if err != nil {
		if os.IsNotExist(err) {
			res.Missing = append(res.Missing, res.Refs...)
			return res, nil
		}
		return res, err
	}
	seen := map[string]bool{}
	for _, ref := range res.Refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		if v, ok := sec[ref]; !ok || v == "" {
			res.Missing = append(res.Missing, ref)
		}
	}
	return res, nil
}

// report prints an actionable hint. The container skips these providers, so
// this warns rather than fails: the agent still runs with the models whose
// secrets are present.
func (c SecretCheck) report(w io.Writer, fix string) {
	if c.OK() {
		return
	}
	fmt.Fprintf(w, "stab: warning: %s references %d secret(s) missing from %s: %s\n",
		c.ModelsPath, len(c.Missing), c.SecretsPath, strings.Join(c.Missing, ", "))
	fmt.Fprintln(w, "stab:   those providers are skipped in the container (Pi rejects an empty apiKey)")
	if fix != "" {
		fmt.Fprintf(w, "stab:   fix: %s\n", fix)
	}
}

// fixHint names the two ways to resolve a missing-secret warning for one
// models.json.
func fixHint(modelsPath, secretsPath string) string {
	return "add the keys to " + secretsPath + ", or remove those providers from " + modelsPath
}
