package cli

import (
	"strings"
	"testing"

	"omaterm/internal/agent"
	"omaterm/internal/model"
	"omaterm/internal/proto"
)

// Two projects, each with a Claude Code pane; the oterm one runs the
// conversation "conv-oterm".
func twoProjects() proto.State {
	return proto.State{
		Projects: []proto.Project{{ID: "p1", Name: "oterm", Dir: "/p/oterm"}, {ID: "p2", Name: "Random", Dir: "/p/random"}},
		Sessions: []proto.Session{
			{ID: "s1", Project: "p1", Panes: []proto.Pane{{ID: "pane1", Dir: "/p/oterm",
				AgentSession: &agent.Session{Agent: "claude", ID: "conv-oterm"}}}},
			{ID: "s2", Project: "p2", Panes: []proto.Pane{{ID: "pane2", Dir: "/p/random"}}},
		},
	}
}

// A background agent's commands carry the pane variables of whichever pane
// started its service: the pane running the caller's conversation wins.
func TestCurrentPrefersTheConversationsPane(t *testing.T) {
	st := twoProjects()
	t.Setenv("OMATERM_PANE", "pane2")
	t.Setenv("CODEX_THREAD_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "conv-oterm")
	if got := current(st); got != "pane1" {
		t.Fatalf("current = %s, want pane1", got)
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", "conv-unknown")
	if got := current(st); got != "pane2" {
		t.Fatalf("current = %s, want the variable's pane2", got)
	}
}

// What an agent starts stays in its project, unless it says otherwise.
func TestSameProject(t *testing.T) {
	st := twoProjects()
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "")
	t.Setenv("OMATERM_PANE", "pane1")
	if err := sameProject(st, "p1", false); err != nil {
		t.Fatalf("own project refused: %v", err)
	}
	err := sameProject(st, "p2", false)
	if err == nil || !strings.Contains(err.Error(), "project Random") || !strings.Contains(err.Error(), "--any-project") {
		t.Fatalf("other project: %v", err)
	}
	if err := sameProject(st, "p2", true); err != nil {
		t.Fatalf("--any-project refused: %v", err)
	}
	t.Setenv("OMATERM_PANE", "")
	t.Setenv("OMATERM_SESSION", "")
	if err := sameProject(st, "p2", false); err != nil {
		t.Fatalf("outside omaterm refused: %v", err)
	}
}

// The hook tells the agent which project and folder its pane is in.
func TestPaneWhere(t *testing.T) {
	if got := paneWhere(twoProjects(), "pane2"); got != ", in project Random (/p/random)" {
		t.Fatalf("got %q", got)
	}
	if got := paneWhere(twoProjects(), "gone"); got != "" {
		t.Fatalf("got %q for no pane", got)
	}
}

// The hook tells the agent its session's name, and the worktree it works
// in, to commit there.
func TestSessionNotes(t *testing.T) {
	st := twoProjects()
	st.Sessions[1].Name = "fix-login"
	st.Sessions[1].Worktree = &model.Worktree{Dir: "/p/random/.worktrees/fix-login", Branch: "fix-login", Base: "main"}
	got := sessionNotes(st, "s2")
	for _, want := range []string{`knows this session as "fix-login"`, "a git worktree", "fix-login, made from main", "merges it into main"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in %q", want, got)
		}
	}
	if got := sessionNotes(st, "s1"); got != "" {
		t.Errorf("s1: %q", got)
	}
}

// A session another's agent started is told who, and when it works on a
// branch made from that agent's, that the agent merges it.
func TestSessionNotesParent(t *testing.T) {
	st := twoProjects()
	st.Sessions[0].Name = "lead"
	st.Sessions[0].Panes = append([]proto.Pane{{ID: "dev"}}, proto.Pane{ID: "pane1", Agent: "claude"})
	st.Sessions[0].Worktree = &model.Worktree{Dir: "/p/oterm/.worktrees/feat", Branch: "feat", Base: "main"}
	st.Sessions[1].Parent = "s1"
	st.Sessions[1].Worktree = &model.Worktree{Dir: "/p/oterm/.worktrees/w1", Branch: "w1", Base: "feat"}
	got := sessionNotes(st, "s2")
	for _, want := range []string{`the agent in session "lead", which started this one, merges it into feat`,
		`The agent in session "lead" (pane pane1) started this session`, "omaterm reply"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in %q", want, got)
		}
	}
	// From another branch than the lead's, the user merges it.
	st.Sessions[1].Worktree.Base = "main"
	if got := sessionNotes(st, "s2"); !strings.Contains(got, "the user reviews it and merges it into main") {
		t.Errorf("%q", got)
	}
}
