//go:build linux || darwin

package daemon

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"omaterm/internal/model"
	"omaterm/internal/session"
)

type sent struct{ title, body, session string }

// notifyDaemon is a daemon whose notifications go to a channel.
func notifyDaemon(t *testing.T) (*Daemon, string, chan sent) {
	t.Helper()
	t.Setenv("SHELL", "/bin/sh")
	dir, err := os.MkdirTemp("", "omn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	st := model.State{Version: model.Version, Projects: []model.Project{{ID: "p1", Name: "web", Dir: dir}},
		Sessions: []model.Session{{ID: "s1", Project: "p1", Name: "deploy", Layout: &model.Layout{Pane: "a1"}, Panes: []model.Pane{{ID: "a1", Dir: dir}}}}}
	stateFile := filepath.Join(dir, "state.json")
	model.Save(stateFile, st)
	ch := make(chan sent, 4)
	socket := filepath.Join(dir, "d.sock")
	d, err := Start(Options{Socket: socket, StateDir: dir, StateFile: stateFile, IdleExit: time.Minute, Version: "test",
		Notifier: func(title, body, sid string) { ch <- sent{title, body, sid} }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Stop)
	return d, socket, ch
}

func blockedScript() string {
	msg := base64.StdEncoding.EncodeToString([]byte("Approve deploy?"))
	return `printf '\033]7501;state=working\007'; sleep 0.3; printf '\033]7501;state=blocked:kind=permission:msg=` + msg + `\033\\'; sleep 30`
}

// With the window closed, a session that needs the user notifies, titled
// as the app would.
func TestDaemonNotifiesWhileWindowClosed(t *testing.T) {
	d, _, ch := notifyDaemon(t)
	if _, err := d.reg.Start(session.StartOptions{ID: "a1", Session: "s1", Exec: true,
		Command: []string{"sh", "-c", blockedScript()}, Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-ch:
		if n.title != "deploy · web" || n.body != "Approve deploy?" || n.session != "s1" {
			t.Fatalf("notification %+v", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no notification")
	}
}

// While the app follows the daemon, the app notifies, not the daemon.
func TestDaemonQuietWithApp(t *testing.T) {
	d, socket, ch := notifyDaemon(t)
	r, err := Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Start(session.StartOptions{ID: "a1", Session: "s1", Exec: true,
		Command: []string{"sh", "-c", blockedScript()}, Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "waiting", func() bool { return d.reg.Get("a1") != nil && d.reg.Get("a1").Info().Status == session.Waiting })
	select {
	case n := <-ch:
		t.Fatalf("the daemon notified with the app open: %+v", n)
	case <-time.After(500 * time.Millisecond):
	}
}

// Once the app goes, the session it showed is no more in view: its agent
// finishing notifies.
func TestAppLeavingUnviews(t *testing.T) {
	d, socket, ch := notifyDaemon(t)
	r, err := Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Start(session.StartOptions{ID: "a1", Session: "s1", Exec: true,
		Command: []string{"sh", "-c", `read x; printf '\033]7501;state=working\007'; sleep 0.5; printf '\033]7501;state=done\007'; sleep 30`}, Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	r.SetViewed("s1")
	time.Sleep(200 * time.Millisecond)
	r.Close()
	waitFor(t, "app gone", func() bool { return !d.appAttached() })
	d.reg.Get("a1").Write([]byte("go\r"))
	select {
	case n := <-ch:
		if n.session != "s1" {
			t.Fatalf("notification %+v", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no notification for the session the app showed last")
	}
}

// A program printing its notification sequence in a loop shows one, not
// one each: thousands of notify-send processes waited half an hour each.
func TestProgramNotificationsLimited(t *testing.T) {
	d, _, ch := notifyDaemon(t)
	script := `for i in 1 2 3 4 5 6 7 8 9 10; do printf '\033]9;again\007'; done; sleep 30`
	if _, err := d.reg.Start(session.StartOptions{ID: "a1", Session: "s1", Exec: true,
		Command: []string{"sh", "-c", script}, Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	// The first comes once the shell runs, which a slow runner takes a
	// while for; the others would come right after it.
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("no notification shown")
	}
	n := 1
	for deadline := time.After(2 * time.Second); ; {
		select {
		case <-ch:
			n++
			continue
		case <-deadline:
		}
		break
	}
	if n != 1 {
		t.Errorf("%d notifications shown, want 1", n)
	}
}
