package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// The terminal view: 1–3 panes in the mock's four layouts, each with a
// header strip, a floating action bar and colored output.

var termColorTable = map[int]ui.Color{
	tcGreen:   colGreen,
	tcLime:    colLime,
	tcPurple:  colPurple,
	tcYellow:  colYellow,
	tcCyan:    colCyan,
	tcBlue:    colBlue,
	tcOrange:  colOrange,
	tcRed:     colRed,
	tcDim:     synDim,
	tcMuted:   colMuted,
	tcWhite:   ui.Hex("#e6edf3"),
	tcLink:    synLink,
	tcDefault: colText2,
}

func (a *appState) terminalView(c *ui.Context) ui.Element {
	return ui.Column(c).Fill().Background(colBg2).Children(func() {
		switch a.layout {
		case layoutSingle:
			a.termPane(c, 0)
		case layoutVertical:
			ui.SplitPercent(c, &a.splitVPct, func() {
				a.termPane(c, 0)
			}, func() {
				a.termPane(c, 1)
			}).Fill()
		case layoutHorizontal:
			ui.SplitVerticalPercent(c, &a.splitHPct, func() {
				a.termPane(c, 0)
			}, func() {
				a.termPane(c, 1)
			}).Fill()
		default: // mixed
			ui.SplitPercent(c, &a.splitVPct, func() {
				a.termPane(c, 0)
			}, func() {
				ui.SplitVerticalPercent(c, &a.splitHPct, func() {
					a.termPane(c, 1)
				}, func() {
					a.termPane(c, 2)
				}).Fill()
			}).Fill()
		}
	})
}

func (a *appState) termPane(c *ui.Context, i int) ui.Element {
	p := &a.panes[i]
	// The mock's .subtle-grid under the pane's output.
	grid := ui.RGBA(255, 255, 255, 0.016)
	e := ui.Column(c).Fill().Background(colBg2).Draw(func(p *ui.Painter, r ui.Rect) {
		for x := r.X - 1; x < r.X+r.W; x += 28 {
			p.Line(x, r.Y, x, r.Y+r.H, 1, grid)
		}
		for y := r.Y - 1; y < r.Y+r.H; y += 28 {
			p.Line(r.X, y, r.X+r.W, y, 1, grid)
		}
	})
	if a.paneFocus == i {
		// The mock's active pane ring: an inset blue stroke over the edges.
		e.DrawOver(func(p *ui.Painter, r ui.Rect) {
			p.Stroke(ui.Rect{X: r.X + 0.5, Y: r.Y + 0.5, W: r.W - 1, H: r.H - 1}, ui.RGBA(90, 169, 255, 0.34), 0, 1)
		})
	}
	e.Children(func() {
		// Header strip: dot, title, shell · pid.
		ui.Row(c).Height(32).AlignItems(ui.Center).Padding(0, 12).Gap(8).BorderWidth(0, 0, 1, 0).BorderColor(colBorder).ClipX().Children(func() {
			kind := dotGreen
			if p.Dim {
				kind = dotYellow
			}
			dot(c, kind, 6)
			txt(c, p.Title, 10, colText2)
			meta := p.Shell
			if p.Pid != 0 {
				meta += " · pid " + itoa(p.Pid)
			}
			mutedTxt2(c, meta, 10)
		})

		// Output, with the floating action bar over its top right, showing
		// while the pane is hovered, as the mock's hover reveal.
		body := ui.Box(c).Grow(1).Clip()
		if body.Clicked() {
			a.paneFocus = i
		}
		hovered := body.Hovered()
		body.Children(func() {
			ui.Scroll(c).Fill().Children(func() {
				pad := ui.Column(c).Padding(16, 20).Gap(0)
				if p.Dim {
					pad = pad.Opacity(0.75)
				}
				pad.Children(func() {
					for _, line := range p.Lines {
						termLineView(c, line, p.Dim)
					}
					if p.Cursor {
						// The mock's final prompt with its cursor block.
						ui.Row(c).Margin(32, 0, 0, 0).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Text(c, "❯").Font(monoFont).FontSize(12).TextColor(colLime)
							ui.Box(c).Width(8).Height(15).Background(ui.Hex("#95d7ff"))
						})
					}
				})
			})
			if hovered {
				ui.Row(c).Absolute().Top(7).Right(8).Gap(2).Radius(6).Border(1, colBorder2).Background(colPaneBar).Padding(2, 4).Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.22)).Children(func() {
					if cmdButton(c, iconColumns2, 14, false).Tooltip("Split right").Clicked() {
						a.layout = layoutVertical
						c.Toast("Vertical terminal split")
					}
					if cmdButton(c, iconRows2, 14, false).Tooltip("Split down").Clicked() {
						a.layout = layoutHorizontal
						c.Toast("Horizontal terminal split")
					}
					if i == 0 {
						if cmdButton(c, iconRotate, 14, false).Tooltip("Restart").Clicked() {
							c.Toast("Restarted terminal")
						}
						if cmdButton(c, iconEllipsis, 14, false).Clicked() {
							c.Toast("More")
						}
					} else {
						if cmdButton(c, iconTrash, 14, false).Tooltip("Kill terminal").Clicked() {
							a.layout = layoutSingle
							c.Toast("Terminal closed")
						}
					}
				})
			}
		})

		// Pane footer: shell, cwd, dimensions.
		ui.Row(c).Height(28).AlignItems(ui.Center).Padding(0, 12).Gap(12).BorderWidth(1, 0, 0, 0).BorderColor(colBorder).Children(func() {
			mutedTxt2(c, p.Shell, 10)
			mutedTxt2(c, a.project().Path, 10)
			ui.Spacer(c)
			mutedTxt2(c, "128×34", 10)
		})
	})
	return e
}

func termLineView(c *ui.Context, line termLine, dim bool) ui.Element {
	spans := make([]ui.Span, 0, len(line.Spans))
	for _, s := range line.Spans {
		sp := ui.Span{Text: s.Text}
		if col, ok := termColorTable[s.Color]; ok {
			sp.Color = col
		}
		spans = append(spans, sp)
	}
	if len(spans) == 0 {
		spans = []ui.Span{{Text: " "}}
	}
	e := ui.RichText(c, spans...).Font(monoFont).FontSize(12).LineHeight(1.72)
	if line.Top > 0 {
		e = e.Margin(line.Top, 0, 0, 0)
	}
	return e
}

var _ = fmt.Sprint
