package main

import (
	"github.com/egoist/mygo/ui"
)

// Projects sidebar, the mock's #projectsSidebar.

func (a *appState) projectsSidebar(c *ui.Context) ui.Element {
	return ui.Column(c).FillHeight().Background(colPanel).Children(func() {
		// Header: hide button, PROJECTS label, open-project button.
		ui.Row(c).Height(42).AlignItems(ui.Center).Padding(0, 8).Gap(2).BorderWidth(0, 0, 1, 0).BorderColor(colBorder).Children(func() {
			if cmdButton(c, iconPanelLeftClose, 16, true).Tooltip("Hide Projects").Clicked() {
				a.sidebarVisible = false
			}
			ui.Text(c, "PROJECTS").FontSize(10).TextColor(colMuted).LetterSpacing(1.4).Padding(0, 6).SingleLine().Shrink(0)
			ui.Spacer(c)
			if cmdButton(c, iconFolderPlus, 14, false).Tooltip("Open project").Clicked() {
				c.Toast("Open project")
			}
		})

		// Project list.
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(8).Gap(4).Children(func() {
				for i := range a.projects {
					a.projectRow(c, i)
				}
			})
		})

		// Footer: Open Project button with its ⌘O key chip.
		ui.Box(c).Padding(8).BorderWidth(1, 0, 0, 0).BorderColor(colBorder).Children(func() {
			b := ui.ButtonBase(c).FillWidth().Height(32).Radius(6).Border(1, colBorder2)
			if b.Hovered() {
				b.Background(colHover)
			}
			b.Children(func() {
				ui.Row(c).Fill().Center().Gap(8).Children(func() {
					ui.Icon(c, iconFolderPlus).Width(14).Height(14).TextColor(colText2)
					ui.Text(c, "Open Project").FontSize(11).TextColor(colText2).SingleLine().Shrink(0)
					ui.Spacer(c)
					ui.Row(c).Radius(4).Border(1, ui.Hex("#313b48")).Background(ui.Hex("#161b22")).Padding(2, 6).Shrink(0).Children(func() {
						ui.Text(c, "⌘O").FontSize(9).TextColor(colMuted).SingleLine()
					})
				})
			})
		})
	})
}

func (a *appState) projectRow(c *ui.Context, i int) ui.Element {
	p := &a.projects[i]
	selected := i == a.active
	e := ui.ButtonBase(c).Key(i).FillWidth().Radius(8).Padding(10, 10).Border(1, ui.Color{})
	if selected {
		e = e.Background(colHover).Border(1, colBorder2)
	} else if e.Hovered() {
		e = e.Background(ui.Hex("#171d25"))
	}
	e.Children(func() {
		ui.Column(c).Gap(4).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				kind := dotGray
				if p.Active {
					kind = dotGreen
				}
				d := dot(c, kind, 8)
				if p.Active && selected {
					d.Shadow(0, 0, 10, 0, ui.RGBA(99, 213, 141, 0.24))
				}
				ui.Text(c, p.Name).FontSize(12).TextColor(colText).SingleLine()
				ui.Spacer(c)
				ui.Text(c, p.Branch).FontSize(10).TextColor(colMuted).SingleLine().Shrink(0)
			})
			ui.Text(c, p.Path).FontSize(9).TextColor(colMuted).Padding(0, 16).SingleLine()
		})
	})
	if e.Clicked() {
		a.setProject(i)
		c.Toast("Switched to " + p.Name)
	}
	return e
}
