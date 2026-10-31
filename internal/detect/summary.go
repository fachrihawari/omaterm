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
	"agy":    agySummary,
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

// Antigravity CLI's transcript: the user's prompt as "> …" at the line's
// start, then lines of its own at the start ("▸ Thought for 2s", "● Ran
// (…)"), and what it says in paragraphs indented two spaces, blank lines
// between.

// agySummary is the last paragraph Antigravity said on its last turn: the
// indented lines at the end of the transcript, after the last prompt.
func agySummary(screen string) string {
	// Only under its prompt box, which ends the screen: a dialog in its
	// place, as a permission's, leaves no last word to read.
	all := lines(screen)
	top := promptBoxTop(all)
	if top < 0 {
		return ""
	}
	rules, rest := 0, 0
	for _, l := range all[top:] {
		switch {
		case horizontalRule(l):
			rules++
		case rules == 2 && strings.TrimSpace(l) != "":
			rest++
		}
	}
	if rest > 1 {
		return ""
	}
	ls := all[:top]
	end := len(ls)
	for end > 0 && strings.TrimSpace(ls[end-1]) == "" {
		end--
	}
	start := end
	for start > 0 && strings.HasPrefix(ls[start-1], "  ") && strings.TrimSpace(ls[start-1]) != "" {
		start--
	}
	if start == end {
		return "" // the screen ends on a mark of its own, as a tool call
	}
	// Words of this turn: a prompt of the user's is above them.
	for i := start - 1; i >= 0; i-- {
		if strings.HasPrefix(ls[i], "> ") {
			return clip(strings.Join(strings.Fields(strings.Join(ls[start:end], " ")), " "))
		}
		if horizontalRule(ls[i]) {
			break
		}
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
