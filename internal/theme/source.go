// Package theme turns a color scheme, as Omarchy's active theme, into the
// semantic palette the app paints with, and the themes of its terminals
// and editors. It holds no app state: callers load a Source, Derive a
// Palette, and hand it to the UI thread.
package theme

import "github.com/egoist/mygo/ui"

// Source is a theme as its author wrote it: the base colors and the 16
// ANSI colors, with no meaning given to them yet.
type Source struct {
	Name string
	Dark bool

	Background, Foreground, BrightForeground ui.Color
	Accent, Selection, Muted                 ui.Color

	// ANSI is the terminal palette: black, red, green, yellow, blue,
	// magenta, cyan, white, then the bright ones.
	ANSI [16]ui.Color
	// Extra holds the other named colors (orange, brown), when given.
	Extra map[string]ui.Color

	// GhosttyConf is the path of the theme's ghostty.conf, which the
	// terminals load as is when set.
	GhosttyConf string
}
