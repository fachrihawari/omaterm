package model

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestMigrateV1(t *testing.T) {
	n := 0
	NewID = func() string { n++; return "id" + strconv.Itoa(n) }
	defer func() { NewID = func() string { return "" } }()
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
			Panes: []Pane{{ID: "x", Dir: "/p"}, {ID: "y", URL: "http://localhost:3000/"}}}}}
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
}
