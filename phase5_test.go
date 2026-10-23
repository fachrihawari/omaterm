package main

import (
	"context"
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
