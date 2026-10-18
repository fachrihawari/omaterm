package main

import (
	"github.com/egoist/mygo/ui"
)

// The bottom status bar. Each segment is a full-height hoverable cell, as
// the mock's footer buttons.

func (a *appState) statusBar(c *ui.Context) ui.Element {
	return ui.Row(c).Height(24).AlignItems(ui.Center).Background(colStatusBg).BorderWidth(1, 0, 0, 0).BorderColor(colBorder).Children(func() {
		statusSegment(c, func() {
			ui.Icon(c, iconGitBranch).Width(12).Height(12).TextColor(colMuted)
			branch := ""
			if p := a.project(); p != nil {
				branch = p.Branch
			}
			ui.Text(c, branch).FontSize(10).TextColor(colMuted).SingleLine().Shrink(0)
		})
		statusSegment(c, func() {
			ui.Text(c, "●").FontSize(8).TextColor(colGreen)
			ui.Text(c, itoa(len(a.processes))+" processes").FontSize(10).TextColor(colMuted).SingleLine().Shrink(0)
		})
		statusSegment(c, func() {
			ui.Icon(c, iconRadio).Width(12).Height(12).TextColor(colMuted)
			ui.Text(c, itoa(len(a.ports))+" ports").FontSize(10).TextColor(colMuted).SingleLine().Shrink(0)
		})

		ui.Spacer(c)

		statusSegment(c, func() {
			ui.Text(c, "CPU 3%").FontSize(10).TextColor(colMuted).SingleLine().Shrink(0)
		})
		statusSegment(c, func() {
			ui.Text(c, "MEM 1.3 GB").FontSize(10).TextColor(colMuted).SingleLine().Shrink(0)
		})
		statusSegment(c, func() {
			ui.Text(c, "UTF-8").FontSize(10).TextColor(colMuted).SingleLine().Shrink(0)
		})
		statusSegment(c, func() {
			ui.Icon(c, iconBell).Width(12).Height(12).TextColor(colMuted)
		})
	})
}

// statusSegment is one full-height, hoverable status bar cell.
func statusSegment(c *ui.Context, children func()) ui.Element {
	e := ui.ButtonBase(c).FillHeight().Padding(0, 10)
	if e.Hovered() {
		e.Background(colInput)
	}
	e.Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(children)
	})
	return e
}
