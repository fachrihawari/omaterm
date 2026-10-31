// Partly adapted from Kero (github.com/egoist/kero, MIT, docs/third_party/kero-LICENSE),
// panes.go (the pane header).

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"omaterm/internal/agent"
	"omaterm/internal/procs"
	"omaterm/internal/session"
)

// headerH is the height of a pane's header.
func (a *appState) headerH() float32 {
	if a.settings.Density == "compact" {
		return 26
	}
	return 30
}

// viewHeader is the header of a view a tab shows whole (an editor, a
// picture, a diff, the graph): as tall as a pane's, with its title
// styled alike, so moving between tabs doesn't shift the content.
func (a *appState) viewHeader(c *ui.Context, children func()) {
	ui.Row(c.Key("view-hdr")).Height(a.headerH()).Shrink(0).AlignItems(ui.Center).
		Padding(0, 6, 0, 10).Gap(8).Children(children)
}

// headerTitle is a header's icon and title, and a faint detail after it
// (a folder, a scope), as a pane's header shows them.
func headerTitle(c *ui.Context, icon *ui.SVG, iconColor ui.Color, title, detail string) {
	ui.Row(c).Gap(7).AlignItems(ui.Center).Shrink(1).MinWidth(0).Children(func() {
		ui.Icon(c, icon).Size(14, 14).TextColor(iconColor)
		ui.Text(c, title).FontSize(12.5).FontWeight(600).TextColor(pal.Text).SingleLine().Ellipsis("…").
			Shrink(0.5).MinWidth(min(90, float32(len(title))*7)).Tooltip(title)
		if detail != "" {
			ui.Text(c, detail).FontSize(12).TextColor(pal.TextFaint).SingleLine().Ellipsis("…").Shrink(2).MinWidth(0)
		}
	})
}

// paneHeader builds a pane's header — its status or program, title and
// directory, git and status chips, and the controls that show on hover —
// and reports whether it is being dragged.
func (a *appState) paneHeader(c *ui.Context, tab *topTab, id string, t *terminal.Terminal, multi, focused, hovered bool) (dragging bool) {
	info, _ := a.paneInfo(id)
	// The card's width, as the last frame laid it out (0 before): a narrow
	// pane's header gives up its chip and lets its title ellipsize.
	w := a.handle("card-" + id).Bounds(c).W
	narrow := w > 0 && w < 360
	h := ui.Row(c.Key("hdr")).Height(a.headerH()).Shrink(0).AlignItems(ui.Center).Padding(0, 6, 0, 10).Gap(8)
	if h.Pressed() && !focused {
		a.focusPane(id)
	}
	h.Children(func() {
		// The title is the pane's handle: dragged, it takes the pane to
		// another place in the tab.
		grip := ui.Row(c.Key("grip")).Gap(7).AlignItems(ui.Center).Shrink(1).MinWidth(0).
			Height(24).Padding(0, 6).Margin(0, 0, 0, -6).Radius(6)
		if multi {
			grip.Drag(paneDrag{tab.ID, id}).Tooltip("Drag to move the pane")
			dragging = grip.Dragging()
			switch {
			case dragging:
				floating(grip.Background(pal.Popover))
			case grip.Hovered():
				grip.Background(pal.Hover).Cursor(ui.CursorGrab)
			}
		}
		// A process's pane is named for the process, with its command: the
		// shell that runs the command is no name for it.
		proc, isProc := a.procOfTab(*tab)
		grip.Children(func() {
			switch {
			case isProc && proc.Status != procs.Stopped:
				a.processIcon(c, proc, 13)
			case statusState(info) != iconNone:
				a.statusIcon(c, info, 13)
			default:
				ui.Icon(c, programIcon(firstNonEmpty(info.Agent, info.Process, info.Runtime))).Size(14, 14).TextColor(pal.TextFaint)
			}
			title := paneTitle(info, t)
			if isProc {
				title = proc.Name
			}
			name := ui.Text(c, title).FontSize(12.5).FontWeight(600).TextColor(pal.Text).SingleLine().Ellipsis("…").
				Shrink(0.5).MinWidth(min(90, float32(len(title))*7)).MaxWidth(260)
			if narrow {
				// Its whole title would draw over the chips and controls
				// beside it: it ellipsizes too.
				name.Shrink(1).MinWidth(min(36, float32(len(title))*7))
			}
			if multi && !focused && !dragging {
				name.TextColor(pal.TextMuted)
			}
			sub := shortDir(info.WorkDir())
			if isProc {
				sub = proc.Command
			}
			if sub != "" && sub != title {
				ui.Text(c, sub).FontSize(12).TextColor(pal.TextFaint).SingleLine().Ellipsis("…").Shrink(2).MinWidth(0)
			}
			if isProc && proc.Link != "" && !narrow {
				// Where the process is served, after what it runs.
				ui.Text(c, procLinkHost(proc.Link)).FontSize(12).TextColor(pal.TextFaint).SingleLine().
					Shrink(0).Tooltip("Served at " + proc.Link)
			}
			if isProc && proc.Share != "" {
				pillShare(c, proc, func() { a.openURL(proc.Share) })
			}
		})
		if g, ok := a.git.of(info.WorkDir()); ok && g.Branch != "" {
			a.gitChip(c, g.Branch, g.Added, g.Removed)
		}
		ui.Spacer(c)
		if w == 0 || w >= 300 {
			// Narrower, the status icon by the title tells the state.
			a.statusChip(c, info)
		}
		a.paneControls(c, tab, id, (hovered || focused && multi) && !dragging, multi)
	})
	return dragging
}

// paneTitle names a pane by what it runs: an agent by its name, a program
// by its title or name, a shell at its prompt by its directory.
func paneTitle(i session.Info, t *terminal.Terminal) string {
	if i.Agent != "" {
		return agent.DisplayName(i.Agent)
	}
	title := agent.CleanTitle(i.Title)
	if title == "" && t != nil {
		title = agent.CleanTitle(t.Title())
	}
	if title != "" && !shellTitle(title, i) {
		return title
	}
	if i.Process != "" {
		return i.Process
	}
	if d := i.WorkDir(); d != "" {
		if s := shortDir(d); s == "~" {
			return s
		}
		return filepath.Base(d)
	}
	if i.Shell != "" {
		return i.Shell
	}
	return "Terminal"
}

// shellTitle tells the title a shell sets at its prompt, as
// "user@host:~/dir", which says no more than the directory, or the title
// a Windows console gives its shell: the program's path, or the window's name.
func shellTitle(title string, i session.Info) bool {
	if i.Process != "" {
		return false
	}
	return title == i.Shell || strings.Contains(title, "@") && strings.Contains(title, ":") || strings.HasPrefix(title, "~") || strings.HasPrefix(title, "/") || windowsShellTitle(title)
}

// windowsShellTitle tells the title of a Windows console's shell: the path
// of its program, as C:\…\pwsh.exe, or the name of its window.
func windowsShellTitle(title string) bool {
	if i := strings.LastIndexByte(title, '\\'); i >= 0 {
		switch strings.ToLower(title[i+1:]) {
		case "pwsh.exe", "powershell.exe", "cmd.exe":
			return true
		}
		return false
	}
	switch strings.ToLower(strings.TrimPrefix(title, "Administrator: ")) {
	case "windows powershell", "command prompt", "pwsh":
		return true
	}
	return false
}

// shortDir writes a directory with the home directory as "~".
func shortDir(d string) string {
	if d == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if d == home {
			return "~"
		}
		if rest, ok := strings.CutPrefix(d, home+"/"); ok {
			return "~/" + rest
		}
	}
	return d
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// gitChip is a pane's branch and the lines changed under it.
func (a *appState) gitChip(c *ui.Context, branch string, added, removed int) {
	chip := ui.Row(c.Key("git")).Height(20).Radius(6).Padding(0, 7).Gap(5).AlignItems(ui.Center).
		Background(pal.Chip).Shrink(2).MinWidth(64).MaxWidth(220)
	chip.Children(func() {
		ui.Icon(c, iconGitBranch).Size(11, 11).TextColor(pal.TextFaint)
		ui.Text(c, branch).FontSize(11.5).TextColor(pal.TextSoft).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		if added > 0 {
			ui.Text(c, "+"+strconv.Itoa(added)).FontSize(11.5).TextColor(pal.Ok).SingleLine().Shrink(0)
		}
		if removed > 0 {
			ui.Text(c, "−"+strconv.Itoa(removed)).FontSize(11.5).TextColor(pal.Bad).SingleLine().Shrink(0)
		}
	})
	if chip.Clicked() {
		a.setInspector(true)
		a.showInspectorTab(2)
	}
}

// statusChip words a pane's status: what it waits for, how long it has
// worked, how it failed.
func (a *appState) statusChip(c *ui.Context, i session.Info) {
	text, bg, fg := statusWords(i, nowFunc())
	if text == "" {
		return
	}
	// The chips give way before the controls: a long message once pushed
	// Zoom and Close off the card.
	ui.Row(c.Key("status")).Height(20).Radius(6).Padding(0, 7).AlignItems(ui.Center).Background(bg).
		Shrink(1).MinWidth(72).MaxWidth(280).
		Children(func() {
			ui.Text(c, text).FontSize(11.5).FontWeight(500).TextColor(fg).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		})
	if i.Status == session.Working && i.Progress < 0 {
		c.After(time.Second) // the elapsed time ticks
	}
}

// statusWords is a status chip's text and colors, "" for none.
func statusWords(i session.Info, now time.Time) (string, ui.Color, ui.Color) {
	switch i.Status {
	case session.Waiting:
		text := i.Message
		if text == "" {
			switch i.Kind {
			case "permission":
				text = "Needs permission"
			case "question":
				text = "Has a question"
			case "auth":
				text = "Needs sign-in"
			default:
				text = "Needs you"
			}
		}
		return text, pal.AttentionSoft, pal.Attention
	case session.Working:
		if i.Progress >= 0 {
			return strconv.Itoa(i.Progress) + "%", pal.Chip, pal.TextMuted
		}
		if i.Message != "" {
			return i.Message, pal.Chip, pal.TextMuted
		}
		return elapsed(now.Sub(time.UnixMilli(i.Since))), pal.Chip, pal.TextMuted
	case session.Failed:
		return firstNonEmpty(i.Message, "Failed"), pal.Bad.Alpha(0.1), pal.Bad
	case session.Exited:
		if i.ExitCode != nil && *i.ExitCode != 0 {
			return "Exited " + strconv.Itoa(*i.ExitCode), pal.Chip, pal.TextMuted
		}
		return "Exited", pal.Chip, pal.TextMuted
	}
	if i.Message != "" {
		return i.Message, pal.Chip, pal.TextMuted
	}
	return "", ui.Color{}, ui.Color{}
}

// elapsed writes a duration as a timer does: 12s, 1m 12s, 1h 5m.
func elapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d.Seconds())
	switch {
	case s < 60:
		return strconv.Itoa(s) + "s"
	case s < 3600:
		return strconv.Itoa(s/60) + "m " + strconv.Itoa(s%60) + "s"
	}
	return strconv.Itoa(s/3600) + "h " + strconv.Itoa(s%3600/60) + "m"
}

// paneControls are a pane's split, zoom and close buttons, which show on
// hover, fading in; hidden, they give their room to the chips.
func (a *appState) paneControls(c *ui.Context, tab *topTab, id string, show, multi bool) {
	// A narrow pane keeps its title readable: it shows zoom and close
	// only, and below that none (the keys and the palette still split,
	// zoom and close it).
	w := a.handle("card-" + id).Bounds(c).W
	if !show || w > 0 && w < 170 {
		return
	}
	// A process's pane is the process alone: a shell split into its tab
	// would go when the process restarts.
	splits := (w == 0 || w >= 260) && tab.Kind != tabProcess
	row := ui.Row(c.Key("ctl")).Gap(2).AlignItems(ui.Center).Shrink(0)
	row.Opacity(row.Animate("show", 1, 120*time.Millisecond))
	row.Children(func() {
		if splits {
			if cmdButton(c, iconColumns2, 13, false).Tooltip("Split right (" + keyLabels("terminal.splitRight") + ")").Clicked() {
				a.splitActivePaneAt(c, id, splitVertical)
			}
			if cmdButton(c, iconRows2, 13, false).Tooltip("Split down (" + keyLabels("terminal.splitDown") + ")").Clicked() {
				a.splitActivePaneAt(c, id, splitHorizontal)
			}
		}
		if multi || tab.Zoomed != "" {
			zoomIcon, tip := iconMaximize, "Zoom pane"
			if tab.Zoomed == id {
				zoomIcon, tip = iconMinimize, "Show all panes"
			}
			if cmdButton(c, zoomIcon, 13, false).Tooltip(tip + " (" + keyLabels("pane.zoom") + ")").Clicked() {
				a.activePaneTermID = id
				a.toggleZoom()
			}
		}
		if cmdButton(c, iconX, 13, false).Tooltip("Close pane").Clicked() {
			if multi {
				a.confirmCloseRunning([]string{id}, "Close Pane", func() { a.removePane(id) })
			} else {
				a.closeTabAsking(a.activeTab)
			}
		}
	})
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}
