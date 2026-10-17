package main

import (
	"github.com/egoist/mygo/ui"
)

// The read-only editor view showing App.tsx, matching the mock's
// breadcrumb + line-number code listing.

var editorFile = []struct {
	num   int
	spans []ui.Span
}{
	{1, []ui.Span{{Text: "import", Color: synKeyword}, {Text: " { "}, {Text: "useState", Color: synVar}, {Text: " } "}, {Text: "from", Color: synKeyword}, {Text: " "}, {Text: "'react'", Color: synString}, {Text: ";"}}},
	{2, []ui.Span{{Text: "import", Color: synKeyword}, {Text: " "}, {Text: "TerminalProject", Color: synType}, {Text: " "}, {Text: "from", Color: synKeyword}, {Text: " "}, {Text: "'./TerminalProject'", Color: synString}, {Text: ";"}}},
	{3, []ui.Span{{Text: "import", Color: synKeyword}, {Text: " "}, {Text: "Inspector", Color: synType}, {Text: " "}, {Text: "from", Color: synKeyword}, {Text: " "}, {Text: "'./Inspector'", Color: synString}, {Text: ";"}}},
	{4, nil},
	{5, []ui.Span{{Text: "export default function", Color: synKeyword}, {Text: " "}, {Text: "App", Color: synFunc}, {Text: "() "}, {Text: "{", Color: synBrace}}},
	{6, []ui.Span{{Text: "  "}, {Text: "const", Color: synKeyword}, {Text: " [project, setProject] = "}, {Text: "useState", Color: synFunc}, {Text: "("}, {Text: "'dev'", Color: synString}, {Text: ");"}}},
	{7, []ui.Span{{Text: "  "}, {Text: "const", Color: synKeyword}, {Text: " [inspector, setInspector] = "}, {Text: "useState", Color: synFunc}, {Text: "("}, {Text: "'info'", Color: synString}, {Text: ");"}}},
	{8, nil},
	{9, []ui.Span{{Text: "  "}, {Text: "return", Color: synKeyword}, {Text: " ("}}},
	{10, []ui.Span{{Text: "    <", Color: synPunct}, {Text: "Shell", Color: synType}, {Text: ">", Color: synPunct}}},
	{11, []ui.Span{{Text: "      <", Color: synPunct}, {Text: "TerminalProject", Color: synType}, {Text: " id", Color: synVar}, {Text: "={", Color: synPunct}, {Text: "project", Color: synVar}, {Text: "} />", Color: synPunct}}},
	{12, []ui.Span{{Text: "      <", Color: synPunct}, {Text: "Inspector", Color: synType}, {Text: " tab", Color: synVar}, {Text: "={", Color: synPunct}, {Text: "inspector", Color: synVar}, {Text: "} />", Color: synPunct}}},
	{13, []ui.Span{{Text: "    </", Color: synPunct}, {Text: "Shell", Color: synType}, {Text: ">", Color: synPunct}}},
	{14, []ui.Span{{Text: "  );"}}},
	{15, []ui.Span{{Text: "}", Color: synBrace}}},
}

func (a *appState) editorView(c *ui.Context) ui.Element {
	return ui.Column(c).Fill().Background(colCodeBg).Children(func() {
		// Breadcrumb + actions.
		ui.Row(c).Height(34).AlignItems(ui.Center).Padding(0, 12).Gap(4).BorderWidth(0, 0, 1, 0).BorderColor(colBorder).Children(func() {
			ui.Text(c, "src").FontSize(10).TextColor(colMuted)
			ui.Icon(c, iconChevronRight).Width(12).Height(12).TextColor(colMuted2)
			ui.Text(c, "components").FontSize(10).TextColor(colMuted)
			ui.Icon(c, iconChevronRight).Width(12).Height(12).TextColor(colMuted2)
			ui.Text(c, "App.tsx").FontSize(10).TextColor(colText2)
			ui.Spacer(c)
			cmdButton(c, iconSplit, 14, false)
			cmdButton(c, iconMore, 14, false)
		})

		// Code listing.
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(8, 0).Children(func() {
				for _, ln := range editorFile {
					codeLine(c, ln.num, ln.spans)
				}
			})
		})

		// Editor status line.
		ui.Row(c).Height(26).AlignItems(ui.Center).Padding(0, 12).Gap(16).BorderWidth(1, 0, 0, 0).BorderColor(colBorder).Children(func() {
			ui.Text(c, "Ln 15, Col 2").FontSize(10).TextColor(colMuted)
			ui.Text(c, "Spaces: 2").FontSize(10).TextColor(colMuted)
			ui.Text(c, "UTF-8").FontSize(10).TextColor(colMuted)
			ui.Spacer(c)
			ui.Text(c, "TypeScript React").FontSize(10).TextColor(colMuted)
		})
	})
}

// codeLine renders a gutter number plus colored code spans.
func codeLine(c *ui.Context, num int, spans []ui.Span) ui.Element {
	return ui.Row(c).Height(21).Children(func() {
		ui.Text(c, itoa(num)).Font("JetBrains Mono").FontSize(12).TextColor(colMuted2).Width(46).Padding(0, 12)
		if len(spans) == 0 {
			spans = []ui.Span{{Text: " "}}
		}
		ui.RichText(c, spans...).Font("JetBrains Mono").FontSize(12)
	})
}
