//go:build linux || darwin

package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omaterm/internal/client"
	"omaterm/internal/model"
	"omaterm/internal/proto"
)

// offlineDaemon starts a daemon whose state.json holds a project, as the
// app leaves it once closed.
func offlineDaemon(t *testing.T, restore bool, st *model.State) (*Daemon, string, string) {
	t.Helper()
	t.Setenv("SHELL", "/bin/sh")
	dir, err := os.MkdirTemp("", "omo")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	proj := filepath.Join(dir, "proj")
	os.Mkdir(proj, 0o700)
	if st == nil {
		st = &model.State{Version: model.Version, ActiveProject: "p1",
			Projects: []model.Project{{ID: "p1", Name: "proj", Dir: proj}}}
	}
	stateFile := filepath.Join(dir, "state.json")
	if err := model.Save(stateFile, *st); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "d.sock")
	d, err := Start(Options{Socket: socket, StateDir: dir, StateFile: stateFile, IdleExit: time.Minute,
		Version: "test", Restore: restore})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Stop)
	return d, socket, proj
}

func call[T any](t *testing.T, c *client.Client, method string, params any) T {
	t.Helper()
	var out T
	if err := c.Call(method, params, &out); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	return out
}

// With the window closed, the omaterm command makes, splits, drives and
// closes sessions through the daemon, which keeps state.json for the app.
func TestCLIWithWindowClosed(t *testing.T) {
	d, socket, proj := offlineDaemon(t, false, nil)
	c, err := client.Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	made := call[proto.Created](t, c, "create", proto.CreateParams{Name: "work"})
	split := call[proto.Created](t, c, "split", proto.CreateParams{Split: made.Pane, Direction: "down",
		Command: []string{"sh", "-c", "echo from-split; exit 3"}})
	if split.Session != made.Session {
		t.Fatalf("split went to session %s, want %s", split.Session, made.Session)
	}
	st := call[proto.State](t, c, "state", nil)
	if len(st.Sessions) != 1 || len(st.Sessions[0].Panes) != 2 || st.Sessions[0].Name != "work" {
		t.Fatalf("state: %+v", st.Sessions)
	}
	if l := st.Sessions[0].Layout; l == nil || l.Row || l.A.Pane != made.Pane || l.B.Pane != split.Pane {
		t.Fatalf("layout %+v, want %s above %s", l, made.Pane, split.Pane)
	}
	if st.Sessions[0].Panes[0].WorkDir() != proj && st.Sessions[0].Panes[0].Dir != proj {
		t.Fatalf("pane dir %q, want %q", st.Sessions[0].Panes[0].Dir, proj)
	}

	info := call[proto.Pane](t, c, "wait", proto.WaitParams{ID: split.Pane, Exit: true, TimeoutMS: 10000})
	if info.CommandExit == nil || *info.CommandExit != 3 {
		t.Fatalf("command exit %v, want 3", info.CommandExit)
	}
	call[bool](t, c, "send", proto.SendParams{ID: made.Pane, Data: "echo typed-offline\n"})
	waitFor(t, "echo", func() bool {
		return strings.Count(call[proto.ReadResult](t, c, "read", proto.ReadParams{ID: made.Pane}).Text, "typed-offline") >= 2
	})

	// The app reads it all at its next launch.
	saved, err := model.Load(d.stateFile())
	if err != nil || len(saved.Sessions) != 1 || len(saved.Sessions[0].Panes) != 2 {
		t.Fatalf("state.json: %+v, %v", saved, err)
	}

	call[bool](t, c, "pane.close", proto.IDParams{ID: split.Pane})
	st = call[proto.State](t, c, "state", nil)
	if l := st.Sessions[0].Layout; l == nil || l.Pane != made.Pane {
		t.Fatalf("layout after close %+v", l)
	}
	if err := c.Call("focus", proto.IDParams{ID: made.Session}, nil); proto.CodeOf(err) != proto.CodeAppClosed {
		t.Fatalf("focus: %v, want app_closed", err)
	}
	if err := c.Call("browser", proto.BrowserParams{ID: made.Pane, Op: "url"}, nil); proto.CodeOf(err) != proto.CodeAppClosed {
		t.Fatalf("browser: %v, want app_closed", err)
	}
	call[bool](t, c, "close", proto.IDParams{ID: made.Session})
	if st = call[proto.State](t, c, "state", nil); len(st.Sessions) != 0 || d.reg.Len() != 0 {
		t.Fatalf("after close: %d sessions, %d panes", len(st.Sessions), d.reg.Len())
	}
}

// While the app follows the daemon, the app answers the omaterm command:
// the daemon leaves state.json alone.
func TestAppOwnsStateWhileOpen(t *testing.T) {
	_, socket, _ := offlineDaemon(t, false, nil)
	r, err := Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	c, err := client.Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Call("create", proto.CreateParams{}, nil); proto.CodeOf(err) != proto.CodeAppClosed {
		t.Fatalf("create with the app open: %v", err)
	}
	cl := call[proto.Clients](t, c, "clients", nil)
	if cl.Apps != 1 {
		t.Fatalf("clients %+v, want one app", cl)
	}
}

// New panes take the app's display variables over the daemon's.
func TestEnvUpdate(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "stale-0")
	_, socket, _ := offlineDaemon(t, false, nil)
	c, err := client.Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	call[bool](t, c, "env.update", map[string]string{"WAYLAND_DISPLAY": "fresh-1", "LD_PRELOAD": "evil.so"})
	made := call[proto.Created](t, c, "create", proto.CreateParams{
		Command: []string{"sh", "-c", `echo "wl=$WAYLAND_DISPLAY pre=$LD_PRELOAD."`}})
	waitFor(t, "env echo", func() bool {
		return strings.Contains(call[proto.ReadResult](t, c, "read", proto.ReadParams{ID: made.Pane}).Text, "wl=fresh-1 pre=.")
	})
}

// A daemon started as a service starts the saved sessions from their
// scrollback, resuming agents.
func TestServiceRestores(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	dir := t.TempDir()
	st := &model.State{Version: model.Version, Projects: []model.Project{{ID: "p1", Name: "x", Dir: dir}},
		Sessions: []model.Session{{ID: "s1", Project: "p1", Layout: &model.Layout{Pane: "a1"},
			Panes: []model.Pane{{ID: "a1", Dir: dir}, {ID: "w1", URL: "https://example.com/"}}}}}
	// Write the history where the daemon looks, then restore as a
	// service's start does.
	d, _, _ := offlineDaemon(t, false, st)
	os.WriteFile(d.historyPath("a1"), []byte("old-scrollback\r\n"), 0o600)
	d.restore()
	p := d.reg.Get("a1")
	if p == nil || d.reg.Get("w1") != nil {
		t.Fatalf("restored panes: a1=%v w1=%v", p != nil, d.reg.Get("w1") != nil)
	}
	waitFor(t, "history", func() bool {
		txt := p.Text(true, 0)
		return strings.Contains(txt, "old-scrollback") && strings.Contains(txt, "restored")
	})
}

// While an app follows the daemon, the omaterm command's calls that reach
// the daemon go to the app, as a browser op must: the first app to
// answer wins, and an app with its control socket off refuses.
func TestForwardsToApp(t *testing.T) {
	_, socket, _ := offlineDaemon(t, false, nil)
	slow, err := Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	fast, err := Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer fast.Close()
	slow.OnCall = func(method string, _ json.RawMessage) (any, error) {
		time.Sleep(500 * time.Millisecond)
		return proto.BrowserResult{Text: "slow"}, nil
	}
	fast.OnCall = func(method string, raw json.RawMessage) (any, error) {
		var p proto.BrowserParams
		json.Unmarshal(raw, &p)
		return proto.BrowserResult{Text: method + ":" + p.Op}, nil
	}
	c, err := client.Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	got := call[proto.BrowserResult](t, c, "browser", proto.BrowserParams{ID: "x", Op: "title"})
	if got.Text != "browser:title" {
		t.Fatalf("answer %q, want the fast app's", got.Text)
	}
	fast.OnCall = func(string, json.RawMessage) (any, error) {
		return nil, proto.Errorf(proto.CodeAppClosed, "control socket off")
	}
	slow.Close()
	if err := c.Call("state", nil, nil); proto.CodeOf(err) != proto.CodeAppClosed {
		t.Fatalf("refused call: %v", err)
	}
}
