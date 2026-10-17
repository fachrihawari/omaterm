package main

import (
	"github.com/egoist/mygo/ui"
)

// The bottom status bar.

func (a *appState) statusBar(c *ui.Context) ui.Element {
	return ui.Row(c).Height(24).AlignItems(ui.Center).Background(colStatusBg).BorderWidth(1, 0, 0, 0).BorderColor(colBorder).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Padding(0, 10).Children(func() {
			ui.Icon(c, iconGitBranch).Width(12).Height(12).TextColor(colMuted)
			ui.Text(c, a.project().Branch).FontSize(10).TextColor(colMuted)
		})
		ui.Row(c).AlignItems(ui.Center).Gap(6).Padding(0, 10).Children(func() {
			ui.Text(c, "●").FontSize(8).TextColor(colGreen)
			ui.Text(c, itoa(len(a.processes))+" processes").FontSize(10).TextColor(colMuted)
		})
		ui.Row(c).AlignItems(ui.Center).Gap(6).Padding(0, 10).Children(func() {
			ui.Icon(c, iconRadio).Width(12).Height(12).TextColor(colMuted)
			ui.Text(c, itoa(len(a.ports))+" ports").FontSize(10).TextColor(colMuted)
		})

		ui.Spacer(c)

		ui.Text(c, "CPU 3%").FontSize(10).TextColor(colMuted).Padding(0, 10)
		ui.Text(c, "MEM 1.3 GB").FontSize(10).TextColor(colMuted).Padding(0, 10)
		ui.Text(c, "UTF-8").FontSize(10).TextColor(colMuted).Padding(0, 10)
		ui.Icon(c, iconBell).Width(12).Height(12).TextColor(colMuted).Margin(0, 10)
	})
}
