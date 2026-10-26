//go:build linux || darwin

package session

import (
	"strings"
	"testing"
	"time"
)

// A pane's start command that fails marks the pane failed, saying how; it
// went unnoticed, the pane idle at its shell. Ctrl+C isn't a failure.
func TestCommandFailureMarksPane(t *testing.T) {
	r := NewRegistry()
	defer r.CloseAll()
	for _, tc := range []struct {
		code   string
		failed bool
	}{{"3", true}, {"130", false}} {
		id := "exit" + tc.code
		p, err := r.Start(StartOptions{ID: id, Session: "s", Command: []string{"sh", "-c", "exit " + tc.code}, Cols: 80, Rows: 24})
		if err != nil {
			t.Fatal(err)
		}
		var info Info
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
			if info = p.Info(); info.CommandExit != nil {
				break
			}
		}
		if info.CommandExit == nil {
			t.Fatalf("exit %s: never told", tc.code)
		}
		time.Sleep(500 * time.Millisecond) // the shell's prompt comes after
		info = p.Info()
		if got := info.Status == Failed; got != tc.failed {
			t.Errorf("exit %s: status %s, want failed=%v", tc.code, info.Status, tc.failed)
		}
		if tc.failed && !strings.Contains(info.Message, "sh exited (code 3)") {
			t.Errorf("exit %s: message %q", tc.code, info.Message)
		}
	}
}
