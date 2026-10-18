package main

import (
	"github.com/egoist/mygo/ui"
)

// The root view: projects sidebar | (tabs + content + inspector) with the
// status bar below, the window-level shortcuts, and modal overlays.

func (a *appState) view(c *ui.Context) {
	c.SetTheme(omaTheme())

	// Window shortcuts. Ctrl-based on Linux (MyGo maps ui.Cmd to the
	// platform's primary modifier).
	c.OnShortcut(ui.Cmd, ui.KeyB, func() { a.sidebarVisible = !a.sidebarVisible })
	c.OnShortcut(ui.Cmd|ui.Shift, ui.KeyB, func() { a.inspectorVisible = !a.inspectorVisible })
	c.OnShortcut(ui.Cmd, ui.Key1, func() { a.activeView = tabTerminal })
	c.OnShortcut(ui.Cmd, ui.Key2, func() { a.activeView = tabEditor })
	c.OnShortcut(ui.Cmd, ui.Key3, func() { a.activeView = tabDiff })
	c.OnShortcut(ui.Cmd, ui.KeyW, func() { a.closeActiveTab(c) })
	c.OnShortcut(ui.Cmd, ui.KeyO, func() { a.openProjectDialog() })
	c.OnShortcut(ui.Cmd, ui.KeyT, func() { a.newTerminalTab() })

	// Terminal split shortcuts (only while the terminal is active).
	if a.activeView == tabTerminal {
		c.OnShortcut(ui.Cmd|ui.Shift, ui.KeyD, func() {
			if leaf := a.activeSplitLeaf(); leaf != "" {
				a.splitPane(leaf, splitHorizontal)
				c.Toast("Split down")
			}
		})
		c.OnShortcut(ui.Cmd|ui.Shift, ui.KeyR, func() {
			if leaf := a.activeSplitLeaf(); leaf != "" {
				a.splitPane(leaf, splitVertical)
				c.Toast("Split right")
			}
		})
	}

	// Overlay openers, only while no overlay is open.
	if a.overlay == overlayNone {
		c.OnShortcut(ui.Cmd, ui.KeyP, func() { a.openOverlay(overlayQuickOpen) })
		c.OnShortcut(ui.Cmd|ui.Shift, ui.KeyP, func() { a.openOverlay(overlayCommandPalette) })
		c.OnShortcut(ui.Cmd|ui.Shift, ui.KeyF, func() { a.openOverlay(overlayGlobalSearch) })
	}
	a.overlayShortcuts(c)

	ui.Column(c).Fill().Background(colBg).Children(func() {
		if a.sidebarVisible {
			ui.Split(c, &a.sidebarW, func() {
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

	// Close-tab confirmation dialog.
	if a.closeTabConfirmOpen != nil {
		result := ui.AlertDialog(c, a.closeTabConfirmOpen,
			"Close Tab",
			"This file has unsaved changes. Close anyway?",
			"Cancel", "Close")
		if result == 1 {
			a.closeTab(a.pendingCloseTab)
			a.closeTabConfirmOpen = nil
		} else if result == 0 {
			a.closeTabConfirmOpen = nil
		}
	}

	a.renderOverlays(c)
}

func (a *appState) mainColumn(c *ui.Context) {
	ui.Column(c).Grow(1).MinWidth(0).Children(func() {
		a.tabBar(c)
		if a.inspectorVisible {
			ui.Row(c).Grow(1).MinWidth(0).FillHeight().Children(func() {
				ui.Box(c).Grow(1).MinWidth(0).FillHeight().ClipX().Children(func() {
					a.content(c)
				})
				ui.Box(c).Width(a.inspectorW).Shrink(0).FillHeight().Children(func() {
					a.inspector(c)
				})
			})
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
	case tabGraph:
		a.graphView(c)
	default:
		a.terminalView(c)
	}
}
