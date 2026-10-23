package main

import (
	"github.com/egoist/mygo/ui"

	"omaterm/service"
)

// The root view: projects sidebar | (tabs + content + inspector) with the
// status bar below, the window-level shortcuts, and modal overlays.

func (a *appState) view(c *ui.Context) {
	a.drainPosted()
	a.trackViewed()
	c.SetTheme(omaTheme())

	// Every window shortcut comes from the binding table in
	// keybindings.go, which also feeds the command palette and the
	// Keyboard Shortcuts overlay.
	a.registerKeybindings(c)
	a.overlayWindowKeys(c)
	for _, id := range a.queuedActions {
		for _, cmd := range commandRegistry {
			if cmd.ID == id && (cmd.Enabled == nil || cmd.Enabled(a)) {
				cmd.Run(a, c)
			}
		}
	}
	a.queuedActions = nil
	for _, msg := range a.queuedToasts {
		c.Toast(msg)
	}
	a.queuedToasts = nil

	root := ui.Column(c).Fill()
	a.wash(root)
	root.Children(func() {
		if a.settings.Background != "plain" {
			// A glow of the accent from the top right corner, as the light
			// of the window comes from there.
			ui.Box(c.Key("glow")).Absolute().Top(0).Left(0).Right(0).Bottom(0).PassThrough().
				LinearGradient(ui.LinearGradient{Angle: 225, Stops: []ui.GradientStop{
					{Color: pal.WashGlow, At: 0}, {Color: pal.WashGlow.Alpha(0), At: 0.55},
				}})
		}
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			a.sidebar(c)
			a.mainColumn(c)
		})
		a.statusBar(c)
	})

	// Close-tab confirmation dialog.
	if a.closeTabConfirmOpen != nil {
		result := a.confirmDialog(c, a.closeTabConfirmOpen,
			"Close Tab",
			"This file has unsaved changes. Close anyway?",
			true, "Cancel", "Close")
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
			true, "Cancel", "Kill")
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
		result := a.confirmDialog(c, a.removeProjectConfirmOpen,
			"Remove Project",
			"Remove "+a.pendingRemoveProjectName+" from the list? Files on disk are not deleted.",
			true, "Cancel", "Remove")
		if result != -1 {
			if result == 1 {
				a.removeProject(a.pendingRemoveProjectIdx)
				c.Toast("Removed " + a.pendingRemoveProjectName)
			}
			a.removeProjectConfirmOpen = nil
		}
	}

	a.daemonDialog(c)
	a.renderOverlays(c)
}

// wash paints the window's backdrop: a soft gradient of the theme's
// background, which the cards sit on, or the plain color.
func (a *appState) wash(e ui.Element) {
	if a.settings.Background == "plain" {
		e.Background(pal.Wash)
		return
	}
	e.LinearGradient(ui.LinearGradient{Angle: 180, Oklab: true, From: pal.Wash, To: pal.Wash.Mix(pal.Accent, 0.04)})
}

func (a *appState) mainColumn(c *ui.Context) {
	gap := a.paneGap()
	left := gap
	if a.sidebarVisible {
		left = 0
	}
	ui.Column(c).Grow(1).MinWidth(0).FillHeight().Children(func() {
		a.tabBar(c)
		ui.Row(c).Grow(1).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch).Padding(0, gap, gap, left).Gap(gap).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
				a.daemonBanner(c)
				// No pane starts until the user chose what runs it.
				if a.daemonRefusedOpen == nil {
					a.content(c)
				}
			})
			if a.inspectorVisible {
				card := ui.Column(c).Width(a.inspectorW).Shrink(0).FillHeight().Radius(a.cardRadius()).Clip().Background(pal.Card)
				raised(card).Children(func() {
					a.inspector(c)
				})
			}
		})
	})
}

func (a *appState) content(c *ui.Context) {
	if a.activeView == tabTerminal {
		a.terminalView(c)
		return
	}
	// The editor, diff and graph views sit on a card as a pane does.
	card := ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Radius(a.cardRadius()).Clip().Background(pal.Card)
	raised(card).Children(func() {
		switch a.activeView {
		case tabEditor:
			a.editorView(c)
		case tabDiff:
			a.diffView(c)
		case tabGraph:
			a.graphView(c)
		}
	})
}

// confirmDialog shows a bordered confirmation dialog using DialogBase (which
// has an explicit border, unlike ui.AlertDialog). danger paints the
// confirming button red, for actions that destroy something. Returns the
// chosen button index, or ui.AlertDismissed when dismissed.
func (a *appState) confirmDialog(c *ui.Context, open *bool, title, message string, danger bool, buttons ...string) int {
	if !*open {
		return -1
	}
	result := -1
	ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		backdrop.Background(pal.Scrim)
		panel.Background(pal.Popover).Radius(10).Border(1, pal.BorderStrong).
			Width(420).MaxHeight(300).Padding(16).Gap(12).
			Shadow(0, 10, 30, 0, pal.FloatShadow)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if danger {
				ui.Icon(c, iconAlert).Width(16).Height(16).TextColor(pal.Bad)
			}
			ui.Text(c, title).FontSize(14).Bold().TextColor(pal.Text)
		})
		if message != "" {
			ui.Text(c, message).FontSize(12).TextColor(pal.TextMuted)
		}
		ui.Row(c).Gap(8).Justify(ui.End).Margin(8, 0, 0, 0).Children(func() {
			for i, label := range buttons {
				if i == len(buttons)-1 {
					var btn ui.Element
					if danger {
						btn = dangerButton(c, label)
					} else {
						btn = ui.PrimaryButton(c, label).Height(32).FontSize(12)
					}
					if btn.Clicked() {
						result = i
					}
				} else {
					btn := ui.ButtonBase(c).Padding(6, 14).Radius(6).Border(1, pal.BorderStrong).Height(32).Children(func() {
						ui.Text(c, label).FontSize(12).TextColor(pal.TextSoft)
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

// dangerButton is a filled red button for destructive confirmations. The
// theme's red is light, so its label is dark for contrast.
func dangerButton(c *ui.Context, label string) ui.Element {
	e := ui.ButtonBase(c).Height(32).Padding(6, 14).Radius(6).Background(pal.Bad).Shrink(0)
	if e.Hovered() {
		e.Background(pal.Bad.Alpha(0.85))
	}
	e.Children(func() {
		ui.Text(c, label).FontSize(12).FontWeight(600).TextColor(pal.OnBad)
	})
	return e
}

// post runs fn on the UI thread: through the window's update queue, or,
// with no window (tests), at the start of the next frame. Goroutines that
// change app state, as a terminal's exit or a background load, hand it
// over this way instead of touching the state themselves.
func (a *appState) post(fn func()) {
	if a.win != nil {
		a.win.Update(fn)
		return
	}
	a.postedMu.Lock()
	a.posted = append(a.posted, fn)
	a.postedMu.Unlock()
}

// drainPosted runs what post queued while there was no window.
func (a *appState) drainPosted() {
	a.postedMu.Lock()
	fns := a.posted
	a.posted = nil
	a.postedMu.Unlock()
	for _, fn := range fns {
		fn()
	}
}
