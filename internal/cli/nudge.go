package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"omaterm/internal/client"
	"omaterm/internal/paths"
	"omaterm/internal/proto"
)

// An agent reaches for tools of its own that omaterm has too: a subagent
// for work omaterm's agents in worktrees would do, a browser beside the
// one in a browser pane. As it first does in a conversation, omaterm's
// PreToolUse hook turns the call down, saying what omaterm offers; called
// again, it goes through, as omaterm may not do what the agent needs.

// claudeToolMatcher is the matcher of the PreToolUse hook omaterm installs in
// Claude Code: its subagents, its shell, and browser MCP servers' tools.
// Codex's hook has none, as Codex names its shell differently by version;
// agyToolMatcher is Antigravity's, which blocks a call whose hook fails, so
// it leaves its shell out.
const (
	claudeToolMatcher = "Agent|Task|Bash|mcp__.*([Pp]laywright|[Pp]uppeteer|[Cc]hrome|[Bb]rowser|[Dd]ev[Tt]ools).*"
	agyToolMatcher    = "invoke_subagent|browser_.*"
)

// readOnlySubagents are subagents that only search and read, which stay
// the agent's to use: Claude Code's types, and Codex's explorer.
var readOnlySubagents = []string{"Explore", "Plan", "claude-code-guide", "statusline-setup", "explorer"}

// subagentTools start a subagent: Claude Code's, Codex's, Antigravity's,
// and a Pi extension's.
var subagentTools = []string{"Agent", "Task", "spawn_agent", "invoke_subagent", "subagent"}

// shellTools run a command: Claude Code's Bash, Codex's shell by its
// names, Antigravity's run_command, Pi's bash.
var shellTools = []string{"Bash", "bash", "shell", "exec_command", "local_shell", "unified_exec", "container.exec", "run_command"}

var (
	// browserTool names a browser tool: a browser MCP server's, or
	// Antigravity's own.
	browserTool = regexp.MustCompile(`(?i)^mcp__.*(playwright|puppeteer|chrome|browser|devtools)|^browser_`)
	// browserShell is a shell command that drives a browser of its own,
	// or opens a page in the user's.
	browserShell = regexp.MustCompile(`(?i)\b(npx|bunx|pnpm\s+dlx|pnpm\s+exec|yarn)\s+(@playwright/\S+|playwright|puppeteer)\b` +
		`|\b(chromium|chromium-browser|google-chrome|google-chrome-stable|chrome|firefox|msedge)\b[^;&|]*--(headless|screenshot|dump-dom)` +
		`|(^|[;&|(]\s*)(xdg-open|open)\s+['"]?https?://`)
	// browserJS is JavaScript that drives Codex's browser, through its
	// node_repl tool.
	browserJS = regexp.MustCompile(`agent\.browsers|browser-client|\btab\.(goto|click|type|screenshot|reload)\(`)
)

// nudgeKind is what a tool call would do that omaterm does: "subagent",
// "browser", or "" for anything else.
func nudgeKind(tool string, input json.RawMessage) string {
	var in struct {
		Type        string          `json:"subagent_type"`
		AgentType   string          `json:"agent_type"`
		Isolation   string          `json:"isolation"`
		Command     json.RawMessage `json:"command"`
		CommandLine string          `json:"CommandLine"`
		Cmd         string          `json:"cmd"`
	}
	json.Unmarshal(input, &in)
	switch {
	case slices.Contains(subagentTools, tool):
		if in.Isolation == "" && (slices.Contains(readOnlySubagents, in.Type) || slices.Contains(readOnlySubagents, in.AgentType)) {
			return ""
		}
		return "subagent"
	case slices.Contains(shellTools, tool):
		cmd := in.CommandLine + in.Cmd
		var s string
		var argv []string
		if json.Unmarshal(in.Command, &s) == nil {
			cmd += s
		} else if json.Unmarshal(in.Command, &argv) == nil {
			cmd += strings.Join(argv, " ")
		}
		if browserShell.MatchString(cmd) {
			return "browser"
		}
	case tool == "mcp__node_repl__js":
		if browserJS.Match(input) {
			return "browser"
		}
	case browserTool.MatchString(tool):
		return "browser"
	}
	return ""
}

// nudgeReason is what omaterm tells an agent whose call it turns down.
func nudgeReason(kind, agent string) string {
	again := " If omaterm can't do what you need here, say why and make this call again: it goes through, as omaterm asks once."
	if kind == "browser" {
		return "omaterm has a browser pane, which the user sees beside you: `omaterm split --url <url>`, " +
			"then `omaterm browser open|text|click|type|wait|screenshot` (see the omaterm skill)." + again
	}
	return "omaterm runs agents of their own for work like this, each in a git worktree, which the user sees and can steer: " +
		"`omaterm new --worktree <branch> -- " + agent + "`, `omaterm ask <branch> \"<its part>\" --no-wait`, " +
		"then `omaterm merge <branch>` once it is done (see the omaterm skill). Subagents stay right for read-only searching and reading." + again
}

// nudge tells why the agent should use omaterm rather than make a call:
// "" when omaterm lets it through, as it does the second time in a
// conversation, and when omaterm can't do it (a browser, with agents not
// let control the app, or its window closed).
func nudge(agent, conversation, tool string, input json.RawMessage) string {
	kind := nudgeKind(tool, input)
	if kind == "" || conversation == "" {
		return ""
	}
	if kind == "browser" && (!agentsControlApp() || !appOpen()) {
		return ""
	}
	if !firstNudge(conversation, kind) {
		return ""
	}
	return nudgeReason(kind, agent)
}

// nudgeDir keeps which conversations were nudged, a file each per kind.
func nudgeDir() string { return filepath.Join(paths.StateDir(), "nudges") }

// firstNudge tells whether a conversation wasn't nudged of kind yet, and
// keeps that it now is. Marks older than a month go.
func firstNudge(conversation, kind string) bool {
	dir := nudgeDir()
	name := filepath.Join(dir, strings.NewReplacer("/", "_", `\`, "_").Replace(conversation)+"-"+kind)
	if _, err := os.Stat(name); err == nil {
		return false
	}
	if os.MkdirAll(dir, 0o700) != nil {
		return false
	}
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return false // another call of the conversation was first
	}
	f.Close()
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > 30*24*time.Hour {
				os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
	return true
}

// appOpen tells whether omaterm's window is open, which its browser panes
// need: an app follows the session daemon, or answers itself.
func appOpen() bool {
	if c, err := client.Dial(paths.DaemonSocketPath()); err == nil {
		defer c.Close()
		var cl proto.Clients
		if c.Call("clients", nil, &cl) == nil {
			return cl.Apps > 0
		}
	}
	c, err := client.Connect()
	if err != nil {
		return false
	}
	c.Close()
	return true
}
