package main

import (
	"math"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"omaterm/service"
)

// The bottom status bar. Each segment is a full-height hoverable cell, as
// the mock's footer buttons.

func (a *appState) statusBar(c *ui.Context) ui.Element {
	return ui.Row(c).Height(24).Shrink(0).AlignItems(ui.Center).Padding(0, 4).Children(func() {
		// Project and branch: the only identity left when the sidebar is
		// hidden, so it stays visible and ellipsizes rather than pushing
		// the counters out.
		statusSegment(c, func() {
			name := ""
			branch := ""
			if p := a.project(); p != nil {
				name = p.Name
				branch = p.Branch
			}
			if name != "" {
				ui.Icon(c, iconFolderGit).Width(12).Height(12).TextColor(pal.TextMuted)
				ui.Text(c, name).FontSize(10).TextColor(pal.TextSoft).SingleLine().
					Ellipsis("…").Shrink(1).MinWidth(0).MaxWidth(150).Tooltip(name)
				if branch != "" {
					ui.Text(c, "›").FontSize(10).TextColor(pal.TextFaint).Shrink(0)
				}
			}
			if branch != "" {
				ui.Icon(c, iconGitBranch).Width(12).Height(12).TextColor(pal.TextMuted)
				ui.Text(c, branch).FontSize(10).TextColor(pal.TextMuted).SingleLine().
					Ellipsis("…").Shrink(1).MinWidth(0).MaxWidth(360).Tooltip(branch)
			}
		})
		// Both open the Info tab, which lists them.
		if statusSegment(c, func() {
			ui.Text(c, "●").FontSize(8).TextColor(pal.Ok)
			ui.Text(c, plural(len(a.processes), "process")).FontSize(10).TextColor(pal.TextMuted).SingleLine().Shrink(0)
		}).Tooltip("Show processes (" + keyLabels("view.info") + ")").Clicked() {
			a.inspectorVisible = true
			a.showInspectorTab(0)
		}
		if statusSegment(c, func() {
			ui.Icon(c, iconRadio).Width(12).Height(12).TextColor(pal.TextMuted)
			ui.Text(c, plural(len(a.ports), "port")).FontSize(10).TextColor(pal.TextMuted).SingleLine().Shrink(0)
		}).Tooltip("Show ports (" + keyLabels("view.info") + ")").Clicked() {
			a.inspectorVisible = true
			a.showInspectorTab(0)
		}

		ui.Spacer(c)

		if a.webOn {
			if statusSegment(c, func() {
				ui.Icon(c, iconGlobe).Width(12).Height(12).TextColor(pal.Accent)
				ui.Text(c, "Web").FontSize(10).TextColor(pal.TextMuted).SingleLine().Shrink(0)
			}).Tooltip("Web access is on: pair a device").Clicked() {
				a.pairDevice(c)
			}
		}

		// Real load when the sampler has run; hidden otherwise so tests
		// and non-Linux systems show no invented numbers.
		if a.sysStats.OK && a.settings.ShowSystemLoad {
			if a.sysStats.CPUPercent >= 0 {
				statusSegment(c, func() {
					ui.Text(c, "CPU "+strconv.Itoa(int(math.Round(a.sysStats.CPUPercent)))+"%").
						FontSize(10).TextColor(pal.TextMuted).SingleLine().Shrink(0)
				})
			}
			if a.sysStats.MemTotal > 0 {
				statusSegment(c, func() {
					ui.Text(c, "MEM "+service.FormatBytes(a.sysStats.MemUsed)).
						FontSize(10).TextColor(pal.TextMuted).SingleLine().Shrink(0).
						Tooltip(service.FormatBytes(a.sysStats.MemUsed) + " of " + service.FormatBytes(a.sysStats.MemTotal) + " used")
				})
			}
		}
	})
}

// plural renders n with its noun, singular at one: "1 process",
// "2 processes", "2 ports".
func plural(n int, noun string) string {
	if n == 1 {
		return itoa(n) + " " + noun
	}
	if strings.HasSuffix(noun, "s") {
		return itoa(n) + " " + noun + "es"
	}
	return itoa(n) + " " + noun + "s"
}

// statusSegment is one full-height, hoverable status bar cell.
func statusSegment(c *ui.Context, children func()) ui.Element {
	e := ui.ButtonBase(c).FillHeight().Padding(0, 10)
	if e.Hovered() {
		e.Background(pal.Input)
	}
	e.Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(children)
	})
	return e
}
