package main

import (
	"github.com/egoist/mygo/ui"
)

// The top tab bar with terminal/editor/diff tabs and the window-level
// controls on the right.

func (a *appState) tabBar(c *ui.Context) ui.Element {
	return ui.Row(c).Height(42).AlignItems(ui.End).Background(colBg).BorderWidth(0, 0, 1, 0).BorderColor(colBorder).Children(func() {
		// Show-projects button, only while the sidebar is hidden.
		if !a.sidebarVisible {
			if cmdButton(c, iconPanelLeft, 16, false).MarginStart(8).Clicked() {
				a.sidebarVisible = true
			}
		}

		ui.Row(c).Grow(1).AlignItems(ui.End).Padding(0, 4).Gap(4).ClipX().Children(func() {
			for i := range a.tabs {
				a.topTab(c, i)
			}
			if cmdButton(c, iconPlus, 16, false).Margin(0, 2).Clicked() {
				a.newTerminalTab()
				c.Toast("New terminal")
			}
		})

		ui.Row(c).AlignItems(ui.Center).Gap(2).Padding(0, 8).Children(func() {
			if cmdButton(c, iconSearch, 16, false).Clicked() {
				c.Toast("Search")
			}
			if cmdButton(c, iconPanelRight, 16, a.inspectorVisible).Clicked() {
				a.inspectorVisible = !a.inspectorVisible
			}
		})
	})
}

func (a *appState) topTab(c *ui.Context, i int) ui.Element {
	t := &a.tabs[i]
	active := i == a.activeTab
	// The mock's .top-tab: rounded top corners, a blue top edge while
	// active, sitting flush with the content below.
	e := ui.ButtonBase(c).Key(i).Height(36).Radius(6, 6, 0, 0).Padding(0, 12).Gap(6)
	if active {
		e = e.Background(ui.Hex("#141a21")).DrawOver(func(p *ui.Painter, r ui.Rect) {
			p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W, H: 1}, ui.RGBA(90, 169, 255, 0.9), 0)
		})
	} else if e.Hovered() {
		e = e.Background(colPanel2)
	}
	e.Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			switch t.Kind {
			case tabTerminal:
				dot(c, t.Dot, 8)
			case tabEditor:
				ui.Icon(c, iconFileCode).Width(14).Height(14).TextColor(synTS)
			case tabDiff:
				ui.Icon(c, iconGitCompare).Width(14).Height(14).TextColor(colGreen)
			}
			labelCol := colText2
			if active {
				labelCol = ui.Hex("#ffffff")
			}
			ui.Text(c, t.Label).FontSize(12).TextColor(labelCol).SingleLine().Shrink(0)
			if t.Detail != "" {
				ui.Spacer(c)
				ui.Text(c, t.Detail).FontSize(10).TextColor(colMuted).SingleLine().Shrink(0)
			}
			if t.Dirty {
				ui.Text(c, "M").FontSize(10).TextColor(colYellow).SingleLine().Shrink(0)
			}
			if len(a.tabs) > 1 {
				ui.Spacer(c)
				x := ui.ButtonBase(c).Padding(2).Radius(3).Shrink(0).Children(func() {
					ui.Icon(c, iconX).Width(14).Height(14).TextColor(colMuted)
				})
				if x.Clicked() {
					a.closeTab(i)
				}
			}
		})
	})
	if e.Clicked() {
		a.activeTab = i
		a.activeView = t.Kind
	}
	return e
}

func (a *appState) newTerminalTab() {
	a.tabs = append(a.tabs, topTab{Kind: tabTerminal, Label: "Tab " + itoa(len(a.tabs)+1), Dot: dotGreen})
	a.activeTab = len(a.tabs) - 1
	a.activeView = tabTerminal
}

func (a *appState) closeTab(i int) {
	a.tabs = append(a.tabs[:i], a.tabs[i+1:]...)
	if a.activeTab >= len(a.tabs) {
		a.activeTab = len(a.tabs) - 1
	}
	if a.activeTab >= 0 {
		a.activeView = a.tabs[a.activeTab].Kind
	}
}
