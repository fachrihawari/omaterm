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
			if cmdButton(c, iconKeyboard, 16, false).
				Tooltip("Keyboard Shortcuts (" + keyLabels("help.keyboardShortcuts") + ")").
				Label("Keyboard Shortcuts").Clicked() {
				a.openOverlay(overlayShortcuts)
			}
			if cmdButton(c, iconSearch, 16, false).
				Tooltip("Search in files (" + keyLabels("search.global") + ")").Clicked() {
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
				if isImagePath(t.Path) {
					ui.Icon(c, iconImage).Width(14).Height(14).TextColor(colGreen)
				} else {
					ui.Icon(c, iconFileCode).Width(14).Height(14).TextColor(synTS)
				}
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
		a.activateTab(i)
	}
	return e
}

// activateTab makes tab i the active one and shows it.
func (a *appState) activateTab(i int) {
	if i < 0 || i >= len(a.tabs) {
		return
	}
	t := a.tabs[i]
	a.activeTab = i
	a.activeView = t.Kind
	if t.Kind == tabTerminal && t.Split != nil {
		if ids := t.Split.termIDs(); len(ids) > 0 {
			a.activePaneTermID = ids[0]
		}
	}
}

// activateKind shows kind: the active tab when it already is of that kind,
// else the first tab of it, else just the view, which then says there is
// nothing to show. Ctrl+2 must not jump from the second editor back to
// the first.
func (a *appState) activateKind(kind topTabKind) {
	if a.activeTab >= 0 && a.activeTab < len(a.tabs) && a.tabs[a.activeTab].Kind == kind {
		a.activeView = kind
		return
	}
	for i, t := range a.tabs {
		if t.Kind == kind {
			a.activateTab(i)
			return
		}
	}
	a.activeView = kind
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
	wasActive := a.activeTab == i
	if i < a.activeTab {
		a.activeTab--
	}
	a.tabs = append(a.tabs[:i], a.tabs[i+1:]...)
	if a.activeTab >= len(a.tabs) {
		a.activeTab = len(a.tabs) - 1
	}
	if a.activeTab >= 0 {
		if wasActive {
			// The tab that took its place becomes the shown view, as
			// clicking it would.
			a.activateTab(a.activeTab)
		} else {
			a.activeView = a.tabs[a.activeTab].Kind
		}
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
