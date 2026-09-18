package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCheckModelsSecretsMissingSecretsFile(t *testing.T) {
	dir := t.TempDir()
	models := filepath.Join(dir, "models.json")
	sec := filepath.Join(dir, "secrets.env")
	writeFile(t, models, `{"providers":{"a":{"apiKey":"$REMOTE_API_KEY"},"b":{"apiKey":"${LOCAL_KEY}"}}}`)

	chk, err := checkModelsSecrets(models, sec)
	if err != nil {
		t.Fatal(err)
	}
	if chk.OK() {
		t.Fatal("expected missing secrets to be reported")
	}
	if strings.Join(chk.Missing, ",") != "REMOTE_API_KEY,LOCAL_KEY" {
		t.Fatalf("unexpected missing refs: %v", chk.Missing)
	}
}

func TestCheckModelsSecretsAllPresent(t *testing.T) {
	dir := t.TempDir()
	models := filepath.Join(dir, "models.json")
	sec := filepath.Join(dir, "secrets.env")
	writeFile(t, models, `{"providers":{"a":{"apiKey":"$REMOTE_API_KEY"}}}`)
	writeFile(t, sec, "REMOTE_API_KEY=value-1\n")

	chk, err := checkModelsSecrets(models, sec)
	if err != nil {
		t.Fatal(err)
	}
	if !chk.OK() {
		t.Fatalf("expected all secrets present, missing=%v", chk.Missing)
	}
	if len(chk.Refs) != 1 {
		t.Fatalf("expected 1 ref, got %v", chk.Refs)
	}
}

func TestCheckModelsSecretsEmptyValueCountsAsMissing(t *testing.T) {
	dir := t.TempDir()
	models := filepath.Join(dir, "models.json")
	sec := filepath.Join(dir, "secrets.env")
	writeFile(t, models, `{"providers":{"a":{"apiKey":"$REMOTE_API_KEY"}}}`)
	writeFile(t, sec, "REMOTE_API_KEY=\n")

	chk, err := checkModelsSecrets(models, sec)
	if err != nil {
		t.Fatal(err)
	}
	if chk.OK() {
		t.Fatal("empty secret value must count as missing")
	}
}

func TestCheckModelsSecretsNoModelsIsOK(t *testing.T) {
	dir := t.TempDir()
	chk, err := checkModelsSecrets(filepath.Join(dir, "models.json"), filepath.Join(dir, "secrets.env"))
	if err != nil {
		t.Fatal(err)
	}
	if !chk.OK() || len(chk.Refs) != 0 {
		t.Fatalf("missing models.json should be an empty OK check: %+v", chk)
	}
}

func TestCheckModelsSecretsDedupesRefs(t *testing.T) {
	dir := t.TempDir()
	models := filepath.Join(dir, "models.json")
	sec := filepath.Join(dir, "secrets.env")
	writeFile(t, models, `{"providers":{"a":{"apiKey":"$K"},"b":{"apiKey":"${K}"}}}`)

	chk, err := checkModelsSecrets(models, sec)
	if err != nil {
		t.Fatal(err)
	}
	if len(chk.Missing) != 1 {
		t.Fatalf("expected one deduped missing ref, got %v", chk.Missing)
	}
}
