package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/cli"
	"omaterm/internal/client"
	"omaterm/internal/ctl"
	"omaterm/internal/daemon"
	"omaterm/internal/proto"
	"omaterm/internal/session"
	"omaterm/internal/worktree"
)

// dialApp serves a's control socket and connects to it, as the omaterm
// command does.
func dialApp(t *testing.T, a *appState) *client.Client {
	t.Helper()
	a.host = newLocalHost(a)
	s, err := ctl.Listen(filepath.Join(t.TempDir(), "omaterm.sock"), a.host, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	a.ctl = s
	c, err := client.Dial(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func sessionOf(t *testing.T, c *client.Client, id string) (proto.Session, bool) {
	t.Helper()
	var st proto.State
	if err := c.Call("state", nil, &st); err != nil {
		t.Fatal(err)
	}
	for _, s := range st.Sessions {
		if s.ID == id {
			return s, true
		}
	}
	return proto.Session{}, false
}

// A session made from an agent's pane is its session's child: saved so,
// and standing on its own once its parent closes.
func TestParentSession(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	c := dialApp(t, a)
	var lead, child proto.Created
	if err := c.Call("create", proto.CreateParams{Name: "lead", Dir: a.workspace, Command: []string{"sleep", "30"}}, &lead); err != nil {
		t.Fatal(err)
	}
	if err := c.Call("create", proto.CreateParams{Name: "w1", Dir: a.workspace, Command: []string{"sleep", "30"}, By: lead.Pane}, &child); err != nil {
		t.Fatal(err)
	}
	if s, _ := sessionOf(t, c, child.Session); s.Parent != lead.Session {
		t.Fatalf("parent %q, want %q", s.Parent, lead.Session)
	}
	saved := false
	for _, s := range a.state().Sessions {
		saved = saved || s.ID == child.Session && s.Parent == lead.Session
	}
	if !saved {
		t.Fatal("the parent isn't saved")
	}
	if err := c.Call("close", proto.IDParams{ID: lead.Session}, nil); err != nil {
		t.Fatal(err)
	}
	if s, ok := sessionOf(t, c, child.Session); !ok || s.Parent != "" {
		t.Fatalf("after its parent closed: %+v", s)
	}
}

// The sidebar shows a session under the one whose agent started it, which
// can hide it; the tabs go in that order; closing one that started others
// asks about them.
func TestSessionTree(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	make := func(name, by string) proto.Created {
		t.Helper()
		v, err := a.cliCreate(proto.CreateParams{Name: name, Dir: a.workspace, Command: []string{"sleep", "30"}, By: by})
		if err != nil {
			t.Fatal(err)
		}
		return v.(proto.Created)
	}
	lead := make("lead", "")
	w1 := make("w1", lead.Pane)
	other := make("other", "")
	w2 := make("w2", lead.Pane)
	g := make("grandchild", w1.Pane)
	index := func(id string) int {
		for i, t := range a.tabs {
			if t.ID == id {
				return i
			}
		}
		return -1
	}

	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	// The tab bar names the sessions too: the sidebar's are left of it.
	inSidebar := func(name string) bool {
		r, ok := tr.Find(name)
		return ok && r.X < a.sidebarWidth()
	}
	rects := map[string]ui.Rect{}
	for _, name := range []string{"lead", "w1", "grandchild", "w2", "other"} {
		r, ok := tr.Find(name)
		if !ok {
			t.Fatalf("no %s in %v", name, tr.Texts())
		}
		rects[name] = r
	}
	for _, pair := range [][2]string{{"lead", "w1"}, {"w1", "grandchild"}, {"grandchild", "w2"}, {"w2", "other"}} {
		if rects[pair[0]].Y >= rects[pair[1]].Y {
			t.Errorf("%s isn't above %s", pair[0], pair[1])
		}
	}
	if !(rects["lead"].X < rects["w1"].X && rects["w1"].X < rects["grandchild"].X && rects["w2"].X == rects["w1"].X && rects["other"].X == rects["lead"].X) {
		t.Errorf("indents %+v", rects)
	}
	// The tab bar, and the keys, go through them in that order too.
	var order []string
	for _, t := range a.tabs {
		order = append(order, t.Label)
	}
	if got := strings.Join(order[len(order)-5:], " "); got != "lead w1 grandchild w2 other" {
		t.Errorf("tabs go %s", got)
	}

	// The chevron left of the lead's icon hides what it started.
	r := rects["lead"]
	tr.ClickAt(r.X-36, r.Y+r.H/2)
	tr.Frame()
	if !a.tabs[index(lead.Session)].Collapsed || inSidebar("w1") || !tr.HasText("+3") {
		t.Fatalf("collapsed %v: %v", a.tabs[index(lead.Session)].Collapsed, tr.Texts())
	}
	if a.activeTab == index(lead.Session) {
		t.Error("the chevron went to the session")
	}
	tr.ClickAt(r.X-36, r.Y+r.H/2)
	tr.Frame()
	if !inSidebar("w1") {
		t.Fatal("expanding didn't show w1")
	}

	// Keeping what w1 started: the grandchild stands on its own.
	a.closeTabAsking(index(w1.Session))
	if a.closeAsk == nil || a.closeAsk.alt != "Keep Them" || a.closeAsk.confirm != "Close All" ||
		!strings.Contains(a.closeAsk.text, "w1 started grandchild.") {
		t.Fatalf("ask %+v", a.closeAsk)
	}
	a.closeAsk.altRun()
	a.closeAsk = nil
	if index(w1.Session) >= 0 || index(g.Session) < 0 {
		t.Fatalf("keep them: w1 %d, grandchild %d", index(w1.Session), index(g.Session))
	}
	tr.Frame()
	if gr, _ := tr.Find("grandchild"); gr.X != rects["lead"].X {
		t.Errorf("the grandchild isn't on its own: %v", gr)
	}

	// Closing them all.
	a.closeTabAsking(index(lead.Session))
	if a.closeAsk == nil || !strings.Contains(a.closeAsk.text, "lead started w2.") {
		t.Fatalf("ask %+v", a.closeAsk)
	}
	a.closeAsk.run()
	a.closeAsk = nil
	if index(lead.Session) >= 0 || index(w2.Session) >= 0 || index(other.Session) < 0 || index(g.Session) < 0 {
		t.Fatalf("close all: lead %d, w2 %d, other %d, grandchild %d", index(lead.Session), index(w2.Session), index(other.Session), index(g.Session))
	}
}

// runCLI runs the omaterm command, returning what it printed and its exit
// status.
func runCLI(t *testing.T, args ...string) (string, int) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	code := cli.Run(args)
	w.Close()
	os.Stdout = stdout
	out, _ := io.ReadAll(r)
	return string(out), code
}

// omaterm wait waits for several sessions at once: for the first, or for
// all of them.
func TestWaitMany(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	dialApp(t, a)
	t.Setenv("OMATERM_SOCKET", a.ctl.Path())
	t.Setenv("OMATERM_PANE", "")
	t.Setenv("OMATERM_SESSION", "")
	for _, s := range []struct{ name, cmd string }{{"fast", "sleep 0.2; exit 3"}, {"slow", "sleep 1.5"}, {"never", "sleep 60"}} {
		if _, err := a.cliCreate(proto.CreateParams{Name: s.name, Dir: a.workspace, Command: []string{"sh", "-c", s.cmd}}); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	out, code := runCLI(t, "wait", "slow", "fast", "--any", "--exit", "-t", "10s")
	if code != 0 || out != "fast exited 3\n" || time.Since(start) > time.Second {
		t.Fatalf("--any: %q, status %d, after %v", out, code, time.Since(start))
	}
	out, code = runCLI(t, "wait", "slow", "fast", "--exit", "-t", "10s")
	if code != 0 || out != "fast exited 3\nslow exited 0\n" {
		t.Fatalf("all: %q, status %d", out, code)
	}
	if _, code = runCLI(t, "wait", "fast", "never", "--exit", "-t", "300ms"); code != 1 {
		t.Fatalf("a timeout exits %d", code)
	}
	if _, code = runCLI(t, "wait", "fast", "nope", "--exit"); code != 3 {
		t.Fatalf("no such target exits %d", code)
	}
}

// gitIn runs git in dir, as a user with a name.
func gitIn(t *testing.T, dir string, args ...string) string {
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

// commitFile writes a file in dir and commits it.
func commitFile(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", name)
	gitIn(t, dir, "commit", "-qm", "Add "+name)
}

// leadAndWorker makes a lead's session in a worktree on feat, with a
// commit of its own, and a worker's it started, in a worktree on w1 made
// from feat, with another.
func leadAndWorker(t *testing.T, a *appState) (lead, worker proto.Created, leadWT, workerWT worktree.Info) {
	t.Helper()
	gitIn(t, a.workspace, "commit", "-qam", "two")
	add := func(from, branch, by string) (proto.Created, worktree.Info) {
		wt, err := worktree.Add(context.Background(), from, branch, "", false)
		if err != nil {
			t.Fatal(err)
		}
		c, err := a.createWorktreeSession(wt, proto.CreateParams{Project: a.project().ID, Command: []string{"sleep", "30"}, By: by})
		if err != nil {
			t.Fatal(err)
		}
		return c, wt
	}
	lead, leadWT = add(a.workspace, "feat", "")
	commitFile(t, leadWT.Dir, "lead.txt", "lead\n")
	worker, workerWT = add(leadWT.Dir, "w1", lead.Pane)
	commitFile(t, workerWT.Dir, "worker.txt", "worker\n")
	return
}

// A worker's branch diff is what it changed since its lead's branch: not
// the lead's own commits, as since the default branch.
func TestBranchDiffSinceItsBase(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	c := dialApp(t, a)
	lead, worker, _, workerWT := leadAndWorker(t, a)
	if workerWT.Base != "feat" {
		t.Fatalf("the worker's base is %q", workerWT.Base)
	}
	if s, _ := sessionOf(t, c, worker.Session); s.Parent != lead.Session {
		t.Fatalf("worker's parent %q", s.Parent)
	}
	var out proto.ReadResult
	if err := c.Call("git.diff", proto.GitParams{Pane: worker.Pane, Base: "branch"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text, "worker.txt") || strings.Contains(out.Text, "lead.txt") {
		t.Fatalf("diff %q", out.Text)
	}
	if err := c.Call("git.diff", proto.GitParams{Pane: lead.Pane, Base: "branch"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text, "lead.txt") {
		t.Fatalf("the lead's diff %q", out.Text)
	}
}

// fakeAgentScript is a script named claude, as omaterm knows the agent by
// its process: it takes bracketed pastes, says it is idle, and writes
// what it is given to its log; a message ended, it works a moment.
const fakeAgentScript = `#!/bin/sh
printf '\033[?2004h\033]7501;state=idle\007'
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$LOG"
  case "$line" in
  *'201~'*) printf '\033]7501;state=working\007'; sleep 0.3; printf '\033]7501;state=done\007' ;;
  esac
done
`

// fakeAgent is the command of a fake agent, in a job of its own as one
// run from a shell is, and the variable naming its log.
func fakeAgent(t *testing.T, name string) (cmd []string, env, log string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	if err := os.WriteFile(path, []byte(fakeAgentScript), 0o755); err != nil {
		t.Fatal(err)
	}
	log = filepath.Join(dir, name+".log")
	return []string{"/bin/sh", "-c", "set -m; '" + path + "'; :"}, "LOG=" + log, log
}

func waitForText(t *testing.T, path, want string) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		b, _ := os.ReadFile(path)
		if strings.Contains(string(b), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %q in %q", want, b)
		}
	}
}

// Without a session daemon, the app's mailbox carries a lead's task to
// its worker, and the worker's answer back to the lead.
func TestMailInTheApp(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	c := dialApp(t, a)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.panes.Run(ctx)
	start := func(name, by string) (proto.Created, string) {
		cmd, env, log := fakeAgent(t, name)
		v, err := a.cliCreate(proto.CreateParams{Name: name, Dir: a.workspace, Command: cmd, Env: []string{env}, By: by})
		if err != nil {
			t.Fatal(err)
		}
		return v.(proto.Created), log
	}
	lead, leadLog := start("lead", "")
	worker, workerLog := start("w1", lead.Pane)
	var m proto.Mail
	if err := c.Call("mail.ask", proto.MailAskParams{From: lead.Pane, FromName: "lead", To: worker.Pane, ToName: "w1",
		Text: "Fix the login", NoWait: true}, &m); err != nil {
		t.Fatal(err)
	}
	waitForText(t, workerLog, "201~")
	if err := c.Call("mail.reply", proto.MailReplyParams{From: worker.Pane, Text: "Fixed"}, nil); err != nil {
		t.Fatal(err)
	}
	waitForText(t, leadLog, "w1's answer to your message "+m.ID+":\nFixed")
	var list []proto.Mail
	if err := c.Call("mail.list", proto.MailListParams{Pane: lead.Pane}, &list); err != nil || len(list) != 2 {
		t.Fatalf("list %+v %v", list, err)
	}
}

// With a session daemon, the app passes the mail methods on to it; a wait
// whose command went away leaves the answer to come to the asker.
func TestMailThroughTheDaemon(t *testing.T) {
	dir, err := os.MkdirTemp("", "omm")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "d.sock")
	d, err := daemon.Start(daemon.Options{Socket: socket, StateDir: dir, StateFile: filepath.Join(dir, "state.json"), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	r, err := daemon.Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	a := newApp()
	a.win = nil
	a.setPanes(r)
	a.projects = []project{{Name: "test", Path: dir, Tabs: []string{"dev"}}}
	a.setProject(0)
	defer stopTermPanes(a)
	c := dialApp(t, a)
	logs := map[string]string{}
	for _, id := range []string{"lead", "w1"} {
		cmd, env, log := fakeAgent(t, id)
		logs[id] = log
		if _, err := r.Start(session.StartOptions{ID: id, Exec: true, Command: cmd, Env: []string{env}, Cols: 100, Rows: 30}); err != nil {
			t.Fatal(err)
		}
	}
	var m proto.Mail
	if err := c.Call("mail.ask", proto.MailAskParams{From: "lead", FromName: "lead", To: "w1", ToName: "w1", Text: "Fix the login"}, &m); err != nil {
		t.Fatal(err)
	}
	// A command waits for the answer, then goes away.
	waiter, err := client.Dial(a.ctl.Path())
	if err != nil {
		t.Fatal(err)
	}
	go waiter.Call("mail.await", proto.MailAwaitParams{ID: m.ID}, nil)
	waitForText(t, logs["w1"], "201~")
	waiter.Close()
	time.Sleep(300 * time.Millisecond)
	if err := c.Call("mail.reply", proto.MailReplyParams{From: "w1", Text: "Fixed"}, nil); err != nil {
		t.Fatal(err)
	}
	waitForText(t, logs["lead"], "w1's answer to your message "+m.ID+":\nFixed")
}

// A lead asks its worker with omaterm ask, the worker answers with omaterm
// reply, and the answer wakes the lead; asked waiting, the lead gets the
// answer printed.
func TestAskReplyCommands(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	c := dialApp(t, a)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.panes.Run(ctx)
	t.Setenv("OMATERM_SOCKET", a.ctl.Path())
	t.Setenv("OMATERM_SESSION", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "")
	start := func(name, by string) (proto.Created, string) {
		cmd, env, log := fakeAgent(t, name)
		v, err := a.cliCreate(proto.CreateParams{Name: name, Dir: a.workspace, Command: cmd, Env: []string{env}, By: by})
		if err != nil {
			t.Fatal(err)
		}
		return v.(proto.Created), log
	}
	lead, leadLog := start("lead", "")
	worker, workerLog := start("w1", lead.Pane)

	t.Setenv("OMATERM_PANE", lead.Pane)
	if out, code := runCLI(t, "ask", "w1", "Fix", "the", "login", "--no-wait"); code != 0 || out != "m1\n" {
		t.Fatalf("ask: %q, status %d", out, code)
	}
	waitForText(t, workerLog, "201~")
	waitForText(t, workerLog, `Message m1 from the agent in session "lead" (pane `+lead.Pane+`), not from the user:`+"\nFix the login\n")

	t.Setenv("OMATERM_PANE", worker.Pane)
	if out, code := runCLI(t, "reply", "Fixed:", "the", "cookie"); code != 0 || out != "answered m1 from lead\n" {
		t.Fatalf("reply: %q, status %d", out, code)
	}
	waitForText(t, leadLog, "w1's answer to your message m1:\nFixed: the cookie")
	if out, _ := runCLI(t, "inbox"); !strings.Contains(out, "m1    lead           w1             replied   Fix the login") {
		t.Fatalf("inbox %q", out)
	}

	t.Setenv("OMATERM_PANE", lead.Pane)
	go func() {
		waitForText(t, workerLog, "Is it tested?")
		time.Sleep(100 * time.Millisecond)
		c.Call("mail.reply", proto.MailReplyParams{From: worker.Pane, Text: "Yes, all green."}, nil)
	}()
	if out, code := runCLI(t, "ask", "w1", "Is it tested?", "-t", "10s"); code != 0 || out != "Yes, all green.\n" {
		t.Fatalf("waiting ask: %q, status %d", out, code)
	}
}

// A lead merges its worker into its own branch, which closes the worker
// and removes its worktree; the main checkout stays as it was. It can't
// merge into the main checkout, nor what wasn't made from its branch; a
// conflict is for the worker to resolve.
func TestLeadMergesItsWorker(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	c := dialApp(t, a)
	lead, worker, leadWT, workerWT := leadAndWorker(t, a)
	mainHead := gitIn(t, a.workspace, "rev-parse", "HEAD")

	var out proto.Merged
	if err := c.Call("worktree.merge", proto.MergeParams{By: lead.Pane, Target: worker.Session}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Branch != "w1" || out.Into != "feat" || !out.Removed || len(out.Commits) != 1 || out.Commits[0] != "Add worker.txt" {
		t.Fatalf("merged %+v", out)
	}
	if got := gitIn(t, leadWT.Dir, "log", "-1", "--format=%s"); got != "Add worker.txt" {
		t.Errorf("the lead's branch has %q last", got)
	}
	if _, err := os.Stat(filepath.Join(leadWT.Dir, "worker.txt")); err != nil {
		t.Error("the worker's file isn't in the lead's worktree")
	}
	if _, err := os.Stat(workerWT.Dir); err == nil {
		t.Error("the worker's worktree stays")
	}
	if _, _, _, ok := a.findSession(worker.Session); ok {
		t.Error("the worker's session stays")
	}
	if got := gitIn(t, a.workspace, "rev-parse", "HEAD"); got != mainHead {
		t.Error("the main checkout moved")
	}

	// From the main checkout, and of a worktree made from main.
	plain, err := a.cliCreate(proto.CreateParams{Name: "plain", Dir: a.workspace, Command: []string{"sleep", "30"}})
	if err != nil {
		t.Fatal(err)
	}
	wt, err := worktree.Add(context.Background(), a.workspace, "other", "", false)
	if err != nil {
		t.Fatal(err)
	}
	other, err := a.createWorktreeSession(wt, proto.CreateParams{Project: a.project().ID, Command: []string{"sleep", "30"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c2 := range []struct {
		by, target, want string
	}{
		{plain.(proto.Created).Pane, other.Session, "you work in the main checkout, and merging into main is the user's"},
		{lead.Pane, other.Session, "other was made from main, not from your branch feat: the user merges it"},
	} {
		if err := c.Call("worktree.merge", proto.MergeParams{By: c2.by, Target: c2.target}, nil); err == nil || !strings.Contains(err.Error(), c2.want) {
			t.Errorf("merged: %v", err)
		}
	}

	// A conflict.
	add, err := worktree.Add(context.Background(), leadWT.Dir, "w2", "", false)
	if err != nil {
		t.Fatal(err)
	}
	w2, err := a.createWorktreeSession(add, proto.CreateParams{Project: a.project().ID, Command: []string{"sleep", "30"}, By: lead.Pane})
	if err != nil {
		t.Fatal(err)
	}
	commitFile(t, add.Dir, "lead.txt", "the worker's\n")
	commitFile(t, leadWT.Dir, "lead.txt", "the lead's\n")
	err = c.Call("worktree.merge", proto.MergeParams{By: lead.Pane, Target: w2.Session}, nil)
	if err == nil || !strings.Contains(err.Error(), "w2 conflicts with feat in lead.txt: ask it to merge feat into its branch") {
		t.Fatalf("conflict: %v", err)
	}
	if st := gitIn(t, leadWT.Dir, "status", "--porcelain"); st != "" {
		t.Fatalf("the lead's worktree after a conflict: %q", st)
	}
}

// omaterm merge, run in the lead's pane, merges its worker by name.
func TestMergeCommand(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	dialApp(t, a)
	t.Setenv("OMATERM_SOCKET", a.ctl.Path())
	t.Setenv("OMATERM_SESSION", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "")
	lead, _, _, _ := leadAndWorker(t, a)
	t.Setenv("OMATERM_PANE", lead.Pane)
	out, code := runCLI(t, "merge", "w1", "--commit", "-m", "Bring w1")
	if code != 0 || out != "merged w1 into feat (1 commit)\nclosed its session, and removed its worktree and branch\n" {
		t.Fatalf("%q, status %d", out, code)
	}
	if _, code := runCLI(t, "merge", "w1"); code != 3 {
		t.Fatalf("merging it again exits %d", code)
	}
}

// Ask for Review starts a reviewer under the session reviewed, in its
// folder, briefed once it is up, and asking it 3 times at most.
func TestAskForReview(t *testing.T) {
	prev := reviewCommand
	var started []string
	reviewCommand = func(agent string) []string { started = append(started, agent); return []string{"sleep", "30"} }
	defer func() { reviewCommand = prev }()
	a := newTestApp(t)
	defer stopTermPanes(a)
	c := dialApp(t, a)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.panes.Run(ctx)
	_, author, _, workerWT := leadAndWorker(t, a)
	cmd, env, _ := fakeAgent(t, "w1")
	// The worker's agent, in a pane of its own beside the sleep.
	if _, err := a.cliSplit(proto.CreateParams{Split: author.Pane, Command: cmd, Env: []string{env}}); err != nil {
		t.Fatal(err)
	}
	_, tabs, ti, _ := a.findSession(author.Session)
	var agentPane string
	for deadline := time.Now().Add(10 * time.Second); agentPane == ""; time.Sleep(50 * time.Millisecond) {
		a.syncStatus()
		agentPane, _, _ = a.agentOf((*tabs)[ti])
		if time.Now().After(deadline) {
			t.Fatal("no agent in the worker's session")
		}
	}

	a.openReviewDialog((*tabs)[ti])
	if a.review == nil || a.review.agent != "codex" {
		t.Fatalf("dialog %+v: the reviewer should be other than the session's claude", a.review)
	}
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("Ask for Review") {
		t.Fatalf("no dialog: %v", tr.Texts())
	}
	if err := tr.Click("Start Review"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if a.review != nil || len(started) != 1 || started[0] != "codex" || a.reviewAgent != "codex" {
		t.Fatalf("dialog %+v, started %v", a.review, started)
	}
	var reviewer *topTab
	for i := range a.tabs {
		if a.tabs[i].Label == "review w1" {
			reviewer = &a.tabs[i]
		}
	}
	if reviewer == nil || reviewer.Parent != author.Session || reviewer.Worktree != nil {
		t.Fatalf("reviewer %+v", reviewer)
	}
	rpane := reviewer.Split.termIDs()[0]
	if dir := a.paneDirs[rpane]; dir != workerWT.Dir {
		t.Errorf("the reviewer works in %s, not the worker's %s", dir, workerWT.Dir)
	}
	var list []proto.Mail
	if err := c.Call("mail.list", proto.MailListParams{Pane: rpane}, &list); err != nil || len(list) != 1 {
		t.Fatalf("mail %+v %v", list, err)
	}
	for _, want := range []string{"review the work of the agent in session \"w1\" (pane " + agentPane + ")",
		"omaterm diff " + agentPane + " --branch", "omaterm ask " + agentPane, "At most 3 rounds"} {
		if !strings.Contains(list[0].Text, want) || !list[0].Note {
			t.Errorf("no %q in the brief %q", want, list[0].Text)
		}
	}
	for i := range 4 {
		err := c.Call("mail.ask", proto.MailAskParams{From: rpane, To: agentPane, Text: "Fix it", NoWait: true}, nil)
		if (err == nil) != (i < 3) {
			t.Fatalf("round %d: %v", i+1, err)
		}
	}
}

// omaterm report in a pane gives the report to it through omaterm: an
// agent's commands run with no terminal, or one of the agent's own.
func TestReportThroughOmaterm(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	dialApp(t, a)
	t.Setenv("OMATERM_SOCKET", a.ctl.Path())
	t.Setenv("OMATERM_SESSION", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "")
	v, err := a.cliCreate(proto.CreateParams{Name: "agent", Dir: a.workspace, Command: []string{"sleep", "30"}})
	if err != nil {
		t.Fatal(err)
	}
	pane := v.(proto.Created).Pane
	t.Setenv("OMATERM_PANE", pane)
	if out, code := runCLI(t, "report", "done", "Merged", "w1"); code != 0 || out != "" {
		t.Fatalf("%q, status %d", out, code)
	}
	info := a.panes.Get(pane).Info()
	if info.Message != "Merged w1" || len(info.Records) != 1 || info.Records[0].State != "done" {
		t.Fatalf("%+v", info)
	}
	// The agent's program reports its turn done, as Claude Code does: what
	// the agent said stays.
	if err := a.panes.Get(pane).Report("state=done:app=claude-code"); err != nil {
		t.Fatal(err)
	}
	if info := a.panes.Get(pane).Info(); info.Message != "Merged w1" {
		t.Fatalf("after the program's own report: %+v", info)
	}
}
