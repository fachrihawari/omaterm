package main

import (
	"github.com/egoist/mygo/ui"
)

// The root view: projects sidebar | (tabs + content + inspector) with the
// status bar below, and the window-level shortcuts.

func (a *appState) view(c *ui.Context) {
	c.SetTheme(omaTheme())

	// Shortcuts, as in the mock.
	c.OnShortcut(ui.Cmd, ui.KeyB, func() { a.sidebarVisible = !a.sidebarVisible })
	c.OnShortcut(ui.Cmd|ui.Shift, ui.KeyB, func() { a.inspectorVisible = !a.inspectorVisible })
	c.OnShortcut(ui.Cmd, ui.Key1, func() { a.activeView = tabTerminal })
	c.OnShortcut(ui.Cmd, ui.Key2, func() { a.activeView = tabEditor })
	c.OnShortcut(ui.Cmd, ui.Key3, func() { a.activeView = tabDiff })

	ui.Column(c).Fill().Background(colBg).Children(func() {
		if a.sidebarVisible {
			ui.SplitPercent(c, &a.sidebarPct, func() {
				a.projectsSidebar(c)
			}, func() {
				a.mainColumn(c)
			}).Grow(1)
		} else {
			ui.Column(c).Grow(1).Children(func() {
				a.mainColumn(c)
			})
		}
		a.statusBar(c)
	})
}

func (a *appState) mainColumn(c *ui.Context) {
	ui.Column(c).Grow(1).MinWidth(0).Children(func() {
		a.tabBar(c)
		if a.inspectorVisible {
			// The inspector is second; SplitPercent's percent is the first
			// pane's share, so hand it the content's.
			contentPct := 100 - a.inspectorPct
			ui.SplitPercent(c, &contentPct, func() {
				a.content(c)
			}, func() {
				a.inspector(c)
			}).Grow(1)
			a.inspectorPct = 100 - contentPct
		} else {
			a.content(c)
		}
	})
}

func (a *appState) content(c *ui.Context) {
	switch a.activeView {
	case tabEditor:
		a.editorView(c)
	case tabDiff:
		a.diffView(c)
	default:
		a.terminalView(c)
	}
}
