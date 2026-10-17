package main

import (
	"github.com/egoist/mygo/ui"
)

// Projects sidebar, the mock's #projectsSidebar.

func (a *appState) projectsSidebar(c *ui.Context) ui.Element {
	return ui.Column(c).FillHeight().Background(colPanel).Children(func() {
		// Header: hide button, PROJECTS label, add buttons.
		ui.Row(c).Height(40).AlignItems(ui.Center).Padding(0, 8).Gap(2).Children(func() {
			if cmdButton(c, iconPanelLeftClose, 16, true).Clicked() {
				a.sidebarVisible = false
			}
			ui.Text(c, "PROJECTS").FontSize(10).TextColor(colMuted).LetterSpacing(1.4).Padding(0, 6)
			ui.Spacer(c)
			if cmdButton(c, iconPlus, 14, false).Clicked() {
				c.Toast("Add project")
			}
			if cmdButton(c, iconFolderSearch, 16, false).Clicked() {
				c.Toast("Open folder")
			}
		})

		// Project list.
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(4, 8).Gap(4).Children(func() {
				for i := range a.projects {
					a.projectRow(c, i)
				}
			})
		})

		// Footer: Open Project button.
		ui.Box(c).Padding(8).BorderWidth(1, 0, 0, 0).BorderColor(colBorder).Children(func() {
			ui.ButtonBase(c).FillWidth().Height(32).Radius(6).Border(1, colBorder2).Children(func() {
				ui.Row(c).Fill().Center().Gap(8).Children(func() {
					ui.Icon(c, iconFolderPlus).Width(14).Height(14).TextColor(colText2)
					ui.Text(c, "Open Project").FontSize(11).TextColor(colText2)
					ui.Spacer(c)
					ui.Text(c, "⌘O").FontSize(9).TextColor(colMuted).Padding(0, 8)
				})
			})
		})
	})
}

func (a *appState) projectRow(c *ui.Context, i int) ui.Element {
	p := &a.projects[i]
	selected := i == a.active
	e := ui.ButtonBase(c).Key(i).FillWidth().Radius(6).Padding(7, 10).Border(1, ui.Color{})
	if selected {
		e = e.Background(colHover).Border(1, colBorder2)
	}
	e.Children(func() {
		ui.Column(c).Gap(2).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				kind := dotGray
				if p.Active {
					kind = dotGreen
				}
				dot(c, kind, 8)
				ui.Text(c, p.Name).FontSize(12).TextColor(colText)
				ui.Spacer(c)
				ui.Text(c, p.Branch).FontSize(9).TextColor(colMuted)
			})
			ui.Text(c, p.Path).FontSize(9).TextColor(colMuted2).Padding(0, 16)
		})
	})
	if e.Clicked() {
		a.setProject(i)
		c.Toast("Switched to " + p.Name)
	}
	return e
}
