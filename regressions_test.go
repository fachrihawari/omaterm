package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"oterm/service"
)

// stopTermPanes ends every shell a test started, so it does not outlive the
// test binary.
func stopTermPanes(a *appState) {
	for _, tp := range a.termPanes {
		if tp != nil {
			tp.Close()
		}
	}
}

// The terminal could not be typed into: a project now opens with its first
// pane holding the keyboard, so typing works without clicking into it.
func TestTerminalTyping(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	var term *terminal.Terminal
	for _, tp := range a.termPanes {
		term = tp
		break
	}
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
	if l := a.termLayout(); l != layoutSingle {
		t.Fatalf("new tab layout = %v, want single", l)
	}
	if len(a.termPanes) == 0 {
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

	a.setTermLayout(layoutVertical)
	tr.Frame()
	if a.termLayout() != layoutVertical {
		t.Fatal("the split did not stick to its tab")
	}
	tr.Key(ui.Cmd, ui.KeyT)
	tr.Frame()
	if l := a.termLayout(); l != layoutSingle {
		t.Fatalf("a new tab inherited the layout %v, want single", l)
	}
	a.activeTab = 0
	a.activeView = tabTerminal
	tr.Frame()
	if a.termLayout() != layoutVertical {
		t.Fatal("the first tab lost its split")
	}
}
