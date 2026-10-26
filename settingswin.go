package main

import (
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"omaterm/internal/theme"
)

// The Settings window: a window of its own beside the main one, laid out
// as macOS's System Settings (a sidebar of pages with a search field, the
// page in a card, rows in groups, subpages a row opens, sheets to
// confirm) and drawn as the rest of omaterm (the wash, rows like the
// sidebar's sessions, the content on a raised card). The design is
// docs/design/settings-window.md, its picture docs/mockups/settings-window.html.

// settingsUI is the window's state, which outlives it, so that it opens
// again where it was left.
type settingsUI struct {
	// stack is the pages visited, pos the one shown, for back and forward.
	stack []string
	pos   int
	query string
	// flash is the setting a search led to, lit for a moment from
	// flashAt; scrolled says the page scrolled to it.
	flash    string
	flashAt  time.Time
	scrolled bool
	// rowN counts the rows of the group being built, for the lines between
	// them; blocks the groups of the page so far, for the room above one.
	rowN, blocks int
	// restart says a setting changed that applies when omaterm starts again.
	restart bool
	// sheet is the confirmation open over the page.
	sheet *settingsSheet
	// data is what the pages show that comes from outside the settings,
	// read again every few seconds while the window shows.
	data   settingsData
	dataAt time.Time
	// focused is which text fields had the keyboard last frame.
	focused map[string]bool
	// closing counts the closes asked and the openings since, so that an
	// opening calls off a close still to run.
	closing int
	// keyQuery filters the Keyboard page.
	keyQuery string
}

// settingsSheet is a confirmation: a title, what it explains, and its
// buttons; danger makes the main one red.
type settingsSheet struct {
	title, text string
	items       [][2]string // a list of what will happen: what, and a note
	confirm     string
	danger      bool
	run         func(c *ui.Context)
}

// settingsPageDef is a page: in the sidebar when it has no parent, else a
// subpage a row of its parent opens.
type settingsPageDef struct {
	ID, Title string
	Icon      *ui.SVG
	Parent    string
	// Group puts the page in the sidebar's group, separated by a gap.
	Group int
	// Hero introduces the page: its text, and where Learn more goes.
	Hero, HeroLink string
	Body           func(a *appState, c *ui.Context)
	// Restore resets the page's settings to their defaults.
	Restore func(s *Settings, d Settings)
}

const (
	settingsWidth, settingsHeight = 820, 640
	settingsSideW                 = 220
	settingsPageW                 = 560
)

// openSettings shows the Settings window, on page when it is not empty.
func (a *appState) openSettings(page string) {
	u := &a.settingsUI
	switch {
	case page != "" && settingsPageByID(page) != nil:
		a.settingsGo(page)
	case len(u.stack) == 0:
		start := a.settingsPage
		if settingsPageByID(start) == nil {
			start = "general"
		}
		u.stack, u.pos = []string{start}, 0
	}
	u.dataAt = time.Time{}
	if a.win == nil {
		return // tests: the view is built with a tester instead
	}
	if w := a.settingsWin; w != nil && !w.IsDestroyed() {
		a.settingsUI.closing++ // a close asked before this one is called off
		w.Show()
		w.Focus()
		w.Invalidate()
		return
	}
	a.settingsWin = mygo.NewWindow(mygo.WindowOptions{
		Title:           "omaterm Settings",
		Width:           settingsWidth,
		Height:          settingsHeight,
		MinWidth:        600,
		MinHeight:       420,
		Parent:          a.win,
		StateKey:        "settings",
		TitleBarStyle:   mygo.TitleBarHidden,
		BackgroundColor: theme.Hex(pal.Wash),
		Content:         ui.View(a.settingsView),
	})
	a.settingsWin.OnClosed(func() { a.settingsWin = nil })
}

// closeSettings closes the Settings window.
// It closes after the event that asked: destroyed inside its own key
// press or click, the window crashed GTK, which still had the event's
// hands on it.
func (a *appState) closeSettings() {
	w := a.settingsWin
	if w == nil || w.IsDestroyed() {
		return
	}
	// Opening it again before this runs keeps it open.
	a.settingsUI.closing++
	gen := a.settingsUI.closing
	w.Update(func() {
		if !w.IsDestroyed() && a.settingsUI.closing == gen {
			w.Close()
		}
	})
}

// invalidateWindows redraws both windows, after a change one of them made
// that the other shows.
func (a *appState) invalidateWindows() {
	if a.win != nil {
		a.win.Invalidate()
	}
	if w := a.settingsWin; w != nil && !w.IsDestroyed() {
		w.Invalidate()
	}
}

func settingsPageByID(id string) *settingsPageDef {
	base, _, _ := strings.Cut(id, ":")
	for i := range settingsPages {
		if settingsPages[i].ID == base {
			return &settingsPages[i]
		}
	}
	return nil
}

// settingsCurrent is the page shown.
func (a *appState) settingsCurrent() string {
	u := &a.settingsUI
	if len(u.stack) == 0 {
		return "general"
	}
	return u.stack[u.pos]
}

// settingsGo shows a page, as a step forward in the history.
func (a *appState) settingsGo(id string) {
	u := &a.settingsUI
	if len(u.stack) > 0 && u.stack[u.pos] == id {
		return
	}
	if len(u.stack) > 0 {
		u.stack = u.stack[:u.pos+1]
	}
	u.stack = append(u.stack, id)
	u.pos = len(u.stack) - 1
	if p := settingsPageByID(id); p != nil && p.Parent == "" {
		a.settingsPage = id
	}
	u.dataAt = time.Time{}
}

// settingsView is the window's view.
func (a *appState) settingsView(c *ui.Context) {
	a.drainPosted()
	c.SetTheme(omaTheme())
	u := &a.settingsUI
	if u.focused == nil {
		u.focused = map[string]bool{}
	}
	if len(u.stack) == 0 {
		u.stack = []string{"general"}
	}
	a.refreshSettingsData(c)
	a.settingsKeys(c)

	root := ui.Row(c).Fill().AlignItems(ui.Stretch)
	a.wash(root)
	root.Children(func() {
		if a.settings.Background != "plain" {
			ui.Box(c.Key("glow")).Absolute().Top(0).Left(0).Right(0).Bottom(0).PassThrough().
				LinearGradient(ui.LinearGradient{Angle: 225, Stops: []ui.GradientStop{
					{Color: pal.WashGlow, At: 0}, {Color: pal.WashGlow.Alpha(0), At: 0.55},
				}})
		}
		a.settingsSidebar(c)
		a.settingsContent(c)
	})
	a.settingsSheetView(c)
}

// settingsKeys are the window's keys: Escape backs out of a search, then
// closes the window, as Ctrl+W does; Ctrl+F goes to the search field.
func (a *appState) settingsKeys(c *ui.Context) {
	u := &a.settingsUI
	if c.Shortcut(0, ui.KeyEscape) {
		if u.query != "" {
			u.query = ""
		} else {
			a.closeSettings()
		}
	}
	if c.Shortcut(ui.Cmd, ui.KeyW) {
		a.closeSettings()
	}
	if c.Shortcut(ui.Cmd, ui.KeyF) {
		a.handle("settings-search").Focus(c)
	}
	if c.Shortcut(ui.Alt, ui.KeyLeft) {
		a.settingsBack()
	}
	if c.Shortcut(ui.Alt, ui.KeyRight) {
		a.settingsForward()
	}
}

func (a *appState) settingsBack() {
	if u := &a.settingsUI; u.pos > 0 {
		u.pos--
		u.dataAt = time.Time{}
	}
}

func (a *appState) settingsForward() {
	if u := &a.settingsUI; u.pos < len(u.stack)-1 {
		u.pos++
		u.dataAt = time.Time{}
	}
}

// --- Sidebar ---

func (a *appState) settingsSidebar(c *ui.Context) {
	u := &a.settingsUI
	bar := c.TitleBar()
	ui.Column(c.Key("settings-side")).Width(settingsSideW).Shrink(0).FillHeight().Padding(0, 6, 8, 8).Children(func() {
		// Room for the window's controls, which also moves the window.
		ui.Box(c).Height(max(bar.Height, 38)).Shrink(0).FillWidth().DragWindow()

		search := ui.Row(c.Key("search")).Height(30).Shrink(0).Margin(0, 2, 10, 2).Padding(0, 10).Gap(8).
			AlignItems(ui.Center).Radius(6).Border(1, pal.Border).Background(pal.Input)
		search.Children(func() {
			ui.Icon(c, iconSearch).Size(14, 14).TextColor(pal.TextFaint)
			in := ui.TextInputBase(c, &u.query).Placeholder("Search settings").FontSize(12.5).Grow(1).MinWidth(0).
				Label("Search settings").Bind(a.handle("settings-search"))
			if in.Submitted() {
				if hits := a.settingsNavItems(); len(hits) > 0 {
					a.settingsOpenHit(hits[0])
				}
			}
		})

		ui.Scroll(c.Key("nav")).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				items := a.settingsNavItems()
				if len(items) == 0 {
					ui.Text(c, "No settings match.").FontSize(12).TextColor(pal.TextFaint).Padding(6, 10)
				}
				group := -1
				for _, it := range items {
					if it.def == nil {
						if group >= 0 && it.page.Group != group {
							ui.Box(c).Height(10).Shrink(0)
						}
						group = it.page.Group
						a.settingsNavRow(c, it.page)
					} else {
						a.settingsNavHit(c, it)
					}
				}
			})
		})

		foot := ui.ButtonBase(c.Key("open-file")).Margin(8, 0, 0, 6).Gap(6).AlignItems(ui.Center).Shrink(0).AlignSelf(ui.Start)
		col := pal.TextMuted
		if foot.Hovered() {
			col = pal.Text
		}
		foot.Children(func() {
			ui.Icon(c, iconFileCog).Size(14, 14).TextColor(col)
			ui.Text(c, "Open settings file").FontSize(12).TextColor(col)
		})
		if foot.Clicked() {
			a.openSettingsFile()
		}
	})
}

// settingsNavItem is a row of the sidebar: a page, or, while searching,
// a setting of the page above it.
type settingsNavItem struct {
	page *settingsPageDef
	def  *settingDef
}

// settingsNavItems are the sidebar's rows: every page, or, while
// searching, the pages that match and their settings that do.
func (a *appState) settingsNavItems() []settingsNavItem {
	q := strings.ToLower(strings.TrimSpace(a.settingsUI.query))
	var out []settingsNavItem
	for i := range settingsPages {
		p := &settingsPages[i]
		if p.Parent != "" {
			continue
		}
		if q == "" {
			out = append(out, settingsNavItem{page: p})
			continue
		}
		var hits []settingsNavItem
		for j := range settingDefs {
			d := &settingDefs[j]
			if d.Page != p.ID {
				continue
			}
			if strings.Contains(strings.ToLower(d.Title+" "+d.Sub+" "+d.Keywords), q) {
				hits = append(hits, settingsNavItem{page: p, def: d})
			}
		}
		if len(hits) == 0 && !strings.Contains(strings.ToLower(p.Title), q) {
			continue
		}
		out = append(out, settingsNavItem{page: p})
		if len(hits) > 4 {
			hits = hits[:4]
		}
		out = append(out, hits...)
	}
	return out
}

// settingsOpenHit shows the page of a sidebar row, lighting the setting
// a search found.
func (a *appState) settingsOpenHit(it settingsNavItem) {
	a.settingsGo(it.page.ID)
	if it.def != nil {
		u := &a.settingsUI
		u.flash, u.flashAt, u.scrolled = it.def.ID, time.Now(), false
	}
}

// settingsNavRow is a page's row, drawn as the main sidebar's sessions.
func (a *appState) settingsNavRow(c *ui.Context, p *settingsPageDef) {
	cur := settingsPageByID(a.settingsCurrent())
	selected := a.settingsUI.query == "" && cur != nil && (cur.ID == p.ID || cur.Parent == p.ID)
	row := ui.ButtonBase(c.Key("nav-"+p.ID)).Height(32).Radius(8).Padding(0, 8).Gap(8).AlignItems(ui.Center).
		Margin(0, 0, 1, 0).FillWidth().Label(p.Title)
	bg := ui.Color{}
	switch {
	case selected:
		bg = pal.RowSelected
	case row.Hovered():
		bg = pal.Hover
	}
	row.Background(a.animColor(row, "bg", bg, 110*time.Millisecond))
	if selected {
		chosen(row)
	}
	row.Children(func() {
		col := pal.TextFaint
		if selected {
			col = pal.TextSoft
		}
		ui.Icon(c, p.Icon).Size(14, 14).TextColor(col)
		ui.Text(c, p.Title).FontSize(13).TextColor(pal.Text).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
	})
	if row.Clicked() {
		a.settingsUI.query = ""
		a.settingsGo(p.ID)
	}
}

// settingsNavHit is a setting a search found, under its page.
func (a *appState) settingsNavHit(c *ui.Context, it settingsNavItem) {
	row := ui.ButtonBase(c.Key("hit-"+it.def.ID)).Height(26).Radius(7).Padding(0, 8, 0, 30).AlignItems(ui.Center).
		FillWidth().Label(it.def.Title)
	col := pal.TextMuted
	if row.Hovered() {
		row.Background(pal.Hover)
		col = pal.Text
	}
	row.Children(func() {
		ui.Text(c, it.def.Title).FontSize(12).TextColor(col).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
	})
	if row.Clicked() {
		a.settingsOpenHit(it)
	}
}

// --- Content ---

func (a *appState) settingsContent(c *ui.Context) {
	id := a.settingsCurrent()
	p := settingsPageByID(id)
	if p == nil {
		return
	}
	card := ui.Column(c.Key("settings-card")).Grow(1).MinWidth(0).Margin(8, 8, 8, 2).Radius(10).Clip().
		Background(pal.Raised)
	raised(card)
	card.Children(func() {
		a.settingsToolbar(c, a.settingsTitle(id, p))
		ui.Scroll(c.Key("page-" + id)).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Column(c.Key("page")).FillWidth().MaxWidth(settingsPageW+40).Padding(16, 20, 28, 20).Children(func() {
					if a.settingsUI.restart {
						a.settingsRestartBanner(c)
					}
					a.settingsUI.rowN, a.settingsUI.blocks = 0, 0
					if p.Hero != "" {
						settingsHero(c, p)
						a.settingsUI.blocks++
					}
					p.Body(a, c)
					if p.Restore != nil {
						a.settingsRestoreButton(c, p)
					}
				})
			})
		})
	})
}

// settingsTitle is a page's title; an agent's subpage is named after it.
func (a *appState) settingsTitle(id string, p *settingsPageDef) string {
	if _, arg, ok := strings.Cut(id, ":"); ok && p.ID == "agent" {
		return agentTitle(arg)
	}
	return p.Title
}

func (a *appState) settingsToolbar(c *ui.Context, title string) {
	u := &a.settingsUI
	bar := c.TitleBar()
	ui.Row(c.Key("toolbar")).Height(max(44, bar.Height)).Shrink(0).FillWidth().Padding(0, 10+bar.Right, 0, 10).Gap(2).
		AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(pal.Border).DragWindow().Children(func() {
		if settingsIconButton(c, "back", iconChevronLeft, "Back", u.pos > 0) {
			a.settingsBack()
		}
		if settingsIconButton(c, "forward", iconChevronRight, "Forward", u.pos < len(u.stack)-1) {
			a.settingsForward()
		}
		ui.Text(c, title).FontSize(13.5).FontWeight(600).TextColor(pal.TextStrong).Margin(0, 0, 0, 6).
			SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		// A desktop that draws no buttons on the window, as a tiling one,
		// gets a close button here.
		if bar == (ui.TitleBar{}) {
			if settingsIconButton(c, "close", iconX, "Close", true) {
				a.closeSettings()
			}
		}
	})
}

// settingsIconButton is a toolbar button of an icon, dimmed when it does
// nothing; it reports a click.
func settingsIconButton(c *ui.Context, key string, icon *ui.SVG, label string, enabled bool) bool {
	b := ui.ButtonBase(c.Key(key)).Size(26, 26).Radius(7).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).
		Label(label).Tooltip(label).Disabled(!enabled)
	col := pal.TextMuted
	if !enabled {
		col = col.Alpha(0.35)
	} else if b.Hovered() {
		b.Background(pal.Hover)
		col = pal.Text
	}
	b.Children(func() { ui.Icon(c, icon).Size(15, 15).TextColor(col) })
	return enabled && b.Clicked()
}

// --- Building blocks ---

// settingsGroupFill is a group's box, a step down from the card it sits
// on.
func settingsGroupFill() ui.Color { return pal.Raised.Mix(pal.Wash, 0.42) }

// settingsHero introduces a page: its icon, title and what it does.
func settingsHero(c *ui.Context, p *settingsPageDef) {
	ui.Row(c.Key("hero")).FillWidth().Gap(14).Padding(14).Radius(8).Background(settingsGroupFill()).
		Border(1, pal.Border).Margin(0, 0, 18, 0).AlignItems(ui.Start).Children(func() {
		ui.Box(c).Size(40, 40).Radius(10).Background(pal.Accent.Alpha(0.12)).AlignItems(ui.Center).Justify(ui.Center).
			Shrink(0).Children(func() { ui.Icon(c, p.Icon).Size(20, 20).TextColor(pal.Accent) })
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Text(c, p.Title).FontSize(14).FontWeight(600).TextColor(pal.TextStrong)
			ui.Text(c, p.Hero).FontSize(12.5).TextColor(pal.TextMuted).Wrap()
			if p.HeroLink != "" {
				l := ui.ButtonBase(c.Key("learn")).AlignSelf(ui.Start).Label("Learn more")
				l.Children(func() { ui.Text(c, "Learn more…").FontSize(12.5).TextColor(pal.Accent) })
				if l.Clicked() {
					c.OpenURL(p.HeroLink)
				}
			}
		})
	})
}

// settingsGroup is a box of rows with lines between them, under an
// optional header and over an optional footer.
func (a *appState) settingsGroup(c *ui.Context, key, header, footer string, rows func()) {
	u := &a.settingsUI
	switch {
	case header != "":
		top := float32(18)
		if u.blocks == 0 {
			top = 0
		}
		ui.Text(c, header).FontSize(12.5).FontWeight(600).TextColor(pal.TextMuted).Margin(top, 2, 6, 2)
	case u.blocks > 0:
		ui.Box(c).Height(14).Shrink(0)
	}
	u.blocks++
	u.rowN = 0
	ui.Column(c.Key("group-"+key)).FillWidth().Radius(8).Background(settingsGroupFill()).Border(1, pal.Border).
		Clip().Children(rows)
	if footer != "" {
		ui.Text(c, footer).FontSize(11.5).TextColor(pal.TextFaint).Margin(6, 2, 0, 2).Wrap()
	}
}

// settingsRowOpts are a row's extras.
type settingsRowOpts struct {
	// id is the setting's, which a search lights; sub explains the row.
	id, sub string
	// restart marks a setting that applies after a restart.
	restart bool
	// check shows a ✓ or ✗ before the title: 1 for ✓, -1 for ✗.
	check int
	// link makes the row a button that opens a page, with a chevron.
	link string
	// mono shows sub in the monospaced font, as a path.
	mono bool
}

// settingsRow is a row of a group: the title and what explains it on the
// left, the control on the right. It reports a click on a link row.
func (a *appState) settingsRow(c *ui.Context, title string, o settingsRowOpts, control func()) bool {
	u := &a.settingsUI
	if u.rowN > 0 {
		ui.Box(c).Height(1).Shrink(0).FillWidth().Margin(0, 0, 0, 12).Background(pal.Border)
	}
	u.rowN++
	key := o.id
	if key == "" {
		key = title
	}
	var row ui.Element
	if o.link != "" {
		row = ui.ButtonBase(c.Key("row-" + key)).Label(title)
		if row.Hovered() {
			row.Background(pal.Hover)
		}
	} else {
		row = ui.Row(c.Key("row-" + key))
	}
	row.MinHeight(40).FillWidth().Padding(8, 12).Gap(12).AlignItems(ui.Center)
	if o.id != "" && o.id == u.flash {
		if el := c.Now().Sub(u.flashAt); el < 1400*time.Millisecond {
			k := float32(1)
			if el > 560*time.Millisecond {
				k = 1 - float32(el-560*time.Millisecond)/float32(840*time.Millisecond)
			}
			row.Background(pal.Accent.Alpha(0.12 * k))
			c.AnimationFrame()
			if !u.scrolled {
				row.ScrollIntoView()
				u.scrolled = true
			}
		} else {
			u.flash = ""
		}
	}
	row.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			if title != "" {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					if o.check != 0 {
						settingsCheckMark(c, o.check > 0)
					}
					ui.Text(c, title).FontSize(13).TextColor(pal.Text).Shrink(1).MinWidth(0).Wrap()
					if o.restart {
						settingsPill(c, "Applies after restart", pillPlain)
					}
				})
			}
			if o.sub != "" {
				s := ui.Text(c, o.sub).FontSize(11.5).TextColor(pal.TextMuted).Wrap()
				if o.mono {
					s.Font(monoFont).FontSize(11.5).TextColor(pal.TextSoft)
				}
				if o.check != 0 {
					s.Margin(0, 0, 0, 24)
				}
			}
		})
		if control != nil {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Shrink(0).Children(control)
		}
		if o.link != "" {
			ui.Icon(c, iconChevronRight).Size(14, 14).TextColor(pal.TextFaint).Shrink(0)
		}
	})
	return o.link != "" && row.Clicked()
}

// settingsCheckMark is a setup step's ✓ or ✗.
func settingsCheckMark(c *ui.Context, ok bool) {
	col, icon := pal.Ok, iconCheck
	if !ok {
		col, icon = pal.Bad, iconX
	}
	ui.Box(c).Size(18, 18).Radius(9).Background(col.Alpha(0.14)).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).
		Children(func() { ui.Icon(c, icon).Size(11, 11).TextColor(col) })
}

type pillKind int

const (
	pillPlain pillKind = iota
	pillOk
	pillWarn
)

// settingsPill is a small status: Installed, Off, Applies after restart.
func settingsPill(c *ui.Context, text string, kind pillKind) {
	bg, fg := pal.Chip, pal.TextMuted
	switch kind {
	case pillOk:
		bg, fg = pal.Ok.Alpha(0.14), pal.Ok
	case pillWarn:
		bg, fg = pal.Warn.Alpha(0.14), pal.Warn
	}
	ui.Box(c).Padding(1, 7).Radius(999).Background(bg).Shrink(0).Children(func() {
		ui.Text(c, text).FontSize(10.5).FontWeight(500).TextColor(fg).SingleLine()
	})
}

// settingsSwitch is a small switch, as macOS's: the accent when on.
func settingsSwitch(c *ui.Context, key string, on *bool, disabled bool) bool {
	sw := ui.SwitchBase(c.Key("sw-"+key), on).Size(30, 18).Radius(9).Shrink(0).Disabled(disabled).Label(key)
	track := pal.BorderStrong
	if *on {
		track = pal.Accent
	}
	if disabled {
		sw.Opacity(0.4)
	}
	x := float32(2)
	if *on {
		x = 14
	}
	sw.Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(r, track, 9)
		p.Fill(ui.Rect{X: r.X + x, Y: r.Y + 2.5, W: 14, H: 14}, black.Alpha(0.18), 7)
		p.Fill(ui.Rect{X: r.X + x, Y: r.Y + 2, W: 14, H: 14}, ui.Color{R: 255, G: 255, B: 255, A: 255}, 7)
	})
	return !disabled && sw.Changed()
}

// settingSwitchRow is a row of a switch over a setting of settingDefs.
func (a *appState) settingSwitchRow(c *ui.Context, id string, disabled bool) {
	d, ok := settingDefByID(id)
	if !ok || d.On == nil {
		return
	}
	on := d.On(&a.settings)
	a.settingsRow(c, d.Title, settingsRowOpts{id: d.ID, sub: d.Sub, restart: d.Restart}, func() {
		if settingsSwitch(c, d.ID, &on, disabled) {
			d.Set(&a.settings, on)
			if d.Restart {
				a.settingsUI.restart = true
			}
			a.applySettings()
		}
	})
}

// settingDefRow is a row of a setting whose control the page draws.
func (a *appState) settingDefRow(c *ui.Context, id string, control func()) {
	d, _ := settingDefByID(id)
	a.settingsRow(c, d.Title, settingsRowOpts{id: d.ID, sub: d.Sub, restart: d.Restart}, control)
}

// settingsPopup is a pop-up button choosing one of options (values and
// what they show), as macOS's: the choice, then up and down chevrons.
func settingsPopup(c *ui.Context, key string, value *string, options [][2]string) bool {
	shown := *value
	for _, o := range options {
		if o[0] == *value {
			shown = o[1]
		}
	}
	before := *value
	sel := ui.SelectBase(c.Key("pop-"+key), value)
	sel.Trigger.Height(26).Padding(0, 8, 0, 10).Gap(6).Radius(6).AlignItems(ui.Center).Label(key)
	face(sel.Trigger, pal.Raised).Border(1, pal.BorderStrong)
	sel.Trigger.Children(func() {
		ui.Text(c, shown).FontSize(12).TextColor(pal.Text).SingleLine()
		ui.Icon(c, iconChevronsUpDown).Size(12, 12).TextColor(pal.TextFaint)
	})
	sel.Popup(func(panel ui.Element) {
		floating(panel.Margin(4, 0, 0, 0).Padding(4).Radius(8).Background(pal.Popover).MinWidth(140))
		for _, o := range options {
			item := sel.Item(o[0]).Padding(5, 10).Radius(6).Gap(8).AlignItems(ui.Center)
			fg := pal.Text
			if item.Highlighted() {
				item.Background(pal.Accent)
				fg = pal.AccentText
			}
			item.Children(func() {
				mark := ui.Icon(c, iconCheck).Size(12, 12).TextColor(fg)
				if o[0] != *value {
					mark.Opacity(0)
				}
				ui.Text(c, o[1]).FontSize(12.5).TextColor(fg).SingleLine()
			})
		}
	})
	return *value != before
}

// settingsSegmented is a segmented control of a few labels; it reports a
// new choice.
func settingsSegmented(c *ui.Context, key string, value *string, labels []string) bool {
	i := 0
	for j, l := range labels {
		if strings.EqualFold(l, *value) {
			i = j
		}
	}
	before := i
	seg := ui.SegmentedBase(c.Key("seg-"+key), &i, len(labels))
	seg.Track.Padding(2).Gap(2).Radius(7).Background(pal.Chip).Children(func() {
		for j, l := range labels {
			s := seg.Segment(j).Padding(3, 10).Radius(5).Label(l)
			fg := pal.TextMuted
			if j == i {
				s.Background(pal.Raised).Shadow(0, 0.5, 2, 0, black.Alpha(0.15))
				fg = pal.Text
			}
			s.Children(func() { ui.Text(c, l).FontSize(12).TextColor(fg).SingleLine() })
		}
	})
	if i != before {
		*value = strings.ToLower(labels[i])
		return true
	}
	return false
}

// settingsSlider is a slider with its value shown after it; it reports a
// change.
func settingsSlider(c *ui.Context, key string, value *float64, lo, hi, step float64, format func(float64) string) bool {
	before := *value
	s := ui.SliderBase(c.Key("slider-"+key), value, lo, hi).Step(step).Size(140, 18).PaddingX(8).Label(key)
	at := float32((*value - lo) / (hi - lo))
	s.Draw(func(p *ui.Painter, r ui.Rect) {
		x0, w := r.X+8, r.W-16
		p.Fill(ui.Rect{X: x0, Y: r.Y + 7, W: w, H: 4}, pal.BorderStrong, 2)
		p.Fill(ui.Rect{X: x0, Y: r.Y + 7, W: w * at, H: 4}, pal.Accent, 2)
		x := x0 + w*at
		p.Fill(ui.Rect{X: x - 8, Y: r.Y + 1.5, W: 16, H: 16}, black.Alpha(0.2), 8)
		p.Fill(ui.Rect{X: x - 8, Y: r.Y + 1, W: 16, H: 16}, pal.Accent, 8)
	})
	ui.Text(c, format(*value)).FontSize(12).TextColor(pal.TextMuted).Width(54).TextAlign(ui.End).SingleLine()
	return *value != before
}

// settingsButton is a small bordered button; primary fills it with the
// accent, danger colors its label red. It reports a click.
func settingsButton(c *ui.Context, label string, primary, danger, disabled bool) bool {
	b := ui.ButtonBase(c.Key("btn-"+label)).Height(26).Padding(0, 10).Radius(6).AlignItems(ui.Center).Shrink(0).
		Label(strings.Split(label, "##")[0]).Disabled(disabled)
	fg := pal.Text
	switch {
	case primary:
		bg := pal.Accent
		if b.Hovered() && !disabled {
			bg = pal.AccentHover
		}
		b.Background(bg)
		fg = pal.AccentText
	default:
		bg := pal.Raised
		if b.Hovered() && !disabled {
			bg = pal.Raised.Mix(pal.Text, 0.08)
		}
		face(b, bg).Border(1, pal.BorderStrong)
	}
	if danger {
		fg = pal.Bad
	}
	if disabled {
		b.Opacity(0.55)
	}
	// "Revoke##id" shows "Revoke": what follows ## only tells buttons of
	// one label apart.
	shown, _, _ := strings.Cut(label, "##")
	b.Children(func() {
		w := 400
		if primary {
			w = 500
		}
		ui.Text(c, shown).FontSize(12).FontWeight(w).TextColor(fg).SingleLine()
	})
	return !disabled && b.Clicked()
}

// settingsTextField is a small text field; it reports Enter or the field
// losing the keyboard after an edit.
func (a *appState) settingsTextField(c *ui.Context, key string, value *string, placeholder string, width float32, mono bool) bool {
	f := ui.Row(c.Key("field-"+key)).Height(26).Width(width).Padding(0, 8).Radius(6).AlignItems(ui.Center).
		Background(pal.Input).Border(1, pal.BorderStrong)
	done := false
	f.Children(func() {
		in := ui.TextInputBase(c, value).Placeholder(placeholder).FontSize(12).Grow(1).MinWidth(0).Label(key)
		if mono {
			in.Font(monoFont)
		}
		focused := in.Focused()
		if focused {
			f.Border(1, pal.Accent)
		}
		// A field takes its value with Enter, or as the keyboard leaves it.
		if in.Submitted() || (a.settingsUI.focused[key] && !focused) {
			done = true
		}
		a.settingsUI.focused[key] = focused
	})
	return done
}

// settingsActions is a row of buttons under a group, at its right.
func settingsActions(c *ui.Context, buttons func()) {
	ui.Row(c).FillWidth().Margin(12, 0, 0, 0).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(buttons)
}

func (a *appState) settingsRestoreButton(c *ui.Context, p *settingsPageDef) {
	ui.Row(c).FillWidth().Margin(22, 0, 0, 0).Justify(ui.End).Children(func() {
		b := ui.ButtonBase(c.Key("restore")).Label("Restore Defaults")
		col := pal.TextMuted
		if b.Hovered() {
			col = pal.Text
		}
		b.Children(func() { ui.Text(c, "Restore Defaults").FontSize(12).TextColor(col) })
		if b.Clicked() {
			a.settingsUI.sheet = &settingsSheet{
				title:   "Restore the defaults of " + p.Title + "?",
				text:    "Every setting on this page goes back to how omaterm ships.",
				confirm: "Restore",
				run: func(c *ui.Context) {
					p.Restore(&a.settings, defaultSettings())
					if p.ID == "terminal" {
						a.fontSize = a.settings.Terminal.FontSize
					}
					if p.ID == "editor" {
						a.diffSplit = a.settings.Editor.DiffSplit
					}
					if p.ID == "appearance" {
						a.setThemeMode(a.settings.Theme)
					}
					a.applySettings()
				},
			}
		}
	})
}

// settingsRestartBanner offers to restart omaterm, for settings that
// apply as it starts.
func (a *appState) settingsRestartBanner(c *ui.Context) {
	ui.Row(c.Key("restart")).FillWidth().Gap(10).Padding(8, 12).Radius(8).Margin(0, 0, 14, 0).AlignItems(ui.Center).
		Background(pal.Accent.Alpha(0.12)).Children(func() {
		ui.Icon(c, iconInfo).Size(14, 14).TextColor(pal.Accent)
		ui.Text(c, "Restart omaterm to apply your changes.").FontSize(12.5).TextColor(pal.Text).Grow(1).MinWidth(0).Wrap()
		if settingsButton(c, "Restart Now", true, false, false) {
			a.saveState()
			a.saveSettings()
			a.saveUIPrefs()
			if a.win != nil {
				mygo.App.Relaunch()
			}
		}
	})
}

// settingsSheetView is the confirmation open over the window: a panel
// near the top, over the dimmed window, as the app's other dialogs.
func (a *appState) settingsSheetView(c *ui.Context) {
	sh := a.settingsUI.sheet
	if sh == nil {
		return
	}
	open := true
	ui.DialogBase(c, &open, func(backdrop, panel ui.Element) {
		backdrop.Background(pal.Scrim).Justify(ui.Start)
		floating(panel.Top(70).Width(400).Padding(18).Gap(6).Radius(10).Background(pal.Popover))
		ui.Text(c, sh.title).FontSize(14).FontWeight(600).TextColor(pal.TextStrong).Wrap()
		if sh.text != "" {
			ui.Text(c, sh.text).FontSize(12.5).TextColor(pal.TextMuted).Wrap().Margin(0, 0, 4, 0)
		}
		for _, it := range sh.items {
			ui.Row(c).Gap(8).Margin(0, 0, 4, 4).Children(func() {
				ui.Text(c, "•").FontSize(12.5).TextColor(pal.TextMuted)
				ui.Column(c).Grow(1).MinWidth(0).Children(func() {
					ui.Text(c, it[0]).FontSize(12.5).TextColor(pal.Text).Wrap()
					if it[1] != "" {
						ui.Text(c, it[1]).FontSize(11.5).TextColor(pal.TextFaint).Wrap()
					}
				})
			})
		}
		ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Margin(8, 0, 0, 0).Children(func() {
			if sh.run == nil {
				if settingsButton(c, "Done", true, false, false) {
					open = false
				}
				return
			}
			if settingsButton(c, "Cancel", false, false, false) {
				open = false
			}
			if settingsButton(c, sh.confirm, true, false, false) {
				open = false
				sh.run(c)
			}
		})
	})
	if !open && a.settingsUI.sheet == sh {
		a.settingsUI.sheet = nil
	}
}
