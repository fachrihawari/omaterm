package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"oterm/service"
)

// The binding table must not point at commands that do not exist, and two
// commands must not claim one key in the same state: the runtime would
// silently give the key to whichever registered first.
func TestKeybindingTableIsConsistent(t *testing.T) {
	if problems := checkKeybindings(); len(problems) > 0 {
		t.Fatalf("binding table:\n%s", strings.Join(problems, "\n"))
	}
	for _, b := range keybindings {
		if b.Combo.Label() == "" {
			t.Errorf("%s: empty key label", b.Command)
		}
		if b.Combo.Key == 0 {
			t.Errorf("%s: binding without a key", b.Command)
		}
	}
}

// A command may have several keys (Ctrl+Tab and Ctrl+PageDown), and the
// palette must show them from the same table.
func TestKeyLabelsComeFromTheTable(t *testing.T) {
	if got := keyPrimary("tab.next"); got != "Ctrl+Tab" {
		t.Errorf("tab.next label = %q", got)
	}
	if got := keyLabels("tab.next"); got != "Ctrl+Tab, Ctrl+PageDown" {
		t.Errorf("tab.next labels = %q", got)
	}
	if got := keyPrimary("terminal.splitRight"); got != "Ctrl+Shift+R" {
		t.Errorf("terminal.splitRight label = %q", got)
	}
	if got := keyLabels("terminal.splitRight"); got != "Ctrl+Shift+R, Ctrl+Shift+5" {
		t.Errorf("terminal.splitRight labels = %q", got)
	}
	if got := keyPrimary("git.refresh"); got != "" {
		t.Errorf("unbound command label = %q, want empty", got)
	}
	if got := combo(ui.Ctrl|ui.Shift, ui.KeyP).Label(); got != "Ctrl+Shift+P" {
		t.Errorf("label = %q", got)
	}
	if got := combo(ui.Alt, ui.KeyUp).Label(); got != "Alt+Up" {
		t.Errorf("label = %q", got)
	}
	if got := combo(ui.Cmd, ui.KeyBackquote).Label(); got != "Ctrl+`" {
		t.Errorf("label = %q", got)
	}
}

// Ctrl+S writes the editor's buffer, as VS Code's Save does.
func TestSaveShortcut(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.openEditor("hello.txt")
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	doc := a.activeEditor()
	if doc == nil || doc.State == nil {
		t.Fatal("editor did not open")
	}
	doc.State.Buffer.Set("saved by the shortcut\n")
	tr.Frame()
	if !doc.State.Buffer.Modified() {
		t.Fatal("buffer should be dirty before saving")
	}

	tr.Key(ui.Cmd, ui.KeyS)
	tr.Frame()

	data, err := os.ReadFile(filepath.Join(a.workspace, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "saved by the shortcut\n" {
		t.Errorf("file = %q", data)
	}
	if doc.State.Buffer.Modified() {
		t.Error("buffer still marked modified after Ctrl+S")
	}
}

// Ctrl+Tab and Ctrl+Shift+Tab walk the tab bar, wrapping at the ends.
func TestTabShortcutsCycle(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	a.openEditor("hello.txt")
	tr.Frame()
	if len(a.tabs) < 2 {
		t.Fatalf("want at least two tabs, got %d", len(a.tabs))
	}
	first := a.activeTab

	tr.Key(ui.Cmd, ui.KeyTab)
	tr.Frame()
	if a.activeTab == first {
		t.Fatal("Ctrl+Tab did not change tabs")
	}
	tr.Key(ui.Cmd|ui.Shift, ui.KeyTab)
	tr.Frame()
	if a.activeTab != first {
		t.Fatalf("Ctrl+Shift+Tab = %d, want %d", a.activeTab, first)
	}
	// It wraps rather than sticking at the last tab.
	for i := 0; i < len(a.tabs); i++ {
		tr.Key(ui.Cmd, ui.KeyTab)
		tr.Frame()
	}
	if a.activeTab != first {
		t.Fatalf("cycling through every tab ended at %d, want %d", a.activeTab, first)
	}
}

// Ctrl+Shift+E and Ctrl+Shift+G open the Files and Git panels, as VS Code's
// Explorer and Source Control views.
func TestInspectorShortcuts(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.inspectorVisible = false
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	tr.Key(ui.Cmd|ui.Shift, ui.KeyG)
	tr.Frame()
	if !a.inspectorVisible || a.inspectorTab != 2 {
		t.Fatalf("Ctrl+Shift+G: visible=%v tab=%d", a.inspectorVisible, a.inspectorTab)
	}

	tr.Key(ui.Cmd|ui.Shift, ui.KeyE)
	tr.Frame()
	if a.inspectorTab != 1 {
		t.Fatalf("Ctrl+Shift+E: tab=%d, want Files", a.inspectorTab)
	}
}

// Ctrl+` shows the terminal, F1 opens the palette, and both come from the
// binding table.
func TestViewAndPaletteShortcuts(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	a.openEditor("hello.txt")
	tr.Frame()
	if a.activeView != tabEditor {
		t.Fatal("editor view not active")
	}
	tr.Key(ui.Cmd, ui.KeyBackquote)
	tr.Frame()
	if a.activeView != tabTerminal {
		t.Fatal("Ctrl+` did not show the terminal")
	}
	// And Ctrl+` again returns to the editor, as the panel toggle does.
	tr.Key(ui.Cmd, ui.KeyBackquote)
	tr.Frame()
	if a.activeView != tabEditor {
		t.Fatalf("Ctrl+` again left the view at %v", a.activeView)
	}
	tr.Key(ui.Cmd, ui.KeyBackquote)
	tr.Frame()

	tr.Key(0, ui.KeyF1)
	tr.Frame()
	if a.overlay != overlayCommandPalette {
		t.Fatalf("F1 opened overlay %v", a.overlay)
	}
	tr.Key(0, ui.KeyEscape)
	tr.Frame()
	if a.overlay != overlayNone {
		t.Fatal("Escape did not close the palette")
	}
}

// The Keyboard Shortcuts overlay lists the table, filters on command names
// and on key labels, and runs the chosen command.
func TestKeyboardShortcutsOverlay(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	tr.Key(ui.Cmd|ui.Shift, ui.KeySlash)
	tr.Frame()
	if a.overlay != overlayShortcuts {
		t.Fatalf("Ctrl+Shift+/ opened overlay %v", a.overlay)
	}
	if !tr.HasText("Keyboard Shortcuts") && !tr.HasText("COMMAND") {
		t.Error("shortcuts dialog did not render its headings")
	}
	if !tr.HasText("Go to File…") || !tr.HasText("Ctrl+P") {
		t.Error("shortcuts dialog is missing a bound command")
	}

	// Search by command name.
	tr.Type("split")
	tr.Frame()
	items := a.keymapItems()
	if len(items) != 2 {
		t.Fatalf("query \"split\" found %d commands", len(items))
	}
	for _, it := range items {
		if !strings.HasPrefix(it.Title, "Split Terminal") {
			t.Errorf("unexpected match %q", it.Title)
		}
	}

	// Search by key.
	a.keyQuery = "ctrl+shift+5"
	tr.Frame()
	items = a.keymapItems()
	if len(items) != 1 || items[0].ID != "terminal.splitRight" {
		t.Fatalf("query by key found %+v", items)
	}

	// The arrows move the highlight, and it stays in view.
	a.keyQuery = ""
	tr.Frame()
	a.keySelected = 0
	tr.Key(0, ui.KeyDown)
	tr.Frame()
	if a.keySelected != 1 {
		t.Fatalf("Down moved the selection to %d", a.keySelected)
	}
	tr.Key(0, ui.KeyUp)
	tr.Frame()
	if a.keySelected != 0 {
		t.Fatalf("Up moved the selection to %d", a.keySelected)
	}

	// Enter runs the highlighted command.
	a.keyQuery = "toggle sidebar"
	tr.Frame()
	items = a.keymapItems()
	if len(items) == 0 || items[0].ID != "view.toggleSidebar" {
		t.Fatalf("query found %+v", items)
	}
	before := a.sidebarVisible
	tr.Key(0, ui.KeyEnter)
	tr.Frame()
	if a.overlay != overlayNone {
		t.Fatal("Enter did not close the shortcuts dialog")
	}
	if a.sidebarVisible == before {
		t.Fatal("Enter did not run the highlighted command")
	}
}

// Alt+arrow moves the keyboard between split panes, as VS Code's terminal
// pane navigation does.
func TestPaneFocusShortcuts(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	tr.Key(ui.Cmd|ui.Shift, ui.KeyD)
	tr.Frame()
	sp := a.activeSplit()
	if sp == nil || sp.isLeaf() {
		t.Fatal("Ctrl+Shift+D did not split the terminal")
	}
	ids := sp.termIDs()
	if len(ids) != 2 {
		t.Fatalf("panes = %d", len(ids))
	}
	// The split gives the keyboard to the new pane, which is below.
	if a.activePaneTermID != ids[1] || a.paneFocus != ids[1] {
		t.Fatalf("new pane not focused: active=%q focus=%q panes=%v",
			a.activePaneTermID, a.paneFocus, ids)
	}

	tr.Key(ui.Alt, ui.KeyUp)
	tr.Frame()
	if a.activePaneTermID != ids[0] || a.paneFocus != ids[0] {
		t.Fatalf("Alt+Up did not move up: active=%q", a.activePaneTermID)
	}
	tr.Key(ui.Alt, ui.KeyDown)
	tr.Frame()
	if a.activePaneTermID != ids[1] {
		t.Fatalf("Alt+Down did not move down: active=%q", a.activePaneTermID)
	}
	// There is nothing to the right of a top/bottom split.
	tr.Key(ui.Alt, ui.KeyRight)
	tr.Frame()
	if a.activePaneTermID != ids[1] {
		t.Fatal("Alt+Right moved to a pane that is not there")
	}
}

// While a terminal pane has the keyboard, keys the shell needs stay with
// the shell, as VS Code's terminal.integrated.commandsToSkipShell does.
func TestTerminalKeepsShellKeys(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !a.terminalHasKeyboard() {
		t.Fatal("the terminal pane should have the keyboard")
	}

	active := func(id string) bool {
		for _, b := range bindingsFor(id) {
			if a.keyActive(b) {
				return true
			}
		}
		return false
	}
	// The shell gets Ctrl+S and Ctrl+Q.
	if active("file.save") || active("app.quit") {
		t.Error("a key the shell needs is still taken by the app")
	}
	// Quick open and the palette still skip the shell, as in VS Code.
	for _, id := range []string{"file.quickOpen", "app.commandPalette", "app.openProject", "view.toggleSidebar", "terminal.splitDown"} {
		if !active(id) {
			t.Errorf("%s should work from the terminal", id)
		}
	}

	// Away from the terminal the same keys are the app's. The next frame
	// clears the terminal's keyboard flag.
	a.activeView = tabEditor
	tr.Frame()
	if !active("file.save") || !active("app.quit") {
		t.Error("Ctrl+S/Ctrl+Q should be the app's outside the terminal")
	}
}

// Ctrl+W still closes the active tab from the terminal, an oterm choice:
// closing a tab is the app's job even while the shell has the keyboard.
func TestCloseTabFromTerminal(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	before := len(a.tabs)
	tr.Key(ui.Cmd, ui.KeyW)
	tr.Frame()
	if len(a.tabs) != before-1 {
		t.Fatalf("Ctrl+W in the terminal: tabs = %d, want %d", len(a.tabs), before-1)
	}
}

// Ctrl+Enter commits from the Git panel once it is open with a message and
// staged files, as VS Code's Source Control commit key does.
func TestCommitShortcut(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	r := a.activeRepoRef()
	if r == nil {
		t.Fatal("test app has no repo")
	}
	if err := service.Stage(r.Root, "hello.txt"); err != nil {
		t.Fatal(err)
	}
	a.repos[0].Staged = []service.GitFile{{Path: "hello.txt", Status: "M", Staged: true}}
	a.repos[0].Changes = nil
	a.commitMsg = "commit from the keyboard"
	a.inspectorVisible = true
	a.showInspectorTab(2)

	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	// Clicking the panel takes the keyboard from the terminal pane, as
	// clicking the commit field does: Ctrl+Enter is the app's there, and
	// the shell's while the terminal has it.
	if err := tr.Click("Git"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if a.terminalHasKeyboard() {
		t.Fatal("the terminal should not have the keyboard in the Git panel")
	}
	head := service.HeadSha(r.Root)
	tr.Key(ui.Cmd, ui.KeyEnter)
	tr.Frame()
	if got := service.HeadSha(r.Root); got == head {
		t.Fatal("Ctrl+Enter did not commit")
	}
	if a.commitMsg != "" {
		t.Errorf("commit message not cleared: %q", a.commitMsg)
	}
}
