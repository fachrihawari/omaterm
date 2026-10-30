package procs

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// copyFixture copies the omaterm.yml fixture into a new project folder.
func copyFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	data, err := os.ReadFile(filepath.Join("testdata", FileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, FileName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestSaveUnchangedKeepsFile(t *testing.T) {
	root := copyFixture(t)
	defs, err := LoadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveFile(root, defs); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, FileName))
	want, _ := os.ReadFile(filepath.Join("testdata", FileName))
	if string(got) != string(want) {
		t.Errorf("saving unchanged defs changed the file:\n%s", got)
	}
}

func TestSaveEditsKeepComments(t *testing.T) {
	root := copyFixture(t)
	defs, err := LoadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	// Change web, drop worker, add docs.
	web := defs[0]
	web.Command = "npm start"
	defs = []Def{web, defs[2], {Name: "docs", Command: "make docs", Port: 8080}}
	if err := SaveFile(root, defs); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, FileName))
	text := string(got)
	for _, keep := range []string{
		"# The processes every teammate runs for this project.",
		"name: myapp",
		"# The dev server, restarted when the source changes.",
		"  queue:\n    command: redis-server\n    port: 6379\n",
	} {
		if !strings.Contains(text, keep) {
			t.Errorf("the saved file lost %q:\n%s", keep, text)
		}
	}
	if strings.Contains(text, "worker") {
		t.Errorf("the deleted process is still there:\n%s", text)
	}
	back, err := LoadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []Def{
		{Name: "web", Command: "npm start", Port: 3000, Autostart: true, Watch: []string{"src/**"}},
		{Name: "queue", Command: "redis-server", Port: 6379},
		{Name: "docs", Command: "make docs", Port: 8080},
	}
	if !reflect.DeepEqual(back, want) {
		t.Errorf("loaded %+v, want %+v", back, want)
	}
}

func TestLoadFileMissing(t *testing.T) {
	defs, err := LoadFile(t.TempDir())
	if defs != nil || err != nil {
		t.Errorf("got %v, %v; want nil, nil", defs, err)
	}
}

func TestLoadFileTooLarge(t *testing.T) {
	root := t.TempDir()
	big := []byte("processes:\n  web:\n    command: x\n# " + strings.Repeat("x", maxFile) + "\n")
	os.WriteFile(filepath.Join(root, FileName), big, 0o644)
	if _, err := LoadFile(root); err == nil {
		t.Error("a file over 1 MB loaded")
	}
}

func TestSaveFileFresh(t *testing.T) {
	root := t.TempDir()
	if err := SaveFile(root, []Def{{Name: "web", Command: "npm run dev"}}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, FileName))
	want := "processes:\n  web:\n    command: npm run dev\n"
	if string(got) != want {
		t.Errorf("fresh file:\n%s\nwant:\n%s", got, want)
	}
}

func TestSaveFileSkipsLocal(t *testing.T) {
	root := t.TempDir()
	if err := SaveFile(root, []Def{{Name: "web", Command: "x", Local: true}}); err != nil {
		t.Fatal(err)
	}
	defs, err := LoadFile(root)
	if err != nil || len(defs) != 0 {
		t.Errorf("a local def went to the file: %v, %v", defs, err)
	}
}
