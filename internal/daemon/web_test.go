//go:build linux || darwin

package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"omaterm/internal/model"
	"omaterm/internal/proto"
	"omaterm/internal/web"
)

// With the window closed, web access wakes a saved pane that does not
// run yet, as a restore would.
func TestWakeWithWindowClosed(t *testing.T) {
	dir := t.TempDir()
	st := &model.State{Version: model.Version, Projects: []model.Project{{ID: "p1", Name: "x", Dir: dir}},
		Sessions: []model.Session{{ID: "s1", Project: "p1", Layout: &model.Layout{Pane: "a1"},
			Panes: []model.Pane{{ID: "a1", Dir: dir}, {ID: "w1", URL: "https://example.com/"}}}}}
	d, _, _ := offlineDaemon(t, false, st)
	os.WriteFile(d.historyPath("a1"), []byte("old-scrollback\r\n"), 0o600)
	wake := func(id string) error {
		raw, _ := json.Marshal(proto.IDParams{ID: id})
		_, err := d.Handle(context.Background(), "pane.wake", raw)
		return err
	}
	if d.reg.Get("a1") != nil {
		t.Fatal("a1 runs before it is woken")
	}
	if err := wake("a1"); err != nil {
		t.Fatal(err)
	}
	p := d.reg.Get("a1")
	if p == nil {
		t.Fatal("a1 did not start")
	}
	if err := wake("a1"); err != nil || d.reg.Get("a1") != p {
		t.Fatalf("waking a running pane: %v", err)
	}
	if proto.CodeOf(wake("nope")) != proto.CodeNotFound || wake("w1") == nil {
		t.Fatal("woke a pane that is none, or a page")
	}
	waitFor(t, "history", func() bool { return p.Text(true, 0) != "" })
}

// pipeAttach attaches as the app's window does, or as a web page, and
// reads what comes; it returns the connection to send frames on.
func pipeAttach(t *testing.T, d *Daemon, pane string, web bool) net.Conn {
	t.Helper()
	var conn net.Conn
	if web {
		conn = d.AttachPipe(pane)
	} else {
		ours, theirs := net.Pipe()
		go func() {
			defer ours.Close()
			d.Attach(ours, bufio.NewReader(ours), proto.Hello{Kind: "attach", Version: proto.Version, Pane: pane}, proto.Welcome{Version: proto.Version})
		}()
		conn = theirs
	}
	br := bufio.NewReader(conn)
	if _, err := br.ReadBytes('\n'); err != nil {
		t.Fatal(err)
	}
	go io.Copy(io.Discard, br)
	t.Cleanup(func() { conn.Close() })
	return conn
}

// A web page fills the browser with the pane; as it goes, the pane takes
// the window's size back, which the window would not ask again.
func TestWebGivesSizeBack(t *testing.T) {
	d, _ := startDaemon(t, 0)
	params, _ := json.Marshal(StartParams{ID: "p1", Command: []string{"sh", "-c", "exec cat"}, Exec: true, Cols: 80, Rows: 24})
	if _, err := d.Handle(context.Background(), "pane.start", params); err != nil {
		t.Fatal(err)
	}
	size := func() [2]int {
		c, r := d.reg.Get("p1").Term().Size()
		return [2]int{c, r}
	}
	resize := func(conn net.Conn, cols, rows int) {
		conn.Write(proto.AppendFrame(nil, proto.FrameResize, proto.Uint16s(cols, rows, 9, 18)))
	}
	window := pipeAttach(t, d, "p1", false)
	resize(window, 100, 30)
	waitFor(t, "the window's size", func() bool { return size() == [2]int{100, 30} })
	page := pipeAttach(t, d, "p1", true)
	resize(page, 60, 20)
	waitFor(t, "the page's size", func() bool { return size() == [2]int{60, 20} })
	page.Close()
	waitFor(t, "the window's size back", func() bool { return size() == [2]int{100, 30} })

	// A page that never sized the pane leaves it be.
	resize(window, 90, 28)
	waitFor(t, "the window's new size", func() bool { return size() == [2]int{90, 28} })
	pipeAttach(t, d, "p1", true).Close()
	page = pipeAttach(t, d, "p1", true)
	resize(page, 50, 16)
	waitFor(t, "the page's size", func() bool { return size() == [2]int{50, 16} })
	resize(window, 91, 29) // the window sized it since
	waitFor(t, "the window's size", func() bool { return size() == [2]int{91, 29} })
	page.Close()
	waitFor(t, "the window's size kept", func() bool { return size() == [2]int{91, 29} })
}

// While web access serves, the daemon stays with no pane and no client:
// a browser may come for the sessions.
func TestWebKeepsDaemon(t *testing.T) {
	d, _ := startDaemon(t, 300*time.Millisecond)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	set := func(on bool) {
		d.webStore().SetConfig(func(c *web.Config) { c.Enabled, c.Port = on, port })
		if _, err := d.Handle(context.Background(), "web.reload", nil); err != nil {
			t.Fatal(err)
		}
	}
	set(true)
	time.Sleep(time.Second)
	select {
	case <-d.Done():
		t.Fatal("exited while serving web access")
	default:
	}
	set(false)
	select {
	case <-d.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("did not exit when idle")
	}
}
