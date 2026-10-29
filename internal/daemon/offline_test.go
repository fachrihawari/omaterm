//go:build linux || darwin

package daemon

import (
	"encoding/json"
	"os"
	"os/exec"
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

// With the window closed, omaterm new --worktree makes the worktree and
// its session in the project, saved for the app.
func TestWorktreeWithWindowClosed(t *testing.T) {
	d, socket, proj := offlineDaemon(t, false, nil)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"commit", "-q", "--allow-empty", "-m", "first"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = proj
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	c, err := client.Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	p := proto.CreateParams{Dir: proj, Worktree: &proto.WorktreeParams{Branch: "x"}}
	made := call[proto.Created](t, c, "create", p)
	dir := filepath.Join(proj, ".worktrees", "x")
	saved, err := model.Load(d.stateFile())
	if err != nil || len(saved.Projects) != 1 || len(saved.Sessions) != 1 {
		t.Fatalf("state.json: %+v, %v", saved, err)
	}
	s := saved.Sessions[0]
	if s.ID != made.Session || s.Project != "p1" || s.Name != "x" || s.Panes[0].Dir != dir ||
		s.Worktree == nil || *s.Worktree != (model.Worktree{Dir: dir, Branch: "x", Base: "main"}) {
		t.Fatalf("session %+v, worktree %+v", s, s.Worktree)
	}
	if st := call[proto.State](t, c, "state", nil); st.Sessions[0].Worktree == nil {
		t.Fatal("state doesn't tell the worktree")
	}
	if err := c.Call("create", p, nil); err == nil || !strings.Contains(err.Error(), "has a session already") {
		t.Fatalf("second session: %v", err)
	}
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

// A session whose worktree was deleted doesn't come back in the home
// folder; one whose folder in it was, comes back in the worktree.
func TestRestoreWorktreeGone(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	dir := t.TempDir()
	gone, kept := filepath.Join(dir, ".worktrees", "gone"), filepath.Join(dir, ".worktrees", "kept")
	os.MkdirAll(kept, 0o755)
	st := &model.State{Version: model.Version, Projects: []model.Project{{ID: "p1", Name: "x", Dir: dir}},
		Sessions: []model.Session{
			{ID: "s1", Project: "p1", Layout: &model.Layout{Pane: "a1"}, Panes: []model.Pane{{ID: "a1", Dir: gone}},
				Worktree: &model.Worktree{Dir: gone, Branch: "gone", Base: "main"}},
			{ID: "s2", Project: "p1", Layout: &model.Layout{Pane: "b1"}, Panes: []model.Pane{{ID: "b1", Dir: filepath.Join(kept, "sub")}},
				Worktree: &model.Worktree{Dir: kept, Branch: "kept", Base: "main"}},
		}}
	d, _, _ := offlineDaemon(t, false, st)
	d.restore()
	if d.reg.Get("a1") != nil {
		t.Fatal("a pane of a deleted worktree started")
	}
	p := d.reg.Get("b1")
	if p == nil {
		t.Fatal("the kept worktree's pane didn't start")
	}
	if got := p.Info().Dir; got != kept {
		t.Fatalf("started in %q, want %q", got, kept)
	}
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

// A state.json that is there but can't be read is never written over:
// it holds every session. 0.9.1 replaced it with an empty one.
func TestBrokenStateLeftAlone(t *testing.T) {
	d, socket, proj := offlineDaemon(t, false, nil)
	broken := []byte(`{"version": 2, "projects": [`)
	if err := os.WriteFile(d.stateFile(), broken, 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := client.Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Call("create", proto.CreateParams{Name: "work", Dir: proj}, nil); err == nil {
		t.Fatal("create went ahead on a broken state.json")
	}
	if got, _ := os.ReadFile(d.stateFile()); string(got) != string(broken) {
		t.Fatalf("state.json was written over: %s", got)
	}
}

// A call passed to the app fails as soon as the app leaves without
// answering; the omaterm command waited ten minutes.
func TestForwardedCallFailsWhenAppLeaves(t *testing.T) {
	_, socket, proj := offlineDaemon(t, false, nil)
	app, err := client.Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Call("subscribe", nil, nil); err != nil {
		t.Fatal(err)
	}
	cli, err := client.Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	done := make(chan error, 1)
	go func() { done <- cli.Call("create", proto.CreateParams{Name: "x", Dir: proj}, nil) }()
	time.Sleep(200 * time.Millisecond) // forwarded, unanswered
	app.Close()
	select {
	case err := <-done:
		if proto.CodeOf(err) != proto.CodeAppClosed {
			t.Fatalf("got %v, want the app-closed error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the call still waits for the app that left")
	}
}

// With the window closed, restart runs the pane's command again; it
// started a bare shell.
func TestOfflineRestartKeepsCommand(t *testing.T) {
	d, socket, proj := offlineDaemon(t, false, nil)
	c, err := client.Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cmd := []string{"sh", "-c", "echo again env=$PROBE_ENV; sleep 30"}
	made := call[proto.Created](t, c, "create", proto.CreateParams{Name: "dev", Dir: proj, Command: cmd, Env: []string{"PROBE_ENV=kept"}})
	call[bool](t, c, "restart", proto.IDParams{ID: made.Pane})
	p := d.reg.Get(made.Pane)
	if p == nil {
		t.Fatal("no pane after the restart")
	}
	if got := p.Info().Command; strings.Join(got, " ") != strings.Join(cmd, " ") {
		t.Fatalf("restarted with %q, want %q", got, cmd)
	}
	// And with the environment it was given.
	waitFor(t, "the restarted command's output", func() bool {
		return strings.Contains(call[proto.ReadResult](t, c, "read", proto.ReadParams{ID: made.Pane}).Text, "env=kept")
	})
}

// With the window closed, a session made from an agent's pane is its
// session's child, saved for the app; closing the parent frees it.
func TestParentWithWindowClosed(t *testing.T) {
	d, socket, proj := offlineDaemon(t, false, nil)
	c, err := client.Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	lead := call[proto.Created](t, c, "create", proto.CreateParams{Dir: proj, Name: "lead"})
	child := call[proto.Created](t, c, "create", proto.CreateParams{Dir: proj, Name: "w1", By: lead.Pane})
	saved, err := model.Load(d.stateFile())
	if err != nil {
		t.Fatal(err)
	}
	if i := sessionIndex(&saved, child.Session); i < 0 || saved.Sessions[i].Parent != lead.Session {
		t.Fatalf("saved %+v", saved.Sessions)
	}
	call[bool](t, c, "close", proto.IDParams{ID: lead.Session})
	for _, s := range call[proto.State](t, c, "state", nil).Sessions {
		if s.ID == child.Session && s.Parent != "" {
			t.Fatalf("parent %q after it closed", s.Parent)
		}
	}
}

// With the window closed, a worker's branch diff is what it changed since
// its lead's branch, which it was made from, and the lead merges it.
func TestBranchDiffWithWindowClosed(t *testing.T) {
	_, socket, proj := offlineDaemon(t, false, nil)
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	commit := func(dir, name string) {
		os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644)
		git(dir, "add", name)
		git(dir, "commit", "-qm", "Add "+name)
	}
	git(proj, "init", "-q", "-b", "main")
	git(proj, "config", "user.name", "t") // the daemon's merge commits
	git(proj, "config", "user.email", "t@example.com")
	commit(proj, "first.txt")
	c, err := client.Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	lead := call[proto.Created](t, c, "create", proto.CreateParams{Dir: proj, Worktree: &proto.WorktreeParams{Branch: "feat"}})
	leadDir := filepath.Join(proj, ".worktrees", "feat")
	commit(leadDir, "lead.txt")
	worker := call[proto.Created](t, c, "create", proto.CreateParams{Dir: leadDir, By: lead.Pane, Worktree: &proto.WorktreeParams{Branch: "w1"}})
	commit(filepath.Join(proj, ".worktrees", "w1"), "worker.txt")
	out := call[proto.ReadResult](t, c, "git.diff", proto.GitParams{Pane: worker.Pane, Base: "branch"})
	if !strings.Contains(out.Text, "worker.txt") || strings.Contains(out.Text, "lead.txt") {
		t.Fatalf("diff %q", out.Text)
	}
	for _, s := range call[proto.State](t, c, "state", nil).Sessions {
		if s.ID == worker.Session && (s.Parent != lead.Session || s.Worktree == nil || s.Worktree.Base != "feat") {
			t.Fatalf("worker %+v", s)
		}
	}

	// The lead merges it: into its branch, closing it, its worktree gone.
	merged := call[proto.Merged](t, c, "worktree.merge", proto.MergeParams{By: lead.Pane, Target: worker.Session})
	if merged.Into != "feat" || !merged.Removed || len(merged.Commits) != 1 {
		t.Fatalf("merged %+v", merged)
	}
	if _, err := os.Stat(filepath.Join(leadDir, "worker.txt")); err != nil {
		t.Error("the worker's file isn't in the lead's worktree")
	}
	if _, err := os.Stat(filepath.Join(proj, ".worktrees", "w1")); err == nil {
		t.Error("the worker's worktree stays")
	}
	for _, s := range call[proto.State](t, c, "state", nil).Sessions {
		if s.ID == worker.Session {
			t.Error("the worker's session stays")
		}
	}
	// Not into the main checkout.
	plain := call[proto.Created](t, c, "create", proto.CreateParams{Dir: proj, Name: "plain"})
	if err := c.Call("worktree.merge", proto.MergeParams{By: plain.Pane, Target: lead.Session}, nil); err == nil ||
		!strings.Contains(err.Error(), "merging into main is the user's") {
		t.Fatalf("merged into main: %v", err)
	}
}

// A pane the daemon starts with the window closed is told its project,
// as the app tells the panes it starts.
func TestOfflineProjectEnv(t *testing.T) {
	d, socket, proj := offlineDaemon(t, false, nil)
	c, err := client.Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	made := call[proto.Created](t, c, "create", proto.CreateParams{Name: "dev",
		Command: []string{"sh", "-c", "echo P=$OMATERM_PROJECT D=$OMATERM_PROJECT_DIR; sleep 30"}})
	waitFor(t, "the project in the pane's environment", func() bool {
		return strings.Contains(call[proto.ReadResult](t, c, "read", proto.ReadParams{ID: made.Pane}).Text, "P=proj D="+proj)
	})
	if p := d.reg.Get(made.Pane); p == nil || p.Info().Project != "p1" {
		t.Fatalf("pane of project %q, want p1", p.Info().Project)
	}
}
