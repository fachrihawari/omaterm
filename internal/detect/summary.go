package detect

import (
	"regexp"
	"strings"
)

// An agent's last word on its turn, as its screen shows it: what it said
// last, above its prompt. The manifests (upstream, see NOTICE.md) tell
// states only; where each agent puts its words is omaterm's to know.

// summaryRunes is as much of it as is kept.
const summaryRunes = 200

// summaries tells, by agent, how to read its last word off its screen.
var summaries = map[string]func(screen string) string{
	"claude": claudeSummary,
}

// Summary is what the agent's screen ended on: its last word on its
// turn; "" when the screen shows none, or omaterm doesn't know where the
// agent puts it.
func Summary(agent, screen string) string {
	if n, ok := manifests[agent]; ok {
		agent = n
	}
	if f := summaries[agent]; f != nil {
		return f(screen)
	}
	return ""
}

// Claude Code's transcript: what it says and the tools it calls start
// with a bullet at the line's start (● since 2.1, ⏺ before); a tool call
// is Bash(…), Read(…), with its result under "⎿"; what it says goes on
// in paragraphs indented two spaces, blank lines between; a line of its
// own, as "✻ Baked for 4s", ends a turn.
var (
	claudeBullet   = regexp.MustCompile(`^[●⏺] `)
	claudeToolCall = regexp.MustCompile(`^\w+\(`)
	claudeMark     = regexp.MustCompile(`^[●⏺⎿✻✢✳✶✽*·]`)
)

// claudeSummary is the last paragraph of the last thing Claude Code said.
func claudeSummary(screen string) string {
	ls := lines(abovePromptBox(screen))
	for i := len(ls) - 1; i >= 0; i-- {
		if !claudeBullet.MatchString(ls[i]) {
			continue
		}
		first := strings.TrimSpace(claudeBullet.ReplaceAllString(ls[i], ""))
		if first == "" || claudeToolCall.MatchString(first) {
			continue
		}
		// The block: this line, then the paragraphs indented under it,
		// to a line of another kind.
		paras, para := []string{}, []string{first}
		for _, l := range ls[i+1:] {
			t := strings.TrimSpace(l)
			switch {
			case t == "":
				if len(para) > 0 {
					paras, para = append(paras, strings.Join(para, " ")), nil
				}
				continue
			case !strings.HasPrefix(l, "  ") || claudeMark.MatchString(t):
				if len(para) > 0 {
					paras = append(paras, strings.Join(para, " "))
				}
				para = nil
			default:
				para = append(para, t)
				continue
			}
			break
		}
		if len(para) > 0 {
			paras = append(paras, strings.Join(para, " "))
		}
		return clip(strings.Join(strings.Fields(paras[len(paras)-1]), " "))
	}
	return ""
}

// clip cuts s to summaryRunes, marking the cut.
func clip(s string) string {
	r := []rune(s)
	if len(r) <= summaryRunes {
		return s
	}
	return strings.TrimRight(string(r[:summaryRunes-1]), " ") + "…"
}
