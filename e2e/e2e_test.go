//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Settings closes with Escape and Ctrl+W, from its own key presses, and
// opens again at once after a close: 0.9.0 crashed on the first.
func TestSettingsCloseAndReopen(t *testing.T) {
	a := Start(t, Options{})
	open := func() {
		a.MustCLI("settings", "general")
		a.WaitWindow("omaterm Settings", true)
		a.WaitFocus("omaterm Settings")
	}
	open()
	a.Shot("open")
	a.Key("-k", "Escape")
	a.WaitWindow("omaterm Settings", false)
	a.Alive()

	open()
	a.Key("-M", "ctrl", "w", "-m", "ctrl")
	a.WaitWindow("omaterm Settings", false)
	a.Alive()

	// Closed and asked for again before the close went through: the new
	// one must stay.
	open()
	a.Key("-k", "Escape")
	a.MustCLI("settings", "about")
	time.Sleep(time.Second)
	a.WaitWindow("omaterm Settings", true)
	a.Alive()
}

// A shell that exits closes its session, and the app goes on.
func TestShellExit(t *testing.T) {
	a := Start(t, Options{})
	a.MustCLI("new", "-n", "bye")
	pane := lsPane(t, a, "bye")
	a.MustCLI("send", pane, "exit", "--enter")
	if !poll(10*time.Second, func() bool { return lsPane(t, a, "bye") == "" }) {
		t.Fatal("the session did not close as its shell exited")
	}
	a.Alive()
}

// Zooming inside a terminal (its own Ctrl+=) and switching themes leave
// the app running.
func TestZoomAndTheme(t *testing.T) {
	a := Start(t, Options{})
	a.MustCLI("new", "-n", "zoom")
	a.MustCLI("focus", "zoom")
	a.WaitFocus("omaterm")
	for i := 0; i < 3; i++ {
		a.Key("-M", "ctrl", "=", "-m", "ctrl")
	}
	a.MustCLI("do", "theme.light")
	a.MustCLI("do", "theme.dark")
	time.Sleep(500 * time.Millisecond)
	a.Shot("zoomed")
	a.Alive()
}

// Zooming in a terminal of the main window (its own Ctrl+=) shows the new
// size in Settings at once: it stayed old there until something else
// redrew that window.
func TestZoomRedrawsSettings(t *testing.T) {
	a := Start(t, Options{})
	a.MustCLI("new", "-n", "zoom")
	a.MustCLI("settings", "terminal")
	a.WaitWindow("omaterm Settings", true)
	a.Sway(`[title="^omaterm$"] focus`)
	a.WaitFocus("omaterm")
	time.Sleep(time.Second)
	before := a.WindowShot("omaterm Settings")
	a.Key("-M", "ctrl", "=", "-m", "ctrl")
	time.Sleep(time.Second)
	after := a.WindowShot("omaterm Settings")
	if bytes.Equal(before, after) {
		a.Shot("stale")
		t.Fatal("Settings still shows the old font size")
	}
	a.Alive()
}

// With the session daemon, a pane keeps what it showed when a setting
// makes its terminal be made again (the cursor).
func TestDaemonPaneSurvivesSettingChange(t *testing.T) {
	a := Start(t, Options{Daemon: true})
	a.MustCLI("new", "-n", "keep")
	pane := lsPane(t, a, "keep")
	a.MustCLI("send", pane, "echo marker-$((40+2))", "--enter")
	if !poll(10*time.Second, func() bool { out, _ := a.CLI("read", pane); return strings.Contains(out, "marker-42") }) {
		t.Fatal("the pane never showed its output")
	}
	a.MustCLI("settings", "terminal")
	a.WaitWindow("omaterm Settings", true)
	a.Key("-k", "Escape")
	a.Alive()
	out := a.MustCLI("read", pane)
	if !strings.Contains(out, "marker-42") {
		t.Fatalf("the pane lost its output:\n%s", out)
	}
}

// Sessions come back after a quit and a launch.
func TestSessionsSurviveRelaunch(t *testing.T) {
	a := Start(t, Options{Daemon: true})
	a.MustCLI("new", "-n", "one")
	a.MustCLI("new", "-n", "two")
	a.Relaunch()
	for _, name := range []string{"one", "two"} {
		if lsPane(t, a, name) == "" {
			t.Fatalf("session %q did not come back", name)
		}
	}
	a.Alive()
}

// lsPane is the focused pane of the session named name, "" for none.
func lsPane(t *testing.T, a *App, name string) string {
	t.Helper()
	out, err := a.CLI("ls", "--json")
	if err != nil {
		t.Fatalf("omaterm ls: %v %s", err, out)
	}
	var sessions []struct {
		Name  string `json:"name"`
		Focus string `json:"focus"`
	}
	if err := json.Unmarshal([]byte(out), &sessions); err != nil {
		t.Fatalf("omaterm ls --json: %v\n%s", err, out)
	}
	for _, s := range sessions {
		if s.Name == name {
			return s.Focus
		}
	}
	return ""
}

// A crash leaves its report in the crash log, and the next launch tells.
func TestCrashIsReported(t *testing.T) {
	a := Start(t, Options{})
	a.cmd.Process.Signal(syscall.SIGABRT) // as a fatal error dies: Go's report
	select {
	case <-a.done:
	case <-time.After(10 * time.Second):
		t.Fatal("omaterm did not die of SIGABRT")
	}
	log := filepath.Join(a.dir, "state", "omaterm", "crash-app.log")
	b, err := os.ReadFile(log)
	if err != nil || !strings.Contains(string(b), "SIGABRT") {
		t.Fatalf("no crash report in %s: %v\n%s", log, err, b)
	}
	a.launch()
	time.Sleep(time.Second)
	a.Shot("banner")
	// The banner is in the window: the report is noted as seen only when
	// dismissed, so the log keeps its size.
	if after, _ := os.ReadFile(log); !strings.Contains(string(after), "--- the crash above: omaterm app") {
		t.Fatalf("the next run didn't note which run crashed:\n%s", after)
	}
}
