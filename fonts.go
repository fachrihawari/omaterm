package main

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// terminalFontFamily is the font the settings pick, else Omarchy's.
func (a *appState) terminalFontFamily() string {
	if f := strings.TrimSpace(a.settings.Terminal.FontFamily); f != "" {
		return f + ", JetBrainsMono Nerd Font, JetBrains Mono, monospace"
	}
	return termFontFamily()
}

// termFont is the terminals' font as the settings have it: family, size,
// line height and ligatures.
func (a *appState) termFontSpec() terminal.Font {
	f := terminal.Font{Family: a.termFont, Size: a.fontSize, LineHeight: a.settings.Terminal.LineHeight}
	if !a.settings.Terminal.Ligatures {
		f.Features = []string{"-calt", "-liga"}
	}
	return f
}

// editorFont is the editors' font: the terminals' family when the
// settings say so, at the shared size.
func (a *appState) editorFont(size float32) ui.Font {
	f := ui.Font{Size: size}
	if a.settings.Editor.TerminalFont {
		f.Family = a.termFont
	}
	return f
}

// termFontFamily is the terminals' and editors' font: the one Omarchy's
// font picker writes to Ghostty's config, so every terminal on the machine
// shares it, falling back to JetBrains Mono.
func termFontFamily() string {
	const fallback = "JetBrainsMono Nerd Font, JetBrains Mono, monospace"
	dir := os.Getenv("XDG_CONFIG_HOME")
	home, _ := os.UserHomeDir()
	if dir == "" {
		dir = filepath.Join(home, ".config")
	}
	configs := []string{filepath.Join(dir, "ghostty", "config")}
	if runtime.GOOS == "darwin" {
		// Ghostty reads this one on macOS too, after the other.
		configs = append(configs, filepath.Join(home, "Library", "Application Support", "com.mitchellh.ghostty", "config"))
	}
	family := ""
	for _, path := range configs {
		if f := ghosttyFont(path); f != "" {
			family = f
		}
	}
	if family == "" {
		return fallback
	}
	return family + ", " + fallback
}

// ghosttyFont is the last font-family a Ghostty config sets, if any.
func ghosttyFont(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	family := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if ok && strings.TrimSpace(k) == "font-family" {
			family = strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return family
}

// defaultFontName is the family the terminals' default font renders in,
// for the font popup to name where Omarchy's isn't what it follows: the
// first family of the list that is installed, else the system's
// monospace, which is SF Mono on a Mac and Cascadia Mono on Windows.
func defaultFontName(families string, installed map[string]bool, goos string) string {
	for _, f := range strings.Split(families, ",") {
		if f = strings.TrimSpace(f); f == "monospace" {
			break
		} else if installed[strings.ToLower(f)] {
			return f
		}
	}
	switch goos {
	case "darwin":
		return "SF Mono"
	case "windows":
		return "Cascadia Mono"
	}
	return "the system's monospace"
}

// installedFonts are the system's font families, lowercased, read once:
// ui.FontFamilies keeps its list until a restart anyway.
var installedFonts = sync.OnceValue(func() map[string]bool {
	m := map[string]bool{}
	for _, f := range ui.FontFamilies() {
		m[strings.ToLower(f.Name)] = true
	}
	return m
})
