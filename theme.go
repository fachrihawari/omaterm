package main

import (
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"omaterm/internal/theme"
)

// pal is the palette the app paints with, derived from the active Omarchy
// theme (themewatch.go), or the built-in dark theme off Omarchy. Only the
// UI thread replaces it, and only between frames (applyTheme), so views
// read it without locks.
var (
	themeSrc  = theme.BuiltinDark()
	pal       = theme.Derive(themeSrc)
	termTheme = theme.TermTheme(themeSrc, pal)
	editTheme = theme.EditorTheme(pal)
)

// setPalette makes src the app's theme. UI thread only.
func setPalette(src theme.Source) {
	themeSrc = src
	pal = theme.Derive(src)
	termTheme = theme.TermTheme(src, pal)
	editTheme = theme.EditorTheme(pal)
}

func omaTheme() *ui.Theme {
	t := ui.DarkTheme()
	if !pal.Dark {
		t = ui.LightTheme()
	}
	t.Background = pal.Wash
	t.Surface = pal.Raised
	t.SurfaceHover = pal.Hover.Over(pal.Raised)
	t.SurfacePressed = pal.Popover.Mix(pal.Text, 0.06)
	t.Border = pal.Border
	t.Text = pal.Text
	t.TextMuted = pal.TextMuted
	t.Accent = pal.Accent
	t.AccentHover = pal.AccentHover
	t.AccentPressed = pal.Accent
	t.AccentText = pal.AccentText
	t.Danger = pal.Bad
	t.Warning = pal.Warn
	t.Success = pal.Ok
	t.Selection = pal.Selection
	t.Focus = pal.Focus
	t.Scrollbar = pal.BorderStrong
	// Toasts and tooltips are raised popovers rather than the theme turned
	// over, so they keep the theme's colors.
	t.Inverse = pal.Popover
	t.InverseText = pal.Text
	t.Radius = 6
	t.Spacing = 4
	t.FontSize = 13
	// Inter, falling back to Noto Sans; terminals and code take the
	// monospaced font where they render.
	t.Font = "Inter, Noto Sans"
	return t
}

// omaEditorTheme colors code from the palette: syntax from the theme's
// ANSI colors, a gutter that reads, and diff colors that mean what they
// say whatever the theme calls green and red.
func omaEditorTheme() *editor.Theme { return editTheme }

// omaTermTheme is the terminals' theme.
func omaTermTheme() *terminal.Theme { return termTheme }
