package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// How omaterm moves: quickly and a little. Hovers and colors fade in
// motionFast, things come and go in motionBase, and panels slide and
// resize in motionPanel. Nothing waits on a motion to take input.
const (
	motionFast  = 120 * time.Millisecond
	motionBase  = 180 * time.Millisecond
	motionPanel = 260 * time.Millisecond
)

var (
	// springPanel slides and resizes, without bouncing.
	springPanel = ui.Animation{Spring: ui.SpringOptions{Duration: motionPanel}}
	// springGlide moves what follows the pointer, as the drop preview.
	springGlide = ui.Animation{Spring: ui.SpringOptions{Duration: 300 * time.Millisecond, Bounce: 0.12}}
	// springPop lands what the user just made or moved, with a light
	// bounce.
	springPop = ui.Animation{Spring: ui.SpringOptions{Duration: 420 * time.Millisecond, Bounce: 0.18}}
)

// motionOff stills every motion, for tests whose pictures and positions
// must not depend on a clock they don't advance.
var motionOff bool

// still reports whether omaterm should not move: Reduce Motion in its
// settings or the desktop's.
func (a *appState) still() bool {
	return motionOff || a.settings.ReduceMotion || a.osStill
}

// motionFrame starts a frame of a window, whose dialogs are dialogs: it
// reads the desktop's Reduce Motion.
func (a *appState) motionFrame(c *ui.Context, dialogs *shown) {
	a.osStill = c.Preferences().ReduceMotion
	dialogs.frame()
	a.dialogs = dialogs
}

// animColor animates e's color to target, or, still, gives it at once.
func (a *appState) animColor(e ui.Element, key string, target ui.Color, d time.Duration) ui.Color {
	if a.still() {
		return target
	}
	return e.AnimateColor(key, target, ui.Animation{Duration: d})
}

// animate animates a value of e to target, or, still, gives it at once.
func (a *appState) animate(e ui.Element, key string, target float32, d time.Duration) float32 {
	if a.still() {
		return target
	}
	return e.Animate(key, target, d)
}

// animateTo moves a value of e to target as anim says, or, still, gives
// it at once.
func (a *appState) animateTo(e ui.Element, key string, target float32, anim ui.Animation) float32 {
	if a.still() {
		return e.Jump(key, target)
	}
	return e.AnimateTo(key, target, anim)
}

// fadeColors fades e's background and border colors as they change, as
// on hover.
func (a *appState) fadeColors(e ui.Element) ui.Element {
	if a.still() {
		return e
	}
	return e.Transition(ui.ElementTransition{Colors: true, Duration: motionFast})
}

// Ways in and out: rows grow in and fold away, the others sliding to
// their places; banners drop in; chips pop; hover buttons fade.
var (
	// motionFold animates where a row is, not its size, which a panel
	// being resized changes: the rows would trail the pointer.
	motionFold = ui.ElementTransition{
		Duration: motionBase,
		Position: true,
		Enter:    &ui.Motion{Collapse: true},
		Exit:     &ui.Motion{Collapse: true},
	}
	// motionDrop fades in from a little above, and out back up: for
	// what the content below makes room for at once, as a banner, since
	// a terminal resized frame by frame would reflow its shell each time.
	motionDrop = ui.ElementTransition{
		Duration: motionBase,
		Enter:    &ui.Motion{Y: -6},
		Exit:     &ui.Motion{Y: -6},
	}
	motionPop = ui.ElementTransition{
		Duration: motionBase,
		Enter:    &ui.Motion{Scale: 0.9},
		Exit:     &ui.Motion{Scale: 0.9},
	}
	// motionTab brings a tab in and folds a closed one away, the others
	// sliding to their places.
	motionTab = ui.ElementTransition{
		Duration: motionBase,
		Position: true,
		Colors:   true,
		Enter:    &ui.Motion{Scale: 0.9},
		Exit:     &ui.Motion{Collapse: true, Scale: 0.9},
	}
	// motionFade fades in and out, as the buttons a hover shows.
	motionFade = ui.ElementTransition{
		Duration: motionFast,
		Colors:   true,
		Enter:    &ui.Motion{},
		Exit:     &ui.Motion{},
	}
)

// transition gives e t, the way it moves, comes and goes, unless still.
func (a *appState) transition(e ui.Element, t ui.ElementTransition) ui.Element {
	if a.still() {
		return e
	}
	return e.Transition(t)
}

// swap cross-fades e's content (text, icon) as it changes; numbers roll.
func (a *appState) swap(e ui.Element, numeric bool) ui.Element {
	if a.still() {
		return e
	}
	return e.ContentTransition(ui.ContentTransition{Numeric: numeric})
}

// shown is which dialogs a window built in its last frame, and builds in
// this one, by their panels' IDs: one coming in is new to it.
type shown struct{ last, now map[uint64]bool }

// frame starts a frame of the window.
func (s *shown) frame() {
	s.last, s.now = s.now, s.last
	if s.now == nil {
		s.now = map[uint64]bool{}
	}
	clear(s.now)
}

// arriveDialog brings in a dialog: it fades in with its scrim as its
// panel drops into place. The frame that first builds it, slow with all
// it lays out, shows it clear; the motion starts in the next one and goes
// on in frames that only paint, so the window isn't built again for it.
func (a *appState) arriveDialog(c *ui.Context, backdrop, panel ui.Element) {
	if a.still() || a.dialogs == nil {
		return
	}
	id := panel.ID()
	a.dialogs.now[id] = true
	fade := ui.Animation{Duration: motionBase}
	if !a.dialogs.last[id] {
		backdrop.AnimatePose(ui.Pose{Fade: 1}, fade)
		panel.AnimatePose(ui.Pose{Y: -8}, springPanel)
		c.Invalidate()
		return
	}
	backdrop.AnimatePose(ui.Pose{}, fade)
	panel.AnimatePose(ui.Pose{}, springPanel)
}

// iconPop is a status icon's last state, by its element, and how many
// times it changed.
type iconPop struct {
	state int
	n     uint64
}

// popOnChange pops e, a status icon, when its state changes: it shrinks
// and springs back, a little past its size. The first state it shows
// doesn't pop, or every icon would as the window opened.
func (a *appState) popOnChange(e ui.Element, state int) {
	if a.iconPops == nil || len(a.iconPops) > 4096 { // the icons of a long day, gone
		a.iconPops = map[uint64]iconPop{}
	}
	id := e.ID()
	p, ok := a.iconPops[id]
	if !ok {
		a.iconPops[id] = iconPop{state: state}
		return
	}
	if p.state != state {
		p.state, p.n = state, p.n+1
		a.iconPops[id] = p
	}
	if p.n == 0 || a.still() {
		return
	}
	s := e.Keyframes("pop", []ui.Keyframe{
		{Value: 0.55},
		{At: motionFast, Value: 1.12, Ease: ui.EaseOut},
		{At: motionBase + 60*time.Millisecond, Value: 1, Ease: ui.EaseInOut},
	}, ui.KeyframeOptions{Run: p.n})
	e.Scale(s, s)
}

// press sinks e a little while it is pressed.
func (a *appState) press(e ui.Element) ui.Element {
	if a.still() {
		return e
	}
	s := e.Animate("press", b2f(!e.Pressed())*0.03+0.97, motionFast/2)
	return e.Scale(s, s)
}
