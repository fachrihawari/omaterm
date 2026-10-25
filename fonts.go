package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

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
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	f, err := os.Open(filepath.Join(dir, "ghostty", "config"))
	if err != nil {
		return fallback
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
	if family == "" {
		return fallback
	}
	return family + ", " + fallback
}
