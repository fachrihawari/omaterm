package session

import (
	"runtime"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// wordKey is what Option+Left and Right send on macOS: Esc b and Esc f,
// which move the shell's cursor by a word in bash, zsh and fish, as
// Ghostty's own keys there do. The terminal sent them as Alt-modified
// arrows, which stock shells leave unbound. Elsewhere Alt is Meta already,
// and Ctrl+arrows are the word keys: nil.
func wordKey(mac bool, mods ui.Modifiers, key ui.Key) []byte {
	if !mac || mods != ui.Alt {
		return nil
	}
	switch key {
	case ui.KeyLeft:
		return []byte("\x1bb")
	case ui.KeyRight:
		return []byte("\x1bf")
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
