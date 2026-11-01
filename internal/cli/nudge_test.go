package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// What omaterm does too: subagents that change things, browsers of
// their own; not the subagents that only search and read, nor the
// shell's other commands.
func TestNudgeKind(t *testing.T) {
	for _, c := range []struct {
		tool, input, want string
	}{
		{"Agent", `{"subagent_type": "general-purpose", "prompt": "Implement the export"}`, "subagent"},
		{"Agent", `{"prompt": "Implement the export"}`, "subagent"},
		{"Task", `{"subagent_type": "fork"}`, "subagent"},
		{"Agent", `{"subagent_type": "Explore"}`, ""},
		{"Agent", `{"subagent_type": "Plan"}`, ""},
		{"Agent", `{"subagent_type": "Explore", "isolation": "worktree"}`, "subagent"},
		{"mcp__playwright__browser_navigate", `{}`, "browser"},
		{"mcp__chrome-devtools__take_screenshot", `{}`, "browser"},
		{"mcp__claude-in-chrome__navigate", `{}`, "browser"},
		{"mcp__linear__list_issues", `{}`, ""},
		{"Bash", `{"command": "npx playwright screenshot http://localhost:3000 shot.png"}`, "browser"},
		{"Bash", `{"command": "cd web && bunx @playwright/test test"}`, "browser"},
		{"Bash", `{"command": "chromium --headless --screenshot http://localhost:5173"}`, "browser"},
		{"Bash", `{"command": "xdg-open http://localhost:3000"}`, "browser"},
		{"Bash", `{"command": "grep -r playwright package.json"}`, ""},
		{"Bash", `{"command": "go test -run TestBrowser ./..."}`, ""},
		{"Bash", `{"command": "curl -s localhost:3000"}`, ""},
		{"Edit", `{}`, ""},
	} {
		if got := nudgeKind(c.tool, json.RawMessage(c.input)); got != c.want {
			t.Errorf("%s %s: %q, want %q", c.tool, c.input, got, c.want)
		}
	}
}

// The first call of a kind in a conversation is turned down, saying what
// omaterm offers; the next goes through, as do another conversation's
// and, with agents not let control the app, a browser's.
func TestNudgeOnce(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("OMATERM_CONFIG_HOME", t.TempDir())
	agentInput := json.RawMessage(`{"subagent_type": "general-purpose"}`)
	why := nudge("claude", "c1", "Agent", agentInput)
	if !strings.Contains(why, "omaterm new --worktree <branch> -- claude") || !strings.Contains(why, "make this call again") {
		t.Fatalf("first: %q", why)
	}
	if why := nudge("claude", "c1", "Agent", agentInput); why != "" {
		t.Fatalf("second: %q", why)
	}
	if why := nudge("claude", "c2", "Agent", agentInput); why == "" {
		t.Fatal("another conversation went through")
	}
	if why := nudge("claude", "c1", "mcp__playwright__browser_navigate", nil); why != "" {
		t.Fatalf("a browser, agents not let control the app: %q", why)
	}
}
