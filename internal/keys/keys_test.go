package keys

import "testing"

// Every name omaterm key takes, as each keyboard mode encodes it.
func TestBytes(t *testing.T) {
	plain := Modes{}
	kitty := Modes{Kitty: 1}
	other := Modes{ModifyOtherKeys: 1}
	both := Modes{Kitty: 1, ModifyOtherKeys: 2}
	cases := []struct {
		name  string
		modes Modes
		want  string
	}{
		// Plain keys are the same in every mode.
		{"enter", plain, "\r"},
		{"return", kitty, "\r"},
		{"tab", other, "\t"},
		{"escape", both, "\x1b"},
		{"esc", plain, "\x1b"},
		{"space", kitty, " "},
		{"backspace", plain, "\x7f"},
		{"delete", other, "\x1b[3~"},
		{"insert", both, "\x1b[2~"},
		{"up", plain, "\x1b[A"},
		{"down", kitty, "\x1b[B"},
		{"right", other, "\x1b[C"},
		{"left", both, "\x1b[D"},
		{"home", plain, "\x1b[H"},
		{"end", kitty, "\x1b[F"},
		{"pageup", other, "\x1b[5~"},
		{"pagedown", both, "\x1b[6~"},
		{"shift+tab", plain, "\x1b[Z"},
		{"shift+tab", kitty, "\x1b[Z"},
		{"shift+tab", both, "\x1b[Z"},
		{"f1", plain, "\x1bOP"}, {"f2", kitty, "\x1bOQ"}, {"f3", other, "\x1bOR"}, {"f4", both, "\x1bOS"},
		{"f5", plain, "\x1b[15~"}, {"f6", kitty, "\x1b[17~"}, {"f7", other, "\x1b[18~"}, {"f8", both, "\x1b[19~"},
		{"f9", plain, "\x1b[20~"}, {"f10", kitty, "\x1b[21~"}, {"f11", other, "\x1b[23~"}, {"f12", both, "\x1b[24~"},

		// Ctrl with a letter is its control byte in every mode.
		{"ctrl+c", plain, "\x03"}, {"ctrl+d", kitty, "\x04"}, {"ctrl+z", other, "\x1a"},
		{"ctrl+l", both, "\x0c"}, {"ctrl+r", plain, "\x12"}, {"ctrl+@", kitty, "\x00"},

		// Enter with modifiers: kitty, modifyOtherKeys, then the fallbacks.
		{"ctrl+enter", plain, "\n"},
		{"ctrl+enter", kitty, "\x1b[13;5u"},
		{"ctrl+enter", other, "\x1b[27;5;13~"},
		{"ctrl+enter", both, "\x1b[13;5u"},
		{"shift+enter", plain, "\n"},
		{"shift+enter", kitty, "\x1b[13;2u"},
		{"shift+enter", other, "\x1b[27;2;13~"},
		{"alt+enter", plain, "\x1b\r"},
		{"alt+enter", kitty, "\x1b[13;3u"},
		{"alt+enter", other, "\x1b[27;3;13~"},
		{"ctrl+shift+enter", kitty, "\x1b[13;6u"},
		{"ctrl+alt+enter", other, "\x1b[27;7;13~"},

		// Shift+Tab with Ctrl and Alt: CSI Z, CSI 9;<mod>u where kitty can tell.
		{"ctrl+shift+tab", plain, "\x1b[Z"},
		{"ctrl+shift+tab", other, "\x1b[Z"},
		{"ctrl+shift+tab", kitty, "\x1b[9;6u"},
		{"ctrl+shift+tab", Modes{Kitty: 8}, "\x1b[9;6u"},
		{"alt+shift+tab", plain, "\x1b\x1b[Z"},
		{"alt+shift+tab", kitty, "\x1b\x1b[Z"},
		{"ctrl+alt+shift+tab", plain, "\x1b\x1b[Z"},
		{"ctrl+alt+shift+tab", kitty, "\x1b[9;8u"},
		{"shift+tab", Modes{Kitty: 31}, "\x1b[Z"},

		// Kitty flags that don't disambiguate leave Ctrl+Enter a newline;
		// flag 1 or 8, alone or among others, make it CSI u.
		{"ctrl+enter", Modes{Kitty: 2}, "\n"},
		{"ctrl+enter", Modes{Kitty: 4}, "\n"},
		{"ctrl+enter", Modes{Kitty: 16}, "\n"},
		{"ctrl+enter", Modes{Kitty: 8}, "\x1b[13;5u"},
		{"ctrl+enter", Modes{Kitty: 1 | 2 | 4}, "\x1b[13;5u"},
		{"ctrl+enter", Modes{Kitty: 31}, "\x1b[13;5u"},
		{"ctrl+space", Modes{Kitty: 2}, "\x00"},
		{"ctrl+space", Modes{Kitty: 8}, "\x1b[32;5u"},
		{"ctrl+enter", Modes{Kitty: 2, ModifyOtherKeys: 2}, "\x1b[27;5;13~"},

		// Ctrl+Space: NUL, or a modified space.
		{"ctrl+space", plain, "\x00"},
		{"ctrl+space", kitty, "\x1b[32;5u"},
		{"ctrl+space", other, "\x1b[27;5;32~"},
		{"ctrl+space", both, "\x1b[32;5u"},
		{"alt+space", plain, "\x1b "},

		// Alt before a key is an Escape.
		{"alt+x", plain, "\x1bx"},
		{"alt+Q", plain, "\x1bq"},
		{"alt+up", plain, "\x1b\x1b[A"},
		{"alt+up", kitty, "\x1b\x1b[A"},

		// Ctrl and Shift change a named key as xterm sends it.
		{"ctrl+up", plain, "\x1b[1;5A"},
		{"shift+up", kitty, "\x1b[1;2A"},
		{"ctrl+shift+left", plain, "\x1b[1;6D"},
		{"ctrl+home", plain, "\x1b[1;5H"},
		{"ctrl+delete", plain, "\x1b[3;5~"},
		{"ctrl+f5", plain, "\x1b[15;5~"},
		{"shift+f1", plain, "\x1b[1;2P"},

		// A lone character keeps its case.
		{"q", plain, "q"},
		{"A", plain, "A"},

		// Application cursor keys, as DECCKM asks.
		{"app+up", plain, "\x1bOA"},
		{"app+down", kitty, "\x1bOB"},
		{"app+home", plain, "\x1bOH"},
		{"ctrl+app+up", plain, "\x1b[1;5A"},
		{"alt+app+left", plain, "\x1b[1;3D"},
		{"shift+app+right", plain, "\x1b[1;2C"},
	}
	for _, c := range cases {
		got, err := Bytes(c.name, c.modes)
		if err != nil || string(got) != c.want {
			t.Errorf("%q in %+v: %q %v, want %q", c.name, c.modes, got, err, c.want)
		}
	}
}

func TestUnknown(t *testing.T) {
	for _, name := range []string{"nosuchkey", "", "ctrl+nosuch"} {
		if b, err := Bytes(name, Modes{}); err == nil {
			t.Errorf("%q: %q, want an error", name, b)
		}
	}
}
