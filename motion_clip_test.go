package main

import (
	"bytes"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/theme"
)

// motionScene is a change to film: set prepares the app, act makes the
// change, and the clip runs on for long after it.
type motionScene struct {
	name string
	set  func(a *appState, tr *ui.Tester)
	act  func(a *appState, tr *ui.Tester)
	long time.Duration
}

var motionScenes = []motionScene{
	{name: "palette", act: func(a *appState, tr *ui.Tester) { a.openOverlay(overlayCommandPalette) }},
	{name: "confirm", act: func(a *appState, tr *ui.Tester) { open := true; a.quitAsk = &open }},
	{name: "banner", act: func(a *appState, tr *ui.Tester) { a.agentSetupOffer = "opencode" }},
}

// TestMotionClips films each scene in the light palette, still and
// moving, at 60 frames a second, into MOTION_CLIP_DIR/<scene>/{still,
// moving}/NNNNN.png, for judging a motion beside the UI before it.
func TestMotionClips(t *testing.T) {
	dir := os.Getenv("MOTION_CLIP_DIR")
	if dir == "" {
		t.Skip("MOTION_CLIP_DIR is not set")
	}
	only := os.Getenv("MOTION_CLIP_SCENES")
	defer setPalette(theme.BuiltinDark())
	defer func() { motionOff = true }()
	for _, sc := range motionScenes {
		if only != "" && !bytes.Contains([]byte(","+only+","), []byte(","+sc.name+",")) {
			continue
		}
		for _, still := range []bool{true, false} {
			motionOff = still
			sub := "moving"
			if still {
				sub = "still"
			}
			out := filepath.Join(dir, sc.name, sub)
			if err := os.MkdirAll(out, 0o755); err != nil {
				t.Fatal(err)
			}
			a := newTestApp(t)
			setPalette(theme.BuiltinLight())
			tr := ui.NewTester(a.view, 1200, 760)
			tr.SetDark(false)
			if sc.set != nil {
				sc.set(a, tr)
			}
			tr.Frame()
			tr.Advance(time.Second)
			tr.Frame()
			long := sc.long
			if long == 0 {
				long = 700 * time.Millisecond
			}
			n := 0
			shoot := func() {
				var buf bytes.Buffer
				if err := png.Encode(&buf, tr.Image()); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(out, fmt.Sprintf("%05d.png", n)), buf.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
				n++
			}
			for range 12 { // a moment before the change
				tr.Advance(time.Second / 60)
				tr.Frame()
				shoot()
			}
			sc.act(a, tr)
			tr.Frame()
			shoot()
			for el := time.Duration(0); el < long; el += time.Second / 60 {
				tr.Advance(time.Second / 60)
				tr.Frame()
				shoot()
			}
			stopTermPanes(a)
		}
	}
}
