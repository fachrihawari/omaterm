package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/theme"
)

// Theme is the app's look as the page takes it: the colors of its roles,
// as CSS values, and the terminal's. The app sends it to the daemon as its
// theme changes (web.setTheme), so the page looks like the window.
type Theme struct {
	Dark bool `json:"dark"`
	// Tokens are the window's colors by role, as theme.Palette names
	// them: wash, sidebar, card, raised, input, hover, rowSelected,
	// border, borderStrong, text, textSoft, textMuted, textFaint, accent,
	// accentText, ok, warn, bad, attention, done, chip.
	Tokens   map[string]string `json:"tokens"`
	Terminal TermColors        `json:"terminal"`
	// Stamp changes as the colors do: a page refetches the theme when
	// the stamp its events carry is new.
	Stamp string `json:"stamp"`
}

// TermColors are a terminal's colors, as CSS values.
type TermColors struct {
	Foreground string     `json:"foreground"`
	Background string     `json:"background"`
	Cursor     string     `json:"cursor"`
	Selection  string     `json:"selection"`
	ANSI       [16]string `json:"ansi"`
}

// ThemeOf is the page's theme for a source, derived as the window's is.
func ThemeOf(src theme.Source) Theme {
	p := theme.Derive(src)
	t := Theme{Dark: p.Dark, Tokens: map[string]string{
		"wash": CSSColor(p.Wash), "sidebar": CSSColor(p.Sidebar), "card": CSSColor(p.Card),
		"raised": CSSColor(p.Raised), "popover": CSSColor(p.Popover), "input": CSSColor(p.Input),
		"hover": CSSColor(p.Hover), "rowSelected": CSSColor(p.RowSelected), "chip": CSSColor(p.Chip),
		"border": CSSColor(p.Border), "borderStrong": CSSColor(p.BorderStrong),
		"text": CSSColor(p.Text), "textStrong": CSSColor(p.TextStrong), "textSoft": CSSColor(p.TextSoft),
		"textMuted": CSSColor(p.TextMuted), "textFaint": CSSColor(p.TextFaint),
		"accent": CSSColor(p.Accent), "accentText": CSSColor(p.AccentText), "selection": CSSColor(p.Selection),
		"ok": CSSColor(p.Ok), "warn": CSSColor(p.Warn), "bad": CSSColor(p.Bad), "info": CSSColor(p.Info),
		"attention": CSSColor(p.Attention), "attentionSoft": CSSColor(p.AttentionSoft), "done": CSSColor(p.Done),
		"scrim": CSSColor(p.Scrim),
	}}
	if term := theme.TermTheme(src, p); term != nil {
		t.Terminal = TermColors{Foreground: CSSColor(term.Foreground), Background: CSSColor(term.Background),
			Cursor: CSSColor(term.Cursor), Selection: CSSColor(term.Selection)}
		if t.Terminal.Cursor == "" || term.Cursor.A == 0 {
			t.Terminal.Cursor = t.Terminal.Foreground
		}
		for i, c := range term.Palette {
			t.Terminal.ANSI[i] = CSSColor(c)
		}
	}
	b, _ := json.Marshal(t)
	sum := sha256.Sum256(b)
	t.Stamp = hex.EncodeToString(sum[:8])
	return t
}

// CSSColor is a color as CSS writes it: #rrggbb when opaque, else rgba().
func CSSColor(c ui.Color) string {
	if c.A == 255 {
		return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
	}
	return fmt.Sprintf("rgba(%d, %d, %d, %.3g)", c.R, c.G, c.B, float64(c.A)/255)
}
