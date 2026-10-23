package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/ui"

	"omaterm/internal/cli"
	"omaterm/internal/session"
	"omaterm/service"
)

// Command is one command-palette entry. Its keys come from the binding
// table in keybindings.go, so the palette, the window shortcuts and the
// Keyboard Shortcuts overlay cannot drift apart.
type Command struct {
	ID       string
	Title    string
	Category string
	// Group names a family of commands that differ only by a number, as
	// the tabs Alt+1 … Alt+9 go to. The Keyboard Shortcuts dialog shows
	// the family as one row titled Group, with the keys side by side:
	// one thing to remember, not nine.
	Group   string
	Enabled func(a *appState) bool
	Run     func(a *appState, c *ui.Context)
}

// commandRegistry lists all commands. Commands whose Enabled returns false
// are shown dimmed and do not run.
var commandRegistry = append([]Command{
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
	{ID: "app.quit", Title: "Quit omaterm", Category: "App",
		Run: func(a *appState, c *ui.Context) { mygo.App.Quit() }},
	{ID: "app.checkUpdates", Title: "Check for Updates…", Category: "App",
		Run: func(a *appState, c *ui.Context) { a.checkForUpdates(c) }},

	{ID: "app.zoomIn", Title: "Increase Font Size", Category: "View",
		Run: func(a *appState, c *ui.Context) { a.zoomFont(1) }},
	{ID: "app.zoomOut", Title: "Decrease Font Size", Category: "View",
		Run: func(a *appState, c *ui.Context) { a.zoomFont(-1) }},
	{ID: "app.zoomReset", Title: "Reset Font Size", Category: "View",
		Run: func(a *appState, c *ui.Context) { a.zoomFont(0) }},

	{ID: "cli.install", Title: "Install Command Line Tool", Category: "App",
		Run: func(a *appState, c *ui.Context) {
			if where, err := cli.InstallCLI(); err != nil {
				toast(c, "Could not install: "+err.Error())
			} else {
				toast(c, "Installed "+where)
			}
		}},
	{ID: "agents.setup", Title: "Set Up Agent Integration", Category: "App",
		Run: func(a *appState, c *ui.Context) { a.setupAgents(c, a.status[a.activeSplitLeaf()].Agent) }},
	{ID: "remote.toggle", Title: "Toggle Remote Control", Category: "App",
		Run: func(a *appState, c *ui.Context) {
			a.settings.RemoteControl = !a.settings.RemoteControl
			a.saveSettings()
			if a.settings.RemoteControl {
				toast(c, "Remote Control on: agents can drive the window and browser panes")
			} else {
				toast(c, "Remote Control off")
			}
		}},
	{ID: "daemon.restart", Title: "Restart Session Daemon", Category: "App",
		Run: func(a *appState, c *ui.Context) {
			if a.restartDaemon() {
				toast(c, "Restarting the session daemon…")
			} else {
				toast(c, "Sessions run in the app: the session daemon is off")
			}
		}},
	{ID: "theme.omarchy", Title: "Theme: Follow Omarchy", Category: "View",
		Run: func(a *appState, c *ui.Context) { a.setThemeMode(themeOmarchy) }},
	{ID: "theme.dark", Title: "Theme: Dark", Category: "View",
		Run: func(a *appState, c *ui.Context) { a.setThemeMode(themeDark) }},
	{ID: "theme.light", Title: "Theme: Light", Category: "View",
		Run: func(a *appState, c *ui.Context) { a.setThemeMode(themeLight) }},

	{ID: "view.toggleSidebar", Title: "Toggle Sidebar", Category: "View",
		Run: func(a *appState, c *ui.Context) { a.sidebarVisible = !a.sidebarVisible }},
	{ID: "view.toggleInspector", Title: "Toggle Inspector", Category: "View",
		Run: func(a *appState, c *ui.Context) { a.inspectorVisible = !a.inspectorVisible }},
	{ID: "view.terminal", Title: "Show Terminal", Category: "View",
		Run: func(a *appState, c *ui.Context) { a.activateKind(tabTerminal) }},
	{ID: "view.editor", Title: "Show Editor", Category: "View",
		Run: func(a *appState, c *ui.Context) { a.activateKind(tabEditor) }},
	{ID: "view.diff", Title: "Show Diff", Category: "View",
		Run: func(a *appState, c *ui.Context) { a.activateKind(tabDiff) }},
	{ID: "view.info", Title: "Show Info", Category: "View",
		Run: func(a *appState, c *ui.Context) { a.showInspectorTab(0) }},
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
	{ID: "session.attention", Title: "Go to Session That Needs You", Category: "Session",
		Run: func(a *appState, c *ui.Context) { a.jumpToAttention(c) }},
	{ID: "session.next", Title: "Next Session", Category: "Session",
		Run: func(a *appState, c *ui.Context) { a.cycleSession(1) }},
	{ID: "session.prev", Title: "Previous Session", Category: "Session",
		Run: func(a *appState, c *ui.Context) { a.cycleSession(-1) }},
	{ID: "browser.open", Title: "Open Browser Pane", Category: "Browser",
		Run: func(a *appState, c *ui.Context) { a.openBrowser("") }},
	{ID: "browser.focusAddress", Title: "Go to Address Bar", Category: "Browser",
		Enabled: func(a *appState) bool { return a.focusedWeb() != nil },
		Run:     func(a *appState, c *ui.Context) { a.focusedWeb().focusAddress = true }},
	{ID: "browser.reload", Title: "Reload Page", Category: "Browser",
		Enabled: func(a *appState) bool { return a.focusedWeb() != nil && a.focusedWeb().view != nil },
		Run:     func(a *appState, c *ui.Context) { a.focusedWeb().view.Page().Reload() }},
	{ID: "browser.back", Title: "Go Back", Category: "Browser",
		Enabled: func(a *appState) bool { return a.focusedWeb() != nil && a.focusedWeb().back },
		Run:     func(a *appState, c *ui.Context) { a.focusedWeb().view.Page().GoBack() }},
	{ID: "browser.forward", Title: "Go Forward", Category: "Browser",
		Enabled: func(a *appState) bool { return a.focusedWeb() != nil && a.focusedWeb().fwd },
		Run:     func(a *appState, c *ui.Context) { a.focusedWeb().view.Page().GoForward() }},
	{ID: "browser.find", Title: "Find in Page", Category: "Browser",
		Enabled: func(a *appState) bool { return a.focusedWeb() != nil },
		Run:     func(a *appState, c *ui.Context) { a.findInFocused() }},
	{ID: "browser.devtools", Title: "Toggle Developer Tools", Category: "Browser",
		Enabled: func(a *appState) bool { return a.focusedWeb() != nil && a.focusedWeb().view != nil },
		Run:     func(a *appState, c *ui.Context) { a.focusedWeb().view.Page().OpenDevTools() }},
	{ID: "view.changes", Title: "Show Changes", Category: "View",
		Run: func(a *appState, c *ui.Context) { a.inspectorVisible = true; a.showInspectorTab(3) }},
	{ID: "pane.zoom", Title: "Zoom Pane", Category: "Terminal",
		Enabled: func(a *appState) bool { return a.activeView == tabTerminal },
		Run:     func(a *appState, c *ui.Context) { a.toggleZoom() }},
	{ID: "terminal.closePane", Title: "Close Terminal Pane", Category: "Terminal",
		Enabled: func(a *appState) bool { return a.activeView == tabTerminal },
		Run:     func(a *appState, c *ui.Context) { a.closeActivePane(c) }},

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
}, digitCommands()...)

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

// fontSizeSteps is the range the zoom keys keep the text in, in DIPs. The
// default is 13; 8 is small enough to fit a wide file, 32 large enough to
// read from across a room.
const (
	minFontSize     = 8
	maxFontSize     = 32
	defaultFontSize = 13
)

// zoomFont changes the text size of the terminal and the editor together:
// d is the steps, or 0 to go back to the default. One size for both is the
// point: a split with a file beside a shell reads as one surface.
func (a *appState) zoomFont(d int) {
	size := a.fontSize
	if size == 0 {
		size = defaultFontSize
	}
	if d == 0 {
		a.fontSize = 0
	} else {
		size = min(max(size+float32(d), minFontSize), maxFontSize)
		a.fontSize = size
	}
	a.applyFontSize()
}

// applyFontSize gives the new size to every terminal and editor that is
// open, not just the ones built from now on.
func (a *appState) applyFontSize() {
	a.panes.Each(func(p session.Handle) {
		if t := p.TermIfOpen(); t != nil {
			t.SetFont(terminal.Font{Family: a.termFont, Size: a.fontSize})
		}
	})
	for _, doc := range a.editors {
		if doc.State != nil {
			doc.State.Font = ui.Font{Size: a.fontSize}
		}
	}
}

// updatesReady says the updater plugin is registered. main sets it; the
// plugin's entry points panic when it is not, as in the tests, which build
// the app state without main.
var updatesReady bool

// checkForUpdates opens the update window, as the menu item does: it shows
// at once, and says when the app is up to date or the check failed. The
// plugin's window is the one that draws it, so the app needs no UI of its
// own for the check.
func (a *appState) checkForUpdates(c *ui.Context) {
	if !updatesReady {
		c.Toast("Updates are not available in this build")
		return
	}
	updater.CheckForUpdates()
}

// toast shows a message, when there is a window to show it in (a command
// run through the socket has none).
func toast(c *ui.Context, msg string) {
	if c != nil {
		c.Toast(msg)
	}
}
