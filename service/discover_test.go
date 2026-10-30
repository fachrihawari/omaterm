package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pat, path string
		want      bool
	}{
		{"*.go", "a.go", true},
		{"*.go", "x/a.go", false}, // * stays within one segment
		{"**/*.go", "x/y/a.go", true},
		{"**/*.go", "a.go", true},
		{"src/**", "src/a/b", true},
		{"src/**", "lib/a/b", false},
		{"internal/*/x.go", "internal/mcp/x.go", true},
		{"internal/*/x.go", "internal/a/b/x.go", false},
		{"a?c.go", "abc.go", true},
		{"a?c.go", "ac.go", false},
	}
	for _, c := range cases {
		if got := MatchGlob(c.pat, c.path); got != c.want {
			t.Errorf("MatchGlob(%q, %q) = %v, want %v", c.pat, c.path, got, c.want)
		}
	}
}

func TestIgnoreFunc(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "dist/\n*.log\n")

	ignore := IgnoreFunc(root)
	cases := []struct {
		path  string
		isDir bool
		want  bool
	}{
		{"dist/x.js", false, true}, // the .gitignore's dist/
		{"dist", true, true},       // and the folder itself
		{"node_modules/x", false, true},
		{"node_modules", true, true}, // never opened
		{".git/config", false, true},
		{"src/a.go", false, false},
		{"a.log", false, true}, // *.log
		{"src/a.log", false, true},
		{"./src/a.go", false, false}, // how a watch may spell it
		{"", false, false},           // the root itself is never ignored
		{".", false, false},
	}
	for _, c := range cases {
		if got := ignore(c.path, c.isDir); got != c.want {
			t.Errorf("IgnoreFunc(%q, %v) = %v, want %v", c.path, c.isDir, got, c.want)
		}
	}
}

// A repository without a .gitignore still skips the folders no watch
// should open.
func TestIgnoreFuncWithoutGitIgnore(t *testing.T) {
	ignore := IgnoreFunc(t.TempDir())
	if !ignore("node_modules/x", false) || !ignore(".git/HEAD", false) {
		t.Error("a heavy folder is watched")
	}
	if ignore("main.go", false) {
		t.Error("a file of the project is ignored")
	}
}
