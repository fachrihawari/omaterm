package main

import (
	"image/png"
	"os"
	"path/filepath"
	"reflect"
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
		// Fields of their own pages' controls, which settingDefs name by
		// other IDs.
		"BrowserBlockAds": true, // no filter list yet
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
