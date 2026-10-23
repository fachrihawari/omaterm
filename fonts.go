package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

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
