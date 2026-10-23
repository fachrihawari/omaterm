// Adapted from Kero (github.com/egoist/kero, MIT,
// docs/third_party/kero-LICENSE), internal/daemon/shellcmd.go.

// Package shellcmd builds the command lines that start a pane's shell: a
// login shell, or one that runs a command first and tells its exit status
// (OSC 133;D) before handing the pane to the shell.
package shellcmd

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// Login returns the user's shell: $SHELL, else the account's, else sh.
func Login() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	if u, err := user.Current(); err == nil {
		if sh := accountShell(u.Username); sh != "" {
			return sh
		}
	}
	return "/bin/sh"
}

// accountShell reads a user's shell from /etc/passwd.
func accountShell(name string) string {
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Split(line, ":")
		if len(f) == 7 && f[0] == name {
			return f[6]
		}
	}
	return ""
}

// Name is a shell's name, as panes show it: "zsh", "fish".
func Name(shell string) string { return filepath.Base(shell) }

// Args is the argv that starts shell, running command first when set. With
// no command it is a login shell ("-zsh"). With one, the command runs in
// an interactive login shell, so that it finds what the user's profile
// puts on PATH; the pane goes on with the shell once it ends, after
// telling its exit status (OSC 133;D).
func Args(shell string, command []string) []string {
	name := Name(shell)
	if len(command) == 0 {
		return []string{"-" + name}
	}
	status := `"$?"`
	if name == "fish" {
		status = "$status"
	}
	script := Join(name, command) + `; printf '\033]133;D;%s\007' ` + status + "; exec " + Quote(name, shell) + " -l"
	if name == "fish" {
		// fish leaves job control off for -c, so the command would run in
		// the shell's process group, where nothing tells it from the shell.
		script = "status job-control full; " + script
	}
	return []string{name, "-l", "-i", "-c", script}
}

// Quote quotes an argument for a shell's command line.
func Quote(shell, s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:,+@%", r))
	}) < 0 {
		return s
	}
	if shell == "fish" {
		return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// OneLine tells a command given as one line, as `omaterm new -- "npm run
// dev"` gives it, which the shell runs as it is.
func OneLine(args []string) bool {
	return len(args) == 1 && strings.ContainsAny(args[0], " ;|&")
}

// Join writes a command for a shell's command line.
func Join(shell string, args []string) string {
	if OneLine(args) {
		return args[0]
	}
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = Quote(shell, a)
	}
	return strings.Join(q, " ")
}
