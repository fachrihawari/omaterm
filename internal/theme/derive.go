package theme

import "github.com/egoist/mygo/ui"

// Palette is what the app paints with: colors named for their job, not
// their hue. Derive makes one from any Source, so every theme gives the
// same roles enough contrast to read.
type Palette struct {
	Name string
	Dark bool

	// The window and its surfaces. Card is the theme's background, which
	// panes and terminals show; Wash is the backdrop the cards sit on.
	Wash, WashGlow, Sidebar, Card, CardDim, Veil  ui.Color
	Raised, Popover, Input, Code                  ui.Color
	Hover, RowSelected, Chip, Kbd, KbdBorder      ui.Color
	Scrim                                         ui.Color
	Border, BorderStrong, Divider, Edge, EdgeHigh ui.Color
	CardShadow, CardShadow2, FloatShadow          ui.Color

	Text, TextStrong, TextSoft, TextMuted, TextFaint ui.Color

	Accent, AccentHover, AccentText, Selection, Focus ui.Color

	// Ok, Warn and Bad mean success, caution and failure, whatever the
	// theme calls green, yellow and red; Attention marks what needs the
	// user, Done what finished.
	Ok, Warn, Bad, Info, Attention, AttentionSoft, OnBad, Done, OnDone ui.Color

	DiffAddBg, DiffDelBg, DiffAddLn, DiffDelLn, Modified ui.Color

	// ANSI is the theme's terminal palette as written, for what shows
	// program output's colors.
	ANSI [16]ui.Color
	Syn  Syntax
}

// Syntax are the colors of code, taken from the theme's ANSI colors as
// its author meant them.
type Syntax struct {
	Keyword, Type, Func, Var, String, Number, Comment, Punct, Tag, Attr, Link, Meta ui.Color
	FileTS, FileJSON, Folder                                                        ui.Color
}

// Derive gives a Source's colors their roles. It reads the base colors
// and checks each semantic color's hue: themes name their colors freely
// (one theme's "green" is violet), so a color is only used for success,
// caution or failure when it looks like it.
func Derive(s Source) *Palette {
	bg, fg := s.Background, s.Foreground
	p := &Palette{Name: s.Name, Dark: s.Dark, ANSI: s.ANSI}
	p.Card, p.Code = bg, bg
	if s.Dark {
		p.Wash = bg.Mix(black, 0.30)
		p.WashGlow = s.Accent.Alpha(0.16)
		p.Raised = bg.Mix(fg, 0.08)
		p.Popover = p.Raised
		p.Input = bg.Mix(fg, 0.05)
		p.Hover = fg.Alpha(0.06)
		p.RowSelected = fg.Alpha(0.10)
		p.Chip = fg.Alpha(0.07)
		p.Border = fg.Alpha(0.08)
		p.BorderStrong = fg.Alpha(0.14)
		p.CardShadow, p.CardShadow2, p.FloatShadow = black.Alpha(0.35), black.Alpha(0.20), black.Alpha(0.45)
		p.CardDim = bg.Alpha(0.5)
		p.Scrim = black.Alpha(0.35)
	} else {
		p.Wash = bg.Mix(fg, 0.06)
		p.WashGlow = s.Accent.Alpha(0.12)
		p.Raised = bg.Mix(white, 0.6)
		p.Popover = p.Raised
		p.Input = bg.Mix(white, 0.6)
		p.Hover = fg.Alpha(0.045)
		p.RowSelected = p.Raised
		p.Chip = fg.Alpha(0.05)
		p.Border = fg.Alpha(0.07)
		p.BorderStrong = fg.Alpha(0.12)
		p.CardShadow, p.CardShadow2, p.FloatShadow = black.Alpha(0.07), black.Alpha(0.035), black.Alpha(0.14)
		p.CardDim = bg.Alpha(0.58)
		p.Scrim = black.Alpha(0.12)
	}
	p.Sidebar = p.Wash
	if s.Dark {
		p.Veil = p.Wash.Alpha(0.42)
	} else {
		p.Veil = p.Wash.Alpha(0.28)
	}
	p.Divider = p.Border
	p.Edge, p.EdgeHigh = fg.Alpha(0.075), fg.Alpha(0.11)
	p.Kbd = p.Chip
	p.KbdBorder = p.BorderStrong

	p.Text = fg
	p.TextStrong = s.BrightForeground
	p.TextSoft = fg.Mix(bg, 0.15)
	p.TextMuted = ensureContrast(fg.Mix(bg, 0.38), bg, 4.5)
	p.TextFaint = ensureContrast(fg.Mix(bg, 0.55), bg, 3.0)

	p.Accent = ensureContrast(s.Accent, bg, 3)
	p.AccentHover = p.Accent.Mix(white, 0.08)
	p.AccentText = onColor(p.Accent)
	p.Selection = s.Selection
	p.Focus = p.Accent

	mono := monochrome(s)
	pick := func(cands []ui.Color, lo, hi, hue float64) ui.Color {
		var c ui.Color
		found := false
		for _, cand := range cands {
			if hueIn(cand, lo, hi) {
				c, found = cand, true
				break
			}
		}
		if !found {
			c = synth(hue, s.Dark, mono)
		}
		return ensureContrast(c, bg, 3)
	}
	a := s.ANSI
	p.Ok = pick([]ui.Color{a[2], a[10]}, 80, 170, 145)
	warnCands := []ui.Color{a[3]}
	if o, ok := s.Extra["orange"]; ok {
		warnCands = append(warnCands, o)
	}
	warnCands = append(warnCands, a[11])
	p.Warn = pick(warnCands, 40, 100, 75)
	p.Bad = pick([]ui.Color{a[1], a[9]}, 340, 40, 25)
	p.Info = pick([]ui.Color{a[4], a[6]}, 200, 270, 250)
	p.Attention = p.Warn
	p.AttentionSoft = p.Attention.Alpha(0.14)
	p.OnBad = onColor(p.Bad)
	p.Done, p.OnDone = p.Text, p.Card

	p.DiffAddBg, p.DiffDelBg = p.Ok.Alpha(0.10), p.Bad.Alpha(0.10)
	p.DiffAddLn, p.DiffDelLn = p.Ok.Mix(bg, 0.4), p.Bad.Mix(bg, 0.4)
	p.Modified = p.Warn

	orange := a[3]
	if o, ok := s.Extra["orange"]; ok {
		orange = o
	}
	read := func(c ui.Color) ui.Color { return ensureContrast(c, bg, 3) }
	p.Syn = Syntax{
		Keyword: read(a[5]), Type: read(a[6]), Func: read(a[4]), Var: fg, String: read(a[2]),
		Number: read(a[3]), Comment: p.TextFaint, Punct: p.TextSoft, Tag: read(a[1]), Attr: read(a[3]),
		Link: p.Accent, Meta: read(orange),
		FileTS: read(a[4]), FileJSON: read(a[3]), Folder: p.Warn,
	}
	return p
}

// onColor is the text color that reads on c.
func onColor(c ui.Color) ui.Color {
	if Contrast(white, c) >= Contrast(black, c) {
		return white
	}
	return black
}

// monochrome reports whether a theme has no colors at all, as Omarchy's
// "white": its semantic colors are then synthesized, but quieter.
func monochrome(s Source) bool {
	for i := 1; i < 7; i++ {
		if toOKLCH(s.ANSI[i]).C >= 0.03 || toOKLCH(s.ANSI[8+i]).C >= 0.03 {
			return false
		}
	}
	return true
}

func synth(hue float64, dark, mono bool) ui.Color {
	o := OKLCH{L: 0.55, C: 0.13, H: hue}
	if dark {
		o.L = 0.78
	}
	if mono {
		o.C = 0.09
	}
	return o.color()
}
