package session

import (
	"os"
	"strings"
)

// GhosttyVersion is the version of Ghostty the terminal emulator,
// libghostty-vt, is built from: programs that know Ghostty, as coding
// agents for their notifications and keyboard, take the pane for one.
const GhosttyVersion = "1.3.2-dev"

// environment is the environment of a pane's shell: the app's, without
// what describes the terminal the app was started from, and with what
// tells programs where they run.
func environment(sessionID, paneID, shell string, extra []string) []string {
	env := make([]string, 0, len(os.Environ())+12)
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case k == "CLAUDECODE", k == "TERM_PROGRAM", k == "TERM_PROGRAM_VERSION", k == "TERM_SESSION_ID",
			k == "ITERM_SESSION_ID", k == "WINDOWID", k == "KITTY_WINDOW_ID", k == "LC_TERMINAL", k == "LC_TERMINAL_VERSION",
			k == "INVOCATION_ID", k == "MYGO_GHOSTTY_VT", k == "TMUX", k == "TMUX_PANE", k == "ALACRITTY_WINDOW_ID",
			strings.HasPrefix(k, "OMATERM_"), strings.HasPrefix(k, "CLAUDE_CODE_"), strings.HasPrefix(k, "GHOSTTY_"),
			strings.HasPrefix(k, "VSCODE_"):
			continue
		}
		env = append(env, kv)
	}
	set := func(kv string) {
		k, _, _ := strings.Cut(kv, "=")
		for i, e := range env {
			if ek, _, _ := strings.Cut(e, "="); ek == k {
				env[i] = kv
				return
			}
		}
		env = append(env, kv)
	}
	set("TERM=xterm-256color")
	set("COLORTERM=truecolor")
	set("TERM_PROGRAM=ghostty")
	set("TERM_PROGRAM_VERSION=" + GhosttyVersion)
	set("LC_TERMINAL=omaterm")
	if shell != "" {
		set("SHELL=" + shell)
	}
	set("OMATERM_SESSION=" + sessionID)
	set("OMATERM_PANE=" + paneID)
	if os.Getenv("LANG") == "" && os.Getenv("LC_ALL") == "" && os.Getenv("LC_CTYPE") == "" {
		set("LANG=en_US.UTF-8")
	}
	for _, kv := range extra {
		if strings.Contains(kv, "=") {
			set(kv)
		}
	}
	return env
}
