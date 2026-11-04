package main

import (
	"github.com/egoist/mygo/ui"
)

// Shared bits of the mock's design language: the .cmd buttons, .pill
// chips, status dots and section headers.

func dotColor(kind int) ui.Color {
	switch kind {
	case dotYellow:
		return pal.Warn
	case dotGray:
		return pal.TextFaint
	default:
		return pal.Ok
	}
}

// dot draws a status dot d DIPs wide.
func dot(c *ui.Context, kind int, d float32) ui.Element {
	col := dotColor(kind)
	return ui.Box(c).Width(d).Height(d).Radius(d / 2).Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(r, col, d/2)
	})
}

// iconBtn is the glyph size every inline command button uses. cmdButton
// pads by size/2, so a single value keeps glyphs and hit boxes identical
// across toolbars.
const iconBtn = 14.0

// cmdButton is the mock's .cmd button: a muted icon, optionally filled
// and white when active.
func (a *appState) cmdButton(c *ui.Context, icon *ui.SVG, size float32, active bool) ui.Element {
	// The mock's .cmd: transparent with a 1px transparent border, and on
	// hover #182029 with a #27313d border and a white icon.
	e := ui.ButtonBase(c).Padding(size/2).Radius(7).Shrink(0).Border(1, ui.Color{})
	hover := e.Hovered()
	if active || hover {
		e = e.Background(pal.Hover).Border(1, pal.BorderStrong)
	}
	a.press(a.fadeColors(e))
	e.Children(func() {
		i := ui.Icon(c, icon).Width(size).Height(size)
		ink := pal.TextMuted
		if active || hover {
			ink = pal.TextStrong
		}
		i.TextColor(a.animColor(i, "ink", ink, motionFast))
	})
	return e
}

// litButton is a cmdButton that says what it does by its color, as a
// process's Start in green and Stop in red: its glyph is in tone whenever
// it shows, and under the pointer it is a chip of that tone.
func (a *appState) litButton(c *ui.Context, icon *ui.SVG, size float32, tone ui.Color) ui.Element {
	e := ui.ButtonBase(c).Padding(size/2).Radius(7).Shrink(0).Border(1, ui.Color{})
	if e.Hovered() {
		e = e.Background(tone.Alpha(0.12)).Border(1, tone.Alpha(0.35))
	}
	a.press(a.fadeColors(e))
	e.Children(func() {
		ui.Icon(c, icon).Width(size).Height(size).TextColor(tone)
	})
	return e
}

// toneButton is a muted cmdButton that turns litButton under the pointer:
// a destructive button among a list's rows, quiet until aimed at.
func (a *appState) toneButton(c *ui.Context, icon *ui.SVG, size float32, tone ui.Color) ui.Element {
	e := ui.ButtonBase(c).Padding(size/2).Radius(7).Shrink(0).Border(1, ui.Color{})
	hover := e.Hovered()
	if hover {
		e = e.Background(tone.Alpha(0.12)).Border(1, tone.Alpha(0.35))
	}
	a.press(a.fadeColors(e))
	e.Children(func() {
		i := ui.Icon(c, icon).Width(size).Height(size)
		ink := pal.TextMuted
		if hover {
			ink = tone
		}
		i.TextColor(a.animColor(i, "ink", ink, motionFast))
	})
	return e
}

// pill is the mock's .pill chip: muted small text on a dark fill.
func pill(c *ui.Context, text string) ui.Element { return pillOn(c, text, false) }

// pillOn is pill on a list row: on the row the list shows chosen, a
// translucent chip of the accent's text color.
func pillOn(c *ui.Context, text string, selected bool) ui.Element {
	bg, fg := pal.Chip, pal.TextMuted
	if selected {
		// Inverted: the accent's text color as the chip, the accent as its
		// text, as readable as the row's title. A tint of the text color
		// left 9px type under 4.5:1 in light themes.
		bg, fg = pal.AccentText, pal.Accent
	}
	return ui.Row(c).Radius(9).Padding(1, 6).Background(bg).Shrink(0).Justify(ui.Center).Children(func() {
		ui.Text(c, text).FontSize(9).TextColor(fg).SingleLine()
	})
}

// pillShrink is pill in a row where it may have to give up width: the chip
// stays, but a long label ellipsizes instead of pushing its siblings out.
func pillShrink(c *ui.Context, text string) ui.Element {
	e := ui.Row(c).Radius(9).Padding(1, 6).Background(pal.Chip).
		Shrink(0).MaxWidthPercent(45).Justify(ui.Center)
	e.Children(func() {
		ui.Text(c, text).FontSize(9).TextColor(pal.TextMuted).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
	})
	e.Tooltip(text)
	return e
}

// keyChip is the key hint the palette and the shortcuts overlay show:
// a small bordered chip, as VS Code's keybinding labels.
func keyChip(c *ui.Context, label string) ui.Element {
	return keyChipOn(c, label, false)
}

// keyChipOn is keyChip on a list row, outlined in the accent's text color
// on the row the list shows chosen.
func keyChipOn(c *ui.Context, label string, selected bool) ui.Element {
	if selected {
		return ui.Row(c).Radius(4).Border(1, pal.AccentText.Alpha(0.5)).
			Padding(2, 6).Shrink(0).Children(func() {
			ui.Text(c, label).FontSize(9).TextColor(pal.AccentText).SingleLine()
		})
	}
	return ui.Row(c).Radius(4).Border(1, pal.KbdBorder).Background(pal.Kbd).
		Padding(2, 6).Shrink(0).Children(func() {
		ui.Text(c, label).FontSize(9).TextColor(pal.TextMuted).SingleLine()
	})
}

// ink is the color of what a list row shows: on the row the list shows
// chosen, which it fills with the accent, the accent's text color, dimmer
// for what is secondary (faint). The row's own colors would not read on
// the accent.
func ink(selected bool, col ui.Color, faint bool) ui.Color {
	switch {
	case !selected:
		return col
	case faint:
		return pal.AccentText.Alpha(0.75)
	}
	return pal.AccentText
}

// sectionLabel is the uppercase muted label above inspector sections.
func sectionLabel(c *ui.Context, text string) ui.Element {
	return ui.Text(c, text).FontSize(10).TextColor(pal.TextMuted).LetterSpacing(1.2).SingleLine().Shrink(0)
}

// countBadge is the small rounded count next to section labels.
func countBadge(c *ui.Context, n int) ui.Element {
	return countBadgeText(c, itoa(n))
}

// countBadgeText is countBadge with arbitrary text, for a count that is
// not known yet ("—").
func countBadgeText(c *ui.Context, s string) ui.Element {
	return ui.Box(c).Radius(9).Padding(1, 6).Background(pal.Chip).Shrink(0).Children(func() {
		ui.Text(c, s).FontSize(9).TextColor(pal.TextMuted).SingleLine()
	})
}

// txt is single-line, non-shrinking text in the given size and color.
func txt(c *ui.Context, s string, size float32, col ui.Color) ui.Element {
	return ui.Text(c, s).FontSize(size).TextColor(col).SingleLine().Shrink(0)
}

// mutedTxt2 is txt in the mock's muted color.
func mutedTxt2(c *ui.Context, s string, size float32) ui.Element {
	return txt(c, s, size, pal.TextMuted)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// monoFont is the mock's monospace family.
const monoFont = "JetBrains Mono, monospace"

// statusColor colors a git status letter as the mock does.
func statusColor(status string) ui.Color {
	switch status {
	case "A", "U", "?":
		return pal.Ok
	case "M":
		return pal.Warn
	case "D", "!": // deleted, or in conflict
		return pal.Bad
	case "R", "C":
		return pal.Accent
	}
	return pal.TextMuted
}
