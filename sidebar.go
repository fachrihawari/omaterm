package main

import (
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// Projects sidebar, the mock's #projectsSidebar.

func (a *appState) projectsSidebar(c *ui.Context) ui.Element {
	return ui.Column(c).FillHeight().Background(pal.Sidebar).Children(func() {
		// Header: hide button, PROJECTS label, open-project button.
		ui.Row(c).Height(42).AlignItems(ui.Center).Padding(0, 8).Gap(2).BorderWidth(0, 0, 1, 0).BorderColor(pal.Border).Children(func() {
			if cmdButton(c, iconPanelLeftClose, 16, true).Tooltip("Hide Projects").Clicked() {
				a.sidebarVisible = false
			}
			ui.Text(c, "PROJECTS").FontSize(10).TextColor(pal.TextMuted).LetterSpacing(1.4).Padding(0, 6).SingleLine().Shrink(0)
			ui.Spacer(c)
		})

		// Project list.
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(8).Gap(4).Children(func() {
				if len(a.projects) == 0 {
					ui.Box(c).Padding(24).Children(func() {
						ui.Column(c).AlignItems(ui.Center).Gap(8).Children(func() {
							ui.Box(c).Width(40).Height(40).Radius(10).Border(1, pal.BorderStrong).Background(pal.Raised).Children(func() {
								ui.Column(c).Fill().Center().Children(func() {
									ui.Icon(c, iconFolderPlus).Width(20).Height(20).TextColor(pal.Accent)
								})
							})
							ui.Text(c, "No projects yet").FontSize(12).TextColor(pal.Text).Bold()
							ui.Text(c, "Open a folder to get started").FontSize(10).TextColor(pal.TextMuted)
							b := ui.ButtonBase(c).Radius(6).Border(1, pal.BorderStrong).Padding(6, 14).Background(pal.Raised).Margin(4, 0, 0, 0)
							if b.Hovered() {
								b.Background(pal.Hover)
							}
							b.Children(func() {
								ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
									ui.Icon(c, iconFolderPlus).Width(12).Height(12).TextColor(pal.TextSoft)
									ui.Text(c, "Open Project").FontSize(11).TextColor(pal.TextSoft)
								})
							})
							if b.Clicked() {
								a.openProjectDialog()
							}
						})
					})
				}
				for i := range a.projects {
					a.projectRow(c, i)
				}
			})
		})

		// Footer: Open Project button with its key chip.
		ui.Box(c).Padding(8).BorderWidth(1, 0, 0, 0).BorderColor(pal.Border).Children(func() {
			b := ui.Row(c).FillWidth().Focusable().Cursor(ui.CursorPointer).Height(32).Radius(6).Border(1, pal.BorderStrong)
			if b.Hovered() {
				b.Background(pal.Hover)
			}
			b.Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Padding(0, 8).Children(func() {
					ui.Icon(c, iconFolderPlus).Width(14).Height(14).TextColor(pal.TextSoft)
					ui.Text(c, "Open Project").FontSize(11).TextColor(pal.TextSoft).SingleLine().Shrink(0)
					ui.Spacer(c)
					ui.Row(c).Radius(4).Border(1, pal.KbdBorder).Background(pal.Kbd).Padding(2, 6).Shrink(0).Children(func() {
						ui.Text(c, keyLabels("app.openProject")).FontSize(9).TextColor(pal.TextMuted).SingleLine()
					})
				})
			})
			if b.Clicked() {
				a.openProjectDialog()
			}
		})
	})
}

func (a *appState) projectRow(c *ui.Context, i int) ui.Element {
	p := &a.projects[i]
	selected := i == a.active
	// A plain row, left-aligned; ButtonBase would center its content.
	e := ui.Row(c).Key(i).FillWidth().Justify(ui.Start).Focusable().Cursor(ui.CursorPointer).Radius(8).Padding(10, 10).Border(1, ui.Color{})
	if selected {
		e = e.Background(pal.Hover).Border(1, pal.BorderStrong)
	} else if e.Hovered() {
		e = e.Background(pal.Hover)
	}
	e.Children(func() {
		ui.Column(c).FillWidth().Gap(4).Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				kind := dotGray
				if p.Active {
					kind = dotGreen
				}
				d := dot(c, kind, 8)
				if p.Active && selected {
					d.Shadow(0, 0, 10, 0, pal.Ok.Alpha(0.24))
				}
				ui.Text(c, p.Name).FontSize(12).TextColor(pal.Text).SingleLine()
			})
			ui.Text(c, p.Path).FontSize(9).TextColor(pal.TextMuted).Padding(0, 16).SingleLine()
		})
	})
	if e.Clicked() {
		a.setProject(i)
		c.Toast("Switched to " + p.Name)
	}
	e.ContextMenu(func(m *ui.Menu) {
		item := m.Item("Remove project")
		if item.Chosen() {
			a.pendingRemoveProjectIdx = i
			a.pendingRemoveProjectName = p.Name
			open := true
			a.removeProjectConfirmOpen = &open
		}
	})
	return e
}

// openProjectDialog shows a native folder picker and adds the chosen folder
// as a project, or switches to it when already listed.
func (a *appState) openProjectDialog() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:    a.win,
			Title:     "Open Project",
			Directory: true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		dir := paths[0]
		a.win.Update(func() {
			// Switch to it when already in the list.
			for i, p := range a.projects {
				if p.Path == dir {
					a.setProject(i)
					return
				}
			}
			a.projects = append(a.projects, project{
				Name: filepath.Base(dir),
				Path: dir,
				Tabs: []string{"dev"},
			})
			a.setProject(len(a.projects) - 1)
			a.saveProjects()
		})
	}()
}
