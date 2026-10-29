package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui/editor"

	"omaterm/internal/session"
)

func TestParseFileLink(t *testing.T) {
	home, _ := os.UserHomeDir()
	for _, c := range []struct {
		link      string
		path      string
		line, col int
	}{
		{"src/a.go:12:3", "src/a.go", 12, 3},
		{"/abs/x.go:7", "/abs/x.go", 7, 0},
		{"~/x.go", filepath.Join(home, "x.go"), 0, 0},
		{"./x", "./x", 0, 0},
		{"../x", "../x", 0, 0},
		{"a.go:12", "a.go", 12, 0},
		{"name.ext", "name.ext", 0, 0},
		{"file:///tmp/a%20b.go:3", "/tmp/a b.go", 3, 0},
		{"file://localhost/tmp/a.go", "/tmp/a.go", 0, 0},
		{":12", ":12", 0, 0},
	} {
		path, line, col := parseFileLink(c.link)
		if path != c.path || line != c.line || col != c.col {
			t.Errorf("%q: got %q %d %d, want %q %d %d", c.link, path, line, col, c.path, c.line, c.col)
		}
	}
}

// Ctrl+click on a path a program printed opens the file in the editor at
// that line and column, relative to where the program runs; a path out
// of the project opens no editor; a web link is the system's unless the
// settings say otherwise.
func TestTermLinkOpensEditor(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	os.MkdirAll(filepath.Join(a.workspace, "src"), 0o755)
	os.MkdirAll(filepath.Join(a.workspace, "sub"), 0o755)
	os.WriteFile(filepath.Join(a.workspace, "src", "a.go"), []byte("package a\n\nfunc A() {}\n"), 0o644)
	id := a.activeSplitLeaf()
	a.status[id] = session.Info{ID: id, Session: a.tabs[0].ID, Dir: a.workspace}

	if !a.openTermLink(id, "src/a.go:3:6") {
		t.Fatal("a path in the project wasn't opened")
	}
	if a.activeView != tabEditor || a.tabs[a.activeTab].Path != "src/a.go" {
		t.Fatalf("view %v, tab %+v", a.activeView, a.tabs[a.activeTab])
	}
	if pos := a.editors["src/a.go"].State.CursorPos(); pos != (editor.Pos{Line: 2, Col: 5}) {
		t.Fatalf("caret at %+v", pos)
	}

	// From a subfolder the program runs in.
	a.status[id] = session.Info{ID: id, Session: a.tabs[0].ID, Dir: filepath.Join(a.workspace, "sub")}
	if !a.openTermLink(id, "../src/a.go:1") || a.editors["src/a.go"].State.CursorPos().Line != 0 {
		t.Fatal("a relative path wasn't taken from the pane's directory")
	}

	// Out of the project: no editor tab (and no window to hand it to the
	// system).
	outside := filepath.Join(t.TempDir(), "o.txt")
	os.WriteFile(outside, []byte("x"), 0o644)
	a.openTermLink(id, outside+":1")
	for _, tab := range a.tabs {
		if tab.Kind == tabEditor && tab.Path != "src/a.go" {
			t.Fatalf("opened %q in the editor", tab.Path)
		}
	}
	if a.openTermLink(id, "nothing/like/this.go:3") {
		t.Fatal("opened a file that is none")
	}
	a.settings.OpenLinks = "system"
	if a.openTermLink(id, "https://example.com") {
		t.Fatal("took a web link from the system")
	}
}
