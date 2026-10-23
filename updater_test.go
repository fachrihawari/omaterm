package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// The Info panel carries the app's own way to check for updates, since a
// Linux window has no menu bar for the plugin's item. In a build without
// the plugin, as in the tests, the button says so rather than panicking.
func TestUpdateSection(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.inspectorVisible = true
	a.showInspectorTab(0)

	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	if !tr.HasText("UPDATES") {
		t.Error("the Info panel has no UPDATES section")
	}
	if !tr.HasText("UPDATES") || !tr.HasText("omaterm") {
		t.Error("the UPDATES section does not name the app")
	}
	if err := tr.Click("Check"); err != nil {
		t.Fatalf("no Check button: %v", err)
	}
	tr.Frame() // must not panic
}
