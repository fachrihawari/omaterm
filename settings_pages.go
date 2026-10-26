package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/ui"

	"omaterm/internal/agent"
	"omaterm/internal/cli"
	"omaterm/internal/crash"
	"omaterm/internal/daemon"
	"omaterm/internal/paths"
	"omaterm/internal/proto"
	"omaterm/internal/session"
	"omaterm/internal/theme"
	"omaterm/internal/web"
	"omaterm/service"
)

// settingsPages are the Settings window's pages, in the sidebar's order.
// init sets them: their views refer back to them.
var settingsPages []settingsPageDef

func init() { settingsPages = settingsPageList() }

func settingsPageList() []settingsPageDef {
	return []settingsPageDef{
		{ID: "general", Title: "General", Icon: iconSettings, Group: 0, Body: (*appState).settingsGeneral,
			Restore: func(s *Settings, d Settings) {
				s.Daemon, s.RestoreSessions, s.ResumeAgents = d.Daemon, d.RestoreSessions, d.ResumeAgents
				s.GitStatus, s.ConfirmClose, s.OpenLinks = d.GitStatus, d.ConfirmClose, d.OpenLinks
			}},
		{ID: "appearance", Title: "Appearance", Icon: iconPalette, Group: 0, Body: (*appState).settingsAppearance,
			Restore: func(s *Settings, d Settings) {
				s.Theme, s.Density, s.Background = d.Theme, d.Density, d.Background
				s.DimUnfocused, s.FocusFollowsMouse, s.ReduceMotion, s.ShowSystemLoad = d.DimUnfocused, d.FocusFollowsMouse, d.ReduceMotion, d.ShowSystemLoad
			}},
		{ID: "terminal", Title: "Terminal", Icon: iconTerminal, Group: 0, Body: (*appState).settingsTerminal,
			Restore: func(s *Settings, d Settings) { s.Terminal, s.MinimumContrast = d.Terminal, d.MinimumContrast }},
		{ID: "editor", Title: "Editor", Icon: iconCode, Group: 0, Body: (*appState).settingsEditor,
			Restore: func(s *Settings, d Settings) { s.Editor = d.Editor }},
		{ID: "notifications", Title: "Notifications", Icon: iconBell, Group: 0, Body: (*appState).settingsNotifications,
			Restore: func(s *Settings, d Settings) { s.Notify = d.Notify }},

		{ID: "agents", Title: "Agents", Icon: iconBot, Group: 1, Body: (*appState).settingsAgents,
			Hero:     "omaterm shows what each agent is doing and resumes its conversation after a restart. A small hook tells it, and a skill teaches the agent the omaterm command.",
			HeroLink: "https://fachrihawari.github.io/omaterm/#cli"},
		{ID: "agent", Parent: "agents", Title: "Agent", Icon: iconBot, Body: (*appState).settingsAgent},
		{ID: "web", Title: "Web Access", Icon: iconSmartphone, Group: 1, Body: (*appState).settingsWeb,
			Hero:     "Use your sessions from a phone or another computer, in a browser. Only devices you pair can open it.",
			HeroLink: "https://fachrihawari.github.io/omaterm/#web"},
		{ID: "pair", Parent: "web", Title: "Pair a Device", Icon: iconSmartphone, Body: (*appState).settingsPair},
		{ID: "browser", Title: "Browser", Icon: iconGlobe, Group: 1, Body: (*appState).settingsBrowser,
			Restore: func(s *Settings, d Settings) { s.SearchEngine = d.SearchEngine }},
		{ID: "keyboard", Title: "Keyboard", Icon: iconKeyboard, Group: 1, Body: (*appState).settingsKeyboard},

		{ID: "about", Title: "About", Icon: iconInfo, Group: 2, Body: (*appState).settingsAbout},
	}
}

// settingsData is what the pages show of the world outside the settings,
// read again every few seconds while the window is open.
type settingsData struct {
	cliWhere     string
	cliOn        bool
	service      bool
	scrollCount  int
	scrollBytes  int64
	agents       map[string]cli.AgentIntegration
	devices      []web.Device
	web          web.Config
	webLink      string
	webLinkFor   web.Config
	webLinkAsked bool
	pair         *webPairing
	// busy names a change running in the background, as installing the
	// service, so its control waits.
	busy string
	// port and url are the Advanced fields of Web Access, as typed.
	port, url string
	// customFont and customSearch are fields as typed.
	customFont, customSearch string
	// omarchy is Omarchy's theme, for its tile.
	omarchy *theme.Source
}

// refreshSettingsData reads what the pages show, at most every two
// seconds.
func (a *appState) refreshSettingsData(c *ui.Context) {
	u := &a.settingsUI
	if !u.dataAt.IsZero() && c.Now().Sub(u.dataAt) < 2*time.Second {
		return
	}
	u.dataAt = c.Now()
	d := &u.data
	d.cliWhere, d.cliOn = cli.CLIStatus()
	d.omarchy = nil
	if src, err := theme.LoadOmarchy(); err == nil {
		d.omarchy = &src
	}
	d.service = daemon.ServiceInstalled()
	d.scrollCount, d.scrollBytes = savedScrollback(a.livePanes())
	d.agents = map[string]cli.AgentIntegration{}
	for _, name := range cli.HookAgents {
		d.agents[name] = cli.Integration(name)
	}
	store := webStore()
	d.devices, _ = store.Devices()
	if cfg, err := store.Config(); err == nil {
		if d.web != cfg || d.port == "" {
			d.port = strconv.Itoa(cfg.ListenPort())
			d.url = cfg.URL
		}
		d.web = cfg
	}
	if !d.webLinkAsked || d.webLinkFor != d.web {
		d.webLinkAsked, d.webLinkFor = true, d.web
		cfg := d.web
		go func() {
			link := web.BaseURL(cfg)
			a.post(func() {
				if a.settingsUI.data.webLinkFor == cfg {
					a.settingsUI.data.webLink = link
				}
				a.invalidateWindows()
			})
		}()
	}
	// The window wakes to read them again.
	c.After(2 * time.Second)
}

// livePanes are the panes running, whose saved scrollback is in use.
func (a *appState) livePanes() map[string]bool {
	live := map[string]bool{}
	a.panes.Each(func(h session.Handle) { live[h.ID()] = true })
	for id := range a.restoring {
		live[id] = true
	}
	return live
}

// savedScrollback counts the scrollback saved for restoring panes, and
// its size.
func savedScrollback(_ map[string]bool) (n int, size int64) {
	files, _ := filepath.Glob(filepath.Join(paths.StateDir(), "sessions", "*.vt"))
	for _, f := range files {
		if st, err := os.Stat(f); err == nil {
			n++
			size += st.Size()
		}
	}
	return n, size
}

// clearSavedScrollback removes the scrollback saved for panes that are
// not running; running ones keep theirs, which they write again.
func clearSavedScrollback(live map[string]bool) {
	files, _ := filepath.Glob(filepath.Join(paths.StateDir(), "sessions", "*.vt"))
	for _, f := range files {
		if !live[strings.TrimSuffix(filepath.Base(f), ".vt")] {
			os.Remove(f)
		}
	}
}

// openSettingsFile opens settings.json with the system's editor for it.
func (a *appState) openSettingsFile() {
	path := settingsFilePath()
	if _, err := os.Stat(path); err != nil {
		a.saveSettings()
	}
	if a.win != nil {
		mygo.Shell.OpenPath(path)
	}
}

// --- General ---

func (a *appState) settingsGeneral(c *ui.Context) {
	d := &a.settingsUI.data
	a.settingsGroup(c, "daemon", "", "Changing the daemon applies after you restart omaterm.", func() {
		a.settingSwitchRow(c, "daemon", runtime.GOOS == "windows")
		if runtime.GOOS == "linux" {
			on := d.service
			a.settingDefRow(c, "loginStart", func() {
				if settingsSwitch(c, "loginStart", &on, d.busy == "service") {
					a.setService(on)
				}
			})
		}
	})
	a.settingsGroup(c, "sessions", "Sessions", "", func() {
		a.settingSwitchRow(c, "restore", false)
		a.settingSwitchRow(c, "resume", !a.settings.RestoreSessions)
		a.settingSwitchRow(c, "snapshot", false)
		a.settingSwitchRow(c, "confirmClose", false)
	})
	a.settingsGroup(c, "links", "Links and tools", "", func() {
		a.settingDefRow(c, "openLinks", func() {
			if settingsPopup(c, "openLinks", &a.settings.OpenLinks, [][2]string{{"pane", "A browser pane"}, {"system", "Your browser"}}) {
				a.applySettings()
			}
		})
		a.settingsRow(c, "Command-line tool", settingsRowOpts{id: "cliTool", sub: d.cliWhere, mono: true}, func() {
			if d.cliOn {
				settingsPill(c, "Installed", pillOk)
				if settingsButton(c, "Remove", false, false, false) {
					if err := cli.UninstallCLI(); err != nil {
						toast(c, "Could not remove: "+err.Error())
					}
					a.settingsUI.dataAt = time.Time{}
				}
			} else if settingsButton(c, "Install", true, false, false) {
				if where, err := cli.InstallCLI(); err != nil {
					toast(c, "Could not install: "+err.Error())
				} else {
					toast(c, "Installed "+where)
				}
				a.settingsUI.dataAt = time.Time{}
			}
		})
		sub := "None saved"
		if d.scrollCount > 0 {
			sub = plural(d.scrollCount, "session") + ", " + service.FormatBytes(uint64(d.scrollBytes))
		}
		a.settingsRow(c, "Saved scrollback", settingsRowOpts{id: "scrollback", sub: sub}, func() {
			if settingsButton(c, "Clear…", false, false, d.scrollCount == 0) {
				a.settingsUI.sheet = &settingsSheet{
					title:   "Clear saved scrollback?",
					text:    "Sessions restored later start with an empty screen. Running sessions keep what they show.",
					confirm: "Clear",
					run: func(c *ui.Context) {
						clearSavedScrollback(a.livePanes())
						a.settingsUI.dataAt = time.Time{}
					},
				}
			}
		})
	})
}

// setService installs or removes the session daemon's systemd service,
// in the background: it restarts the daemon.
func (a *appState) setService(on bool) {
	d := &a.settingsUI.data
	d.busy = "service"
	go func() {
		var err error
		if on {
			err = cli.InstallService()
		} else {
			err = cli.UninstallService()
		}
		a.post(func() {
			d.busy = ""
			a.settingsUI.dataAt = time.Time{}
			switch {
			case err != nil:
				a.queuedToasts = append(a.queuedToasts, "Could not change the service: "+err.Error())
			case on:
				a.queuedToasts = append(a.queuedToasts, "The session daemon now starts at login")
			}
			a.invalidateWindows()
		})
	}()
}

// --- Appearance ---

func (a *appState) settingsAppearance(c *ui.Context) {
	a.settingsGroup(c, "theme", "Theme", "Omarchy follows your Omarchy theme as it changes. System follows light or dark mode.", func() {
		a.settingsUI.rowN++
		ui.Row(c.Key("tiles")).FillWidth().Gap(12).Padding(12).Children(func() {
			if hasOmarchy() {
				a.themeTile(c, themeOmarchy, "Omarchy")
			}
			a.themeTile(c, themeSystem, "System")
			a.themeTile(c, themeDark, "Dark")
			a.themeTile(c, themeLight, "Light")
		})
	})
	a.settingsGroup(c, "window", "Window", "", func() {
		a.settingDefRow(c, "density", func() {
			if settingsSegmented(c, "density", &a.settings.Density, []string{"Comfortable", "Compact"}) {
				a.applySettings()
			}
		})
		a.settingDefRow(c, "background", func() {
			if settingsSegmented(c, "background", &a.settings.Background, []string{"Wash", "Plain"}) {
				a.applySettings()
			}
		})
		a.settingSwitchRow(c, "dim", false)
		a.settingSwitchRow(c, "focusmouse", false)
		a.settingSwitchRow(c, "motion", false)
		a.settingSwitchRow(c, "load", false)
	})
}

// themeTile is a theme to choose, as a small picture of a window in it.
func (a *appState) themeTile(c *ui.Context, mode, name string) {
	on := a.themeMode == mode
	t := ui.ButtonBase(c.Key("tile-" + mode)).Column().Grow(1).Basis(0).MinWidth(0).Gap(6).AlignItems(ui.Center).Label(name)
	t.Children(func() {
		pv := ui.Row(c).Height(64).FillWidth().Radius(8).Border(1, pal.BorderStrong).Clip()
		if on {
			pv.Border(2, pal.Accent)
		}
		dark := func(e ui.Element) { e.Background(ui.Color{R: 0x12, G: 0x13, B: 0x1b, A: 255}) }
		pv.Children(func() {
			side, card := ui.Color{R: 0x12, G: 0x13, B: 0x1b, A: 255}, ui.Color{R: 0x23, G: 0x25, B: 0x32, A: 255}
			switch mode {
			case themeLight:
				side, card = ui.Color{R: 0xe9, G: 0xe9, B: 0xea, A: 255}, ui.Color{R: 255, G: 255, B: 255, A: 255}
			case themeOmarchy:
				side, card = ui.Color{R: 0x18, G: 0x18, B: 0x25, A: 255}, ui.Color{R: 0x45, G: 0x47, B: 0x5a, A: 255}
				if src := a.settingsUI.data.omarchy; src != nil {
					side, card = src.Background.Mix(ui.Color{A: 255}, 0.3), src.Background.Mix(src.Foreground, 0.12)
				}
			}
			if mode == themeSystem {
				ui.Row(c).Grow(1).FillHeight().Background(ui.Color{R: 0xe9, G: 0xe9, B: 0xea, A: 255}).Children(func() {
					ui.Box(c).Grow(1).Margin(6).Radius(4).Background(ui.Color{R: 255, G: 255, B: 255, A: 255})
				})
				ui.Row(c).Grow(1).FillHeight().Children(func() {
					dark(ui.Box(c).Grow(1).FillHeight().Children(func() {
						ui.Box(c).Fill().Margin(6).Radius(4).Background(card)
					}))
				})
				return
			}
			ui.Box(c).WidthPercent(30).FillHeight().Background(side)
			ui.Box(c).Grow(1).Margin(6).Radius(4).Background(card)
		})
		col, w := pal.TextMuted, 400
		if on {
			col, w = pal.Text, 500
		}
		ui.Text(c, name).FontSize(12).FontWeight(w).TextColor(col)
	})
	if t.Clicked() && !on {
		a.setThemeMode(mode)
		a.settings.Theme = mode
	}
}

// --- Terminal ---

func (a *appState) settingsTerminal(c *ui.Context) {
	t := &a.settings.Terminal
	d := &a.settingsUI.data
	a.settingsGroup(c, "preview", "", "", func() {
		a.settingsUI.rowN++
		a.terminalPreview(c)
	})
	a.settingsGroup(c, "text", "Text", "", func() {
		mode := "follow"
		if t.FontFamily != "" || d.customFont != "" {
			mode = "custom"
		}
		follow := strings.TrimSpace(strings.Split(termFontFamily(), ",")[0])
		a.settingDefRow(c, "font", func() {
			if settingsPopup(c, "font", &mode, [][2]string{{"follow", "Follow Omarchy (" + follow + ")"}, {"custom", "Custom…"}}) {
				if mode == "follow" {
					t.FontFamily, d.customFont = "", ""
					a.applySettings()
				} else {
					d.customFont = follow
				}
			}
		})
		if mode == "custom" {
			if d.customFont == "" {
				d.customFont = t.FontFamily
			}
			a.settingsRow(c, "Font family", settingsRowOpts{id: "fontFamily"}, func() {
				if a.settingsTextField(c, "fontFamily", &d.customFont, "JetBrains Mono", 200, true) {
					t.FontFamily = strings.TrimSpace(d.customFont)
					a.applySettings()
				}
			})
		}
		size := float64(a.fontSize)
		if size == 0 {
			size = defaultFontSize
		}
		a.settingDefRow(c, "fontSize", func() {
			if settingsSlider(c, "fontSize", &size, minFontSize, maxFontSize, 1, func(v float64) string { return fmt.Sprintf("%.0f pt", v) }) {
				a.fontSize = float32(size)
				a.applySettings()
			}
		})
		lh := float64(t.LineHeight)
		if lh == 0 {
			lh = 1
		}
		a.settingDefRow(c, "lineHeight", func() {
			if settingsSlider(c, "lineHeight", &lh, 1, 1.6, 0.05, func(v float64) string { return fmt.Sprintf("%.2f", v) }) {
				t.LineHeight = float32(lh)
				a.applySettings()
			}
		})
		a.settingSwitchRow(c, "ligatures", false)
		mc := float64(a.settings.MinimumContrast)
		a.settingDefRow(c, "contrast", func() {
			if settingsSlider(c, "contrast", &mc, 0, 7, 0.5, func(v float64) string {
				if v == 0 {
					return "Auto"
				}
				return fmt.Sprintf("%.1f:1", v)
			}) {
				a.settings.MinimumContrast = float32(mc)
				a.applySettings()
			}
		})
	})
	a.settingsGroup(c, "cursor", "Cursor", "", func() {
		a.settingDefRow(c, "cursor", func() {
			if settingsSegmented(c, "cursor", &t.Cursor, []string{"Block", "Bar", "Underline"}) {
				a.applySettings()
			}
		})
		a.settingSwitchRow(c, "blink", false)
	})
	a.settingsGroup(c, "mouse", "Mouse and clipboard", "", func() {
		a.settingSwitchRow(c, "copySelect", false)
		a.settingDefRow(c, "rightClick", func() {
			if settingsPopup(c, "rightClick", &t.RightClick, [][2]string{{"menu", "Shows a menu"}, {"paste", "Pastes"}}) {
				a.applySettings()
			}
		})
		a.settingDefRow(c, "clipboardRead", func() {
			if settingsPopup(c, "clipboardRead", &t.ClipboardRead, [][2]string{{"never", "Never"}, {"ask", "Ask each time"}}) {
				a.applySettings()
			}
		})
	})
	a.settingsGroup(c, "shell", "Shell and history", "Shell and scrollback apply to new sessions.", func() {
		login := "your login shell"
		if sh := os.Getenv("SHELL"); sh != "" {
			login += " (" + sh + ")"
		}
		a.settingsRow(c, "Shell", settingsRowOpts{id: "shell", sub: "Empty uses " + login + "."}, func() {
			if a.settingsTextField(c, "shell", &t.Shell, os.Getenv("SHELL"), 200, true) {
				t.Shell = strings.TrimSpace(t.Shell)
				a.applySettings()
			}
		})
		mb := strconv.Itoa(max(t.ScrollbackMB, 0))
		if mb == "0" {
			mb = "10"
		}
		a.settingsRow(c, "Scrollback", settingsRowOpts{id: "scrollbackSize", sub: "Per pane, kept by the session daemon."}, func() {
			if settingsPopup(c, "scrollbackSize", &mb, [][2]string{{"2", "2 MB"}, {"10", "10 MB"}, {"50", "50 MB"}, {"100", "100 MB"}}) {
				t.ScrollbackMB, _ = strconv.Atoi(mb)
				if t.ScrollbackMB == 10 {
					t.ScrollbackMB = 0
				}
				a.applySettings()
			}
		})
		a.settingDefRow(c, "bell", func() {
			if settingsPopup(c, "bell", &t.Bell, [][2]string{{"off", "Off"}, {"visual", "Flash the pane"}, {"notify", "Notify when you look away"}}) {
				a.applySettings()
			}
		})
	})
}

// terminalPreview shows a few lines as the terminals draw them: their
// font, size, line height and cursor, in their colors.
func (a *appState) terminalPreview(c *ui.Context) {
	t := a.settings.Terminal
	size := a.fontSize
	if size == 0 {
		size = defaultFontSize
	}
	lh := t.LineHeight
	if lh == 0 {
		lh = 1
	}
	font := a.termFont
	ansi := pal.ANSI
	prompt, ok := ansi[4], ansi[2]
	row := func(spans ...ui.Span) {
		ui.RichText(c, spans...).Font(font).FontSize(size).LineHeight(lh * 1.25).SingleLine()
	}
	text := pal.Text
	// Without ligatures, a zero-width non-joiner keeps the font from
	// joining the pairs, as the terminal's "-calt" does.
	code := "if err != nil { return } // a -> b"
	if !t.Ligatures {
		code = strings.NewReplacer("!=", "!\u200c=", "->", "-\u200c>").Replace(code)
	}
	ui.Column(c.Key("tprev")).Margin(12).Padding(10, 12).Radius(6).Background(pal.Card).Border(1, pal.Border).
		Clip().Children(func() {
		row(ui.Span{Text: "~/Projects/omaterm ", Color: prompt}, ui.Span{Text: "❯ ", Color: ok}, ui.Span{Text: "go test ./...", Color: text})
		row(ui.Span{Text: "ok", Color: ok}, ui.Span{Text: "  omaterm/internal/web  0.412s", Color: text})
		row(ui.Span{Text: code, Color: text})
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.RichText(c, ui.Span{Text: "~/Projects/omaterm ", Color: prompt}, ui.Span{Text: "❯", Color: ok}).
				Font(font).FontSize(size).LineHeight(lh * 1.25).Shrink(0)
			ui.Box(c).Width(size * 0.6).Shrink(0)
			w, h := size*0.6, size*1.2
			switch t.Cursor {
			case "bar":
				w = 2
			case "underline":
				h = 2
			}
			cur := ui.Box(c.Key("cursor")).Size(w, h).Background(text)
			if t.Cursor == "underline" {
				cur.AlignSelf(ui.End)
			}
			if t.CursorBlink && !a.settings.ReduceMotion {
				if c.Now().UnixMilli()/530%2 == 1 {
					cur.Opacity(0)
				}
				c.After(530 * time.Millisecond)
			}
		})
	})
}

// --- Editor ---

func (a *appState) settingsEditor(c *ui.Context) {
	e := &a.settings.Editor
	a.settingsGroup(c, "editor", "", "", func() {
		a.settingSwitchRow(c, "editorFont", false)
		tab := strconv.Itoa(e.TabSize)
		a.settingDefRow(c, "tabSize", func() {
			if settingsPopup(c, "tabSize", &tab, [][2]string{{"0", "From the language"}, {"2", "2"}, {"4", "4"}, {"8", "8"}}) {
				e.TabSize, _ = strconv.Atoi(tab)
				a.applySettings()
			}
		})
		a.settingSwitchRow(c, "wrap", false)
		a.settingSwitchRow(c, "lineNumbers", false)
		a.settingSwitchRow(c, "markdown", false)
	})
	a.settingsGroup(c, "diffs", "Diffs", "", func() {
		layout := "split"
		if !a.diffSplit {
			layout = "unified"
		}
		a.settingDefRow(c, "diffLayout", func() {
			if settingsSegmented(c, "diffLayout", &layout, []string{"Split", "Unified"}) {
				a.diffSplit = layout == "split"
				a.applySettings()
			}
		})
	})
}

// --- Notifications ---

func (a *appState) settingsNotifications(c *ui.Context) {
	a.settingsGroup(c, "when", "Notify me when a session", "Only while you look away: never for the session in front of you.", func() {
		a.settingSwitchRow(c, "notify-waiting", false)
		a.settingSwitchRow(c, "notify-done", false)
		a.settingSwitchRow(c, "notify-error", false)
	})
	a.settingsGroup(c, "how", "", "", func() {
		a.settingSwitchRow(c, "notify-sound", false)
		a.settingSwitchRow(c, "notify-programs", false)
		a.settingsRow(c, "Try it", settingsRowOpts{id: "notify-test"}, func() {
			if settingsButton(c, "Send Test Notification", false, false, false) {
				if a.win == nil || !mygo.NotificationsSupported() {
					toast(c, "This desktop shows no notifications")
				} else {
					a.notify("", "omaterm", "Notifications work. This is how a session that needs you tells you.")
				}
			}
		})
	})
}

// --- Agents ---

func agentTitle(name string) string { return agent.DisplayName(name) }

func (a *appState) settingsAgents(c *ui.Context) {
	d := &a.settingsUI.data
	var others []string
	for _, n := range agent.Names {
		if _, ok := d.agents[n]; !ok {
			others = append(others, agent.DisplayName(n))
		}
	}
	foot := "omaterm also recognizes " + strings.Join(others, ", ") + " by their process, without a hook."
	a.settingsGroup(c, "integration", "Integration", foot, func() {
		for _, name := range cli.HookAgents {
			it := d.agents[name]
			sub, on := "Not set up", false
			switch {
			case it.HookState == "current" && it.SkillState == "current":
				sub, on = "Hook and skill installed", true
			case it.HookState != "missing" || it.SkillState != "missing":
				sub = "Needs an update"
			case !it.Present:
				sub = "Not installed on this computer"
			}
			if a.settingsRow(c, agent.DisplayName(name), settingsRowOpts{id: "agent-" + name, sub: sub, link: "agent:" + name}, func() {
				if on {
					settingsPill(c, "On", pillOk)
				} else if sub == "Needs an update" {
					settingsPill(c, "Update", pillWarn)
				} else {
					settingsPill(c, "Off", pillPlain)
				}
			}) {
				a.settingsGo("agent:" + name)
			}
		}
	})
	a.settingsGroup(c, "control", "Control", "", func() {
		a.settingSwitchRow(c, "agentcontrol", false)
		a.settingSwitchRow(c, "socket", false)
	})
}

// settingsAgent is an agent's subpage: its hook and skill, set up or
// removed.
func (a *appState) settingsAgent(c *ui.Context) {
	_, name, _ := strings.Cut(a.settingsCurrent(), ":")
	it, ok := a.settingsUI.data.agents[name]
	if !ok {
		return
	}
	state := func(s string) {
		switch s {
		case "current":
			settingsPill(c, "Installed", pillOk)
		case "outdated":
			settingsPill(c, "Outdated", pillWarn)
		default:
			settingsPill(c, "Not installed", pillPlain)
		}
	}
	hook := "Status hook"
	if name == "pi" || name == "opencode" {
		hook = "Status plugin"
	}
	note := cli.HookNotes[name]
	if note == "" {
		note = "Sessions started before setup pick it up after they restart."
	}
	a.settingsGroup(c, "agent", "", note, func() {
		a.settingsRow(c, hook, settingsRowOpts{sub: cli.ShortPath(it.HookPath), mono: true}, func() { state(it.HookState) })
		a.settingsRow(c, "Skill", settingsRowOpts{sub: cli.ShortPath(it.SkillDir), mono: true}, func() { state(it.SkillState) })
	})
	settingsActions(c, func() {
		installed := it.HookState != "missing"
		if installed && settingsButton(c, "Remove Integration", false, true, false) {
			a.settingsUI.sheet = &settingsSheet{
				title:   "Remove the " + agent.DisplayName(name) + " integration?",
				text:    "omaterm stops learning this agent's conversation, so a restart can only continue the folder's last one.",
				confirm: "Remove",
				run: func(c *ui.Context) {
					if err := cli.RemoveAgent(name); err != nil {
						toast(c, "Could not remove: "+err.Error())
					}
					a.hookChecked = map[string]bool{}
					a.settingsUI.dataAt = time.Time{}
				},
			}
		}
		label := "Set Up"
		if installed && (it.HookState != "current" || it.SkillState != "current") {
			label = "Update"
		}
		if (!installed || label == "Update") && settingsButton(c, label, true, false, false) {
			if err := cli.SetupAgent(name); err != nil {
				toast(c, "Setup failed: "+err.Error())
			} else {
				msg := agent.DisplayName(name) + " is set up"
				if n := cli.HookNotes[name]; n != "" {
					msg += ". " + n
				}
				toast(c, msg)
			}
			a.hookChecked = map[string]bool{}
			a.settingsUI.dataAt = time.Time{}
		}
	})
}

// --- Web Access ---

func (a *appState) settingsWeb(c *ui.Context) {
	d := &a.settingsUI.data
	foot := "Reach it from elsewhere with Tailscale or a tunnel (omaterm web tunnel)."
	if a.webOn && a.webNetwork && d.web.URL == "" {
		foot = "Plain HTTP on your network: use it on networks you trust, or Tailscale or a tunnel from elsewhere."
	}
	a.settingsGroup(c, "web", "", foot, func() {
		on := a.webOn
		a.settingDefRow(c, "web", func() {
			if settingsSwitch(c, "web", &on, false) {
				a.setWeb(c, on)
				a.settingsUI.dataAt = time.Time{}
			}
		})
		net := a.webOn && a.webNetwork
		a.settingsRow(c, "On your network", settingsRowOpts{id: "webNetwork", sub: "Listens on every address, not only this computer’s."}, func() {
			if settingsSwitch(c, "webNetwork", &net, !a.webOn) {
				a.setWebNetwork(c, net)
				a.settingsUI.dataAt = time.Time{}
			}
		})
		a.settingsRow(c, "Link", settingsRowOpts{id: "webLink"}, func() {
			if !a.webOn {
				settingsPill(c, "Off", pillPlain)
				return
			}
			link := d.webLink
			if link == "" {
				link = "…"
			}
			ui.Text(c, link).Font(monoFont).FontSize(12).TextColor(pal.TextSoft).SingleLine().Ellipsis("…").MaxWidth(240)
			if settingsButton(c, "Copy", false, false, d.webLink == "") {
				mygo.Clipboard.WriteText(d.webLink)
				toast(c, "Copied the link")
			}
		})
	})
	a.settingsGroup(c, "devices", "Paired devices", "", func() {
		if len(d.devices) == 0 {
			a.settingsRow(c, "", settingsRowOpts{sub: "No devices yet"}, nil)
		}
		for _, dev := range d.devices {
			dev := dev
			seen := "never"
			if dev.LastSeen > 0 {
				seen = ago(time.Since(time.UnixMilli(dev.LastSeen))) + " ago"
			}
			until := time.UnixMilli(max(dev.LastSeen, dev.Created)).Add(web.DeviceTTL).Format("Jan 2")
			name := dev.Name
			if name == "" {
				name = "A browser"
			}
			a.settingsRow(c, name, settingsRowOpts{id: "dev-" + dev.ID, sub: "Last seen " + seen + " · signed in until " + until}, func() {
				if settingsButton(c, "Revoke##"+dev.ID, false, false, false) {
					a.revokeSheet(dev.ID, name)
				}
			})
		}
		if a.settingsRow(c, "Pair a Device…", settingsRowOpts{id: "pair", link: "pair"}, nil) {
			a.settingsUI.data.pair = nil
			a.settingsGo("pair")
		}
	})
	if len(d.devices) > 1 {
		settingsActions(c, func() {
			if settingsButton(c, "Revoke All…", false, true, false) {
				a.revokeSheet("all", "every device")
			}
		})
	}
	a.settingsGroup(c, "advanced", "Advanced", "", func() {
		a.settingDefRow(c, "webPort", func() {
			if a.settingsTextField(c, "webPort", &d.port, strconv.Itoa(web.DefaultPort), 80, true) {
				n, err := strconv.Atoi(strings.TrimSpace(d.port))
				if err != nil || n < 1 || n > 65535 {
					toast(c, "A port is a number from 1 to 65535")
					d.port = strconv.Itoa(d.web.ListenPort())
					return
				}
				if n != d.web.ListenPort() {
					if n == web.DefaultPort {
						n = 0
					}
					a.changeWeb(c, func(cfg *web.Config) { cfg.Port = n })
					a.settingsUI.dataAt = time.Time{}
				}
			}
		})
		a.settingsRow(c, "Public URL", settingsRowOpts{id: "webURL", sub: "A Tailscale or tunnel address, for the link and QR code."}, func() {
			if a.settingsTextField(c, "webURL", &d.url, "https://…", 200, true) {
				url := strings.TrimSpace(d.url)
				if url != d.web.URL {
					a.changeWeb(c, func(cfg *web.Config) { cfg.URL = url })
					a.settingsUI.dataAt = time.Time{}
				}
			}
		})
	})
}

// revokeSheet asks before signing a device, or every one, out.
func (a *appState) revokeSheet(id, name string) {
	text := "It is signed out at once, and any session it has open closes within a few seconds. Pair it again to use it."
	if id == "all" {
		text = "Every device is signed out at once, and the pairing codes waiting stop working."
	}
	a.settingsUI.sheet = &settingsSheet{
		title: "Revoke " + name + "?", text: text, confirm: "Revoke",
		run: func(c *ui.Context) {
			if _, err := webStore().Revoke(id); err != nil {
				toast(c, "Could not revoke: "+err.Error())
			}
			a.settingsUI.dataAt = time.Time{}
		},
	}
}

// settingsPair shows a pairing code: the QR code a phone's camera opens,
// the code to type, and the link.
func (a *appState) settingsPair(c *ui.Context) {
	d := &a.settingsUI.data
	if !a.webOn {
		a.settingsGroup(c, "pair-off", "", "", func() {
			a.settingsRow(c, "Web access is off", settingsRowOpts{sub: "Turn it on to pair a device."}, func() {
				if settingsButton(c, "Turn On", true, false, false) {
					a.setWeb(c, true)
				}
			})
		})
		return
	}
	if d.pair == nil || (d.pair.ready && d.pair.err == "" && time.Now().After(d.pair.expires)) {
		d.pair = a.startPairing()
	}
	p := d.pair
	foot := "Scan with the phone’s camera, or open the link and type the code."
	if p.ready && p.err == "" {
		foot += " It works once, until " + p.expires.Format("15:04") + "."
		if p.local {
			foot += " Only this computer can open it: turn on On your network, or use a tunnel."
		}
	}
	a.settingsGroup(c, "pair", "", foot, func() {
		a.settingsUI.rowN++
		ui.Column(c.Key("pair-body")).FillWidth().Padding(14).Gap(8).AlignItems(ui.Center).Children(func() {
			switch {
			case !p.ready:
				ui.Text(c, "Making a code…").FontSize(12).TextColor(pal.TextMuted).Padding(40, 0)
			case p.err != "":
				ui.Text(c, p.err).FontSize(12).TextColor(pal.Bad)
			default:
				qrView(c, p.qr, 4)
				ui.Text(c, p.code).FontSize(20).FontWeight(600).Font(monoFont).TextColor(pal.TextStrong)
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, p.link).Font(monoFont).FontSize(11.5).TextColor(pal.TextSoft).SingleLine().Ellipsis("…").MaxWidth(360)
					if settingsButton(c, "Copy", false, false, false) {
						mygo.Clipboard.WriteText(p.link)
						toast(c, "Copied the link")
					}
				})
			}
		})
	})
	settingsActions(c, func() {
		if settingsButton(c, "New Code", false, false, !p.ready) {
			d.pair = a.startPairing()
		}
	})
}

// --- Browser ---

func (a *appState) settingsBrowser(c *ui.Context) {
	d := &a.settingsUI.data
	engine := a.settings.SearchEngine
	if _, ok := proto.SearchEngines[engine]; !ok {
		if d.customSearch == "" {
			d.customSearch = engine
		}
		engine = "custom"
	}
	a.settingsGroup(c, "browser", "", "", func() {
		a.settingDefRow(c, "searchEngine", func() {
			if settingsPopup(c, "searchEngine", &engine, [][2]string{
				{"duckduckgo", "DuckDuckGo"}, {"google", "Google"}, {"kagi", "Kagi"}, {"brave", "Brave Search"}, {"custom", "Custom…"},
			}) {
				if engine == "custom" {
					if d.customSearch == "" {
						d.customSearch = "https://"
					}
					a.settings.SearchEngine = d.customSearch
				} else {
					a.settings.SearchEngine = engine
				}
				a.applySettings()
			}
		})
		if engine == "custom" {
			a.settingsRow(c, "Search URL", settingsRowOpts{id: "searchURL", sub: "%s is where the words go."}, func() {
				if a.settingsTextField(c, "searchURL", &d.customSearch, "https://example.com/?q=%s", 240, true) {
					a.settings.SearchEngine = strings.TrimSpace(d.customSearch)
					a.applySettings()
				}
			})
		}
		a.settingsRow(c, "Browsing data", settingsRowOpts{id: "clearData", sub: "Cookies, storage and cache of every browser pane."}, func() {
			if settingsButton(c, "Clear…", false, false, false) {
				a.settingsUI.sheet = &settingsSheet{
					title:   "Clear browsing data?",
					text:    "Signs you out of sites in browser panes and clears their cache.",
					confirm: "Clear",
					run: func(c *ui.Context) {
						if a.win == nil {
							return
						}
						if err := mygo.App.ClearBrowsingData(); err != nil {
							toast(c, "Could not clear: "+err.Error())
						} else {
							toast(c, "Cleared browsing data")
						}
					},
				}
			}
		})
	})
}

// --- Keyboard ---

// settingsKeyboard lists every shortcut, filtered and grouped as the
// Keyboard Shortcuts dialog does. It shows them here rather than opening
// that dialog: the main window is under the Settings window.
func (a *appState) settingsKeyboard(c *ui.Context) {
	u := &a.settingsUI
	ui.Row(c.Key("key-filter")).FillWidth().Height(30).Padding(0, 10).Gap(8).AlignItems(ui.Center).Radius(6).
		Border(1, pal.Border).Background(pal.Input).Children(func() {
		ui.Icon(c, iconSearch).Size(14, 14).TextColor(pal.TextFaint)
		ui.TextInputBase(c, &u.keyQuery).Placeholder("Filter by name or keys, as “split” or “ctrl+shift”").
			FontSize(12.5).Grow(1).MinWidth(0).Label("Filter shortcuts")
	})
	u.blocks++
	items := keymapFilter(u.keyQuery)
	if len(items) == 0 {
		ui.Text(c, "No shortcut matches.").FontSize(12).TextColor(pal.TextFaint).Margin(18, 2, 0, 2)
	}
	for start := 0; start < len(items); {
		cat := items[start].Category
		end := start
		for end < len(items) && items[end].Category == cat {
			end++
		}
		group := items[start:end]
		a.settingsGroup(c, "keys-"+cat, cat, "", func() {
			for _, e := range group {
				e := e
				a.settingsRow(c, e.Title, settingsRowOpts{id: "key-" + e.ID}, func() {
					for i, b := range e.Keys {
						if i > 1 {
							break
						}
						keyChip(c, b.Combo.Label())
					}
				})
			}
		})
		start = end
	}
	ui.Text(c, "Shortcuts can’t be changed yet.").FontSize(11.5).TextColor(pal.TextFaint).Margin(12, 2, 0, 2)
}

// --- About ---

func (a *appState) settingsAbout(c *ui.Context) {
	a.settingsGroup(c, "version", "", "", func() {
		sub := "This build cannot update itself"
		if updatesReady && mygo.Updater.Enabled() {
			sub = "Checks for updates daily"
			if t := updater.LastCheck(); !t.IsZero() {
				sub = "Last checked " + ago(time.Since(t)) + " ago"
			}
		}
		a.settingsRow(c, "omaterm "+appVersion(), settingsRowOpts{id: "updates", sub: sub}, func() {
			if settingsButton(c, "Check for Updates", false, false, !updatesReady) {
				a.checkForUpdates(c)
			}
		})
	})
	if updatesReady && mygo.Updater.Enabled() {
		a.settingsGroup(c, "auto", "", "", func() {
			check := updater.AutomaticChecks()
			a.settingsRow(c, "Check for updates automatically", settingsRowOpts{id: "autoCheck"}, func() {
				if settingsSwitch(c, "autoCheck", &check, false) {
					updater.SetAutomaticChecks(check)
				}
			})
			dl := updater.AutomaticDownloads()
			a.settingsRow(c, "Download updates automatically", settingsRowOpts{id: "autoDownload"}, func() {
				if settingsSwitch(c, "autoDownload", &dl, !check) {
					updater.SetAutomaticDownloads(dl)
				}
			})
		})
	}
	files := [][2]string{
		{"Settings", settingsFilePath()},
		{"Sessions and layout", stateFilePath()},
		{"Web access", web.StorePath(paths.ConfigDir())},
		{"Saved scrollback", filepath.Join(paths.StateDir(), "sessions")},
		{"Crash reports", crash.LogPath(paths.StateDir(), "app")},
		{"Session daemon's crash reports", crash.LogPath(paths.StateDir(), "daemon")},
	}
	if runtime.GOOS == "linux" {
		files = append(files, [2]string{"Session daemon service", daemon.UnitPath()})
	}
	a.settingsGroup(c, "files", "Files", "", func() {
		for _, f := range files {
			f := f
			a.settingsRow(c, f[0], settingsRowOpts{id: "file-" + f[0], sub: cli.ShortPath(f[1]), mono: true}, func() {
				_, err := os.Stat(f[1])
				if settingsButton(c, "Reveal##"+f[0], false, false, err != nil) && a.win != nil {
					mygo.Shell.ShowItemInFolder(f[1])
				}
			})
		}
	})
	a.settingsGroup(c, "links", "", "", func() {
		for _, l := range [][2]string{
			{"Website", "https://fachrihawari.github.io/omaterm/"},
			{"What’s New", "https://github.com/fachrihawari/omaterm/releases"},
			{"Report an Issue", "https://github.com/fachrihawari/omaterm/issues"},
			{"Credits", "https://fachrihawari.github.io/omaterm/#thanks"},
		} {
			if a.settingsRow(c, l[0], settingsRowOpts{id: "link-" + l[0], link: l[1]}, nil) {
				c.OpenURL(l[1])
			}
		}
	})
}
