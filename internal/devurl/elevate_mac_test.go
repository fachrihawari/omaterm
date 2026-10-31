package devurl

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeOsascript puts an osascript first, and alone, on the PATH: it keeps
// its script, prints "✓ done" when it succeeds, else errText, and exits with
// code. No dialog opens.
func fakeOsascript(t *testing.T, errText string, code int) (script func() string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "script")
	body := "#!/bin/sh\nprintf '%s' \"$2\" > '" + log + "'\n"
	if code == 0 {
		body += "echo '✓ done'\n"
	} else {
		body += "echo '" + errText + "' >&2\nexit " + strconv.Itoa(code) + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "osascript"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return func() string {
		b, _ := os.ReadFile(log)
		return string(b)
	}
}

// On macOS the dialog says who asks and why, and what the step prints is
// passed on.
func TestElevateThroughOsascript(t *testing.T) {
	script := fakeOsascript(t, "", 0)
	var stdout bytes.Buffer
	if err := Elevate("darwin", "/Applications/omaterm.app/Contents/MacOS/omaterm", []string{"proxy", "setup", "--system"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`do shell script "'/Applications/omaterm.app/Contents/MacOS/omaterm' 'proxy' 'setup' '--system'"`,
		`with prompt "omaterm wants to trust its development certificate authority`,
		"with administrator privileges",
	} {
		if !strings.Contains(script(), want) {
			t.Errorf("script %s\nlacks %s", script(), want)
		}
	}
	if stdout.String() != "✓ done\n" {
		t.Errorf("stdout %q", stdout.String())
	}
	if err := Elevate("darwin", "/x", []string{"proxy", "uninstall", "--system"}, &stdout, &bytes.Buffer{}); err != nil ||
		!strings.Contains(script(), `with prompt "omaterm wants to remove its development certificate authority`) {
		t.Errorf("uninstall: %v, script %s", err, script())
	}
}

// Cancelling the dialog is Cancelled, no screen to show it on is no agent,
// and a failing step tells its own message.
func TestElevateOsascriptErrors(t *testing.T) {
	for errText, want := range map[string]error{
		"0:120: execution error: User canceled. (-128)":                ErrCancelled,
		"0:120: execution error: No user interaction allowed. (-1713)": ErrNoAgent,
	} {
		fakeOsascript(t, errText, 1)
		if err := Elevate("darwin", "/x", nil, &bytes.Buffer{}, &bytes.Buffer{}); !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", errText, err, want)
		}
	}
	fakeOsascript(t, "0:183: execution error: security: SecTrustSettingsSetTrustSettings: The authorization was denied. (1)", 1)
	err := Elevate("darwin", "/x", nil, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || errors.Is(err, ErrCancelled) || errors.Is(err, ErrNoAgent) ||
		!strings.HasPrefix(err.Error(), "security: SecTrustSettingsSetTrustSettings: The authorization was denied. (") {
		t.Errorf("the step failing: %v", err)
	}
	// No osascript at all: no dialog to ask with.
	t.Setenv("PATH", t.TempDir())
	if err := Elevate("darwin", "/x", nil, &bytes.Buffer{}, &bytes.Buffer{}); !errors.Is(err, ErrNoAgent) {
		t.Errorf("no osascript: %v", err)
	}
}
