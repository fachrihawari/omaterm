package main

import (
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"omaterm/internal/agent"
	"omaterm/internal/session"
)

// The top tab bar with terminal/editor/diff tabs and the window-level
// controls on the right.

func (a *appState) tabBar(c *ui.Context) ui.Element {
	left := float32(8)
	if a.sidebarVisible {
		left = 0
	}
	return ui.Row(c).Height(40).Shrink(0).AlignItems(ui.Center).Padding(0, 8, 0, left).Gap(4).Children(func() {
		// Show-sidebar button, only while the sidebar is hidden.
		if !a.sidebarVisible {
			if cmdButton(c, iconPanelLeft, 15, false).
				Tooltip("Show Sidebar (" + keyLabels("view.toggleSidebar") + ")").Label("Show Sidebar").Clicked() {
				a.sidebarVisible = true
			}
		}
		// The tabs scroll sideways when they don't fit, the shown one kept
		// in view; "+" stays outside, always there. Clipped, tabs past the
		// edge and "+" were out of reach.
		ui.ScrollHorizontal(c.Key("tabs")).Shrink(1).MinWidth(0).Height(40).Children(func() {
			ui.Row(c).Height(40).AlignItems(ui.Center).Gap(4).Children(func() {
				for i := 0; i < len(a.tabs); i++ {
					e := a.topTab(c, i)
					if i == a.activeTab && a.activeTab != a.tabInView {
						e.ScrollIntoView()
						a.tabInView = a.activeTab
					}
				}
			})
		})
		if a.closingTab > 0 {
			i := a.closingTab - 1
			a.closingTab = 0
			a.closeTabAsking(i)
		}
		ui.Row(c).Grow(1).MinWidth(0).AlignItems(ui.Center).Children(func() {
			plus := ui.ButtonBase(c.Key("plus")).Size(26, 26).Radius(13).Shrink(0).Center().
				Tooltip("New Terminal (" + keyLabels("terminal.new") + ")")
			if plus.Hovered() {
				plus.Background(pal.Hover)
			}
			plus.Children(func() {
				ui.Icon(c, iconPlus).Size(14, 14).TextColor(pal.TextMuted)
			})
			if plus.Clicked() {
				a.newTerminalTab()
			}
		})
		ui.Row(c).AlignItems(ui.Center).Gap(2).Children(func() {
			if cmdButton(c, iconKeyboard, 15, false).
				Tooltip("Keyboard Shortcuts (" + keyLabels("help.keyboardShortcuts") + ")").
				Label("Keyboard Shortcuts").Clicked() {
				a.openOverlay(overlayShortcuts)
			}
			if cmdButton(c, iconSearch, 15, false).
				Tooltip("Search in files (" + keyLabels("search.global") + ")").Clicked() {
				a.openOverlay(overlayGlobalSearch)
			}
			if cmdButton(c, iconPanelRight, 15, a.inspectorVisible).
				Tooltip("Toggle Inspector (" + keyLabels("view.toggleInspector") + ")").Clicked() {
				a.inspectorVisible = !a.inspectorVisible
			}
		})
	})
}

// topTab is a tab as a pill: a session with its status, or an editor,
// diff or graph with its icon.
func (a *appState) topTab(c *ui.Context, i int) ui.Element {
	if i < 0 || i >= len(a.tabs) {
		return ui.Box(c)
	}
	t := &a.tabs[i]
	active := i == a.activeTab
	e := ui.ButtonBase(c.Key(i)).Height(26).Radius(13).Padding(0, 6, 0, 10).Gap(6).Shrink(0).AlignItems(ui.Center)
	switch {
	case active:
		raised(e.Background(pal.Raised))
	case e.Hovered():
		e.Background(pal.Hover)
	}
	e.Children(func() {
		switch t.Kind {
		case tabTerminal:
			info, _ := a.sessionInfo(*t)
			if statusState(info) != iconNone {
				a.statusIcon(c, info, 12)
			} else {
				ui.Icon(c, programIcon(firstNonEmpty(info.Agent, info.Process))).Size(13, 13).TextColor(pal.TextFaint)
			}
		case tabEditor:
			if isImagePath(t.Path) {
				ui.Icon(c, iconImage).Size(13, 13).TextColor(pal.Ok)
			} else {
				ui.Icon(c, iconFileCode).Size(13, 13).TextColor(pal.Syn.FileTS)
			}
		case tabDiff:
			ui.Icon(c, iconGitCompare).Size(13, 13).TextColor(pal.Ok)
		case tabGraph:
			ui.Icon(c, iconGitGraph).Size(13, 13).TextColor(pal.ANSI[5])
		}
		label := t.Label
		if t.Kind == tabTerminal {
			info, _ := a.sessionInfo(*t)
			label = a.sessionName(*t, info)
		}
		col := pal.TextMuted
		if active {
			col = pal.Text
		}
		ui.Text(c, label).FontSize(12).TextColor(col).SingleLine().Ellipsis("…").MaxWidth(180)
		if t.Detail != "" {
			ui.Text(c, t.Detail).FontSize(11).TextColor(pal.TextFaint).SingleLine().Shrink(0)
		}
		if t.Dirty {
			ui.Text(c, "M").FontSize(10).TextColor(pal.Warn).SingleLine().Shrink(0)
		}
		if t.Kind == tabEditor && t.Path != "" {
			if doc := a.editors[t.Path]; doc != nil && doc.State != nil && doc.State.Buffer.Modified() {
				ui.Text(c, "●").FontSize(10).TextColor(pal.Warn).SingleLine().Shrink(0)
			}
		}
		// The close button keeps its room, so pills keep their width, but
		// shows only on the active or hovered pill.
		show := len(a.tabs) > 1 && (active || e.Hovered())
		x := ui.ButtonBase(c.Key("x")).Size(18, 18).Radius(9).Shrink(0).Center().Opacity(b2f(show)).Disabled(!show)
		if x.Hovered() {
			x.Background(pal.Hover)
		}
		x.Children(func() {
			ui.Icon(c, iconX).Size(12, 12).TextColor(pal.TextMuted)
		})
		if x.Clicked() {
			a.closingTab = i + 1 // after the row is built: closed in it, the next tab was skipped
		}
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
	a.rememberFocus()
	t := a.tabs[i]
	a.activeTab = i
	a.activeView = t.Kind
	switch t.Kind {
	case tabEditor:
		a.checkDisk(a.editors[t.Path]) // changed while another tab showed
	case tabDiff:
		a.showDiffTab(t)
	}
	if t.Kind == tabTerminal && t.Split != nil {
		if t.Focus != "" && t.Split.contains(t.Focus) {
			a.activePaneTermID = t.Focus
		} else if ids := t.Split.termIDs(); len(ids) > 0 {
			a.activePaneTermID = ids[0]
		}
	}
}

// rememberFocus keeps, in the active tab, the pane that has the keyboard,
// for its return and the next launch.
func (a *appState) rememberFocus() {
	if a.activeTab < 0 || a.activeTab >= len(a.tabs) {
		return
	}
	t := &a.tabs[a.activeTab]
	if t.Split != nil && a.activePaneTermID != "" && t.Split.contains(a.activePaneTermID) {
		t.Focus = a.activePaneTermID
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
	if t.Split != nil { // any tab a split ever got, so its shells end
		for _, id := range t.Split.termIDs() {
			a.closePaneRuntime(id)
		}
		a.saveSoon()
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
// editor has unsaved changes or a session still runs a program.
func (a *appState) closeActiveTab(c *ui.Context) {
	if a.activeTab < 0 || a.activeTab >= len(a.tabs) {
		return
	}
	a.closeTabAsking(a.activeTab)
}

// editorModified tells whether the shown project's editor of path has
// unsaved changes.
func (a *appState) editorModified(path string) bool {
	doc := a.editors[path]
	return path != "" && doc != nil && doc.State != nil && doc.State.Buffer.Modified()
}

// unsavedEditors names the files with unsaved changes in every project,
// the shown one's and those waiting in a.ws.
func (a *appState) unsavedEditors() []string {
	var out []string
	add := func(editors map[string]*openFile) {
		for path, doc := range editors {
			if doc != nil && doc.State != nil && doc.State.Buffer.Modified() && !slices.Contains(out, path) {
				out = append(out, path)
			}
		}
	}
	add(a.editors)
	for owner, ws := range a.ws {
		if owner != a.wsOwner && ws != nil {
			add(ws.editors)
		}
	}
	slices.Sort(out)
	return out
}

// projectUnsaved counts a project's editors with unsaved changes.
func (a *appState) projectUnsaved(id string) int {
	editors := a.editors
	if id != a.wsOwner {
		editors = nil
		if ws := a.ws[id]; ws != nil {
			editors = ws.editors
		}
	}
	n := 0
	for _, doc := range editors {
		if doc != nil && doc.State != nil && doc.State.Buffer.Modified() {
			n++
		}
	}
	return n
}

// beforeClose asks before the window closes, or omaterm quits or
// relaunches, with editors unsaved: their changes were lost unasked.
func (a *appState) beforeClose(e *mygo.CloseEvent) {
	if a.quitAnyway || len(a.unsavedEditors()) == 0 {
		a.quitWant = ""
		return
	}
	e.PreventDefault()
	if a.quitWant == "" {
		a.quitWant = "quit"
	}
	open := true
	a.quitAsk = &open
	// The dialog is the main window's: Settings, a window above it, would
	// hide it (Restart Now is in Settings).
	a.closeSettings()
	if a.win != nil {
		a.win.Focus()
		a.win.Invalidate()
	}
}

// relaunch relaunches omaterm, asking first if editors are unsaved.
func (a *appState) relaunch() {
	a.quitWant = "relaunch"
	if a.win != nil {
		mygo.App.Relaunch()
	}
}

// quitNow quits, or relaunches for want "relaunch", unasked.
func (a *appState) quitNow(want string) {
	if a.win == nil {
		return
	}
	if want == "relaunch" {
		mygo.App.Relaunch()
	} else {
		mygo.App.Quit()
	}
}

// closePendingEditor closes the editor tab the unsaved-changes dialog
// asked about, wherever it is now: tabs may have moved meanwhile, and the
// index it had once closed another, possibly a running session.
func (a *appState) closePendingEditor() {
	for i, t := range a.tabs {
		if t.Kind == tabEditor && t.Path == a.pendingClosePath {
			a.closeTab(i)
			return
		}
	}
}

// closeTabAsking closes a tab, first asking when it is a session that
// still runs a program other than its shell and the settings say to ask.
func (a *appState) closeTabAsking(i int) {
	if i < 0 || i >= len(a.tabs) {
		return
	}
	t := a.tabs[i]
	if t.Kind == tabEditor && a.editorModified(t.Path) {
		// The tab's × too, not just Ctrl+W: it closed edits unasked.
		open := true
		a.pendingClosePath = t.Path
		a.closeTabConfirmOpen = &open
		return
	}
	if t.Kind != tabTerminal || t.Split == nil {
		a.closeTab(i)
		return
	}
	id := t.ID
	a.confirmCloseRunning(t.Split.termIDs(), "Close Session", func() {
		for j, tab := range a.tabs {
			if tab.ID == id {
				a.closeTab(j)
				return
			}
		}
	})
}

// closeAsk is a close waiting for the user to confirm it.
type closeAsk struct {
	open        bool
	title, text string
	confirm     string // the button that does it: "Close" when empty
	run         func()
}

// askDanger runs run once the user confirms a step that can't be undone,
// as discarding a file's changes.
func (a *appState) askDanger(title, text, confirm string, run func()) {
	a.closeAsk = &closeAsk{open: true, title: title, text: text, confirm: confirm, run: run}
}

// confirmCloseRunning runs close at once, or, when one of the panes still
// runs a program and the settings say to ask, once the user confirms.
func (a *appState) confirmCloseRunning(panes []string, title string, close func()) {
	if a.settings.ConfirmClose {
		if what := a.runningProgram(panes); what != "" {
			a.closeAsk = &closeAsk{open: true, title: title,
				text: what + " is still running. Closing ends it.", run: close}
			return
		}
	}
	close()
}

// runningProgram names what one of the panes runs besides its shell, ""
// when none does.
func (a *appState) runningProgram(panes []string) string {
	for _, id := range panes {
		info, ok := a.status[id]
		if !ok || info.Status == session.Exited {
			continue
		}
		if info.Agent != "" {
			return agent.DisplayName(info.Agent)
		}
		if info.Process != "" {
			return info.Process
		}
	}
	return ""
}

// newTerminalTab creates a tab backed by a real terminal pane.
func (a *appState) newTerminalTab() {
	a.newTerminalTabReal()
}
