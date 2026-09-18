package tmux

import (
	"strings"
	"testing"
)

func TestNewSessionLayout(t *testing.T) {
	argv := NewSession("stab", DefaultLayout("pi", "/workspace/.stables"), "/workspace")
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "new-session -d -s stab") {
		t.Errorf("session creation missing: %q", joined)
	}
	if !strings.Contains(joined, "-n main pi") {
		t.Errorf("expected main window running the harness: %q", joined)
	}
	if !strings.Contains(joined, "-n shell") || !strings.Contains(joined, "-n logs") || !strings.Contains(joined, "-n help") {
		t.Errorf("expected shell, logs, and help windows: %q", joined)
	}
	if !strings.Contains(joined, "-c /workspace") {
		t.Errorf("workdir missing: %q", joined)
	}
}

func TestAttachWindow(t *testing.T) {
	argv := AttachWindow("stab", "shell")
	joined := strings.Join(argv, " ")
	if joined != "tmux attach-session -t stab:shell" {
		t.Errorf("unexpected attach: %q", joined)
	}
}

func TestHasSession(t *testing.T) {
	argv := HasSession("stab")
	if strings.Join(argv, " ") != "tmux has-session -t stab" {
		t.Errorf("unexpected has-session: %v", argv)
	}
}

func TestHelpWindowReadsConfigComments(t *testing.T) {
	cmd := HelpWindowCommand("/workspace/.stables")
	if !strings.Contains(cmd, "grep '^#'") || !strings.Contains(cmd, "tmux.conf") || !strings.Contains(cmd, "/workspace/.stables/tmux.conf") {
		t.Errorf("help window should read the comment block from tmux.conf: %q", cmd)
	}
}

func TestAttachGroupedScript(t *testing.T) {
	script := AttachGroupedScript("stab", "shell")
	for _, want := range []string{
		"tmux new-session -d -s \"$S\" -t 'stab'",
		"tmux select-window -t \"$S:shell\"",
		"tmux attach-session -t \"$S\"",
		"tmux kill-session -t \"$S\"",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("grouped attach script missing %q in %q", want, script)
		}
	}
}

func TestAttachGroupedScriptIgnoresUnsafeWindow(t *testing.T) {
	script := AttachGroupedScript("stab", "shell; rm -rf /")
	if strings.Contains(script, "rm -rf") {
		t.Fatalf("unsafe window name leaked into script: %q", script)
	}
}
