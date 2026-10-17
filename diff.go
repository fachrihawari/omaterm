package main

import (
	"github.com/egoist/mygo/ui"
)

// The diff view: INDEX ↔ WORKING TREE for GitPanel.tsx, split or inline,
// with stage/discard actions in its toolbar.

// The split diff's two sides, transcribed from the mock: the left column
// (INDEX) and the right (WORKING TREE) each show their own lines.

type sideLine struct {
	num   int
	del   bool
	add   bool
	spans []ui.Span
}

var diffLeft = []sideLine{
	{1, false, false, []ui.Span{{Text: "type", Color: synKeyword}, {Text: " "}, {Text: "Props", Color: synType}, {Text: " = "}, {Text: "{", Color: synBrace}}},
	{2, false, false, []ui.Span{{Text: "  "}, {Text: "repo", Color: synVar}, {Text: ": "}, {Text: "Repository", Color: synType}, {Text: ";"}}},
	{3, false, false, []ui.Span{{Text: "}", Color: synBrace}, {Text: ";"}}},
	{4, false, false, nil},
	{5, false, false, []ui.Span{{Text: "export function", Color: synBool}, {Text: " "}, {Text: "GitPanel", Color: synFunc}, {Text: "(", Color: synPunct}, {Text: "props", Color: synVar}, {Text: ": "}, {Text: "Props", Color: synType}, {Text: ") "}, {Text: "{", Color: synBrace}}},
	{6, true, false, []ui.Span{{Text: "  "}, {Text: "return", Color: synKeyword}, {Text: " <", Color: synPunct}, {Text: "div", Color: synType}, {Text: ">", Color: synPunct}, {Text: "{props.repo.name}", Color: synVar}, {Text: "</", Color: synPunct}, {Text: "div", Color: synType}, {Text: ">", Color: synPunct}, {Text: ";"}}},
	{7, false, false, []ui.Span{{Text: "}", Color: synBrace}}},
}

var diffRight = []sideLine{
	{1, false, false, []ui.Span{{Text: "type", Color: synKeyword}, {Text: " "}, {Text: "Props", Color: synType}, {Text: " = "}, {Text: "{", Color: synBrace}}},
	{2, false, false, []ui.Span{{Text: "  "}, {Text: "repo", Color: synVar}, {Text: ": "}, {Text: "Repository", Color: synType}, {Text: ";"}}},
	{3, false, true, []ui.Span{{Text: "  "}, {Text: "compact", Color: synVar}, {Text: "?: "}, {Text: "boolean", Color: synBool}, {Text: ";"}}},
	{4, false, false, []ui.Span{{Text: "}", Color: synBrace}, {Text: ";"}}},
	{5, false, false, nil},
	{6, false, false, []ui.Span{{Text: "export function", Color: synBool}, {Text: " "}, {Text: "GitPanel", Color: synFunc}, {Text: "(", Color: synPunct}, {Text: "props", Color: synVar}, {Text: ": "}, {Text: "Props", Color: synType}, {Text: ") "}, {Text: "{", Color: synBrace}}},
	{7, false, true, []ui.Span{{Text: "  "}, {Text: "const", Color: synKeyword}, {Text: " "}, {Text: "changes", Color: synVar}, {Text: " = "}, {Text: "useGitChanges", Color: synFunc}, {Text: "(props.repo.path);"}}},
	{8, false, true, []ui.Span{{Text: "  "}, {Text: "return", Color: synKeyword}, {Text: " <", Color: synPunct}, {Text: "ChangeList", Color: synType}, {Text: " items", Color: synVar}, {Text: "={", Color: synPunct}, {Text: "changes", Color: synVar}, {Text: "} />", Color: synPunct}, {Text: ";"}}},
	{9, false, false, []ui.Span{{Text: "}", Color: synBrace}}},
}

// The inline diff's unified rows: old and new line numbers.
type inlineLine struct {
	oldNo int
	newNo int
	del   bool
	add   bool
	spans []ui.Span
}

var diffInline = []inlineLine{
	{1, 1, false, false, diffLeft[0].spans},
	{2, 2, false, false, diffLeft[1].spans},
	{0, 3, false, true, diffRight[2].spans},
	{3, 4, false, false, diffLeft[2].spans},
	{4, 5, false, false, nil},
	{5, 6, false, false, diffLeft[4].spans},
	{6, 0, true, false, diffLeft[5].spans},
	{0, 7, false, true, diffRight[6].spans},
	{0, 8, false, true, diffRight[7].spans},
	{7, 9, false, false, diffLeft[6].spans},
}

func (a *appState) diffView(c *ui.Context) ui.Element {
	return ui.Column(c).Fill().Background(colCodeBg).Children(func() {
		// Toolbar.
		ui.Row(c).Height(40).AlignItems(ui.Center).Padding(0, 12).Gap(8).Background(colPanel).BorderWidth(0, 0, 1, 0).BorderColor(colBorder).Children(func() {
			ui.Icon(c, iconGitCompare).Width(16).Height(16).TextColor(colGreen)
			ui.Text(c, "GitPanel.tsx").FontSize(12)
			scope := "Working Tree · INDEX ↔ WORKING TREE"
			if a.diffStaged {
				scope = "Staged · HEAD ↔ INDEX"
			}
			ui.Text(c, scope).FontSize(10).TextColor(colMuted)

			ui.Spacer(c)

			stage := ui.ButtonBase(c).Radius(6).Padding(4, 10).Background(ui.Hex("#222b36")).Children(func() {
				label := "Stage File"
				if a.diffStaged {
					label = "Unstage File"
				}
				ui.Text(c, label).FontSize(10).TextColor(colText2)
			})
			if stage.Clicked() {
				a.diffStaged = !a.diffStaged
				if a.diffStaged {
					c.Toast("File staged")
				} else {
					c.Toast("File moved to unstaged changes")
				}
			}

			if !a.diffStaged {
				discard := ui.ButtonBase(c).Radius(6).Padding(4, 10).Children(func() {
					ui.Text(c, "Discard").FontSize(10).TextColor(colRed)
				})
				if discard.Clicked() {
					c.Toast("Discarded working tree changes")
				}
			}

			ui.Box(c).Width(1).Height(16).Background(colBorder).Margin(0, 4)

			split := ui.ButtonBase(c).Radius(6).Padding(4, 10).Children(func() {
				t := ui.Text(c, "Split").FontSize(10)
				if a.diffSplit {
					t.TextColor(ui.Hex("#ffffff"))
				} else {
					t.TextColor(colMuted)
				}
			})
			if a.diffSplit {
				split.Background(ui.Hex("#222b36"))
			}
			if split.Clicked() {
				a.diffSplit = true
			}

			inline := ui.ButtonBase(c).Radius(6).Padding(4, 10).Children(func() {
				t := ui.Text(c, "Inline").FontSize(10)
				if !a.diffSplit {
					t.TextColor(ui.Hex("#ffffff"))
				} else {
					t.TextColor(colMuted)
				}
			})
			if !a.diffSplit {
				inline.Background(ui.Hex("#222b36"))
			}
			if inline.Clicked() {
				a.diffSplit = false
			}
		})

		if a.diffSplit {
			a.splitDiff(c)
		} else {
			a.inlineDiff(c)
		}
	})
}

func (a *appState) splitDiff(c *ui.Context) ui.Element {
	return ui.SplitPercent(c, &a.diffSplitPct, func() {
		// Left: INDEX.
		ui.Column(c).Fill().BorderWidth(0, 1, 0, 0).BorderColor(colBorder).Children(func() {
			ui.Box(c).Height(28).Padding(0, 12).BorderWidth(0, 0, 1, 0).BorderColor(colBorder).Background(colDiffTop).Children(func() {
				ui.Column(c).Fill().Children(func() {
					ui.Text(c, "INDEX · GitPanel.tsx").FontSize(10).TextColor(colMuted).Padding(9, 0).SingleLine()
				})
			})
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Column(c).Padding(8, 0).Children(func() {
					for _, ln := range diffLeft {
						diffRowKind(c, ln.num, ln.del, false, ln.spans)
					}
				})
			})
		})
	}, func() {
		// Right: WORKING TREE.
		ui.Column(c).Fill().Children(func() {
			ui.Box(c).Height(28).Padding(0, 12).BorderWidth(0, 0, 1, 0).BorderColor(colBorder).Background(colDiffTop).Children(func() {
				ui.Text(c, "WORKING TREE · GitPanel.tsx").FontSize(10).TextColor(colMuted).Padding(9, 0).SingleLine()
			})
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Column(c).Padding(8, 0).Children(func() {
					for _, ln := range diffRight {
						diffRowKind(c, ln.num, false, ln.add, ln.spans)
					}
				})
			})
		})
	}).Grow(1)
}

func diffRowKind(c *ui.Context, num int, del, add bool, spans []ui.Span) ui.Element {
	e := ui.Row(c).Height(21)
	if del {
		e = e.Background(colDiffDel)
	} else if add {
		e = e.Background(colDiffAdd)
	}
	e.Children(func() {
		numCol := colMuted2
		if del {
			numCol = colDiffDelLn
		} else if add {
			numCol = colDiffAddLn
		}
		label := ""
		if num > 0 {
			label = itoa(num)
		}
		ui.Text(c, label).Font("JetBrains Mono").FontSize(12).TextColor(numCol).Width(46).Padding(0, 12)
		if len(spans) == 0 {
			spans = []ui.Span{{Text: " "}}
		}
		ui.RichText(c, spans...).Font("JetBrains Mono").FontSize(12)
	})
	return e
}

func (a *appState) inlineDiff(c *ui.Context) ui.Element {
	return ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(8, 0).Children(func() {
			for _, ln := range diffInline {
				e := ui.Row(c).Height(21)
				if ln.del {
					e = e.Background(colDiffDel)
				} else if ln.add {
					e = e.Background(colDiffAdd)
				}
				e.Children(func() {
					oldLabel, newLabel := "", ""
					if ln.oldNo > 0 {
						oldLabel = itoa(ln.oldNo)
					}
					if ln.newNo > 0 {
						newLabel = itoa(ln.newNo)
					}
					numCol := colMuted2
					if ln.del {
						numCol = colDiffDelLn
					} else if ln.add {
						numCol = colDiffAddLn
					}
					ui.Text(c, oldLabel).Font("JetBrains Mono").FontSize(12).TextColor(numCol).Width(46).Padding(0, 12)
					ui.Text(c, newLabel).Font("JetBrains Mono").FontSize(12).TextColor(numCol).Width(46).Padding(0, 12)
					spans := ln.spans
					if len(spans) == 0 {
						spans = []ui.Span{{Text: " "}}
					}
					ui.RichText(c, spans...).Font("JetBrains Mono").FontSize(12)
				})
			}
		})
	})
}
