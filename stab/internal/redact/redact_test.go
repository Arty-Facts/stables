package redact

import "testing"

func TestRedactKeyValue(t *testing.T) {
	in := `api_key=EXAMPLEKEY and password=EXAMPLESECRET token:"xyz"`
	out := Redact(in)
	for _, leak := range []string{"sk-abc123", "supersecret", "xyz"} {
		if contains(out, leak) {
			t.Errorf("secret %q leaked in %q", leak, out)
		}
	}
}

func TestRedactBearer(t *testing.T) {
	out := Redact("Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.abc")
	if contains(out, "eyJhbGciOiJIUzI1NiJ9.abc") {
		t.Errorf("bearer token leaked: %q", out)
	}
}

func TestRedactExplicitSecrets(t *testing.T) {
	out := Redact("hello world", "world")
	if contains(out, "world") {
		t.Errorf("explicit secret leaked: %q", out)
	}
}

func TestRedactShortSecretsSkipped(t *testing.T) {
	// Secrets shorter than 4 chars are left alone to avoid mangling everything.
	out := Redact("a b c", "b")
	if !contains(out, "b") {
		t.Errorf("short secret should be skipped")
	}
}

func TestRedactSlice(t *testing.T) {
	got := RedactSlice([]string{"token=secret1", "plain"}, "secret1")
	if contains(got[0], "secret1") {
		t.Errorf("slice element not redacted: %q", got[0])
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
