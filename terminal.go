package main

import (
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// The terminal view: real terminal panes (Ghostty VT via MyGo's plugin)
// with split layout support. Each pane is its own *terminal.Terminal;
// the plugin handles I/O, rendering, input, scrollback, selection, etc.

func (a *appState) terminalView(c *ui.Context) ui.Element {
	return ui.Column(c).Fill().Background(colBg2).Children(func() {
		switch a.termLayout() {
		case layoutSingle:
			a.termPane(c, 0)
		case layoutVertical:
			ui.SplitPercent(c, &a.splitVPct, func() {
				a.termPane(c, 0)
			}, func() {
				a.termPane(c, 1)
			}).Fill()
		case layoutHorizontal:
			ui.SplitVerticalPercent(c, &a.splitHPct, func() {
				a.termPane(c, 0)
			}, func() {
				a.termPane(c, 1)
			}).Fill()
		default: // mixed
			ui.SplitPercent(c, &a.splitVPct, func() {
				a.termPane(c, 0)
			}, func() {
				ui.SplitVerticalPercent(c, &a.splitHPct, func() {
					a.termPane(c, 1)
				}, func() {
					a.termPane(c, 2)
				}).Fill()
			}).Fill()
		}
	})
}

// termPaneKey identifies one pane: the terminal tab it belongs to and its
// index within that tab's layout.
func (a *appState) termPaneKey(i int) string {
	id := "tab" + itoa(a.activeTab)
	if a.activeTab >= 0 && a.activeTab < len(a.tabs) && a.tabs[a.activeTab].Path != "" {
		id = a.tabs[a.activeTab].Path
	}
	return id + "/" + itoa(i)
}

// termLayout returns the active terminal tab's pane arrangement. A tab
// starts with one pane and is split from there.
func (a *appState) termLayout() layoutKind {
	if a.activeTab >= 0 && a.activeTab < len(a.tabs) {
		return a.tabs[a.activeTab].Layout
	}
	return layoutSingle
}

// setTermLayout splits (or unsplits) the active terminal tab.
func (a *appState) setTermLayout(k layoutKind) {
	if a.activeTab >= 0 && a.activeTab < len(a.tabs) {
		a.tabs[a.activeTab].Layout = k
	}
}

// term returns the terminal for pane i, creating it on first use. The
// program starts when a view first shows it (or shortly after New).
func (a *appState) term(i int) *terminal.Terminal {
	key := a.termPaneKey(i)
	if t, ok := a.termPanes[key]; ok && t != nil {
		return t
	}
	shell := os.Getenv("SHELL")
	var cmd []string
	if shell != "" {
		cmd = []string{shell}
	}
	t, err := terminal.New(terminal.Options{
		Command: cmd, // empty runs the login shell
		Dir:     a.workspace,
		OnTitle: func(title string) {
			// Recorded for the pane header; read via t.Title().
		},
		OnExit: func(code int) {
			// Mark the pane as exited so the toolbar offers Restart.
			// Safe to touch app state here only lightly; the view
			// re-reads the terminal's state on the next frame.
		},
	})
	if err != nil {
		// Keep a nil entry so we don't retry every frame; the pane
		// renders an error instead.
		a.termPanes[key] = nil
		return nil
	}
	a.termPanes[key] = t
	return t
}

func (a *appState) termPane(c *ui.Context, i int) {
	t := a.term(i)
	ui.Column(c).Fill().Background(colBg2).Children(func() {
		// Header strip.
		ui.Row(c).Height(32).AlignItems(ui.Center).Padding(0, 12).Gap(8).
			BorderWidth(0, 0, 1, 0).BorderColor(colBorder).ClipX().Children(func() {
			title := "Terminal"
			if t != nil {
				if tt := t.Title(); tt != "" {
					title = tt
				}
				if d := t.Dir(); d != "" {
					ui.Text(c, d).FontSize(10).TextColor(colMuted2).SingleLine().Shrink(0)
				}
			}
			dot(c, dotGreen, 6)
			txt(c, title, 10, colText2)
			ui.Spacer(c)
			// Hover toolbar.
			hovered := false
			_ = hovered
		})

		if t == nil {
			ui.Box(c).Grow(1).Children(func() {
				ui.Text(c, "failed to start terminal").FontSize(12).TextColor(colRed)
			})
			return
		}

		// The terminal itself: handles I/O, rendering, input, scrollback,
		// selection, copy/paste, mouse, IME, zoom.
		terminal.View(c, t).Fill().AutoFocus()

		// Floating toolbar on hover.
		// (Rendered after the terminal so it sits on top.)
	})
}

// restartTermPane closes and recreates pane i.
func (a *appState) restartTermPane(i int) {
	key := a.termPaneKey(i)
	if t, ok := a.termPanes[key]; ok && t != nil {
		t.Close()
	}
	delete(a.termPanes, key)
	c := a.term(i) // recreate
	_ = c
}

// killTermPane closes pane i and removes it.
func (a *appState) killTermPane(i int) {
	key := a.termPaneKey(i)
	if t, ok := a.termPanes[key]; ok && t != nil {
		t.Close()
	}
	delete(a.termPanes, key)
}

// newTerminalTabReal adds a terminal tab; its pane's shell starts the first
// time the tab is drawn.
func (a *appState) newTerminalTabReal() {
	shell := os.Getenv("SHELL")
	name := "Terminal"
	if shell != "" {
		if i := strings.LastIndex(shell, "/"); i >= 0 {
			name = shell[i+1:]
		}
	}
	a.tabs = append(a.tabs, topTab{
		Kind:   tabTerminal,
		Label:  name,
		Dot:    dotGreen,
		Path:   randomTermID(),
		Layout: layoutSingle,
	})
	a.activeTab = len(a.tabs) - 1
	a.activeView = tabTerminal
}

func randomTermID() string {
	return "term-" + time.Now().Format("20060102-150405.000000")
}
