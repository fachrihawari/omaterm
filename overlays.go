package main

import (
	"context"
	"sort"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"omaterm/service"
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
	case overlayBranch:
		a.branchQuery = ""
		a.branchSelected = 0
		a.branchList = ui.ListState{}
	case overlayShortcuts:
		a.keyQuery = ""
		a.keySelected = 0
		a.keyList = ui.ListState{}
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
	case overlayBranch:
		a.dialog(c, 520, 480, func() { a.branchBody(c) })
	case overlayShortcuts:
		a.dialog(c, 900, 680, func() { a.keyboardShortcutsBody(c) })
	}
}

// dialogTopFraction is where a dialog's panel starts, as a share of the
// window height. DialogBase centers its panel; editors such as VS Code
// hang the quick open, command palette and search panels near the top
// instead, so that a short panel stays under the eye rather than floating
// in the middle of the window.
const dialogTopFraction = 0.2

// dialogMinBottom keeps a panel off the window's bottom edge.
const dialogMinBottom = 24

// dialogMinSide keeps a panel off the window's side edges.
const dialogMinSide = 24

// dialog shows body in a panel the app styles itself, over a dim backdrop.
// ui.Modal would wrap the content in the theme's own padding and
// background, which read as a dark frame around it.
func (a *appState) dialog(c *ui.Context, w, h float32, body func()) {
	open := true
	winW, winH := c.Size()
	top := winH * dialogTopFraction
	// The panel sizes to its content, but its cap must leave room for the
	// top offset, or a tall panel would hang off the bottom. The width is
	// capped too: the shortcuts list is wider than the minimum window.
	if avail := winH - top - dialogMinBottom; avail > 0 && h > avail {
		h = avail
	}
	if avail := winW - 2*dialogMinSide; avail > 0 && w > avail {
		w = avail
	}
	ui.DialogBase(c, &open, func(backdrop, panel ui.Element) {
		// DialogBase centers the panel along the backdrop's main axis.
		// Starting the column at its top and offsetting the panel keeps
		// the horizontal centering, and puts the panel at a fixed share of
		// the window height whatever its own height is.
		backdrop.Justify(ui.Start)
		panel.Top(top).Background(pal.Popover).Radius(10).Border(1, pal.BorderStrong).
			Width(w).MaxHeight(h).Clip().
			Shadow(0, 12, 40, 0, pal.FloatShadow)
		body()
	})
	if !open {
		a.closeOverlay()
	}
}

// overlayWindowKeys installs only the Escape shortcut at window level;
// arrow/enter handling lives inside each overlay body so it takes
// precedence over the scroll container.
func (a *appState) overlayWindowKeys(c *ui.Context) {
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
	ui.Box(c).Padding(10, 12).BorderWidth(0, 0, 1, 0).BorderColor(pal.Border).Children(func() {
		ui.Row(c).Height(30).Radius(6).Border(1, pal.Border).Background(pal.Input).AlignItems(ui.Center).Padding(0, 10).Gap(8).Children(func() {
			ui.Icon(c, iconSearch).Width(14).Height(14).TextColor(pal.TextMuted)
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
				ui.Text(c, "No matching files").FontSize(12).TextColor(pal.TextMuted)
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

// overlayRow renders one list row: the file's name with the matched bytes
// highlighted, and its directory on the right. Showing only the base name
// on the left keeps the row from repeating the path.
func (a *appState) overlayRow(c *ui.Context, path string, indices []int, selected bool) {
	e := ui.Row(c).Key(path).FillWidth().Height(30).Padding(0, 10).Gap(8).Radius(6).AlignItems(ui.Center).Cursor(ui.CursorPointer)
	if selected {
		e.Background(pal.Hover)
	} else if e.Hovered() {
		e.Background(pal.Hover)
	}
	base := pathBase(path)
	offset := len(path) - len(base)
	e.Children(func() {
		fileBadge(c, base)
		a.highlightedSegment(c, base, offset, indices)
		if dir := pathDir(path); dir != "" {
			ui.Spacer(c)
			ui.Text(c, dir).FontSize(10).TextColor(pal.TextFaint).SingleLine().Shrink(0)
		}
	})
	if e.Clicked() {
		a.openEditor(path)
		a.closeOverlay()
	}
}

// highlightedSegment renders text with the matched byte offsets (which
// index the full path) in the accent color; offset is where text starts.
func (a *appState) highlightedSegment(c *ui.Context, text string, offset int, indices []int) {
	in := map[int]bool{}
	for _, i := range indices {
		if i >= offset && i < offset+len(text) {
			in[i-offset] = true
		}
	}
	if len(in) == 0 {
		ui.Text(c, text).FontSize(12).TextColor(pal.TextSoft).SingleLine().Shrink(0)
		return
	}
	var spans []ui.Span
	var cur strings.Builder
	curOn := false
	flush := func(on bool) {
		if cur.Len() == 0 {
			return
		}
		col := pal.TextSoft
		if on {
			col = pal.Accent
		}
		spans = append(spans, ui.Span{Text: cur.String(), Color: col})
		cur.Reset()
	}
	for i := 0; i < len(text); i++ {
		on := in[i]
		if on != curOn && cur.Len() > 0 {
			flush(curOn)
		}
		curOn = on
		cur.WriteByte(text[i])
	}
	flush(curOn)
	ui.RichText(c, spans...).FontSize(12).SingleLine().Shrink(0)
}

func (a *appState) overlayFooter(c *ui.Context, hints ...string) {
	ui.Row(c).Height(32).AlignItems(ui.Center).Padding(0, 12).Gap(14).Background(pal.Sidebar).BorderWidth(1, 0, 0, 0).BorderColor(pal.Border).Children(func() {
		for _, h := range hints {
			ui.Text(c, h).FontSize(10).TextColor(pal.TextFaint).SingleLine().Shrink(0)
		}
	})
}

// --- Branch picker ---

// openBranchOverlay shows the branch picker, like quick open.
func (a *appState) openBranchOverlay() {
	r := a.activeRepoRef()
	if r == nil {
		return
	}
	if bs, err := service.ListBranches(r.Root); err == nil {
		a.branches = bs
		a.branchesRepo = r.Root
	}
	if a.currentBranch == "" {
		a.currentBranch = r.Branch
	}
	a.openOverlay(overlayBranch)
	a.branchSelected = 0
	for i, b := range a.branches {
		if b == a.currentBranch {
			a.branchSelected = i
			break
		}
	}
	a.branchList = ui.ListState{}
	a.branchList.ScrollIntoView(a.branchSelected)
}

// branchItems filters the branch list by the query.
func (a *appState) branchItems() []service.FuzzyMatch {
	return service.FuzzyFilter(a.branches, a.branchQuery, 100)
}

func (a *appState) branchBody(c *ui.Context) {
	items := a.branchItems()
	if a.branchSelected >= len(items) {
		a.branchSelected = len(items) - 1
	}
	if a.branchSelected < 0 {
		a.branchSelected = 0
	}
	if c.Shortcut(0, ui.KeyDown) {
		a.moveBranchSelection(1, len(items))
	}
	if c.Shortcut(0, ui.KeyUp) {
		a.moveBranchSelection(-1, len(items))
	}

	submit := false
	ui.Box(c).Padding(10, 12).BorderWidth(0, 0, 1, 0).BorderColor(pal.Border).Children(func() {
		ui.Row(c).Height(30).Radius(6).Border(1, pal.Border).Background(pal.Input).AlignItems(ui.Center).Padding(0, 10).Gap(8).Children(func() {
			ui.Icon(c, iconGitBranch).Width(14).Height(14).TextColor(pal.TextMuted)
			ti := ui.TextInputBase(c, &a.branchQuery).Placeholder("Switch branch…").FontSize(13).Grow(1)
			if ti.Changed() {
				a.branchSelected = 0
			}
			if ti.Submitted() {
				submit = true
			}
		})
	})

	a.branchList.Selected = &a.branchSelected
	a.branchList.Key = func(i int) any { return items[i].Value }
	list := ui.List(c, &a.branchList, len(items), func(i int) {
		a.branchRow(c, items[i].Value, i == a.branchSelected)
	}).Grow(1).Padding(6).Children(func() {
		if len(items) == 0 {
			ui.Box(c).Padding(20).Children(func() {
				ui.Text(c, "No matching branches").FontSize(12).TextColor(pal.TextMuted)
			})
		}
	})
	if list.Submitted() || c.Shortcut(0, ui.KeyEnter) {
		submit = true
	}
	if submit {
		a.checkoutBranch(c, items)
	}

	a.overlayFooter(c, "↑↓ navigate", "↵ checkout", "esc close")
}

// moveBranchSelection moves the branch choice by d and keeps it in view.
func (a *appState) moveBranchSelection(d, n int) {
	if n == 0 {
		return
	}
	next := a.branchSelected + d
	if next < 0 {
		next = 0
	}
	if next > n-1 {
		next = n - 1
	}
	if next == a.branchSelected {
		return
	}
	a.branchSelected = next
	a.branchList.ScrollIntoView(next)
}

// checkoutBranch switches to the selected branch and closes the picker.
func (a *appState) checkoutBranch(c *ui.Context, items []service.FuzzyMatch) {
	if a.branchSelected < 0 || a.branchSelected >= len(items) {
		return
	}
	r := a.activeRepoRef()
	if r == nil {
		return
	}
	name := items[a.branchSelected].Value
	if name == a.currentBranch {
		a.closeOverlay()
		return
	}
	out, err := service.Checkout(r.Root, name)
	if err != nil {
		// Keep the picker open so another branch can be tried.
		c.Toast("Checkout failed: " + strings.TrimSpace(out))
		return
	}
	a.currentBranch = name
	a.branches = nil
	a.branchesRepo = ""
	a.closeOverlay()
	a.refreshActiveRepo()
	c.Toast("Switched to " + name)
}

// branchRow renders one branch, marking the current one.
func (a *appState) branchRow(c *ui.Context, name string, selected bool) {
	cur := name == a.currentBranch
	e := ui.Row(c).Key(name).FillWidth().Height(30).Padding(0, 10).Gap(8).Radius(6).AlignItems(ui.Center).Cursor(ui.CursorPointer)
	if selected {
		e.Background(pal.Hover)
	} else if e.Hovered() {
		e.Background(pal.Hover)
	}
	e.Children(func() {
		if cur {
			ui.Icon(c, iconCheck).Width(14).Height(14).TextColor(pal.Ok)
		} else {
			ui.Box(c).Width(14)
		}
		ui.Text(c, name).FontSize(12).TextColor(pal.Text).SingleLine().Grow(1).MinWidth(0)
		if cur {
			pill(c, "current")
		}
	})
	if e.Clicked() {
		a.branchSelected = 0
		a.checkoutBranch(c, []service.FuzzyMatch{{Value: name}})
	}
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
	ui.Box(c).Padding(10, 12).BorderWidth(0, 0, 1, 0).BorderColor(pal.Border).Children(func() {
		ui.Row(c).Height(30).Radius(6).Border(1, pal.Border).Background(pal.Input).AlignItems(ui.Center).Padding(0, 10).Gap(8).Children(func() {
			ui.Icon(c, iconSquareTerminal).Width(14).Height(14).TextColor(pal.TextMuted)
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
		row.Background(pal.Hover)
	} else if row.Hovered() {
		row.Background(pal.Hover)
	}
	row.Children(func() {
		ui.Text(c, e.Category+":").FontSize(10).TextColor(pal.TextFaint).Width(64).SingleLine().Shrink(0)
		titleCol := pal.TextSoft
		if !enabled {
			titleCol = pal.TextFaint
		}
		ui.Text(c, e.Title).FontSize(12).TextColor(titleCol).SingleLine().
			Shrink(1).MinWidth(0).Ellipsis("…")
		ui.Spacer(c)
		if keys := keyPrimary(e.ID); keys != "" {
			keyChip(c, keys)
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
		a.searchPreview = &editor.State{ReadOnly: true, Theme: omaEditorTheme(), Font: ui.Font{Size: a.fontSize}}
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
	ui.Box(c).Padding(10, 12).BorderWidth(0, 0, 1, 0).BorderColor(pal.Border).Children(func() {
		ui.Row(c).Height(34).Radius(6).Border(1, pal.Border).Background(pal.Input).AlignItems(ui.Center).Padding(0, 10).Gap(8).Children(func() {
			ui.Icon(c, iconSearch).Width(15).Height(15).TextColor(pal.TextMuted)
			ti := ui.TextInputBase(c, &a.searchQuery).Placeholder("Search in files (content)").FontSize(13).Grow(1)
			if ti.Changed() {
				a.runSearch()
			}
			if ti.Submitted() {
				submit = true
			}
			ui.Spacer(c)
			ui.Text(c, itoa(len(a.searchResults))+" results").FontSize(10).TextColor(pal.TextFaint).Shrink(0)
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
				ui.Text(c, msg).FontSize(12).TextColor(pal.TextMuted)
			})
		}
	})
	return list.Submitted()
}

func (a *appState) searchResultRow(c *ui.Context, m service.Match, selected bool) {
	e := ui.Row(c).Key(m.Path+":"+itoa(m.Line)+":"+itoa(m.Start)).FillWidth().Padding(6, 10).Gap(8).Radius(6).AlignItems(ui.Center).Cursor(ui.CursorPointer)
	if selected {
		e.Background(pal.Hover)
	} else if e.Hovered() {
		e.Background(pal.Hover)
	}
	e.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				fileBadge(c, pathBase(m.Path))
				ui.Text(c, m.Path).FontSize(11).TextColor(pal.TextSoft).SingleLine().Shrink(0)
				ui.Text(c, ":"+itoa(m.Line)).FontSize(10).TextColor(pal.TextFaint).SingleLine().Shrink(0)
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
					ui.Span{Text: before, Color: pal.TextMuted},
					ui.Span{Text: match, Color: pal.Warn},
					ui.Span{Text: after, Color: pal.TextMuted},
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
	ui.Column(c).Fill().BorderWidth(0, 0, 0, 1).BorderColor(pal.Border).Children(func() {
		if a.searchPreview == nil {
			ui.Column(c).Fill().Center().Children(func() {
				ui.Text(c, "Select a result to preview").FontSize(12).TextColor(pal.TextMuted)
			})
			return
		}
		editor.View(c, a.searchPreview).Fill()
	})
}

// --- Keyboard shortcuts ---

// keymapEntry is one command in the shortcuts overlay: its keys and when
// they apply.
type keymapEntry struct {
	Command
	Keys  []keyBinding
	Scope keyScope
}

// keymapItems lists the commands that have keys, filtered by the query.
// The query matches the title, the category, the scope and the keys, so
// "ctrl+shift+d" and "split" both find the split commands.
//
// Commands of one family (Command.Group) fold into a single entry, whose
// keys are the family's in order: Alt+1 … Alt+9 is one row, not nine.
func (a *appState) keymapItems() []keymapEntry {
	q := strings.ToLower(strings.TrimSpace(a.keyQuery))
	out := make([]keymapEntry, 0, len(commandRegistry))
	grouped := map[string]int{}
	for _, cmd := range commandRegistry {
		bs := bindingsFor(cmd.ID)
		if len(bs) == 0 {
			continue
		}
		if cmd.Group != "" {
			if i, ok := grouped[cmd.Group]; ok {
				out[i].Keys = append(out[i].Keys, bs...)
				continue
			}
		}
		if q != "" {
			labels := make([]string, len(bs))
			for i, b := range bs {
				labels[i] = b.Combo.Label()
			}
			hay := strings.ToLower(cmd.Category + " " + cmd.Title + " " +
				bs[0].Scope.String() + " " + strings.Join(labels, " "))
			if !strings.Contains(hay, q) {
				continue
			}
		}
		if cmd.Group != "" {
			grouped[cmd.Group] = len(out)
			cmd.Title = cmd.Group
		}
		out = append(out, keymapEntry{Command: cmd, Keys: bs, Scope: bs[0].Scope})
	}
	// Group by category, then by name, so related commands sit together as
	// they do in VS Code's list. The table's order is for reading the
	// source, not for the dialog.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Title < out[j].Title
	})
	return out
}

func (a *appState) keyboardShortcutsBody(c *ui.Context) {
	items := a.keymapItems()
	if a.keySelected >= len(items) {
		a.keySelected = len(items) - 1
	}
	if a.keySelected < 0 {
		a.keySelected = 0
	}
	if c.Shortcut(0, ui.KeyDown) {
		a.moveKeymapSelection(1, len(items))
	}
	if c.Shortcut(0, ui.KeyUp) {
		a.moveKeymapSelection(-1, len(items))
	}

	submit := false
	ui.Row(c).Height(40).AlignItems(ui.Center).Padding(0, 16).BorderWidth(0, 0, 1, 0).BorderColor(pal.Border).Children(func() {
		ui.Text(c, "Keyboard Shortcuts").FontSize(13).Bold().TextColor(pal.Text)
		ui.Spacer(c)
		ui.Text(c, keyLabels("help.keyboardShortcuts")).FontSize(10).TextColor(pal.TextFaint)
	})
	ui.Box(c).Padding(10, 12).BorderWidth(0, 0, 1, 0).BorderColor(pal.Border).Children(func() {
		ui.Row(c).Height(30).Radius(6).Border(1, pal.Border).Background(pal.Input).AlignItems(ui.Center).Padding(0, 10).Gap(8).Children(func() {
			ui.Icon(c, iconSearch).Width(14).Height(14).TextColor(pal.TextMuted)
			ti := ui.TextInputBase(c, &a.keyQuery).Placeholder("Type a command or a key").FontSize(13).Grow(1)
			if ti.Changed() {
				a.keySelected = 0
			}
			if ti.Submitted() {
				submit = true
			}
			ui.Spacer(c)
			ui.Text(c, itoa(len(items))+" commands").FontSize(10).TextColor(pal.TextFaint).Shrink(0)
		})
	})

	// Column headings, so the When and Key columns read as columns.
	ui.Row(c).Height(22).AlignItems(ui.Center).Padding(0, 16).Gap(10).BorderWidth(0, 0, 1, 0).BorderColor(pal.Border).Children(func() {
		ui.Text(c, "COMMAND").FontSize(9).TextColor(pal.TextMuted).LetterSpacing(1.2).Shrink(0)
		ui.Spacer(c)
		ui.Text(c, "WHEN").FontSize(9).TextColor(pal.TextMuted).LetterSpacing(1.2).Width(78).Shrink(0)
		// The keys are right-aligned in their column, so its heading is too.
		ui.Row(c).Width(220).Justify(ui.End).Shrink(0).Children(func() {
			ui.Text(c, "KEY").FontSize(9).TextColor(pal.TextMuted).LetterSpacing(1.2).Shrink(0)
		})
	})

	a.keyList.Selected = &a.keySelected
	a.keyList.Key = func(i int) any { return items[i].ID }
	list := ui.List(c, &a.keyList, len(items), func(i int) {
		a.keymapRow(c, items[i], i == a.keySelected)
	}).Grow(1).Padding(6).Children(func() {
		if len(items) == 0 {
			ui.Box(c).Padding(20).Children(func() {
				ui.Text(c, "No matching commands").FontSize(12).TextColor(pal.TextMuted)
			})
		}
	})
	if list.Submitted() || c.Shortcut(0, ui.KeyEnter) {
		submit = true
	}
	if submit {
		a.runKeymapSelection(c, items)
	}

	a.overlayFooter(c, "type to filter", "↑↓ navigate", "↵ run", "esc close")
}

// moveKeymapSelection moves the shortcuts choice by d and keeps it in view.
func (a *appState) moveKeymapSelection(d, n int) {
	if n == 0 {
		return
	}
	next := a.keySelected + d
	if next < 0 {
		next = 0
	}
	if next > n-1 {
		next = n - 1
	}
	if next == a.keySelected {
		return
	}
	a.keySelected = next
	a.keyList.ScrollIntoView(next)
}

// runKeymapSelection closes the overlay and runs the chosen command, as
// choosing it in the palette would.
func (a *appState) runKeymapSelection(c *ui.Context, items []keymapEntry) {
	if a.keySelected < 0 || a.keySelected >= len(items) {
		return
	}
	cmd := items[a.keySelected]
	if cmd.Enabled != nil && !cmd.Enabled(a) {
		return
	}
	a.closeOverlay()
	cmd.Run(a, c)
}

// keymapRow renders one command with its keys and scope.
func (a *appState) keymapRow(c *ui.Context, e keymapEntry, selected bool) {
	enabled := e.Enabled == nil || e.Enabled(a)
	row := ui.Row(c).Key(e.ID).FillWidth().Height(34).Padding(0, 10).Gap(10).
		Radius(6).AlignItems(ui.Center).Cursor(ui.CursorPointer)
	if selected {
		row.Background(pal.Hover)
	} else if row.Hovered() {
		row.Background(pal.Hover)
	}
	row.Children(func() {
		titleCol := pal.TextSoft
		if !enabled {
			titleCol = pal.TextFaint
		}
		ui.Text(c, e.Category+":").FontSize(10).TextColor(pal.TextFaint).Width(64).SingleLine().Shrink(0)
		ui.Text(c, e.Title).FontSize(12).TextColor(titleCol).SingleLine().
			Shrink(1).MinWidth(0).Ellipsis("…")
		ui.Spacer(c)
		ui.Text(c, e.Scope.String()).FontSize(9).TextColor(pal.TextFaint).Width(78).
			SingleLine().Shrink(0)
		ui.Row(c).Width(220).Justify(ui.End).Gap(4).Children(func() {
			// A family as long as the digits shows its range: Alt+1 … Alt+9
			// reads faster than nine chips, and says the same thing.
			if e.Group != "" && len(e.Keys) > 2 {
				keyChip(c, e.Keys[0].Combo.Label()+" … "+e.Keys[len(e.Keys)-1].Combo.Label())
				return
			}
			for _, b := range e.Keys {
				keyChip(c, b.Combo.Label())
			}
		})
	})
	if row.Clicked() && enabled {
		a.closeOverlay()
		e.Run(a, c)
	}
}
