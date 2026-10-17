package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// The terminal view: 1–3 panes in the mock's four layouts, each with a
// header strip, a floating action bar and colored output.

var termColorTable = map[int]ui.Color{
	tcGreen:   colGreen,
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
	narrow := a.layout != layoutSingle
	e := ui.Column(c).Fill().Background(colBg2)
	e.Children(func() {
		// Header strip.
		ui.Row(c).Height(28).AlignItems(ui.Center).Padding(0, 10).Gap(6).BorderWidth(0, 0, 1, 0).BorderColor(colBorder).ClipX().Children(func() {
			kind := dotGreen
			if p.Dim {
				kind = dotYellow
			}
			dot(c, kind, 6)
			mutedTxt2(c, p.Title, 10)
			if !narrow {
				txt(c, p.Shell, 9, colMuted2)
				if p.Pid != 0 {
					txt(c, "· pid "+itoa(p.Pid), 9, colMuted2)
				}
			}
		})

		// Output, with the floating action bar over its top right.
		ui.Box(c).Grow(1).Clip().Children(func() {
			ui.Scroll(c).Fill().Children(func() {
				pad := ui.Column(c).Padding(12, 20).Gap(0)
				if p.Dim {
					pad = pad.Opacity(0.75)
				}
				pad.Children(func() {
					for _, line := range p.Lines {
						termLineView(c, line, p.Dim)
					}
				})
			})
			ui.Row(c).Absolute().Top(6).Right(8).Gap(0).Radius(6).Border(1, colBorder2).Background(colPaneBar).Padding(1, 2).Children(func() {
				if cmdButton(c, iconColumns2, 13, false).Tooltip("Split right").Clicked() {
					a.layout = layoutVertical
					c.Toast("Vertical terminal split")
				}
				if cmdButton(c, iconRows2, 13, false).Tooltip("Split down").Clicked() {
					a.layout = layoutHorizontal
					c.Toast("Horizontal terminal split")
				}
				if i == 0 {
					if cmdButton(c, iconRotate, 13, false).Tooltip("Restart").Clicked() {
						c.Toast("Restarted terminal")
					}
					if cmdButton(c, iconEllipsis, 13, false).Clicked() {
						c.Toast("More")
					}
				} else {
					if cmdButton(c, iconTrash, 13, false).Tooltip("Kill terminal").Clicked() {
						a.layout = layoutSingle
						c.Toast("Terminal closed")
					}
				}
			})
		})

		// Pane footer with cwd + size.
		ui.Row(c).Height(26).AlignItems(ui.Center).Padding(0, 12).Gap(6).BorderWidth(1, 0, 0, 0).BorderColor(colBorder).Children(func() {
			txt(c, a.project().Path, 9, colMuted2)
			ui.Spacer(c)
			txt(c, "128×34", 9, colMuted2)
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
	return ui.RichText(c, spans...).Font("JetBrains Mono").FontSize(12).LineHeight(1.72)
}

var _ = fmt.Sprint
