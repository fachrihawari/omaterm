//go:build linux || darwin

package daemon

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"

	"omaterm/internal/session"
)

func startDaemon(t *testing.T, idle time.Duration) (*Daemon, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "omd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "d.sock")
	// StateFile names the temp dir's state.json: without it the daemon
	// falls back to the user's real one, whose web.json its tests would
	// change — and a daemon started after one of those could run a real
	// tunnel.
	d, err := Start(Options{Socket: socket, StateDir: dir, StateFile: filepath.Join(dir, "state.json"), IdleExit: idle, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Stop)
	return d, socket
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A pane the daemon runs shows in the app's terminal, takes what is typed
// there, and goes on once the app lets go of it.
func TestAttachOutlivesClient(t *testing.T) {
	_, socket := startDaemon(t, time.Minute)
	r, err := Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	h, err := r.Start(session.StartOptions{ID: "p1", Session: "s1", Command: []string{"sh", "-c", "echo hello; exec cat"},
		Exec: true, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	term := h.Term()
	if term == nil {
		t.Fatal("no terminal")
	}
	waitFor(t, "first frame", func() bool { return strings.Contains(term.Text(), "hello") })

	h.(*remotePane).stream.Write([]byte("typed-in-view\r")) // as typed in the view
	waitFor(t, "typed echo", func() bool { return strings.Count(term.Text(), "typed-in-view") >= 2 })
	if !strings.Contains(h.Text(false, 0), "typed-in-view") {
		t.Fatalf("daemon screen lacks input: %q", h.Text(false, 0))
	}
	r.Close()

	r2, err := Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	h2 := r2.Get("p1")
	if h2 == nil {
		t.Fatal("pane gone after the client left")
	}
	if t2 := h2.Term(); t2 == nil {
		t.Fatal("no terminal on reattach")
	} else {
		waitFor(t, "reattached screen", func() bool { return strings.Contains(t2.Text(), "typed-in-view") })
	}
}

// The app hears of a pane's exit as a status change.
func TestRemoteExitHook(t *testing.T) {
	_, socket := startDaemon(t, time.Minute)
	r, err := Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	exited := make(chan int, 1)
	r.SetHooks(session.Hooks{OnExit: func(id string, code int) { exited <- code }})
	h, err := r.Start(session.StartOptions{ID: "p2", Command: []string{"sh", "-c", "sleep 0.3; exit 3"}, Exec: true, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := h.Wait(ctx, func(i session.Info) bool { return i.Status == session.Exited }); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-exited:
		if code != 3 {
			t.Fatalf("exit code %d, want 3", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no exit hook")
	}
	r.Remove("p2")
	waitFor(t, "pane removed", func() bool { return r.Len() == 0 })
}

// With no pane and no client, the daemon ends itself.
func TestIdleExit(t *testing.T) {
	d, socket := startDaemon(t, 500*time.Millisecond)
	r, err := Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	select {
	case <-d.Done():
		t.Fatal("exited with a client")
	default:
	}
	r.Close()
	select {
	case <-d.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("did not exit when idle")
	}
}

// A second daemon will not take a socket a live one answers.
func TestSecondDaemonRefused(t *testing.T) {
	_, socket := startDaemon(t, time.Minute)
	if d, err := Start(Options{Socket: socket, StateDir: t.TempDir(), Version: "test"}); err == nil {
		d.Stop()
		t.Fatal("second daemon started")
	}
}

// Restarting the daemon connects to a new one, and tells of the panes the
// old one took with it.
func TestRestartTellsLostPanes(t *testing.T) {
	d, socket := startDaemon(t, time.Minute)
	r, err := Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var next *Daemon
	r.Spawn = func() error {
		<-d.Done()
		var err error
		next, err = Start(Options{Socket: socket, StateDir: t.TempDir(), IdleExit: time.Minute, Version: "next"})
		return err
	}
	t.Cleanup(func() {
		if next != nil {
			next.Stop()
		}
	})
	lost := make(chan []string, 1)
	r.OnLost = func(ids []string) { lost <- ids }
	if _, err := r.Start(session.StartOptions{ID: "p3", Command: []string{"cat"}, Exec: true, Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	r.Restart()
	select {
	case ids := <-lost:
		if len(ids) != 1 || ids[0] != "p3" {
			t.Fatalf("lost %v, want [p3]", ids)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("not told of lost panes")
	}
	if r.Version() != "next" || r.Len() != 0 {
		t.Fatalf("version %q, %d panes; want the new daemon's, none", r.Version(), r.Len())
	}
	if _, err := r.Start(session.StartOptions{ID: "p4", Command: []string{"cat"}, Exec: true, Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
}

// A size a client asks for is bounded: a paired web page asking for
// 65535×65535 cells took the daemon's memory, and every session with it.
func TestResizeBounded(t *testing.T) {
	d, socket := startDaemon(t, time.Minute)
	r, err := Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Start(session.StartOptions{ID: "big", Session: "s1", Command: []string{"sh", "-c", "exec cat"},
		Exec: true, Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	p := d.reg.Get("big")
	d.resize("big", p, 65535, 65535, 65535, 65535)
	if c, rows := p.Term().Size(); c > maxCols || rows > maxRows {
		t.Fatalf("size %dx%d, want at most %dx%d", c, rows, maxCols, maxRows)
	}
}

// A daemon that stopped answering doesn't freeze the window: attaching
// to a pane, which the window does as it draws, gives up.
func TestAttachGivesUp(t *testing.T) {
	old := uiCallTimeout
	uiCallTimeout = 200 * time.Millisecond
	defer func() { uiCallTimeout = old }()
	socket := filepath.Join(t.TempDir(), "stuck.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // reads nothing, answers nothing
		}
	}()
	done := make(chan error, 1)
	go func() { _, err := attach(socket, "p1"); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("attach to a daemon that never answers succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("attach hangs on a daemon that never answers")
	}
}

// Clipboard reads are refused in the daemon unless the settings ask: one
// passed on with no app to answer it hung the program.
func TestClipboardReadsRefusedUnlessAsked(t *testing.T) {
	var o session.StartOptions
	termPrefs{}.apply(&o)
	if o.ClipboardRead != terminal.ClipboardDeny {
		t.Fatalf("by default %v, want deny", o.ClipboardRead)
	}
	termPrefs{ClipboardRead: "ask"}.apply(&o)
	if o.ClipboardRead != terminal.ClipboardForward {
		t.Fatalf("asked %v, want forward", o.ClipboardRead)
	}
}
