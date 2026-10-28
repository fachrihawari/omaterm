package main

import (
	"time"

	"github.com/egoist/mygo/ui"

	"omaterm/service"
)

// The root view: projects sidebar | (tabs + content + inspector) with the
// status bar below, the window-level shortcuts, and modal overlays.

func (a *appState) view(c *ui.Context) {
	a.drainPosted()
	a.trackViewed()
	a.followWorktreeRepo()
	c.SetTheme(omaTheme())
	winW, _ := c.Size()
	a.fitPanels(winW)

	// Every window shortcut comes from the binding table in
	// keybindings.go, which also feeds the command palette and the
	// Keyboard Shortcuts overlay.
	a.registerKeybindings(c)
	a.overlayWindowKeys(c)
	// A dialog answers first: an overlay under it goes, and the omaterm
	// command's actions wait for it, as the window's keys do. Run over a
	// dialog, the palette covered it, two scrims deep.
	if a.modalOpen() {
		if a.overlay != overlayNone {
			a.closeOverlay()
		}
	} else {
		for _, id := range a.queuedActions {
			for _, cmd := range commandRegistry {
				if cmd.ID == id && (cmd.Enabled == nil || cmd.Enabled(a)) {
					cmd.Run(a, c)
				}
			}
		}
		a.queuedActions = nil
	}
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

	// Closing a session or pane that still runs a program.
	if ask := a.closeAsk; ask != nil {
		confirm := ask.confirm
		if confirm == "" {
			confirm = "Close"
		}
		buttons := []string{"Cancel", confirm}
		if ask.alt != "" {
			buttons = []string{"Cancel", ask.alt, confirm}
		}
		result := a.confirmDialog(c, &ask.open, ask.title, ask.text, true, buttons...)
		if result != -1 || !ask.open {
			a.closeAsk = nil
			switch {
			case result == len(buttons)-1:
				ask.run()
			case result == 1:
				ask.altRun()
			}
		}
	}

	// Close-tab confirmation dialog.
	if a.closeTabConfirmOpen != nil {
		result := a.confirmDialog(c, a.closeTabConfirmOpen,
			"Close Tab",
			"This file has unsaved changes. Close anyway?",
			true, "Cancel", "Close")
		if result != -1 {
			if result == 1 {
				a.closePendingEditor()
			}
			a.closeTabConfirmOpen = nil
		}
	}

	// Saving over a file changed on disk.
	if a.overwriteAsk != nil {
		doc := a.overwriteDoc
		result := a.confirmDialog(c, a.overwriteAsk, "Overwrite "+pathBase(doc.Path)+"?",
			doc.Path+" changed on disk since you opened it, perhaps by an agent. Saving replaces those changes with yours.",
			true, "Cancel", "Overwrite")
		if result != -1 {
			a.overwriteAsk, a.overwriteDoc = nil, nil
			if result == 1 && doc.State != nil {
				a.writeEditor(doc, c)
			}
		}
	}

	// Quitting with editors unsaved, or a session's program that ends.
	if a.quitAsk != nil {
		verb := "Quit"
		if a.quitWant == "relaunch" {
			verb = "Restart"
		}
		result := a.confirmDialog(c, a.quitAsk, verb+" omaterm?", a.quitText(verb),
			true, "Cancel", verb+" Anyway")
		if result != -1 {
			want := a.quitWant
			a.quitAsk, a.quitWant = nil, ""
			if result == 1 {
				a.quitAnyway = true
				a.quitNow(want)
			}
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

	a.webPairDialog(c)
	a.worktreeDialog(c)

	// Remove-project confirmation dialog.
	if a.removeProjectConfirmOpen != nil {
		result := a.confirmDialog(c, a.removeProjectConfirmOpen,
			"Remove Project",
			"Remove "+a.pendingRemoveProjectName+" from the list? Files on disk are not deleted."+
				a.endingText(a.projectPaneIDs(a.pendingRemoveProjectID), a.projectUnsaved(a.pendingRemoveProjectID)),
			true, "Cancel", "Remove")
		if result != -1 {
			if result == 1 {
				for i, p := range a.projects {
					if p.ID == a.pendingRemoveProjectID {
						a.removeProject(i)
						break
					}
				}
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
// modalOpen tells whether a dialog of the window waits for an answer.
func (a *appState) modalOpen() bool {
	return a.closeTabConfirmOpen != nil || a.quitAsk != nil || a.overwriteAsk != nil || a.killConfirmOpen != nil || a.removeProjectConfirmOpen != nil ||
		a.closeAsk != nil || a.webPairOpen != nil || a.daemonRefusedOpen != nil || a.wtNew != nil
}

// animColor animates e's color to target, or, with Reduce Motion on,
// gives it at once.
func (a *appState) animColor(e ui.Element, key string, target ui.Color, d time.Duration) ui.Color {
	if a.settings.ReduceMotion {
		return target
	}
	return e.AnimateColor(key, target, ui.Animation{Duration: d})
}

// animate animates a value of e to target, or, with Reduce Motion on,
// gives it at once.
func (a *appState) animate(e ui.Element, key string, target float32, d time.Duration) float32 {
	if a.settings.ReduceMotion {
		return target
	}
	return e.Animate(key, target, d)
}

func (a *appState) wash(e ui.Element) {
	if a.settings.Background == "plain" {
		e.Background(pal.Wash)
		return
	}
	e.LinearGradient(ui.LinearGradient{Angle: 180, Oklab: true, From: pal.Wash, To: pal.Wash.Mix(pal.Accent, 0.04)})
}

// minPanesWidth is what the panes keep however narrow the window: the side
// panels give way to them. A window tiled to half a laptop's screen is
// about 750 wide.
const minPanesWidth = 320

const (
	panelSidebar   = "sidebar"
	panelInspector = "inspector"
)

// fitPanels decides which side panels a window w wide shows: those the user
// has on, while the panes keep minPanesWidth. Too narrow for both, the one
// opened last stays, the inspector going first.
func (a *appState) fitPanels(w float32) {
	side, insp := a.sidebarVisible, a.inspectorVisible
	for side || insp {
		need := float32(minPanesWidth)
		if side {
			need += a.sidebarWidth()
		}
		if insp {
			need += a.inspectorW + a.paneGap()
		}
		if need <= w {
			break
		}
		if insp && (!side || a.lastPanel != panelInspector) {
			insp = false
		} else {
			side = false
		}
	}
	a.sidebarShown, a.inspectorShown = side, insp
}

// setSidebar and setInspector show or hide a side panel; one shown is the
// one that stays when the window is too narrow for both.
func (a *appState) setSidebar(on bool) {
	a.sidebarVisible = on
	if on {
		a.lastPanel = panelSidebar
	}
}

func (a *appState) setInspector(on bool) {
	a.inspectorVisible = on
	if on {
		a.lastPanel = panelInspector
	}
}

func (a *appState) mainColumn(c *ui.Context) {
	gap := a.paneGap()
	left := gap
	if a.sidebarShown {
		left = 0
	}
	ui.Column(c).Grow(1).MinWidth(0).FillHeight().Children(func() {
		a.tabBar(c)
		ui.Row(c).Grow(1).MinWidth(0).MinHeight(0).AlignItems(ui.Stretch).Padding(0, gap, gap, left).Gap(gap).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
				a.stateBanner(c)
				a.serviceBanner(c)
				a.ignoredDaemonBanner(c)
				a.crashBanner(c)
				a.daemonBanner(c)
				a.agentSetupBanner(c)
				// No pane starts until the user chose what runs it: a card
				// says so behind the dialog, where the panes were a hole.
				if a.daemonRefusedOpen == nil {
					a.content(c)
				} else {
					card := ui.Column(c.Key("waiting")).Grow(1).MinHeight(0).Radius(a.cardRadius()).Background(pal.Card).
						AlignItems(ui.Center).Justify(ui.Center).Gap(8)
					cardEdge(card).Children(func() {
						ui.Icon(c, iconTerminal).Size(22, 22).TextColor(pal.TextFaint)
						ui.Text(c, "Your sessions wait for the session daemon").FontSize(12.5).TextColor(pal.TextMuted)
					})
				}
			})
			if a.inspectorShown {
				card := ui.Column(c).Width(a.inspectorW).Shrink(0).FillHeight().Radius(a.cardRadius()).Clip().Background(pal.Card)
				cardEdge(card).Children(func() {
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
	cardEdge(card).Children(func() {
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

// The app's dialogs, in either window, share one look: dialogWidth wide, a
// fifth of the way down the window over a dimmed one, a 600-weight title,
// dialogButtonHeight buttons, red for the one that destroys something.
const (
	dialogWidth        = 420
	dialogButtonHeight = 30
)

// dialogFrame styles a DialogBase's backdrop and panel.
func dialogFrame(c *ui.Context, backdrop, panel ui.Element) {
	winW, winH := c.Size()
	w := float32(dialogWidth)
	if avail := winW - 2*dialogMinSide; avail > 0 && w > avail {
		w = avail
	}
	// DialogBase centers the panel; starting the column at its top and
	// offsetting the panel puts it where the pickers are.
	backdrop.Justify(ui.Start).Background(pal.Scrim)
	floating(panel.Top(winH * dialogTopFraction).Width(w).Padding(18).Gap(10).
		Radius(10).Background(pal.Popover))
}

// dialogTitle is a dialog's title, with a warning sign for danger.
func dialogTitle(c *ui.Context, title string, danger bool) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if danger {
			ui.Icon(c, iconAlert).Width(16).Height(16).TextColor(pal.Bad)
		}
		ui.Text(c, title).FontSize(14).FontWeight(600).TextColor(pal.TextStrong).Wrap()
	})
}

// dialogButton is a dialog's button: primary filled with the accent, red
// with danger, else bordered. It reports a click.
func dialogButton(c *ui.Context, label string, primary, danger bool) bool {
	b := ui.ButtonBase(c.Key("dlg-"+label)).Height(dialogButtonHeight).Padding(0, 14).Radius(6).
		AlignItems(ui.Center).Shrink(0).Label(label)
	fg, weight := pal.Text, 500
	switch {
	case primary && danger:
		b.Background(pal.Bad)
		if b.Hovered() {
			b.Background(pal.Bad.Alpha(0.85))
		}
		fg, weight = pal.OnBad, 600
	case primary:
		b.Background(pal.Accent)
		if b.Hovered() {
			b.Background(pal.AccentHover)
		}
		fg, weight = pal.AccentText, 600
	default:
		bg := pal.Raised
		if b.Hovered() {
			bg = pal.Raised.Mix(pal.Text, 0.08)
		}
		face(b, bg).Border(1, pal.BorderStrong)
	}
	b.Children(func() {
		ui.Text(c, label).FontSize(12).FontWeight(weight).TextColor(fg).SingleLine()
	})
	return b.Clicked()
}

// confirmDialog asks to confirm with a dialog: the last of buttons
// confirms, red when danger, for actions that destroy something. Returns
// the chosen button index, or ui.AlertDismissed when dismissed.
func (a *appState) confirmDialog(c *ui.Context, open *bool, title, message string, danger bool, buttons ...string) int {
	if !*open {
		return -1
	}
	result := -1
	ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		dialogFrame(c, backdrop, panel)
		dialogTitle(c, title, danger)
		if message != "" {
			ui.Text(c, message).FontSize(12.5).TextColor(pal.TextMuted).Wrap()
		}
		ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Margin(8, 0, 0, 0).Children(func() {
			for i, label := range buttons {
				last := i == len(buttons)-1
				if dialogButton(c, label, last, last && danger) {
					result = i
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

// post runs fn on the UI thread: through the window's update queue, or,
// with no window (tests), at the start of the next frame. Goroutines that
// change app state, as a terminal's exit or a background load, hand it
// over this way instead of touching the state themselves.
func (a *appState) post(fn func()) {
	// postWin, not a.win: goroutines post before and while main sets it.
	if w := a.postWin.Load(); w != nil {
		w.Update(fn)
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
