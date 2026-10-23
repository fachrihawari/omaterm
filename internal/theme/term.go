package theme

import (
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// TermTheme is the terminals' theme: the theme's ghostty.conf when it has
// one, else its colors, always on the Card background so a terminal and
// its pane are one surface.
func TermTheme(s Source, p *Palette) *terminal.Theme {
	if s.GhosttyConf != "" {
		if th, err := terminal.GhosttyTheme(s.GhosttyConf); err == nil && th != nil {
			t := *th
			t.Background = p.Card
			return &t
		}
	}
	return &terminal.Theme{
		Foreground: s.Foreground,
		Background: p.Card,
		Cursor:     p.TextStrong,
		Selection:  p.Selection,
		Palette:    s.ANSI,
	}
}

// EditorTheme colors the code editors, the diff views and previews.
func EditorTheme(p *Palette) *editor.Theme {
	t := &editor.Theme{
		Background:        p.Code,
		Text:              p.Text,
		LineNumber:        p.TextMuted,
		ActiveLineNumber:  p.Text,
		ActiveLine:        p.Hover,
		Selection:         p.Selection,
		InactiveSelection: p.Selection.Alpha(0.6),
		Cursor:            p.Accent,
		MatchingBracket:   p.BorderStrong,
		Match:             p.Warn.Alpha(0.25),
		CurrentMatch:      p.Warn.Alpha(0.45),
		Occurrence:        p.Hover,
		IndentGuide:       p.Border,
		ActiveIndentGuide: p.BorderStrong,
		WrapGuide:         p.Border,
		Whitespace:        p.TextFaint.Alpha(0.5),
		Error:             p.Bad,
		Warning:           p.Warn,
		Info:              p.Info,
		Hint:              p.TextMuted,
		Added:             p.Ok,
		Deleted:           p.Bad,
		Modified:          p.Modified,
	}
	tok := func(k editor.TokenKind, c ui.Color, italic, bold bool) {
		t.Tokens[k] = editor.TokenStyle{Color: c, Italic: italic, Bold: bold}
	}
	s := p.Syn
	tok(editor.TokenKeyword, s.Keyword, false, false)
	tok(editor.TokenType, s.Type, false, false)
	tok(editor.TokenFunction, s.Func, false, false)
	tok(editor.TokenVariable, s.Var, false, false)
	tok(editor.TokenProperty, s.Var, false, false)
	tok(editor.TokenConstant, s.Number, false, false)
	tok(editor.TokenNumber, s.Number, false, false)
	tok(editor.TokenString, s.String, false, false)
	tok(editor.TokenEscape, s.Meta, false, false)
	tok(editor.TokenRegexp, s.String, false, false)
	tok(editor.TokenComment, s.Comment, true, false)
	tok(editor.TokenOperator, s.Punct, false, false)
	tok(editor.TokenPunctuation, s.Punct, false, false)
	tok(editor.TokenTag, s.Tag, false, false)
	tok(editor.TokenAttribute, s.Attr, false, false)
	tok(editor.TokenMeta, s.Meta, false, false)
	tok(editor.TokenHeading, s.Func, false, true)
	tok(editor.TokenEmphasis, p.Text, true, false)
	tok(editor.TokenStrong, p.TextStrong, false, true)
	tok(editor.TokenLink, s.Link, false, false)
	tok(editor.TokenCode, s.String, false, false)
	tok(editor.TokenInserted, p.Ok, false, false)
	tok(editor.TokenDeleted, p.Bad, false, false)
	tok(editor.TokenInvalid, p.Bad, false, false)
	return t
}
