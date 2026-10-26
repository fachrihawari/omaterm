package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// The UI audit's guards: defects of a kind found once, checked on every
// screen they can appear on, so the kind does not come back.

// dist is how far apart two colors are.
func dist(a, b color.RGBA) float64 {
	dr, dg, db := float64(a.R)-float64(b.R), float64(a.G)-float64(b.G), float64(a.B)-float64(b.B)
	return math.Sqrt(dr*dr + dg*dg + db*db)
}

func at(img *image.RGBA, x, y float32) color.RGBA {
	return img.RGBAAt(int(x), int(y))
}

// squareCorners returns the corners of a rounded card (bounds r, radius
// rad) that show the card's own colors where the rounding should show
// what is behind it: just inside the bounds, outside the arc.
func squareCorners(img *image.RGBA, r ui.Rect, rad float32) []string {
	in := rad * 0.2 // well outside the arc, inside the bounds
	type corner struct {
		name         string
		x, y         float32 // the point to look at
		outX, outY   float32 // behind the card, next to the corner
		cardX, cardY float32 // the card itself, near the corner
	}
	cs := []corner{
		{"top-left", r.X + in, r.Y + in, r.X - 3, r.Y + in, r.X + rad + 4, r.Y + rad + 4},
		{"top-right", r.X + r.W - in - 1, r.Y + in, r.X + r.W + 2, r.Y + in, r.X + r.W - rad - 5, r.Y + rad + 4},
		{"bottom-left", r.X + in, r.Y + r.H - in - 1, r.X - 3, r.Y + r.H - in - 1, r.X + rad + 4, r.Y + r.H - rad - 5},
		{"bottom-right", r.X + r.W - in - 1, r.Y + r.H - in - 1, r.X + r.W + 2, r.Y + r.H - in - 1, r.X + r.W - rad - 5, r.Y + r.H - rad - 5},
	}
	var bad []string
	for _, c := range cs {
		p, out, card := at(img, c.x, c.y), at(img, c.outX, c.outY), at(img, c.cardX, c.cardY)
		if dist(out, card) < 6 {
			continue // card and backdrop alike here: nothing to tell
		}
		if dist(p, card) < dist(p, out) {
			bad = append(bad, fmt.Sprintf("%s (corner %v, card %v, behind %v)", c.name, p, card, out))
		}
	}
	return bad
}

// Pane cards are round at all four corners, the terminal reaching their
// bottom edge or not, alone or split.
func TestAuditPaneCorners(t *testing.T) {
	for _, theme := range []string{"tokyo-night", "lupine"} {
		t.Run(theme, func(t *testing.T) {
			a := newTestApp(t)
			defer stopTermPanes(a)
			a.applyTheme(shotTheme(t, theme))
			a.splitPane(a.activeSplitLeaf(), splitVertical)
			bounds := map[string]ui.Rect{}
			view := func(c *ui.Context) {
				a.view(c)
				for _, id := range a.activeSplit().termIDs() {
					bounds[id] = a.handle("card-" + id).Bounds(c)
				}
			}
			tr := ui.NewTester(view, 1440, 900)
			tr.Frame()
			tr.Frame()
			img := tr.Image()
			for id, r := range bounds {
				if r.W == 0 {
					t.Fatalf("pane %s has no bounds", id)
				}
				if bad := squareCorners(img, r, a.cardRadius()); len(bad) > 0 {
					t.Errorf("pane %s: square corners %v", id, bad)
				}
			}
		})
	}
}

// luminance is a color's WCAG relative luminance.
func luminance(c color.RGBA) float64 {
	lin := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

func contrastRatio(a, b color.RGBA) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// textContrast measures how a text reads where it is drawn: its rectangle's
// commonest color is the background, and the pixel furthest from it the
// glyphs' own color.
func textContrast(img *image.RGBA, r ui.Rect, scale float32) float64 {
	x0, y0 := int(r.X*scale), int(r.Y*scale)
	x1, y1 := int((r.X+r.W)*scale), int((r.Y+r.H)*scale)
	counts := map[color.RGBA]int{}
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			counts[img.RGBAAt(x, y)]++
		}
	}
	var bg color.RGBA
	for c, n := range counts {
		if n > counts[bg] {
			bg = c
		}
	}
	best := 1.0
	for c := range counts {
		if k := contrastRatio(c, bg); k > best {
			best = k
		}
	}
	return best
}

// A text that must read, and how well: 4.5 for what is read, 3 for what
// is secondary (WCAG AA for text and large text).
type readable struct {
	text string
	min  float64
}

// auditScene renders the app as setup leaves it, at twice the size so the
// glyphs have solid cores, and checks each text reads.
func auditScene(t *testing.T, theme string, setup func(a *appState) []readable) {
	t.Helper()
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.applyTheme(shotTheme(t, theme))
	checks := setup(a)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.SetScale(2)
	tr.Frame()
	tr.Frame()
	img := tr.Image()
	for _, ch := range checks {
		r, ok := tr.Find(ch.text)
		if !ok {
			t.Errorf("%s: %q not shown", theme, ch.text)
			continue
		}
		if k := textContrast(img, r, 2); k < ch.min {
			t.Errorf("%s: %q reads at %.2f:1, want %.1f", theme, ch.text, k, ch.min)
		}
	}
}

var auditThemes = []string{"tokyo-night", "lupine", "white"}

// The row a list shows chosen reads, in every list and theme: its text,
// what is secondary in it, and its key chips.
func TestAuditSelectedRowsRead(t *testing.T) {
	scenes := map[string]func(a *appState) []readable{
		"palette": func(a *appState) []readable {
			a.openOverlay(overlayCommandPalette)
			a.cmdQuery = "split terminal right"
			return []readable{{"Split Terminal Right", 4.5}, {"Ctrl+Shift+R", 3}}
		},
		"palette-setting": func(a *appState) []readable {
			a.openOverlay(overlayCommandPalette)
			a.cmdQuery = "dim unfocused"
			return []readable{{"Dim Unfocused Panes", 4.5}, {"Setting:", 3}}
		},
		"quick-open": func(a *appState) []readable {
			a.fileIndex = []string{"app.go", "service/git.go"}
			a.openOverlay(overlayQuickOpen)
			a.quickQuery = "git"
			return []readable{{"service", 3}}
		},
		"shortcuts": func(a *appState) []readable {
			a.openOverlay(overlayShortcuts)
			a.keyQuery = "command palette"
			return []readable{{"Command Palette…", 4.5}}
		},
		"branches": func(a *appState) []readable {
			a.openBranchOverlay()
			a.branches = []string{"feature/audit", "main"}
			a.branchQuery = ""
			a.branchSelected = 0
			a.currentBranch = "feature/audit"
			return []readable{{"feature/audit", 4.5}, {"current", 4.5}}
		},
	}
	for _, theme := range auditThemes {
		for name, setup := range scenes {
			t.Run(theme+"/"+name, func(t *testing.T) { auditScene(t, theme, setup) })
		}
	}
}

// The palette puts first what a query names: titles before categories.
func TestAuditPaletteRanking(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	for q, want := range map[string]string{
		"setup":           "agents.setup",
		"split right":     "terminal.splitRight",
		"zoom":            "pane.zoom",
		"> theme light":   "theme.light",
		"dim unfocused":   "setting:dim",
		"restart session": "daemon.restart",
	} {
		a.cmdQuery = q
		got := a.paletteCommands()
		if len(got) == 0 || got[0].ID != want {
			first := ""
			if len(got) > 0 {
				first = got[0].ID + " (" + got[0].Category + ": " + got[0].Title + ")"
			}
			t.Errorf("%q: first %s, want %s", q, first, want)
		}
	}
	// A category alone still finds its entries, after the command that
	// opens the Settings window, which its title names.
	a.cmdQuery = "setting"
	if got := a.paletteCommands(); len(got) < 2 || got[0].ID != "app.settings" || !got[1].Setting {
		t.Errorf(`"setting" finds no Settings… then a setting first`)
	}
}

// The Git panel: a disabled Commit still reads, and a file at the root
// sits on its row's centre line with its badge.
func TestAuditGitPanel(t *testing.T) {
	for _, theme := range auditThemes {
		t.Run(theme, func(t *testing.T) {
			auditScene(t, theme, func(a *appState) []readable {
				a.inspectorTab, a.inspectorVisible = 2, true
				a.commitMsg = ""
				return []readable{{"Commit", 3}}
			})
		})
	}
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.inspectorTab, a.inspectorVisible = 2, true
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	tr.Frame()
	name, ok1 := tr.Find("hello.txt")
	mark, ok2 := tr.Find("M")
	if !ok1 || !ok2 {
		t.Fatalf("file row not shown: %q", tr.Texts())
	}
	if d := (name.Y + name.H/2) - (mark.Y + mark.H/2); d > 2 || d < -2 {
		t.Fatalf("file name %v and status %v off by %.1f", name, mark, d)
	}
}

// Patterns that made defects, kept out of the source:
//   - a Clip without a Radius: the GPU rounds only the innermost clip, so
//     a square clip inside a rounded card squares the card's corners;
//   - a disabled element faded again: it already paints at half opacity,
//     which twice over leaves it unreadable.
func TestAuditSourcePatterns(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(b), "\n")
		for i, l := range lines {
			if strings.Contains(l, ".Clip()") {
				// The statement: this line and the ones it continues.
				stmt := l
				for j := i - 1; j >= 0 && strings.HasSuffix(strings.TrimSpace(lines[j]), "."); j-- {
					stmt = lines[j] + stmt
				}
				if !strings.Contains(stmt, "Radius(") {
					t.Errorf("%s:%d: Clip without a Radius squares a rounded card's corners", f, i+1)
				}
			}
			if strings.Contains(l, "Disabled(") && (strings.Contains(l, ".Opacity(0.") || strings.Contains(l, ".Alpha(0.")) {
				t.Errorf("%s:%d: a disabled element faded twice", f, i+1)
			}
		}
	}
}

// In the smallest window, a pane's title gives way to its status chip
// instead of drawing over it.
func TestAuditNarrowPaneHeader(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.projects[0].Tabs = []string{"dev", "api", "tests"}
	a.ws, a.wsOwner, a.active = map[string]*projectWS{}, "", -1
	a.setProject(0)
	shotStatuses(a)
	tr := ui.NewTester(a.view, 900, 560)
	tr.Frame()
	tr.Frame()
	title, ok := tr.Find("Claude Code")
	if !ok {
		t.Fatalf("header not shown: %q", tr.Texts())
	}
	// Too narrow for it, the chip goes; shown, it is beside the title.
	chip, shown := tr.Find("Needs permission")
	if shown && title.X+title.W > chip.X+0.5 && title.Y < chip.Y+chip.H && chip.Y < title.Y+title.H {
		t.Fatalf("title %v overlaps the chip %v", title, chip)
	}
}
