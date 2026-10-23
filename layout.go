package main

// The panes of a session as cards: a split tree laid out with gaps
// between its panes, dividers in the gaps that a drag moves, and pane
// headers that a drag takes to another place in the session. The layout,
// drop zones and drop preview follow Kero's (github.com/egoist/kero, MIT,
// docs/third_party/kero-LICENSE), panes.go.

import (
	"math"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// paneGap is the room between panes, and around them.
func (a *appState) paneGap() float32 {
	if a.settings.Density == "compact" {
		return 4
	}
	return 8
}

func (a *appState) cardRadius() float32 {
	if a.settings.Density == "compact" {
		return 8
	}
	return 10
}

// handle returns the handle of a keyed element, made on first use.
func (a *appState) handle(key string) *ui.Handle {
	if a.handles == nil {
		a.handles = map[string]*ui.Handle{}
	}
	h := a.handles[key]
	if h == nil {
		h = &ui.Handle{}
		a.handles[key] = h
	}
	return h
}

// renderPanes lays out the active tab's panes: the zoomed one alone, or
// the split tree.
func (a *appState) renderPanes(c *ui.Context) {
	root := a.ensureSingleSplit()
	tab := &a.tabs[a.activeTab]
	multi := !root.isLeaf()
	if tab.Zoomed != "" && root.contains(tab.Zoomed) {
		a.paneCard(c, tab, tab.Zoomed, multi)
		return
	}
	tab.Zoomed = ""
	a.layoutNode(c, tab, root, "", multi)
}

func (n *splitNode) contains(id string) bool {
	for _, t := range n.termIDs() {
		if t == id {
			return true
		}
	}
	return false
}

func (a *appState) layoutNode(c *ui.Context, tab *topTab, n *splitNode, path string, multi bool) {
	if n.isLeaf() {
		a.paneCard(c, tab, n.TermID, multi)
		return
	}
	ratio := n.Ratio
	if ratio <= 0 || ratio >= 100 {
		ratio = 50
	}
	// Row and Column each make an element, so make only the one used.
	key := "split-" + tab.ID + path
	var box ui.Element
	if n.Dir == splitVertical {
		box = ui.Row(c.Key(key))
	} else {
		box = ui.Column(c.Key(key))
	}
	box.Grow(1).Basis(0).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch).Bind(a.handle(key))
	box.Children(func() {
		ui.Column(c.Key("a")).Grow(ratio).Basis(0).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			a.layoutNode(c, tab, n.First, path+"a", multi)
		})
		a.divider(c, n, key)
		ui.Column(c.Key("b")).Grow(100 - ratio).Basis(0).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			a.layoutNode(c, tab, n.Second, path+"b", multi)
		})
	})
}

// divider is the gap between two panes, which a drag moves; a double
// click shares the room evenly again.
func (a *appState) divider(c *ui.Context, n *splitNode, key string) {
	gap := a.paneGap()
	row := n.Dir == splitVertical
	d := ui.Box(c.Key("div")).Shrink(0).KeepFocus()
	if row {
		d.Width(gap).Cursor(ui.CursorResizeColumn)
	} else {
		d.Height(gap).Cursor(ui.CursorResizeRow)
	}
	if d.Hovered() || d.Pressed() {
		d.Draw(func(p *ui.Painter, r ui.Rect) {
			if row {
				p.Fill(ui.Rect{X: r.X + r.W/2 - 1.5, Y: r.Y + r.H/2 - 18, W: 3, H: 36}, pal.TextFaint.Alpha(0.7), 1.5)
			} else {
				p.Fill(ui.Rect{X: r.X + r.W/2 - 18, Y: r.Y + r.H/2 - 1.5, W: 36, H: 3}, pal.TextFaint.Alpha(0.7), 1.5)
			}
		})
	}
	if dx, dy, ok := d.Dragged(); ok {
		b := a.handle(key).Bounds(c)
		size, delta := b.W-gap, dx
		if !row {
			size, delta = b.H-gap, dy
		}
		if size > 0 {
			// The ratio goes on past its bounds, unclamped, so the divider
			// comes back as the pointer does.
			if a.layoutDrag.split != key {
				r := float64(n.Ratio)
				if r <= 0 || r >= 100 {
					r = 50
				}
				a.layoutDrag.split, a.layoutDrag.ratio = key, r
			}
			a.layoutDrag.ratio += float64(delta / size * 100)
			n.Ratio = float32(math.Min(math.Max(a.layoutDrag.ratio, 12), 88))
		}
	} else if a.layoutDrag.split == key && !d.Pressed() {
		a.layoutDrag.split = ""
		a.saveSoon()
	}
	if d.DoubleClicked() {
		n.Ratio = 50
		a.saveSoon()
	}
}

// paneCard is a pane: a card with its header and its terminal.
func (a *appState) paneCard(c *ui.Context, tab *topTab, id string, multi bool) {
	t := a.term(id)
	focused := a.activePaneTermID == id
	card := ui.Column(c.Key("pane-" + id)).Grow(1).Basis(0).MinWidth(0).MinHeight(0).
		Radius(a.cardRadius()).Clip().Bind(a.handle("card-" + id))
	card.Background(card.AnimateColor("bg", pal.Card, ui.Animation{Duration: 140 * time.Millisecond}))
	raised(card)
	if multi && focused {
		card.BorderColor(card.AnimateColor("ring", pal.Accent.Alpha(0.55), ui.Animation{Duration: 140 * time.Millisecond}))
	}
	hovered := card.Hovered()
	if card.Pressed() && !focused {
		a.focusPane(id)
	}

	// Another pane dragged over this one: where it would go.
	zone := zoneNone
	if multi {
		if d, over := ui.DragOver[paneDrag](card); over && d.tab == tab.ID && d.pane != id {
			if x, y, in := card.PointerPosition(); in {
				b := a.handle("card-" + id).Bounds(c)
				zone = zoneAt(x/max(b.W, 1), y/max(b.H, 1))
			}
		}
		if d, ok := ui.Drop[paneDrag](card); ok && d.tab == tab.ID && d.pane != id {
			target, drop := id, a.lastZone
			a.post(func() { a.movePane(d.pane, target, drop) })
		}
	}
	if zone != zoneNone {
		a.lastZone = zone
	}

	dragging := false
	card.Children(func() {
		dragging = a.paneHeader(c, tab, id, t, multi, focused, hovered)
		if t == nil {
			ui.Box(c).Grow(1).Padding(12).Children(func() {
				ui.Text(c, "The shell could not start").FontSize(12).TextColor(pal.Bad)
			})
		} else {
			box := ui.Box(c.Key("term")).Grow(1).MinHeight(0).Clip()
			if box.Hovered() && !focused && a.settings.FocusFollowsMouse {
				a.focusPane(id)
			}
			box.Children(func() {
				v := terminal.View(c, t).Fill().Bind(a.handle("term-" + id))
				if focused {
					v.AutoFocus()
				}
			})
		}
		if multi && a.settings.DimUnfocused {
			// The panes the user is not in fade, text and all.
			v := ui.Box(c.Key("veil")).Absolute().Top(0).Left(0).Right(0).Bottom(0).PassThrough().Background(pal.Veil)
			on := float32(0)
			if !focused {
				on = 1
			}
			v.Opacity(v.Animate("on", on, 160*time.Millisecond))
		}
		if zone != zoneNone {
			a.dropPreview(c, id, zone)
		}
	})

	// The pane dragged sinks back where it was; one just moved springs in
	// at its new place.
	pose := ui.Pose{}
	switch {
	case dragging:
		pose = ui.Pose{Scale: 0.985, Fade: 0.45}
	case a.arrived[id]:
		pose = ui.Pose{Scale: 0.94, Fade: 0.7}
		delete(a.arrived, id)
		c.Invalidate()
	}
	card.AnimatePose(pose, ui.Animation{Spring: ui.SpringOptions{Duration: 420 * time.Millisecond, Bounce: 0.18}})
}

// focusPane gives a pane of the active tab the keyboard.
func (a *appState) focusPane(id string) {
	a.activePaneTermID = id
	a.handle("term-" + id).Focus()
}

// paneDrag is a pane dragged by its header to another place in its tab.
type paneDrag struct{ tab, pane string }

// dropZone is where a pane dropped on another goes: beside it, on a side,
// or in its place, which the other takes (swap).
type dropZone int

const (
	zoneNone dropZone = iota
	zoneLeft
	zoneRight
	zoneUp
	zoneDown
	zoneSwap
)

// zoneAt returns the zone under the pointer, at u, v of a pane (0 to 1):
// its middle swaps, else the nearest edge.
func zoneAt(u, v float32) dropZone {
	if u > 0.3 && u < 0.7 && v > 0.3 && v < 0.7 {
		return zoneSwap
	}
	best, zone := u, zoneLeft
	for _, e := range []struct {
		d float32
		z dropZone
	}{{1 - u, zoneRight}, {v, zoneUp}, {1 - v, zoneDown}} {
		if e.d < best {
			best, zone = e.d, e.z
		}
	}
	return zone
}

// dropPreview shows, over the pane under the pointer, the room the
// dragged pane would take, gliding from zone to zone.
func (a *appState) dropPreview(c *ui.Context, target string, zone dropZone) {
	if a.previewKey != target {
		a.previewKey, a.previewSince = target, c.Now()
	}
	r := [...][4]float32{
		zoneLeft: {0, 0, 50, 100}, zoneRight: {50, 0, 50, 100},
		zoneUp: {0, 0, 100, 50}, zoneDown: {0, 50, 100, 50}, zoneSwap: {0, 0, 100, 100},
	}[zone]
	label := map[dropZone]string{zoneLeft: "Split left", zoneRight: "Split right", zoneUp: "Split up", zoneDown: "Split down", zoneSwap: "Swap"}[zone]
	glyph := iconColumns2
	if zone == zoneUp || zone == zoneDown {
		glyph = iconRows2
	}
	if zone == zoneSwap {
		glyph = iconRefresh
	}
	fadeIn := min(float32(c.Now().Sub(a.previewSince))/float32(140*time.Millisecond), 1)
	if fadeIn < 1 {
		c.AnimationFrame()
	}
	spring := ui.Animation{Spring: ui.SpringOptions{Duration: 300 * time.Millisecond, Bounce: 0.12}}
	o := ui.Column(c.Key("drop")).Absolute().PassThrough().Padding(8).Opacity(fadeIn)
	o.LeftPercent(o.AnimateTo("l", r[0], spring)).TopPercent(o.AnimateTo("t", r[1], spring)).
		WidthPercent(o.AnimateTo("w", r[2], spring)).HeightPercent(o.AnimateTo("h", r[3], spring))
	o.Children(func() {
		room := ui.Column(c).Grow(1).Radius(9).AlignItems(ui.Center).Justify(ui.Center).
			Background(pal.Accent.Alpha(0.10)).InsetShadow(0, 0, 28, 0, pal.Accent.Alpha(0.22)).
			Border(1.5, pal.Accent.Alpha(0.45))
		room.Children(func() {
			pill := ui.Row(c).Height(28).Padding(0, 12).Gap(7).Radius(14).AlignItems(ui.Center).
				Background(pal.Accent).TextColor(pal.AccentText)
			floating(pill).Children(func() {
				ui.Icon(c, glyph).Size(13, 13).TextColor(pal.AccentText)
				ui.Text(c, label).FontSize(12).FontWeight(600).SingleLine().TextColor(pal.AccentText)
			})
		})
	})
}

// movePane puts a pane dropped on another beside it, or in its place.
func (a *appState) movePane(pane, target string, zone dropZone) {
	if a.activeTab < 0 || a.activeTab >= len(a.tabs) || zone == zoneNone {
		return
	}
	tab := &a.tabs[a.activeTab]
	if tab.Split == nil || !tab.Split.contains(pane) || !tab.Split.contains(target) {
		return
	}
	if zone == zoneSwap {
		swapLeaves(tab.Split, pane, target)
		a.arrived[target] = true
	} else {
		tab.Split = moveLeaf(tab.Split, pane, target, zone)
	}
	a.arrived[pane] = true
	tab.Zoomed = ""
	a.focusPane(pane)
	a.saveSoon()
}

// swapLeaves trades the places of two panes.
func swapLeaves(n *splitNode, x, y string) {
	if n == nil {
		return
	}
	if n.isLeaf() {
		switch n.TermID {
		case x:
			n.TermID = y
		case y:
			n.TermID = x
		}
		return
	}
	swapLeaves(n.First, x, y)
	swapLeaves(n.Second, x, y)
}

// moveLeaf takes a pane from its place and puts it beside target, on the
// side the zone names; it returns the new root.
func moveLeaf(root *splitNode, pane, target string, zone dropZone) *splitNode {
	root, ok := removeLeaf(root, pane)
	if !ok || root == nil {
		return root
	}
	var replace func(n *splitNode) *splitNode
	replace = func(n *splitNode) *splitNode {
		if n.isLeaf() {
			if n.TermID != target {
				return n
			}
			moved, kept := &splitNode{TermID: pane}, &splitNode{TermID: target}
			s := &splitNode{Ratio: 50}
			switch zone {
			case zoneLeft:
				s.Dir, s.First, s.Second = splitVertical, moved, kept
			case zoneRight:
				s.Dir, s.First, s.Second = splitVertical, kept, moved
			case zoneUp:
				s.Dir, s.First, s.Second = splitHorizontal, moved, kept
			default:
				s.Dir, s.First, s.Second = splitHorizontal, kept, moved
			}
			return s
		}
		n.First, n.Second = replace(n.First), replace(n.Second)
		return n
	}
	return replace(root)
}

// toggleZoom shows the focused pane alone, or the split again.
func (a *appState) toggleZoom() {
	if a.activeTab < 0 || a.activeTab >= len(a.tabs) {
		return
	}
	tab := &a.tabs[a.activeTab]
	if tab.Zoomed != "" {
		tab.Zoomed = ""
		return
	}
	if tab.Split != nil && !tab.Split.isLeaf() {
		tab.Zoomed = a.activeSplitLeaf()
	}
}
