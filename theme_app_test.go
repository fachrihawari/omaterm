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
