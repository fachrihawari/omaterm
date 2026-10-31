package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
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

	// Out of the project, as a plan under ~/.claude: in the editor too,
	// by its absolute path, and saved there.
	outside := filepath.Join(t.TempDir(), "o.txt")
	os.WriteFile(outside, []byte("x\ny\n"), 0o644)
	if !a.openTermLink(id, outside+":2") || a.tabs[a.activeTab].Path != outside {
		t.Fatalf("a file out of the project wasn't opened: tab %+v", a.tabs[a.activeTab])
	}
	doc := a.editors[outside]
	if doc == nil || doc.State.CursorPos().Line != 1 {
		t.Fatal("the file out of the project isn't in the editor at its line")
	}
	doc.State.Buffer.Set("zx\ny\n")
	ui.NewTester(func(c *ui.Context) { a.writeEditor(doc, c) }, 400, 300).Frame()
	if b, _ := os.ReadFile(outside); string(b) != "zx\ny\n" {
		t.Fatalf("saved %q", b)
	}
	if _, err := os.Stat(filepath.Join(a.workspace, outside)); err == nil {
		t.Fatal("saved under the project")
	}
	if a.openTermLink(id, "nothing/like/this.go:3") {
		t.Fatal("opened a file that is none")
	}
	a.settings.OpenLinks = "system"
	if a.openTermLink(id, "https://example.com") {
		t.Fatal("took a web link from the system")
	}
}

// A file URL's /C:/a.go names C:/a.go on Windows, where a leading / would
// otherwise read as a folder called C:.
func TestParseFileLinkWindows(t *testing.T) {
	for _, c := range []struct {
		link      string
		path      string
		line, col int
	}{
		{"file:///C:/a.go", "C:/a.go", 0, 0},
		{"file:///c:/src/a%20b.go:3:2", "c:/src/a b.go", 3, 2},
		{"/C:/a.go:12", "C:/a.go", 12, 0},
		{"/x/a.go", "/x/a.go", 0, 0},
		{"src/a.go:12", "src/a.go", 12, 0},
	} {
		path, line, col := parseFileLinkOS("windows", c.link)
		if path != c.path || line != c.line || col != c.col {
			t.Errorf("%q: got %q %d %d, want %q %d %d", c.link, path, line, col, c.path, c.line, c.col)
		}
	}
	if path, _, _ := parseFileLinkOS("linux", "file:///C:/a.go"); path != "/C:/a.go" {
		t.Errorf("Linux got %q, want the folder /C:", path)
	}
}

// A path rooted at / from a Windows pane is on its drive, as Git Bash
// prints it: /src/a.go in C:\proj is C:\src\a.go, not C:\proj\src\a.go.
func TestTermLinkPath(t *testing.T) {
	for _, c := range []struct {
		goos, dir, path, want string
	}{
		{"windows", `C:\proj`, "/src/a.go", "C:/src/a.go"},
		{"windows", `c:\proj`, `\src\a.go`, `c:\src\a.go`},
		{"windows", `C:\proj`, "C:/a.go", "C:/a.go"},
		{"windows", `C:\proj`, "src/a.go", filepath.Join(`C:\proj`, "src/a.go")},
		{"linux", "/proj", "/src/a.go", "/src/a.go"},
		{"linux", "/proj", "src/a.go", "/proj/src/a.go"},
	} {
		if got := termLinkPath(c.goos, c.dir, c.path); got != c.want {
			t.Errorf("termLinkPath(%q, %q) = %q, want %q", c.dir, c.path, got, c.want)
		}
	}
}
