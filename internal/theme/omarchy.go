package theme

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/egoist/mygo/ui"
)

// Dir is where Omarchy keeps the active theme: theme.name and the theme
// directory, which omarchy-theme-set replaces whole on a switch, so a
// watcher has to watch this directory rather than the theme's own.
// $OMATERM_OMARCHY_DIR overrides it, for tests.
func Dir() string {
	if d := os.Getenv("OMATERM_OMARCHY_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "omarchy", "current")
}

// LoadOmarchy reads the active Omarchy theme from Dir.
func LoadOmarchy() (Source, error) {
	dir := Dir()
	src, err := ParseColors(filepath.Join(dir, "theme", "colors.toml"))
	if err != nil {
		return Source{}, err
	}
	if name, err := os.ReadFile(filepath.Join(dir, "theme.name")); err == nil {
		src.Name = strings.TrimSpace(string(name))
	}
	if conf := filepath.Join(dir, "theme", "ghostty.conf"); fileExists(conf) {
		if pal, ok := ghosttyPalette(conf); ok {
			src.ANSI = pal
		}
		src.GhosttyConf = conf
	}
	return src, nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// ParseColors reads an Omarchy colors.toml. Its ANSI slots follow
// Omarchy's templates: 0 background, 1–6 red, green, yellow, blue,
// magenta, cyan, 7 foreground, 8 muted, 9–14 the bright ones, 15
// bright_foreground.
func ParseColors(path string) (Source, error) {
	var m map[string]string
	if _, err := toml.DecodeFile(path, &m); err != nil {
		return Source{}, fmt.Errorf("theme: %s: %w", path, err)
	}
	return fromColors(m)
}

func fromColors(m map[string]string) (Source, error) {
	get := func(key string, fallback ui.Color) ui.Color {
		if v, ok := m[key]; ok {
			if c, err := parseColor(v); err == nil {
				return c
			}
		}
		return fallback
	}
	bg, err := parseColor(m["background"])
	if err != nil {
		return Source{}, fmt.Errorf("theme: background: %w", err)
	}
	fg, err := parseColor(m["foreground"])
	if err != nil {
		return Source{}, fmt.Errorf("theme: foreground: %w", err)
	}
	src := Source{Background: bg, Foreground: fg, Extra: map[string]ui.Color{}}
	switch m["mode"] {
	case "dark":
		src.Dark = true
	case "light":
		src.Dark = false
	default:
		src.Dark = luminance(bg) < 0.4*0.4
	}
	src.BrightForeground = get("bright_foreground", fg)
	src.Muted = get("muted", fg.Mix(bg, 0.5))
	src.Accent = get("accent", get("blue", fg))
	src.Selection = get("selection", src.Accent.Alpha(0.28).Over(bg))
	names := []string{"red", "green", "yellow", "blue", "magenta", "cyan"}
	src.ANSI[0], src.ANSI[7], src.ANSI[8], src.ANSI[15] = bg, fg, src.Muted, src.BrightForeground
	for i, n := range names {
		c := get(n, fg)
		src.ANSI[1+i] = c
		src.ANSI[9+i] = get("bright_"+n, c)
	}
	for _, n := range []string{"orange", "brown"} {
		if c, ok := m[n]; ok {
			if v, err := parseColor(c); err == nil {
				src.Extra[n] = v
			}
		}
	}
	return src, nil
}

func parseColor(s string) (ui.Color, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return ui.Color{}, fmt.Errorf("bad color %q", s)
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return ui.Color{}, fmt.Errorf("bad color %q", s)
	}
	return ui.Color{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}, nil
}

// ghosttyPalette reads the palette lines of a ghostty.conf, which a theme
// may tune past what colors.toml says.
func ghosttyPalette(path string) ([16]ui.Color, bool) {
	var pal [16]ui.Color
	data, err := os.ReadFile(path)
	if err != nil {
		return pal, false
	}
	var set [16]bool // each slot once, however many lines name it
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok || strings.TrimSpace(k) != "palette" {
			continue
		}
		idx, col, ok := strings.Cut(strings.TrimSpace(v), "=")
		if !ok {
			continue
		}
		i, err := strconv.Atoi(strings.TrimSpace(idx))
		if err != nil || i < 0 || i > 15 {
			continue
		}
		c, err := parseColor(col)
		if err != nil {
			continue
		}
		pal[i] = c
		set[i] = true
	}
	for _, ok := range set {
		if !ok {
			return pal, false
		}
	}
	return pal, true
}
