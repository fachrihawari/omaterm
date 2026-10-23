package main

import (
	"encoding/json"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// A web page with the keyboard gets every key: the app's shortcuts never
// reach the window. Each page therefore runs pageKeysScript first, which
// takes the app's chords before the page sees them and asks for their
// command by navigating to omaterm-key:<secret>/<command>; the app cancels
// that navigation and runs the command. The secret, which the page's own
// scripts cannot read, keeps a page from running the app's commands.

const pageKeyScheme = "omaterm-key:"

// pageKeyCommands maps a chord, as pageKeysScript writes it from a DOM
// keydown (modifier letters, ":", the key's code), to its command: the
// bindings that apply anywhere or in a browser pane.
func pageKeyCommands() map[string]string {
	out := map[string]string{}
	for _, b := range keybindings {
		if b.Scope != scopeGlobal && b.Scope != scopeBrowser {
			continue
		}
		code := domCode(b.Combo.Key)
		if code == "" {
			continue
		}
		mods := ""
		for _, m := range []struct {
			mod ui.Modifiers
			s   string
		}{{ui.Ctrl, "c"}, {ui.Alt, "a"}, {ui.Shift, "s"}, {ui.Super, "m"}} {
			if b.Combo.Mods&m.mod != 0 {
				mods += m.s
			}
		}
		out[mods+":"+code] = b.Command
	}
	return out
}

// domCode is a key's KeyboardEvent.code, "" for keys no binding of a
// page needs.
func domCode(k ui.Key) string {
	switch {
	case k >= ui.KeyA && k <= ui.KeyZ:
		return "Key" + string(rune('A'+k-ui.KeyA))
	case k >= ui.Key0 && k <= ui.Key9:
		return "Digit" + string(rune('0'+k-ui.Key0))
	}
	switch k {
	case ui.KeyLeft:
		return "ArrowLeft"
	case ui.KeyRight:
		return "ArrowRight"
	case ui.KeyUp:
		return "ArrowUp"
	case ui.KeyDown:
		return "ArrowDown"
	case ui.KeyEnter:
		return "Enter"
	case ui.KeySlash:
		return "Slash"
	case ui.KeyEqual:
		return "Equal"
	case ui.KeyMinus:
		return "Minus"
	}
	return ""
}

// pageKeysScript is the script a page runs first, for the web view whose
// secret it is.
func pageKeysScript(secret string) string {
	keys, _ := json.Marshal(pageKeyCommands())
	s, _ := json.Marshal(pageKeyScheme + secret + "/")
	return `(() => {
  const keys = ` + string(keys) + `, to = ` + string(s) + `, go = location.assign.bind(location);
  addEventListener("keydown", (e) => {
    const k = (e.ctrlKey ? "c" : "") + (e.altKey ? "a" : "") + (e.shiftKey ? "s" : "") + (e.metaKey ? "m" : "") + ":" + e.code;
    const id = keys[k];
    if (!id || e.isComposing) return;
    e.preventDefault();
    e.stopImmediatePropagation();
    go(to + id);
  }, true);
})();`
}

// pageKey takes a navigation to omaterm-key: of a web view with secret,
// returning its command, one of a page's; it cancels every navigation to
// the scheme.
func pageKey(e *mygo.NavigateEvent, secret string) (string, bool) {
	rest, ok := strings.CutPrefix(e.URL, pageKeyScheme)
	if !ok {
		return "", false
	}
	e.PreventDefault()
	id, ok := strings.CutPrefix(rest, secret+"/")
	if !ok || secret == "" {
		return "", false
	}
	for _, cmd := range pageKeyCommands() {
		if cmd == id {
			return id, true
		}
	}
	return "", false
}
