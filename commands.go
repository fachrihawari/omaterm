package main

import (
	"github.com/egoist/mygo/ui"
	"oterm/service"
)

// Command is one command-palette entry.
type Command struct {
	ID       string
	Title    string
	Category string
	Keys     string // display label, e.g. "Ctrl+P"
	Enabled  func(a *appState) bool
	Run      func(a *appState, c *ui.Context)
}

// commandRegistry lists all commands. Commands whose Enabled returns false
// are shown dimmed and do not run.
var commandRegistry = []Command{
	{ID: "file.quickOpen", Title: "Go to File…", Category: "File", Keys: "Ctrl+P",
		Run: func(a *appState, c *ui.Context) { a.openOverlay(overlayQuickOpen) }},
	{ID: "app.commandPalette", Title: "Command Palette…", Category: "App", Keys: "Ctrl+Shift+P",
		Run: func(a *appState, c *ui.Context) { a.openOverlay(overlayCommandPalette) }},
	{ID: "search.global", Title: "Search in Files…", Category: "Search", Keys: "Ctrl+Shift+F",
		Run: func(a *appState, c *ui.Context) { a.openOverlay(overlayGlobalSearch) }},

	{ID: "view.toggleSidebar", Title: "Toggle Sidebar", Category: "View", Keys: "Ctrl+B",
		Run: func(a *appState, c *ui.Context) { a.sidebarVisible = !a.sidebarVisible }},
	{ID: "view.toggleInspector", Title: "Toggle Inspector", Category: "View", Keys: "Ctrl+Shift+B",
		Run: func(a *appState, c *ui.Context) { a.inspectorVisible = !a.inspectorVisible }},
	{ID: "view.terminal", Title: "Show Terminal", Category: "View", Keys: "Ctrl+1",
		Run: func(a *appState, c *ui.Context) { a.activeView = tabTerminal }},
	{ID: "view.editor", Title: "Show Editor", Category: "View", Keys: "Ctrl+2",
		Run: func(a *appState, c *ui.Context) { a.activeView = tabEditor }},
	{ID: "view.diff", Title: "Show Diff", Category: "View", Keys: "Ctrl+3",
		Run: func(a *appState, c *ui.Context) { a.activeView = tabDiff }},
	{ID: "view.gitGraph", Title: "Open Git Graph", Category: "View",
		Enabled: func(a *appState) bool { return a.activeRepoRef() != nil },
		Run:     func(a *appState, c *ui.Context) { a.openGraphTab() }},

	{ID: "terminal.new", Title: "New Terminal Tab", Category: "Terminal", Keys: "Ctrl+T",
		Run: func(a *appState, c *ui.Context) { a.newTerminalTab() }},

	{ID: "git.refresh", Title: "Git: Refresh", Category: "Git",
		Enabled: func(a *appState) bool { return a.activeRepoRef() != nil },
		Run:     func(a *appState, c *ui.Context) { a.refreshActiveRepo(); c.Toast("Git status refreshed") }},
	{ID: "git.stageAll", Title: "Git: Stage All Changes", Category: "Git",
		Enabled: func(a *appState) bool { r := a.activeRepoRef(); return r != nil && len(r.Changes) > 0 },
		Run:     func(a *appState, c *ui.Context) { a.gitStageAll(c) }},
	{ID: "git.unstageAll", Title: "Git: Unstage All Changes", Category: "Git",
		Enabled: func(a *appState) bool { r := a.activeRepoRef(); return r != nil && len(r.Staged) > 0 },
		Run:     func(a *appState, c *ui.Context) { a.gitUnstageAll(c) }},
	{ID: "git.commit", Title: "Git: Commit Staged…", Category: "Git",
		Enabled: func(a *appState) bool { r := a.activeRepoRef(); return r != nil && len(r.Staged) > 0 },
		Run:     func(a *appState, c *ui.Context) { a.inspectorTab = 2; a.inspectorVisible = true }},
	{ID: "git.stashPush", Title: "Git: Stash Changes…", Category: "Git",
		Enabled: func(a *appState) bool { r := a.activeRepoRef(); return r != nil && len(r.Changes)+len(r.Staged) > 0 },
		Run:     func(a *appState, c *ui.Context) { a.inspectorTab = 2; a.inspectorVisible = true }},
	{ID: "git.stashPop", Title: "Git: Pop Latest Stash", Category: "Git",
		Enabled: func(a *appState) bool { r := a.activeRepoRef(); return r != nil && len(r.Stashes) > 0 },
		Run:     func(a *appState, c *ui.Context) { a.gitStashPop(c, 0) }},
}

// gitStageAll stages everything in the active repo.
func (a *appState) gitStageAll(c *ui.Context) {
	r := a.activeRepoRef()
	if r == nil {
		return
	}
	if err := service.StageAll(r.Root); err != nil {
		c.Toast("Git error: " + err.Error())
		return
	}
	a.refreshActiveRepo()
	c.Toast("Staged all changes")
}

func (a *appState) gitUnstageAll(c *ui.Context) {
	r := a.activeRepoRef()
	if r == nil {
		return
	}
	if err := service.UnstageAll(r.Root); err != nil {
		c.Toast("Git error: " + err.Error())
		return
	}
	a.refreshActiveRepo()
	c.Toast("Unstaged all changes")
}
