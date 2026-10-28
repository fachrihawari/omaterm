package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omaterm/internal/client"
	"omaterm/internal/ctl"
	"omaterm/internal/model"
	"omaterm/internal/proto"
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
