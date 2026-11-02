package main

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/session"
)

// iconState is what a pane's status icon shows.
type iconState int

const (
	iconNone     iconState = iota // a shell at its prompt: nothing to show
	iconWorking                   // a spinning arc
	iconProgress                  // an arc filling up with the reported progress
	iconWaiting                   // an amber ring with a pulsing dot: needs you
	iconFailed                    // a red disc with "!"
	iconExited                    // a ring with an ✕
	iconDone                      // a filled check: finished, not yet seen
	iconDoneSeen                  // a faint check: finished, seen
)

// statusState is the icon of a pane or session's Info.
func statusState(i session.Info) iconState {
	switch i.Status {
	case session.Working:
		if i.Progress >= 0 {
			return iconProgress
		}
		return iconWorking
	case session.Waiting:
		return iconWaiting
	case session.Failed:
		return iconFailed
	case session.Exited:
		return iconExited
	}
	if i.Unread {
		return iconDone
	}
	if i.Message != "" || len(i.Records) > 0 {
		return iconDoneSeen
	}
	return iconNone
}

// statusIcon draws a status icon of size DIPs. Working and waiting icons
// move, unless the user asked for less motion.
func (a *appState) statusIcon(c *ui.Context, i session.Info, size float32) ui.Element {
	st := statusState(i)
	progress := float32(i.Progress) / 100
	unread := i.Unread
	still := a.still()
	e := ui.Box(c).Size(size, size).Shrink(0)
	a.popOnChange(e, int(statusFamily(st)))
	if st == iconProgress {
		progress = a.animate(e, "progress", progress, motionPanel)
	}
	return e.Draw(func(p *ui.Painter, r ui.Rect) {
		drawStatus(p, r, st, progress, unread, still)
	})
}

// statusFamily is what a status icon pops for changing: working with or
// without progress is working, and done seen is done.
func statusFamily(st iconState) iconState {
	switch st {
	case iconProgress:
		return iconWorking
	case iconDoneSeen:
		return iconDone
	}
	return st
}

func drawStatus(p *ui.Painter, r ui.Rect, st iconState, progress float32, unread, still bool) {
	if st == iconNone {
		return
	}
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	rad := min(r.W, r.H)/2 - 1
	const stroke = 1.7
	ring := func(c ui.Color) {
		p.StrokePath(new(ui.Path).Circle(cx, cy, rad), stroke, c)
	}
	arc := func(from, to float64, c ui.Color) {
		path := new(ui.Path)
		const steps = 24
		for k := 0; k <= steps; k++ {
			a := from + (to-from)*float64(k)/steps
			x, y := cx+rad*float32(math.Cos(a)), cy+rad*float32(math.Sin(a))
			if k == 0 {
				path.MoveTo(x, y)
			} else {
				path.LineTo(x, y)
			}
		}
		p.StrokePath(path, stroke, c)
	}
	disc := func(c ui.Color) { p.FillPath(new(ui.Path).Circle(cx, cy, rad+0.5), c) }
	switch st {
	case iconWorking:
		ring(pal.Text.Alpha(0.13))
		turn := 0.0
		if !still {
			turn = float64(p.Now().UnixMilli()%1000) / 1000
			p.AnimationFrame()
		}
		start := turn*2*math.Pi - math.Pi/2
		arc(start, start+0.3*2*math.Pi, pal.Text)
	case iconProgress:
		ring(pal.Text.Alpha(0.13))
		if progress > 0 {
			arc(-math.Pi/2, -math.Pi/2+float64(min(progress, 1))*2*math.Pi, pal.Accent)
		}
	case iconWaiting:
		ring(pal.Attention)
		alpha := float32(1)
		if !still {
			t := float64(p.Now().UnixMilli()%1600) / 1600
			alpha = 0.65 + 0.35*float32(math.Sin(t*2*math.Pi))
			p.After(33 * time.Millisecond)
		}
		p.FillPath(new(ui.Path).Circle(cx, cy, rad*0.42), pal.Attention.Alpha(alpha))
	case iconFailed:
		c := pal.Bad
		if !unread {
			c = c.Alpha(0.75)
		}
		disc(c)
		w := max(1.4, rad*0.28)
		p.Fill(ui.Rect{X: cx - w/2, Y: cy - rad*0.55, W: w, H: rad * 0.65}, pal.OnBad, w/2)
		p.Fill(ui.Rect{X: cx - w/2, Y: cy + rad*0.3, W: w, H: w}, pal.OnBad, w/2)
	case iconExited:
		c := pal.TextFaint
		if unread {
			c = pal.Bad
		}
		ring(c)
		d := rad * 0.38
		p.StrokePath(new(ui.Path).MoveTo(cx-d, cy-d).LineTo(cx+d, cy+d).MoveTo(cx+d, cy-d).LineTo(cx-d, cy+d), 1.5, c)
	case iconDone, iconDoneSeen:
		check := new(ui.Path).MoveTo(cx-rad*0.42, cy+rad*0.02).LineTo(cx-rad*0.1, cy+rad*0.34).LineTo(cx+rad*0.45, cy-rad*0.32)
		if st == iconDone {
			disc(pal.Done)
			p.StrokePath(check, 1.7, pal.OnDone)
		} else {
			p.StrokePath(check, 1.7, pal.TextFaint)
		}
	}
}
