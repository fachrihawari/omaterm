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
