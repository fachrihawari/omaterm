package main

import (
	"github.com/egoist/mygo/ui"
)

// Shared bits of the mock's design language: the .cmd buttons, .pill
// chips, status dots and section headers.

func dotColor(kind int) ui.Color {
	switch kind {
	case dotYellow:
		return colYellow
	case dotGray:
		return colMuted2
	default:
		return colGreen
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
func cmdButton(c *ui.Context, icon *ui.SVG, size float32, active bool) ui.Element {
	// The mock's .cmd: transparent with a 1px transparent border, and on
	// hover #182029 with a #27313d border and a white icon.
	e := ui.ButtonBase(c).Padding(size/2).Radius(7).Shrink(0).Border(1, ui.Color{})
	hover := e.Hovered()
	switch {
	case active:
		e = e.Background(colHover).Border(1, colBorder2)
	case hover:
		e = e.Background(ui.Hex("#182029")).Border(1, ui.Hex("#27313d"))
	}
	e.Children(func() {
		i := ui.Icon(c, icon).Width(size).Height(size)
		if active || hover {
			i.TextColor(ui.Hex("#ffffff"))
		} else {
			i.TextColor(colMuted)
		}
	})
	return e
}

// pill is the mock's .pill chip: muted small text on a dark fill.
func pill(c *ui.Context, text string) ui.Element {
	return ui.Row(c).Radius(9).Padding(1, 6).Background(ui.Hex("#222b36")).Shrink(0).Justify(ui.Center).Children(func() {
		ui.Text(c, text).FontSize(9).TextColor(colMuted).SingleLine()
	})
}

// pillShrink is pill in a row where it may have to give up width: the chip
// stays, but a long label ellipsizes instead of pushing its siblings out.
func pillShrink(c *ui.Context, text string) ui.Element {
	e := ui.Row(c).Radius(9).Padding(1, 6).Background(ui.Hex("#222b36")).
		Shrink(0).MaxWidthPercent(45).Justify(ui.Center)
	e.Children(func() {
		ui.Text(c, text).FontSize(9).TextColor(colMuted).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
	})
	e.Tooltip(text)
	return e
}

// sectionLabel is the uppercase muted label above inspector sections.
func sectionLabel(c *ui.Context, text string) ui.Element {
	return ui.Text(c, text).FontSize(10).TextColor(colMuted).LetterSpacing(1.2).SingleLine().Shrink(0)
}

// countBadge is the small rounded count next to section labels.
func countBadge(c *ui.Context, n int) ui.Element {
	return countBadgeText(c, itoa(n))
}

// countBadgeText is countBadge with arbitrary text, for a count that is
// not known yet ("—").
func countBadgeText(c *ui.Context, s string) ui.Element {
	return ui.Box(c).Radius(9).Padding(1, 6).Background(ui.Hex("#222b36")).Shrink(0).Children(func() {
		ui.Text(c, s).FontSize(9).TextColor(colMuted).SingleLine()
	})
}

// txt is single-line, non-shrinking text in the given size and color.
func txt(c *ui.Context, s string, size float32, col ui.Color) ui.Element {
	return ui.Text(c, s).FontSize(size).TextColor(col).SingleLine().Shrink(0)
}

// truncTxt truncates s to at most max chars, adding …, then renders it
// like txt. Use where the mock truncates with CSS text-overflow.
func truncTxt(c *ui.Context, s string, size float32, col ui.Color, max int) ui.Element {
	if len(s) > max && max > 1 {
		s = s[:max-1] + "…"
	}
	return txt(c, s, size, col)
}

// mutedTxt2 is txt in the mock's muted color.
func mutedTxt2(c *ui.Context, s string, size float32) ui.Element {
	return txt(c, s, size, colMuted)
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
		return colGreen
	case "M":
		return colYellow
	case "D":
		return colRed
	case "R", "C":
		return colBlue
	}
	return colMuted
}
