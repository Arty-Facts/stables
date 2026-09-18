// Package tmux builds tmux command lines and the session configuration for the
// tmux-first local design. It only constructs argv and config text; execution
// happens inside the Docker container via `docker exec`.
package tmux

import (
	"fmt"
	"strings"
)

// Window names in the stable session layout.
const (
	WindowMain      = "main"      // the harness (Pi) coordinator window
	WindowMessenger = "messenger" // reserved for a future messenger model
	WindowShell     = "shell"     // operator shell
	WindowLogs      = "logs"      // runtime and event logs
	WindowHelp      = "help"      // keybinding cheat-sheet (read from tmux.conf)
)

// SessionName is the stable tmux session name inside a deployment container.
const SessionName = "stab"

// WindowCommand is the shell command run in a window.
type WindowCommand struct {
	Name    string
	Command string
}

// DefaultLayout returns the standard layout. The main window runs the harness
// (Pi) so `stab .` drops the user straight into the harness; shell, logs, and
// a help window are secondary.
func DefaultLayout(harnessCommand, configDir string) []WindowCommand {
	if harnessCommand == "" {
		harnessCommand = "pi"
	}
	return []WindowCommand{
		{Name: WindowMain, Command: harnessCommand},
		{Name: WindowShell, Command: "bash"},
		{Name: WindowLogs, Command: "tail -F /var/log/stab/runtime.log 2>/dev/null || sleep infinity"},
		{Name: WindowHelp, Command: HelpWindowCommand(configDir)},
	}
}

// HelpWindowCommand renders the keybinding cheat-sheet by reading the comment
// block out of the project's tmux.conf. It is generated from the same file that
// defines the bindings, so editing tmux.conf updates the help automatically.
func HelpWindowCommand(configDir string) string {
	if configDir == "" {
		configDir = "~/.stables"
	}
	return fmt.Sprintf(`grep '^#' %s/tmux.conf 2>/dev/null; printf '
-- full key menu: press Ctrl-b then ? --
-- reload config:  Ctrl-b : then source-file %s/tmux.conf --
'; sleep infinity`, configDir, configDir)
}

// AgentWindow returns a per-agent window command (future milestone).
func AgentWindow(agentID string, command string) WindowCommand {
	return WindowCommand{Name: "agent-" + agentID, Command: command}
}

// HasSession builds a query that exits non-zero if the session does not exist.
func HasSession(session string) []string {
	return []string{"tmux", "has-session", "-t", session}
}

// NewSession builds the command that creates a detached session and its
// windows. The first window becomes the active one.
func NewSession(session string, windows []WindowCommand, workdir string) []string {
	return NewSessionConfig(session, windows, workdir, "")
}

// NewSessionConfig is NewSession with an optional tmux config file (-f) loaded
// at server start.
func NewSessionConfig(session string, windows []WindowCommand, workdir, configFile string) []string {
	args := []string{"tmux"}
	if configFile != "" {
		args = append(args, "-f", configFile)
	}
	args = append(args, "new-session", "-d", "-s", session)
	if workdir != "" {
		args = append(args, "-c", workdir)
	}
	if len(windows) == 0 {
		return args
	}
	first := windows[0]
	args = append(args, "-n", first.Name, first.Command)
	for _, w := range windows[1:] {
		args = append(args, ";", "new-window", "-t", session, "-n", w.Name, w.Command)
	}
	return args
}

// NewWindow builds a command to add a window to an existing session.
func NewWindow(session string, w WindowCommand) []string {
	return []string{"tmux", "new-window", "-t", session, "-n", w.Name, w.Command}
}

// Attach builds the interactive attach command.
func Attach(session string) []string {
	return []string{"tmux", "attach-session", "-t", session}
}

// AttachGroupedScript returns a bash script that attaches a new client to the
// base session through a fresh grouped session. Grouped sessions share the same
// windows, but each client navigates independently; the grouped session is torn
// down when the client detaches.
func AttachGroupedScript(base, window string) string {
	var sb strings.Builder
	sb.WriteString(`S="stab-$RANDOM$RANDOM"; `)
	sb.WriteString(`tmux new-session -d -s "$S" -t ` + shellQuote(base) + ` || exit 1; `)
	if window != "" && validWindowName(window) {
		sb.WriteString(`tmux select-window -t "$S:` + window + `" 2>/dev/null || true; `)
	}
	sb.WriteString(`trap 'tmux kill-session -t "$S" 2>/dev/null' EXIT INT TERM HUP; `)
	sb.WriteString(`tmux attach-session -t "$S"`)
	return sb.String()
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func validWindowName(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			continue
		}
		return false
	}
	return s != ""
}

// AttachWindow builds an attach command targeting a specific window.
func AttachWindow(session, window string) []string {
	return []string{"tmux", "attach-session", "-t", session + ":" + window}
}

// KillSession builds the session teardown command.
func KillSession(session string) []string {
	return []string{"tmux", "kill-session", "-t", session}
}

// SendKeys builds a command to send keystrokes to a window.
func SendKeys(session, window, keys string) []string {
	return []string{"tmux", "send-keys", "-t", session + ":" + window, keys}
}
