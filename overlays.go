package main

import (
	"context"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"oterm/service"
)

// Overlays: quick open (Ctrl+P), command palette (Ctrl+Shift+P) and the
// telescope-style global search (Ctrl+Shift+F), all built on ui.Modal.

func (a *appState) openOverlay(k overlayKind) {
	a.overlay = k
	switch k {
	case overlayQuickOpen:
		a.quickQuery = ""
		a.quickSelected = 0
		if a.fileIndex == nil {
			a.rebuildFileIndex()
		}
	case overlayCommandPalette:
		a.cmdQuery = ""
		a.cmdSelected = 0
	case overlayGlobalSearch:
		a.searchSelected = 0
		a.searchResults = nil
		a.searchPreview = nil
	}
}

func (a *appState) closeOverlay() {
	a.overlay = overlayNone
	a.searchJob.Cancel()
}

// renderOverlays draws whichever overlay is open, over the dimmed window.
func (a *appState) renderOverlays(c *ui.Context) {
	switch a.overlay {
	case overlayQuickOpen:
		a.dialog(c, 680, 560, func() { a.quickOpenBody(c) })
	case overlayCommandPalette:
		a.dialog(c, 680, 560, func() { a.commandPaletteBody(c) })
	case overlayGlobalSearch:
		a.dialog(c, 1100, 700, func() { a.globalSearchBody(c) })
	}
}

// dialog shows body in a panel the app styles itself, over a dim backdrop.
// ui.Modal would wrap the content in the theme's own padding and
// background, which read as a dark frame around it.
func (a *appState) dialog(c *ui.Context, w, h float32, body func()) {
	open := true
	ui.DialogBase(c, &open, func(_, panel ui.Element) {
		panel.Background(colPanel).Radius(10).Border(1, colBorder2).
			Width(w).MaxHeight(h).Clip().
			Shadow(0, 12, 40, 0, ui.RGBA(0, 0, 0, 0.45))
		body()
	})
	if !open {
		a.closeOverlay()
	}
}

// overlayShortcuts installs only the Escape shortcut at window level;
// arrow/enter handling lives inside each overlay body so it takes
// precedence over the scroll container.
func (a *appState) overlayShortcuts(c *ui.Context) {
	if a.overlay == overlayNone {
		return
	}
	c.OnShortcut(0, ui.KeyEscape, func() { a.closeOverlay() })
}

// --- Quick open ---

func (a *appState) quickOpenItems() []service.FuzzyMatch {
	items := a.fileIndex
	if a.quickQuery == "" {
		// Recent documents first, then everything else.
		var recents []string
		if a.win != nil {
			prefix := a.workspace + "/"
			for _, p := range mygo.App.RecentDocuments() {
				if strings.HasPrefix(p, prefix) {
					recents = append(recents, strings.TrimPrefix(p, prefix))
				}
			}
		}
		if len(recents) > 0 {
			out := make([]service.FuzzyMatch, 0, len(recents))
			for _, r := range recents {
				out = append(out, service.FuzzyMatch{Value: r, Score: 1e9})
			}
			rest := service.FuzzyFilter(items, "", 200)
			out = append(out, rest...)
			return out
		}
	}
	return service.FuzzyFilter(items, a.quickQuery, 200)
}

func (a *appState) quickOpenBody(c *ui.Context) {
	items := a.quickOpenItems()
	if a.quickSelected >= len(items) {
		a.quickSelected = len(items) - 1
	}
	if a.quickSelected < 0 {
		a.quickSelected = 0
	}
	// Shortcuts as queries, not registrations: they fire wherever the
	// focus is, the input keeping it by default.
	if c.Shortcut(0, ui.KeyDown) {
		a.moveQuickSelection(1, len(items))
	}
	if c.Shortcut(0, ui.KeyUp) {
		a.moveQuickSelection(-1, len(items))
	}

	submit := false
	ui.Box(c).Padding(10, 12).BorderWidth(0, 0, 1, 0).BorderColor(colBorder).Children(func() {
		ui.Row(c).Height(30).Radius(6).Border(1, colBorder).Background(colInput).AlignItems(ui.Center).Padding(0, 10).Gap(8).Children(func() {
			ui.Icon(c, iconSearch).Width(14).Height(14).TextColor(colMuted)
			ti := ui.TextInputBase(c, &a.quickQuery).Placeholder("Search files by name").FontSize(13).Grow(1)
			if ti.Changed() {
				a.quickSelected = 0
			}
			// The focused input claims Enter, so read it here as well.
			if ti.Submitted() {
				submit = true
			}
		})
	})

	// The list keeps the chosen row in view as the arrows move it.
	a.quickList.Selected = &a.quickSelected
	a.quickList.Key = func(i int) any { return items[i].Value }
	list := ui.List(c, &a.quickList, len(items), func(i int) {
		a.overlayRow(c, items[i].Value, items[i].Indices, i == a.quickSelected)
	}).Grow(1).Padding(6).Children(func() {
		if len(items) == 0 {
			ui.Box(c).Padding(20).Children(func() {
				ui.Text(c, "No matching files").FontSize(12).TextColor(colMuted)
			})
		}
	})
	if list.Submitted() || c.Shortcut(0, ui.KeyEnter) {
		submit = true
	}
	if submit {
		a.openQuickSelection(items)
	}

	a.overlayFooter(c, "↑↓ navigate", "↵ open", "esc close")
}

// moveQuickSelection moves the quick open choice by d and keeps it in view.
func (a *appState) moveQuickSelection(d, n int) {
	if n == 0 {
		return
	}
	next := a.quickSelected + d
	if next < 0 {
		next = 0
	}
	if next > n-1 {
		next = n - 1
	}
	if next == a.quickSelected {
		return
	}
	a.quickSelected = next
	a.quickList.ScrollIntoView(next)
}

func (a *appState) openQuickSelection(items []service.FuzzyMatch) {
	if a.quickSelected < 0 || a.quickSelected >= len(items) {
		return
	}
	a.openEditor(items[a.quickSelected].Value)
	a.closeOverlay()
}

// overlayRow renders one list row with the matched bytes highlighted.
func (a *appState) overlayRow(c *ui.Context, path string, indices []int, selected bool) {
	e := ui.Row(c).Key(path).FillWidth().Height(30).Padding(0, 10).Gap(8).Radius(6).AlignItems(ui.Center).Cursor(ui.CursorPointer)
	if selected {
		e.Background(colHover)
	} else if e.Hovered() {
		e.Background(ui.Hex("#171d25"))
	}
	e.Children(func() {
		fileBadge(c, pathBase(path))
		a.highlightedPath(c, path, indices)
		if dir := pathDir(path); dir != "" {
			ui.Spacer(c)
			ui.Text(c, dir).FontSize(10).TextColor(colMuted2).SingleLine().Shrink(0)
		}
	})
	if e.Clicked() {
		a.openEditor(path)
		a.closeOverlay()
	}
}

// highlightedPath renders path with the matched bytes in the accent color.
func (a *appState) highlightedPath(c *ui.Context, path string, indices []int) {
	if len(indices) == 0 {
		ui.Text(c, path).FontSize(12).TextColor(colText2).SingleLine().Shrink(0)
		return
	}
	in := map[int]bool{}
	for _, i := range indices {
		in[i] = true
	}
	var spans []ui.Span
	var cur strings.Builder
	curOn := false
	flush := func(on bool) {
		if cur.Len() == 0 {
			return
		}
		col := colText2
		if on {
			col = colBlue
		}
		spans = append(spans, ui.Span{Text: cur.String(), Color: col})
		cur.Reset()
	}
	for i := 0; i < len(path); i++ {
		on := in[i]
		if on != curOn && cur.Len() > 0 {
			flush(curOn)
		}
		curOn = on
		cur.WriteByte(path[i])
	}
	flush(curOn)
	ui.RichText(c, spans...).FontSize(12).SingleLine().Shrink(0)
}

func (a *appState) overlayFooter(c *ui.Context, hints ...string) {
	ui.Row(c).Height(32).AlignItems(ui.Center).Padding(0, 12).Gap(14).BorderWidth(1, 0, 0, 0).BorderColor(colBorder).Children(func() {
		for _, h := range hints {
			ui.Text(c, h).FontSize(10).TextColor(colMuted2).SingleLine().Shrink(0)
		}
	})
}

// --- Command palette ---

type paletteEntry struct {
	Command
	Score   float64
	Indices []int
}

func (a *appState) paletteCommands() []paletteEntry {
	titles := make([]string, len(commandRegistry))
	for i, cmd := range commandRegistry {
		titles[i] = cmd.Category + ": " + cmd.Title
	}
	matches := service.FuzzyFilter(titles, a.cmdQuery, 100)
	out := make([]paletteEntry, 0, len(matches))
	for _, m := range matches {
		// Find the command by title index.
		for i, cmd := range commandRegistry {
			if titles[i] == m.Value {
				out = append(out, paletteEntry{Command: cmd, Score: m.Score, Indices: m.Indices})
				break
			}
		}
	}
	return out
}

func (a *appState) commandPaletteBody(c *ui.Context) {
	cmds := a.paletteCommands()
	if a.cmdSelected >= len(cmds) {
		a.cmdSelected = len(cmds) - 1
	}
	if a.cmdSelected < 0 {
		a.cmdSelected = 0
	}
	if c.Shortcut(0, ui.KeyDown) {
		a.movePaletteSelection(1, len(cmds))
	}
	if c.Shortcut(0, ui.KeyUp) {
		a.movePaletteSelection(-1, len(cmds))
	}

	submit := false
	ui.Box(c).Padding(10, 12).BorderWidth(0, 0, 1, 0).BorderColor(colBorder).Children(func() {
		ui.Row(c).Height(30).Radius(6).Border(1, colBorder).Background(colInput).AlignItems(ui.Center).Padding(0, 10).Gap(8).Children(func() {
			ui.Icon(c, iconSquareTerminal).Width(14).Height(14).TextColor(colMuted)
			ti := ui.TextInputBase(c, &a.cmdQuery).Placeholder("Type a command").FontSize(13).Grow(1)
			if ti.Changed() {
				a.cmdSelected = 0
			}
			if ti.Submitted() {
				submit = true
			}
		})
	})

	a.cmdList.Selected = &a.cmdSelected
	a.cmdList.Key = func(i int) any { return cmds[i].ID }
	list := ui.List(c, &a.cmdList, len(cmds), func(i int) {
		a.commandRow(c, cmds[i], i == a.cmdSelected)
	}).Grow(1).Padding(6)
	if list.Submitted() || c.Shortcut(0, ui.KeyEnter) {
		submit = true
	}
	if submit {
		a.runPaletteSelection(c, cmds)
	}

	a.overlayFooter(c, "↑↓ navigate", "↵ run", "esc close")
}

// movePaletteSelection moves the palette choice by d and keeps it in view.
func (a *appState) movePaletteSelection(d, n int) {
	if n == 0 {
		return
	}
	next := a.cmdSelected + d
	if next < 0 {
		next = 0
	}
	if next > n-1 {
		next = n - 1
	}
	if next == a.cmdSelected {
		return
	}
	a.cmdSelected = next
	a.cmdList.ScrollIntoView(next)
}

func (a *appState) runPaletteSelection(c *ui.Context, cmds []paletteEntry) {
	if a.cmdSelected < 0 || a.cmdSelected >= len(cmds) {
		return
	}
	cmd := cmds[a.cmdSelected]
	if cmd.Enabled != nil && !cmd.Enabled(a) {
		return
	}
	a.closeOverlay()
	cmd.Run(a, c)
}

func (a *appState) commandRow(c *ui.Context, e paletteEntry, selected bool) {
	enabled := e.Enabled == nil || e.Enabled(a)
	row := ui.Row(c).Key(e.ID).FillWidth().Height(34).Padding(0, 10).Gap(10).Radius(6).AlignItems(ui.Center).Cursor(ui.CursorPointer)
	if selected {
		row.Background(colHover)
	} else if row.Hovered() {
		row.Background(ui.Hex("#171d25"))
	}
	row.Children(func() {
		ui.Text(c, e.Category+":").FontSize(10).TextColor(colMuted2).Width(64).SingleLine().Shrink(0)
		titleCol := colText2
		if !enabled {
			titleCol = colMuted2
		}
		ui.Text(c, e.Title).FontSize(12).TextColor(titleCol).SingleLine().Shrink(0)
		ui.Spacer(c)
		if e.Keys != "" {
			ui.Row(c).Radius(4).Border(1, ui.Hex("#313b48")).Background(ui.Hex("#161b22")).Padding(2, 6).Shrink(0).Children(func() {
				ui.Text(c, e.Keys).FontSize(9).TextColor(colMuted).SingleLine()
			})
		}
	})
	if row.Clicked() && enabled {
		a.closeOverlay()
		e.Run(a, c)
	}
}

// --- Global search ---

func (a *appState) runSearch() {
	query := a.searchQuery
	if query == "" || a.workspace == "" || a.win == nil {
		a.searchResults = nil
		return
	}
	win := a.win
	a.searchJob.Run(func(ctx context.Context) error {
		matches, err := service.SearchContent(ctx, a.workspace, query)
		if err != nil {
			return err
		}
		win.Update(func() {
			a.searchResults = matches
			if a.searchSelected >= len(matches) {
				a.searchSelected = 0
			}
			a.updateSearchPreview()
		})
		return nil
	}, nil)
}

// updateSearchPreview loads the selected match's file window into the
// read-only preview editor and scrolls to the match line.
func (a *appState) updateSearchPreview() {
	if a.searchSelected < 0 || a.searchSelected >= len(a.searchResults) {
		a.searchPreview = nil
		return
	}
	m := a.searchResults[a.searchSelected]
	lines, first, err := service.ReadFileWindow(a.workspace, m.Path, m.Line, 400)
	if err != nil {
		a.searchPreview = nil
		return
	}
	text := strings.Join(lines, "\n")
	if a.searchPreview == nil {
		a.searchPreview = &editor.State{ReadOnly: true}
	}
	if a.searchPreview.Buffer == nil {
		a.searchPreview.Buffer = editor.NewBuffer(text)
	} else {
		a.searchPreview.Buffer.Reset(text)
	}
	if lang := editor.LanguageFor(m.Path); lang != nil {
		a.searchPreview.Buffer.SetLanguage(lang)
	}
	a.searchPreview.GoTo(editor.Pos{Line: m.Line - first, Col: 0})
	// Select the matched range on the match line so it shows highlighted.
	start := a.searchPreview.Buffer.Offset(editor.Pos{Line: m.Line - first, Col: m.Start})
	end := a.searchPreview.Buffer.Offset(editor.Pos{Line: m.Line - first, Col: m.End})
	a.searchPreview.Select(start, end)
}

func (a *appState) globalSearchBody(c *ui.Context) {
	if a.searchSelected >= len(a.searchResults) {
		a.searchSelected = len(a.searchResults) - 1
	}
	if a.searchSelected < 0 {
		a.searchSelected = 0
	}
	if c.Shortcut(0, ui.KeyDown) {
		a.moveSearchSelection(1)
	}
	if c.Shortcut(0, ui.KeyUp) {
		a.moveSearchSelection(-1)
	}

	submit := false
	// Search input.
	ui.Box(c).Padding(10, 12).BorderWidth(0, 0, 1, 0).BorderColor(colBorder).Children(func() {
		ui.Row(c).Height(34).Radius(6).Border(1, colBorder).Background(colInput).AlignItems(ui.Center).Padding(0, 10).Gap(8).Children(func() {
			ui.Icon(c, iconSearch).Width(15).Height(15).TextColor(colMuted)
			ti := ui.TextInputBase(c, &a.searchQuery).Placeholder("Search in files (content)").FontSize(13).Grow(1)
			if ti.Changed() {
				a.runSearch()
			}
			if ti.Submitted() {
				submit = true
			}
			ui.Spacer(c)
			ui.Text(c, itoa(len(a.searchResults))+" results").FontSize(10).TextColor(colMuted2).Shrink(0)
		})
	})

	// Split: results left, preview right.
	ui.SplitPercent(c, &a.searchSplit, func() {
		if a.searchResultsPane(c) {
			submit = true
		}
	}, func() {
		a.searchPreviewPane(c)
	}).Grow(1)

	if c.Shortcut(0, ui.KeyEnter) {
		submit = true
	}
	if submit {
		a.openSearchSelection()
	}

	a.overlayFooter(c, "↑↓ navigate", "↵ open at line", "esc close")
}

// moveSearchSelection moves the search choice by d and keeps it in view,
// loading its preview.
func (a *appState) moveSearchSelection(d int) {
	n := len(a.searchResults)
	if n == 0 {
		return
	}
	next := a.searchSelected + d
	if next < 0 {
		next = 0
	}
	if next > n-1 {
		next = n - 1
	}
	if next == a.searchSelected {
		return
	}
	a.searchSelected = next
	a.searchList.ScrollIntoView(next)
	a.updateSearchPreview()
}

func (a *appState) openSearchSelection() {
	if a.searchSelected < 0 || a.searchSelected >= len(a.searchResults) {
		return
	}
	m := a.searchResults[a.searchSelected]
	a.openEditorAt(m.Path, m.Line)
	a.closeOverlay()
}

// searchResultsPane builds the results list, reporting whether the user
// submitted it (Enter or a double click on a row).
func (a *appState) searchResultsPane(c *ui.Context) bool {
	a.searchList.Selected = &a.searchSelected
	a.searchList.Key = func(i int) any {
		m := a.searchResults[i]
		return m.Path + ":" + itoa(m.Line) + ":" + itoa(m.Start)
	}
	list := ui.List(c, &a.searchList, len(a.searchResults), func(i int) {
		a.searchResultRow(c, a.searchResults[i], i == a.searchSelected)
	}).Fill().Padding(6).Children(func() {
		if len(a.searchResults) == 0 {
			ui.Box(c).Padding(20).Children(func() {
				msg := "Type to search file contents"
				if a.searchQuery != "" {
					msg = "No results"
				}
				ui.Text(c, msg).FontSize(12).TextColor(colMuted)
			})
		}
	})
	return list.Submitted()
}

func (a *appState) searchResultRow(c *ui.Context, m service.Match, selected bool) {
	e := ui.Row(c).Key(m.Path+":"+itoa(m.Line)+":"+itoa(m.Start)).FillWidth().Padding(6, 10).Gap(8).Radius(6).AlignItems(ui.Center).Cursor(ui.CursorPointer)
	if selected {
		e.Background(colHover)
	} else if e.Hovered() {
		e.Background(ui.Hex("#171d25"))
	}
	e.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				fileBadge(c, pathBase(m.Path))
				ui.Text(c, m.Path).FontSize(11).TextColor(colText2).SingleLine().Shrink(0)
				ui.Text(c, ":"+itoa(m.Line)).FontSize(10).TextColor(colMuted2).SingleLine().Shrink(0)
			})
			// Line text with the match highlighted.
			ui.Row(c).ClipX().Children(func() {
				before := m.Text
				match := ""
				after := ""
				if m.Start >= 0 && m.End <= len(m.Text) && m.Start < m.End {
					before = m.Text[:m.Start]
					match = m.Text[m.Start:m.End]
					after = m.Text[m.End:]
				}
				ui.RichText(c,
					ui.Span{Text: before, Color: colMuted},
					ui.Span{Text: match, Color: colYellow},
					ui.Span{Text: after, Color: colMuted},
				).Font(monoFont).FontSize(11).SingleLine()
			})
		})
	})
	if e.Clicked() {
		a.openEditorAt(m.Path, m.Line)
		a.closeOverlay()
	}
}

func (a *appState) searchPreviewPane(c *ui.Context) {
	ui.Column(c).Fill().BorderWidth(0, 0, 0, 1).BorderColor(colBorder).Children(func() {
		if a.searchPreview == nil {
			ui.Column(c).Fill().Center().Children(func() {
				ui.Text(c, "Select a result to preview").FontSize(12).TextColor(colMuted)
			})
			return
		}
		editor.View(c, a.searchPreview).Fill()
	})
}
