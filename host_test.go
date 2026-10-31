package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/client"
	"omaterm/internal/ctl"
	"omaterm/internal/model"
	"omaterm/internal/procs"
	"omaterm/internal/proto"
	"omaterm/internal/session"
	"omaterm/internal/worktree"
	"omaterm/service"
)

// The control socket drives the app's sessions as the omaterm command
// does: make one, split it, type into a pane, read it, wait for its
// command, close it.
func TestControlSocket(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.host = newLocalHost(a)
	s, err := ctl.Listen(filepath.Join(t.TempDir(), "omaterm.sock"), a.host, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a.ctl = s
	c, err := client.Dial(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var made proto.Created
	if err := c.Call("create", proto.CreateParams{Name: "probe", Dir: a.workspace, Command: []string{"sh", "-c", "echo HELLO_SOCKET; sleep 0.3; exit 3"}}, &made); err != nil {
		t.Fatal(err)
	}
	var info proto.Pane
	if err := c.Call("wait", proto.WaitParams{ID: made.Pane, Exit: true, TimeoutMS: 10000}, &info); err != nil {
		t.Fatal(err)
	}
	if info.CommandExit == nil || *info.CommandExit != 3 {
		t.Fatalf("command exit %+v", info.CommandExit)
	}
	var out proto.ReadResult
	if err := c.Call("read", proto.ReadParams{ID: made.Pane, Scrollback: true}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text, "HELLO_SOCKET") {
		t.Fatalf("read %q", out.Text)
	}

	var split proto.Created
	if err := c.Call("split", proto.CreateParams{Split: made.Pane, Direction: "down", Command: []string{"cat"}}, &split); err != nil {
		t.Fatal(err)
	}
	if split.Session != made.Session {
		t.Fatalf("split went to %s", split.Session)
	}
	if err := c.Call("send", proto.SendParams{ID: split.Pane, Data: "typed-through-socket\r"}, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.Call("read", proto.ReadParams{ID: split.Pane}, &out)
		if strings.Contains(out.Text, "typed-through-socket") || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(out.Text, "typed-through-socket") {
		t.Fatalf("send/read %q", out.Text)
	}

	var st proto.State
	if err := c.Call("state", nil, &st); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range st.Sessions {
		if s.ID == made.Session {
			found = len(s.Panes) == 2 && s.Name == "probe"
		}
	}
	if !found {
		t.Fatalf("state: %+v", st.Sessions)
	}

	if err := c.Call("rename", proto.RenameParams{ID: made.Session, Name: "renamed"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call("close", proto.IDParams{ID: made.Session}, nil); err != nil {
		t.Fatal(err)
	}
	if a.panes.Get(made.Pane) != nil || a.panes.Get(split.Pane) != nil {
		t.Fatal("closing the session left its panes")
	}
	if proto.CodeOf(c.Call("send", proto.SendParams{ID: "nope", Data: "x"}, nil)) != proto.CodeNotFound {
		t.Fatal("send to no pane")
	}
	if proto.CodeOf(c.Call("do", proto.DoParams{Action: "view.toggleSidebar"}, nil)) != proto.CodeRemoteControl {
		t.Fatal("do without the agents' leave")
	}
}

func TestSplitAt(t *testing.T) {
	root := splitAt(&splitNode{TermID: "a"}, "a", "b", splitHorizontal, true, 0.3)
	if root.Dir != splitHorizontal || root.First.TermID != "b" || root.Ratio != 30 {
		t.Fatalf("%+v", root)
	}
}

// An agent's hook finds its pane by its process's ancestry, and the pane
// keeps the agent's session to resume.
func TestAgentSessionByPID(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.termCmd = []string{"sh", "-c", "sleep 30"}
	id := a.activeSplitLeaf()
	if a.term(id) == nil {
		t.Fatal("no pane")
	}
	pid := a.panes.Get(id).Info().PID
	v, err := a.agentSession(proto.AgentSessionParams{PID: pid, Agent: "claude", Session: "abc-123"})
	if err != nil {
		t.Fatal(err)
	}
	if r := v.(proto.AgentSessionResult); r.Pane != id || r.Away {
		t.Fatalf("%+v", r)
	}
	if s := a.panes.Get(id).Info().AgentSession; s == nil || s.ID != "abc-123" {
		t.Fatalf("session %+v", s)
	}
	if _, err := a.agentSession(proto.AgentSessionParams{PID: pid, Agent: "claude", Session: "bad;id"}); err == nil {
		t.Fatal("accepted a bad id")
	}
	// Told each turn, as Antigravity's hook does: the same changes nothing,
	// another conversation replaces it.
	for _, sid := range []string{"abc-123", "def-456"} {
		if _, err := a.agentSession(proto.AgentSessionParams{PID: pid, Agent: "agy", Session: sid}); err != nil {
			t.Fatal(err)
		}
		if s := a.panes.Get(id).Info().AgentSession; s == nil || s.Agent != "agy" || s.ID != sid {
			t.Fatalf("session %+v, want agy %s", s, sid)
		}
	}
}

// Web access wakes a pane the window has not shown since it opened, which
// does not run yet.
func TestWakePane(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	id := newID()
	a.tabs = append(a.tabs, topTab{ID: newID(), Kind: tabTerminal, Label: "unshown", Split: &splitNode{TermID: id}})
	if a.panes.Get(id) != nil {
		t.Fatal("runs before it is woken")
	}
	if err := a.wakePane(id); err != nil {
		t.Fatal(err)
	}
	p := a.panes.Get(id)
	if p == nil {
		t.Fatal("did not start")
	}
	if err := a.wakePane(id); err != nil || a.panes.Get(id) != p {
		t.Fatalf("waking a running pane: %v", err)
	}
	if proto.CodeOf(a.wakePane("nope")) != proto.CodeNotFound {
		t.Fatal("woke a pane that is none")
	}
}

// omaterm new --worktree makes the session in a worktree of its project's
// repository, in the same project, and saves it; the worktree takes one
// session, and a split none of its own.
func TestCreateWorktreeOverSocket(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.host = newLocalHost(a)
	s, err := ctl.Listen(filepath.Join(t.TempDir(), "omaterm.sock"), a.host, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := client.Dial(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	p := proto.CreateParams{Dir: a.workspace, Command: []string{"sleep", "30"}, Worktree: &proto.WorktreeParams{Branch: "feat/x"}}
	var made proto.Created
	if err := c.Call("create", p, &made); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(a.workspace, ".worktrees", "feat-x")
	if a.paneDirs[made.Pane] != dir {
		t.Fatalf("pane in %q, want %q", a.paneDirs[made.Pane], dir)
	}
	var st proto.State
	if err := c.Call("state", nil, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Projects) != 1 {
		t.Fatalf("projects %+v", st.Projects)
	}
	var got *proto.Session
	for i := range st.Sessions {
		if st.Sessions[i].ID == made.Session {
			got = &st.Sessions[i]
		}
	}
	if got == nil || got.Name != "feat/x" || got.Worktree == nil || got.Worktree.Dir != dir || got.Worktree.Base != "main" {
		t.Fatalf("session %+v", got)
	}
	saved := false
	for _, s := range a.state().Sessions {
		saved = saved || (s.ID == made.Session && s.Worktree != nil && s.Worktree.Branch == "feat/x")
	}
	if !saved {
		t.Fatal("the worktree isn't saved")
	}

	if err := c.Call("create", p, &made); err == nil || !strings.Contains(err.Error(), "has a session already") {
		t.Fatalf("second session: %v", err)
	}
	p.Split = made.Pane
	if err := c.Call("split", p, &made); err == nil {
		t.Fatal("split with a worktree")
	}
}

// A session's panes start in its worktree: a split too, which has no
// folder of its own; with the worktree gone, they don't start at all.
func TestWorktreePanesStartInIt(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	wt := filepath.Join(a.workspace, ".worktrees", "x")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	a.tabs[a.activeTab].Worktree = &model.Worktree{Dir: wt, Branch: "x", Base: "main"}
	split := a.splitPane(a.activeSplitLeaf(), splitVertical)
	p, err := a.startRestored(split, a.tabs[a.activeTab].ID, a.workspace)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Info().Dir; got != wt {
		t.Fatalf("split started in %q, want %q", got, wt)
	}

	os.RemoveAll(wt)
	other := a.splitPane(split, splitHorizontal)
	if _, err := a.startRestored(other, a.tabs[a.activeTab].ID, a.workspace); err == nil || a.panes.Get(other) != nil {
		t.Fatalf("started with the worktree gone: %v", err)
	}
	if !a.paneFailed[other] {
		t.Fatal("not marked failed")
	}
}

// The Git panel shows the worktree of the session the user goes to, and
// the project's checkout again for a session there; the project's branch
// stays the checkout's.
func TestGitPanelFollowsWorktree(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	wt, err := worktree.Add(context.Background(), a.workspace, "x", "", false)
	if err != nil {
		t.Fatal(err)
	}
	a.newTerminalTabReal()
	a.tabs[a.activeTab].Worktree = &model.Worktree{Dir: wt.Dir, Branch: "x", Base: "main"}
	r, err := service.RepoStatus(wt.Dir, a.workspace)
	if err != nil {
		t.Fatal(err)
	}
	a.applyRepos(a.workspace, append(a.repos, *r))
	a.followWorktreeRepo()
	if got := a.activeRepoRef().Root; got != wt.Dir {
		t.Fatalf("panel on %s, want the worktree", got)
	}
	a.applyRepos(a.workspace, a.repos)
	if b := a.project().Branch; b != "main" {
		t.Fatalf("project branch %q", b)
	}
	a.followWorktreeRepo()
	a.activeTab = 0
	a.followWorktreeRepo()
	if got := a.activeRepoRef().Root; got != a.workspace {
		t.Fatalf("panel on %s, want the checkout", got)
	}
}

// New Agent, in a worktree, says what is wrong with a branch's name, and
// with a good one makes the worktree and its session, in the project.
func TestNewWorktreeDialog(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.wtAgent = "shell"
	a.openWorktreeDialog(a.active)
	if a.wtNew == nil || a.wtNew.base != "main" {
		t.Fatalf("dialog %+v", a.wtNew)
	}
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("New Agent") || !a.wtNew.worktree {
		t.Fatal("no dialog, or not in a worktree")
	}
	tr.Type("a b")
	tr.Frame()
	if !tr.HasText("A branch's name has no spaces") {
		t.Fatalf("no word of the space: %v", tr.Texts())
	}
	for range 3 {
		tr.Key(0, ui.KeyBackspace)
	}
	tr.Type("feat/x")
	tr.Key(0, ui.KeyEnter)
	tr.Frame()
	if a.wtNew != nil {
		t.Fatalf("dialog still open: %+v", a.wtNew)
	}
	dir := filepath.Join(a.workspace, ".worktrees", "feat-x")
	pi, tab := a.worktreeSession(dir)
	if tab == nil || pi != a.active || tab.Label != "feat/x" || tab.Worktree.Base != "main" {
		t.Fatalf("session %+v in project %d", tab, pi)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}

// New Agent with Work in a worktree off starts the agent in the project's
// own folder, makes no worktree, and remembers the choice; in a folder git
// doesn't know, the switch is off and can't be turned on.
func TestNewAgentWithoutWorktree(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.wtAgent = "shell"
	a.openWorktreeDialog(a.active)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("Branch") {
		t.Fatal("in a git repository, the dialog starts in a worktree, as last time")
	}
	if err := tr.Click("wt-use"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if a.wtNew.worktree || tr.HasText("Branch") {
		t.Fatal("the switch did not turn the worktree off")
	}
	sessions := len(a.tabs)
	if err := tr.Click("Start"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if a.wtNew != nil || len(a.tabs) != sessions+1 {
		t.Fatalf("no new session: dialog=%+v tabs=%d", a.wtNew, len(a.tabs))
	}
	tab := a.tabs[a.activeTab]
	if tab.Kind != tabTerminal || tab.Worktree != nil || a.paneDirs[tab.Split.TermID] != a.workspace {
		t.Fatalf("the agent's session: %+v in %q", tab, a.paneDirs[tab.Split.TermID])
	}
	if _, err := os.Stat(filepath.Join(a.workspace, ".worktrees")); err == nil {
		t.Fatal("a worktree was made")
	}
	if a.wtUse {
		t.Fatal("the choice of no worktree was not remembered")
	}

	a.projects = append(a.projects, project{ID: "plain", Name: "plain", Path: t.TempDir()})
	a.wtUse = true
	a.openWorktreeDialog(len(a.projects) - 1)
	tr.Frame()
	if a.wtNew == nil || a.wtNew.git || a.wtNew.worktree || !tr.HasText("plain isn't in a git repository, which a worktree needs.") {
		t.Fatalf("outside git: %+v", a.wtNew)
	}
	if err := tr.Click("wt-use"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if a.wtNew.worktree {
		t.Fatal("the worktree switch turned on outside git")
	}
}

// Closing a worktree's session takes the worktree along when nothing in
// it is missing from its base; with work in it, it asks to keep it or
// discard it. Closed by the omaterm command, it stays.
func TestCloseWorktreeSession(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	start := func(branch string) (worktree.Info, string) {
		t.Helper()
		wt, err := worktree.Add(context.Background(), a.workspace, branch, "", false)
		if err != nil {
			t.Fatal(err)
		}
		c, err := a.createWorktreeSession(wt, proto.CreateParams{Project: a.project().ID, Command: []string{"sleep", "30"}})
		if err != nil {
			t.Fatal(err)
		}
		return wt, c.Session
	}
	index := func(id string) int {
		for i, t := range a.tabs {
			if t.ID == id {
				return i
			}
		}
		return -1
	}
	exists := func(dir string) bool { _, err := os.Stat(dir); return err == nil }

	clean, id := start("clean")
	a.closeTabAsking(index(id))
	if a.closeAsk != nil || index(id) >= 0 || exists(clean.Dir) {
		t.Fatalf("clean: asked %v, tab %d, folder kept %v", a.closeAsk != nil, index(id), exists(clean.Dir))
	}

	kept, id := start("kept")
	os.WriteFile(filepath.Join(kept.Dir, "new.txt"), []byte("work\n"), 0o644)
	a.closeTabAsking(index(id))
	if a.closeAsk == nil || a.closeAsk.alt != "Keep Worktree" || !strings.Contains(a.closeAsk.text, "1 uncommitted file not in main") {
		t.Fatalf("with work: %+v", a.closeAsk)
	}
	a.closeAsk.altRun()
	a.closeAsk = nil
	if index(id) >= 0 || !exists(kept.Dir) {
		t.Fatalf("keep: tab %d, folder kept %v", index(id), exists(kept.Dir))
	}

	gone, id := start("gone")
	a.discardWorktreeAsking(a.tabs[index(id)])
	if a.closeAsk == nil || a.closeAsk.confirm != "Discard" {
		t.Fatalf("discard: %+v", a.closeAsk)
	}
	a.closeAsk.run()
	if index(id) >= 0 || exists(gone.Dir) {
		t.Fatalf("discarded: tab %d, folder kept %v", index(id), exists(gone.Dir))
	}

	cli, id := start("cli")
	if err := a.cliCloseSession(id); err != nil || !exists(cli.Dir) {
		t.Fatalf("omaterm close: %v, folder kept %v", err, exists(cli.Dir))
	}
}

// Merge squashes a worktree's work into its base, then closes its session
// and removes it; one that would conflict isn't made, and Update leaves
// the conflict in the worktree, for the Git panel.
func TestMergeWorktree(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	gitIn := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	gitIn(a.workspace, "commit", "-qam", "two")
	start := func(branch string) (worktree.Info, topTab) {
		t.Helper()
		wt, err := worktree.Add(context.Background(), a.workspace, branch, "", false)
		if err != nil {
			t.Fatal(err)
		}
		c, err := a.createWorktreeSession(wt, proto.CreateParams{Project: a.project().ID, Command: []string{"sleep", "30"}})
		if err != nil {
			t.Fatal(err)
		}
		_, tabs, ti, _ := a.findSession(c.Session)
		return wt, (*tabs)[ti]
	}

	wt, tab := start("feat")
	os.WriteFile(filepath.Join(wt.Dir, "feat.txt"), []byte("feat\n"), 0o644)
	gitIn(wt.Dir, "add", ".")
	gitIn(wt.Dir, "commit", "-qm", "Add feat")
	a.openMergeDialog(tab)
	st := a.wtMerge
	if st == nil || st.sum == nil || st.msg != "Add feat" || st.how != "squash" {
		t.Fatalf("dialog %+v", st)
	}
	a.mergeFromDialog(st, true)
	if a.wtMerge != nil || st.err != "" {
		t.Fatalf("still open: %q", st.err)
	}
	if log := gitIn(a.workspace, "log", "-1", "--format=%s"); log != "Add feat" {
		t.Fatalf("main's last commit %q", log)
	}
	if _, err := os.Stat(wt.Dir); err == nil {
		t.Fatal("the worktree is left")
	}
	if _, found := a.worktreeSession(wt.Dir); found != nil {
		t.Fatal("its session is left")
	}

	wt, tab = start("clash")
	os.WriteFile(filepath.Join(wt.Dir, "hello.txt"), []byte("one\nthree\n"), 0o644)
	os.WriteFile(filepath.Join(a.workspace, "hello.txt"), []byte("one\nfour\n"), 0o644)
	gitIn(a.workspace, "commit", "-qam", "four")
	a.openMergeDialog(tab)
	a.mergeFromDialog(a.wtMerge, true)
	if a.wtMerge == nil || !strings.Contains(a.wtMerge.err, "Conflicts with main in hello.txt. Choose Update from main") {
		t.Fatalf("conflict: %+v", a.wtMerge)
	}
	if s := gitIn(a.workspace, "status", "--porcelain"); s != "" {
		t.Fatalf("main checkout changed: %q", s)
	}
	a.wtMerge = nil
	a.updateWorktree(tab)
	if gitIn(wt.Dir, "status", "--porcelain") != "UU hello.txt" {
		t.Fatal("no conflict in the worktree")
	}
	if a.inspectorTab != 2 {
		t.Fatal("the Git panel isn't shown")
	}
}

// A worktree's session offers the Git tab's Branch view: what its branch
// changed since its base, committed or not, with no Revert.
func TestGitTabBranchView(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	wt, err := worktree.Add(context.Background(), a.workspace, "feat", "", false)
	if err != nil {
		t.Fatal(err)
	}
	c, err := a.createWorktreeSession(wt, proto.CreateParams{Project: a.project().ID, Command: []string{"sleep", "30"}, Focus: true})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(wt.Dir, "committed.txt"), []byte("c\n"), 0o644)
	for _, args := range [][]string{{"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@e", "commit", "-qm", "c"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = wt.Dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	os.WriteFile(filepath.Join(wt.Dir, "uncommitted.txt"), []byte("u\n"), 0o644)
	_, tabs, ti, _ := a.findSession(c.Session)
	id := (*tabs)[ti].Split.termIDs()[0]
	a.status[id] = session.Info{ID: id, Session: c.Session, Dir: wt.Dir, Status: session.Idle, Progress: -1}
	a.inspectorVisible, a.inspectorTab = true, 2
	tr := ui.NewTester(a.view, 1440, 900)
	frameUntil(t, tr, "the switch", func() bool { return tr.HasText("Branch") })
	if tr.HasText("Last turn") {
		t.Fatal("Last turn offered with no turn")
	}
	if err := tr.Click("Branch"); err != nil {
		t.Fatal(err)
	}
	frameUntil(t, tr, "the branch's files", func() bool {
		return tr.HasText("committed.txt") && tr.HasText("uncommitted.txt") && tr.HasText("· since main")
	})
	if tr.HasText("hello.txt") {
		t.Fatal("the main checkout's change shows")
	}
}

// A pane started again keeps the environment omaterm new --env gave it.
func TestRestartKeepsEnv(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	c := dialApp(t, a)
	var made proto.Created
	if err := c.Call("create", proto.CreateParams{Name: "dev", Dir: a.workspace, Env: []string{"PROBE_ENV=kept"},
		Command: []string{"sh", "-c", "echo env=$PROBE_ENV; sleep 30"}}, &made); err != nil {
		t.Fatal(err)
	}
	if err := c.Call("restart", proto.IDParams{ID: made.Pane}, nil); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var out proto.ReadResult
		if err := c.Call("read", proto.ReadParams{ID: made.Pane}, &out); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.Text, "env=kept") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the restart: %q", out.Text)
		}
	}
}

// A pane is told the project it runs in.
func TestProjectEnv(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	c := dialApp(t, a)
	var made proto.Created
	if err := c.Call("create", proto.CreateParams{Name: "dev", Dir: a.workspace,
		Command: []string{"sh", "-c", "echo P=$OMATERM_PROJECT D=$OMATERM_PROJECT_DIR; sleep 30"}}, &made); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var out proto.ReadResult
		if err := c.Call("read", proto.ReadParams{ID: made.Pane}, &out); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.Text, "P=test D="+a.workspace) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("read %q", out.Text)
		}
	}
}

// omaterm send waits for the user to finish typing in the pane, and is
// no typing of theirs.
func TestSendWaitsForTheUser(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	c := dialApp(t, a)
	var made proto.Created
	if err := c.Call("create", proto.CreateParams{Name: "dev", Dir: a.workspace, Command: []string{"cat"}}, &made); err != nil {
		t.Fatal(err)
	}
	a.panes.Get(made.Pane).Write([]byte("typed-")) // as the keyboard
	start := time.Now()
	if err := c.Call("send", proto.SendParams{ID: made.Pane, Data: "sent\r"}, nil); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < time.Second {
		t.Fatal("sent while the user typed")
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var out proto.ReadResult
		if err := c.Call("read", proto.ReadParams{ID: made.Pane}, &out); err != nil {
			t.Fatal(err)
		}
		if strings.Count(out.Text, "typed-sent") >= 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("read %q", out.Text)
		}
	}
}

// The process.* methods work over the app's socket, which the omaterm
// command talks to while the window is open: here with the app's own
// host, as without a daemon.
func TestProcessMethodsOverSocket(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.useLocalProcHost()
	local := a.procHost.(*localProcs)
	defer local.Close()
	web := procs.Def{Name: "web", Command: "sh -c 'echo up; sleep 30'"}
	if err := os.WriteFile(filepath.Join(a.workspace, procs.FileName), []byte("processes:\n  web:\n    command: \"sh -c 'echo up; sleep 30'\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := dialApp(t, a)
	project := a.projects[a.active].ID
	var list []proto.Process
	if err := c.Call("process.list", proto.ProcessParams{Project: project}, &list); err != nil || len(list) != 1 || list[0].Status != procs.Untrusted {
		t.Fatalf("list: %+v, %v", list, err)
	}
	if err := c.Call("process.start", proto.ProcessParams{Project: project, Name: "web"}, nil); err == nil || !strings.Contains(err.Error(), "isn't trusted") {
		t.Fatalf("an untrusted start: %v", err)
	}
	if err := procs.OpenTrust(procs.TrustPath()).Add(a.workspace, web.Variant()); err != nil {
		t.Fatal(err)
	}
	var proc proto.Process
	if err := c.Call("process.sync", proto.ProcessParams{Project: a.projects[a.active].Name}, &list); err != nil || len(list) != 1 || !list[0].Trusted {
		t.Fatalf("sync by name: %+v, %v", list, err)
	}
	if err := c.Call("process.start", proto.ProcessParams{Project: project, Name: "web"}, &proc); err != nil || proc.Status != procs.Starting || proc.Pane == "" {
		t.Fatalf("start: %+v, %v", proc, err)
	}
	if err := c.Call("process.stop", proto.ProcessParams{Project: project, Name: "web"}, &proc); err != nil || proc.Status != procs.Stopped {
		t.Fatalf("stop: %+v, %v", proc, err)
	}
	if err := c.Call("process.start", proto.ProcessParams{Project: project, Name: "nope"}, nil); err == nil {
		t.Fatal("an unknown process started")
	}
}
