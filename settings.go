package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"time"
)

// Settings are the user's preferences, in settings.json beside state.json.
// Fields missing from the file keep their defaults, so adding one never
// needs a migration.
type Settings struct {
	// Theme is "omarchy" (follow the active Omarchy theme), "system"
	// (light or dark as the desktop is), "dark" or "light".
	Theme string `json:"theme"`
	// Density is "comfortable" or "compact".
	Density string `json:"density"`
	// Background is "wash" (a soft gradient behind the cards) or "plain".
	Background   string `json:"background"`
	DimUnfocused bool   `json:"dimUnfocused"`
	// FocusFollowsMouse gives the keyboard to the pane the pointer enters.
	FocusFollowsMouse bool `json:"focusFollowsMouse"`
	// MinimumContrast is the terminals' minimum contrast ratio; 0 picks 1
	// in dark themes and 3 in light ones.
	MinimumContrast float32 `json:"minimumContrast"`
	ReduceMotion    bool    `json:"reduceMotion"`
	// ShowSystemLoad shows the CPU and memory in the status bar.
	ShowSystemLoad bool `json:"showSystemLoad"`

	Notify NotifySettings `json:"notify"`

	// ControlSocket lets the omaterm command reach the app; RemoteControl
	// ("Let Agents Control the App") lets it run the app's commands and
	// drive browser panes, not only sessions.
	ControlSocket bool `json:"controlSocket"`
	RemoteControl bool `json:"remoteControl"`

	// Daemon runs the sessions' shells in a process of their own, so
	// they go on when the window closes.
	Daemon          bool `json:"daemon"`
	RestoreSessions bool `json:"restoreSessions"`
	ResumeAgents    bool `json:"resumeAgents"`
	// GitStatus snapshots the work tree as each agent turn starts, which
	// the Git tab's Last turn compares against.
	GitStatus bool `json:"gitStatus"`
	// ConfirmClose asks before closing a session that still runs a
	// program other than its shell.
	ConfirmClose bool `json:"confirmClose"`
	// OpenLinks is where a link clicked in a terminal opens: "pane" (a
	// browser pane) or "system" (the default browser).
	OpenLinks string `json:"openLinks"`

	// SearchEngine is what the browser's address bar searches with: a
	// name of searchEngines, or a URL with %s for the words.
	SearchEngine string `json:"searchEngine"`

	Terminal TerminalSettings `json:"terminal"`
	Editor   EditorSettings   `json:"editor"`
}

// NotifySettings choose which session changes notify while you look away.
type NotifySettings struct {
	Waiting bool `json:"waiting"`
	Done    bool `json:"done"`
	Error   bool `json:"error"`
	// Sound plays the desktop's notification sound; Programs shows the
	// notifications programs send themselves (OSC 9 and 777).
	Sound    bool `json:"sound"`
	Programs bool `json:"programs"`
}

// TerminalSettings are the terminals' look and behavior.
type TerminalSettings struct {
	// FontFamily is the terminals' font; empty follows Omarchy's, which is
	// Ghostty's (fonts.go).
	FontFamily string `json:"fontFamily"`
	// FontSize is the terminal's and the editor's text size in DIPs; 0 is
	// the default 13.
	FontSize float32 `json:"fontSize"`
	// LineHeight is the rows' height as a multiple of the font's; 0 is 1.
	LineHeight float32 `json:"lineHeight"`
	Ligatures  bool    `json:"ligatures"`
	// Cursor is "block", "bar" or "underline".
	Cursor      string `json:"cursor"`
	CursorBlink bool   `json:"cursorBlink"`
	// CopyOnSelect copies a selection to the clipboard as it ends.
	CopyOnSelect bool `json:"copyOnSelect"`
	// RightClick is "menu" or "paste".
	RightClick string `json:"rightClick"`
	// ClipboardRead is whether programs may read the clipboard (OSC 52):
	// "never" or "ask".
	ClipboardRead string `json:"clipboardRead"`
	// ScrollbackMB is how much output a pane keeps above its screen, in
	// MB; 0 is 10.
	ScrollbackMB int `json:"scrollbackMB"`
	// Shell runs in new panes; empty is the login shell.
	Shell string `json:"shell"`
	// Bell is what a program's bell does: "off", "visual" (the pane's
	// header flashes) or "notify" (a notification while you look away).
	Bell string `json:"bell"`
	// OptionAsMeta makes Option the Meta key on macOS. Off, as in
	// Terminal.app, iTerm and Ghostty, Option types accented letters and
	// symbols, which many keyboard layouts need.
	OptionAsMeta bool `json:"optionAsMeta"`
}

// EditorSettings are the file editor's and the diff's.
type EditorSettings struct {
	// TerminalFont gives the editor the terminals' font family.
	TerminalFont bool `json:"terminalFont"`
	// TabSize overrides the language's; 0 keeps it.
	TabSize         int  `json:"tabSize"`
	Wrap            bool `json:"wrap"`
	LineNumbers     bool `json:"lineNumbers"`
	MarkdownPreview bool `json:"markdownPreview"`
	// DiffSplit shows diffs side by side rather than unified.
	DiffSplit bool `json:"diffSplit"`
}

// themeSystem follows the desktop's light or dark appearance.
const themeSystem = "system"

func defaultSettings() Settings {
	return Settings{
		Theme:             defaultTheme(),
		Density:           "comfortable",
		Background:        "wash",
		DimUnfocused:      true,
		FocusFollowsMouse: true,
		ShowSystemLoad:    true,
		Notify:            NotifySettings{Waiting: true, Done: true, Error: true, Sound: true, Programs: true},
		ControlSocket:     true,
		Daemon:            true,
		RestoreSessions:   true,
		ResumeAgents:      true,
		GitStatus:         true,
		ConfirmClose:      true,
		OpenLinks:         "system",
		SearchEngine:      "duckduckgo",
		Terminal: TerminalSettings{
			Ligatures: true, Cursor: "block", CursorBlink: true,
			RightClick: "menu", ClipboardRead: "never", Bell: "off",
		},
		Editor: EditorSettings{TerminalFont: true, LineNumbers: true, MarkdownPreview: true, DiffSplit: true},
	}
}

// defaultTheme follows Omarchy where it is, the desktop elsewhere.
func defaultTheme() string {
	if hasOmarchy() {
		return themeOmarchy
	}
	return themeSystem
}

func settingsFilePath() string {
	return filepath.Join(filepath.Dir(stateFilePath()), "settings.json")
}

// settingsBroken is the copy of a settings.json that couldn't be read,
// kept before the defaults are saved over it; settingsWrongKey a key the
// file gave a value of the wrong type, which was passed over.
var settingsBroken, settingsWrongKey string

// settingsBase are the settings as the app last read or wrote them: a
// save writes into the file only what changed since, so what was edited
// in the file meanwhile (by hand, or by another omaterm) stays, as do
// keys this version doesn't know.
var settingsBase Settings

// loadSettings reads settings.json over the defaults; a missing or broken
// file gives the defaults. A broken one, as a slip in an editor leaves it,
// is copied aside first. A value of the wrong type passes over that key
// alone: it reset every setting.
func loadSettings() Settings {
	s := defaultSettings()
	if data, err := os.ReadFile(settingsFilePath()); err == nil {
		if err := json.Unmarshal(data, &s); err != nil {
			var wrong *json.UnmarshalTypeError
			if errors.As(err, &wrong) {
				settingsWrongKey = wrong.Field
			} else {
				s = defaultSettings()
				bad := settingsFilePath() + ".bad-" + time.Now().Format("20060102-150405")
				if os.WriteFile(bad, data, 0o600) == nil {
					settingsBroken = bad
				}
			}
		}
	}
	switch s.Theme {
	case themeOmarchy, themeSystem, themeDark, themeLight:
	default:
		s.Theme = defaultTheme()
	}
	settingsBase = copySettings(s)
	return s
}

// copySettings is a copy of s that shares nothing with it.
func copySettings(s Settings) Settings {
	var c Settings
	data, _ := json.Marshal(s)
	json.Unmarshal(data, &c)
	return c
}

// settingsFileHas tells whether settings.json has the key at its top.
func settingsFileHas(key string) bool {
	var m map[string]json.RawMessage
	data, err := os.ReadFile(settingsFilePath())
	if err != nil || json.Unmarshal(data, &m) != nil {
		return false
	}
	_, ok := m[key]
	return ok
}

// saveSettings writes what changed of the settings into settings.json,
// atomically. Tests, which have no window, never write.
func (a *appState) saveSettings() {
	if a.win == nil {
		return
	}
	a.settings.Theme = a.themeMode
	a.settings.Terminal.FontSize = a.fontSize
	a.settings.Editor.DiffSplit = a.diffSplit
	data, err := mergeSettings(settingsFilePath(), settingsBase, a.settings)
	if err != nil {
		// Unreadable now, as mid-edit: written over, the edit was lost.
		if !a.settingsSaveFailed {
			a.toast(nil, "Your settings file can't be read, so changes aren't saved: "+err.Error())
		}
		a.settingsSaveFailed = true
		return
	}
	a.settingsSaveFailed = false
	path := settingsFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	if os.Rename(tmp, path) == nil {
		settingsBase = copySettings(a.settings)
	}
}

// mergeSettings is settings.json as it is on disk, with what changed from
// base to now written into it. A file that can't be read is an error,
// unless it is the broken one a copy was kept of at launch.
func mergeSettings(path string, base, now Settings) ([]byte, error) {
	disk := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &disk); err != nil {
			if settingsBroken == "" {
				return nil, err
			}
			disk = map[string]any{} // the broken one, copied aside
		}
	}
	mergeChanged(disk, settingsMap(base), settingsMap(now))
	return json.MarshalIndent(disk, "", "  ")
}

// settingsMap is s as JSON objects.
func settingsMap(s Settings) map[string]any {
	var m map[string]any
	data, _ := json.Marshal(s)
	json.Unmarshal(data, &m)
	return m
}

// mergeChanged writes into disk each value of now that differs from base,
// key by key down nested objects.
func mergeChanged(disk, base, now map[string]any) {
	for k, nv := range now {
		bv := base[k]
		if reflect.DeepEqual(bv, nv) {
			continue
		}
		nm, ok1 := nv.(map[string]any)
		bm, ok2 := bv.(map[string]any)
		dm, ok3 := disk[k].(map[string]any)
		if ok1 && ok2 && ok3 {
			mergeChanged(dm, bm, nm)
			continue
		}
		disk[k] = nv
	}
}

// settingDef is a setting the Settings window and the command palette
// both show: a switch when it has On, else a row whose page draws its own
// control. Search finds it by its title, its explanation and Keywords.
type settingDef struct {
	ID, Page, Title, Sub, Keywords string
	On                             func(*Settings) bool
	Set                            func(*Settings, bool)
	// Restart says the setting takes effect when omaterm starts again.
	Restart bool
	// PaletteTitle lists the switch in the command palette too, by this
	// name: the palette's titles are in title case, the window's not.
	PaletteTitle string
	// MacOnly shows the setting on macOS alone, where its key is.
	MacOnly bool
	// LinuxOnly shows the setting on Linux alone, where its service is.
	LinuxOnly bool
	// NotWindows hides the setting on Windows, which doesn't run the
	// session daemon the setting is for.
	NotWindows bool
}

// notOnWindows is why a feature that needs the session daemon is left out
// of Windows, where it doesn't run yet.
const notOnWindows = "Not available on Windows yet"

// shown tells whether the def is a setting of this system.
func (d settingDef) shown() bool {
	switch {
	case d.MacOnly:
		return onMac
	case d.LinuxOnly:
		return runtime.GOOS == "linux"
	case d.NotWindows:
		return !onWindows
	}
	return true
}

// boolDef is a switch over one field.
func boolDef(id, page, title, sub string, field func(*Settings) *bool) settingDef {
	return settingDef{ID: id, Page: page, Title: title, Sub: sub,
		On:  func(s *Settings) bool { return *field(s) },
		Set: func(s *Settings, on bool) { *field(s) = on }}
}

// settingDefs are the settings, in the order their pages show them.
var settingDefs = func() []settingDef {
	sw := func(d settingDef, palette string, restart bool, keywords string) settingDef {
		d.PaletteTitle, d.Restart, d.Keywords = palette, restart, keywords
		return d
	}
	row := func(id, page, title, sub, keywords string) settingDef {
		return settingDef{ID: id, Page: page, Title: title, Sub: sub, Keywords: keywords}
	}
	macOnly := func(d settingDef) settingDef {
		d.MacOnly = true
		return d
	}
	linuxOnly := func(d settingDef) settingDef {
		d.LinuxOnly = true
		return d
	}
	notWindows := func(d settingDef) settingDef {
		d.NotWindows = true
		return d
	}
	return []settingDef{
		// General
		notWindows(sw(boolDef("daemon", "general", "Keep sessions running when the window closes", "Shells and agents run in the session daemon.",
			func(s *Settings) *bool { return &s.Daemon }), "Keep Sessions Running When the Window Closes", true, "daemon background")),
		linuxOnly(row("loginStart", "general", "Start the session daemon at login", "A systemd user service, so sessions are back before you open omaterm.", "systemd service boot")),
		sw(boolDef("restore", "general", "Restore sessions at launch", "",
			func(s *Settings) *bool { return &s.RestoreSessions }), "Restore Sessions at Launch", false, "reopen"),
		sw(boolDef("resume", "general", "Resume agents in their own conversation", "Needs the agent integration (Agents page).",
			func(s *Settings) *bool { return &s.ResumeAgents }), "Resume Agents in Their Own Conversation", false, "claude codex continue"),
		sw(boolDef("snapshot", "general", "Snapshot each agent turn", "What “Last turn” in the Git tab compares against.",
			func(s *Settings) *bool { return &s.GitStatus }), "Snapshot Each Agent Turn", false, "git last turn changes"),
		sw(boolDef("confirmClose", "general", "Ask before closing a session that is still running", "An agent, a dev server or any program other than the shell.",
			func(s *Settings) *bool { return &s.ConfirmClose }), "Ask Before Closing a Running Session", false, "quit warn"),
		row("openLinks", "general", "Open links from terminals in", "", "url browser click"),
		row("cliTool", "general", "Command-line tool", "", "omaterm command install path"),
		row("scrollback", "general", "Saved scrollback", "", "history clear restore"),

		// Appearance
		row("theme", "appearance", "Theme", "", "omarchy dark light system color"),
		row("density", "appearance", "Density", "", "compact comfortable"),
		row("background", "appearance", "Background", "Wash is the soft gradient behind the cards.", "wash plain gradient"),
		sw(boolDef("dim", "appearance", "Dim unfocused panes", "",
			func(s *Settings) *bool { return &s.DimUnfocused }), "Dim Unfocused Panes", false, ""),
		sw(boolDef("focusmouse", "appearance", "Focus follows mouse", "The pane under the pointer takes the keyboard.",
			func(s *Settings) *bool { return &s.FocusFollowsMouse }), "Focus Follows Mouse", false, "hover"),
		sw(boolDef("motion", "appearance", "Reduce motion", "No springs, fades or pulsing status dots.",
			func(s *Settings) *bool { return &s.ReduceMotion }), "Reduce Motion", false, "animation"),
		sw(boolDef("load", "appearance", "Show CPU and memory in the status bar", "",
			func(s *Settings) *bool { return &s.ShowSystemLoad }), "Show CPU and Memory in the Status Bar", false, "system load stats"),

		// Terminal
		row("font", "terminal", "Font", "", "family typeface"),
		row("fontSize", "terminal", "Size", keyLabels("app.zoomIn")+" and "+keyLabels("app.zoomOut")+" change it too.", "font zoom"),
		row("lineHeight", "terminal", "Line height", "", "spacing"),
		sw(boolDef("ligatures", "terminal", "Ligatures", "Joins characters such as -> and != in fonts that have them.",
			func(s *Settings) *bool { return &s.Terminal.Ligatures }), "", false, "font"),
		row("contrast", "terminal", "Minimum contrast", "Lifts text a program colors too faintly. Auto is 1 in dark themes, 3 in light.", "readability"),
		row("cursor", "terminal", "Cursor style", "", "block bar underline"),
		sw(boolDef("blink", "terminal", "Cursor blink", "",
			func(s *Settings) *bool { return &s.Terminal.CursorBlink }), "", false, "cursor"),
		sw(boolDef("copySelect", "terminal", "Copy on select", "",
			func(s *Settings) *bool { return &s.Terminal.CopyOnSelect }), "Copy on Select", false, "clipboard selection"),
		row("rightClick", "terminal", "Right click", "", "mouse paste menu"),
		row("clipboardRead", "terminal", "Programs may read the clipboard", "Through OSC 52, as tmux and Neovim do.", "osc 52 paste"),
		row("shell", "terminal", "Shell", "", "zsh bash fish"),
		row("scrollbackSize", "terminal", "Scrollback", "", "history lines buffer"),
		row("bell", "terminal", "Bell", "", "beep sound"),
		macOnly(sw(boolDef("optionMeta", "terminal", "Use Option as Meta", "⌥B, ⌥F and ⌥D move and delete by word, instead of typing ∫ ƒ ∂.",
			func(s *Settings) *bool { return &s.Terminal.OptionAsMeta }), "Use Option as Meta", false, "alt key esc word")),

		// Editor
		sw(boolDef("editorFont", "editor", "Use the terminal font", "",
			func(s *Settings) *bool { return &s.Editor.TerminalFont }), "", false, "font family"),
		row("tabSize", "editor", "Tab size", "", "indent spaces"),
		sw(boolDef("wrap", "editor", "Wrap long lines", "",
			func(s *Settings) *bool { return &s.Editor.Wrap }), "Wrap Long Lines in the Editor", false, "soft wrap"),
		sw(boolDef("lineNumbers", "editor", "Line numbers", "",
			func(s *Settings) *bool { return &s.Editor.LineNumbers }), "", false, "gutter"),
		sw(boolDef("markdown", "editor", "Open Markdown as a preview", "",
			func(s *Settings) *bool { return &s.Editor.MarkdownPreview }), "", false, "md readme"),
		row("diffLayout", "editor", "Diff layout", "", "split unified side by side"),

		// Notifications
		sw(boolDef("notify-waiting", "notifications", "Needs you", "An agent asks for permission or input.",
			func(s *Settings) *bool { return &s.Notify.Waiting }), "Notify When a Session Needs You", false, "notify waiting blocked"),
		sw(boolDef("notify-done", "notifications", "Finishes", "",
			func(s *Settings) *bool { return &s.Notify.Done }), "Notify When a Session Finishes", false, "notify done"),
		sw(boolDef("notify-error", "notifications", "Fails", "An agent reports a failure, or the program a session started exits with an error.",
			func(s *Settings) *bool { return &s.Notify.Error }), "Notify When a Session Fails", false, "notify error"),
		sw(boolDef("notify-sound", "notifications", "Play a sound", "The desktop’s notification sound.",
			func(s *Settings) *bool { return &s.Notify.Sound }), "", false, "silent"),
		sw(boolDef("notify-programs", "notifications", "Show notifications from programs", "Programs that send their own (OSC 9 and 777).",
			func(s *Settings) *bool { return &s.Notify.Programs }), "", false, "osc"),
		row("notify-test", "notifications", "Send test notification", "", "try"),

		// Agents
		row("integration", "agents", "Agent integration", "", "hook skill claude codex pi opencode antigravity"),
		sw(boolDef("agentcontrol", "agents", "Let agents control the app", "The omaterm command may run the app’s commands and drive browser panes, not only sessions.",
			func(s *Settings) *bool { return &s.RemoteControl }), "Let Agents Control the App", false, "remote control automation"),
		sw(boolDef("socket", "agents", "Allow the omaterm command", "Turning this off leaves agents without omaterm.",
			func(s *Settings) *bool { return &s.ControlSocket }), "Allow the omaterm Command", true, "control socket cli"),

		// Web Access (web.json, drawn by its page)
		notWindows(row("web", "web", "Web access", "", "phone remote browser")),
		notWindows(row("webNetwork", "web", "On your network", "", "lan 0.0.0.0")),
		notWindows(row("devices", "web", "Paired devices", "", "revoke phone")),
		notWindows(row("pair", "web", "Pair a device", "", "qr code")),
		notWindows(row("webPort", "web", "Port", "", "7370")),
		notWindows(row("webURL", "web", "Public URL", "", "tailscale tunnel")),

		// Dev URLs (the proxy's, drawn by its page)
		notWindows(row("devurlSetup", "devurls", "Dev URLs setup", "", "https proxy localhost certificate ssl tls portless routes trust")),

		// Browser
		row("searchEngine", "browser", "Search with", "", "google duckduckgo kagi brave"),
		row("clearData", "browser", "Browsing data", "", "cookies cache clear"),

		// Keyboard and About
		row("shortcuts", "keyboard", "Keyboard shortcuts", "", "keys bindings"),
		row("updates", "about", "Check for updates", "", "version update"),
		row("files", "about", "Files", "", "settings.json state config"),
	}
}()

// settingDefByID finds a def; ok is false for an unknown ID.
func settingDefByID(id string) (settingDef, bool) {
	for _, d := range settingDefs {
		if d.ID == id {
			return d, true
		}
	}
	return settingDef{}, false
}
