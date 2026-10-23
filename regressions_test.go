package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"omaterm/internal/session"
	"omaterm/service"
)

// stopTermPanes ends every shell a test started, so it does not outlive the
// test binary.
func stopTermPanes(a *appState) {
	a.panes.CloseAll()
}

// The terminal could not be typed into: a project now opens with its first
// pane holding the keyboard, so typing works without clicking into it.
func TestTerminalTyping(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	var term *terminal.Terminal
	a.panes.Each(func(p session.Handle) {
		if term == nil {
			term = p.Term()
		}
	})
	if term == nil {
		t.Fatal("no terminal pane created")
	}

	tr.Type("echo TYPED_OK")
	tr.Key(0, ui.KeyEnter)
	tr.Frame()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tr.Frame()
		if strings.Contains(term.Text(), "TYPED_OK") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the shell never received the typed command")
}

// The arrows moved the highlight without scrolling to it, and Enter did
// nothing: both must work with the search input keeping the focus.
func TestQuickOpenArrowAndEnter(t *testing.T) {
	a := newTestApp(t)
	// Enough files that the list has to scroll.
	for i := 0; i < 300; i++ {
		name := filepath.Join(a.workspace, fmt.Sprintf("f%03d.txt", i))
		if err := os.WriteFile(name, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a.fileIndex = service.IndexFiles(a.workspace, 100000)

	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	tr.Key(ui.Cmd, ui.KeyP)
	tr.Frame()
	if a.overlay != overlayQuickOpen {
		t.Fatal("Ctrl+P did not open quick open")
	}

	// One press moves one row.
	for i := 0; i < 5; i++ {
		before := a.quickSelected
		tr.Key(0, ui.KeyDown)
		tr.Frame()
		if a.quickSelected != before+1 {
			t.Fatalf("down moved the selection %d -> %d, want one row", before, a.quickSelected)
		}
	}

	// Walking to the end keeps the last row on screen.
	items := a.quickOpenItems()
	for i := 0; i < len(items)+5; i++ {
		tr.Key(0, ui.KeyDown)
		tr.Frame()
	}
	last := items[len(items)-1].Value
	if !tr.HasText(last) {
		t.Fatalf("last row %q is off screen: the list did not scroll to the selection", last)
	}

	// And walking back up keeps the first row on screen.
	for i := 0; i < len(items)+5; i++ {
		tr.Key(0, ui.KeyUp)
		tr.Frame()
	}
	if !tr.HasText(items[0].Value) {
		t.Fatalf("first row %q is off screen after walking back up", items[0].Value)
	}

	// Enter opens the highlighted file.
	tr.Key(0, ui.KeyEnter)
	tr.Frame()
	if a.overlay != overlayNone {
		t.Fatal("Enter did not close quick open")
	}
	if _, ok := a.editors[items[0].Value]; !ok {
		t.Fatalf("Enter did not open %q", items[0].Value)
	}
}

// Enter must run the highlighted command even though the input claims it.
func TestPaletteEnterRunsCommand(t *testing.T) {
	a := newTestApp(t)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	a.openOverlay(overlayCommandPalette)
	tr.Frame()

	tr.Type("toggle sidebar")
	tr.Frame()
	cmds := a.paletteCommands()
	if len(cmds) == 0 || cmds[0].ID != "view.toggleSidebar" {
		t.Fatalf("top match = %+v, want view.toggleSidebar", cmds)
	}

	before := a.sidebarVisible
	tr.Key(0, ui.KeyEnter)
	tr.Frame()
	if a.overlay != overlayNone {
		t.Fatal("Enter did not close the palette")
	}
	if a.sidebarVisible == before {
		t.Fatal("Enter did not run the highlighted command")
	}
}

// Enter must open the highlighted match in global search too.
func TestSearchEnterOpensMatch(t *testing.T) {
	a := newTestApp(t)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	a.openOverlay(overlayGlobalSearch)
	a.searchQuery = "two"
	results, err := service.SearchContent(context.Background(), a.workspace, "two")
	if err != nil || len(results) == 0 {
		t.Skipf("no search results in the fixture: %v", err)
	}
	a.searchResults = results
	a.updateSearchPreview()
	tr.Frame()

	want := a.searchResults[a.searchSelected].Path
	tr.Key(0, ui.KeyEnter)
	tr.Frame()
	if a.overlay != overlayNone {
		t.Fatal("Enter did not close global search")
	}
	if _, ok := a.editors[want]; !ok {
		t.Fatalf("Enter did not open %q", want)
	}
}

// A new terminal tab opened with three panes: it must start with one, and
// Ctrl+W must close it again.
func TestTerminalTabStartsSingle(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	before := len(a.tabs)
	tr.Key(ui.Cmd, ui.KeyT)
	tr.Frame()
	if len(a.tabs) != before+1 {
		t.Fatalf("tabs = %d, want %d", len(a.tabs), before+1)
	}
	if sp := a.activeSplit(); sp == nil || !sp.isLeaf() {
		t.Fatalf("new tab should be a single pane, got %+v", sp)
	}
	if a.panes.Len() == 0 {
		t.Fatal("no pane started for the new tab")
	}

	tr.Key(ui.Cmd, ui.KeyW)
	tr.Frame()
	if len(a.tabs) != before {
		t.Fatalf("tabs = %d after Ctrl+W, want %d", len(a.tabs), before)
	}
}

// A split belongs to the tab it was made in, not to every terminal tab.
func TestSplitIsPerTab(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	leaf := a.activeSplitLeaf()
	a.splitPane(leaf, splitVertical)
	tr.Frame()
	if sp := a.activeSplit(); sp == nil || sp.isLeaf() || sp.Dir != splitVertical {
		t.Fatal("the split did not stick to its tab")
	}
	tr.Key(ui.Cmd, ui.KeyT)
	tr.Frame()
	if sp := a.activeSplit(); sp == nil || !sp.isLeaf() {
		t.Fatalf("a new tab should be single, got %+v", sp)
	}
	a.activeTab = 0
	a.activeView = tabTerminal
	tr.Frame()
	if sp := a.activeSplit(); sp == nil || sp.isLeaf() || sp.Dir != splitVertical {
		t.Fatal("the first tab lost its split")
	}
}

// Splitting can build arbitrary trees (up to 5+ panes) and closing a
// pane keeps the surviving sessions alive.
func TestSplitTreeUpToFivePanes(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	ids := a.activeSplit().termIDs()
	if len(ids) != 1 {
		t.Fatalf("start panes = %d, want 1", len(ids))
	}
	// Split the (single) pane four times -> 5 panes.
	for i := 0; i < 4; i++ {
		a.splitPane(a.activeSplit().termIDs()[0], splitVertical)
	}

	if got := a.activeSplit().leafCount(); got != 5 {
		t.Fatalf("leaf count = %d, want 5", got)
	}
	// Terminals are created lazily on render; frame to materialize them.
	tr.Frame()
	if a.panes.Len() != 5 {
		t.Fatalf("terminals = %d, want 5", a.panes.Len())
	}

	// Close a middle pane; survivors keep their terminals.
	victim := a.activeSplit().termIDs()[2]
	a.removePane(victim)
	tr.Frame()
	if a.panes.Get(victim) != nil {
		t.Fatal("closed pane's terminal was not removed")
	}
	if got := a.activeSplit().leafCount(); got != 4 {
		t.Fatalf("leaf count after close = %d, want 4", got)
	}
	if a.panes.Len() != 4 {
		t.Fatalf("terminals after close = %d, want 4", a.panes.Len())
	}
}

// Splitting a pane balances it against its siblings: A | B split from A
// gives three equal thirds, not 25 | 25 | 50.
func TestSplitBalancesSiblings(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	// Build A | B.
	a.splitPane(a.activeSplit().termIDs()[0], splitVertical)
	root := a.activeSplit()
	if root.isLeaf() || root.Ratio != 50 {
		t.Fatalf("A|B ratio = %v, want 50", root.Ratio)
	}

	// Split A (the first leaf).
	a.splitPane(root.termIDs()[0], splitVertical)
	root = a.activeSplit()
	if got, want := root.Ratio, float32(200.0/3.0); abs(got-want) > 0.01 {
		t.Errorf("three-pane root ratio = %v, want %v", got, want)
	}
	if got := root.First.Ratio; got != 50 {
		t.Errorf("inner ratio = %v, want 50", got)
	}
	// Each of the three columns is a third of the width.
	for i, w := range splitWidths(root, 900) {
		if abs(w-300) > 1 {
			t.Errorf("pane %d width = %v, want 300", i, w)
		}
	}
}

// Splitting twice keeps four panes equal.
func TestSplitBalancesFourPanes(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	a.splitPane(a.activeSplit().termIDs()[0], splitVertical)
	a.splitPane(a.activeSplit().termIDs()[0], splitVertical)
	a.splitPane(a.activeSplit().termIDs()[0], splitVertical)

	root := a.activeSplit()
	if got := root.leafCount(); got != 4 {
		t.Fatalf("panes = %d, want 4", got)
	}
	for i, w := range splitWidths(root, 800) {
		if abs(w-200) > 1 {
			t.Errorf("pane %d width = %v, want 200", i, w)
		}
	}
}

// Removing a pane rebalances the survivors.
func TestRemoveRebalances(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	a.splitPane(a.activeSplit().termIDs()[0], splitVertical)
	a.splitPane(a.activeSplit().termIDs()[0], splitVertical)
	root := a.activeSplit()
	if got := root.leafCount(); got != 3 {
		t.Fatalf("panes = %d, want 3", got)
	}

	// Close the middle pane; the two survivors split the width evenly.
	a.removePane(root.termIDs()[1])
	root = a.activeSplit()
	if got := root.leafCount(); got != 2 {
		t.Fatalf("panes after close = %d, want 2", got)
	}
	if root.Ratio != 50 {
		t.Errorf("ratio after close = %v, want 50", root.Ratio)
	}
}

// splitWidths returns the width each leaf gets in a total of total DIPs.
func splitWidths(n *splitNode, total float32) []float32 {
	if n.isLeaf() {
		return []float32{total}
	}
	ratio := n.Ratio / 100
	if ratio == 0 {
		ratio = 0.5
	}
	var out []float32
	out = append(out, splitWidths(n.First, total*ratio)...)
	return append(out, splitWidths(n.Second, total*(1-ratio))...)
}

func abs(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}

// A linear history must join each commit to the ones above and below, so
// the lane reads as one line rather than a stub per row.
func TestGraphLaneContinuity(t *testing.T) {
	commits := []service.Commit{
		{Hash: "c", Parents: []string{"b"}},
		{Hash: "b", Parents: []string{"a"}},
		{Hash: "a"},
	}
	lanes := computeLanes(commits)

	if lanes[0].FromAbove {
		t.Error("the newest commit has nothing above it")
	}
	if !lanes[0].Continues {
		t.Error("the newest commit must continue into the row below")
	}
	for i := 1; i < len(commits); i++ {
		if !lanes[i].FromAbove {
			t.Errorf("commit %d is not joined to the row above", i)
		}
	}
	if lanes[len(commits)-1].Continues {
		t.Error("the root commit must not continue below")
	}
}

// A merge allocates a lane for its second parent; that lane needs its own
// descent, while a merge into an existing lane must not repaint it.
func TestGraphMergeLaneFlags(t *testing.T) {
	commits := []service.Commit{
		{Hash: "m", Parents: []string{"b", "f"}}, // merge
		{Hash: "f", Parents: []string{"a"}},
		{Hash: "b", Parents: []string{"a"}},
		{Hash: "a"},
	}
	lanes := computeLanes(commits)

	var newLaneEdges int
	for _, e := range lanes[0].Edges {
		if e.Kind == edgeMerge && e.NewLane {
			newLaneEdges++
		}
	}
	if newLaneEdges != 1 {
		t.Errorf("merge rows with a fresh lane = %d, want 1", newLaneEdges)
	}
}

// A long branch name must ellipsize in place instead of pushing the remote
// actions (fetch/pull/push) past the inspector's edge.
func TestBranchRowLongName(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	r := a.activeRepoRef()
	if r == nil {
		t.Fatal("no repo discovered")
	}
	long := "feature/very-long-branch-name-that-would-overflow-the-inspector-panel"
	cmd := exec.Command("git", "checkout", "-q", "-b", long)
	cmd.Dir = r.Root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout: %s\n%s", err, out)
	}
	fresh, err := service.RepoStatus(r.Root, a.workspace)
	if err != nil {
		t.Fatal(err)
	}
	a.repos = []service.Repo{*fresh}
	a.currentBranch = long
	a.inspectorTab = 2
	a.inspectorVisible = true

	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	// The inspector is 330 wide, flush against the right edge.
	inspectorLeft := float32(1440 - 330)
	branch, ok := tr.Find(long)
	if !ok {
		t.Fatalf("branch name %q not rendered", long)
	}
	if branch.X+branch.W > 1440 {
		t.Errorf("branch trigger runs past the window: right edge %.1f", branch.X+branch.W)
	}
	for _, label := range []string{"Fetch", "Pull", "Push"} {
		btn, ok := tr.Find(label)
		if !ok {
			t.Fatalf("%s button not rendered", label)
		}
		if btn.X < inspectorLeft {
			t.Errorf("%s starts at %.1f, left of the inspector at %.1f", label, btn.X, inspectorLeft)
		}
		if btn.X+btn.W > 1440 {
			t.Errorf("%s is clipped: right edge %.1f past 1440", label, btn.X+btn.W)
		}
		if btn.X < branch.X+branch.W {
			t.Errorf("%s at %.1f overlaps the branch trigger ending at %.1f", label, btn.X, branch.X+branch.W)
		}
	}
}

// The Info card carries the project name, its path and a branch chip in one
// row. A long branch used to take the whole row and squeeze the name column
// to nothing; the text column keeps its space and the chip ellipsizes.
func TestInfoCardLongBranch(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.currentBranch = "feature/very-long-branch-name-that-would-overflow-the-inspector"
	if p := a.project(); p != nil {
		p.Name = "probe-project"
		p.Branch = a.currentBranch
	}
	a.inspectorTab = 0
	a.inspectorVisible = true
	a.sidebarVisible = false // the sidebar shows the name too; isolate the card

	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	name, ok := tr.Find("probe-project")
	if !ok {
		t.Fatal("project name not rendered")
	}
	if name.W < 40 {
		t.Errorf("project name collapsed to %v wide; the branch chip took its space", name.W)
	}
	branch, ok := tr.Find(a.currentBranch)
	if !ok {
		t.Fatal("branch label not rendered")
	}
	if branch.X+branch.W > 1440 {
		t.Errorf("branch label runs past the window: %.1f", branch.X+branch.W)
	}
}
