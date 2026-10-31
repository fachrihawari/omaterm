package session

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// On macOS Option+Left and Right move the shell's cursor by words, as
// Ghostty's do, and Command+Left, Right and Backspace go to the line's
// start and end and delete it, as Terminal.app's do; elsewhere, and with
// other modifiers, the key goes on.
func TestWordKeys(t *testing.T) {
	for _, c := range []struct {
		mac  bool
		mods ui.Modifiers
		key  ui.Key
		want string
	}{
		{true, ui.Alt, ui.KeyLeft, "\x1bb"},
		{true, ui.Alt, ui.KeyRight, "\x1bf"},
		{true, ui.Alt | ui.Shift, ui.KeyLeft, ""},
		{true, ui.Alt, ui.KeyUp, ""},
		{true, 0, ui.KeyLeft, ""},
		{false, ui.Alt, ui.KeyLeft, ""},
		{true, ui.Super, ui.KeyLeft, "\x01"},
		{true, ui.Super, ui.KeyRight, "\x05"},
		{true, ui.Super, ui.KeyBackspace, "\x15"},
		{true, ui.Super | ui.Shift, ui.KeyLeft, ""},
		{true, ui.Super, ui.KeyUp, ""},
		{true, ui.Alt, ui.KeyBackspace, ""},
		{false, ui.Super, ui.KeyLeft, ""},
		{false, ui.Super, ui.KeyBackspace, ""},
	} {
		if got := string(wordKey(c.mac, c.mods, c.key)); got != c.want {
			t.Errorf("mac=%v mods=%v key=%v: %q, want %q", c.mac, c.mods, c.key, got, c.want)
		}
	}
}
