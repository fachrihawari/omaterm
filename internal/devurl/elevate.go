package devurl

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
)

var (
	// ErrCancelled is the administrator's password being refused or the
	// dialog closed.
	ErrCancelled = errors.New("cancelled: no password was given")
	// ErrNoAgent is no program being there to ask for the password.
	ErrNoAgent = errors.New("no polkit agent to ask for the password")
)

// Elevate runs exe with args as root, asking for the administrator's
// password the way the desktop does: through pkexec on Linux, and through
// AppleScript's administrator privileges on macOS. What it prints goes to
// stdout and stderr.
func Elevate(goos, exe string, args []string, stdout, stderr io.Writer) error {
	if goos == "darwin" {
		script := `do shell script ` + appleScriptString(shellJoin(append([]string{exe}, args...))) + ` with administrator privileges`
		return exec.Command("osascript", "-e", script).Run()
	}
	var errText bytes.Buffer
	cmd := exec.Command("pkexec", append([]string{exe}, args...)...)
	cmd.Stdout = stdout
	cmd.Stderr = io.MultiWriter(stderr, &errText)
	cmd.Stdin = os.Stdin
	err := cmd.Run()
	if err == nil {
		return nil
	}
	var exit *exec.ExitError
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return ErrNoAgent
	case errors.As(err, &exit) && exit.ExitCode() == 126:
		return ErrCancelled
	case errors.As(err, &exit) && exit.ExitCode() == 127,
		strings.Contains(errText.String(), "No authentication agent"):
		return ErrNoAgent
	}
	return err
}

// shellJoin quotes each word for a POSIX shell.
func shellJoin(words []string) string {
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}

// appleScriptString is s as an AppleScript string literal.
func appleScriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
