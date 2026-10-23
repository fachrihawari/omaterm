package theme

import "github.com/egoist/mygo/ui"

// BuiltinDark is omaterm's own dark theme, the one it had before it
// followed Omarchy: used when no Omarchy theme is installed.
func BuiltinDark() Source {
	h := ui.Hex
	return Source{
		Name: "omaterm-dark", Dark: true,
		Background: h("#11161c"), Foreground: h("#d7dde5"), BrightForeground: h("#ffffff"),
		Accent: h("#2f81f7"), Selection: h("#1d3a5f"), Muted: h("#7f8a99"),
		ANSI: [16]ui.Color{
			h("#11161c"), h("#ff6f6f"), h("#63d58d"), h("#e9c66d"), h("#5aa9ff"), h("#c792ea"), h("#61d8df"), h("#d7dde5"),
			h("#7f8a99"), h("#ff8585"), h("#b9f263"), h("#f3a85f"), h("#7cbcff"), h("#d7a8f0"), h("#86e3e8"), h("#ffffff"),
		},
		Extra: map[string]ui.Color{"orange": h("#f3a85f")},
	}
}

// BuiltinLight is the light counterpart of BuiltinDark.
func BuiltinLight() Source {
	h := ui.Hex
	return Source{
		Name: "omaterm-light", Dark: false,
		Background: h("#fafafa"), Foreground: h("#24292f"), BrightForeground: h("#000000"),
		Accent: h("#0969da"), Selection: h("#cfe3fb"), Muted: h("#8c959f"),
		ANSI: [16]ui.Color{
			h("#fafafa"), h("#cf222e"), h("#1a7f37"), h("#9a6700"), h("#0969da"), h("#8250df"), h("#1b7c83"), h("#24292f"),
			h("#8c959f"), h("#a40e26"), h("#2da44e"), h("#bf8700"), h("#218bff"), h("#a475f9"), h("#3192aa"), h("#000000"),
		},
		Extra: map[string]ui.Color{"orange": h("#bc4c00")},
	}
}
