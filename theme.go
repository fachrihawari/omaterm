package main

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// The mock's oma.* palette (omaterm_mock_ui_v5.html, dark theme).
var (
	colBg      = ui.Hex("#0b0e12")
	colBg2     = ui.Hex("#0f1318")
	colPanel   = ui.Hex("#11161c")
	colPanel2  = ui.Hex("#161c24")
	colPanel3  = ui.Hex("#1b222c")
	colBorder  = ui.Hex("#252d38")
	colBorder2 = ui.Hex("#303a48")
	colText    = ui.Hex("#d7dde5")
	colText2   = ui.Hex("#bcc5d0")
	colMuted   = ui.Hex("#7f8a99")
	colMuted2  = ui.Hex("#76828f")
	colBlue    = ui.Hex("#5aa9ff")
	colBlue2   = ui.Hex("#2f81f7")
	colCyan    = ui.Hex("#61d8df")
	colGreen   = ui.Hex("#63d58d")
	colYellow  = ui.Hex("#e9c66d")
	colOrange  = ui.Hex("#f3a85f")
	colRed     = ui.Hex("#ff6f6f")
	colPurple  = ui.Hex("#c792ea")
	colLime    = ui.Hex("#b9f263")

	// Surfaces used across the mock.
	colHover      = ui.Hex("#18202a") // hovered rows / pressed buttons
	colInput      = ui.Hex("#151a21") // inputs, commit box, search field
	colCard       = ui.Hex("#141920") // info cards
	colPaneBar    = ui.Hex("#141a21") // floating pane action bar
	colCodeBg     = ui.Hex("#101318") // editor and diff background
	colDiffTop    = ui.Hex("#0f1216") // diff column headers
	colStatusBg   = ui.Hex("#0d1014") // status bar
	colDiffAdd    = ui.RGBA(99, 213, 141, 0.09)
	colDiffDel    = ui.RGBA(255, 111, 111, 0.10)
	colDiffAddLn  = ui.Hex("#5d8a67")
	colDiffDelLn  = ui.Hex("#8b5e5e")
	colSelection  = ui.RGBA(90, 169, 255, 0.28)
	colActivePane = ui.Hex("#3d4451")
)

// Syntax colors used by the mock's code and terminal output.
var (
	synKeyword  = ui.Hex("#c792ea")
	synType     = ui.Hex("#61d8df")
	synBrace    = ui.Hex("#e9c66d")
	synVar      = ui.Hex("#9cdcfe")
	synBool     = ui.Hex("#569cd6")
	synFunc     = ui.Hex("#dcdcaa")
	synPunct    = ui.Hex("#808080")
	synString   = ui.Hex("#7ec699")
	synLink     = ui.Hex("#4a9eff")
	synDim      = ui.Hex("#4a5568")
	synTS       = ui.Hex("#519aba") // TS file badge
	synJSON     = ui.Hex("#cbcb41") // json file badge
	synFolderIc = ui.Hex("#e9c66d")
)

func omaTheme() *ui.Theme {
	t := ui.DarkTheme()
	t.Background = colBg
	t.Surface = colPanel2
	t.SurfaceHover = colHover
	t.SurfacePressed = colPanel3
	t.Border = colBorder
	t.Text = colText
	t.TextMuted = colMuted
	t.Accent = colBlue2
	t.AccentHover = ui.Hex("#3d8bf8")
	t.AccentPressed = colBlue2
	t.AccentText = ui.Hex("#ffffff")
	t.Danger = colRed
	t.Warning = colYellow
	t.Success = colGreen
	t.Selection = colSelection
	t.Focus = colBlue
	t.Scrollbar = colBorder2
	// Toasts and tooltips stay dark, as the mock's .toast.
	t.Inverse = colPanel3
	t.InverseText = colText
	t.Radius = 6
	t.Spacing = 4
	t.FontSize = 13
	// Inter, the mock's UI font, falling back to Noto Sans; JetBrains Mono
	// for code and terminals, set where they render.
	t.Font = "Inter, Noto Sans"
	return t
}

// omaEditorTheme overrides the one editor color that is too dim to read:
// MyGo derives the gutter from TextMuted at 75% alpha (~3.1:1 on our code
// background). The rest of the fields stay zero and resolve to the
// default theme, so syntax colors keep following the window theme.
func omaEditorTheme() *editor.Theme {
	return &editor.Theme{
		LineNumber:       colMuted,
		ActiveLineNumber: colText,
	}
}
