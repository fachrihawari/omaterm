package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// The Files tab's New File makes a file, folders in its name and all,
// shows it and opens it; a name there already, or out of the project,
// leaves the field to fix it; Escape drops the field. New Folder, from a
// folder's menu, makes one in it.
func TestFilesNewFileAndFolder(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	root := a.workspace
	os.MkdirAll(filepath.Join(root, "dir"), 0o755)
	os.WriteFile(filepath.Join(root, "taken.txt"), nil, 0o644)
	a.showInspectorTab(1)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	if err := tr.Click("New File"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if a.newEntry == nil || a.newEntry.dir != root || a.newEntry.folder {
		t.Fatalf("New File: %+v", a.newEntry)
	}
	tr.Type("src/app/main.go")
	tr.Key(0, ui.KeyEnter)
	tr.Frame()
	if _, err := os.Stat(filepath.Join(root, "src", "app", "main.go")); err != nil {
		t.Fatalf("the file wasn't made: %v", err)
	}
	if a.newEntry != nil {
		t.Error("the field stayed after the file was made")
	}
	if a.activeView != tabEditor || a.tabs[a.activeTab].Path != "src/app/main.go" {
		t.Errorf("the new file isn't open: view %v tab %+v", a.activeView, a.tabs[a.activeTab])
	}
	if n := a.fileNodeAt(filepath.Join(root, "src", "app")); n == nil || !n.Open || len(n.Children) != 1 {
		t.Errorf("the new file's folder isn't shown open with it: %+v", n)
	}

	for _, name := range []string{"taken.txt", "../out.txt"} {
		a.startNewEntry(root, false)
		tr.Frame()
		tr.Type(name)
		tr.Key(0, ui.KeyEnter)
		tr.Frame()
		if a.newEntry == nil {
			t.Errorf("%s: the field closed", name)
		}
		tr.Key(0, ui.KeyEscape)
		tr.Frame()
		if a.newEntry != nil {
			t.Errorf("%s: Escape left the field", name)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "out.txt")); err == nil {
		t.Error("made a file out of the project")
	}

	a.showInspectorTab(1)
	tr.Frame()
	if err := tr.RightClick("dir"); err != nil {
		t.Fatal(err)
	}
	if err := tr.ChooseMenuItem("New Folder…"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if a.newEntry == nil || a.newEntry.dir != filepath.Join(root, "dir") || !a.newEntry.folder {
		t.Fatalf("New Folder… in dir: %+v", a.newEntry)
	}
	tr.Type("assets")
	tr.Key(0, ui.KeyEnter)
	tr.Frame()
	if st, err := os.Stat(filepath.Join(root, "dir", "assets")); err != nil || !st.IsDir() {
		t.Fatalf("the folder wasn't made: %v", err)
	}
	if n := a.fileNodeAt(filepath.Join(root, "dir", "assets")); n == nil || !n.Dir {
		t.Error("the new folder isn't listed")
	}
}
