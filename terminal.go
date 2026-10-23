package main

import (
	"omaterm/internal/session"
	"os"
	"strings"

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
	if a.activeTab >= 0 && a.activeTab < len(a.tabs) {
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
	if p := a.panes.Get(id); p != nil {
		return p.Term()
	}
	if a.paneFailed[id] {
		return nil
	}
	tab := a.tabOfPane(id)
	sessionID := ""
	if tab >= 0 {
		sessionID = a.tabs[tab].ID
	}
	dir := a.paneDirs[id]
	if dir == "" {
		dir = a.workspace
	}
	p, err := a.panes.Start(session.StartOptions{
		ID: id, Session: sessionID, Dir: dir,
		Command: a.termCmd, Exec: len(a.termCmd) > 0,
		Font:            terminal.Font{Size: a.fontSize},
		Theme:           omaTermTheme(),
		MinimumContrast: a.minimumContrast(),
		Padding:         terminal.Padding{X: 10, Y: 6},
	})
	if err != nil {
		a.paneFailed[id] = true
		return nil
	}
	return p.Term()
}

// paneExited closes a pane whose shell exited: the pane, or its tab when
// it was the tab's last. UI thread.
func (a *appState) paneExited(id string) {
	tab := a.tabOfPane(id)
	if tab < 0 || a.panes.Get(id) == nil {
		return
	}
	if a.tabs[tab].Split.isLeaf() {
		a.closeTab(tab)
	} else {
		a.removePaneIn(tab, id)
	}
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
	a.panes.Remove(termID)
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
	}
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

func (a *appState) terminalView(c *ui.Context) ui.Element {
	if len(a.tabs) == 0 {
		return ui.Column(c).Fill().Background(pal.Card).Center().Gap(12).Children(func() {
			ui.Box(c).Width(48).Height(48).Radius(12).Border(1, pal.BorderStrong).Background(pal.Raised).Children(func() {
				ui.Column(c).Fill().Center().Children(func() {
					ui.Icon(c, iconTerminal).Width(24).Height(24).TextColor(pal.Accent)
				})
			})
			ui.Text(c, "No tabs open").FontSize(16).TextColor(pal.Text).Bold()
			ui.Text(c, "Open a project or create a terminal to get started").FontSize(12).TextColor(pal.TextMuted)
			ui.Row(c).Gap(8).Margin(8, 0, 0, 0).Children(func() {
				b := ui.ButtonBase(c).Radius(6).Border(1, pal.BorderStrong).Padding(8, 16).Background(pal.Raised)
				if b.Hovered() {
					b.Background(pal.Hover)
				}
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

// ---------------------------------------------------------------------------
// Toolbar actions
// ---------------------------------------------------------------------------

func (a *appState) killTermPaneByTermID(termID string) {
	a.removePane(termID)
	// If only one pane left, collapse (already handled by removePane setting
	// Split to nil when empty; but single leaf remains for 1 pane).
}

// newTerminalTabReal adds a terminal tab with a single pane.
func (a *appState) newTerminalTabReal() {
	shell := os.Getenv("SHELL")
	name := "Terminal"
	if shell != "" {
		if i := strings.LastIndex(shell, "/"); i >= 0 {
			name = shell[i+1:]
		}
	}
	a.tabs = append(a.tabs, topTab{
		ID:    newID(),
		Kind:  tabTerminal,
		Label: name,
		Dot:   dotGreen,
		Path:  randomTermID(),
		Split: &splitNode{TermID: randomTermID()},
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
