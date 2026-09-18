package cli

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

// promptPassword reads a password without echo. Returns "" when stdin is not a
// terminal or the read fails, so non-interactive installs fall back to the
// base (unsealed) install.
func promptPassword() string {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return ""
	}
	fmt.Fprint(os.Stderr, "Installer password: ")
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return ""
	}
	return string(b)
}
