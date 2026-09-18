package remote

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Target struct {
	Name string
	User string
	Host string
}

func Parse(s string) Target {
	name := s
	user := ""
	host := s
	if i := strings.IndexByte(s, '@'); i >= 0 {
		user = s[:i]
		host = s[i+1:]
		name = host
	}
	return Target{Name: name, User: user, Host: host}
}

func (t Target) SSHAddr() string {
	if t.User != "" {
		return t.User + "@" + t.Host
	}
	return t.Host
}

func CopySelf(t Target) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command("scp", exe, t.SSHAddr()+":~/stables")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func Run(t Target, args ...string) error {
	cmdline := "chmod +x ~/stables && ~/stables " + shellJoin(args)
	cmd := exec.Command("ssh", t.SSHAddr(), cmdline)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func shellJoin(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = shellQuote(a)
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool {
		return !(r == '-' || r == '_' || r == '.' || r == '/' || r == ':' || r == '=' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z')
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func AddressHint(t Target) string {
	if t.Host == "" {
		return ""
	}
	return fmt.Sprintf("http://%s", t.Host)
}
