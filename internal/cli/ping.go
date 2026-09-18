package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	ttscmp "github.com/stables/stables/internal/components/tts"
	"github.com/stables/stables/internal/state"
)

// cmdTTSPing leaves a message for the user through the running voice server.
//
// The queue exists so background work can tell the user something when it has no
// way to speak and no idea whether anyone is listening. This is the shell side of
// that: it produces the same message an agent produces, so a ping is also a test of
// the path an agent will use.
//
// It speaks the MCP endpoint deliberately rather than the REST one. Both reach the
// same queue, and MCP is what a client will actually call, so a ping exercises the
// real surface instead of a parallel one that could rot unnoticed.
func cmdTTSPing(home string, args []string) int {
	return ttsPing(home, args, os.Stdout, os.Stderr)
}

const pingUsage = `usage:
  stables tts-mcp-ping "text" [--kind ping|say|summary|question] [--source NAME]
                             [--url http://host:port]

Leaves a message that the voice client reads aloud the next time you are idle.
It goes through the server's MCP endpoint, which is the same path an agent uses.`

// pingKinds are the kinds the backend accepts, from the queue's own KINDS.
var pingKinds = []string{"say", "summary", "question", "ping"}

type pingArgs struct {
	text   string
	kind   string
	source string
	// url empty means: the installed server, on its installed port.
	url string
}

// parsePingArgs reads the command line. The text is positional and may be quoted
// or not: an unquoted sentence is joined, because refusing to send a message over
// quoting is a poor reason to fail.
func parsePingArgs(args []string, hostname string) (pingArgs, error) {
	parsed := pingArgs{kind: "ping", source: hostname}
	if parsed.source == "" {
		parsed.source = "cli"
	}

	var words []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			words = append(words, arg)
			continue
		}
		name, value, inline := strings.Cut(arg, "=")
		take := func() (string, error) {
			if inline {
				return value, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", name)
			}
			i++
			return args[i], nil
		}
		var err error
		switch name {
		case "--kind":
			parsed.kind, err = take()
		case "--source":
			parsed.source, err = take()
		case "--url":
			parsed.url, err = take()
		case "--help", "-h":
			return parsed, errPingUsage
		default:
			return parsed, fmt.Errorf("unknown flag %s", name)
		}
		if err != nil {
			return parsed, err
		}
	}

	parsed.text = strings.TrimSpace(strings.Join(words, " "))
	if parsed.text == "" {
		return parsed, fmt.Errorf("nothing to say: give the text to queue")
	}
	if !slicesContains(pingKinds, parsed.kind) {
		return parsed, fmt.Errorf("unknown kind %q; want one of %s", parsed.kind, strings.Join(pingKinds, ", "))
	}
	return parsed, nil
}

// errPingUsage is returned when the user asked for help, which is not a failure
// worth reporting as one.
var errPingUsage = fmt.Errorf("help")

func ttsPing(home string, args []string, stdout, stderr io.Writer) int {
	hostname, _ := os.Hostname()
	parsed, err := parsePingArgs(args, hostname)
	if err != nil {
		if err != errPingUsage {
			fmt.Fprintf(stderr, "stables: %v\n\n", err)
		}
		fmt.Fprintln(stderr, pingUsage)
		if err == errPingUsage {
			return 0
		}
		return 2
	}

	url := parsed.url
	if url == "" {
		url = fmt.Sprintf("http://127.0.0.1:%d/mcp", installedTTSPort(home))
	}

	reply, err := pingQueue(url, parsed, &http.Client{Timeout: 10 * time.Second})
	if err != nil {
		fmt.Fprintf(stderr, "stables: %v\n", err)
		if isConnectionRefused(err) {
			fmt.Fprintf(stderr,
				"  nothing is answering there — start the server with: stables install tts\n")
		}
		return 1
	}
	fmt.Fprintf(stdout, "%s — the voice client reads it aloud when you are idle\n", reply)
	return 0
}

// installedTTSPort is where the voice server was installed, falling back to the
// component's default: a ping should work on a default install without flags, and
// still be told the truth when nothing is installed.
func installedTTSPort(home string) int {
	if m, err := state.Load(home); err == nil {
		if component, ok := m.Installations["tts"]; ok && component.TTSPort != 0 {
			return component.TTSPort
		}
	}
	return ttscmp.DefaultData(home).Port
}

type rpcReply struct {
	Result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// pingQueue performs the JSON-RPC call and returns the server's receipt.
func pingQueue(url string, args pingArgs, client *http.Client) (string, error) {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "notify",
			"arguments": map[string]any{
				"text":   args.text,
				"kind":   args.kind,
				"source": args.source,
			},
		},
	})
	if err != nil {
		return "", err
	}

	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Bounded: a server that answers with a megabyte of error should not become the
	// command's memory problem.
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("server answered %s: %s", resp.Status, truncate(string(data), 200))
	}

	var reply rpcReply
	if err := json.Unmarshal(data, &reply); err != nil {
		return "", fmt.Errorf("server sent something that is not JSON-RPC: %s", truncate(string(data), 200))
	}
	if reply.Error != nil {
		return "", fmt.Errorf("the server rejected the message: %s", reply.Error.Message)
	}
	if len(reply.Result.Content) == 0 {
		return "", fmt.Errorf("the server answered without a result")
	}
	if reply.Result.IsError {
		// A tool-level failure is reported inside a successful JSON-RPC result.
		return "", fmt.Errorf("%s", reply.Result.Content[0].Text)
	}
	return reply.Result.Content[0].Text, nil
}

func isConnectionRefused(err error) bool {
	return strings.Contains(err.Error(), "connection refused") ||
		strings.Contains(err.Error(), "no such host")
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func slicesContains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}
