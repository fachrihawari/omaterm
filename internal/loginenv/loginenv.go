// Package loginenv gives an app opened from Finder or the Dock the
// environment the user's shell sets up. launchd starts it with
// PATH=/usr/bin:/bin:/usr/sbin:/sbin and none of the profile's variables,
// so neither the app nor the session daemon it starts finds tools from
// Homebrew, ~/.local/bin or nvm. Ghostty, VS Code and Zed do the same.
package loginenv

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"omaterm/internal/shellcmd"
)

// Timeout bounds the shell: a profile that waits for input or runs
// something slow must not keep the app from opening.
const Timeout = 5 * time.Second

// Wanted tells whether to import the login environment: on macOS, when no
// terminal started the app. launchd sets no TERM; a terminal always does,
// and its environment already is the login one.
func Wanted(goos string, getenv func(string) string) bool {
	return goos == "darwin" && getenv("TERM") == ""
}

// Import merges the user's login environment into the process's, when
// Wanted. Failures are only logged: the app runs on with what it has.
func Import(goos string) {
	if !Wanted(goos, os.Getenv) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	env, err := Load(ctx, shellcmd.Login())
	if err != nil {
		log.Printf("login environment: %v", err)
		return
	}
	for k, v := range Merge(os.Environ(), env) {
		os.Setenv(k, v)
	}
}

// Load runs shell as an interactive login shell, so that it reads the
// profile and the rc file (~/.zshrc, where nvm and mise usually load),
// and returns the environment it ends with, as KEY=value entries.
func Load(ctx context.Context, shell string) ([]string, error) {
	mark, err := marker()
	if err != nil {
		return nil, err
	}
	// The markers fence env's output off from whatever the rc files
	// print. env -0 keeps values with newlines whole; an env without it
	// gives lines, which Parse also reads.
	script := "printf '%s' " + mark + "; command env -0 2>/dev/null || command env; printf '%s' " + mark
	cmd := exec.CommandContext(ctx, shell, "-i", "-l", "-c", script)
	cmd.Env = append(os.Environ(), "OMATERM_RESOLVING_ENVIRONMENT=1")
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	// What a profile starts in the background goes with the shell, and
	// keeps no pipe open past it.
	inGroup(cmd)
	cmd.Cancel = func() error { killGroup(cmd); return nil }
	cmd.WaitDelay = time.Second
	runErr := cmd.Run()
	if runErr != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	// A shell that exits non-zero after printing (an rc file's last
	// command failed) still gave the environment.
	env, ok := Parse(out.Bytes(), mark)
	if !ok {
		if runErr != nil {
			return nil, runErr
		}
		return nil, errors.New(shell + " printed no environment")
	}
	return env, nil
}

// marker is a word no rc file prints.
func marker() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "_OMATERM_ENV_" + hex.EncodeToString(b) + "_", nil
}

// Parse reads the environment printed between two marks: entries ended
// by NUL (env -0), else lines, where a line without "=" goes on the value
// before it.
func Parse(out []byte, mark string) ([]string, bool) {
	s := string(out)
	i := strings.Index(s, mark)
	if i < 0 {
		return nil, false
	}
	s = s[i+len(mark):]
	j := strings.Index(s, mark)
	if j < 0 {
		return nil, false
	}
	s = s[:j]
	var env []string
	if strings.Contains(s, "\x00") {
		for _, e := range strings.Split(s, "\x00") {
			if strings.Index(e, "=") > 0 {
				env = append(env, e)
			}
		}
		return env, true
	}
	for _, line := range strings.Split(strings.TrimSuffix(s, "\n"), "\n") {
		switch {
		case strings.Index(line, "=") > 0:
			env = append(env, line)
		case len(env) > 0:
			env[len(env)-1] += "\n" + line
		}
	}
	return env, true
}

// Merge returns what to set in a process whose environment is current,
// from the shell's: its PATH, and the variables the process lacks. The
// process keeps its own values, and what isn't the user's settings stays
// out (see skip).
func Merge(current, shell []string) map[string]string {
	have := map[string]bool{}
	for _, e := range current {
		if k, _, ok := strings.Cut(e, "="); ok {
			have[k] = true
		}
	}
	set := map[string]string{}
	for _, e := range shell {
		k, v, ok := strings.Cut(e, "=")
		if !ok || k == "" || skip(k) {
			continue
		}
		if k == "PATH" {
			if v != "" {
				set[k] = v
			}
			continue
		}
		if !have[k] {
			set[k] = v
		}
	}
	return set
}

// skip tells a variable not to take from the shell:
//   - XDG_*: omaterm's state directory and the daemon's socket follow them,
//     so taking XDG_STATE_HOME or XDG_RUNTIME_DIR would lose a running
//     daemon;
//   - the shell's own and the terminal's: PWD, SHLVL, _, TERM*, the size,
//     prompts;
//   - OMATERM_*: omaterm's own, set per pane or per run.
func skip(k string) bool {
	switch k {
	case "PWD", "OLDPWD", "SHLVL", "_", "COLUMNS", "LINES", "PROMPT_COMMAND":
		return true
	}
	for _, p := range []string{"XDG_", "TERM", "OMATERM_", "PS", "PROMPT", "RPROMPT", "RPS"} {
		if strings.HasPrefix(k, p) {
			// PS1–PS4 only: PS_FORMAT and the like are a program's.
			if p == "PS" && (len(k) != 3 || k[2] < '0' || k[2] > '9') {
				continue
			}
			return true
		}
	}
	return false
}
