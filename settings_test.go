package main

import (
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/theme"
)

// newSettingsTester builds the Settings window's view on a page.
func newSettingsTester(t *testing.T, page string) (*appState, *ui.Tester) {
	t.Helper()
	a := newTestApp(t)
	a.settingsUI = settingsUI{}
	a.openSettings(page)
	tr := ui.NewTester(a.settingsView, settingsWidth, settingsHeight)
	tr.Frame()
	return a, tr
}

// TestSettingsShots draws every page, dark and light, for a look against
// docs/mockups/settings-window.html: SETTINGS_SHOTS=dir go test -run
// TestSettingsShots .
func TestSettingsShots(t *testing.T) {
	dir := os.Getenv("SETTINGS_SHOTS")
	if dir == "" {
		t.Skip("set SETTINGS_SHOTS to a directory")
	}
	os.MkdirAll(dir, 0o755)
	defer setPalette(theme.BuiltinDark())
	for _, mode := range []string{"dark", "light"} {
		for _, p := range settingsPages {
			id := p.ID
			if id == "agent" {
				id = "agent:claude"
			}
			a, tr := newSettingsTester(t, id)
			if w := os.Getenv("SETTINGS_SHOTS_WIDTH"); w != "" {
				n, _ := strconv.Atoi(w)
				tr.SetSize(n, settingsHeight)
			}
			a.setThemeMode(mode)
			tr.Frame()
			tr.Frame()
			f, err := os.Create(filepath.Join(dir, "settings-"+mode+"-"+p.ID+".png"))
			if err != nil {
				t.Fatal(err)
			}
			png.Encode(f, tr.Image())
			f.Close()
			stopTermPanes(a)
		}
	}
}

// Every page draws, with its title in the toolbar.
func TestSettingsPagesRender(t *testing.T) {
	for _, p := range settingsPages {
		id := p.ID
		if id == "agent" {
			id = "agent:claude"
		}
		a, tr := newSettingsTester(t, id)
		want := a.settingsTitle(id, &p)
		if !tr.HasText(want) {
			t.Errorf("page %s: no title %q in %v", id, want, tr.Texts())
		}
		stopTermPanes(a)
	}
}

// A switch in the window changes the setting, and the palette agrees.
func TestSettingsSwitchAgreesWithPalette(t *testing.T) {
	a, tr := newSettingsTester(t, "appearance")
	defer stopTermPanes(a)
	before := a.settings.DimUnfocused
	if err := tr.Click("dim"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if a.settings.DimUnfocused == before {
		t.Fatal("the switch did not change dimUnfocused")
	}
	for _, e := range a.paletteSources() {
		if e.ID == "setting:dim" && e.On != a.settings.DimUnfocused {
			t.Errorf("palette shows %v, settings %v", e.On, a.settings.DimUnfocused)
		}
	}
}

// Search lists the settings that match under their pages, and choosing
// one opens its page.
func TestSettingsSearch(t *testing.T) {
	a, tr := newSettingsTester(t, "general")
	defer stopTermPanes(a)
	a.settingsUI.query = "font"
	tr.Frame()
	items := a.settingsNavItems()
	var pages []string
	for _, it := range items {
		if it.def == nil {
			pages = append(pages, it.page.ID)
		}
	}
	if !reflect.DeepEqual(pages, []string{"terminal", "editor"}) {
		t.Fatalf("pages for font: %v", pages)
	}
	if err := tr.Click("Use the terminal font"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if got := a.settingsCurrent(); got != "editor" {
		t.Errorf("page %q, want editor", got)
	}
	if a.settingsUI.flash != "editorFont" {
		t.Errorf("flash %q, want editorFont", a.settingsUI.flash)
	}
}

// Back and forward walk the pages visited.
func TestSettingsHistory(t *testing.T) {
	a, tr := newSettingsTester(t, "general")
	defer stopTermPanes(a)
	for _, p := range []string{"Terminal", "About"} {
		if err := tr.Click(p); err != nil {
			t.Fatal(err)
		}
		tr.Frame()
	}
	a.settingsBack()
	if got := a.settingsCurrent(); got != "terminal" {
		t.Fatalf("back: %q", got)
	}
	a.settingsForward()
	if got := a.settingsCurrent(); got != "about" {
		t.Fatalf("forward: %q", got)
	}
}

// Restore Defaults puts back the page's settings, and only them.
func TestSettingsRestoreDefaults(t *testing.T) {
	a, tr := newSettingsTester(t, "terminal")
	defer stopTermPanes(a)
	a.settings.Terminal.Cursor = "bar"
	a.settings.DimUnfocused = !defaultSettings().DimUnfocused
	tr.SetSize(settingsWidth, 1600)
	tr.Frame()
	if err := tr.Click("Restore Defaults"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if err := tr.Click("Restore"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if a.settings.Terminal.Cursor != "block" {
		t.Errorf("cursor %q, want block", a.settings.Terminal.Cursor)
	}
	if a.settings.DimUnfocused == defaultSettings().DimUnfocused {
		t.Error("restoring Terminal changed Appearance's dimUnfocused")
	}
}

// Every setting is in the window: a field of Settings has a row, or is
// named here as one that has none.
func TestSettingsCoverAll(t *testing.T) {
	noUI := map[string]bool{
		"Theme": true, // the tiles
	}
	shown := map[string]bool{}
	for _, d := range settingDefs {
		shown[strings.ToLower(d.ID)] = true
	}
	// The fields the defs cover, by the def that shows each.
	covers := map[string]string{
		"Density": "density", "Background": "background", "DimUnfocused": "dim", "FocusFollowsMouse": "focusmouse",
		"MinimumContrast": "contrast", "ReduceMotion": "motion", "ShowSystemLoad": "load", "ControlSocket": "socket",
		"RemoteControl": "agentcontrol", "Daemon": "daemon", "RestoreSessions": "restore", "ResumeAgents": "resume",
		"GitStatus": "snapshot", "ConfirmClose": "confirmClose", "OpenLinks": "openLinks", "SearchEngine": "searchEngine",
		"Notify.Waiting": "notify-waiting", "Notify.Done": "notify-done", "Notify.Error": "notify-error",
		"Notify.Sound": "notify-sound", "Notify.Programs": "notify-programs",
		"Terminal.FontFamily": "font", "Terminal.FontSize": "fontSize", "Terminal.LineHeight": "lineHeight",
		"Terminal.Ligatures": "ligatures", "Terminal.Cursor": "cursor", "Terminal.CursorBlink": "blink",
		"Terminal.CopyOnSelect": "copySelect", "Terminal.RightClick": "rightClick", "Terminal.ClipboardRead": "clipboardRead",
		"Terminal.ScrollbackMB": "scrollbackSize", "Terminal.Shell": "shell", "Terminal.Bell": "bell",
		"Editor.TerminalFont": "editorFont", "Editor.TabSize": "tabSize", "Editor.Wrap": "wrap",
		"Editor.LineNumbers": "lineNumbers", "Editor.MarkdownPreview": "markdown", "Editor.DiffSplit": "diffLayout",
	}
	var walk func(prefix string, typ reflect.Type)
	walk = func(prefix string, typ reflect.Type) {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			name := prefix + f.Name
			if f.Type.Kind() == reflect.Struct {
				walk(name+".", f.Type)
				continue
			}
			if noUI[name] {
				continue
			}
			id, ok := covers[name]
			if !ok {
				t.Errorf("setting %s has no row in the Settings window (or add it to noUI)", name)
				continue
			}
			if !shown[strings.ToLower(id)] {
				t.Errorf("setting %s: no def %q", name, id)
			}
		}
	}
	walk("", reflect.TypeOf(Settings{}))
}

// The Keyboard page filters its own list; it opened the shortcuts dialog
// in the main window, which the Settings window covers.
func TestSettingsKeyboardFilters(t *testing.T) {
	a, tr := newSettingsTester(t, "keyboard")
	defer stopTermPanes(a)
	if tr.HasText("All Keyboard Shortcuts") {
		t.Fatal("the page still sends the user to a dialog behind the window")
	}
	a.settingsUI.keyQuery = "split"
	tr.Frame()
	if !tr.HasText("Split Terminal Right") || tr.HasText("Quick Open") {
		t.Fatalf("filtering \"split\" shows %v", tr.Texts())
	}
	if a.overlay != overlayNone {
		t.Fatal("an overlay opened in the main window")
	}
}

// A field takes its text with Enter; Escape puts back what it had, and
// doesn't close the window.
func TestSettingsFieldEnterAndEscape(t *testing.T) {
	a, tr := newSettingsTester(t, "terminal")
	defer stopTermPanes(a)
	tr.SetSize(settingsWidth, 1600)
	tr.Frame()
	if err := tr.Click("shell"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	tr.Type("/bin/nope")
	tr.Frame()
	tr.Key(0, ui.KeyEscape)
	tr.Frame()
	if a.settings.Terminal.Shell != "" {
		t.Fatalf("Escape kept %q", a.settings.Terminal.Shell)
	}
	if err := tr.Click("shell"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	tr.Type("/bin/zsh")
	tr.Key(0, ui.KeyEnter)
	tr.Frame()
	if a.settings.Terminal.Shell != "/bin/zsh" {
		t.Fatalf("Enter gave %q", a.settings.Terminal.Shell)
	}
}

// Picking Custom… keeps the search engine in use until a URL that works
// is given: it saved "https://", and every search broke.
func TestCustomSearchEngineWaitsForAURL(t *testing.T) {
	a, tr := newSettingsTester(t, "browser")
	defer stopTermPanes(a)
	a.settingsUI.data.searchCustom = true
	a.settingsUI.data.customSearch = "https://"
	tr.Frame()
	if a.settings.SearchEngine != "duckduckgo" {
		t.Fatalf("search engine %q, want duckduckgo kept", a.settings.SearchEngine)
	}
	if !tr.HasText("Type a URL with %s where the words go, then press Enter. Until then the search engine above stays.") {
		t.Fatal("no hint for the custom URL")
	}
}

// Restore Defaults on Terminal puts back the font, and the page's field.
func TestRestoreTerminalClearsFields(t *testing.T) {
	a, tr := newSettingsTester(t, "terminal")
	defer stopTermPanes(a)
	a.settings.Terminal.FontFamily = "Iosevka"
	a.settingsUI.data.customFont = "Iosevka"
	tr.SetSize(settingsWidth, 1800)
	tr.Frame()
	if err := tr.Click("Restore Defaults"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if err := tr.Click("Restore"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if a.settings.Terminal.FontFamily != "" || a.settingsUI.data.customFont != "" {
		t.Fatalf("font %q, field %q after restoring", a.settings.Terminal.FontFamily, a.settingsUI.data.customFont)
	}
}

// Every setting search finds leads to a row or group of its page, which
// lights up; five led nowhere.
func TestSettingsSearchHitsLeadSomewhere(t *testing.T) {
	a, tr := newSettingsTester(t, "general")
	defer stopTermPanes(a)
	tr.SetSize(settingsWidth, 4000)
	for _, d := range settingDefs {
		a.settingsGo(d.Page)
		tr.Frame()
		if !a.settingsUI.shown[d.ID] {
			t.Errorf("search hit %q (%s) leads to nothing on page %s", d.ID, d.Title, d.Page)
		}
	}
}

// Enter in the search opens the first setting found, not just its page.
func TestSettingsSearchEnterOpensSetting(t *testing.T) {
	a, tr := newSettingsTester(t, "general")
	defer stopTermPanes(a)
	if err := tr.Click("Search settings"); err != nil {
		t.Fatal(err)
	}
	tr.Type("ligatures")
	tr.Key(0, ui.KeyEnter)
	tr.Frame()
	if a.settingsCurrent() != "terminal" || a.settingsUI.flash != "ligatures" {
		t.Fatalf("page %q, lit %q", a.settingsCurrent(), a.settingsUI.flash)
	}
}

// "Open settings file" lines up with the pages above it: its icon and
// label sat 2 and 4px left of theirs.
func TestSettingsFooterAligned(t *testing.T) {
	_, tr := newSettingsTester(t, "general")
	foot, ok := tr.Find("Open settings file")
	if !ok {
		t.Fatal("no footer")
	}
	nav, ok := tr.Find("Appearance")
	if !ok {
		t.Fatal("no Appearance page")
	}
	// The row, as its label names it; its text starts after its padding,
	// icon and gap.
	if want := nav.X + 8 + 14 + 8; foot.X != want {
		t.Errorf("footer text at x=%.0f, the pages' at x=%.0f", foot.X, want)
	}
}
