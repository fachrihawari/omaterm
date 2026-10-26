package theme

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

var fixtures = []string{"tokyo-night", "lupine", "white", "matte-black", "catppuccin-latte"}

func load(t *testing.T, name string) Source {
	t.Helper()
	s, err := ParseColors(filepath.Join("testdata", name+".toml"))
	if err != nil {
		t.Fatal(err)
	}
	s.Name = name
	return s
}

func TestParseColors(t *testing.T) {
	s := load(t, "tokyo-night")
	if !s.Dark {
		t.Error("tokyo-night is dark")
	}
	if Hex(s.Background) != "#1a1b26" || Hex(s.Accent) != "#7aa2f7" {
		t.Errorf("bg %s accent %s", Hex(s.Background), Hex(s.Accent))
	}
	// Omarchy's slots: 0 background, 1 red, 2 green, 7 foreground, 8 muted.
	want := map[int]string{0: "#1a1b26", 1: "#f7768e", 2: "#9ece6a", 7: "#a9b1d6", 8: "#414868", 15: "#c0caf5"}
	for i, w := range want {
		if Hex(s.ANSI[i]) != w {
			t.Errorf("ANSI[%d] = %s, want %s", i, Hex(s.ANSI[i]), w)
		}
	}
	if l := load(t, "lupine"); l.Dark {
		t.Error("lupine is light")
	}
}

func TestLoadOmarchy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OMATERM_OMARCHY_DIR", dir)
	if err := os.MkdirAll(filepath.Join(dir, "theme"), 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, "testdata/lupine.toml", filepath.Join(dir, "theme", "colors.toml"))
	copyFile(t, "testdata/lupine-ghostty.conf", filepath.Join(dir, "theme", "ghostty.conf"))
	os.WriteFile(filepath.Join(dir, "theme.name"), []byte("lupine\n"), 0o644)
	s, err := LoadOmarchy()
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "lupine" || s.GhosttyConf == "" {
		t.Errorf("name %q ghostty %q", s.Name, s.GhosttyConf)
	}
	th := TermTheme(s, Derive(s))
	if th.Background != Derive(s).Card {
		t.Error("terminal background is not the card")
	}
	if Hex(th.Palette[1]) != "#c900c4" {
		t.Errorf("palette from ghostty.conf: %s", Hex(th.Palette[1]))
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Every theme's roles read on its background, whatever its colors.
func TestDeriveContrast(t *testing.T) {
	for _, name := range append(fixtures, "builtin-dark", "builtin-light") {
		var s Source
		switch name {
		case "builtin-dark":
			s = BuiltinDark()
		case "builtin-light":
			s = BuiltinLight()
		default:
			s = load(t, name)
		}
		p := Derive(s)
		if p.Card != s.Background {
			t.Errorf("%s: card is not the background", name)
		}
		checks := []struct {
			role string
			c    ui.Color
			min  float64
		}{
			{"TextMuted", p.TextMuted, 4.5}, {"TextFaint", p.TextFaint, 3},
			{"Ok", p.Ok, 3}, {"Warn", p.Warn, 3}, {"Bad", p.Bad, 3}, {"Accent", p.Accent, 3},
		}
		for _, ch := range checks {
			if r := Contrast(ch.c, p.Card); r < ch.min-0.05 {
				t.Errorf("%s: %s %s contrast %.2f < %.1f", name, ch.role, Hex(ch.c), r, ch.min)
			}
		}
	}
}

// A theme's colors only get a meaning their hue supports.
func TestDeriveHueFallback(t *testing.T) {
	for _, name := range fixtures {
		p := Derive(load(t, name))
		if !hueIn(p.Ok, 80, 170) {
			t.Errorf("%s: Ok %s is not green (hue %.0f)", name, Hex(p.Ok), toOKLCH(p.Ok).H)
		}
		if !hueIn(p.Warn, 40, 100) {
			t.Errorf("%s: Warn %s is not amber (hue %.0f)", name, Hex(p.Warn), toOKLCH(p.Warn).H)
		}
		if !hueIn(p.Bad, 340, 40) {
			t.Errorf("%s: Bad %s is not red (hue %.0f)", name, Hex(p.Bad), toOKLCH(p.Bad).H)
		}
	}
	// tokyo-night's own colors fit, so they are used as they are.
	p := Derive(load(t, "tokyo-night"))
	if Hex(p.Ok) != "#9ece6a" || Hex(p.Bad) != "#f7768e" {
		t.Errorf("tokyo-night ok %s bad %s", Hex(p.Ok), Hex(p.Bad))
	}
}

// The built-in dark theme stays close to omaterm's look before themes.
func TestBuiltinDarkCloseToLegacy(t *testing.T) {
	p := Derive(BuiltinDark())
	close := func(role string, got ui.Color, want string) {
		a, b := toOKLCH(got), toOKLCH(ui.Hex(want))
		if d := abs(a.L - b.L); d > 0.04 {
			t.Errorf("%s = %s, want near %s (ΔL %.3f)", role, Hex(got), want, d)
		}
	}
	close("Wash", p.Wash, "#0b0e12")
	close("Card", p.Card, "#11161c")
	close("Text", p.Text, "#d7dde5")
	close("TextMuted", p.TextMuted, "#7f8a99")
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func TestOKLCHRoundTrip(t *testing.T) {
	for _, h := range []string{"#1a1b26", "#7aa2f7", "#f7768e", "#fafafa", "#000000", "#ffffff"} {
		if got := Hex(toOKLCH(ui.Hex(h)).color()); got != h {
			t.Errorf("%s → %s", h, got)
		}
	}
}

func TestEditorTheme(t *testing.T) {
	p := Derive(load(t, "tokyo-night"))
	et := EditorTheme(p)
	if et.Background != p.Code || et.Added != p.Ok {
		t.Error("editor theme does not follow the palette")
	}
}

// A palette counts its slots, not its lines: all 16 with one given twice
// was refused, and 0-7 each given twice was taken with 8-15 black.
func TestGhosttyPaletteSlots(t *testing.T) {
	dir := t.TempDir()
	write := func(lines []string) string {
		path := filepath.Join(dir, "ghostty.conf")
		os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
		return path
	}
	var all []string
	for i := 0; i < 16; i++ {
		all = append(all, fmt.Sprintf("palette = %d=#%02x0000", i, i*10))
	}
	if _, ok := ghosttyPalette(write(append(all, "palette = 3=#ffffff"))); !ok {
		t.Error("16 slots with one given again: refused")
	}
	half := append(append([]string{}, all[:8]...), all[:8]...)
	if _, ok := ghosttyPalette(write(half)); ok {
		t.Error("8 slots given twice: taken as 16")
	}
}
