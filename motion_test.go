package main

import (
	"image/color"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// scrimAt is the window's color in its bottom-left corner, which a
// dialog's scrim darkens.
func scrimAt(tr *ui.Tester) color.RGBA {
	img := tr.Image()
	b := img.Bounds()
	return img.RGBAAt(b.Min.X+4, b.Max.Y-4)
}

// openQuitDialog shows the dialog asking to quit with editors unsaved.
func openQuitDialog(a *appState, tr *ui.Tester) {
	open := true
	a.quitAsk = &open
	tr.Frame()
}

// A dialog comes in: its scrim starts clear and darkens over a moment, as
// its panel drops in; the Quit button is there to click all along.
func TestDialogArrives(t *testing.T) {
	withMotion(t)
	a := newTestApp(t)
	defer stopTermPanes(a)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	before := scrimAt(tr)
	openQuitDialog(a, tr)
	if got := scrimAt(tr); got != before {
		t.Fatalf("the scrim was dark as the dialog opened: %v, was %v", got, before)
	}
	if !tr.HasText("Quit Anyway") {
		t.Fatalf("the dialog's buttons wait for it to come in: %v", tr.Texts())
	}
	tr.Advance(time.Second)
	tr.Frame()
	if got := scrimAt(tr); got == before {
		t.Fatalf("the scrim never darkened: %v", got)
	}
	if tr.FramePending() {
		t.Fatal("the dialog still moves once it came in")
	}
}

// With Reduce Motion on, in omaterm's settings or the desktop's, a
// dialog is there at once.
func TestDialogStillWithReduceMotion(t *testing.T) {
	withMotion(t)
	for _, desktop := range []bool{false, true} {
		a := newTestApp(t)
		tr := ui.NewTester(a.view, 1440, 900)
		if desktop {
			tr.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
		} else {
			a.settings.ReduceMotion = true
		}
		tr.Frame()
		before := scrimAt(tr)
		openQuitDialog(a, tr)
		if got := scrimAt(tr); got == before {
			t.Errorf("desktop %v: the dialog faded in with Reduce Motion on", desktop)
		}
		stopTermPanes(a)
	}
}

// A dialog coming in moves in frames that only paint: the window isn't
// built again for each, which stuttered with a big view.
func TestDialogArrivesWithoutBuilding(t *testing.T) {
	withMotion(t)
	a := newTestApp(t)
	defer stopTermPanes(a)
	builds := 0
	tr := ui.NewTester(func(c *ui.Context) { builds++; a.view(c) }, 1440, 900)
	tr.Frame()
	tr.Advance(time.Second)
	tr.Frame()
	quiet := builds
	tr.Advance(time.Second)
	quiet = builds - quiet // what the window builds of itself in a second
	openQuitDialog(a, tr)
	tr.Advance(time.Second / 60)
	start := builds
	tr.Advance(300 * time.Millisecond)
	if n := builds - start; n > quiet+1 {
		t.Fatalf("the window was built %d times as the dialog came in (%d in a quiet second)", n, quiet)
	}
}

// A dialog open as the window first shows, as the daemon's, comes in.
func TestDialogOnFirstFrame(t *testing.T) {
	withMotion(t)
	a := newTestApp(t)
	defer stopTermPanes(a)
	open := true
	a.quitAsk = &open
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	tr.Advance(time.Second)
	tr.Frame()
	if !tr.HasText("Quit Anyway") {
		t.Fatalf("no dialog: %v", tr.Texts())
	}
}
