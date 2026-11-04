package main

import (
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// The Info panel carries the app's own way to check for updates only in a
// build that updates itself, as a Linux window has no menu bar for the
// plugin's item. One that can't, as in the tests, says why in Settings ›
// About instead of under every project.
func TestUpdateSection(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.inspectorVisible = true
	a.showInspectorTab(0)

	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	if updatesReady && mygo.Updater.Enabled() {
		t.Skip("this build updates itself")
	}
	if tr.HasText("UPDATES") {
		t.Error("the Info panel shows an UPDATES card that a build that can't update itself can't use")
	}
}
