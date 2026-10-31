package session

import (
	"runtime"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// wordKey is what the macOS line-editing keys send. Option+Left and Right
// send Esc b and Esc f, which move the shell's cursor by a word in bash,
// zsh and fish, as Ghostty's own keys there do: the terminal sent them as
// Alt-modified arrows, which stock shells leave unbound. Command+Left and
// Right send Ctrl+A and Ctrl+E, to the start and end of the line, and
// Command+Backspace Ctrl+U, which deletes the line, as Terminal.app and
// iTerm do: the terminal left Command's keys to the app, which binds none
// of these. Elsewhere Alt is Meta already, and Ctrl+arrows are the word
// keys: nil.
func wordKey(mac bool, mods ui.Modifiers, key ui.Key) []byte {
	if !mac {
		return nil
	}
	switch mods {
	case ui.Alt:
		switch key {
		case ui.KeyLeft:
			return []byte("\x1bb")
		case ui.KeyRight:
			return []byte("\x1bf")
		}
	case ui.Super:
		switch key {
		case ui.KeyLeft:
			return []byte("\x01")
		case ui.KeyRight:
			return []byte("\x05")
		case ui.KeyBackspace:
			return []byte("\x15")
		}
	}
	return nil
}

// WordKeys is a terminal's OnKey, sending wordKey's keys to *t: the
// terminal it is an option of, set once it is made.
func WordKeys(t **terminal.Terminal) func(ui.Modifiers, ui.Key) bool {
	mac := runtime.GOOS == "darwin"
	return func(mods ui.Modifiers, key ui.Key) bool {
		seq := wordKey(mac, mods, key)
		if seq == nil || *t == nil {
			return false
		}
		(*t).Send(seq)
		return true
	}
}
