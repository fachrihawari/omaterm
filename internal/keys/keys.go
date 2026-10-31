// Package keys turns the names omaterm key and the web page's key bar
// use ("esc", "ctrl+enter", "f5") into the bytes a terminal sends.
package keys

import (
	"fmt"
	"strings"
)

// Modes is how the program in a pane asked its keys to be sent: the
// keyboard protocols it turned on. A zero value is the plain terminal.
type Modes struct {
	// Kitty is the kitty keyboard protocol's flags, non-zero when the
	// program asked for keys to be disambiguated.
	Kitty int
	// ModifyOtherKeys is xterm's modifyOtherKeys level; 0 is off.
	ModifyOtherKeys int
}

// base are the names that mean one key, as the bytes a terminal sends.
var base = map[string]string{
	"enter": "\r", "return": "\r", "tab": "\t", "escape": "\x1b", "esc": "\x1b", "space": " ",
	"backspace": "\x7f", "delete": "\x1b[3~", "insert": "\x1b[2~",
	"up": "\x1b[A", "down": "\x1b[B", "right": "\x1b[C", "left": "\x1b[D",
	"home": "\x1b[H", "end": "\x1b[F", "pageup": "\x1b[5~", "pagedown": "\x1b[6~",
	"f1": "\x1bOP", "f2": "\x1bOQ", "f3": "\x1bOR", "f4": "\x1bOS",
	"f5": "\x1b[15~", "f6": "\x1b[17~", "f7": "\x1b[18~", "f8": "\x1b[19~",
	"f9": "\x1b[20~", "f10": "\x1b[21~", "f11": "\x1b[23~", "f12": "\x1b[24~",
}

// appCursor are the keys whose sequence changes in application cursor
// mode (DECCKM).
var appCursor = map[string]byte{"up": 'A', "down": 'B', "right": 'C', "left": 'D', "home": 'H', "end": 'F'}

// mods are the modifiers a key's name asks for.
type mods struct{ ctrl, alt, shift bool }

// xterm is the modifiers as xterm numbers them: 1 plus shift, alt and
// ctrl as bits.
func (m mods) xterm() int {
	n := 1
	if m.shift {
		n++
	}
	if m.alt {
		n += 2
	}
	if m.ctrl {
		n += 4
	}
	return n
}

// Bytes encodes a key by name, as the program asked keys to be sent.
func Bytes(name string, m Modes) ([]byte, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	mod, rest := splitMods(n)
	// Application cursor keys, as a program that turned DECCKM on
	// expects: ESC O A rather than ESC [ A, and the usual form when a
	// modifier changes them.
	if r, ok := strings.CutPrefix(rest, "app+"); ok {
		final, ok := appCursor[r]
		if !ok {
			return nil, fmt.Errorf("unknown key %q", name)
		}
		if mod.ctrl || mod.alt || mod.shift {
			return []byte(fmt.Sprintf("\x1b[1;%d%c", mod.xterm(), final)), nil
		}
		return []byte{0x1b, 'O', final}, nil
	}
	switch {
	case rest == "tab" && mod.shift:
		return shiftTab(mod, m), nil
	case rest == "enter" || rest == "return":
		return enter(mod, m), nil
	case rest == "space" && (mod.ctrl || mod.alt):
		return space(mod, m), nil
	case !mod.ctrl && !mod.alt && !mod.shift:
		if b, ok := base[rest]; ok {
			return []byte(b), nil
		}
	case mod.ctrl && !mod.shift && len(rest) == 1:
		if b, ok := control(rest[0]); ok {
			if mod.alt {
				b = append([]byte{0x1b}, b...)
			}
			return b, nil
		}
	case mod.alt && !mod.ctrl && !mod.shift:
		// Alt is an Escape before the key, as terminals have long sent it.
		if b, ok := base[rest]; ok {
			return append([]byte{0x1b}, b...), nil
		}
		if len([]rune(rest)) == 1 {
			return append([]byte{0x1b}, []byte(rest)...), nil
		}
	}
	if b, ok := modified(rest, mod); ok {
		return b, nil
	}
	// A lone character, as it is (an uppercase one keeps its case).
	if len([]rune(name)) == 1 {
		return []byte(name), nil
	}
	return nil, fmt.Errorf("unknown key %q", name)
}

// splitMods takes the ctrl+, alt+ and shift+ prefixes off a name.
func splitMods(n string) (mods, string) {
	var m mods
	for {
		switch {
		case strings.HasPrefix(n, "ctrl+"):
			m.ctrl, n = true, n[len("ctrl+"):]
		case strings.HasPrefix(n, "alt+"):
			m.alt, n = true, n[len("alt+"):]
		case strings.HasPrefix(n, "shift+"):
			m.shift, n = true, n[len("shift+"):]
		default:
			return m, n
		}
	}
}

// disambiguates tells that the program asked, by the kitty protocol, for
// keys a plain terminal can't tell apart to be sent as CSI u: flag 1,
// "disambiguate escape codes", or flag 8, "report all keys as escape codes".
// The other flags (event types, alternate keys, text) leave Ctrl+Enter as
// it was.
func disambiguates(m Modes) bool { return m.Kitty&(1|8) != 0 }

// shiftTab is Shift+Tab with maybe Ctrl and Alt. Shift+Tab is CSI Z; Ctrl
// can only be told apart by the kitty protocol (CSI 9;<mod>u), else it is
// the same key; Alt is an Escape before it.
func shiftTab(mod mods, modes Modes) []byte {
	if mod.ctrl && disambiguates(modes) {
		return []byte(fmt.Sprintf("\x1b[9;%du", mod.xterm()))
	}
	if mod.alt {
		return []byte("\x1b\x1b[Z")
	}
	return []byte("\x1b[Z")
}

// enter is Enter with modifiers. The kitty protocol names it CSI 13;<mod>u
// and modifyOtherKeys CSI 27;<mod>;13~; a plain terminal has no way to
// tell it from Enter, so Ctrl+Enter and Shift+Enter go as Ctrl+J, which
// most TUIs take as a newline without submitting, and Alt+Enter as Escape
// and Enter.
func enter(m mods, modes Modes) []byte {
	if !m.ctrl && !m.alt && !m.shift {
		return []byte("\r")
	}
	mod := m.xterm()
	switch {
	case disambiguates(modes):
		return []byte(fmt.Sprintf("\x1b[13;%du", mod))
	case modes.ModifyOtherKeys >= 1:
		return []byte(fmt.Sprintf("\x1b[27;%d;13~", mod))
	case m.ctrl || m.shift:
		return []byte("\n")
	default:
		return []byte("\x1b\r")
	}
}

// space is Ctrl+Space, or Alt+Space. The kitty and modifyOtherKeys
// protocols name it as Enter with modifiers does; a plain terminal sends
// NUL for Ctrl+Space and Escape and a space for Alt+Space.
func space(m mods, modes Modes) []byte {
	mod := m.xterm()
	switch {
	case disambiguates(modes):
		return []byte(fmt.Sprintf("\x1b[32;%du", mod))
	case modes.ModifyOtherKeys >= 1:
		return []byte(fmt.Sprintf("\x1b[27;%d;32~", mod))
	case m.ctrl:
		return []byte{0}
	default:
		return []byte("\x1b ")
	}
}

// control is a Ctrl+<character> byte, where one exists.
func control(c byte) ([]byte, bool) {
	switch {
	case c >= 'a' && c <= 'z':
		return []byte{c - 'a' + 1}, true
	case c >= '@' && c <= '_':
		return []byte{c - '@'}, true
	}
	return nil, false
}

// keyParam is a named key's number in a CSI sequence, and its final byte.
type keyParam struct {
	num   string
	final byte
}

// named are the keys a modifier can change: their xterm sequence is
// CSI <num>;<mod><final>, or CSI 1;<mod><final> for the ones numbered 1.
var named = map[string]keyParam{
	"up": {"1", 'A'}, "down": {"1", 'B'}, "right": {"1", 'C'}, "left": {"1", 'D'},
	"home": {"1", 'H'}, "end": {"1", 'F'},
	"insert": {"2", '~'}, "delete": {"3", '~'}, "pageup": {"5", '~'}, "pagedown": {"6", '~'},
	"f1": {"1", 'P'}, "f2": {"1", 'Q'}, "f3": {"1", 'R'}, "f4": {"1", 'S'},
	"f5": {"15", '~'}, "f6": {"17", '~'}, "f7": {"18", '~'}, "f8": {"19", '~'},
	"f9": {"20", '~'}, "f10": {"21", '~'}, "f11": {"23", '~'}, "f12": {"24", '~'},
}

// modified is a named key with Ctrl or Shift, as xterm sends it.
func modified(name string, m mods) ([]byte, bool) {
	k, ok := named[name]
	if !ok || !(m.ctrl || m.shift) {
		return nil, false
	}
	return []byte(fmt.Sprintf("\x1b[%s;%d%c", k.num, m.xterm(), k.final)), true
}
