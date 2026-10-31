package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui/editor"

	"omaterm/internal/theme"
)

// Colors come from the palette: a hex or RGBA literal outside the theme
// files would ignore the user's theme.
func TestNoStrayColors(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "theme.go" || f == "elevation.go" {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "ui" {
					switch sel.Sel.Name {
					case "Hex", "RGBA", "RGB":
						t.Errorf("%s: ui.%s: use a palette color", fset.Position(call.Pos()), sel.Sel.Name)
					}
				}
			}
			return true
		})
	}
}

// Switching the theme recolors open editors, not only new ones.
func TestApplyThemeUpdatesEditors(t *testing.T) {
	a := newApp()
	a.win = nil
	st := &editor.State{Buffer: editor.NewBuffer("x"), Theme: omaEditorTheme()}
	a.editors = map[string]*openFile{"x.go": {Path: "x.go", State: st}}
	before := st.Theme
	a.applyTheme(theme.BuiltinLight())
	defer a.applyTheme(theme.BuiltinDark())
	if st.Theme == before || st.Theme.Background != pal.Code {
		t.Fatal("open editor kept the old theme")
	}
	if pal.Dark {
		t.Fatal("palette is still dark")
	}
}

// With Omarchy's theme dir pointing at a fixture, the app follows it.
func TestFollowsOmarchyTheme(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "theme"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join("internal", "theme", "testdata", "tokyo-night.toml"))
	os.WriteFile(filepath.Join(dir, "theme", "colors.toml"), data, 0o644)
	t.Setenv("OMATERM_OMARCHY_DIR", dir)
	src := themeSource(themeOmarchy)
	if theme.Hex(src.Background) != "#1a1b26" {
		t.Fatalf("background %s", theme.Hex(src.Background))
	}
	if themeSource(themeLight).Dark {
		t.Fatal("light mode gave a dark theme")
	}
}

// Without Omarchy, as on a Mac, nothing offers to follow it: not the
// palette, the theme's caption or the font popup, which names the font
// that renders instead.
func TestNoOmarchyWordingWithoutOmarchy(t *testing.T) {
	for _, omarchy := range []bool{false, true} {
		dir := os.DevNull + "-omarchy"
		if omarchy {
			dir = t.TempDir()
		}
		t.Setenv("OMATERM_OMARCHY_DIR", dir)
		a, tr := newSettingsTester(t, "appearance")
		a.cmdQuery = "theme"
		found := false
		for _, e := range a.paletteCommands() {
			found = found || e.ID == "theme.omarchy"
		}
		if found != omarchy {
			t.Errorf("omarchy=%v: palette offers Follow Omarchy %v", omarchy, found)
		}
		if got := tr.HasText("Omarchy follows your Omarchy theme as it changes. System follows light or dark mode."); got != omarchy {
			t.Errorf("omarchy=%v: caption names Omarchy %v", omarchy, got)
		}
		a.settingsGo("terminal")
		tr.Frame()
		if err := tr.Click("font"); err != nil {
			t.Fatal(err)
		}
		tr.Frame()
		named := false
		for _, s := range tr.Texts() {
			named = named || strings.HasPrefix(s, "Follow Omarchy (")
		}
		if named != omarchy {
			t.Errorf("omarchy=%v: font popup says Follow Omarchy %v: %q", omarchy, named, tr.Texts())
		}
		stopTermPanes(a)
	}
}

// The default font is named by the first family installed, else the
// system's monospace: SF Mono on a Mac.
func TestDefaultFontName(t *testing.T) {
	const list = "JetBrainsMono Nerd Font, JetBrains Mono, monospace"
	for _, c := range []struct {
		installed map[string]bool
		mac       bool
		want      string
	}{
		{map[string]bool{"jetbrains mono": true}, true, "JetBrains Mono"},
		{map[string]bool{"jetbrainsmono nerd font": true, "jetbrains mono": true}, false, "JetBrainsMono Nerd Font"},
		{map[string]bool{"menlo": true}, true, "SF Mono"},
		{nil, false, "the system's monospace"},
	} {
		if got := defaultFontName(list, c.installed, c.mac); got != c.want {
			t.Errorf("%v mac=%v: %q, want %q", c.installed, c.mac, got, c.want)
		}
	}
}

// A Ghostty config's last font-family is its font.
func TestGhosttyFont(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	os.WriteFile(path, []byte("font-family = \"Fira Code\"\ntheme = x\nfont-family=Iosevka\n"), 0o644)
	if got := ghosttyFont(path); got != "Iosevka" {
		t.Errorf("font %q, want Iosevka", got)
	}
	if got := ghosttyFont(path + "-none"); got != "" {
		t.Errorf("no config gave %q", got)
	}
}
