package devurl

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var (
	// ErrCancelled is the administrator's password being refused or the
	// dialog closed.
	ErrCancelled = errors.New("cancelled: no password was given")
	// ErrNoAgent is no program being there to ask for the password: no
	// polkit agent on Linux, no screen to show the dialog on in macOS.
	ErrNoAgent = errors.New("no password dialog could open")
)

// Elevate runs exe with args as root, asking for the administrator's
// password the way the desktop does: through pkexec on Linux, and through
// AppleScript's administrator privileges on macOS. What it prints goes to
// stdout and stderr; on macOS, what a failing step printed is in the error.
func Elevate(goos, exe string, args []string, stdout, stderr io.Writer) error {
	if goos == "darwin" {
		return elevateMac(exe, args, stdout)
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
		return fmt.Errorf("%w (pkexec isn't installed)", ErrNoAgent)
	case errors.As(err, &exit) && exit.ExitCode() == 126:
		return ErrCancelled
	case errors.As(err, &exit) && exit.ExitCode() == 127,
		strings.Contains(errText.String(), "No authentication agent"):
		return fmt.Errorf("%w (no polkit agent)", ErrNoAgent)
	}
	return err
}

// elevateMac runs the step through osascript's administrator privileges.
// The prompt names omaterm and why: without one, the dialog says
// "osascript wants to make changes".
func elevateMac(exe string, args []string, stdout io.Writer) error {
	prompt := "omaterm wants to trust its development certificate authority, so that https://<name>.localhost opens without a warning."
	if len(args) > 1 && args[1] == "uninstall" {
		prompt = "omaterm wants to remove its development certificate authority from the System keychain."
	}
	script := `do shell script ` + appleScriptString(shellJoin(append([]string{exe}, args...))) +
		` with prompt ` + appleScriptString(prompt) + ` with administrator privileges without altering line endings`
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		return osascriptError(string(out), err)
	}
	stdout.Write(out) // the step's own output, as do shell script returns it
	return nil
}

// osascriptCode is the error number osascript ends its message with.
var osascriptCode = regexp.MustCompile(`\s*\(-?\d+\)$`)

// osascriptError is what a failed osascript tells: the dialog cancelled
// (-128), no screen to show it on (-1713, as over ssh), or the step's own
// message, which osascript prints as "0:183: execution error: <it> (1)".
func osascriptError(out string, err error) error {
	msg := strings.TrimSpace(out)
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return fmt.Errorf("%w (osascript isn't there)", ErrNoAgent)
	case strings.HasSuffix(msg, "(-128)") || strings.Contains(msg, "User canceled"):
		return ErrCancelled
	case strings.HasSuffix(msg, "(-1713)"):
		return fmt.Errorf("%w (no one is logged in to the screen)", ErrNoAgent)
	}
	if _, rest, ok := strings.Cut(msg, "execution error: "); ok {
		msg = rest
	}
	if msg = osascriptCode.ReplaceAllString(msg, ""); msg == "" {
		return err
	}
	return fmt.Errorf("%s (%w)", msg, err)
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
