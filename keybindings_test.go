package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"omaterm/internal/session"
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

// The palette shows a command's key from the same table the window uses.
func TestKeyLabelsComeFromTheTable(t *testing.T) {
	want := func(linux, mac string) string {
		if onMac {
			return mac
		}
		return linux
	}
	if got := keyPrimary("tab.select2"); got != want("Alt+2", "⌘2") {
		t.Errorf("tab.select2 label = %q", got)
	}
	if got := keyPrimary("project.select3"); got != want("Alt+Shift+3", "⌥⌘3") {
		t.Errorf("project.select3 label = %q", got)
	}
	if got := keyPrimary("terminal.splitRight"); got != want("Ctrl+Shift+R", "⇧⌘R") {
		t.Errorf("terminal.splitRight label = %q", got)
	}
	if got := keyPrimary("git.refresh"); got != "" {
		t.Errorf("unbound command label = %q, want empty", got)
	}
	for _, c := range []struct {
		k          keyCombo
		linux, mac string
	}{
		{combo(ui.Ctrl|ui.Shift, ui.KeyP), "Ctrl+Shift+P", "⌃⇧P"},
		{combo(ui.Super|ui.Shift, ui.KeyP), "Shift+Super+P", "⇧⌘P"},
		{combo(ui.Alt, ui.KeyUp), "Alt+Up", "⌥↑"},
		{combo(ui.Super|ui.Shift, ui.KeyEnter), "Shift+Super+Enter", "⇧⌘↩"},
		{combo(ui.Ctrl, ui.KeyBackquote), "Ctrl+`", "⌃`"},
	} {
		if got := c.k.label(false); got != c.linux {
			t.Errorf("label = %q, want %q", got, c.linux)
		}
		if got := c.k.label(true); got != c.mac {
			t.Errorf("mac label = %q, want %q", got, c.mac)
		}
	}
}

// Both platforms' tables are sound, and macOS's leaves Option to type:
// ⌥1 is ¡ there, and [ ] { } @ on German and Nordic layouts, which jumped
// to tabs and projects as they typed; ⌥← and ⌥→ move by words.
func TestMacKeysLeaveOptionToType(t *testing.T) {
	for _, mac := range []bool{false, true} {
		if problems := checkTable(keybindingsFor(mac)); len(problems) > 0 {
			t.Errorf("mac=%v table:\n%s", mac, strings.Join(problems, "\n"))
		}
	}
	find := func(table []keyBinding, id string) keyCombo {
		for _, b := range table {
			if b.Command == id {
				return b.Combo
			}
		}
		t.Fatalf("%s unbound", id)
		return keyCombo{}
	}
	mac, linux := keybindingsFor(true), keybindingsFor(false)
	for _, b := range mac {
		if b.Combo.Mods == ui.Alt && b.Combo.Key != ui.KeyUp && b.Combo.Key != ui.KeyDown {
			t.Errorf("mac: %s on %s, which Option types or moves words with", b.Command, b.Combo.label(true))
		}
		if b.Combo.Mods&ui.Ctrl != 0 {
			t.Errorf("mac: %s on %s, where ⌘ was meant", b.Command, b.Combo.label(true))
		}
	}
	for id, want := range map[string]keyCombo{
		"tab.select1": combo(ui.Super, ui.Key1), "project.select9": combo(ui.Alt|ui.Super, ui.Key9),
		"browser.back": combo(ui.Super, ui.KeyBracketLeft), "browser.forward": combo(ui.Super, ui.KeyBracketRight),
		"tab.close": combo(ui.Super, ui.KeyW),
	} {
		if got := find(mac, id); got != want {
			t.Errorf("mac %s = %s, want %s", id, got.label(true), want.label(true))
		}
	}
	for id, want := range map[string]keyCombo{
		"tab.select1": combo(ui.Alt, ui.Key1), "project.select9": combo(ui.Alt|ui.Shift, ui.Key9),
		"browser.back": combo(ui.Alt, ui.KeyLeft), "tab.close": combo(ui.Ctrl, ui.KeyW),
	} {
		if got := find(linux, id); got != want {
			t.Errorf("%s = %s, want %s", id, got.label(false), want.label(false))
		}
	}
}

// The macOS menu bar keeps ⌘Q, copy and paste, but has no File or View
// menu, whose ⌘W closed the window, quitting omaterm, and whose ⌘R, ⇧⌘R
// and zoom keys did nothing: the menu's keys come before the window's.
func TestAppMenuLeavesTheAppItsKeys(t *testing.T) {
	var roles []mygo.MenuRole
	for _, it := range appMenu().Items() {
		roles = append(roles, it.Role)
	}
	want := []mygo.MenuRole{mygo.RoleAppMenu, mygo.RoleEditMenu, mygo.RoleWindowMenu}
	if !slices.Equal(roles, want) {
		t.Fatalf("menu roles = %v, want %v", roles, want)
	}
}

// On macOS the shortcuts filter finds ⇧⌘D by "cmd+shift+d", in any order,
// as on Linux "ctrl+shift+d" finds Ctrl+Shift+D: the labels are glyphs.
func TestKeymapFilterSpelledKeys(t *testing.T) {
	cmd := Command{ID: "terminal.splitDown", Title: "Split Down", Category: "Terminal"}
	mac := []keyBinding{{cmd.ID, combo(ui.Super|ui.Shift, ui.KeyD), scopeTerminal}}
	linux := []keyBinding{{cmd.ID, combo(ui.Ctrl|ui.Shift, ui.KeyD), scopeTerminal}}
	was := onMac
	defer func() { onMac = was }()
	onMac = true
	for _, q := range []string{"cmd+shift+d", "shift+cmd+d", "⇧⌘d", "split cmd+d", "command+d"} {
		if !keymapMatches(cmd, mac, q) {
			t.Errorf("mac: %q missed ⇧⌘D", q)
		}
	}
	for _, q := range []string{"ctrl+shift+d", "cmd+shift+e", "alt+d"} {
		if keymapMatches(cmd, mac, q) {
			t.Errorf("mac: %q found ⇧⌘D", q)
		}
	}
	onMac = false
	for _, q := range []string{"ctrl+shift+d", "shift+ctrl+d", "ctrl+shift", "split"} {
		if !keymapMatches(cmd, linux, q) {
			t.Errorf("%q missed Ctrl+Shift+D", q)
		}
	}
	if keymapMatches(cmd, linux, "cmd+d") {
		t.Error(`"cmd+d" found Ctrl+Shift+D off macOS`)
	}
}

// The key after the editor's Ctrl+K (⌘K) is the chord's, as in VS Code:
// Ctrl+K Ctrl+0 folds everything rather than zooming back, and Ctrl+K
// Ctrl+1 folds a level rather than going to the first tab (⌘1 on macOS).
// After the chord, the window's keys are the window's again.
func TestEditorChordKeepsItsSecondKey(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	a.openEditor("hello.txt")
	tr.Frame()
	tr.ClickAt(720, 300)
	tr.Frame()
	doc := a.activeEditor()
	if doc == nil || !doc.State.Focused() {
		t.Fatal("the editor has no focus")
	}

	tr.Key(ui.Cmd, ui.KeyEqual)
	tr.Frame()
	zoomed := a.fontSize
	tr.Key(ui.Cmd, ui.KeyK)
	tr.Frame()
	tr.Key(ui.Cmd, ui.Key0)
	tr.Frame()
	if a.fontSize != zoomed {
		t.Fatalf("Ctrl+K Ctrl+0 zoomed back: size %v, want %v", a.fontSize, zoomed)
	}
	if a.editorChord != chordNone {
		t.Fatal("the chord is still waiting after its second key")
	}

	tab := a.activeTab
	tr.Key(ui.Cmd, ui.KeyK)
	tr.Frame()
	tr.Key(ui.Cmd, ui.Key1)
	tr.Frame()
	if a.activeTab != tab {
		t.Fatalf("Ctrl+K Ctrl+1 went to tab %d", a.activeTab)
	}

	tr.Key(ui.Cmd, ui.Key0)
	tr.Frame()
	if a.fontSize != 0 {
		t.Fatalf("Ctrl+0 after the chord: size %v, want the default", a.fontSize)
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
func TestTabShortcutsJump(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	a.openEditor("hello.txt")
	tr.Frame()
	if len(a.tabs) < 2 {
		t.Fatalf("want at least two tabs, got %d", len(a.tabs))
	}
	// Alt+1 and Alt+2 (⌘1 and ⌘2 on macOS) jump straight to the first
	// and second tab.
	tabMods, _ := digitMods(onMac)
	tr.Key(tabMods, ui.Key1)
	tr.Frame()
	if a.activeTab != 0 {
		t.Fatalf("Alt+1 went to tab %d, want 0", a.activeTab)
	}
	if a.activeView != a.tabs[0].Kind {
		t.Errorf("Alt+1 showed view %v, want %v", a.activeView, a.tabs[0].Kind)
	}
	tr.Key(tabMods, ui.Key2)
	tr.Frame()
	if a.activeTab != 1 {
		t.Fatalf("Alt+2 went to tab %d, want 1", a.activeTab)
	}
	// A tab that does not exist is not an error, just a no-op.
	tr.Key(tabMods, ui.Key9)
	tr.Frame()
	if a.activeTab != 1 {
		t.Fatalf("Alt+9 moved to tab %d", a.activeTab)
	}
}

// Alt+Shift+N switches projects, and saves the choice for the next launch.
func TestProjectShortcuts(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.projects = append(a.projects,
		project{Name: "second", Path: t.TempDir(), Tabs: []string{"dev"}},
		project{Name: "third", Path: t.TempDir(), Tabs: []string{"dev"}})
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	_, projectMods := digitMods(onMac) // ⌥⌘N on macOS
	tr.Key(projectMods, ui.Key2)
	tr.Frame()
	if a.active != 1 {
		t.Fatalf("Alt+Shift+2 went to project %d, want 1", a.active)
	}
	if got := a.workspace; got != a.projects[1].Path {
		t.Errorf("workspace = %q, want %q", got, a.projects[1].Path)
	}
	tr.Key(projectMods, ui.Key3)
	tr.Frame()
	if a.active != 2 {
		t.Fatalf("Alt+Shift+3 went to project %d, want 2", a.active)
	}
	// A project that does not exist is a no-op.
	tr.Key(projectMods, ui.Key9)
	tr.Frame()
	if a.active != 2 {
		t.Fatalf("Alt+Shift+9 moved to project %d", a.active)
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

// Alt+N reaches the editor and diff tabs too, and Ctrl+Shift+P opens the
// palette, all from the binding table.
func TestViewAndPaletteShortcuts(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	a.openEditor("hello.txt")
	a.focusDiffTab("hello.txt")
	tr.Frame()
	if a.activeView != tabDiff {
		t.Fatal("diff view not active")
	}
	// The diff tab is last, the editor before it, the terminal first.
	last := len(a.tabs) - 1
	tabMods, _ := digitMods(onMac)
	tr.Key(tabMods, ui.Key1)
	tr.Frame()
	if a.activeView != tabTerminal {
		t.Fatalf("Alt+1 showed %v, want the terminal", a.activeView)
	}
	tr.Key(tabMods, tabDigits[last])
	tr.Frame()
	if a.activeView != tabDiff {
		t.Fatalf("Alt+%d showed %v, want the diff", last+1, a.activeView)
	}

	tr.Key(ui.Cmd|ui.Shift, ui.KeyP)
	tr.Frame()
	if a.overlay != overlayCommandPalette {
		t.Fatalf("Ctrl+Shift+P opened overlay %v", a.overlay)
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
	a.keyQuery = "ctrl+shift+r"
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

// Ctrl+W still closes the active tab from the terminal, an omaterm choice:
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

// In a split, Ctrl+W closes the pane the keyboard is in, as its × does,
// and the last pane the tab: it ended every shell of the split, and the
// session beside a browser pane closed to close the page.
func TestCloseKeyClosesThePane(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	before := len(a.tabs)
	shell := a.activeSplitLeaf()
	tr.Key(ui.Cmd|ui.Shift, ui.KeyD)
	tr.Frame()
	if n := a.activeSplit().leafCount(); n != 2 {
		t.Fatalf("after Split Down: %d panes", n)
	}
	tr.Key(ui.Cmd, ui.KeyW)
	tr.Frame()
	if len(a.tabs) != before || a.activeSplit().leafCount() != 1 {
		t.Fatalf("Ctrl+W in a split: tabs = %d (want %d), panes = %d (want 1)",
			len(a.tabs), before, a.activeSplit().leafCount())
	}
	if a.activeSplitLeaf() != shell {
		t.Fatalf("the first shell went: focus %q, want %q", a.activeSplitLeaf(), shell)
	}

	// A browser pane goes alone, the session's shell with it staying.
	a.openBrowser("localhost:5173")
	page := a.activeSplitLeaf()
	if !a.isBrowser(page) {
		t.Fatal("the browser pane has no focus")
	}
	commandsByID["tab.close"].Run(a, nil)
	if a.isBrowser(page) || len(a.tabs) != before || a.activeSplitLeaf() != shell {
		t.Fatalf("closing the page: browser kept=%v tabs=%d focus=%q", a.isBrowser(page), len(a.tabs), a.activeSplitLeaf())
	}

	tr.Key(ui.Cmd, ui.KeyW)
	tr.Frame()
	if len(a.tabs) != before-1 {
		t.Fatalf("Ctrl+W on the last pane: tabs = %d, want %d", len(a.tabs), before-1)
	}
}

// Quitting with no daemon ends the sessions' programs: it asks first, as
// closing a session does; a daemon's panes go on, and quit unasked.
func TestQuitAsksBeforeEndingPrograms(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	if !a.panes.Owned() {
		t.Skip("panes run in a daemon")
	}
	id := a.activeSplitLeaf()
	a.status = map[string]session.Info{id: {ID: id, Status: session.Working, Process: "sleep"}}

	e := &mygo.CloseEvent{}
	a.beforeClose(e)
	if !e.DefaultPrevented() || a.quitAsk == nil {
		t.Fatal("quit with sleep running did not ask")
	}
	if got := a.quitText("Quit"); got != "sleep is still running. Quitting ends it." {
		t.Errorf("dialog text = %q", got)
	}

	a.quitAsk, a.quitWant = nil, ""
	a.settings.ConfirmClose = false
	e = &mygo.CloseEvent{}
	a.beforeClose(e)
	if e.DefaultPrevented() {
		t.Fatal("asked with Confirm Close off")
	}

	a.settings.ConfirmClose = true
	a.status = map[string]session.Info{id: {ID: id, Status: session.Idle}}
	e = &mygo.CloseEvent{}
	a.beforeClose(e)
	if e.DefaultPrevented() {
		t.Fatal("asked with only a shell running")
	}
}

// Ctrl+=, Ctrl+- and Ctrl+0 change the terminal's and the editor's text
// size together, and stay within the range.
func TestZoomShortcuts(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	a.openEditor("hello.txt")
	tr.Frame()
	doc := a.activeEditor()
	if doc == nil || doc.State == nil {
		t.Fatal("editor did not open")
	}
	if doc.State.Font.Size != 0 {
		t.Fatalf("default editor size = %v, want 0 (the theme's)", doc.State.Font.Size)
	}

	tr.Key(ui.Cmd, ui.KeyEqual)
	tr.Frame()
	if a.fontSize != defaultFontSize+1 {
		t.Fatalf("after one step fontSize = %v", a.fontSize)
	}
	if doc.State.Font.Size != a.fontSize {
		t.Errorf("editor size = %v, want %v", doc.State.Font.Size, a.fontSize)
	}

	tr.Key(ui.Cmd, ui.KeyMinus)
	tr.Frame()
	tr.Key(ui.Cmd, ui.KeyMinus)
	tr.Frame()
	if a.fontSize != defaultFontSize-1 {
		t.Fatalf("after two steps down fontSize = %v", a.fontSize)
	}
	if doc.State.Font.Size != a.fontSize {
		t.Errorf("editor size = %v, want %v", doc.State.Font.Size, a.fontSize)
	}

	// It stops at the ends of the range rather than shrinking to nothing.
	for i := 0; i < 40; i++ {
		tr.Key(ui.Cmd, ui.KeyMinus)
		tr.Frame()
	}
	if a.fontSize != minFontSize {
		t.Fatalf("fontSize = %v, want the floor %v", a.fontSize, minFontSize)
	}
	for i := 0; i < 60; i++ {
		tr.Key(ui.Cmd, ui.KeyEqual)
		tr.Frame()
	}
	if a.fontSize != maxFontSize {
		t.Fatalf("fontSize = %v, want the ceiling %v", a.fontSize, maxFontSize)
	}

	tr.Key(ui.Cmd, ui.Key0)
	tr.Frame()
	if a.fontSize != 0 {
		t.Fatalf("Ctrl+0 left fontSize = %v, want the default", a.fontSize)
	}
	if doc.State.Font.Size != 0 {
		t.Errorf("editor size = %v after reset, want 0", doc.State.Font.Size)
	}
}

// Ctrl+Shift+I, E and G show the three inspector panels.
func TestInspectorPanelShortcuts(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.inspectorVisible = false
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	for _, tc := range []struct {
		key  ui.Key
		want int
		name string
	}{
		{ui.KeyI, 0, "Info"},
		{ui.KeyE, 1, "Files"},
		{ui.KeyG, 2, "Git"},
	} {
		a.inspectorVisible = false
		tr.Key(ui.Cmd|ui.Shift, tc.key)
		tr.Frame()
		if !a.inspectorVisible {
			t.Errorf("Ctrl+Shift+%s did not show the inspector", tc.name)
		}
		if a.inspectorTab != tc.want {
			t.Errorf("Ctrl+Shift+%s: tab = %d, want %d", tc.name, a.inspectorTab, tc.want)
		}
	}
}

// Alt+Up and Down are the editor's in an editor (move lines), and switch
// sessions elsewhere: they switched sessions in the editor too.
func TestAltArrowsEditorsOwn(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	var next keyBinding
	for _, b := range keybindings {
		if b.Command == "session.next" {
			next = b
		}
	}
	a.activeView = tabTerminal
	if !a.keyActive(next) {
		t.Error("Alt+Down doesn't switch sessions from a terminal")
	}
	a.activeView = tabEditor
	if a.keyActive(next) {
		t.Error("Alt+Down switches sessions in the editor")
	}
}
