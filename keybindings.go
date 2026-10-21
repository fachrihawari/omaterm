package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// Keybindings are declared once, here, and drive three things: the window
// shortcuts, the key chips in the command palette, and the Keyboard
// Shortcuts overlay. A command may have several bindings (Ctrl+Tab and
// Ctrl+PageDown both move to the next tab), and a command may have none,
// which leaves it to the palette and the UI.
//
// The defaults follow VS Code where the app has the same concept. Where
// oterm is terminal-first the oterm meaning wins: Ctrl+T opens a terminal
// tab and Ctrl+W closes one, where VS Code would send those to the shell.

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
	// scopeGlobal bindings work wherever the keyboard is, except that a
	// binding that does not skip the shell waits while the terminal has
	// the keyboard.
	scopeGlobal keyScope = iota
	// scopeTerminal bindings work while the terminal view is shown.
	scopeTerminal
	// scopeEditor bindings work while an editor tab is shown.
	scopeEditor
	// scopeGit bindings work while the Git inspector is open.
	scopeGit
)

// String names the scope as the shortcuts overlay shows it.
func (s keyScope) String() string {
	switch s {
	case scopeTerminal:
		return "Terminal"
	case scopeEditor:
		return "Editor"
	case scopeGit:
		return "Git panel"
	}
	return "Anywhere"
}

// keyBinding ties a command to one key. SkipShell keeps the binding the
// app's while the terminal has the keyboard, as VS Code's
// terminal.integrated.commandsToSkipShell does; without it the shell gets
// the key, as it needs Ctrl+S (XOFF) and Ctrl+Q (XON).
type keyBinding struct {
	Command   string
	Combo     keyCombo
	Scope     keyScope
	SkipShell bool
}

// keybindings is the default table. A command may appear more than once.
var keybindings = []keyBinding{
	// App and navigation.
	{"app.commandPalette", combo(ui.Cmd|ui.Shift, ui.KeyP), scopeGlobal, true},
	{"app.commandPalette", combo(0, ui.KeyF1), scopeGlobal, true},
	{"file.quickOpen", combo(ui.Cmd, ui.KeyP), scopeGlobal, true},
	{"search.global", combo(ui.Cmd|ui.Shift, ui.KeyF), scopeGlobal, true},
	{"help.keyboardShortcuts", combo(ui.Cmd|ui.Shift, ui.KeySlash), scopeGlobal, true},
	// Open Project skips the shell: the terminal is oterm's main view, and
	// Ctrl+O is how a project is opened. Quit does not, so the shell keeps
	// its Ctrl+Q (XON).
	{"app.openProject", combo(ui.Cmd, ui.KeyO), scopeGlobal, true},
	{"app.quit", combo(ui.Cmd, ui.KeyQ), scopeGlobal, false},

	// View.
	{"view.toggleSidebar", combo(ui.Cmd, ui.KeyB), scopeGlobal, true},
	{"view.toggleInspector", combo(ui.Cmd|ui.Shift, ui.KeyB), scopeGlobal, true},
	{"view.terminal", combo(ui.Cmd, ui.Key1), scopeGlobal, true},
	{"view.toggleTerminal", combo(ui.Cmd, ui.KeyBackquote), scopeGlobal, true},
	{"view.editor", combo(ui.Cmd, ui.Key2), scopeGlobal, true},
	{"view.diff", combo(ui.Cmd, ui.Key3), scopeGlobal, true},
	{"view.files", combo(ui.Cmd|ui.Shift, ui.KeyE), scopeGlobal, true},
	{"view.git", combo(ui.Cmd|ui.Shift, ui.KeyG), scopeGlobal, true},

	// Tabs. Ctrl+W closes a tab even from the terminal: closing a tab is
	// the app's job in a terminal-first app.
	{"tab.next", combo(ui.Cmd, ui.KeyTab), scopeGlobal, true},
	{"tab.next", combo(ui.Cmd, ui.KeyPageDown), scopeGlobal, true},
	{"tab.prev", combo(ui.Cmd|ui.Shift, ui.KeyTab), scopeGlobal, true},
	{"tab.prev", combo(ui.Cmd, ui.KeyPageUp), scopeGlobal, true},
	{"tab.close", combo(ui.Cmd, ui.KeyW), scopeGlobal, true},

	// Editor.
	{"file.save", combo(ui.Cmd, ui.KeyS), scopeEditor, false},
	{"markdown.togglePreview", combo(ui.Cmd|ui.Shift, ui.KeyV), scopeEditor, false},

	// Terminal. The split keys are the ones the app has always used;
	// Ctrl+Shift+5 is VS Code's, kept as a second binding.
	{"terminal.new", combo(ui.Cmd, ui.KeyT), scopeGlobal, true},
	{"terminal.new", combo(ui.Cmd|ui.Shift, ui.KeyBackquote), scopeGlobal, true},
	{"terminal.splitDown", combo(ui.Cmd|ui.Shift, ui.KeyD), scopeTerminal, true},
	{"terminal.splitRight", combo(ui.Cmd|ui.Shift, ui.KeyR), scopeTerminal, true},
	{"terminal.splitRight", combo(ui.Cmd|ui.Shift, ui.Key5), scopeTerminal, true},
	{"terminal.focusLeft", combo(ui.Alt, ui.KeyLeft), scopeTerminal, true},
	{"terminal.focusRight", combo(ui.Alt, ui.KeyRight), scopeTerminal, true},
	{"terminal.focusUp", combo(ui.Alt, ui.KeyUp), scopeTerminal, true},
	{"terminal.focusDown", combo(ui.Alt, ui.KeyDown), scopeTerminal, true},

	// Git.
	{"git.commit", combo(ui.Cmd, ui.KeyEnter), scopeGit, false},
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

// keyLabels writes all of a command's keys as one label, for tooltips:
// "Ctrl+Shift+R, Ctrl+Shift+5".
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

// terminalHasKeyboard reports whether a terminal pane had the keyboard in
// the last committed frame. The terminal view records it while it builds;
// the app's shortcuts read it before the next frame's view is built.
func (a *appState) terminalHasKeyboard() bool { return a.termFocus }

// keyActive reports whether a binding applies in the current state.
func (a *appState) keyActive(b keyBinding) bool {
	switch b.Scope {
	case scopeTerminal:
		return a.activeView == tabTerminal
	case scopeEditor:
		return a.activeView == tabEditor && !a.terminalHasKeyboard()
	case scopeGit:
		return a.inspectorVisible && a.inspectorTab == 2 && !a.terminalHasKeyboard()
	}
	// Global: a key the shell needs waits while the terminal has the
	// keyboard, as Ctrl+S and Ctrl+Q do.
	return b.SkipShell || !a.terminalHasKeyboard()
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

// --- Pane focus ---

// paneDir is a direction in which focus can move between terminal panes.
type paneDir int

const (
	paneLeft paneDir = iota
	paneRight
	paneUp
	paneDown
)

// paneRects lays the split tree over a unit square and records each leaf's
// rectangle, so focus can move to the pane next to the active one.
func paneRects(n *splitNode, x, y, w, h float32, out map[string]ui.Rect) {
	if n == nil {
		return
	}
	if n.isLeaf() {
		out[n.TermID] = ui.Rect{X: x, Y: y, W: w, H: h}
		return
	}
	frac := n.Ratio / 100
	if frac <= 0 || frac >= 1 {
		frac = 0.5
	}
	if n.Dir == splitVertical {
		paneRects(n.First, x, y, w*frac, h, out)
		paneRects(n.Second, x+w*frac, y, w*(1-frac), h, out)
		return
	}
	paneRects(n.First, x, y, w, h*frac, out)
	paneRects(n.Second, x, y+h*frac, w, h*(1-frac), out)
}

// focusPane moves the keyboard to the pane nearest the active one in dir,
// as VS Code's pane navigation does. The panes are laid out in the unit
// square, so the comparison does not need the real geometry.
func (a *appState) focusPane(dir paneDir) {
	root := a.activeSplit()
	if root == nil {
		return
	}
	cur := a.activeSplitLeaf()
	rects := map[string]ui.Rect{}
	paneRects(root, 0, 0, 1, 1, rects)
	from, ok := rects[cur]
	if !ok {
		return
	}
	fx, fy := from.X+from.W/2, from.Y+from.H/2

	best := ""
	bestPrimary, bestSecondary := float32(0), float32(0)
	for id, r := range rects {
		if id == cur {
			continue
		}
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		var primary, secondary float32
		switch dir {
		case paneLeft:
			if cx >= fx {
				continue
			}
			primary, secondary = fx-cx, absf(cy-fy)
		case paneRight:
			if cx <= fx {
				continue
			}
			primary, secondary = cx-fx, absf(cy-fy)
		case paneUp:
			if cy >= fy {
				continue
			}
			primary, secondary = fy-cy, absf(cx-fx)
		case paneDown:
			if cy <= fy {
				continue
			}
			primary, secondary = cy-fy, absf(cx-fx)
		}
		if best == "" || primary < bestPrimary ||
			(primary == bestPrimary && secondary < bestSecondary) {
			best, bestPrimary, bestSecondary = id, primary, secondary
		}
	}
	if best == "" {
		return
	}
	a.activePaneTermID = best
	// FocusBind turns the request into focus when the pane builds.
	a.paneFocus = best
}

func absf(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
