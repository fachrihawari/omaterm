package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"oterm/service"
)

// Command is one command-palette entry. Its keys come from the binding
// table in keybindings.go, so the palette, the window shortcuts and the
// Keyboard Shortcuts overlay cannot drift apart.
type Command struct {
	ID       string
	Title    string
	Category string
	Enabled  func(a *appState) bool
	Run      func(a *appState, c *ui.Context)
}

// commandRegistry lists all commands. Commands whose Enabled returns false
// are shown dimmed and do not run.
var commandRegistry = []Command{
	{ID: "file.quickOpen", Title: "Go to File…", Category: "File",
		Run: func(a *appState, c *ui.Context) { a.openOverlay(overlayQuickOpen) }},
	{ID: "app.commandPalette", Title: "Command Palette…", Category: "App",
		Run: func(a *appState, c *ui.Context) { a.openOverlay(overlayCommandPalette) }},
	{ID: "help.keyboardShortcuts", Title: "Keyboard Shortcuts", Category: "Help",
		Run: func(a *appState, c *ui.Context) { a.openOverlay(overlayShortcuts) }},
	{ID: "search.global", Title: "Search in Files…", Category: "Search",
		Run: func(a *appState, c *ui.Context) { a.openOverlay(overlayGlobalSearch) }},
	{ID: "app.openProject", Title: "Open Project…", Category: "App",
		Run: func(a *appState, c *ui.Context) { a.openProjectDialog() }},
	{ID: "app.quit", Title: "Quit oterm", Category: "App",
		Run: func(a *appState, c *ui.Context) { mygo.App.Quit() }},

	{ID: "view.toggleSidebar", Title: "Toggle Sidebar", Category: "View",
		Run: func(a *appState, c *ui.Context) { a.sidebarVisible = !a.sidebarVisible }},
	{ID: "view.toggleInspector", Title: "Toggle Inspector", Category: "View",
		Run: func(a *appState, c *ui.Context) { a.inspectorVisible = !a.inspectorVisible }},
	{ID: "view.terminal", Title: "Show Terminal", Category: "View",
		Run: func(a *appState, c *ui.Context) { a.activateKind(tabTerminal) }},
	{ID: "view.toggleTerminal", Title: "Toggle Terminal", Category: "View",
		Run: func(a *appState, c *ui.Context) { a.toggleTerminal() }},
	{ID: "view.editor", Title: "Show Editor", Category: "View",
		Run: func(a *appState, c *ui.Context) { a.activateKind(tabEditor) }},
	{ID: "view.diff", Title: "Show Diff", Category: "View",
		Run: func(a *appState, c *ui.Context) { a.activateKind(tabDiff) }},
	{ID: "view.files", Title: "Show Files", Category: "View",
		Run: func(a *appState, c *ui.Context) { a.showInspectorTab(1) }},
	{ID: "view.git", Title: "Show Source Control", Category: "View",
		Run: func(a *appState, c *ui.Context) { a.showInspectorTab(2) }},
	{ID: "view.gitGraph", Title: "Open Git Graph", Category: "View",
		Enabled: func(a *appState) bool { return a.activeRepoRef() != nil },
		Run:     func(a *appState, c *ui.Context) { a.openGraphTab() }},

	{ID: "tab.next", Title: "Next Tab", Category: "Tab",
		Run: func(a *appState, c *ui.Context) { a.cycleTab(1) }},
	{ID: "tab.prev", Title: "Previous Tab", Category: "Tab",
		Run: func(a *appState, c *ui.Context) { a.cycleTab(-1) }},
	{ID: "tab.close", Title: "Close Tab", Category: "Tab",
		Run: func(a *appState, c *ui.Context) { a.closeActiveTab(c) }},

	{ID: "file.save", Title: "Save File", Category: "File",
		Enabled: func(a *appState) bool {
			doc := a.activeEditor()
			return doc != nil && doc.State != nil && doc.State.Buffer.Modified()
		},
		Run: func(a *appState, c *ui.Context) {
			if doc := a.activeEditor(); doc != nil && doc.State != nil {
				a.saveEditor(doc, c)
			}
		}},
	{ID: "markdown.togglePreview", Title: "Markdown: Toggle Preview", Category: "File",
		Enabled: func(a *appState) bool {
			doc := a.activeEditor()
			return doc != nil && (hasSuffix(doc.Path, ".md") || hasSuffix(doc.Path, ".mdx"))
		},
		Run: func(a *appState, c *ui.Context) {
			if doc := a.activeEditor(); doc != nil {
				doc.Preview = !doc.Preview
			}
		}},

	{ID: "terminal.new", Title: "New Terminal Tab", Category: "Terminal",
		Run: func(a *appState, c *ui.Context) { a.newTerminalTab() }},
	{ID: "terminal.splitDown", Title: "Split Terminal Down", Category: "Terminal",
		Enabled: func(a *appState) bool { return a.activeView == tabTerminal },
		Run:     func(a *appState, c *ui.Context) { a.splitActivePane(c, splitHorizontal) }},
	{ID: "terminal.splitRight", Title: "Split Terminal Right", Category: "Terminal",
		Enabled: func(a *appState) bool { return a.activeView == tabTerminal },
		Run:     func(a *appState, c *ui.Context) { a.splitActivePane(c, splitVertical) }},
	{ID: "terminal.closePane", Title: "Close Terminal Pane", Category: "Terminal",
		Enabled: func(a *appState) bool { return a.activeView == tabTerminal },
		Run:     func(a *appState, c *ui.Context) { a.closeActivePane(c) }},
	{ID: "terminal.focusLeft", Title: "Focus Pane Left", Category: "Terminal",
		Enabled: func(a *appState) bool { return a.activeView == tabTerminal },
		Run:     func(a *appState, c *ui.Context) { a.focusPane(paneLeft) }},
	{ID: "terminal.focusRight", Title: "Focus Pane Right", Category: "Terminal",
		Enabled: func(a *appState) bool { return a.activeView == tabTerminal },
		Run:     func(a *appState, c *ui.Context) { a.focusPane(paneRight) }},
	{ID: "terminal.focusUp", Title: "Focus Pane Up", Category: "Terminal",
		Enabled: func(a *appState) bool { return a.activeView == tabTerminal },
		Run:     func(a *appState, c *ui.Context) { a.focusPane(paneUp) }},
	{ID: "terminal.focusDown", Title: "Focus Pane Down", Category: "Terminal",
		Enabled: func(a *appState) bool { return a.activeView == tabTerminal },
		Run:     func(a *appState, c *ui.Context) { a.focusPane(paneDown) }},

	{ID: "git.refresh", Title: "Git: Refresh", Category: "Git",
		Enabled: func(a *appState) bool { return a.activeRepoRef() != nil },
		Run:     func(a *appState, c *ui.Context) { a.refreshActiveRepo(); c.Toast("Git status refreshed") }},
	{ID: "git.stageAll", Title: "Git: Stage All Changes", Category: "Git",
		Enabled: func(a *appState) bool { r := a.activeRepoRef(); return r != nil && len(r.Changes) > 0 },
		Run:     func(a *appState, c *ui.Context) { a.gitStageAll(c) }},
	{ID: "git.unstageAll", Title: "Git: Unstage All Changes", Category: "Git",
		Enabled: func(a *appState) bool { r := a.activeRepoRef(); return r != nil && len(r.Staged) > 0 },
		Run:     func(a *appState, c *ui.Context) { a.gitUnstageAll(c) }},
	{ID: "git.commit", Title: "Git: Commit Staged", Category: "Git",
		Enabled: func(a *appState) bool { r := a.activeRepoRef(); return r != nil && len(r.Staged) > 0 },
		Run: func(a *appState, c *ui.Context) {
			a.showInspectorTab(2)
			if a.commitReady() {
				a.gitCommit(c)
			}
		}},
	{ID: "git.stashPush", Title: "Git: Stash Changes…", Category: "Git",
		Enabled: func(a *appState) bool { r := a.activeRepoRef(); return r != nil && len(r.Changes)+len(r.Staged) > 0 },
		Run:     func(a *appState, c *ui.Context) { a.showInspectorTab(2) }},
	{ID: "git.stashPop", Title: "Git: Pop Latest Stash", Category: "Git",
		Enabled: func(a *appState) bool { r := a.activeRepoRef(); return r != nil && len(r.Stashes) > 0 },
		Run:     func(a *appState, c *ui.Context) { a.gitStashPop(c, 0) }},
	{ID: "git.switchBranch", Title: "Git: Switch Branch…", Category: "Git",
		Enabled: func(a *appState) bool { return a.activeRepoRef() != nil },
		Run:     func(a *appState, c *ui.Context) { a.openBranchOverlay() }},
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

// splitActivePane splits the focused pane and gives the new one the
// keyboard, as VS Code's split does.
func (a *appState) splitActivePane(c *ui.Context, dir splitDir) {
	a.splitActivePaneAt(c, a.activeSplitLeaf(), dir)
}

// splitActivePaneAt splits pane termID, which a pane's own button names.
func (a *appState) splitActivePaneAt(c *ui.Context, termID string, dir splitDir) {
	if termID == "" {
		return
	}
	id := a.splitPane(termID, dir)
	if id == "" {
		return
	}
	a.activePaneTermID = id
	a.paneFocus = id
	if dir == splitHorizontal {
		c.Toast("Split down")
	} else {
		c.Toast("Split right")
	}
}

// closeActivePane closes the focused pane, and its tab when it was the
// last one.
func (a *appState) closeActivePane(c *ui.Context) {
	leaf := a.activeSplitLeaf()
	if leaf == "" {
		return
	}
	root := a.activeSplit()
	if root == nil {
		return
	}
	if root.isLeaf() {
		a.closeActiveTab(c)
		return
	}
	a.removePane(leaf)
	if ids := a.activeSplit().termIDs(); len(ids) > 0 {
		a.activePaneTermID = ids[0]
		a.paneFocus = ids[0]
	}
	c.Toast("Pane closed")
}

// cycleTab moves the active tab by d, wrapping around.
func (a *appState) cycleTab(d int) {
	n := len(a.tabs)
	if n == 0 {
		return
	}
	a.activateTab((a.activeTab + d + n) % n)
}

// toggleTerminal shows the terminal from another view, and returns to that
// view from the terminal, as VS Code's Ctrl+` shows and hides the panel.
func (a *appState) toggleTerminal() {
	if a.activeView == tabTerminal {
		kind := a.prevView
		if kind == tabTerminal {
			kind = tabEditor
		}
		a.activateKind(kind)
		return
	}
	a.prevView = a.activeView
	a.activateKind(tabTerminal)
}
