package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
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

// keyCombo is one key with the modifiers held, as a shortcut is pressed.
type keyCombo struct {
	Mods ui.Modifiers
	Key  ui.Key
}

func combo(mods ui.Modifiers, key ui.Key) keyCombo {
	return keyCombo{Mods: mods, Key: key}
}

// Label writes the combo as the UI shows it: "Ctrl+Shift+P" on Linux,
// "⇧⌘P" on macOS, where the modifiers are glyphs and ⌘ comes first.
func (k keyCombo) Label() string {
	var b strings.Builder
	if ui.Cmd == ui.Super {
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
	b.WriteString(keyLabel(k.Key))
	return b.String()
}

// keyLabel names a key as VS Code does: letters in upper case, and words
// for the keys that type nothing.
func keyLabel(k ui.Key) string {
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
)

// String names the scope as the shortcuts overlay shows it.
func (s keyScope) String() string {
	switch s {
	case scopeTerminal:
		return "Terminal"
	case scopeEditor:
		return "Editor"
	}
	return "Anywhere"
}

// keyBinding ties a command to one key.
type keyBinding struct {
	Command string
	Combo   keyCombo
	Scope   keyScope
}

// keybindings is the default table. A command may have no binding at all,
// which leaves it to the palette and the UI.
var keybindings = append([]keyBinding{
	// App.
	{"file.quickOpen", combo(ui.Cmd, ui.KeyP), scopeGlobal},
	{"app.commandPalette", combo(ui.Cmd|ui.Shift, ui.KeyP), scopeGlobal},
	{"search.global", combo(ui.Cmd|ui.Shift, ui.KeyF), scopeGlobal},
	{"app.openProject", combo(ui.Cmd, ui.KeyO), scopeGlobal},
	{"help.keyboardShortcuts", combo(ui.Cmd|ui.Shift, ui.KeySlash), scopeGlobal},

	// Zoom, of the terminal and the editor alike.
	{"app.zoomIn", combo(ui.Cmd, ui.KeyEqual), scopeGlobal},
	{"app.zoomOut", combo(ui.Cmd, ui.KeyMinus), scopeGlobal},
	{"app.zoomReset", combo(ui.Cmd, ui.Key0), scopeGlobal},

	// The side panels: Ctrl+B toggles one, Shift the other.
	{"view.toggleSidebar", combo(ui.Cmd, ui.KeyB), scopeGlobal},
	{"view.toggleInspector", combo(ui.Cmd|ui.Shift, ui.KeyB), scopeGlobal},
	{"view.files", combo(ui.Cmd|ui.Shift, ui.KeyE), scopeGlobal},
	{"view.git", combo(ui.Cmd|ui.Shift, ui.KeyG), scopeGlobal},
	{"view.info", combo(ui.Cmd|ui.Shift, ui.KeyI), scopeGlobal},

	// Tabs. Ctrl+W closes one even from the terminal: closing a tab is
	// the app's job in a terminal-first app.
	{"tab.close", combo(ui.Cmd, ui.KeyW), scopeGlobal},

	// Terminal.
	{"terminal.new", combo(ui.Cmd, ui.KeyT), scopeGlobal},
	{"terminal.splitDown", combo(ui.Cmd|ui.Shift, ui.KeyD), scopeTerminal},
	{"terminal.splitRight", combo(ui.Cmd|ui.Shift, ui.KeyR), scopeTerminal},
	{"pane.zoom", combo(ui.Cmd|ui.Shift, ui.KeyEnter), scopeTerminal},

	// Sessions: the one that needs you, and the next and previous in the
	// sidebar's order.
	{"session.attention", combo(ui.Cmd|ui.Shift, ui.KeyJ), scopeGlobal},
	{"session.next", combo(ui.Alt, ui.KeyDown), scopeGlobal},
	{"session.prev", combo(ui.Alt, ui.KeyUp), scopeGlobal},

	// Editor.
	{"file.save", combo(ui.Cmd, ui.KeyS), scopeEditor},
}, digitBindings()...)

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

// digitBindings ties those commands to Alt+N and Alt+Shift+N.
func digitBindings() []keyBinding {
	out := make([]keyBinding, 0, 2*len(tabDigits))
	for i, k := range tabDigits {
		n := i + 1
		out = append(out,
			keyBinding{"tab.select" + itoa(n), combo(ui.Alt, k), scopeGlobal},
			keyBinding{"project.select" + itoa(n), combo(ui.Alt|ui.Shift, k), scopeGlobal})
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

// keyActive reports whether a binding applies in the current state.
func (a *appState) keyActive(b keyBinding) bool {
	switch b.Scope {
	case scopeTerminal:
		return a.activeView == tabTerminal
	case scopeEditor:
		return a.activeView == tabEditor
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
func checkKeybindings() []string {
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
	// The terminal view hides the editor and the Git panel, so its
	// bindings never compete with theirs.
	return a != scopeTerminal && b != scopeTerminal
}
