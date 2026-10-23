//go:build linux || darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omaterm/internal/daemon"
	"omaterm/internal/session"
)

// A daemon pane not shown for long lets go of the app's terminal, and one
// shown keeps it; showing it again attaches anew, its screen intact.
func TestDetachHiddenDaemonPanes(t *testing.T) {
	dir, err := os.MkdirTemp("", "omh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "d.sock")
	d, err := daemon.Start(daemon.Options{Socket: socket, StateDir: dir, StateFile: filepath.Join(dir, "state.json"), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	r, err := daemon.Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	a := newApp()
	a.win = nil
	a.panes = r
	shownID := a.activeSplitLeaf()
	for _, id := range []string{"hidden1", shownID} {
		if _, err := r.Start(session.StartOptions{ID: id, Exec: true, Command: []string{"sh", "-c", "echo screen-" + id + "; exec cat"}, Cols: 80, Rows: 24}); err != nil {
			t.Fatal(err)
		}
		a.term(id)
	}
	old := time.Now().Add(-time.Hour)
	a.paneShown["hidden1"], a.paneShown[shownID] = old, old
	a.detachHidden(time.Now())
	if r.Get("hidden1").TermIfOpen() != nil {
		t.Fatal("hidden pane kept its terminal")
	}
	if r.Get(shownID).TermIfOpen() == nil {
		t.Fatal("shown pane lost its terminal")
	}
	term := a.term("hidden1")
	deadline := time.Now().Add(5 * time.Second)
	for term == nil || !strings.Contains(term.Text(), "screen-hidden1") {
		if time.Now().After(deadline) {
			t.Fatal("reattached pane lacks its screen")
		}
		time.Sleep(20 * time.Millisecond)
		term = r.Get("hidden1").TermIfOpen()
	}
}
