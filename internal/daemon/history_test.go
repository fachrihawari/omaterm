package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"omaterm/internal/session"
)

// A pane's scrollback read just before it closed is not written after:
// the file stayed on disk for good. 0.9.3 fixed this in the app only, and
// the daemon is where sessions run by default.
func TestNoHistoryAfterClose(t *testing.T) {
	d, _ := startDaemon(t, time.Minute)
	if _, err := d.reg.Start(session.StartOptions{ID: "p1", Session: "s1", Command: []string{"/bin/sh", "-c", "sleep 30"}, Exec: true, Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	d.writeHistory("p1", []byte("open"))
	if _, err := os.Stat(d.historyPath("p1")); err != nil {
		t.Fatalf("an open pane's scrollback isn't written: %v", err)
	}
	d.endPane("p1")
	d.writeHistory("p1", []byte("late"))
	if _, err := os.Stat(d.historyPath("p1")); err == nil {
		t.Error("scrollback written after its pane closed")
	}
}

// Scrollback no session holds is swept once a day old; a recent one, or
// one a session holds, stays.
func TestSweepHistories(t *testing.T) {
	d, _ := startDaemon(t, time.Minute)
	dir := filepath.Join(d.opts.StateDir, "sessions")
	old := time.Now().Add(-48 * time.Hour)
	for name, age := range map[string]time.Time{"gone.vt": old, "recent.vt": time.Now(), "gone.vt.123.tmp": old} {
		path := filepath.Join(dir, name)
		os.WriteFile(path, []byte("x"), 0o600)
		os.Chtimes(path, age, age)
	}
	d.sweepHistories()
	for name, want := range map[string]bool{"gone.vt": false, "recent.vt": true, "gone.vt.123.tmp": false} {
		_, err := os.Stat(filepath.Join(dir, name))
		if got := err == nil; got != want {
			t.Errorf("%s kept=%v, want %v", name, got, want)
		}
	}
}
