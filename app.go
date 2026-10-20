package main

import (
	"github.com/egoist/mygo/ui"

	"oterm/service"
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
		result := a.confirmDialog(c, a.closeTabConfirmOpen,
			"Close Tab",
			"This file has unsaved changes. Close anyway?",
			"Cancel", "Close")
		if result != -1 {
			if result == 1 {
				a.closeTab(a.pendingCloseTab)
			}
			a.closeTabConfirmOpen = nil
		}
	}

	// Kill-process confirmation dialog.
	if a.killConfirmOpen != nil {
		result := a.confirmDialog(c, a.killConfirmOpen,
			"Kill Process",
			"Kill "+a.pendingKillName+" (PID "+itoa(a.pendingKillPid)+")?",
			"Cancel", "Kill")
		if result != -1 {
			if result == 1 {
				if err := service.KillProcess(a.pendingKillPid); err != nil {
					c.Toast("Kill failed: " + err.Error())
				} else {
					c.Toast("Killed " + a.pendingKillName)
				}
				a.refreshProcesses()
			}
			a.killConfirmOpen = nil
		}
	}

	// Remove-project confirmation dialog.
	if a.removeProjectConfirmOpen != nil {
		result := ui.AlertDialog(c, a.removeProjectConfirmOpen,
			"Remove Project",
			"Remove "+a.pendingRemoveProjectName+" from the list? Files on disk are not deleted.",
			"Cancel", "Remove")
		if result == 1 {
			a.removeProject(a.pendingRemoveProjectIdx)
			a.removeProjectConfirmOpen = nil
			c.Toast("Removed " + a.pendingRemoveProjectName)
		} else if result == 0 {
			a.removeProjectConfirmOpen = nil
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

// confirmDialog shows a bordered confirmation dialog using DialogBase (which
// has an explicit border, unlike ui.AlertDialog). Returns the chosen button
// index, or ui.AlertDismissed when dismissed.
func (a *appState) confirmDialog(c *ui.Context, open *bool, title, message string, buttons ...string) int {
	if !*open {
		return -1
	}
	result := -1
	ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.5))
		panel.Background(colPanel).Radius(10).Border(1, colBorder2).
			Width(420).MaxHeight(300).Padding(16).Gap(12).
			Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.4))
		ui.Text(c, title).FontSize(14).Bold().TextColor(colText)
		if message != "" {
			ui.Text(c, message).FontSize(12).TextColor(colMuted)
		}
		ui.Row(c).Gap(8).Justify(ui.End).Margin(8, 0, 0, 0).Children(func() {
			for i, label := range buttons {
				if i == len(buttons)-1 {
					btn := ui.PrimaryButton(c, label).Height(32).FontSize(12)
					if btn.Clicked() {
						result = i
					}
				} else {
					btn := ui.ButtonBase(c).Padding(6, 14).Radius(6).Border(1, colBorder2).Height(32).Children(func() {
						ui.Text(c, label).FontSize(12).TextColor(colText2)
					})
					if btn.Clicked() {
						result = i
					}
				}
			}
		})
	})
	if result != -1 {
		*open = false
		return result
	}
	if !*open {
		return ui.AlertDismissed
	}
	return -1
}
