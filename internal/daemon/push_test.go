//go:build linux || darwin

package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"omaterm/internal/procs"
	"omaterm/internal/proto"
	"omaterm/internal/session"
	"omaterm/internal/web"
)

// pushDaemon is notifyDaemon whose phones' notifications go to a channel.
func pushDaemon(t *testing.T) (*Daemon, string, chan sent, chan web.Notice) {
	t.Helper()
	pushes := make(chan web.Notice, 8)
	d, socket, ch := notifyDaemon(t, func(o *Options) { o.Push = func(n web.Notice) { pushes <- n } })
	return d, socket, ch, pushes
}

func nextPush(t *testing.T, pushes chan web.Notice) web.Notice {
	t.Helper()
	select {
	case n := <-pushes:
		return n
	case <-time.After(10 * time.Second):
		t.Fatal("nothing pushed")
	}
	return web.Notice{}
}

func noPush(t *testing.T, pushes chan web.Notice) {
	t.Helper()
	select {
	case n := <-pushes:
		t.Fatalf("pushed %+v", n)
	case <-time.After(500 * time.Millisecond):
	}
}

func startScript(t *testing.T, d *Daemon, pane, script string) {
	t.Helper()
	if _, err := d.reg.Start(session.StartOptions{ID: pane, Session: "s1", Exec: true,
		Command: []string{"sh", "-c", script}, Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
}

// The phones are told with the app open too, as the desktop is not: a
// laptop left at home usually has it open.
func TestDaemonPushesWithApp(t *testing.T) {
	_, socket, ch, pushes := pushDaemon(t)
	r, err := Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Start(session.StartOptions{ID: "a1", Session: "s1", Exec: true,
		Command: []string{"sh", "-c", blockedScript()}, Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	n := nextPush(t, pushes)
	want := web.Notice{Session: "s1", Pane: "a1", Title: "deploy · web", State: "Needs permission", Words: "Approve deploy?", Urgent: true, Waiting: 1}
	if n != want {
		t.Fatalf("pushed %+v", n)
	}
	select {
	case n := <-ch:
		t.Fatalf("the desktop notified with the app open: %+v", n)
	default:
	}
}

// While someone uses the desktop nothing goes to the phones, for the
// session it shows or any other: the desktop tells them itself. With no
// input there for a while, the session it shows is told too, and its
// work done counts as unseen.
func TestDaemonPushAtDesk(t *testing.T) {
	d, _, _, pushes := pushDaemon(t)
	d.reg.SetViewed("s2")
	d.deskInput()
	startScript(t, d, "a1", blockedScript())
	waitFor(t, "waiting", func() bool { return d.reg.Get("a1").Info().Status == session.Waiting })
	startScript(t, d, "a3", `printf '\033]9;Build finished\007'; sleep 30`)
	d.processEvent(procs.Event{Kind: procs.EventFailed, Project: "p1", Name: "api", Code: 2})
	noPush(t, pushes)

	d.deskAt.Store(time.Now().Add(-3 * time.Minute).UnixMilli())
	d.reg.SetViewed("s1")
	startScript(t, d, "a2", `printf '\033]7501;state=working\007'; sleep 0.3; printf '\033]7501;state=done\007'; sleep 30`)
	if n := nextPush(t, pushes); n.State != "Done" || n.Pane != "a2" || n.Urgent || n.Waiting != 1 {
		t.Fatalf("pushed %+v", n)
	}
}

// Input from the app, or a session it turns to, is someone at the desktop;
// input from a web page is not.
func TestDaemonDeskInput(t *testing.T) {
	d, _, _, _ := pushDaemon(t)
	startScript(t, d, "a1", "sleep 30")
	if d.atDesk() {
		t.Fatal("at the desk before any input")
	}
	for _, web := range []bool{true, false} {
		ours, theirs := net.Pipe()
		go d.attach(ours, bufio.NewReader(ours), proto.Hello{Kind: "attach", Version: proto.Version, Pane: "a1"}, proto.Welcome{}, web)
		bufio.NewReader(theirs).ReadString('\n') // the welcome
		theirs.Write(proto.AppendFrame(nil, proto.FrameInput, []byte("x")))
		time.Sleep(100 * time.Millisecond)
		theirs.Close()
		if d.atDesk() != !web {
			t.Fatalf("a web page's input: %v; at the desk: %v", web, d.atDesk())
		}
	}
	d.deskAt.Store(0)
	params, _ := json.Marshal(proto.IDParams{ID: "s1"})
	d.Handle(context.Background(), "viewed", params)
	if !d.atDesk() {
		t.Fatal("turning to a session is no input")
	}
}

// A program's own notification goes under its session's name, marked as
// a program's, unless the settings keep programs quiet.
func TestDaemonPushProgram(t *testing.T) {
	d, _, _, pushes := pushDaemon(t)
	startScript(t, d, "a1", `sleep 0.3; printf '\033]9;Build finished\007'; sleep 30`)
	if n := nextPush(t, pushes); !n.Program || n.Title != "deploy · web" || n.Words != "Build finished" || n.State != "Sent a notification" {
		t.Fatalf("pushed %+v", n)
	}
	os.WriteFile(filepath.Join(filepath.Dir(d.stateFile()), "settings.json"), []byte(`{"notify":{"waiting":true,"done":true,"error":true,"programs":false}}`), 0o600)
	startScript(t, d, "a2", `sleep 0.3; printf '\033]9;Build finished\007'; sleep 30`)
	noPush(t, pushes)
}

// A process that fails is told, urgently; a tap opens the list.
func TestDaemonPushProcess(t *testing.T) {
	d, _, _, pushes := pushDaemon(t)
	d.processEvent(procs.Event{Kind: procs.EventFailed, Project: "p1", Name: "api", Code: 2})
	if n := nextPush(t, pushes); n.Title != "api" || n.State != "Failed (exit 2)" || !n.Urgent || n.Pane != "" {
		t.Fatalf("pushed %+v", n)
	}
	d.processEvent(procs.Event{Kind: procs.EventStarted, Project: "p1", Name: "api"})
	noPush(t, pushes)
}
