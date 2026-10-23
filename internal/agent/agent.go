// Parts ported from Kero (github.com/egoist/kero, MIT,
// docs/third_party/kero-LICENSE): the agent list and its detection
// (internal/daemon/daemon.go), titles (internal/proto/proto.go) and resume
// commands (internal/daemon/store.go).

// Package agent knows the coding agents a pane may run: how to tell one
// from its process, how its title shows it works, and how to resume its
// conversation after a restart.
package agent

import (
	"path/filepath"
	"regexp"
	"strings"
)

// Names are the agents the app recognizes, by the name of their executable
// or script.
var Names = []string{
	"claude", "codex", "gemini", "opencode", "amp", "aider", "cursor-agent", "droid",
	"qwen", "goose", "crush", "copilot", "kiro-cli", "cline", "auggie", "kimi", "pi",
}

// Detect returns the agent a process is, from its name and first
// arguments, "" for none. npm installs agents as scripts that node runs,
// so the script's path counts too.
func Detect(name string, args []string) string {
	cands := []string{name}
	for i, a := range args {
		if i > 2 {
			break
		}
		cands = append(cands, filepath.Base(a))
	}
	for _, c := range cands {
		c = strings.ToLower(strings.TrimSuffix(c, filepath.Ext(c)))
		for _, a := range Names {
			if c == a || strings.HasPrefix(c, a+"-") && a != "pi" {
				return a
			}
		}
	}
	// Scripts installed by npm: …/node_modules/@openai/codex/bin/codex.js,
	// or …/@anthropic-ai/claude-code/cli.js.
	for i, a := range args {
		if i > 2 {
			break
		}
		a = strings.ReplaceAll(a, `\`, "/")
		for _, ag := range Names {
			if len(ag) > 3 && (strings.Contains(a, "/"+ag+"/") || strings.Contains(a, "/"+ag+"-code/")) {
				return ag
			}
		}
	}
	return ""
}

var displayNames = map[string]string{
	"claude": "Claude Code", "codex": "Codex", "gemini": "Gemini", "opencode": "OpenCode",
	"amp": "Amp", "aider": "Aider", "cursor-agent": "Cursor", "droid": "Droid", "qwen": "Qwen Code",
	"goose": "Goose", "crush": "Crush", "copilot": "Copilot", "kiro-cli": "Kiro", "cline": "Cline",
	"auggie": "Auggie", "kimi": "Kimi", "pi": "Pi",
}

// DisplayName is an agent's name as people write it.
func DisplayName(agent string) string {
	if n, ok := displayNames[agent]; ok {
		return n
	}
	return agent
}

// TitleBusy reports whether a title shows a spinner, as agents' and other
// programs' titles do while they work: a braille pattern, or a spinner's
// half-circle.
func TitleBusy(t string) bool {
	r, _ := firstRune(strings.TrimSpace(t))
	return r > 0x2800 && r <= 0x28FF || strings.ContainsRune("◐◓◑◒", r)
}

// CleanTitle strips the status glyphs programs put before their title.
func CleanTitle(t string) string {
	t = strings.TrimSpace(t)
	for {
		r, size := firstRune(t)
		if size == 0 || !isTitleGlyph(r) {
			break
		}
		t = strings.TrimSpace(t[size:])
	}
	return t
}

func firstRune(s string) (rune, int) {
	for _, r := range s {
		return r, len(string(r))
	}
	return 0, 0
}

func isTitleGlyph(r rune) bool {
	switch {
	case r >= 0x2800 && r <= 0x28FF: // braille spinners
		return true
	case r >= 0x2700 && r <= 0x27BF: // dingbats: ✳ ✶ ✻ ✽ ✓ ✗
		return true
	case r >= 0x25A0 && r <= 0x25FF: // geometric shapes: ◇ ◆ ● ◐
		return true
	case r >= 0x2600 && r <= 0x26FF: // misc symbols: ⚡ ☐
		return true
	case strings.ContainsRune("·•*✦✧⏺⏵>»", r):
		return true
	}
	return false
}

// Session is an agent's own conversation, which a restart resumes.
type Session struct {
	Agent string `json:"agent"`
	ID    string `json:"id"`
}

// SessionIDRe is what an agent's session id may be: no shell syntax, so it
// can go into a command line.
var SessionIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// ResumeCommand is the command that resumes an agent's conversation, nil
// for none.
func ResumeCommand(s *Session) []string {
	if s == nil || !SessionIDRe.MatchString(s.ID) {
		return nil
	}
	switch s.Agent {
	case "claude":
		return []string{"claude", "--resume", s.ID}
	case "codex":
		return []string{"codex", "resume", s.ID}
	case "pi", "opencode":
		return []string{s.Agent, "--session", s.ID}
	}
	return nil
}
