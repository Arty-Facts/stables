package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ttscmp "github.com/stables/stables/internal/components/tts"
	"github.com/stables/stables/internal/state"
)

func TestPingSendsTheMessageThroughMCP(t *testing.T) {
	var got map[string]any
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("body is not JSON: %s", body)
		}
		w.Header().Set("content-type", "application/json")
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[`+
			`{"type":"text","text":"queued #7 (ping)"}],"isError":false}}`)
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	// The text is unquoted on purpose: refusing to send a message over quoting
	// would be a poor reason to fail.
	code := ttsPing(t.TempDir(),
		[]string{"the", "build", "is", "green", "--url", srv.URL + "/mcp"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr.String())
	}
	if path != "/mcp" {
		t.Errorf("path = %q, want /mcp", path)
	}
	if got["jsonrpc"] != "2.0" || got["method"] != "tools/call" {
		t.Errorf("envelope = %v", got)
	}
	params, _ := got["params"].(map[string]any)
	if params["name"] != "notify" {
		t.Errorf("tool = %v, want notify", params["name"])
	}
	arguments, _ := params["arguments"].(map[string]any)
	if arguments["text"] != "the build is green" {
		t.Errorf("text = %q", arguments["text"])
	}
	if arguments["kind"] != "ping" {
		t.Errorf("kind = %v, want ping by default", arguments["kind"])
	}
	// The source tells the user where it came from, so it is always set.
	if arguments["source"] == "" || arguments["source"] == nil {
		t.Error("source must be filled in")
	}
	if !strings.Contains(stdout.String(), "queued #7 (ping)") {
		t.Errorf("the receipt must be reported: %q", stdout.String())
	}
}

func TestPingCarriesKindAndSource(t *testing.T) {
	var arguments map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &body)
		params, _ := body["params"].(map[string]any)
		arguments, _ = params["arguments"].(map[string]any)
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"queued #1 (question)"}]}}`)
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	code := ttsPing(t.TempDir(), []string{"needs", "a", "decision",
		"--kind", "question", "--source", "ci", "--url", srv.URL}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr.String())
	}
	if arguments["kind"] != "question" || arguments["source"] != "ci" {
		t.Errorf("arguments = %v", arguments)
	}
}

func TestPingRefusesBadInputWithoutCallingTheServer(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"nothing to say", []string{"--url", srv.URL}},
		{"blank text", []string{"   ", "--url", srv.URL}},
		{"unknown kind", []string{"hello", "--kind", "shout", "--url", srv.URL}},
		{"unknown flag", []string{"hello", "--loud", "--url", srv.URL}},
		{"missing value", []string{"hello", "--kind"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := ttsPing(t.TempDir(), tc.args, &stdout, &stderr); code != 2 {
				t.Errorf("exit %d, want 2 (usage)", code)
			}
			if !strings.Contains(stderr.String(), "usage:") {
				t.Errorf("a usage failure must show the usage: %q", stderr.String())
			}
		})
	}
	if called {
		t.Error("bad input must not reach the server")
	}
}

func TestPingSaysWhenNothingIsListening(t *testing.T) {
	// Port 1 is reserved and nothing listens there, which is the state a user hits
	// when the container is down.
	var stdout, stderr bytes.Buffer
	code := ttsPing(t.TempDir(), []string{"hello", "--url", "http://127.0.0.1:1/mcp"},
		&stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	// The failure must name the fix, not just the symptom.
	if !strings.Contains(stderr.String(), "stables install tts") {
		t.Errorf("stderr should point at the fix: %q", stderr.String())
	}
}

func TestPingReportsAToolLevelFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A tool failure rides inside a successful JSON-RPC result, so only the
		// isError flag distinguishes it.
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[`+
			`{"type":"text","text":"notify needs a non-empty 'text'"}],"isError":true}}`)
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	if code := ttsPing(t.TempDir(), []string{"hello", "--url", srv.URL}, &stdout, &stderr); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "notify needs a non-empty") {
		t.Errorf("the server's reason must be shown: %q", stderr.String())
	}
}

func TestPingReportsANonJSONAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "<html>nginx</html>")
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	if code := ttsPing(t.TempDir(), []string{"hello", "--url", srv.URL}, &stdout, &stderr); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "502") {
		t.Errorf("the status must be shown: %q", stderr.String())
	}
}

func TestPingHelpIsNotAFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := ttsPing(t.TempDir(), []string{"--help"}, &stdout, &stderr); code != 0 {
		t.Errorf("exit %d, want 0 for --help", code)
	}
	if !strings.Contains(stderr.String(), "tts-mcp-ping") {
		t.Errorf("help should describe the command: %q", stderr.String())
	}
}

func TestPingUsesTheInstalledPort(t *testing.T) {
	home := t.TempDir()
	// A default install needs no flags, so the installed port has to be found.
	m := state.Manifest{Installations: map[string]state.Component{
		"tts": {TTSPort: 18000},
	}}
	if err := m.Save(home); err != nil {
		t.Fatal(err)
	}
	if got := installedTTSPort(home); got != 18000 {
		t.Errorf("installed port = %d, want 18000", got)
	}
	// And with nothing installed, the component's default, so the error message
	// names the port the user would have.
	empty := t.TempDir()
	if got, want := installedTTSPort(empty), ttscmp.DefaultData(empty).Port; got != want {
		t.Errorf("default port = %d, want %d", got, want)
	}
}
