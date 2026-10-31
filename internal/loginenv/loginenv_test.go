//go:build !windows

package loginenv

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestWanted(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	if !Wanted("darwin", env(nil)) {
		t.Error("macOS, opened from the Dock: wanted")
	}
	if Wanted("darwin", env(map[string]string{"TERM": "xterm-256color"})) {
		t.Error("macOS, started from a terminal: not wanted")
	}
	if Wanted("linux", env(nil)) {
		t.Error("Linux: never")
	}
}

func TestParse(t *testing.T) {
	const m = "_MARK_"
	// NUL-ended, after an rc file's noise and before more of it.
	out := []byte("Welcome!\n" + m + "A=1\x00MULTI=two\nlines\x00junk\x00" + m + "bye\n")
	env, ok := Parse(out, m)
	if !ok || !slices.Equal(env, []string{"A=1", "MULTI=two\nlines"}) {
		t.Errorf("NUL: %q %v", env, ok)
	}
	// Lines, from an env without -0: a line without "=" goes on.
	env, ok = Parse([]byte(m+"A=1\nMULTI=two\nlines\nB=\n"+m), m)
	if !ok || !slices.Equal(env, []string{"A=1", "MULTI=two\nlines", "B="}) {
		t.Errorf("lines: %q %v", env, ok)
	}
	for _, out := range []string{"", "no marks", m + "A=1\x00"} {
		if _, ok := Parse([]byte(out), m); ok {
			t.Errorf("%q: parsed", out)
		}
	}
}

func TestMerge(t *testing.T) {
	current := []string{"PATH=/usr/bin:/bin", "HOME=/Users/me", "LANG=C"}
	shell := []string{
		"PATH=/opt/homebrew/bin:/usr/bin:/bin",
		"HOME=/elsewhere", "LANG=en_US.UTF-8", // the process keeps its own
		"NVM_DIR=/Users/me/.nvm", "PS_FORMAT=pid", "EMPTY=",
		"XDG_STATE_HOME=/x", "XDG_RUNTIME_DIR=/r", "XDG_CONFIG_HOME=/c",
		"PWD=/Users/me", "OLDPWD=/", "SHLVL=2", "_=/usr/bin/env",
		"TERM=dumb", "TERM_PROGRAM=Apple_Terminal", "COLUMNS=80", "LINES=24",
		"PS1=%n$ ", "PROMPT=%n$ ", "RPROMPT=x", "PROMPT_COMMAND=x",
		"OMATERM_SOCKET=/s", "OMATERM_RESOLVING_ENVIRONMENT=1",
		"broken", "=x",
	}
	got := Merge(current, shell)
	want := map[string]string{"PATH": "/opt/homebrew/bin:/usr/bin:/bin", "NVM_DIR": "/Users/me/.nvm", "PS_FORMAT": "pid", "EMPTY": ""}
	if len(got) != len(want) {
		t.Errorf("got %q, want %q", got, want)
	}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			t.Errorf("%s = %q (%v), want %q", k, g, ok, v)
		}
	}
	// An empty PATH from the shell is no PATH.
	if _, ok := Merge(current, []string{"PATH="})["PATH"]; ok {
		t.Error("empty PATH taken")
	}
}

// fakeShell writes a shell that prints noise as an rc file would, sets
// what a profile would, then runs its -c script, as `zsh -i -l -c` does.
func fakeShell(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fakesh")
	script := "#!/bin/sh\n" +
		"[ \"$1 $2 $3\" = '-i -l -c' ] || exit 9\n" +
		"echo 'Last login: today'\n" + body
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad(t *testing.T) {
	sh := fakeShell(t, "FROM_PROFILE='two\nlines'; export FROM_PROFILE\n"+
		"PATH=/opt/fake/bin:$PATH; export PATH\n"+
		"exec /bin/sh -c \"$4\"\n")
	env, err := Load(context.Background(), sh)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for k, v := range Merge(nil, env) {
		m[k] = v
	}
	if m["FROM_PROFILE"] != "two\nlines" {
		t.Errorf("FROM_PROFILE = %q", m["FROM_PROFILE"])
	}
	if p := m["PATH"]; len(p) < 13 || p[:13] != "/opt/fake/bin" {
		t.Errorf("PATH = %q", p)
	}
	if m["OMATERM_RESOLVING_ENVIRONMENT"] != "" {
		t.Error("omaterm's own marker came back")
	}
	found := false
	for _, e := range env {
		found = found || e == "OMATERM_RESOLVING_ENVIRONMENT=1"
	}
	if !found {
		t.Error("the shell was not told it is read by omaterm")
	}
}

// A shell that hangs (a profile waiting for something) is given up on,
// with what it started.
func TestLoadTimesOut(t *testing.T) {
	sh := fakeShell(t, "sleep 30\n")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := Load(ctx, sh); err == nil {
		t.Fatal("no error")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %v", d)
	}
}

// A shell that prints no environment, or can't start, is an error.
func TestLoadFails(t *testing.T) {
	if _, err := Load(context.Background(), fakeShell(t, "exit 0\n")); err == nil {
		t.Error("no environment: no error")
	}
	if _, err := Load(context.Background(), filepath.Join(t.TempDir(), "nosh")); err == nil {
		t.Error("no shell: no error")
	}
}
