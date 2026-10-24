package service

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// On macOS the home Library folder is never read: the other apps'
// containers in it make macOS block omaterm.
func TestProtectedLibrary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, f := range []string{"Library/Containers/app/x.txt", "src/a.go"} {
		p := filepath.Join(home, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("needle\n"), 0o644)
	}
	lib := filepath.Join(home, "Library")

	defer func(v bool) { onMac = v }(onMac)
	onMac = false
	if Protected(lib) {
		t.Fatal("Library protected off macOS")
	}

	onMac = true
	if !Protected(lib) || Protected(filepath.Join(lib, "Containers")) || Protected(home) {
		t.Fatal("only the home Library folder is protected")
	}
	if files := IndexFiles(home, 0); !slices.Equal(files, []string{"src/a.go"}) {
		t.Fatalf("indexed %v, want only src/a.go", files)
	}
	matches, err := searchContentGo(context.Background(), home, "needle")
	if err != nil || len(matches) != 1 || matches[0].Path != "src/a.go" {
		t.Fatalf("searched %v (%v), want only src/a.go", matches, err)
	}
	if HasRipgrep() {
		matches, err := searchContentRG(context.Background(), home, "needle")
		if err != nil || len(matches) != 1 || matches[0].Path != "src/a.go" {
			t.Fatalf("rg searched %v (%v), want only src/a.go", matches, err)
		}
	}
	// A project the user opens inside Library is searched as usual.
	if files := IndexFiles(lib, 0); len(files) != 1 {
		t.Fatalf("indexed %v inside Library, want its file", files)
	}
}
