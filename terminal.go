package main

import (
	"omaterm/internal/paths"
	"omaterm/internal/session"
	"omaterm/internal/shellcmd"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// ---------------------------------------------------------------------------
// Split tree model for terminal panes. Replaces the old layoutKind enum.
// ---------------------------------------------------------------------------

type splitDir int

const (
	splitVertical   splitDir = iota // side by side (left | right)
	splitHorizontal                 // stacked (top / bottom)
)

type splitNode struct {
	// Leaf: a terminal pane. TermID is non-empty for leaves.
	TermID string
	// Internal: a split.
	Dir    splitDir
	Ratio  float32 // 0..100, percentage First takes (0 => 50)
	First  *splitNode
	Second *splitNode
}

func (n *splitNode) isLeaf() bool { return n != nil && n.TermID != "" }

func (n *splitNode) leafCount() int {
	if n == nil {
		return 0
	}
	if n.isLeaf() {
		return 1
	}
	return n.First.leafCount() + n.Second.leafCount()
}

func (n *splitNode) termIDs() []string {
	if n == nil {
		return nil
	}
	if n.isLeaf() {
		return []string{n.TermID}
	}
	return append(n.First.termIDs(), n.Second.termIDs()...)
}

// splitLeaf replaces the leaf with TermID == termID with a split node.
// Returns a non-nil replacement only when n itself was the target leaf
// (caller must swap the root). Otherwise mutates in place.
func (n *splitNode) splitLeaf(termID string, dir splitDir, newID string) *splitNode {
	if n == nil {
		return nil
	}
	if n.isLeaf() {
		if n.TermID == termID {
			return &splitNode{Dir: dir, Ratio: 50,
				First: n, Second: &splitNode{TermID: newID}}
		}
		return nil
	}
	if r := n.First.splitLeaf(termID, dir, newID); r != nil {
		n.First = r
	}
	if r := n.Second.splitLeaf(termID, dir, newID); r != nil {
		n.Second = r
	}
	return nil
}

// removeLeaf removes the leaf with the given TermID. Returns the replacement
// node (nil if the whole tree is gone) and whether removal happened.
func removeLeaf(n *splitNode, termID string) (*splitNode, bool) {
	if n == nil {
		return nil, false
	}
	if n.isLeaf() {
		if n.TermID == termID {
			return nil, true
		}
		return n, false
	}
	if nf, ok := removeLeaf(n.First, termID); ok {
		if nf == nil {
			return n.Second, true
		} // promote sibling
		n.First = nf
		return n, true
	}
	if ns, ok := removeLeaf(n.Second, termID); ok {
		if ns == nil {
			return n.First, true
		}
		n.Second = ns
		return n, true
	}
	return n, false
}

// ---------------------------------------------------------------------------
// appState helpers
// ---------------------------------------------------------------------------

func (a *appState) activeSplit() *splitNode {
	if a.activeTab < 0 || a.activeTab >= len(a.tabs) {
		return nil
	}
	return a.tabs[a.activeTab].Split
}

func (a *appState) setActiveSplit(n *splitNode) {
	if a.activeTab >= 0 && a.activeTab < len(a.tabs) && (a.tabs[a.activeTab].Kind == tabTerminal || a.tabs[a.activeTab].Kind == tabProcess) {
		a.tabs[a.activeTab].Split = n
	}
}

func (a *appState) ensureSingleSplit() *splitNode {
	if n := a.activeSplit(); n != nil {
		return n
	}
	n := &splitNode{TermID: randomTermID()}
	a.setActiveSplit(n)
	return n
}

// activeSplitLeaf returns the TermID of the pane that should receive
// split/close actions: the clicked pane, or the first leaf.
func (a *appState) activeSplitLeaf() string {
	if a.activePaneTermID != "" {
		return a.activePaneTermID
	}
	if n := a.activeSplit(); n != nil {
		ids := n.termIDs()
		if len(ids) > 0 {
			a.activePaneTermID = ids[0]
			return ids[0]
		}
	}
	return ""
}

func (a *appState) term(id string) *terminal.Terminal {
	a.paneShown[id] = time.Now()
	if p := a.panes.Get(id); p != nil {
		return p.Term()
	}
	if tab := a.tabOfPane(id); tab >= 0 && a.tabs[tab].Kind == tabProcess {
		return nil
	}
	if a.paneFailed[id] {
		return nil
	}
	sessionID := ""
	if tab := a.tabOfPane(id); tab >= 0 {
		sessionID = a.tabs[tab].ID
	}
	p, err := a.startRestored(id, sessionID, a.workspace)
	if err != nil {
		return nil
	}
	return p.Term()
}

// startRestored starts a pane not running yet: in its folder, else in
// fallback, with the last run's scrollback and agent when it is restored.
func (a *appState) startRestored(id, sessionID, fallback string) (session.Handle, error) {
	dir := a.paneDirs[id]
	if dir == "" {
		dir = fallback
	}
	if _, tabs, ti, ok := a.findSession(sessionID); ok && (*tabs)[ti].Worktree != nil {
		var err error
		if dir, err = (*tabs)[ti].Worktree.PaneDir(a.paneDirs[id]); err != nil {
			a.paneFailed[id] = true
			return nil, err
		}
	}
	command, exec := a.termCmd, len(a.termCmd) > 0
	history, resume := a.restoreOptions(id)
	if resume != nil && !exec {
		command = resume
	}
	p, err := a.startPaneWith(id, sessionID, dir, command, exec, history)
	if err != nil {
		a.paneFailed[id] = true
	}
	return p, err
}

// startPane runs a pane's shell, running command first when set (or only
// command, with exec), in dir.
func (a *appState) startPane(id, sessionID, dir string, command []string, exec bool, env ...string) (session.Handle, error) {
	return a.startPaneWith(id, sessionID, dir, command, exec, nil, env...)
}

// startPaneWith is startPane with the scrollback a restored pane shows
// above its shell.
func (a *appState) startPaneWith(id, sessionID, dir string, command []string, exec bool, history []byte, extra ...string) (session.Handle, error) {
	// The omaterm command comes first: another program of that name on
	// the PATH must not answer for it in a pane.
	env := []string{"PATH=" + paths.BinDir() + string(os.PathListSeparator) + os.Getenv("PATH")}
	if a.ctl != nil {
		env = append(env, "OMATERM_SOCKET="+a.ctl.Path())
	}
	// What the caller adds (omaterm new --env), after: it may set PATH.
	env = append(env, extra...)
	o := a.termStartOptions(id)
	o.ID, o.Session, o.Dir = id, sessionID, dir
	o.Command, o.Exec, o.Env, o.History = command, exec, env, history
	if pi, _, _, ok := a.findSession(sessionID); ok {
		p := &a.projects[pi]
		o.Project, o.ProjectName, o.ProjectDir = p.ID, p.Name, p.Path
	}
	return a.panes.Start(o)
}

// termStartOptions are a terminal's look and behavior as the settings
// have them, for the panes the app runs and the terminals that show the
// session daemon's; id is the pane's, for what a link clicked in it opens.
func (a *appState) termStartOptions(id string) session.StartOptions {
	t := a.settings.Terminal
	o := session.StartOptions{
		Font:            a.termFontSpec(),
		Theme:           omaTermTheme(),
		MinimumContrast: a.minimumContrast(),
		Padding:         terminal.Padding{X: 10, Y: 6},
		NoBlink:         !t.CursorBlink,
		Scrollback:      t.ScrollbackMB << 20,
		Shell:           strings.TrimSpace(t.Shell),
		OptionAsAlt:     t.OptionAsMeta,
		OpenLink:        func(link string) bool { return a.openTermLink(id, link) },
		OnZoom: func(z terminal.Zoom) bool {
			if z.Reset {
				a.zoomFont(0)
			} else {
				a.zoomFont(z.Steps)
			}
			return true
		},
	}
	switch t.Cursor {
	case "bar":
		o.Cursor = terminal.CursorBar
	case "underline":
		o.Cursor = terminal.CursorUnderline
	}
	if t.CopyOnSelect {
		o.CopyOnSelect = terminal.CopyOnSelectClipboard
	}
	if t.RightClick == "paste" {
		o.RightClick = terminal.RightClickPaste
	}
	if t.ClipboardRead == "ask" {
		o.ClipboardRead = terminal.ClipboardAsk
	}
	return o
}

// openTermLink opens what was clicked in pane's terminal: a file path,
// as the program printed it (relative to where it runs, with a line and
// column after it), in the editor when it is in the project, else in
// what the system opens it with; a web link in a browser pane beside it,
// when the settings say so. False leaves the rest to the system.
func (a *appState) openTermLink(pane, link string) bool {
	if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
		if a.settings.OpenLinks != "pane" {
			return false
		}
		if a.activeTab < 0 || a.activeTab >= len(a.tabs) || a.tabs[a.activeTab].Kind != tabTerminal {
			return false
		}
		a.openBrowser(link)
		return true
	}
	path, line, col := parseFileLink(link)
	if path == "" {
		return false
	}
	if !filepath.IsAbs(path) {
		dir := a.status[pane].WorkDir()
		if dir == "" {
			dir = a.paneDirs[pane]
		}
		if dir == "" {
			dir = a.workspace
		}
		path = termLinkPath(runtime.GOOS, dir, path)
	}
	path = filepath.Clean(path)
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		return false
	}
	// A file outside the project, as a plan under ~/.claude, opens in the
	// editor too: the system would hand it to whatever opens its type, and
	// on Windows run a program.
	if rel, err := filepath.Rel(a.workspace, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		path = filepath.ToSlash(rel)
	}
	a.openEditorAt(path, line, col)
	return true
}

// openURL opens a page where links open: a browser pane, or the default
// browser, as the settings say.
func (a *appState) openURL(url string) {
	if a.settings.OpenLinks == "pane" {
		a.activateKind(tabTerminal)
		a.openBrowser(url)
		return
	}
	if a.win != nil {
		mygo.Shell.OpenExternal(url)
	}
}

// paneExited closes a pane whose shell exited: the pane, or its session
// when it was the session's last, in whichever project it is, not only
// the one shown. UI thread.
func (a *appState) paneExited(id string) {
	if a.status[id].Role == "process" {
		return
	}
	if h := a.panes.Get(id); h != nil && h.Info().Role == "process" {
		return
	}
	if a.panes.Get(id) == nil {
		return
	}
	pi, tabs, ti, ok := a.findPane(id)
	if !ok {
		return
	}
	// A shell that ended on its own with an error (killed, out of memory)
	// says so before its session goes; one the user exited goes quietly.
	if h := a.panes.Get(id); h != nil {
		if info := h.Info(); info.ExitCode != nil && *info.ExitCode != 0 {
			name := a.sessionName((*tabs)[ti], info)
			a.queuedToasts = append(a.queuedToasts, "“"+name+"” in "+a.projects[pi].Name+" closed: its shell ended (code "+strconv.Itoa(*info.ExitCode)+")")
		}
	}
	a.cliClosePane(id)
}

// minimumContrast is the terminals' least contrast: the setting, else 1
// in dark themes, where theme authors pick their colors with care, and 3
// in light ones, whose bright colors often wash out.
func (a *appState) minimumContrast() float32 {
	if a.settings.MinimumContrast > 0 {
		return a.settings.MinimumContrast
	}
	if pal.Dark {
		return 1
	}
	return 3
}

// axisWeight is how many columns (or rows) a subtree takes along dir: a
// leaf takes one, a split along the same axis takes one per leaf it holds,
// and a split across the other axis counts as a single column.
func (n *splitNode) axisWeight(dir splitDir) int {
	if n == nil {
		return 0
	}
	if n.isLeaf() || n.Dir != dir {
		return 1
	}
	return n.First.axisWeight(dir) + n.Second.axisWeight(dir)
}

// rebalance makes siblings along one axis share the space equally, so a
// split never leaves one pane half the width of its neighbours.
func rebalance(n *splitNode) {
	if n == nil || n.isLeaf() {
		return
	}
	rebalance(n.First)
	rebalance(n.Second)
	w1, w2 := n.First.axisWeight(n.Dir), n.Second.axisWeight(n.Dir)
	if w1+w2 > 0 {
		n.Ratio = 100 * float32(w1) / float32(w1+w2)
	}
}

// splitPane splits the pane termID and returns the new pane's ID, or ""
// when the pane was not found.
func (a *appState) splitPane(termID string, dir splitDir) string {
	newID := randomTermID()
	root := a.ensureSingleSplit()
	if r := root.splitLeaf(termID, dir, newID); r != nil {
		a.setActiveSplit(r)
	}
	if n := a.activeSplit(); n != nil {
		rebalance(n)
	}
	a.saveSoon()
	for _, id := range a.activeSplit().termIDs() {
		if id == newID {
			return newID
		}
	}
	return ""
}

// tabOfPane returns the index of the tab whose split holds the pane, or -1.
func (a *appState) tabOfPane(termID string) int {
	for i, tab := range a.tabs {
		if tab.Split == nil {
			continue
		}
		for _, id := range tab.Split.termIDs() {
			if id == termID {
				return i
			}
		}
	}
	return -1
}

// removePane closes the pane wherever it is, not only in the active tab: a
// shell can exit in a tab the user is not looking at.
func (a *appState) removePane(termID string) {
	a.removePaneIn(a.tabOfPane(termID), termID)
}

func (a *appState) removePaneIn(tab int, termID string) {
	if tab < 0 || tab >= len(a.tabs) {
		return
	}
	root := a.tabs[tab].Split
	if root == nil {
		return
	}
	newRoot, removed := removeLeaf(root, termID)
	if !removed {
		return
	}
	active := tab == a.activeTab
	if a.activePaneTermID == termID {
		a.activePaneTermID = ""
	}
	a.closePaneRuntime(termID)
	a.saveSoon()
	a.tabs[tab].Split = newRoot
	if newRoot != nil {
		rebalance(newRoot)
		// A pane closed under the keyboard: give it to the first one left,
		// unless the caller picked another.
		if active && a.activePaneTermID == "" {
			if ids := newRoot.termIDs(); len(ids) > 0 {
				a.activePaneTermID = ids[0]
			}
		}
		if active {
			// Typing went nowhere until a click: the pane left gets the
			// keyboard as it is drawn.
			a.refocusPane = true
		}
	}
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

func (a *appState) terminalView(c *ui.Context) ui.Element {
	processTab := a.activeTab >= 0 && a.activeTab < len(a.tabs) && a.tabs[a.activeTab].Kind == tabProcess
	if processTab && a.tabs[a.activeTab].Split == nil {
		return a.processPlaceholder(c, a.tabs[a.activeTab])
	}
	// The terminal view shows a session tab. Asked for while an editor or
	// a diff is the tab (as after discarding a diff's changes), it shows
	// the first session; a split was once added to that tab, starting a
	// shell that nothing closed.
	if a.activeTab < 0 || a.activeTab >= len(a.tabs) || (a.tabs[a.activeTab].Kind != tabTerminal && a.tabs[a.activeTab].Kind != tabProcess) {
		for i, t := range a.tabs {
			if t.Kind == tabTerminal {
				a.activateTab(i)
				break
			}
		}
	}
	if len(a.tabs) == 0 || a.activeTab < 0 || a.activeTab >= len(a.tabs) || (a.tabs[a.activeTab].Kind != tabTerminal && a.tabs[a.activeTab].Kind != tabProcess) {
		if p := a.project(); p != nil && !slices.ContainsFunc(a.tabs, func(t topTab) bool { return t.Kind == tabTerminal }) {
			return a.projectStart(c, p)
		}
		return ui.Column(c).Fill().Background(pal.Card).Center().Gap(12).Children(func() {
			ui.Box(c).Width(48).Height(48).Radius(12).Border(1, pal.BorderStrong).Background(pal.Raised).Children(func() {
				ui.Column(c).Fill().Center().Children(func() {
					ui.Icon(c, iconTerminal).Width(24).Height(24).TextColor(pal.Accent)
				})
			})
			what := "No tabs open"
			if len(a.tabs) > 0 {
				what = "No terminal open"
			}
			ui.Text(c, what).FontSize(16).TextColor(pal.Text).Bold()
			ui.Text(c, "Open a project or create a terminal to get started").FontSize(12).TextColor(pal.TextMuted)
			ui.Row(c).Gap(8).Margin(8, 0, 0, 0).Children(func() {
				b := ui.ButtonBase(c).Radius(6).Border(1, pal.BorderStrong).Padding(8, 16).Background(pal.Raised)
				if b.Hovered() {
					b.Background(pal.Hover)
				}
				a.press(a.fadeColors(b))
				b.Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
						ui.Icon(c, iconFolderPlus).Width(14).Height(14).TextColor(pal.TextSoft)
						ui.Text(c, "Open Project").FontSize(12).TextColor(pal.TextSoft)
					})
				})
				if b.Clicked() {
					a.openProjectDialog()
				}

				b2 := ui.ButtonBase(c).Radius(6).Border(1, pal.BorderStrong).Padding(8, 16).Background(pal.Raised)
				if b2.Hovered() {
					b2.Background(pal.Hover)
				}
				a.press(a.fadeColors(b2))
				b2.Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
						ui.Icon(c, iconTerminal).Width(14).Height(14).TextColor(pal.TextSoft)
						ui.Text(c, "New Terminal").FontSize(12).TextColor(pal.TextSoft)
					})
				})
				if b2.Clicked() {
					a.newTerminalTab()
				}
			})
			ui.Text(c, keyLabels("app.openProject")+" open project  ·  "+
				keyLabels("terminal.new")+" new terminal  ·  "+
				keyLabels("file.quickOpen")+" open file  ·  "+
				keyLabels("help.keyboardShortcuts")+" shortcuts").
				FontSize(10).TextColor(pal.TextFaint).Margin(8, 0, 0, 0)
		})
	}
	return ui.Column(c).Fill().MinHeight(0).Children(func() {
		a.renderPanes(c)
	})
}

// projectStart is what a project with no session shows: what can start in
// it, as its + offers, each with a word on what it is.
func (a *appState) projectStart(c *ui.Context, p *project) ui.Element {
	pi := a.active
	title, about := "Nothing runs in "+p.Name+" yet", "Start a shell, an agent, or one of the project's processes."
	if len(a.procList[p.ID]) > 0 {
		title, about = "No sessions in "+p.Name, "Start a shell or an agent; its processes are in the sidebar."
	}
	return ui.Column(c.Key("project-start")).Fill().Background(pal.Card).Center().Gap(10).Children(func() {
		ui.Box(c).Size(44, 44).Radius(12).Border(1, pal.BorderStrong).Background(pal.Raised).Children(func() {
			ui.Column(c).Fill().Center().Children(func() {
				ui.Icon(c, iconFolder).Size(22, 22).TextColor(pal.Accent)
			})
		})
		ui.Text(c, title).FontSize(15).FontWeight(600).TextColor(pal.Text).Margin(2, 0, 0, 0)
		ui.Text(c, about).FontSize(12.5).TextColor(pal.TextMuted)
		ui.Column(c).Width(300).Gap(6).Margin(6, 0, 0, 0).Children(func() {
			action := func(icon *ui.SVG, label, sub, keys string, first bool, do func()) {
				b := ui.ButtonBase(c.Key("start-"+label)).Height(40).Radius(8).Padding(0, 12).Gap(10).AlignItems(ui.Center).
					Border(1, pal.BorderStrong).Background(pal.Raised).Label(label)
				if first {
					b.Border(1, pal.Accent.Alpha(0.55))
				}
				if b.Hovered() {
					b.Background(pal.Hover.Over(pal.Raised))
				}
				a.press(a.fadeColors(b))
				b.Children(func() {
					ui.Icon(c, icon).Size(15, 15).TextColor(pal.TextMuted)
					ui.Column(c).Grow(1).MinWidth(0).Children(func() {
						ui.Text(c, label).FontSize(12.5).FontWeight(550).TextColor(pal.Text).SingleLine()
						ui.Text(c, sub).FontSize(11).TextColor(pal.TextFaint).SingleLine().Ellipsis("…")
					})
					if keys != "" {
						ui.Text(c, keys).FontSize(10.5).TextColor(pal.TextFaint).SingleLine().Shrink(0)
					}
				})
				if b.Clicked() {
					do()
				}
			}
			action(iconTerminal, "New Session", "A shell in "+p.Name, keyLabels("terminal.new"), true, a.newTerminalTab)
			action(iconBot, "New Agent…", "Claude Code, Codex, Antigravity…", "", false, func() { a.openWorktreeDialog(pi) })
			action(iconPlus, "Add Process…", "A dev server or watcher, kept running", "", false, func() { a.openProcessDialog(pi, nil) })
		})
	})
}

// ---------------------------------------------------------------------------
// Toolbar actions
// ---------------------------------------------------------------------------

// newTerminalTabReal adds a terminal tab with a single pane.
func (a *appState) newTerminalTabReal() {
	a.rememberFocus()
	a.tabs = append(a.tabs, topTab{
		ID:      newID(),
		Kind:    tabTerminal,
		Label:   shellcmd.Name(shellcmd.Login()),
		Dot:     dotGreen,
		Path:    randomTermID(),
		Split:   &splitNode{TermID: randomTermID()},
		Created: nowFunc().UnixMilli(),
	})
	a.activeTab = len(a.tabs) - 1
	a.activeView = tabTerminal
	a.saveSoon()
	if len(a.tabs) > 0 && a.tabs[a.activeTab].Split != nil {
		ids := a.tabs[a.activeTab].Split.termIDs()
		if len(ids) > 0 {
			a.activePaneTermID = ids[0]
		}
	}
}

func randomTermID() string { return newID() }

// closePaneRuntime ends what a pane runs: its shell, or its page.
func (a *appState) closePaneRuntime(id string) {
	a.historyMu.Lock() // not while saveHistories writes its file
	a.panes.Remove(id)
	os.Remove(historyPath(id))
	a.historyMu.Unlock()
	delete(a.restoring, id)
	delete(a.paneAgents, id)
	a.dropWeb(id)
	delete(a.paneDirs, id)
	delete(a.paneEnv, id)
	delete(a.paneFailed, id)
}
