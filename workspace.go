package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"omaterm/internal/agent"
	"omaterm/internal/model"
	"omaterm/internal/shellcmd"
)

// projectWS is a project's workspace while another project shows: its
// tabs (and so its sessions, whose panes keep running), the tab and pane
// it showed, and its open editors.
type projectWS struct {
	tabs       []topTab
	activeTab  int
	activeView topTabKind
	activePane string
	editors    map[string]*openFile
}

// stashWorkspace keeps the shown project's workspace for its return.
func (a *appState) stashWorkspace() {
	if a.wsOwner == "" {
		return
	}
	a.ws[a.wsOwner] = &projectWS{
		tabs: a.tabs, activeTab: a.activeTab, activeView: a.activeView,
		activePane: a.activePaneTermID, editors: a.editors,
	}
	a.wsOwner = ""
}

// restoreWorkspace shows p's workspace: the one it left, else its saved
// sessions, else a session per tab name it starts with.
func (a *appState) restoreWorkspace(p *project) {
	a.wsOwner = p.ID
	w := a.ws[p.ID]
	if w == nil {
		w = a.buildWorkspace(p)
	}
	delete(a.ws, p.ID)
	a.tabs, a.activeTab, a.activeView = w.tabs, w.activeTab, w.activeView
	a.activePaneTermID, a.editors = w.activePane, w.editors
	// Sessions may have closed while it was away (the omaterm command, a
	// shell that exited): never show a tab or pane that is gone.
	if a.activeTab >= len(a.tabs) {
		a.activeTab = len(a.tabs) - 1
	}
	if !w.hasPane(a.activePaneTermID) {
		a.activePaneTermID = ""
	}
}

// hasPane tells whether a pane is in one of the workspace's tabs.
func (w *projectWS) hasPane(id string) bool {
	if id == "" {
		return false
	}
	for _, t := range w.tabs {
		if t.Split != nil && t.Split.contains(id) {
			return true
		}
	}
	return false
}

// buildWorkspace makes the workspace of a project not yet shown this run:
// its saved sessions, else a session per tab name it starts with.
func (a *appState) buildWorkspace(p *project) *projectWS {
	w := &projectWS{editors: map[string]*openFile{}, activeView: tabTerminal}
	if saved := a.saved[p.ID]; len(saved) > 0 {
		delete(a.saved, p.ID)
		for _, s := range saved {
			split := fromLayout(s.Layout)
			if split == nil {
				continue
			}
			for _, pane := range s.Panes {
				a.restoring[pane.ID] = true
				if pane.Dir != "" {
					a.paneDirs[pane.ID] = pane.Dir
				}
				if pane.Agent != nil {
					a.paneAgents[pane.ID] = pane.Agent
				}
				if pane.URL != "" {
					a.paneURLs[pane.ID], a.paneTitles[pane.ID] = pane.URL, pane.Title
				}
			}
			w.tabs = append(w.tabs, topTab{ID: s.ID, Kind: tabTerminal, Label: s.Name, Dot: dotGreen, Split: split})
		}
	}
	if len(w.tabs) == 0 {
		names := p.Tabs
		if len(names) == 0 {
			names = []string{shellcmd.Name(shellcmd.Login())}
		}
		for _, name := range names {
			t := topTab{ID: newID(), Kind: tabTerminal, Label: name, Dot: dotGreen,
				Split: &splitNode{TermID: randomTermID()}}
			if name == "tests" {
				t.Dot = dotYellow
			}
			w.tabs = append(w.tabs, t)
		}
	}
	if len(w.tabs) > 0 && w.tabs[0].Split != nil {
		if ids := w.tabs[0].Split.termIDs(); len(ids) > 0 {
			w.activePane = ids[0]
		}
	}
	return w
}

// workspaceOf returns a project's workspace to change: the shown one's
// fields, through a.tabs, or the one it waits in, made if need be.
func (a *appState) workspaceTabs(p *project) *[]topTab {
	if p.ID == a.wsOwner {
		return &a.tabs
	}
	w := a.ws[p.ID]
	if w == nil {
		w = a.buildWorkspace(p)
		a.ws[p.ID] = w
	}
	return &w.tabs
}

// projectTabs returns a project's tabs, shown or not: a project not
// shown this run gets its workspace from its saved sessions, so the
// sidebar lists them; with none saved, it has none yet.
func (a *appState) projectTabs(id string) []topTab {
	if id == a.wsOwner {
		return a.tabs
	}
	if w := a.ws[id]; w != nil {
		return w.tabs
	}
	if len(a.saved[id]) > 0 {
		for i := range a.projects {
			if a.projects[i].ID == id {
				return *a.workspaceTabs(&a.projects[i])
			}
		}
	}
	return nil
}

// closeProjectPanes ends the sessions of a project.
func (a *appState) closeProjectPanes(id string) {
	for _, t := range a.projectTabs(id) {
		if t.Kind == tabTerminal && t.Split != nil {
			for _, pid := range t.Split.termIDs() {
				a.closePaneRuntime(pid)
			}
		}
	}
	delete(a.ws, id)
}

// loadState reads state.json: the projects, whose folders must still
// exist, and their sessions, which start as the projects show. It returns
// the index of the project to show.
func (a *appState) loadState() int {
	st, err := model.Load(stateFilePath())
	if model.Unreadable(err) {
		// Broken, as a crash mid-write or an editor can leave it: it holds
		// every session, so keep a copy, and never write over it or end
		// the sessions this run doesn't know.
		bad, berr := model.Backup(stateFilePath())
		if berr != nil {
			bad = stateFilePath()
		}
		a.stateLoadFailed = bad
		log.Printf("state.json can't be read (%v): kept as %s, and left alone", err, bad)
		return 0
	}
	if err != nil {
		// The rename moved the file: read the old one, so the project list
		// survives. The next save writes it to the new path.
		if st, err = model.Load(legacyStateFilePath()); err != nil {
			return 0
		}
	}
	a.projects = nil
	active := 0
	for _, p := range st.Projects {
		abs, err := filepath.Abs(p.Dir)
		if p.Dir == "" || err != nil {
			continue
		}
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			// Its folder is gone for now (an unplugged drive, a rename):
			// keep it and its sessions, whose panes go on running.
			p.Dir = abs
			a.unavailable = append(a.unavailable, p)
			continue
		}
		if p.ID == "" {
			p.ID = newID()
		}
		if p.ID == st.ActiveProject {
			active = len(a.projects)
		}
		a.projects = append(a.projects, project{ID: p.ID, Name: p.Name, Path: abs, Collapsed: p.Collapsed})
	}
	for _, s := range st.Sessions {
		a.saved[s.Project] = append(a.saved[s.Project], s)
	}
	return active
}

// state is what to save of the projects and sessions.
func (a *appState) state() model.State {
	st := model.State{}
	if p := a.project(); p != nil {
		st.ActiveProject = p.ID
	}
	// Projects whose folder is missing, as they were saved.
	for _, p := range a.unavailable {
		st.Projects = append(st.Projects, p)
		st.Sessions = append(st.Sessions, a.saved[p.ID]...)
	}
	for _, p := range a.projects {
		abs, err := filepath.Abs(p.Path)
		if err != nil {
			abs = p.Path
		}
		st.Projects = append(st.Projects, model.Project{ID: p.ID, Name: p.Name, Dir: abs, Collapsed: p.Collapsed})
		tabs := a.projectTabs(p.ID)
		if tabs == nil {
			// Never shown this run: its sessions as they were saved.
			st.Sessions = append(st.Sessions, a.saved[p.ID]...)
			continue
		}
		for _, t := range tabs {
			if t.Kind != tabTerminal || t.Split == nil {
				continue
			}
			s := model.Session{ID: t.ID, Project: p.ID, Name: t.Label, Layout: toLayout(t.Split)}
			for _, id := range t.Split.termIDs() {
				dir := a.paneDirs[id]
				if info, ok := a.status[id]; ok && info.Dir != "" {
					dir = info.Dir
				}
				if dir == "" {
					dir = abs
				}
				if url, ok := a.paneURLs[id]; ok {
					s.Panes = append(s.Panes, model.Pane{ID: id, URL: url, Title: a.paneTitles[id]})
					continue
				}
				pane := model.Pane{ID: id, Dir: dir}
				info, ok := a.status[id]
				switch ag := a.paneAgents[id]; {
				case ok && info.AgentSession != nil:
					pane.Agent = info.AgentSession
				case ok && info.Agent != "":
					// An agent whose hook never told its conversation: a
					// restart continues the last one of its folder.
					pane.Agent = &agent.Session{Agent: info.Agent}
				case ag != nil && a.panes.Get(id) == nil:
					pane.Agent = ag
				}
				s.Panes = append(s.Panes, pane)
			}
			st.Sessions = append(st.Sessions, s)
		}
	}
	return st
}

// saveSoon saves state.json shortly, once a burst of changes settles.
// Tests, which have no window, never write.
func (a *appState) saveSoon() {
	if a.win == nil || a.saveTimer != nil {
		return
	}
	a.saveTimer = time.AfterFunc(300*time.Millisecond, func() {
		a.post(func() {
			a.saveTimer = nil
			a.saveState()
		})
	})
}

// saveState writes state.json now.
func (a *appState) saveState() {
	if a.win == nil || a.stateLoadFailed != "" {
		return
	}
	if len(a.projects) == 0 && len(a.unavailable) == 0 {
		os.Remove(stateFilePath())
		return
	}
	model.Save(stateFilePath(), a.state())
}

// toLayout converts a split tree to the saved layout.
func toLayout(n *splitNode) *model.Layout {
	if n == nil {
		return nil
	}
	if n.isLeaf() {
		return &model.Layout{Pane: n.TermID}
	}
	r := n.Ratio
	if r <= 0 {
		r = 50
	}
	return &model.Layout{Row: n.Dir == splitVertical, Ratio: float64(r) / 100, A: toLayout(n.First), B: toLayout(n.Second)}
}

// fromLayout converts a saved layout to a split tree; nil for a broken
// one.
func fromLayout(l *model.Layout) *splitNode {
	if l == nil {
		return nil
	}
	if l.Pane != "" {
		return &splitNode{TermID: l.Pane}
	}
	a, b := fromLayout(l.A), fromLayout(l.B)
	if a == nil || b == nil {
		return a
	}
	dir := splitHorizontal
	if l.Row {
		dir = splitVertical
	}
	return &splitNode{Dir: dir, Ratio: float32(l.Ratio * 100), First: a, Second: b}
}
