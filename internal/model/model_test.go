package model

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestMigrateV1(t *testing.T) {
	n := 0
	NewID = func() string { n++; return "id" + strconv.Itoa(n) }
	defer func() { NewID = RandomID }()
	path := filepath.Join(t.TempDir(), "state.json")
	os.WriteFile(path, []byte(`{"projects":[{"Name":"a","Path":"/a","Tabs":["dev","tests"]},{"Name":"b","Path":"/b","Tabs":["dev"]}],"active":1}`), 0o644)
	st, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Projects) != 2 || len(st.Sessions) != 3 {
		t.Fatalf("%+v", st)
	}
	if st.ActiveProject != st.Projects[1].ID {
		t.Error("active project")
	}
	s := st.Sessions[1]
	if s.Name != "tests" || s.Project != st.Projects[0].ID || len(s.Panes) != 1 || s.Panes[0].Dir != "/a" || s.Layout.Pane != s.Panes[0].ID {
		t.Fatalf("%+v", s)
	}
}

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	in := State{ActiveProject: "p", Projects: []Project{{ID: "p", Name: "p", Dir: "/p", Collapsed: true}},
		Sessions: []Session{{ID: "s", Project: "p", Layout: &Layout{Row: true, Ratio: 0.4, A: &Layout{Pane: "x"}, B: &Layout{Pane: "y"}},
			Panes:    []Pane{{ID: "x", Dir: "/p"}, {ID: "y", URL: "http://localhost:3000/"}},
			Worktree: &Worktree{Dir: "/p/.worktrees/x", Branch: "x", Base: "main"}}}}
	if err := Save(path, in); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	out, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if out.Version != Version || !out.Projects[0].Collapsed || out.Sessions[0].Layout.Ratio != 0.4 {
		t.Fatalf("%+v", out)
	}
	if got := out.Sessions[0].Layout.Panes(); len(got) != 2 || got[1] != "y" {
		t.Fatalf("panes %v", got)
	}
	if w := out.Sessions[0].Worktree; w == nil || *w != *in.Sessions[0].Worktree {
		t.Fatalf("worktree %+v", w)
	}
}

// A sessions file a newer omaterm wrote is not written over: what only
// the newer one keeps was lost.
func TestSaveKeepsNewerFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	os.WriteFile(path, []byte(`{"version": 99, "sessions": []}`), 0o600)
	if err := Save(path, State{}); !errors.Is(err, ErrNewer) {
		t.Fatalf("save over a newer file: %v", err)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), `"version": 99`) {
		t.Errorf("the newer file was written over: %s", data)
	}
}

// A pane of a session in a worktree starts in its folder, else in the
// worktree; with the worktree gone, nowhere.
func TestWorktreePaneDir(t *testing.T) {
	var none *Worktree
	if dir, err := none.PaneDir("/x"); dir != "/x" || err != nil {
		t.Fatalf("no worktree: %q %v", dir, err)
	}
	w := &Worktree{Dir: t.TempDir(), Branch: "x", Base: "main"}
	sub := filepath.Join(w.Dir, "sub")
	os.Mkdir(sub, 0o755)
	for in, want := range map[string]string{sub: sub, "": w.Dir, filepath.Join(w.Dir, "gone"): w.Dir} {
		if dir, err := w.PaneDir(in); dir != want || err != nil {
			t.Errorf("%q: %q %v, want %q", in, dir, err, want)
		}
	}
	os.RemoveAll(w.Dir)
	if dir, err := w.PaneDir(sub); err == nil || dir != "" || !w.Gone() {
		t.Fatalf("gone: %q %v", dir, err)
	}
}
