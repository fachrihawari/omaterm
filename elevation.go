// Adapted from Kero (github.com/egoist/kero, MIT,
// docs/third_party/kero-LICENSE), elevation.go.

package main

import "github.com/egoist/mygo/ui"

// Surfaces stand out by their color and an edge. In a light theme the edge
// is a small soft shadow and no line; in a dark one, where shadows hardly
// show, a hairline a little lighter than the surface over a dark shadow,
// as a lit edge.

var black = ui.Color{A: 255}

// raised lifts a surface off what is under it a little: the pane cards,
// the selected row, the active tab, buttons.
func raised(e ui.Element) ui.Element {
	if pal.Dark {
		return darkShadow(litEdge(e))
	}
	return e.Shadow(0, 1, 2, 0, pal.CardShadow).Shadow(0, 1, 6, 0, pal.CardShadow2)
}

// cardEdge lifts a pane's card: raised, and in a light theme edged with a
// hairline as the dark one is with its lit edge. Without it a light card
// sat a pixel higher than a dark one and a focused pane's ring, a border
// color, had no border to color.
func cardEdge(e ui.Element) ui.Element {
	raised(e)
	if !pal.Dark {
		e.Border(1, black.Alpha(0.08))
	}
	return e
}

// chosen lifts the chosen row of a list a hair: an even ring and a faint
// shadow close under it; in the dark, as raised.
func chosen(e ui.Element) ui.Element {
	if pal.Dark {
		return raised(e)
	}
	return e.Shadow(0, 0, 0, 0.5, black.Alpha(0.06)).Shadow(0, 0.5, 2, 0, black.Alpha(0.05))
}

// floating lifts a surface above the window: the palette, dialogs, menus.
func floating(e ui.Element) ui.Element {
	if pal.Dark {
		return darkShadow(litEdge(e)).Shadow(0, 16, 40, 0, pal.FloatShadow)
	}
	return e.Shadow(0, 1, 3, 0, pal.CardShadow).Shadow(0, 16, 40, 0, pal.FloatShadow)
}

// litEdge draws a dark surface's edge as lit from above: a 1px border in
// the text's color, 18% at the top fading to 6% at the bottom.
func litEdge(e ui.Element) ui.Element {
	return e.Border(1, pal.Text.Alpha(0.12)).BorderLinearGradient(ui.LinearGradient{
		Angle: 180, From: pal.Text.Alpha(0.18), To: pal.Text.Alpha(0.06),
	})
}

// darkShadow is the surface shadow in the dark: three tight layers.
func darkShadow(e ui.Element) ui.Element {
	return e.Shadow(0, 1, 1, 0, black.Alpha(0.2)).Shadow(0, 2, 2, -1, black.Alpha(0.2)).Shadow(0, 4, 6, -3, black.Alpha(0.24))
}

// face styles a control's face: its fill, and in the dark its lit edge.
func face(e ui.Element, bg ui.Color) ui.Element {
	e.Background(bg)
	if pal.Dark {
		return darkShadow(litEdge(e))
	}
	return e.Shadow(0, 0.5, 1.5, 0, black.Alpha(0.13)).Shadow(0, 0, 0, 0.5, black.Alpha(0.05))
}
