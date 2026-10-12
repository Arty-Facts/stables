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
	// The main window runs the harness through the failure guard, so a harness
	// that cannot start reports itself instead of deleting the window.
	if !strings.Contains(joined, "-n main "+HarnessWindowCommand("pi")) {
		t.Errorf("expected main window running the guarded harness: %q", joined)
	}
	if !strings.Contains(joined, "-n shell") || !strings.Contains(joined, "-n logs") || !strings.Contains(joined, "-n help") {
		t.Errorf("expected shell, logs, and help windows: %q", joined)
	}
	if !strings.Contains(joined, "-c /workspace") {
		t.Errorf("workdir missing: %q", joined)
	}
}

func TestHarnessWindowCommandKeepsWindowOnFailure(t *testing.T) {
	cmd := HarnessWindowCommand("pi")
	for _, want := range []string{
		"pi;",                  // runs the harness
		"__stab_harness_rc=$?", // captures its status before anything else
		`[ "$__stab_harness_rc" -ne 0 ]`,
		"[stab] harness failed",
		"exec bash -l", // the window stays alive with a shell
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("harness guard missing %q in %q", want, cmd)
		}
	}
	if strings.HasPrefix(cmd, "pi ||") {
		t.Errorf("guard must not depend on `||` alone: %q", cmd)
	}
}

func TestHarnessCommandDefaultsToPi(t *testing.T) {
	if HarnessCommand("") != "pi" {
		t.Errorf("empty harness should default to pi, got %q", HarnessCommand(""))
	}
	if HarnessCommand("myagent") != "myagent" {
		t.Errorf("configured harness must be preserved")
	}
	// The window and any probe must agree on what runs.
	if !strings.HasPrefix(HarnessWindowCommand(""), "pi;") {
		t.Errorf("defaulted window command should run pi: %q", HarnessWindowCommand(""))
	}
}

func TestHarnessBinary(t *testing.T) {
	tests := map[string]string{
		"":                 "pi",
		"pi":               "pi",
		"pi --resume":      "pi",
		"./run.sh --watch": "./run.sh",
	}
	for in, want := range tests {
		if got := HarnessBinary(in); got != want {
			t.Errorf("HarnessBinary(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestListWindows(t *testing.T) {
	argv := ListWindows("stab")
	if strings.Join(argv, " ") != `tmux list-windows -t stab -F #{window_name}` {
		t.Errorf("unexpected list-windows: %v", argv)
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
