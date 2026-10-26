//go:build linux || darwin

package daemon

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"omaterm/internal/session"
)

// A daemon that takes longer to come back than the quick retries is still
// reconnected to, and the app hears "connected" only then: it gave up,
// said connected, and left every pane blank until a relaunch.
func TestReconnectKeepsTrying(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "d.sock")
	start := func() *Daemon {
		d, err := Start(Options{Socket: socket, StateDir: dir, IdleExit: time.Minute, Version: "test"})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	first := start()
	r, err := Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	r.Spawn, r.quickly = nil, 300*time.Millisecond
	var mu sync.Mutex
	var states []bool
	r.OnConnection = func(reconnecting bool) {
		mu.Lock()
		states = append(states, reconnecting)
		mu.Unlock()
	}
	first.Stop()
	os.Remove(socket)
	time.Sleep(1500 * time.Millisecond) // well past the quick retries
	mu.Lock()
	early := append([]bool(nil), states...)
	mu.Unlock()
	if len(early) != 1 || !early[0] {
		t.Fatalf("before the daemon came back the app heard %v, want only reconnecting", early)
	}
	second := start()
	defer second.Stop()
	waitFor(t, "the reconnect", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(states) == 2 && !states[1]
	})
	if err := r.client().Call("panes", nil, nil); err != nil {
		t.Errorf("connected, a call fails: %v", err)
	}
}

// After a reconnect, the daemon is told again which session the user
// looks at: a new one knew of none, so the session on screen got unread
// marks.
func TestViewedAfterReconnect(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "d.sock")
	start := func() *Daemon {
		d, err := Start(Options{Socket: socket, StateDir: dir, IdleExit: time.Minute, Version: "test"})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	first := start()
	r, err := Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	r.Spawn, r.quickly = nil, 300*time.Millisecond
	r.SetViewed("s1")
	waitFor(t, "the first daemon told", func() bool { return first.reg.Viewed() == "s1" })
	first.Stop()
	os.Remove(socket)
	second := start()
	defer second.Stop()
	waitFor(t, "the new daemon told", func() bool { return second.reg.Viewed() == "s1" })
}

// A pane closed while the daemon was away is closed once it is back: it
// ran on unseen, an agent perhaps still editing the repo, until the next
// launch.
func TestCloseWhileAwayLands(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "d.sock")
	d, err := Start(Options{Socket: socket, StateDir: dir, IdleExit: time.Minute, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	r, err := Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Start(session.StartOptions{ID: "p1", Session: "s1", Command: []string{"/bin/sh", "-c", "sleep 30"}, Exec: true, Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the pane", func() bool { return d.reg.Get("p1") != nil })
	// The close is lost on the way, as to a daemon that stopped answering.
	r.mu.Lock()
	r.closeLater = append(r.closeLater, "p1")
	r.mu.Unlock()
	go r.closePending()
	waitFor(t, "the late close", func() bool { return d.reg.Get("p1") == nil })
}
