package main

import (
	"context"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omaterm/internal/agent"
	"omaterm/internal/client"
	"omaterm/internal/ctl"
	"omaterm/internal/gitsnap"
	"omaterm/internal/proto"
	"omaterm/internal/session"

	"github.com/egoist/mygo/ui"
)

// An agent starting a turn snapshots its worktree; one going on after a
// question it asked (waiting) does not start a new turn.
func TestTurnSnapshot(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.termCmd = []string{"sh", "-c", "sleep 30"}
	id := a.activeSplitLeaf()
	a.term(id)
	info := a.panes.Get(id).Info()
	info.Agent, info.Status, info.Dir = "claude", session.Working, a.workspace

	a.onStatus(info, session.Waiting)
	time.Sleep(300 * time.Millisecond)
	if a.panes.Get(id).Info().Turn != nil {
		t.Fatal("waiting → working started a turn")
	}
	a.onStatus(info, session.Idle)
	deadline := time.Now().Add(10 * time.Second)
	for a.panes.Get(id).Info().Turn == nil && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	turn := a.panes.Get(id).Info().Turn
	if turn == nil || turn.Tree == "" {
		t.Fatal("no turn snapshot")
	}
	// The turn's changes are what changed after it: none yet.
	r, err := gitsnap.Open(context.Background(), a.workspace)
	if err != nil {
		t.Fatal(err)
	}
	files, err := r.Changes(context.Background(), turn.Tree, "")
	if err != nil || len(files) != 0 {
		t.Fatalf("files since the turn: %v %v", files, err)
	}
	os.WriteFile(filepath.Join(a.workspace, "new.txt"), []byte("x\n"), 0o644)
	files, _ = r.Changes(context.Background(), turn.Tree, "")
	if len(files) != 1 || files[0].Path != "new.txt" {
		t.Fatalf("after an edit: %+v", files)
	}
}

// A pane of the last run comes back with its scrollback under a banner,
// and an agent resumes its conversation.
func TestRestoreOptions(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	a := newApp()
	a.win = nil
	os.MkdirAll(filepath.Dir(historyPath("p1")), 0o700)
	os.WriteFile(historyPath("p1"), []byte("old output\r\n"), 0o600)
	a.restoring["p1"] = true
	a.paneAgents["p1"] = &agent.Session{Agent: "claude", ID: "abc"}
	h, cmd := a.restoreOptions("p1")
	if !strings.Contains(string(h), "old output") || !strings.Contains(string(h), "restored · session ended") {
		t.Fatalf("history %q", h)
	}
	if strings.Join(cmd, " ") != "claude --resume abc" {
		t.Fatalf("command %q", cmd)
	}
	if h, cmd := a.restoreOptions("p1"); h != nil || cmd != nil {
		t.Fatal("a pane is restored once")
	}
	a.restoring["p2"] = true
	a.paneAgents["p2"] = &agent.Session{Agent: "claude", ID: "abc"}
	a.settings.ResumeAgents = false
	if _, cmd := a.restoreOptions("p2"); cmd != nil {
		t.Fatal("resumed with resumeAgents off")
	}
}

// omaterm diff: a pane's uncommitted changes, through the socket.
func TestGitDiffMethod(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.termCmd = []string{"sh", "-c", "sleep 30"}
	id := a.activeSplitLeaf()
	a.term(id)
	a.host = newLocalHost(a)
	s, err := ctl.Listen(filepath.Join(t.TempDir(), "s.sock"), a.host, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := client.Dial(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var files []gitsnap.File
	if err := c.Call("git.changes", proto.GitParams{Pane: id}, &files); err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "hello.txt" {
		t.Fatalf("%+v", files)
	}
	var out proto.ReadResult
	if err := c.Call("git.diff", proto.GitParams{Pane: id, Paths: []string{filepath.Join(a.workspace, "hello.txt")}}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text, "+two") {
		t.Fatalf("patch %q", out.Text)
	}
	if proto.CodeOf(c.Call("git.diff", proto.GitParams{Pane: id, Paths: []string{"/etc/passwd"}}, &out)) != proto.CodeUsage {
		t.Fatal("a path outside the repository")
	}
}

// A commit of several files shows them one at a time, with a strip to go
// through them.
func TestMultiFileCommitDiff(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = a.workspace
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
		return strings.TrimSpace(string(out))
	}
	os.WriteFile(filepath.Join(a.workspace, "a.txt"), []byte("a\n"), 0o644)
	os.WriteFile(filepath.Join(a.workspace, "b.txt"), []byte("b\n"), 0o644)
	git("add", "a.txt", "b.txt")
	git("commit", "-q", "-m", "two files")
	a.openCommitDiff(git("rev-parse", "HEAD"))
	if len(a.diffFiles) != 2 || a.diffFileIdx != 0 || !strings.Contains(a.diffTitle, "a.txt") {
		t.Fatalf("files %d idx %d title %q", len(a.diffFiles), a.diffFileIdx, a.diffTitle)
	}
	a.diffFileShow(1)
	if !strings.Contains(a.diffTitle, "b.txt") || a.diffPatch == nil {
		t.Fatalf("second file: %q", a.diffTitle)
	}
}

// changesFixture is an app whose pane's agent took a turn in a repo, which
// added a line to a.txt; its status is pinned, the pane not started.
func changesFixture(t *testing.T, status session.Status) (*appState, *ui.Tester, string) {
	t.Helper()
	a := newTestApp(t)
	dir := a.workspace
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644)
	r, err := gitsnap.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := r.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte("one\ntwo\nagent line\nthree\n"), 0o644)
	id := a.activeSplitLeaf()
	info := session.Info{ID: id, Dir: dir, Agent: "claude", Status: status, Progress: -1,
		Turn: &session.Turn{Tree: tree, Root: r.Root, At: time.Now().UnixMilli()}}
	a.inspectorVisible, a.inspectorTab, a.changes.mode = true, 2, changesTurn
	a.termCmd = []string{"sh", "-c", "sleep 30"}
	view := func(c *ui.Context) {
		a.status[id] = info // the pane's own status would replace it
		a.view(c)
	}
	return a, ui.NewTester(view, 1440, 900), path
}

func frameUntil(t *testing.T, tr *ui.Tester, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		tr.Frame()
		if ok() {
			return
		}
	}
	t.Fatalf("timed out: %s (texts %q)", what, tr.Texts())
}

// openTurnFile opens a.txt in the Git tab's Last turn, and returns it once its
// diff loaded.
func openTurnFile(t *testing.T, a *appState, tr *ui.Tester) *changeFile {
	t.Helper()
	frameUntil(t, tr, "the turn's file", func() bool { return tr.HasText("a.txt") })
	if err := tr.Click("a.txt"); err != nil {
		t.Fatal(err)
	}
	frameUntil(t, tr, "the diff", func() bool {
		cf := a.changes.open["a.txt"]
		return cf != nil && cf.ds != nil
	})
	return a.changes.open["a.txt"]
}

// Revert in Last turn puts the turn's hunk back on disk.
func TestChangesRevertHunk(t *testing.T) {
	a, tr, path := changesFixture(t, session.Idle)
	defer stopTermPanes(a)
	cf := openTurnFile(t, a, tr)
	if !cf.ds.HunkActions {
		t.Fatal("no Revert on the turn's hunk")
	}
	// The buttons show on the change under the pointer: the third line,
	// below the file's row.
	row, _ := tr.Find("a.txt")
	tr.Move(row.X+80, row.Y+row.H/2+70)
	frameUntil(t, tr, "the hunk's buttons", func() bool { return tr.HasText("Revert") })
	if os.Getenv("OMATERM_SHOT_REVERT") != "" {
		f, _ := os.Create(os.Getenv("OMATERM_SHOT_REVERT"))
		png.Encode(f, tr.Image())
		f.Close()
	}
	if err := tr.Click("Revert"); err != nil {
		t.Fatal(err)
	}
	frameUntil(t, tr, "reverted on disk", func() bool {
		b, _ := os.ReadFile(path)
		return string(b) == "one\ntwo\nthree\n"
	})
}

// While the agent works, the turn's hunks offer no Revert, and a revert
// asked anyway leaves the file alone.
func TestChangesNoRevertWhileWorking(t *testing.T) {
	a, tr, path := changesFixture(t, session.Working)
	defer stopTermPanes(a)
	cf := openTurnFile(t, a, tr)
	if cf.ds.HunkActions {
		t.Fatal("Revert offered while the agent works")
	}
	a.revertHunk(nil, cf, 0)
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "agent line") {
		t.Fatalf("reverted while working: %q", b)
	}
}

// The Git tab offers Last turn only while the focused pane's agent has a
// turn; it starts on all changes, and the switch shows the turn's files.
func TestGitTabLastTurnSwitch(t *testing.T) {
	plain := newTestApp(t)
	defer stopTermPanes(plain)
	plain.inspectorVisible, plain.inspectorTab = true, 2
	tr := ui.NewTester(plain.view, 1440, 900)
	tr.Frame()
	if tr.HasText("Last turn") {
		t.Fatal("Last turn offered with no agent turn")
	}

	a, tr, _ := changesFixture(t, session.Idle)
	defer stopTermPanes(a)
	a.changes.mode = changesAll
	frameUntil(t, tr, "the switch", func() bool { return tr.HasText("Last turn") })
	if err := tr.Click("Last turn"); err != nil {
		t.Fatal(err)
	}
	frameUntil(t, tr, "the turn's file", func() bool { return a.changes.mode == changesTurn && tr.HasText("a.txt") })
}
