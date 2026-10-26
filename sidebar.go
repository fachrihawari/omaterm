package main

import (
	"omaterm/internal/session"
	"omaterm/internal/shellcmd"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// Projects sidebar, the mock's #projectsSidebar.

// The sidebar lists the projects and, under each, its sessions (terminal
// tabs) with what they are doing, as Kero's and Rex's do.

func (a *appState) sidebarWidth() float32 {
	return max(200, min(a.sidebarW, 360))
}

// sidebar slides in and out; while shown it is resizable by its edge.
func (a *appState) sidebar(c *ui.Context) {
	box := ui.Row(c.Key("sidebar")).FillHeight().Shrink(0).ClipX()
	target := float32(0)
	if a.sidebarVisible {
		target = a.sidebarWidth()
	}
	w := box.AnimateTo("w", target, ui.Animation{Spring: ui.SpringOptions{Duration: 260 * time.Millisecond}})
	if a.settings.ReduceMotion {
		w = target
	}
	box.Width(w)
	if w < 1 {
		return
	}
	box.Children(func() {
		ui.Column(c).Width(a.sidebarWidth()).Shrink(0).FillHeight().Children(func() {
			a.sidebarHeader(c)
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Column(c).Padding(2, 6, 8, 8).Children(func() {
					if len(a.projects) == 0 {
						a.sidebarEmpty(c)
					}
					for i := range a.projects {
						a.projectGroup(c, i)
					}
					for i := range a.unavailable {
						a.unavailableProject(c, i)
					}
				})
			})
			a.sidebarFooter(c)
		})
		// The edge: dragged, it resizes the sidebar.
		edge := ui.Box(c.Key("edge")).Width(6).FillHeight().Shrink(0).Cursor(ui.CursorResizeColumn).KeepFocus()
		if dx, _, ok := edge.Dragged(); ok {
			a.sidebarW = max(200, min(a.sidebarW+dx, 360))
		}
	})
}

func (a *appState) sidebarHeader(c *ui.Context) {
	ui.Row(c).Height(40).Shrink(0).AlignItems(ui.Center).Padding(0, 4, 0, 8).Gap(2).Children(func() {
		if cmdButton(c, iconPanelLeftClose, 15, false).Tooltip("Hide Sidebar (" + keyLabels("view.toggleSidebar") + ")").Clicked() {
			a.sidebarVisible = false
		}
		ui.Text(c, "Projects").FontSize(11).FontWeight(600).TextColor(pal.TextFaint).Padding(0, 4).SingleLine().Grow(1)
		if cmdButton(c, iconPlus, 15, false).Tooltip("New Session (" + keyLabels("terminal.new") + ")").Clicked() {
			a.newTerminalTab()
		}
		if cmdButton(c, iconFolderPlus, 15, false).Tooltip("Open Project (" + keyLabels("app.openProject") + ")").Clicked() {
			a.openProjectDialog()
		}
	})
}

func (a *appState) sidebarEmpty(c *ui.Context) {
	ui.Column(c).Padding(24, 8).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Icon(c, iconFolderPlus).Size(22, 22).TextColor(pal.Accent)
		ui.Text(c, "No projects yet").FontSize(12.5).FontWeight(600).TextColor(pal.Text)
		ui.Text(c, "Open a folder to get started").FontSize(11.5).TextColor(pal.TextMuted)
		b := ui.ButtonBase(c).Radius(7).Padding(6, 14).Margin(4, 0, 0, 0)
		face(b, pal.Raised)
		if b.Hovered() {
			b.Background(pal.Hover.Over(pal.Raised))
		}
		b.Children(func() {
			ui.Text(c, "Open Project").FontSize(12).TextColor(pal.Text)
		})
		if b.Clicked() {
			a.openProjectDialog()
		}
	})
}

// unavailableProject is a project whose folder is missing: its sessions
// are kept, and shown when the folder is back, at the next launch.
func (a *appState) unavailableProject(c *ui.Context, i int) {
	p := a.unavailable[i]
	n := len(a.saved[p.ID])
	tip := "Folder missing: " + p.Dir + ". Its " + plural(n, "session") + " are kept and come back with it."
	h := ui.Row(c.Key("missing-"+p.ID)).Height(28).Padding(0, 6).Gap(6).AlignItems(ui.Center).Radius(7).
		Margin(6, 0, 0, 0).Tooltip(tip)
	if h.Hovered() {
		h.Background(pal.Hover)
	}
	h.Children(func() {
		ui.Icon(c, iconFolder).Size(12, 12).TextColor(pal.TextFaint)
		ui.Text(c, p.Name).FontSize(12.5).FontWeight(600).TextColor(pal.TextFaint).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		ui.Text(c, "folder missing").FontSize(11).TextColor(pal.TextFaint).SingleLine().Shrink(0)
	})
	h.ContextMenu(func(m *ui.Menu) {
		if m.Item("Remove Project…").Chosen() {
			var panes []string
			for _, s := range a.saved[p.ID] {
				for _, pn := range s.Panes {
					panes = append(panes, pn.ID)
				}
			}
			id := p.ID
			a.askDanger("Remove Project", "Remove "+p.Name+" from the list?"+a.endingText(panes, 0), "Remove",
				func() { a.removeUnavailable(id) })
		}
	})
}

// removeUnavailable forgets a project whose folder is missing, and ends
// its sessions.
func (a *appState) removeUnavailable(id string) {
	for _, s := range a.saved[id] {
		for _, p := range s.Panes {
			a.closePaneRuntime(p.ID) // its scrollback on disk too
		}
	}
	delete(a.saved, id)
	for i, p := range a.unavailable {
		if p.ID == id {
			a.unavailable = append(a.unavailable[:i], a.unavailable[i+1:]...)
			break
		}
	}
	a.saveSoon()
}

// projectGroup is a project's header and, unless it is collapsed, its
// sessions.
func (a *appState) projectGroup(c *ui.Context, i int) {
	p := &a.projects[i]
	key := p.ID
	if key == "" {
		key = strconv.Itoa(i)
	}
	// Keys are scoped by parent: each project's rows sit in a column of
	// its own.
	ui.Column(c.Key("proj-" + key)).Children(func() { a.projectGroupBody(c, i) })
}

func (a *appState) projectGroupBody(c *ui.Context, i int) {
	p := &a.projects[i]
	tabs := a.projectTabs(p.ID)
	h := ui.Row(c.Key("head")).Height(28).Padding(0, 6).Gap(6).AlignItems(ui.Center).Radius(7).Margin(6, 0, 0, 0).Cursor(ui.CursorPointer)
	hovered := h.Hovered()
	if hovered {
		h.Background(pal.Hover)
	}
	h.Children(func() {
		chev := ui.Icon(c, iconChevronDown).Size(12, 12).TextColor(pal.TextFaint)
		if p.Collapsed {
			chev.Rotate(-90)
		}
		ui.Text(c, p.Name).FontSize(12.5).FontWeight(600).TextColor(pal.TextMuted).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		if p.Collapsed {
			if lead, ok := a.projectLead(tabs); ok && statusState(lead) != iconNone {
				a.statusIcon(c, lead, 11)
			}
		}
		if hovered {
			if cmdButton(c, iconPlus, 13, false).Tooltip("New Session in " + p.Name).Clicked() {
				a.switchProject(i)
				a.newTerminalTab()
			}
		}
	})
	if h.Clicked() {
		if i == a.active {
			p.Collapsed = !p.Collapsed
			a.saveSoon()
		} else {
			a.switchProject(i)
		}
	}
	h.ContextMenu(func(m *ui.Menu) {
		if m.Item("New Session").Chosen() {
			a.switchProject(i)
			a.newTerminalTab()
		}
		label := "Collapse"
		if p.Collapsed {
			label = "Expand"
		}
		if m.Item(label).Chosen() {
			p.Collapsed = !p.Collapsed
			a.saveSoon()
		}
		if m.Item("Remove Project…").Chosen() {
			a.pendingRemoveProjectID = p.ID
			a.pendingRemoveProjectName = p.Name
			open := true
			a.removeProjectConfirmOpen = &open
		}
	})
	if p.Collapsed {
		return
	}
	n := 0
	for ti, t := range tabs {
		if t.Kind != tabTerminal {
			continue
		}
		n++
		a.sessionRow(c, i, ti, t)
	}
	if n == 0 {
		ui.Text(c, "No sessions").FontSize(11.5).TextColor(pal.TextFaint).Padding(4, 0, 4, 28)
	}
}

// projectLead is the session of a project that needs the user most.
func (a *appState) projectLead(tabs []topTab) (session.Info, bool) {
	var infos []session.Info
	for _, t := range tabs {
		if i, ok := a.sessionInfo(t); ok {
			infos = append(infos, i)
		}
	}
	return session.Lead(infos)
}

// sessionRow is a session in the sidebar: what it runs, its name, the
// branch when it is not the project's, how long since it changed, and its
// status.
func (a *appState) sessionRow(c *ui.Context, pi, ti int, t topTab) {
	p := &a.projects[pi]
	info, _ := a.sessionInfo(t)
	selected := pi == a.active && ti == a.activeTab && a.activeView == tabTerminal
	unread := info.Unread || info.Status == session.Waiting && !selected
	row := ui.Row(c.Key("s-"+t.ID)).Height(32).Radius(8).Padding(0, 8, 0, 22).Gap(8).AlignItems(ui.Center).
		Margin(0, 0, 1, 0).Cursor(ui.CursorPointer)
	bg := ui.Color{}
	switch {
	case selected:
		bg = pal.RowSelected
	case row.Hovered():
		bg = pal.Hover
	}
	row.Background(a.animColor(row, "bg", bg, 110*time.Millisecond))
	if selected {
		chosen(row)
	}
	row.Children(func() {
		ui.Icon(c, programIcon(firstNonEmpty(info.Agent, info.Process, info.Runtime))).Size(14, 14).TextColor(pal.TextFaint)
		if a.renaming == t.ID {
			in := ui.TextInputBase(c.Key("rename"), &a.renameText).FontSize(13).Grow(1).MinWidth(0).AutoFocus()
			focused := in.Focused()
			// Enter, or a click away within the window, takes the name;
			// Escape, or nothing typed, leaves it. A click away dropped
			// what was typed.
			switch {
			case c.Shortcut(0, ui.KeyEscape):
				a.renaming, a.renameFocused = "", false
			case in.Submitted(), a.renameFocused && !focused && (a.win == nil || a.win.IsFocused()):
				a.renameSession(pi, t.ID, a.renameText)
				a.renaming, a.renameFocused = "", false
			case focused:
				a.renameFocused = true
			}
			return
		}
		name := ui.Text(c, a.sessionName(t, info)).FontSize(13).TextColor(pal.Text).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		switch {
		case unread:
			name.FontWeight(650).TextColor(pal.TextStrong)
		case info.Status == session.Exited:
			name.TextColor(pal.TextMuted)
		}
		if g, ok := a.git.of(info.WorkDir()); ok && g.Branch != "" && g.Branch != p.Branch && !strings.HasPrefix(p.Branch, g.Branch) {
			ui.Text(c, g.Branch).FontSize(11.5).TextColor(pal.TextFaint).SingleLine().Ellipsis("…").Shrink(2).MinWidth(0)
		}
		ui.Spacer(c)
		if info.Since > 0 {
			ui.Text(c, ago(nowFunc().Sub(time.UnixMilli(info.Since)))).FontSize(11.5).TextColor(pal.TextFaint).SingleLine().Shrink(0)
		}
		a.statusIcon(c, info, 14)
	})
	if tip := a.sessionTooltip(info); tip != "" {
		row.Tooltip(tip)
	}
	if row.DoubleClicked() {
		a.renaming, a.renameText = t.ID, t.Label
	} else if row.Clicked() {
		a.focusSession(pi, t.ID)
	}
	row.ContextMenu(func(m *ui.Menu) {
		if m.Item("Rename").Chosen() {
			a.focusSession(pi, t.ID)
			a.renaming, a.renameText = t.ID, t.Label
		}
		if m.Item("Copy Working Directory").Chosen() {
			mygo.Clipboard.WriteText(info.WorkDir())
			c.Toast("Copied")
		}
		if m.Item("Close Session").Chosen() {
			a.focusSession(pi, t.ID)
			a.closeTabAsking(a.activeTab)
		}
	})
}

// sessionName is what a session is called: the name the user gave it, or
// what runs in it.
func (a *appState) sessionName(t topTab, info session.Info) string {
	if t.Label != "" && !autoLabel(t.Label) {
		return t.Label
	}
	if info.ID != "" {
		return paneTitle(info, nil)
	}
	if t.Label != "" {
		return t.Label
	}
	return "Terminal"
}

// autoLabel tells a tab label the app gave, the shell's name, from one the
// user did.
func autoLabel(l string) bool {
	return l == "Terminal" || l == shellcmd.Name(shellcmd.Login())
}

func (a *appState) sessionTooltip(i session.Info) string {
	var lines []string
	if i.Message != "" {
		lines = append(lines, i.Message)
	}
	where := shortDir(i.WorkDir())
	if prog := programName(i.Process, i.Agent); prog != "" {
		where = prog + " · " + where
	}
	if where != "" {
		lines = append(lines, where)
	}
	if g, ok := a.git.of(i.WorkDir()); ok && g.Branch != "" {
		s := g.Branch
		if g.Files > 0 {
			s += " · " + strconv.Itoa(g.Files) + " files +" + strconv.Itoa(g.Added) + " −" + strconv.Itoa(g.Removed)
		}
		lines = append(lines, s)
	}
	return strings.Join(lines, "\n")
}

// ago writes how long ago, as a sidebar does: now, 3m, 2h, 4d, 1w.
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	case d < 7*24*time.Hour:
		return strconv.Itoa(int(d.Hours()/24)) + "d"
	}
	return strconv.Itoa(int(d.Hours()/24/7)) + "w"
}

// focusSession shows a session: its project, and its tab.
func (a *appState) focusSession(pi int, tabID string) {
	if pi != a.active {
		a.switchProject(pi)
	}
	for i, t := range a.tabs {
		if t.ID == tabID {
			a.activateTab(i)
			return
		}
	}
}

// renameSession names a session; an empty name gives it back its automatic
// one.
func (a *appState) renameSession(pi int, tabID, name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = shellcmd.Name(shellcmd.Login())
	}
	tabs := a.projectTabs(a.projects[pi].ID)
	for i := range tabs {
		if tabs[i].ID == tabID {
			// The user's name: an agent's next conversation must not
			// rename it back.
			tabs[i].Label, tabs[i].AgentNamed = name, false
		}
	}
	a.saveSoon()
}

// sidebarFooter holds the settings, open-project and palette buttons, and
// chips that count the sessions needing you, working, and done.
func (a *appState) sidebarFooter(c *ui.Context) {
	waiting, working, done := a.statusCounts()
	ui.Row(c).Height(44).Shrink(0).AlignItems(ui.Center).Padding(0, 6, 6, 8).Gap(4).Children(func() {
		if cmdButton(c, iconSettings, 15, false).Tooltip("Settings (" + keyLabels("app.settings") + ")").Clicked() {
			a.openSettings("")
		}
		if cmdButton(c, iconFolderPlus, 15, false).Tooltip("Open Project (" + keyLabels("app.openProject") + ")").Clicked() {
			a.openProjectDialog()
		}
		if cmdButton(c, iconSearch, 15, false).Tooltip("Command Palette (" + keyLabels("app.commandPalette") + ")").Clicked() {
			a.openOverlay(overlayCommandPalette)
		}
		ui.Spacer(c)
		// The chips that fit, most urgent first: the footer once ran past
		// the sidebar, cutting the last one. A chip is about 34 px and 7 a
		// digit; the three buttons and the padding take about 124.
		room := a.sidebarWidth() - 124
		chip := func(key string, n int, st session.Info, attention bool, next func() bool) {
			if n == 0 {
				return
			}
			need := float32(34 + 7*len(strconv.Itoa(n)) + 4)
			if need > room {
				return
			}
			room -= need
			e := ui.Row(c.Key(key)).Height(24).Radius(12).Padding(0, 8, 0, 6).Gap(5).AlignItems(ui.Center).Cursor(ui.CursorPointer)
			fg, bg := pal.TextMuted, pal.Chip
			if attention {
				fg, bg = pal.Attention, pal.AttentionSoft
			}
			e.Background(bg).Children(func() {
				a.statusIcon(c, st, 12)
				ui.Text(c, strconv.Itoa(n)).FontSize(12).FontWeight(500).TextColor(fg).SingleLine()
			})
			if e.Clicked() {
				next()
			}
		}
		chip("needs", waiting, session.Info{Status: session.Waiting, Progress: -1}, true, func() bool { return a.jumpTo(iconWaiting) })
		chip("working", working, session.Info{Status: session.Working, Progress: -1}, false, func() bool { return a.jumpTo(iconWorking) })
		chip("done", done, session.Info{Status: session.Idle, Unread: true, Progress: -1}, false, func() bool { return a.jumpTo(iconDone) })
	})
}

// statusCounts counts the sessions of every project that wait for the
// user, work, and finished unseen.
func (a *appState) statusCounts() (waiting, working, done int) {
	for _, p := range a.projects {
		for _, t := range a.projectTabs(p.ID) {
			info, ok := a.sessionInfo(t)
			if !ok {
				continue
			}
			switch statusState(info) {
			case iconWaiting:
				waiting++
			case iconWorking, iconProgress:
				working++
			case iconDone:
				done++
			}
		}
	}
	return
}

// jumpTo shows the next session, after the one shown, in sidebar order,
// whose icon is st (working counts progress too). It reports whether
// there was one.
func (a *appState) jumpTo(st ...iconState) bool {
	type ref struct {
		pi    int
		id    string
		match bool
	}
	var all []ref
	cur := -1
	for pi, p := range a.projects {
		for ti, t := range a.projectTabs(p.ID) {
			if t.Kind != tabTerminal {
				continue
			}
			if pi == a.active && ti == a.activeTab {
				cur = len(all)
			}
			info, _ := a.sessionInfo(t)
			s := statusState(info)
			if s == iconProgress {
				s = iconWorking
			}
			match := false
			for _, want := range st {
				match = match || s == want
			}
			all = append(all, ref{pi, t.ID, match})
		}
	}
	for k := 1; k <= len(all); k++ {
		r := all[(cur+k+len(all))%len(all)]
		if r.match {
			a.focusSession(r.pi, r.id)
			return true
		}
	}
	return false
}

// openProjectDialog shows a native folder picker and adds the chosen folder
// as a project, or switches to it when already listed.
func (a *appState) openProjectDialog() {
	if a.stateLoadFailed != "" {
		a.toast(nil, errStateUnreadable.Error())
		return
	}
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
				ID:   newID(),
				Name: filepath.Base(dir),
				Path: dir,
			})
			a.setProject(len(a.projects) - 1)
		})
	}()
}
