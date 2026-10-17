package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// Renders the app's views to PNGs in testdata/ for visual comparison
// against the mock. Run: go test -run TestShots
func TestShots(t *testing.T) {
	shots := []struct {
		name  string
		setup func(a *appState)
	}{
		{"terminal", nil},
		{"editor", func(a *appState) { a.activeView = tabEditor }},
		{"diff", func(a *appState) { a.activeView = tabDiff }},
		{"diff-inline", func(a *appState) { a.activeView = tabDiff; a.diffSplit = false }},
		{"files", func(a *appState) { a.inspectorTab = 1 }},
		{"git", func(a *appState) { a.inspectorTab = 2 }},
		{"single", func(a *appState) { a.layout = layoutSingle }},
		{"no-sidebars", func(a *appState) { a.sidebarVisible = false; a.inspectorVisible = false }},
	}
	for _, s := range shots {
		a := newApp()
		if s.setup != nil {
			s.setup(a)
		}
		tr := ui.NewTester(a.view, 1440, 900)
		os.MkdirAll("testdata", 0o755)
		f, err := os.Create(filepath.Join("testdata", "shot-"+s.name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, tr.Image()); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}
