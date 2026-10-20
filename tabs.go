package main

import (
	"github.com/egoist/mygo/ui"
)

// The top tab bar with terminal/editor/diff tabs and the window-level
// controls on the right.

func (a *appState) tabBar(c *ui.Context) ui.Element {
	return ui.Row(c).Height(42).AlignItems(ui.End).Background(colBg).BorderWidth(0, 0, 1, 0).BorderColor(colBorder).Children(func() {
		// Show-projects button, only while the sidebar is hidden.
		if !a.sidebarVisible {
			if cmdButton(c, iconPanelLeft, 16, false).MarginStart(8).
				Tooltip("Show Projects").Label("Show Projects").Clicked() {
				a.sidebarVisible = true
			}
		}

		ui.Row(c).Grow(1).AlignItems(ui.End).Padding(0, 4).Gap(4).ClipX().Children(func() {
			for i := 0; i < len(a.tabs); i++ {
				if i >= len(a.tabs) {
					break
				}
				a.topTab(c, i)
			}
			if cmdButton(c, iconPlus, 16, false).Margin(0, 2).Clicked() {
				a.newTerminalTab()
				c.Toast("New terminal")
			}
		})

		ui.Row(c).AlignItems(ui.Center).Gap(2).Padding(0, 8).Children(func() {
			if cmdButton(c, iconSearch, 16, false).Tooltip("Search in files").Clicked() {
				a.openOverlay(overlayGlobalSearch)
			}
			if cmdButton(c, iconPanelRight, 16, a.inspectorVisible).Clicked() {
				a.inspectorVisible = !a.inspectorVisible
			}
		})
	})
}

func (a *appState) topTab(c *ui.Context, i int) ui.Element {
	if i < 0 || i >= len(a.tabs) {
		return ui.Box(c)
	}
	t := &a.tabs[i]
	active := i == a.activeTab
	// The mock's .top-tab: rounded top corners, a blue top edge while
	// active, sitting flush with the content below.
	e := ui.ButtonBase(c).Key(i).Height(36).Radius(6, 6, 0, 0).Padding(0, 12).Gap(6)
	if active {
		e = e.Background(ui.Hex("#141a21")).DrawOver(func(p *ui.Painter, r ui.Rect) {
			p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W, H: 1}, ui.RGBA(90, 169, 255, 0.9), 0)
		})
	} else if e.Hovered() {
		e = e.Background(colPanel2)
	}
	e.Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			switch t.Kind {
			case tabTerminal:
				dot(c, t.Dot, 8)
			case tabEditor:
				ui.Icon(c, iconFileCode).Width(14).Height(14).TextColor(synTS)
			case tabDiff:
				ui.Icon(c, iconGitCompare).Width(14).Height(14).TextColor(colGreen)
			case tabGraph:
				ui.Icon(c, iconGitGraph).Width(14).Height(14).TextColor(colPurple)
			}
			labelCol := colText2
			if active {
				labelCol = ui.Hex("#ffffff")
			}
			ui.Text(c, t.Label).FontSize(12).TextColor(labelCol).SingleLine().Shrink(0)
			if t.Detail != "" {
				ui.Spacer(c)
				ui.Text(c, t.Detail).FontSize(10).TextColor(colMuted).SingleLine().Shrink(0)
			}
			if t.Dirty {
				ui.Text(c, "M").FontSize(10).TextColor(colYellow).SingleLine().Shrink(0)
			}
			if t.Kind == tabEditor && t.Path != "" {
				if doc := a.editors[t.Path]; doc != nil && doc.State != nil && doc.State.Buffer.Modified() {
					ui.Text(c, "●").FontSize(10).TextColor(colYellow).SingleLine().Shrink(0)
				}
			}
			if len(a.tabs) > 1 {
				ui.Spacer(c)
				// The close button is always laid out, so tabs keep their
				// width, but it only shows on the active or hovered tab.
				showClose := active || e.Hovered()
				opacity := float32(0)
				if showClose {
					opacity = 1
				}
				x := ui.ButtonBase(c).Padding(2).Radius(3).Shrink(0).
					Opacity(opacity).Disabled(!showClose).Children(func() {
					ui.Icon(c, iconX).Width(14).Height(14).TextColor(colMuted)
				})
				if x.Clicked() {
					a.closeTab(i)
				}
			}
		})
	})
	if e.Clicked() {
		a.activeTab = i
		a.activeView = t.Kind
		if t.Kind == tabTerminal && t.Split != nil {
			ids := t.Split.termIDs()
			if len(ids) > 0 {
				a.activePaneTermID = ids[0]
			}
		}
	}
	return e
}

func (a *appState) closeTab(i int) {
	if i < 0 || i >= len(a.tabs) {
		return
	}
	t := a.tabs[i]
	if t.Kind == tabEditor && t.Path != "" {
		delete(a.editors, t.Path)
	}
	if t.Kind == tabTerminal && t.Split != nil {
		a.termPanesMu.Lock()
		for _, id := range t.Split.termIDs() {
			if tp := a.termPanes[id]; tp != nil {
				tp.Close()
			}
			delete(a.termPanes, id)
		}
		a.termPanesMu.Unlock()
	}
	a.tabs = append(a.tabs[:i], a.tabs[i+1:]...)
	if a.activeTab >= len(a.tabs) {
		a.activeTab = len(a.tabs) - 1
	}
	if a.activeTab >= 0 {
		a.activeView = a.tabs[a.activeTab].Kind
	}
}

// closeActiveTab closes the active tab, asking for confirmation when an
// editor has unsaved changes.
func (a *appState) closeActiveTab(c *ui.Context) {
	if a.activeTab < 0 || a.activeTab >= len(a.tabs) {
		return
	}
	t := a.tabs[a.activeTab]
	if t.Kind == tabEditor && t.Path != "" {
		if doc := a.editors[t.Path]; doc != nil && doc.State != nil && doc.State.Buffer.Modified() {
			open := true
			a.pendingCloseTab = a.activeTab
			a.closeTabConfirmOpen = &open
			return
		}
	}
	a.closeTab(a.activeTab)
}

// newTerminalTab creates a tab backed by a real terminal pane.
func (a *appState) newTerminalTab() {
	a.newTerminalTabReal()
}
