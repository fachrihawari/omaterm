package main

import (
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"omaterm/internal/agent"
	"omaterm/internal/procs"
	"omaterm/internal/session"
)

// The top tab bar with terminal/editor/diff tabs and the window-level
// controls on the right.

func (a *appState) tabBar(c *ui.Context) ui.Element {
	left := float32(8)
	if a.sidebarShown {
		left = 0
	}
	return ui.Row(c).Height(40).Shrink(0).AlignItems(ui.Center).Padding(0, 8, 0, left).Gap(4).Children(func() {
		// Show-sidebar button, only while the sidebar is hidden.
		if !a.sidebarShown {
			if a.cmdButton(c, iconPanelLeft, 15, false).
				Tooltip("Show Sidebar (" + keyLabels("view.toggleSidebar") + ")").Label("Show Sidebar").Clicked() {
				a.setSidebar(true)
			}
		}
		// The tabs scroll sideways when they don't fit, the shown one kept
		// in view; "+" stays outside, always there. Clipped, tabs past the
		// edge and "+" were out of reach.
		ui.ScrollHorizontal(c.Key("tabs")).Shrink(1).MinWidth(0).Height(40).Children(func() {
			ui.Row(c).Height(40).AlignItems(ui.Center).Gap(4).Children(func() {
				seen := map[string]bool{}
				for _, i := range a.visibleTabIndices() {
					e := a.topTab(c, i, seen)
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
			a.press(a.fadeColors(plus))
			plus.Children(func() {
				ui.Icon(c, iconPlus).Size(14, 14).TextColor(pal.TextMuted)
			})
			if plus.Clicked() {
				a.newTerminalTab()
			}
		})
		ui.Row(c).AlignItems(ui.Center).Gap(2).Children(func() {
			if a.cmdButton(c, iconKeyboard, 15, false).
				Tooltip("Keyboard Shortcuts (" + keyLabels("help.keyboardShortcuts") + ")").
				Label("Keyboard Shortcuts").Clicked() {
				a.openOverlay(overlayShortcuts)
			}
			if a.cmdButton(c, iconSearch, 15, false).
				Tooltip("Search in files (" + keyLabels("search.global") + ")").Clicked() {
				a.openOverlay(overlayGlobalSearch)
			}
			if a.cmdButton(c, iconPanelRight, 15, a.inspectorShown).
				Tooltip("Toggle Inspector (" + keyLabels("view.toggleInspector") + ")").Clicked() {
				a.setInspector(!a.inspectorShown)
			}
		})
	})
}

// visibleTabIndices are the tabs the tab bar shows and Next Tab walks:
// every tab, a process's too, so that what the sidebar opens can be gone
// back to the way any other tab is.
func (a *appState) visibleTabIndices() []int {
	out := make([]int, len(a.tabs))
	for i := range a.tabs {
		out[i] = i
	}
	return out
}

// topTab is a tab as a pill: a session with its status, or an editor,
// diff or graph with its icon.
func (a *appState) topTab(c *ui.Context, i int, seen map[string]bool) ui.Element {
	if i < 0 || i >= len(a.tabs) {
		return ui.Box(c)
	}
	t := &a.tabs[i]
	active := i == a.activeTab
	// A click on the process's Dev URL opens it, and does not also
	// activate the tab.
	linkClicked := false
	e := ui.ButtonBase(c.Key(tabKey(t, seen))).Height(26).Radius(13).Padding(0, 6, 0, 10).Gap(6).Shrink(0).AlignItems(ui.Center)
	switch {
	case active:
		raised(e.Background(pal.Raised))
	case e.Hovered():
		e.Background(pal.Hover)
	}
	a.transition(e, motionTab)
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
		case tabProcess:
			// Its status, as its sidebar row shows it; a stopped one
			// shows what it is instead of nothing.
			if proc, ok := a.procOfTab(*t); ok && proc.Status != procs.Stopped {
				a.processIcon(c, proc, 12)
			} else {
				ui.Icon(c, iconTerminal).Size(13, 13).TextColor(pal.TextFaint)
			}
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
		if t.Kind == tabProcess {
			if proc, ok := a.procOfTab(*t); ok {
				switch {
				case proc.Link != "":
					// Served at its Dev URL: the host, which opens it.
					link := ui.Text(c, procLinkHost(proc.Link)).FontSize(11).TextColor(pal.TextFaint).SingleLine().Shrink(0).
						Cursor(ui.CursorPointer).Tooltip("Open " + proc.Link)
					if link.Clicked() {
						linkClicked = true
						a.openURL(proc.Link)
					}
				case proc.Port > 0:
					ui.Text(c, ":"+itoa(proc.Port)).FontSize(11).TextColor(pal.TextFaint).SingleLine().Shrink(0)
				}
				if proc.Share != "" {
					// The pill is a button of its own: it takes the press,
					// so the tab does not also activate.
					pillShare(c, proc, func() { a.openURL(proc.Share) })
				}
			}
		}
		if t.Dirty {
			ui.Text(c, "M").FontSize(10).TextColor(pal.Warn).SingleLine().Shrink(0)
		}
		if t.Kind == tabEditor && t.Path != "" {
			if doc := a.editors[t.Path]; doc != nil && doc.State != nil && doc.State.Buffer.Modified() {
				a.transition(ui.Text(c.Key("modified"), "●").FontSize(10).TextColor(pal.Warn).SingleLine().Shrink(0), motionPop)
			}
		}
		// The close button keeps its room, so pills keep their width, but
		// shows only on the active or hovered pill.
		show := len(a.tabs) > 1 && (active || e.Hovered())
		x := ui.ButtonBase(c.Key("x")).Size(18, 18).Radius(9).Shrink(0).Center().Disabled(!show)
		x.Opacity(a.animate(x, "show", b2f(show), motionFast))
		if x.Hovered() {
			x.Background(pal.Hover)
		}
		a.fadeColors(x)
		x.Children(func() {
			ui.Icon(c, iconX).Size(12, 12).TextColor(pal.TextMuted)
		})
		if x.Clicked() {
			a.closingTab = i + 1 // after the row is built: closed in it, the next tab was skipped
		}
	})
	if e.Clicked() && !linkClicked {
		a.activateTab(i)
	}
	return e
}

// tabKey names tab t's pill by what it shows, not where, so a pill
// keeps its motion as tabs before it close; seen makes a name shared by
// two tabs one of each.
func tabKey(t *topTab, seen map[string]bool) string {
	k := strconv.Itoa(int(t.Kind)) + ":" + t.ID + ":" + t.Path + ":" + t.Process
	if t.ID == "" && t.Path == "" && t.Process == "" {
		k += ":" + t.Label
	}
	for base, n := k, 2; seen[k]; n++ {
		k = base + "#" + strconv.Itoa(n)
	}
	seen[k] = true
	return k
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
	if (t.Kind == tabTerminal || t.Kind == tabProcess) && t.Split != nil {
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
// nothing to show. Alt+2 must not jump from the second editor back to
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
	if t.Kind != tabProcess && t.Split != nil { // any tab a split ever got, so its shells end
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
	// The view shows no tab of its kind ("No file open"): the tab behind
	// it, a session, is not what the user sees to close.
	if a.tabs[a.activeTab].Kind != a.activeView {
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
// relaunches, with editors unsaved, or, with no daemon to keep them, a
// session running a program: they were lost unasked.
func (a *appState) beforeClose(e *mygo.CloseEvent) {
	if a.quitAnyway || len(a.unsavedEditors()) == 0 && a.quitEndsProgram() == "" {
		a.quitWant = ""
		a.stopProcListRefresh()
		if a.procHost != nil {
			a.procHost.Close()
		}
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

// quitEndsProgram names a program other than a shell that quitting would
// end, "" for none or when the settings say not to ask. A daemon's panes
// go on without the app; without one (on Windows, or with the daemon off)
// they end with it, as Alt+F4 or ⌘Q ended running agents unasked.
func (a *appState) quitEndsProgram() string {
	if a.panes == nil || !a.panes.Owned() || !a.settings.ConfirmClose {
		return ""
	}
	ids := make([]string, 0, len(a.status))
	for id := range a.status {
		ids = append(ids, id)
	}
	slices.Sort(ids) // the same program named every frame
	return a.runningProgram(ids)
}

// quitText says what quitting, or verb, loses: "a.go has unsaved changes.
// Claude Code is still running. Quitting loses them and ends it."
func (a *appState) quitText(verb string) string {
	var said, loses []string
	if files := a.unsavedEditors(); len(files) == 1 {
		said, loses = append(said, files[0]+" has unsaved changes."), append(loses, "loses them")
	} else if len(files) > 1 {
		said, loses = append(said, plural(len(files), "file")+" have unsaved changes."), append(loses, "loses them")
	}
	if what := a.quitEndsProgram(); what != "" {
		said, loses = append(said, what+" is still running."), append(loses, "ends it")
	}
	if len(said) == 0 { // saved, or ended, while the dialog was open
		return "Nothing is unsaved or running any more."
	}
	gerund := map[string]string{"Quit": "Quitting", "Restart": "Restarting"}[verb]
	return strings.Join(said, " ") + " " + gerund + " " + strings.Join(loses, " and ") + "."
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
	if t.Kind == tabProcess {
		// Its tab goes; the process does not: Stop is what ends it.
		a.closeTab(i)
		if proc, ok := a.procOfTab(t); ok && (proc.Status == procs.Starting || proc.Status == procs.Running) {
			a.toast(nil, proc.Name+" keeps running. Stop it from the sidebar.")
		}
		return
	}
	if t.Kind != tabTerminal || t.Split == nil {
		a.closeTab(i)
		return
	}
	if kids := a.startedBy(t.ID); len(kids) > 0 {
		a.closeParentAsking(t, kids)
		return
	}
	a.closeSessionAsking(t)
}

// closeSessionAsking closes a session its own way: asking about its
// worktree's work, or the program it still runs.
func (a *appState) closeSessionAsking(t topTab) {
	if t.Worktree != nil && !t.Worktree.Gone() {
		a.closeWorktreeAsking(t)
		return
	}
	id := t.ID
	a.confirmCloseRunning(t.Split.termIDs(), "Close Session", func() { a.closeTabID(id) })
}

// startedBy lists the sessions of the shown project that the session id
// started, and those they started, and so on.
func (a *appState) startedBy(id string) []topTab {
	var kids []topTab
	for _, n := range sessionOrder(a.tabs) {
		if a.tabs[n.index].ID == id {
			for _, k := range n.kids {
				kids = append(kids, a.tabs[k])
			}
		}
	}
	return kids
}

// closeParentAsking closes a session that started others: with them, or
// leaving them as sessions of their own. Their worktrees stay either way,
// as closing a worktree's session from elsewhere keeps it.
func (a *appState) closeParentAsking(t topTab, kids []topTab) {
	names := make([]string, len(kids))
	worktrees := false
	for i, k := range kids {
		names[i] = a.tabName(k)
		worktrees = worktrees || k.Worktree != nil
	}
	text := a.tabName(t) + " started " + andList(names) + ". Close them too, or keep them as sessions of their own."
	if worktrees {
		text += " Their worktrees stay either way."
	}
	if t.Worktree == nil {
		// Asked here, not again after.
		if what := a.runningProgram(t.Split.termIDs()); what != "" {
			text = what + " is still running. Closing ends it. " + text
		}
	}
	closeIt := func() {
		if t.Worktree != nil && !t.Worktree.Gone() {
			a.closeWorktreeAsking(t)
		} else {
			a.closeTabID(t.ID)
		}
	}
	a.closeAsk = &closeAsk{open: true, title: "Close Session", text: text, alt: "Keep Them", altRun: closeIt,
		confirm: "Close All", run: func() {
			for _, k := range kids {
				a.cliCloseSession(k.ID)
			}
			closeIt()
		}}
}

// andList joins names as a sentence does: "a", "a and b", "a, b and c".
func andList(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// closeTabID closes the shown project's tab id.
func (a *appState) closeTabID(id string) {
	for j, tab := range a.tabs {
		if tab.ID == id {
			a.closeTab(j)
			return
		}
	}
}

// askConfirm asks a question whose answer changes something the user can
// change back, as sharing a process: its last button runs run. Unlike
// askDanger, the sheet is no warning.
func (a *appState) askConfirm(title, text, confirm string, run func()) {
	a.closeAsk = &closeAsk{open: true, title: title, text: text, confirm: confirm, run: run, plain: true}
}

// closeAsk is a close waiting for the user to confirm it.
type closeAsk struct {
	open        bool
	title, text string
	confirm     string // the button that does it: "Close" when empty
	run         func()
	// alt is a button between Cancel and confirm, which runs altRun.
	alt    string
	altRun func()
	// plain tells that the sheet warns of nothing that can't be undone:
	// its confirm button is the accent, not the danger color.
	plain bool
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
