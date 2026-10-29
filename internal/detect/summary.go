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

// claudeToolCall is a tool call as Claude Code prints it after its "⏺"
// (Bash(date), Read(file.go)…), with its "⎿" result under it.
var claudeToolCall = regexp.MustCompile(`^\w+\(`)

// claudeSummary is Claude Code's: the last paragraph it said, marked "⏺"
// and wrapped under a two-space indent, not a tool call.
func claudeSummary(screen string) string {
	ls := lines(abovePromptBox(screen))
	for i := len(ls) - 1; i >= 0; i-- {
		if !strings.HasPrefix(ls[i], "⏺ ") {
			continue
		}
		para := []string{strings.TrimSpace(strings.TrimPrefix(ls[i], "⏺ "))}
		for j := i + 1; j < len(ls); j++ {
			l := ls[j]
			if strings.TrimSpace(l) == "" {
				break
			}
			if !strings.HasPrefix(l, "  ") || strings.HasPrefix(strings.TrimSpace(l), "⎿") {
				para = nil
				break
			}
			para = append(para, strings.TrimSpace(l))
		}
		if para == nil || para[0] == "" || claudeToolCall.MatchString(para[0]) {
			continue
		}
		return clip(strings.Join(strings.Fields(strings.Join(para, " ")), " "))
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
