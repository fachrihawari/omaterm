// Ported from Kero (github.com/egoist/kero, MIT, docs/third_party/kero-LICENSE),
// internal/cli/hook.go, with the agent plugins in agents/ (pi.ts and
// opencode/, including its package.json).

package cli

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"omaterm/internal/atomicfile"
	"omaterm/internal/client"
	"omaterm/internal/clilink"
	"omaterm/internal/paths"
	"omaterm/internal/procs"
	"omaterm/internal/proto"
)

// hookCmd is what an agent runs as its session starts (the hook `omaterm
// skill setup` installs): it tells the agent that it runs in omaterm, in
// which session and pane, and tells omaterm the agent's own session, to
// resume after a restart. Before the agent's tool calls, it nudges it
// toward omaterm's own (nudge). It never fails the agent: outside
// omaterm, or on any trouble, it says nothing.
func hookCmd(args []string) error {
	agent := ""
	if len(args) > 0 {
		agent = args[0]
	}
	if agent == "context" && len(args) >= 3 {
		return contextCmd(args[1], args[2], len(args) > 3 && args[3] == "wait")
	}
	if agent == "agy" {
		return agyHookCmd(args[1:])
	}
	pane := os.Getenv("OMATERM_PANE")
	var in struct {
		Event   string          `json:"hook_event_name"`
		AgentID string          `json:"agent_id"`   // set for Claude Code's subagents
		Session string          `json:"session_id"` // the agent's own
		Cwd     string          `json:"cwd"`
		Source  string          `json:"source"` // "startup", "resume", "clear", "compact"
		Tool    string          `json:"tool_name"`
		Input   json.RawMessage `json:"tool_input"`
	}
	json.NewDecoder(io.LimitReader(os.Stdin, 1<<20)).Decode(&in)
	if pane == "" || in.AgentID != "" {
		return nil
	}
	if in.Event == "PreToolUse" {
		why := nudge(agent, in.Session, in.Tool, in.Input)
		if why == "" {
			return nil
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"hookSpecificOutput": map[string]string{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": why,
		}})
	}
	if in.Event != "SessionStart" {
		return nil
	}
	if t := os.Getenv("CODEX_THREAD_ID"); t != "" && in.Session != "" && t != in.Session {
		return nil // a Codex run by another agent in this pane, not the pane's
	}
	sid, away := os.Getenv("OMATERM_SESSION"), false
	c, err := connect()
	if err != nil {
		c = nil
	} else {
		defer c.Close()
		if in.Session != "" {
			// omaterm tells the pane: the environment may be that of
			// another, as when Codex's app server, which panes share,
			// runs the hook.
			var r proto.AgentSessionResult
			if c.Call("pane.agentSession", proto.AgentSessionParams{PID: os.Getpid(), Agent: agent, Session: in.Session, Dir: in.Cwd}, &r) != nil {
				return nil
			}
			pane, sid, away = r.Pane, r.Session, r.Away
		}
		if in.Source != "resume" && in.Source != "compact" {
			forgetAgentName(c, pane, sid)
		}
	}
	out := map[string]any{"hookSpecificOutput": map[string]string{
		"hookEventName":     "SessionStart",
		"additionalContext": hookContext(c, pane, sid, away),
	}}
	return json.NewEncoder(os.Stdout).Encode(out)
}

// agyHookCmd is what Antigravity runs (its hooks.json, see agyHook):
// SessionStart as a conversation starts, PreInvocation before each call of
// its model, PreToolUse before its subagents' and browser's tool calls,
// which it nudges toward omaterm's (nudge). At a turn's start both tell omaterm the pane's conversation,
// since a resumed one starts no session; SessionStart also tells the agent
// where it runs. It always answers with a JSON object, as Antigravity's
// hooks must, and never fails the agent.
func agyHookCmd(args []string) error {
	event := ""
	if len(args) > 0 {
		event = args[0]
	}
	var in struct {
		Conversation string   `json:"conversationId"`
		Workspaces   []string `json:"workspacePaths"`
		Invocation   int      `json:"invocationNum"` // 0 at a turn's start
		ToolCall     struct {
			Name string          `json:"name"`
			Args json.RawMessage `json:"args"`
		} `json:"toolCall"` // PreToolUse's
	}
	json.NewDecoder(io.LimitReader(os.Stdin, 1<<20)).Decode(&in)
	out := map[string]any{}
	defer func() { json.NewEncoder(os.Stdout).Encode(out) }()
	pane := os.Getenv("OMATERM_PANE")
	if event == "PreToolUse" {
		// Antigravity blocks a call whose hook gives no decision.
		out["decision"] = "allow"
		if pane == "" {
			return nil
		}
		if why := nudge("agy", in.Conversation, in.ToolCall.Name, in.ToolCall.Args); why != "" {
			out["decision"], out["reason"] = "deny", why
		}
		return nil
	}
	if pane == "" || in.Conversation == "" || event == "PreInvocation" && in.Invocation != 0 {
		return nil
	}
	c, err := connect()
	if err != nil {
		return nil
	}
	defer c.Close()
	dir := ""
	if len(in.Workspaces) > 0 {
		dir = in.Workspaces[0]
	}
	var r proto.AgentSessionResult
	if c.Call("pane.agentSession", proto.AgentSessionParams{PID: os.Getpid(), Agent: "agy", Session: in.Conversation, Dir: dir}, &r) != nil {
		return nil
	}
	if event != "SessionStart" {
		return nil
	}
	forgetAgentName(c, r.Pane, r.Session)
	out["injectSteps"] = []any{map[string]any{
		"systemMessage": map[string]string{"systemMessage": hookContext(c, r.Pane, r.Session, r.Away)},
	}}
	return nil
}

// forgetAgentName drops the name an agent gave session sid, as a new
// conversation starts in its first pane, the one the name came from: the
// session shows the pane's title again.
func forgetAgentName(c *client.Client, pane, sid string) {
	var st proto.State
	if c.Call("state", nil, &st) != nil {
		return
	}
	for _, s := range st.Sessions {
		if s.ID == sid && s.AgentNamed && len(s.Panes) > 0 && s.Panes[0].ID == pane {
			c.Call("rename", proto.RenameParams{ID: sid}, nil)
		}
	}
}

// contextCmd prints what to tell the agent of an agent's session, for an
// agent whose commands run away from its pane, as OpenCode's do in its
// service: nothing when no pane runs that session. wait gives a pane that
// runs the agent a moment to tell its session.
func contextCmd(agent, session string, wait bool) error {
	c, err := connect()
	if err != nil {
		return nil
	}
	defer c.Close()
	for deadline := time.Now().Add(1500 * time.Millisecond); ; time.Sleep(150 * time.Millisecond) {
		var st proto.State
		if c.Call("state", nil, &st) != nil {
			return nil
		}
		pending := false
		for _, s := range st.Sessions {
			for _, p := range s.Panes {
				if a := p.AgentSession; a != nil && a.Agent == agent && a.ID == session {
					fmt.Println(hookContext(c, p.ID, s.ID, true))
					return nil
				}
				pending = pending || p.Agent == agent && (p.AgentSession == nil || p.AgentSession.ID != session)
			}
		}
		if !wait || !pending || time.Now().After(deadline) {
			return nil
		}
	}
}

// hookContext is what the hook tells an agent in pane, of session sid;
// c is omaterm, nil when it is unreachable. away tells an agent whose
// commands don't run in its pane, and don't have its variables.
func hookContext(c *client.Client, pane, sid string, away bool) string {
	var st proto.State
	known := c != nil && c.Call("state", nil, &st) == nil
	// Where the pane is: an agent sees a folder that isn't its own.
	where := ""
	if known {
		where = paneWhere(st, pane)
	}
	var b strings.Builder
	if away {
		fmt.Fprintf(&b, "You are running in omaterm, a terminal for coding agents: session %s, pane %s%s. "+
			"The `omaterm` command (see the omaterm skill) runs things beside you, such as a dev server, tests or another agent. "+
			"Your commands don't run in your pane and don't have its $OMATERM_SESSION and $OMATERM_PANE: give omaterm your pane as the target, "+
			"as in `omaterm split %s --down -- npm run dev`.", sid, pane, where, pane)
	} else {
		fmt.Fprintf(&b, "You are running in omaterm, a terminal for coding agents: session %s, pane %s ($OMATERM_SESSION and $OMATERM_PANE; "+
			"$OMATERM_PROJECT and $OMATERM_PROJECT_DIR name your project and its folder)%s. "+
			"The `omaterm` command (see the omaterm skill) runs things beside you, such as a dev server, tests or another agent, "+
			"and tells the user what you are doing.", sid, pane, where)
	}
	if known {
		b.WriteString(sessionNotes(st, sid))
	}
	b.WriteString(firstChoice(st, pane))
	return b.String()
}

// firstChoice tells the agent to use omaterm before tools of its own that
// do the same: a browser, subagents, a background shell. It names only
// what works now, so that the agent doesn't learn from a failure to leave
// omaterm alone.
func firstChoice(st proto.State, pane string) string {
	agent, dir := "claude", ""
	for _, s := range st.Sessions {
		for _, p := range s.Panes {
			if p.ID != pane {
				continue
			}
			if p.Agent != "" {
				agent = p.Agent
			}
			for _, pr := range st.Projects {
				if pr.ID == s.Project {
					dir = pr.Dir
				}
			}
		}
	}
	var b strings.Builder
	b.WriteString("\n\nUse omaterm before your own tools where it does what you need, as the user watches omaterm, not your tool calls:")
	if agentsControlApp() {
		b.WriteString("\n- To look at a web page, click through it or screenshot it: a browser pane, `omaterm split --url <url>`, " +
			"then `omaterm browser text|click|type|screenshot`, rather than a browser tool of your own (a browser plugin, MCP server or subagent, or a headless browser).")
	} else {
		b.WriteString("\n- To show the user a web page: a browser pane, `omaterm split --url <url>`. Driving it with `omaterm browser` " +
			"needs the setting “Let Agents Control the App” (Settings › Agents), which is off: when you need to click through or read a page, " +
			"ask the user to turn it on before reaching for another browser.")
	}
	b.WriteString("\n- For work worth splitting, or changes made in parallel: agents of their own, each in a worktree, " +
		"`omaterm new --worktree <branch> -- " + agent + "`, `omaterm ask <branch> \"<its part>\" --no-wait`, then `omaterm merge <branch>`, " +
		"rather than subagents, which stay fine for read-only searching and reading.")
	var names []string
	if dir != "" {
		defs, _ := procs.LoadFile(dir)
		for _, d := range defs {
			names = append(names, d.Name)
		}
	}
	if len(names) > 0 {
		b.WriteString("\n- For a dev server or a watcher: the project's processes (" + strings.Join(names, ", ") +
			"), `omaterm ps` and `omaterm start <name> --wait`, rather than starting your own.")
	} else {
		b.WriteString("\n- For a dev server or a watcher: `omaterm split --down -- <command>`, rather than a background shell.")
	}
	b.WriteString("\nUse your own tools when omaterm can't do what's needed, and say why.")
	return b.String()
}

// agentsControlApp tells whether the setting “Let Agents Control the App”
// is on, which omaterm browser needs.
func agentsControlApp() bool {
	b, err := os.ReadFile(filepath.Join(paths.ConfigDir(), "settings.json"))
	if err != nil {
		return false
	}
	var s struct {
		RemoteControl bool `json:"remoteControl"`
	}
	return json.Unmarshal(b, &s) == nil && s.RemoteControl
}

// sessionNotes tells the agent what to know of its session: its name,
// the worktree it works in, and the session that started it.
func sessionNotes(st proto.State, sid string) string {
	var b strings.Builder
	for _, s := range st.Sessions {
		if s.ID != sid || len(s.Panes) == 0 {
			continue
		}
		switch {
		case s.AgentNamed:
			fmt.Fprintf(&b, " This conversation named the session %q: leave its name alone.", s.Name)
		case s.Name != "":
			fmt.Fprintf(&b, " The user knows this session as %q: leave its name alone.", s.Name)
		}
		parent, ok := sessionByID(st, s.Parent)
		if w := s.Worktree; w != nil {
			who := "the user reviews it and merges it"
			if ok && parent.Worktree != nil && parent.Worktree.Branch == w.Base {
				who = fmt.Sprintf("the agent in session %q, which started this one, merges it", parent.Name)
			}
			fmt.Fprintf(&b, " This session works in a git worktree, on a branch of its own, %s, made from %s: "+
				"commit your work on it there; %s into %s.", w.Branch, w.Base, who, w.Base)
		}
		if ok {
			fmt.Fprintf(&b, " The agent in session %q (pane %s) started this session: "+
				"answer the messages it sends you with omaterm reply.", parent.Name, agentPane(parent))
		}
	}
	return b.String()
}

// sessionByID finds a session of st.
func sessionByID(st proto.State, id string) (proto.Session, bool) {
	for _, s := range st.Sessions {
		if s.ID == id && id != "" {
			return s, true
		}
	}
	return proto.Session{}, false
}

// agentPane is the pane that speaks for a session to other agents: the
// first that runs an agent, else its focused one.
func agentPane(s proto.Session) string {
	for _, p := range s.Panes {
		if p.Agent != "" {
			return p.ID
		}
	}
	if s.Focus != "" {
		return s.Focus
	}
	if len(s.Panes) > 0 {
		return s.Panes[0].ID
	}
	return ""
}

// paneWhere says which project and folder pane is in, as ", in project
// oterm (~/Projects/oterm)"; "" when it isn't found.
func paneWhere(st proto.State, pane string) string {
	for _, s := range st.Sessions {
		for _, p := range s.Panes {
			if p.ID != pane {
				continue
			}
			name := s.Project
			for _, pr := range st.Projects {
				if pr.ID == s.Project {
					name = pr.Name
				}
			}
			return fmt.Sprintf(", in project %s (%s)", name, shortDir(p.WorkDir()))
		}
	}
	return ""
}

// integration is what omaterm installs in an agent, beside its skill, to
// hook it into omaterm: a hook in its settings, or a plugin of its own.
type integration interface {
	name() string // the agent, as `omaterm hook` takes it
	path() string // where it goes
	state() string
	install() error
	remove() error
}

// agentHook is a hook omaterm installs in an agent's settings: a SessionStart
// command running `omaterm hook <agent>`, and a PreToolUse one.
type agentHook struct {
	agent string // as `omaterm hook` takes it: "claude", "codex"
	file  string // the JSON file holding the agent's hooks
}

// hookEvent is an event of the agent's that omaterm's hook runs on, and its
// matcher, if any.
type hookEvent struct{ name, matcher string }

// events are the events omaterm hooks: the agent's tool calls too, to
// nudge it toward omaterm (nudge). Codex's hook takes every tool, as
// Codex names its shell differently by version.
func (h agentHook) events() []hookEvent {
	if h.agent == "claude" {
		return []hookEvent{{"SessionStart", ""}, {"PreToolUse", claudeToolMatcher}}
	}
	return []hookEvent{{"SessionStart", ""}, {"PreToolUse", ""}}
}

func (h agentHook) name() string { return h.agent }
func (h agentHook) path() string { return h.file }

// omaterm is the omaterm command of omaterm's own directory (package
// clilink), which stays where it is as omaterm updates.
func omaterm() string {
	exe := filepath.Join(paths.BinDir(), "omaterm")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	return exe
}

// commandIsThere tells whether omaterm's command is where hooks run it: a
// hook that runs a missing file fails at every start, so it isn't current.
func commandIsThere() bool {
	_, err := os.Stat(omaterm())
	return err == nil
}

// command is the hook's command line: the omaterm command of omaterm's own
// directory (package clilink), which stays where it is as omaterm updates.
func (h agentHook) command() string {
	if runtime.GOOS == "windows" {
		return `"` + omaterm() + `" hook ` + h.agent
	}
	return "'" + strings.ReplaceAll(omaterm(), "'", `'\''`) + "' hook " + h.agent
}

// ours tells omaterm's hook commands, of this omaterm or another's.
func (h agentHook) ours(command string) bool {
	return strings.Contains(command, "omaterm") && strings.HasSuffix(command, " hook "+h.agent)
}

// state is "current", "outdated" (omaterm's hook, of another command line
// or matcher, or missing on one of its events) or "missing".
func (h agentHook) state() string {
	b, err := os.ReadFile(h.file)
	if err != nil {
		return "missing"
	}
	settings, err := parseObject(b)
	if err != nil {
		return "missing"
	}
	found, current := 0, 0
	for _, ev := range h.events() {
		n, exact := 0, false
		h.each(settings, ev.name, func(command, matcher string) {
			n++
			exact = exact || command == h.command() && matcher == ev.matcher
		})
		found += n
		if n == 1 && exact {
			current++
		}
	}
	switch {
	case found == 0:
		return "missing"
	case current < len(h.events()) || found > current || !commandIsThere():
		return "outdated"
	}
	return "current"
}

// each calls fn with the command, and the matcher of its group, of each
// of omaterm's hooks on event.
func (h agentHook) each(settings object, event string, fn func(command, matcher string)) {
	hooks, _ := parseObject(settings.get("hooks"))
	var groups []object
	json.Unmarshal(hooks.get(event), &groups)
	for _, g := range groups {
		var matcher string
		json.Unmarshal(g.get("matcher"), &matcher)
		var handlers []struct{ Type, Command string }
		json.Unmarshal(g.get("hooks"), &handlers)
		for _, hd := range handlers {
			if hd.Type == "command" && h.ours(hd.Command) {
				fn(hd.Command, matcher)
			}
		}
	}
}

// install puts the hook in the agent's settings, in place of omaterm's
// others, keeping the rest of the file as it is.
func (h agentHook) install() error {
	linkCLI()
	return h.edit(true)
}

// linkCLI makes the omaterm command that hooks and plugins run, should omaterm
// not have made it yet.
func linkCLI() {
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		bin := paths.BinDir()
		if clilink.Target(bin) == "" {
			clilink.Link(exe, bin)
		}
	}
}

// remove takes omaterm's hooks out of the agent's settings.
func (h agentHook) remove() error {
	if _, err := os.Stat(h.file); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return h.edit(false)
}

func (h agentHook) edit(add bool) error {
	b, err := os.ReadFile(h.file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	settings := object{}
	if len(bytes.TrimSpace(b)) > 0 {
		if settings, err = parseObject(b); err != nil {
			return fmt.Errorf("%s: %w", shortDir(h.file), err)
		}
	}
	hooks, err := parseObject(settings.get("hooks"))
	if err != nil {
		return fmt.Errorf("%s: hooks: %w", shortDir(h.file), err)
	}
	// Already there, alone: nothing to do.
	if st := h.state(); add && st == "current" || !add && st == "missing" {
		return nil
	}
	for _, ev := range h.events() {
		var groups []object
		if raw := hooks.get(ev.name); raw != nil {
			if err := json.Unmarshal(raw, &groups); err != nil {
				return fmt.Errorf("%s: hooks.%s: %w", shortDir(h.file), ev.name, err)
			}
		}
		// Drop omaterm's handlers, and the groups they leave empty.
		kept := groups[:0]
		for _, g := range groups {
			var handlers []json.RawMessage
			json.Unmarshal(g.get("hooks"), &handlers)
			n := len(handlers)
			handlers = slices.DeleteFunc(handlers, func(raw json.RawMessage) bool {
				var hd struct{ Type, Command string }
				json.Unmarshal(raw, &hd)
				return hd.Type == "command" && h.ours(hd.Command)
			})
			if n > 0 && len(handlers) == 0 {
				continue
			}
			if len(handlers) != n {
				g = g.set("hooks", marshal(handlers))
			}
			kept = append(kept, g)
		}
		groups = kept
		if add {
			handler := object{{"type", marshal("command")}, {"command", marshal(h.command())}, {"timeout", marshal(10)}}
			group := object{}
			if ev.matcher != "" {
				group = group.set("matcher", marshal(ev.matcher))
			}
			groups = append(groups, group.set("hooks", marshal([]object{handler})))
		}
		if len(groups) > 0 {
			hooks = hooks.set(ev.name, marshal(groups))
		} else {
			hooks = hooks.del(ev.name)
		}
	}
	if len(hooks) > 0 {
		settings = settings.set("hooks", hooks.marshal())
	} else {
		settings = settings.del("hooks")
	}
	out := settings.marshal()
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, out, "", "  "); err != nil {
		return err
	}
	pretty.WriteByte('\n')
	if bytes.Equal(pretty.Bytes(), b) {
		return nil
	}
	return writeFileAtomic(h.file, pretty.Bytes())
}

// agentPlugin is a plugin omaterm installs for an agent: files of its own,
// in a directory the agent loads plugins from.
type agentPlugin struct {
	agent string
	dir   string            // where the agent loads it from
	files map[string]string // by name in dir: the asset (package agents)
	own   bool              // dir is the plugin's alone, to remove with it
}

//go:embed agents
var agentAssets embed.FS

func (p agentPlugin) name() string { return p.agent }

func (p agentPlugin) path() string {
	if p.own || len(p.files) != 1 {
		return p.dir
	}
	for name := range p.files {
		return filepath.Join(p.dir, name)
	}
	return p.dir
}

// content is a file as omaterm writes it: the asset, with the omaterm command
// it runs.
func (p agentPlugin) content(asset string) []byte {
	b, _ := agentAssets.ReadFile(asset)
	return bytes.ReplaceAll(b, []byte("__OMATERM__"), marshal(omaterm()))
}

func (p agentPlugin) state() string {
	same, there := 0, 0
	for name, asset := range p.files {
		b, err := os.ReadFile(filepath.Join(p.dir, name))
		if err != nil {
			continue
		}
		there++
		if bytes.Equal(b, p.content(asset)) {
			same++
		}
	}
	switch {
	case same == len(p.files) && commandIsThere():
		return "current"
	case there == 0:
		return "missing"
	}
	return "outdated"
}

func (p agentPlugin) install() error {
	linkCLI()
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return err
	}
	for name, asset := range p.files {
		if err := writeFileAtomic(filepath.Join(p.dir, name), p.content(asset)); err != nil {
			return err
		}
	}
	return nil
}

func (p agentPlugin) remove() error {
	for name := range p.files {
		if err := os.Remove(filepath.Join(p.dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if p.own {
		os.Remove(p.dir) // if nothing else is in it
	}
	return nil
}

// agyHook is omaterm's hook in Antigravity's hooks.json, whose top-level
// keys name hooks: "omaterm", running `omaterm hook agy` as a conversation
// starts (SessionStart), before each model call (PreInvocation), since a
// resumed conversation starts no session, and before its subagents' and
// browser's tool calls (PreToolUse).
type agyHook struct {
	file string // ~/.gemini/config/hooks.json
}

// agyHookName is omaterm's key in hooks.json.
const agyHookName = "omaterm"

func (h agyHook) name() string { return "agy" }
func (h agyHook) path() string { return h.file }

// spec is omaterm's entry, as it should be: PreToolUse's handlers go in a
// group of their matcher.
func (h agyHook) spec() json.RawMessage {
	handler := func(event string) object {
		cmd := agentHook{agent: "agy"}.command() + " " + event
		return object{{"type", marshal("command")}, {"command", marshal(cmd)}, {"timeout", marshal(10)}}
	}
	handlers := func(event string) json.RawMessage { return marshal([]object{handler(event)}) }
	tools := marshal([]object{{{"matcher", marshal(agyToolMatcher)}, {"hooks", marshal([]object{handler("PreToolUse")})}}})
	return object{{"SessionStart", handlers("SessionStart")}, {"PreInvocation", handlers("PreInvocation")}, {"PreToolUse", tools}}.marshal()
}

func (h agyHook) read() (object, []byte, error) {
	b, err := os.ReadFile(h.file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return object{}, b, nil
	}
	hooks, err := parseObject(b)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", shortDir(h.file), err)
	}
	return hooks, b, nil
}

// state is "current", "outdated" (omaterm's entry, of another command
// line) or "missing".
func (h agyHook) state() string {
	hooks, _, err := h.read()
	if err != nil {
		return "missing"
	}
	raw := hooks.get(agyHookName)
	if raw == nil {
		return "missing"
	}
	var have, want bytes.Buffer
	if json.Compact(&have, raw) != nil || json.Compact(&want, h.spec()) != nil || have.String() != want.String() {
		return "outdated"
	}
	if !commandIsThere() {
		return "outdated"
	}
	return "current"
}

func (h agyHook) install() error {
	linkCLI()
	return h.edit(true)
}

func (h agyHook) remove() error {
	if _, err := os.Stat(h.file); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return h.edit(false)
}

// edit puts omaterm's entry in hooks.json, or takes it out, keeping the
// user's other hooks as they are.
func (h agyHook) edit(add bool) error {
	hooks, b, err := h.read()
	if err != nil {
		return err
	}
	if add {
		hooks = hooks.set(agyHookName, h.spec())
	} else {
		hooks = hooks.del(agyHookName)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, hooks.marshal(), "", "  "); err != nil {
		return err
	}
	pretty.WriteByte('\n')
	if bytes.Equal(pretty.Bytes(), b) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(h.file), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(h.file, pretty.Bytes())
}

// agentHooks returns what omaterm installs to hook agents into omaterm: for
// Claude Code, and for Codex, Pi, OpenCode and Antigravity when they are
// installed (or named).
func agentHooks(agent string) []integration {
	home := paths.Home()
	dirOf := func(env string, def ...string) string {
		if d := os.Getenv(env); d != "" {
			return d
		}
		return filepath.Join(append([]string{home}, def...)...)
	}
	claude := dirOf("CLAUDE_CONFIG_DIR", ".claude")
	codex := dirOf("CODEX_HOME", ".codex")
	pi := dirOf("PI_CODING_AGENT_DIR", ".pi", "agent")
	opencode := filepath.Join(dirOf("XDG_CONFIG_HOME", ".config"), "opencode")
	agy := antigravityConfig()
	installed := func(dir string) bool {
		st, err := os.Stat(dir)
		return err == nil && st.IsDir()
	}
	all := []struct {
		agent, dir string
		it         integration
	}{
		{"claude", claude, agentHook{"claude", filepath.Join(claude, "settings.json")}},
		{"codex", codex, agentHook{"codex", filepath.Join(codex, "hooks.json")}},
		{"pi", pi, agentPlugin{agent: "pi", dir: filepath.Join(pi, "extensions"), files: map[string]string{"omaterm.ts": "agents/pi.ts"}}},
		{"opencode", opencode, agentPlugin{agent: "opencode", dir: filepath.Join(opencode, "plugins", "omaterm"), own: true, files: map[string]string{
			"package.json": "agents/opencode/package.json", "index.js": "agents/opencode/index.js", "tui.js": "agents/opencode/tui.js",
		}}},
		{"agy", agy, agyHook{file: filepath.Join(agy, "hooks.json")}},
	}
	var out []integration
	for _, a := range all {
		switch {
		case agent == a.agent,
			a.agent == "claude" && agent == "",
			// The shared skill's agents: Antigravity reads a skill of its own.
			a.agent != "claude" && (agent == "" || agent == "agents" && a.agent != "agy") && installed(a.dir):
			out = append(out, a.it)
		}
	}
	return out
}

// object is a JSON object that keeps its keys' order, so that editing a
// settings file leaves the rest of it as it was.
type object []member

type member struct {
	Key string
	Val json.RawMessage
}

func parseObject(b []byte) (object, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return object{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	var o object
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o = o.set(t.(string), v)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("more than one JSON value")
	}
	return o, nil
}

func (o *object) UnmarshalJSON(b []byte) error {
	p, err := parseObject(b)
	*o = p
	return err
}

func (o object) get(key string) json.RawMessage {
	for _, m := range o {
		if m.Key == key {
			return m.Val
		}
	}
	return nil
}

func (o object) set(key string, v json.RawMessage) object {
	for i, m := range o {
		if m.Key == key {
			o[i].Val = v
			return o
		}
	}
	return append(o, member{key, v})
}

func (o object) del(key string) object {
	return slices.DeleteFunc(o, func(m member) bool { return m.Key == key })
}

func (o object) marshal() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(marshal(m.Key))
		b.WriteByte(':')
		b.Write(m.Val)
	}
	b.WriteByte('}')
	return b.Bytes()
}

func (o object) MarshalJSON() ([]byte, error) { return o.marshal(), nil }

func marshal(v any) json.RawMessage {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
	return bytes.TrimSpace(b.Bytes())
}

// writeFileAtomic replaces a file whole, keeping its mode.
func writeFileAtomic(path string, b []byte) error {
	// Through a link to its target: renamed over, a dotfiles manager's
	// link (stow, home-manager) became a plain file it no longer tracked.
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return atomicfile.Rename(tmp.Name(), path)
}
