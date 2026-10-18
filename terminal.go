package main

import (
	"os"
	"strings"
	"time"

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
	if t, ok := a.termPanes[id]; ok && t != nil {
		return t
	}
	shell := os.Getenv("SHELL")
	var cmd []string
	if shell != "" {
		cmd = []string{shell}
	}
	t, err := terminal.New(terminal.Options{
		Command: cmd,
		Dir:     a.workspace,
		OnTitle: func(title string) {},
		OnExit:  func(code int) {},
	})
	if err != nil {
		a.termPanes[id] = nil
		return nil
	}
	a.termPanes[id] = t
	return t
}

func (a *appState) splitPane(termID string, dir splitDir) {
	root := a.ensureSingleSplit()
	if r := root.splitLeaf(termID, dir, randomTermID()); r != nil {
		a.setActiveSplit(r)
	}
}

func (a *appState) removePane(termID string) {
	root := a.activeSplit()
	if root == nil {
		return
	}
	newRoot, removed := removeLeaf(root, termID)
	if !removed {
		return
	}
	if t, ok := a.termPanes[termID]; ok && t != nil {
		t.Close()
	}
	delete(a.termPanes, termID)
	if newRoot == nil {
		// tab should close; for now leave nil (closeTab handles cleanup)
		a.setActiveSplit(nil)
	} else {
		a.setActiveSplit(newRoot)
	}
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

func (a *appState) terminalView(c *ui.Context) ui.Element {
	return ui.Column(c).Fill().Background(colBg2).Children(func() {
		a.renderSplit(c, a.ensureSingleSplit())
	})
}

func (a *appState) renderSplit(c *ui.Context, node *splitNode) {
	if node == nil {
		return
	}
	if node.isLeaf() {
		a.termPane(c, node.TermID)
		return
	}
	if node.Ratio == 0 {
		node.Ratio = 50
	}
	if node.Dir == splitVertical {
		ui.SplitPercent(c, &node.Ratio,
			func() { a.renderSplit(c, node.First) },
			func() { a.renderSplit(c, node.Second) }).Fill()
	} else {
		ui.SplitVerticalPercent(c, &node.Ratio,
			func() { a.renderSplit(c, node.First) },
			func() { a.renderSplit(c, node.Second) }).Fill()
	}
}

func (a *appState) termPane(c *ui.Context, termID string) {
	t := a.term(termID)
	isActive := a.activePaneTermID == termID
	ui.Column(c).Fill().Background(colBg2).Children(func() {
		// Header strip.
		ui.Row(c).Height(32).AlignItems(ui.Center).Padding(0, 12).Gap(8).
			BorderWidth(0, 0, 1, 0).BorderColor(colBorder).ClipX().Children(func() {
			kind := dotGreen
			if t == nil {
				kind = dotGray
			}
			dot(c, kind, 6)
			title := "Terminal"
			if t != nil {
				if tt := t.Title(); tt != "" {
					title = tt
				}
				if d := t.Dir(); d != "" {
					ui.Text(c, d).FontSize(10).TextColor(colMuted2).SingleLine().Shrink(0)
				}
			}
			txt(c, title, 10, colText2)
			ui.Spacer(c)
			// Compact toolbar.
			if cmdButton(c, iconColumns2, 14, false).Tooltip("Split right (Ctrl+Shift+R)").Clicked() {
				a.splitPane(termID, splitVertical)
				c.Toast("Split right")
			}
			if cmdButton(c, iconRows2, 14, false).Tooltip("Split down (Ctrl+Shift+D)").Clicked() {
				a.splitPane(termID, splitHorizontal)
				c.Toast("Split down")
			}
			if cmdButton(c, iconSplit, 14, false).Tooltip("Equalize panes").Clicked() {
				if n := a.activeSplit(); n != nil {
					equalizeSplit(n)
				}
				c.Toast("Equalized")
			}
			if cmdButton(c, iconRotate, 14, false).Tooltip("Restart terminal").Clicked() {
				a.restartTermPane(termID)
				c.Toast("Restarted terminal")
			}
			// Kill (hide for the only pane).
			ids := []string{}
			if n := a.activeSplit(); n != nil {
				ids = n.termIDs()
			}
			if len(ids) > 1 {
				if cmdButton(c, iconTrash, 14, false).Tooltip("Close pane").Clicked() {
					a.removePane(termID)
					c.Toast("Pane closed")
				}
			}
		})

		if t == nil {
			ui.Box(c).Grow(1).Children(func() {
				ui.Text(c, "failed to start terminal").FontSize(12).TextColor(colRed)
			})
			return
		}

		// Terminal view.
		box := ui.Box(c).Grow(1).Clip()
		if box.Clicked() || box.Hovered() {
			a.activePaneTermID = termID
		}
		// Focus indicator border.
		if isActive {
			box.DrawOver(func(p *ui.Painter, r ui.Rect) {
				p.Stroke(ui.Rect{X: r.X + 0.5, Y: r.Y + 0.5, W: r.W - 1, H: r.H - 1},
					ui.RGBA(90, 169, 255, 0.5), 0, 1)
			})
		}
		box.Children(func() {
			terminal.View(c, t).Fill().AutoFocus()
		})

	})
}

// ---------------------------------------------------------------------------
// Toolbar actions
// ---------------------------------------------------------------------------

func (a *appState) restartTermPane(termID string) {
	if t, ok := a.termPanes[termID]; ok && t != nil {
		t.Close()
	}
	delete(a.termPanes, termID)
	a.term(termID) // recreate
}

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
		Kind:  tabTerminal,
		Label: name,
		Dot:   dotGreen,
		Path:  randomTermID(),
		Split: &splitNode{TermID: randomTermID()},
	})
	a.activeTab = len(a.tabs) - 1
	a.activeView = tabTerminal
	if len(a.tabs) > 0 && a.tabs[a.activeTab].Split != nil {
		ids := a.tabs[a.activeTab].Split.termIDs()
		if len(ids) > 0 {
			a.activePaneTermID = ids[0]
		}
	}
}

// equalizeSplit resets every split ratio in the tree to 50/50.
func equalizeSplit(n *splitNode) {
	if n == nil || n.isLeaf() {
		return
	}
	n.Ratio = 50
	equalizeSplit(n.First)
	equalizeSplit(n.Second)
}

func randomTermID() string {
	return "term-" + time.Now().Format("20060102-150405.000000")
}
