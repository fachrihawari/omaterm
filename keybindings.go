package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// Keybindings are declared once, here, and drive three things: the window
// shortcuts, the key chips in the command palette, and the Keyboard
// Shortcuts overlay. A command may have several bindings, and a command may
// have none, which leaves it to the palette and the UI.
//
// The scheme is short enough to remember. Ctrl does things: it opens what
// has a name (P for file, T for terminal, E/G/I for the panels) and Shift
// picks the other one of a pair (B the other sidebar, D/R the split below
// and right). Alt goes places: Alt+N jumps to the nth tab, Alt+Shift+N to
// the nth project. There are no chords and no function keys.
//
// On macOS ⌘ takes Ctrl's place, and Option, which types characters there
// (⌥1 is ¡, and [ ] { } @ on German and Nordic layouts), goes places only
// with the arrows or with ⌘: ⌘N jumps to a tab and ⌥⌘N to a project, one
// modifier more as Shift is elsewhere (⇧⌘3, 4 and 5 are the system's
// screenshots), and a browser pane goes back and forward with ⌘[ and ⌘],
// as Safari does.

// keyCombo is one key with the modifiers held, as a shortcut is pressed.
type keyCombo struct {
	Mods ui.Modifiers
	Key  ui.Key
}

func combo(mods ui.Modifiers, key ui.Key) keyCombo {
	return keyCombo{Mods: mods, Key: key}
}

// onMac tells whether the keys follow macOS, where ⌘ is the command key.
var onMac = ui.Cmd == ui.Super

// Label writes the combo as the UI shows it: "Ctrl+Shift+P" on Linux,
// "⇧⌘P" on macOS, where the modifiers are glyphs and ⌘ comes first.
func (k keyCombo) Label() string { return k.label(onMac) }

// label writes the combo as Label does, for macOS or the others.
func (k keyCombo) label(mac bool) string {
	var b strings.Builder
	if mac {
		for _, m := range []struct {
			mod  ui.Modifiers
			name string
		}{{ui.Ctrl, "⌃"}, {ui.Alt, "⌥"}, {ui.Shift, "⇧"}, {ui.Super, "⌘"}} {
			if k.Mods&m.mod != 0 {
				b.WriteString(m.name)
			}
		}
	} else {
		for _, m := range []struct {
			mod  ui.Modifiers
			name string
		}{{ui.Ctrl, "Ctrl+"}, {ui.Alt, "Alt+"}, {ui.Shift, "Shift+"}, {ui.Super, "Super+"}} {
			if k.Mods&m.mod != 0 {
				b.WriteString(m.name)
			}
		}
	}
	b.WriteString(keyLabel(k.Key, mac))
	return b.String()
}

// macKeyGlyphs are the keys macOS menus draw as a glyph, not a word.
var macKeyGlyphs = map[ui.Key]string{
	ui.KeyEnter: "↩", ui.KeyEscape: "⎋", ui.KeyBackspace: "⌫", ui.KeyTab: "⇥",
	ui.KeyDelete: "⌦", ui.KeyLeft: "←", ui.KeyRight: "→", ui.KeyUp: "↑", ui.KeyDown: "↓",
	ui.KeyPageUp: "⇞", ui.KeyPageDown: "⇟", ui.KeyHome: "↖", ui.KeyEnd: "↘",
}

// keyLabel names a key as VS Code does: letters in upper case, and words
// for the keys that type nothing, which macOS draws as glyphs.
func keyLabel(k ui.Key, mac bool) string {
	if g, ok := macKeyGlyphs[k]; ok && mac {
		return g
	}
	switch {
	case k >= ui.KeyA && k <= ui.KeyZ:
		return string(rune('A' + k - ui.KeyA))
	case k >= ui.Key0 && k <= ui.Key9:
		return string(rune('0' + k - ui.Key0))
	case k >= ui.KeyF1 && k <= ui.KeyF12:
		return "F" + itoa(int(k-ui.KeyF1)+1)
	}
	switch k {
	case ui.KeyEnter:
		return "Enter"
	case ui.KeyEscape:
		return "Esc"
	case ui.KeyBackspace:
		return "Backspace"
	case ui.KeyTab:
		return "Tab"
	case ui.KeySpace:
		return "Space"
	case ui.KeyDelete:
		return "Delete"
	case ui.KeyInsert:
		return "Insert"
	case ui.KeyHome:
		return "Home"
	case ui.KeyEnd:
		return "End"
	case ui.KeyPageUp:
		return "PageUp"
	case ui.KeyPageDown:
		return "PageDown"
	case ui.KeyLeft:
		return "Left"
	case ui.KeyRight:
		return "Right"
	case ui.KeyUp:
		return "Up"
	case ui.KeyDown:
		return "Down"
	case ui.KeyMinus:
		return "-"
	case ui.KeyEqual:
		return "="
	case ui.KeyComma:
		return ","
	case ui.KeyPeriod:
		return "."
	case ui.KeySlash:
		return "/"
	case ui.KeySemicolon:
		return ";"
	case ui.KeyQuote:
		return "'"
	case ui.KeyBracketLeft:
		return "["
	case ui.KeyBracketRight:
		return "]"
	case ui.KeyBackslash:
		return "\\"
	case ui.KeyBackquote:
		return "`"
	}
	return ""
}

// keyScope says when a binding applies. The Keyboard Shortcuts overlay
// shows it in its "When" column.
type keyScope int

const (
	// scopeGlobal bindings work wherever the keyboard is.
	scopeGlobal keyScope = iota
	// scopeTerminal bindings work while the terminal view is shown.
	scopeTerminal
	// scopeEditor bindings work while an editor tab is shown.
	scopeEditor
	// scopeBrowser bindings work while a browser pane has the keyboard;
	// they take keys a terminal would otherwise get (Ctrl+L, Ctrl+R).
	scopeBrowser
	// scopeNotEditor bindings work but in an editor, whose own keys they
	// are there (Alt+Up and Down move lines).
	scopeNotEditor
)

// String names the scope as the shortcuts overlay shows it.
func (s keyScope) String() string {
	switch s {
	case scopeTerminal:
		return "Terminal"
	case scopeEditor:
		return "Editor"
	case scopeBrowser:
		return "Browser"
	}
	return "Anywhere"
}

// keyBinding ties a command to one key.
type keyBinding struct {
	Command string
	Combo   keyCombo
	Scope   keyScope
}

// keybindings is the default table, for the platform omaterm runs on.
var keybindings = keybindingsFor(onMac)

// keybindingsFor builds the table for macOS or the others. A command may
// have no binding at all, which leaves it to the palette and the UI.
func keybindingsFor(mac bool) []keyBinding {
	cmd := ui.Ctrl
	if mac {
		cmd = ui.Super
	}
	// Back and forward: Alt and the arrows, but ⌘[ and ⌘] on macOS, where
	// ⌥← and ⌥→ move by words in a page's text fields.
	back, forward := combo(ui.Alt, ui.KeyLeft), combo(ui.Alt, ui.KeyRight)
	if mac {
		back, forward = combo(cmd, ui.KeyBracketLeft), combo(cmd, ui.KeyBracketRight)
	}
	return append([]keyBinding{
		// App.
		{"file.quickOpen", combo(cmd, ui.KeyP), scopeGlobal},
		{"app.commandPalette", combo(cmd|ui.Shift, ui.KeyP), scopeGlobal},
		{"search.global", combo(cmd|ui.Shift, ui.KeyF), scopeGlobal},
		{"app.openProject", combo(cmd, ui.KeyO), scopeGlobal},
		{"help.keyboardShortcuts", combo(cmd|ui.Shift, ui.KeySlash), scopeGlobal},
		{"app.settings", combo(cmd, ui.KeyComma), scopeGlobal},

		// Zoom, of the terminal and the editor alike.
		{"app.zoomIn", combo(cmd, ui.KeyEqual), scopeGlobal},
		{"app.zoomOut", combo(cmd, ui.KeyMinus), scopeGlobal},
		{"app.zoomReset", combo(cmd, ui.Key0), scopeGlobal},

		// The side panels: Ctrl+B toggles one, Shift the other.
		{"view.toggleSidebar", combo(cmd, ui.KeyB), scopeGlobal},
		{"view.toggleInspector", combo(cmd|ui.Shift, ui.KeyB), scopeGlobal},
		{"view.files", combo(cmd|ui.Shift, ui.KeyE), scopeGlobal},
		{"view.git", combo(cmd|ui.Shift, ui.KeyG), scopeGlobal},
		{"view.info", combo(cmd|ui.Shift, ui.KeyI), scopeGlobal},
		{"view.changes", combo(cmd|ui.Shift, ui.KeyA), scopeGlobal},

		// Tabs. Ctrl+W closes the pane, or the tab with one pane, even
		// from the terminal: closing is the app's job in a terminal-first
		// app.
		{"tab.close", combo(cmd, ui.KeyW), scopeGlobal},

		// Terminal.
		{"terminal.new", combo(cmd, ui.KeyT), scopeGlobal},
		{"terminal.splitDown", combo(cmd|ui.Shift, ui.KeyD), scopeTerminal},
		{"terminal.splitRight", combo(cmd|ui.Shift, ui.KeyR), scopeTerminal},
		{"pane.zoom", combo(cmd|ui.Shift, ui.KeyEnter), scopeTerminal},

		// Browser panes: Ctrl+Shift+O opens one (Ctrl+O opens a project),
		// and the browser's own keys work while one has the keyboard.
		{"browser.open", combo(cmd|ui.Shift, ui.KeyO), scopeGlobal},
		{"browser.focusAddress", combo(cmd, ui.KeyL), scopeBrowser},
		{"browser.reload", combo(cmd, ui.KeyR), scopeBrowser},
		{"browser.back", back, scopeBrowser},
		{"browser.forward", forward, scopeBrowser},
		{"browser.find", combo(cmd, ui.KeyF), scopeBrowser},

		// Sessions: the one that needs you, and the next and previous in
		// the sidebar's order.
		{"session.attention", combo(cmd|ui.Shift, ui.KeyJ), scopeGlobal},
		{"session.next", combo(ui.Alt, ui.KeyDown), scopeNotEditor},
		{"session.prev", combo(ui.Alt, ui.KeyUp), scopeNotEditor},

		// Editor.
		{"file.save", combo(cmd, ui.KeyS), scopeEditor},
	}, digitBindings(mac)...)
}

// tabDigits are the keys Alt+N jumps to a tab with, and Alt+Shift+N to a
// project. They are the row above the letters, in order.
var tabDigits = []ui.Key{
	ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5,
	ui.Key6, ui.Key7, ui.Key8, ui.Key9,
}

// digitCommands adds the commands Alt+N and Alt+Shift+N run: one per digit,
// for the tab and the project at that place.
func digitCommands() []Command {
	out := make([]Command, 0, 2*len(tabDigits))
	for i := range tabDigits {
		n := i + 1
		out = append(out,
			Command{
				ID:       "tab.select" + itoa(n),
				Title:    "Go to Tab " + itoa(n),
				Category: "Tab",
				Group:    "Go to Tab",
				Run:      func(a *appState, c *ui.Context) { a.activateTab(n - 1) },
			},
			Command{
				ID:       "project.select" + itoa(n),
				Title:    "Switch to Project " + itoa(n),
				Category: "Project",
				Group:    "Switch to Project",
				Enabled:  func(a *appState) bool { return n <= len(a.projects) },
				Run:      func(a *appState, c *ui.Context) { a.switchProject(n - 1) },
			})
	}
	return out
}

// digitMods are the modifiers that, with a digit, go to a tab and to a
// project: Alt and Alt+Shift, but ⌘ and ⌥⌘ on macOS, where Option alone
// types. Not ⌃: it is Mission Control's Switch to Desktop N once on.
func digitMods(mac bool) (tab, project ui.Modifiers) {
	if mac {
		return ui.Super, ui.Alt | ui.Super
	}
	return ui.Alt, ui.Alt | ui.Shift
}

// digitBindings ties those commands to the digits.
func digitBindings(mac bool) []keyBinding {
	tab, project := digitMods(mac)
	out := make([]keyBinding, 0, 2*len(tabDigits))
	for i, k := range tabDigits {
		n := i + 1
		out = append(out,
			keyBinding{"tab.select" + itoa(n), combo(tab, k), scopeGlobal},
			keyBinding{"project.select" + itoa(n), combo(project, k), scopeGlobal})
	}
	return out
}

// commandsByID indexes the palette's commands, so a binding names one.
var commandsByID = func() map[string]*Command {
	m := make(map[string]*Command, len(commandRegistry))
	for i := range commandRegistry {
		m[commandRegistry[i].ID] = &commandRegistry[i]
	}
	return m
}()

// bindingsFor returns the bindings of a command, in table order.
func bindingsFor(id string) []keyBinding {
	var out []keyBinding
	for _, b := range keybindings {
		if b.Command == id {
			out = append(out, b)
		}
	}
	return out
}

// keyLabels writes a command's keys as one label, for tooltips.
func keyLabels(id string) string {
	bs := bindingsFor(id)
	labels := make([]string, len(bs))
	for i, b := range bs {
		labels[i] = b.Combo.Label()
	}
	return strings.Join(labels, ", ")
}

// withKeys adds a command's keys to a tooltip, "Save (Ctrl+S)", or
// leaves it alone for a command with none.
func withKeys(tip, id string) string {
	if k := keyLabels(id); k != "" {
		return tip + " (" + k + ")"
	}
	return tip
}

// keyPrimary writes the command's first key alone, which is the one the
// palette shows: VS Code's palette lists one binding per command, not
// every alias.
func keyPrimary(id string) string {
	bs := bindingsFor(id)
	if len(bs) == 0 {
		return ""
	}
	return bs[0].Combo.Label()
}

// editorChordStep is how far the editor is in a chord of its keys.
type editorChordStep int

const (
	chordNone editorChordStep = iota
	// chordPressed: the chord's first key is down.
	chordPressed
	// chordWaiting: it is up, and the editor waits for the second.
	chordWaiting
)

// editorChordKeys are the first keys of the editor's chords, as ⌘K.
var editorChordKeys = func() map[keyCombo]bool {
	m := map[keyCombo]bool{}
	for _, b := range editor.DefaultBindings() {
		if b.ThenKey != 0 {
			m[combo(b.Mods, b.Key)] = true
		}
	}
	return m
}()

// trackEditorChord follows the editor's chords from the input it is
// offered, which is every key but a chord's second key down, and leaves
// the input to the editor. The window's shortcuts come before the editor:
// ⌘K ⌘0 (Fold All) zoomed back, and ⌘K ⌘1 went to the first tab, leaving
// the chord waiting for the key after.
func (a *appState) trackEditorChord(ev ui.InputEvent) bool {
	if ev.Key == ui.KeyUnknown { // a modifier, as ⌘ let go of
		return false
	}
	switch ev.Kind {
	case ui.InputKeyDown:
		// A key offered is no chord's second: any chord is over.
		a.editorChord, a.editorChordKey = chordNone, 0
		if editorChordKeys[combo(ev.Mods, ev.Key)] {
			a.editorChord, a.editorChordKey = chordPressed, ev.Key
		}
	case ui.InputKeyUp:
		if a.editorChord == chordPressed && ev.Key == a.editorChordKey {
			a.editorChord = chordWaiting
		} else if a.editorChord != chordNone {
			// The second key's release: the editor has run the chord.
			a.editorChord, a.editorChordKey = chordNone, 0
		}
	}
	return false
}

// keyActive reports whether a binding applies in the current state.
func (a *appState) keyActive(b keyBinding) bool {
	if a.editorChord != chordNone && a.activeView == tabEditor {
		if doc := a.activeEditor(); doc != nil && doc.State != nil && doc.State.Focused() {
			return false // the key after an editor's ⌘K is the chord's
		}
	}
	switch b.Scope {
	case scopeTerminal:
		return a.activeView == tabTerminal
	case scopeEditor:
		return a.activeView == tabEditor
	case scopeBrowser:
		return a.focusedWeb() != nil
	case scopeNotEditor:
		return a.activeView != tabEditor
	}
	return true
}

// registerKeybindings installs this frame's window shortcuts. A command
// that is disabled does nothing, as a disabled palette entry does.
func (a *appState) registerKeybindings(c *ui.Context) {
	for _, b := range keybindings {
		if !a.keyActive(b) {
			continue
		}
		cmd, ok := commandsByID[b.Command]
		if !ok {
			continue
		}
		c.OnShortcut(b.Combo.Mods, b.Combo.Key, func() {
			if cmd.Enabled != nil && !cmd.Enabled(a) {
				return
			}
			cmd.Run(a, c)
		})
	}
}

// checkKeybindings reports the table's mistakes: a binding to a command
// that does not exist, or two bindings of one key that can apply at the
// same time. Tests and the shortcuts overlay both use it.
func checkKeybindings() []string { return checkTable(keybindings) }

// checkTable reports the mistakes of one platform's table.
func checkTable(keybindings []keyBinding) []string {
	var problems []string
	seen := map[string]bool{}
	for i := range commandRegistry {
		id := commandRegistry[i].ID
		if seen[id] {
			problems = append(problems, "duplicate command id "+id)
		}
		seen[id] = true
	}
	for i, b := range keybindings {
		if _, ok := commandsByID[b.Command]; !ok {
			problems = append(problems, "binding to unknown command "+b.Command)
		}
		if b.Combo.Label() == "" {
			problems = append(problems, "binding of "+b.Command+" has no key")
		}
		for _, other := range keybindings[i+1:] {
			if b.Combo != other.Combo || !scopesOverlap(b.Scope, other.Scope) {
				continue
			}
			problems = append(problems, b.Combo.Label()+" is bound to both "+
				b.Command+" and "+other.Command)
		}
	}
	return problems
}

// scopesOverlap reports whether two scopes can apply in the same state.
func scopesOverlap(a, b keyScope) bool {
	if a == b || a == scopeGlobal || b == scopeGlobal {
		return true
	}
	// Everywhere but the editor.
	if a == scopeNotEditor || b == scopeNotEditor {
		return a != scopeEditor && b != scopeEditor
	}
	// A browser pane is in the terminal view: their keys compete.
	if a == scopeBrowser || b == scopeBrowser {
		return a == scopeTerminal || b == scopeTerminal
	}
	// The terminal view hides the editor and the Git panel, so its
	// bindings never compete with theirs.
	return a != scopeTerminal && b != scopeTerminal
}
