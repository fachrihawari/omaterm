// Adapted from Kero (github.com/egoist/kero, MIT, docs/third_party/kero-LICENSE),
// internal/cli/skill.go, the skill's text included.

package cli

import (
	"fmt"
	"omaterm/internal/paths"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// skillGuide teaches a coding agent omaterm: the skill `omaterm skill setup`
// installs, from this binary, so that it follows omaterm's updates.
const skillGuide = `---
name: omaterm
description: Drive terminal sessions in omaterm — start agents and commands in their own sessions or panes, type into them, wait for them, read their screens, ask other agents and answer them, and report your own status. Use when running inside omaterm ($OMATERM_PANE is set) and the task needs another terminal, a long-running process, a parallel agent, another agent's help or review, or the user's attention.
---

# omaterm

You run inside omaterm, a terminal for coding agents whose sessions keep
running in the background. $OMATERM_SESSION is your session and $OMATERM_PANE your pane; the
` + "`omaterm`" + ` command drives them. A target is a session or a pane id (a
prefix works), or a session's name; a session stands for its focused pane.

## Run something beside you

    omaterm split --down -- npm run dev          # a pane under yours; prints its id
    omaterm new -n review -- claude              # a session of its own
    omaterm new -n tests -d ~/code/app -- go test ./...
    omaterm split --url localhost:3000           # a browser pane, to show the user a page

What you start stays in your own project: new and split refuse a target in
another project, unless you pass --any-project because the user asked for it.

## Work on a branch of its own

    omaterm new --worktree fix-login -- claude   # an agent in a git worktree of its own

A worktree session works in .worktrees/<branch> of the repository, on a
new branch made from yours (--base <branch> picks another), with the
repository's ignored .env files copied in (--no-env leaves them out).
Several agents can work in parallel this way without touching each
other's files, or yours. The user reviews each branch and merges it, or
discards it, from omaterm; omaterm close ends the session and keeps its
worktree.

## Work with other agents

    omaterm ask w1 "Does the login keep the session cookie?"   # waits, prints its answer
    omaterm ask w1 "Fix the login test" --no-wait  # its answer comes to you later
    omaterm reply "Fixed: the cookie expired"      # answer a message you were sent
    omaterm reply m7 --file notes.md               # a given message, a long answer
    omaterm inbox                                  # your messages, and their answers

A message from omaterm ask waits until the other agent is at its prompt,
then goes in as its prompt, saying who it is from and its id. Answer
every message you are sent with omaterm reply. An answer to an ask you
didn't wait for comes to you as a message of its own once you are at
your prompt: so for work that takes long, ask with --no-wait and end
your turn. An agent that stops without answering gets you the end of
its screen instead.

To split a task between agents, work in a worktree yourself, then:

    omaterm new --worktree w1 -- claude            # each on a branch made from yours
    omaterm new --worktree w2 -- codex
    omaterm ask w1 "<its part: what, which files, what to leave alone>" --no-wait
    omaterm ask w2 "<its part>" --no-wait
    omaterm wait w1 w2 --all                       # or --any, for the first done
    omaterm diff w1 --branch                       # what w1 changed
    omaterm merge w1                               # into your branch; closes w1
    omaterm merge w2 --commit -m "Add the export"  # a merge commit, not a squash

The sessions you start show under yours. omaterm merge takes only a
worktree made from your own branch, and squashes it unless --commit;
--keep leaves its session and worktree. A conflict is for its agent to
resolve: ask it to merge your branch into its own, then merge again.
Merging into the main checkout is the user's, never yours.

## Drive it

    omaterm send <target> "some text" --enter    # type, then press Enter
    omaterm key <target> ctrl+c                  # enter, escape, up, tab, ctrl+d…
    omaterm prompt <target> "run the tests"      # send + Enter, wait, print screen
    omaterm wait <target> [--for idle,waiting,error,exited] [--timeout 10m]
    omaterm wait <target> --exit                 # until its command ends; exits with its status
    omaterm read <target> [-n 40] [-a]           # the screen, or with scrollback
    omaterm status <target> --json               # status, program, directory
    omaterm ls                                   # every session and pane

A pane's status is working, waiting (it needs the user), idle, error or
exited. ` + "`omaterm wait`" + ` with no --for waits until it stops working. A
command such as a test run doesn't show as working: wait for it with --exit,
or start it with --wait, which exits with the command's status:

    pane=$(omaterm split --down -- go test ./...)
    omaterm wait "$pane" --exit || omaterm read "$pane" -n 80
    omaterm split --down --wait -- make build    # the same, in one step

## Use a web page

A browser pane's page is yours to drive, as a user would, to check what
you built: open it with omaterm split --url, then

    omaterm browser open localhost:3000/todos     # go to a page, wait for it to load
    omaterm browser type '#new-todo' "Buy milk"   # fill a field (--enter submits)
    omaterm browser click 'button[type=submit]'   # click an element (CSS selector)
    omaterm browser wait --text "Welcome"         # or wait <selector>; --timeout 30s
    omaterm browser text [selector]               # the page's text, or an element's
    omaterm browser html [selector]
    omaterm browser eval 'document.title'         # run JavaScript, print its value
    omaterm browser screenshot -o /tmp/page.png   # the pane must be on screen
    omaterm browser back|forward|reload

With no target, omaterm browser drives your session's browser pane; name
one (omaterm browser <target> text) when there are several. It needs the
app open and the palette setting "Let Agents Control the App" on.

## See what you changed

    omaterm diff --turn                        # what your last turn changed
    omaterm diff                               # uncommitted changes
    omaterm diff --branch                      # since the default branch, or your worktree's base

## Tell the user

    omaterm report working "Migrating the database" --progress 40
    omaterm report blocked "Approve the production deploy?" --kind permission
    omaterm report done "Deployed to staging"
    omaterm notify "Need your API key to continue"

Reports are OSC 7501 sequences: omaterm shows them in its sidebar, and
notifies the user when you are blocked or done while they look elsewhere.
Report blocked only when you truly cannot go on without them.

## Etiquette

- Close what you started when it is done: ` + "`omaterm close <target>`" + `.
- Reach another agent with omaterm ask; type with send, key or prompt only
  into panes you started, unless the user asked.
- Never merge into the main checkout: the user reviews and merges.
- Exit status 3 means no such target, 4 that omaterm is not running, or
  that its window is closed for what needs it (focus, browser, do); the
  sessions keep running, and the rest works, while the window is closed.
`

func skillCmd(args []string) error {
	f, err := parse(args, map[string]string{"agent": "agent"}, map[string]string{"dry-run": "dry", "n": "dry"})
	if err != nil {
		return err
	}
	sub := "status"
	if len(f.pos) > 0 {
		sub = f.pos[0]
	}
	agent := strings.ToLower(f.vals["agent"])
	dirs, hooks := skillDirs(agent), hooksFor(agent)
	switch sub {
	case "show":
		fmt.Print(skillGuide)
		return nil
	case "status":
		for _, name := range sortedKeys(dirs) {
			fmt.Printf("%-8s skill  %-9s %s\n", name, skillState(dirs[name]), shortDir(dirs[name]))
		}
		for _, h := range hooks {
			fmt.Printf("%-8s hook   %-9s %s\n", h.name(), h.state(), shortDir(h.path()))
		}
		return nil
	case "setup", "remove":
		for _, name := range sortedKeys(dirs) {
			dir := dirs[name]
			switch {
			case f.set["dry"]:
				fmt.Printf("would %s %s (%s)\n", sub, shortDir(dir), name)
			case sub == "remove":
				if err := os.RemoveAll(dir); err != nil {
					return err
				}
				fmt.Printf("removed %s\n", shortDir(dir))
			default:
				if err := writeSkill(dir); err != nil {
					return err
				}
				fmt.Printf("installed %s (%s)\n", shortDir(dir), name)
			}
		}
		for _, h := range hooks {
			switch {
			case f.set["dry"]:
				fmt.Printf("would %s the hook in %s (%s)\n", sub, shortDir(h.path()), h.name())
			case sub == "remove":
				if err := h.remove(); err != nil {
					return err
				}
				fmt.Printf("removed the hook from %s\n", shortDir(h.path()))
			default:
				if err := h.install(); err != nil {
					return err
				}
				fmt.Printf("installed the hook in %s (%s)\n", shortDir(h.path()), h.name())
			}
		}
		if sub == "setup" && !f.set["dry"] && len(hooks) > 0 {
			fmt.Println("\nAgents started from now on in omaterm learn which session they're in.")
			for _, h := range hooks {
				if note := HookNotes[h.name()]; note != "" {
					fmt.Println(note)
				}
			}
		}
		return nil
	}
	return usageErr("omaterm skill setup|show|status|remove [--agent claude|codex|pi|opencode|agents] [--dry-run]")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func skillState(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	switch {
	case err != nil:
		return "missing"
	case string(b) != skillGuide:
		return "outdated"
	}
	return "current"
}

func writeSkill(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillGuide), 0o644)
}

// hooksFor returns the hooks that go with an --agent: Codex's, Pi's and
// OpenCode's with the skill they share.
func hooksFor(agent string) []integration {
	switch agent {
	case "", "claude", "codex", "pi", "opencode", "agents":
		return agentHooks(agent)
	}
	return nil
}

// skillDirs returns where agents read skills: Claude Code's, and the
// shared one of Codex, OpenCode and others.
func skillDirs(agent string) map[string]string {
	home := paths.Home()
	claude := os.Getenv("CLAUDE_CONFIG_DIR")
	if claude == "" {
		claude = filepath.Join(home, ".claude")
	}
	all := map[string]string{
		"claude": filepath.Join(claude, "skills", "omaterm"),
		"agents": filepath.Join(home, ".agents", "skills", "omaterm"),
	}
	if agent = strings.ToLower(agent); agent != "" {
		if d, ok := all[agent]; ok {
			return map[string]string{agent: d}
		}
		if agent == "codex" || agent == "opencode" || agent == "pi" {
			return map[string]string{"agents": all["agents"]}
		}
	}
	return all
}

// SkillState is where a skill for agents is, and whether it is current,
// with the hooks that go with it.
type SkillState struct {
	Agent string // "claude", or "agents" (Codex, OpenCode, Pi…)
	Dir   string
	State string // "current", "outdated" (or partly installed) or "missing"
}

// HookNotes are what the user still has to do for an agent's new hook.
var HookNotes = map[string]string{
	"claude":   "Claude Code runs hooks only in folders you trust.",
	"codex":    "Codex asks you to approve a new hook: run /hooks in it.",
	"opencode": "OpenCode's background service loads plugins as it starts: run `opencode service restart` when no session is busy.",
}

// HookInstalled tells whether an agent's hook, which tells omaterm the
// agent's conversation, is installed and current; true for an agent
// omaterm has no hook for.
func HookInstalled(agent string) bool {
	switch agent {
	case "claude", "codex", "pi", "opencode":
	default:
		return true
	}
	for _, h := range hooksFor(agent) {
		if h.state() != "current" {
			return false
		}
	}
	return true
}

// SkillStates returns the state of omaterm's skill and hooks for each kind
// of agent.
func SkillStates() []SkillState {
	var out []SkillState
	for _, agent := range []string{"claude", "agents"} {
		dir := skillDirs(agent)[agent]
		states := []string{skillState(dir)}
		for _, h := range hooksFor(agent) {
			states = append(states, h.state())
		}
		st := "outdated"
		switch {
		case !slices.ContainsFunc(states, func(s string) bool { return s != "current" }):
			st = "current"
		case !slices.ContainsFunc(states, func(s string) bool { return s != "missing" }):
			st = "missing"
		}
		out = append(out, SkillState{Agent: agent, Dir: dir, State: st})
	}
	return out
}

// InstallSkill installs, or updates, omaterm's skill and hooks for a kind of
// agent.
func InstallSkill(agent string) error {
	dir := skillDirs(agent)[agent]
	if dir == "" {
		return fmt.Errorf("no agent %q", agent)
	}
	if err := writeSkill(dir); err != nil {
		return err
	}
	for _, h := range hooksFor(agent) {
		if err := h.install(); err != nil {
			return err
		}
	}
	return nil
}

// RemoveSkill removes omaterm's skill and hooks for a kind of agent.
func RemoveSkill(agent string) error {
	dir := skillDirs(agent)[agent]
	if dir == "" {
		return fmt.Errorf("no agent %q", agent)
	}
	for _, h := range hooksFor(agent) {
		if err := h.remove(); err != nil {
			return err
		}
	}
	return os.RemoveAll(dir)
}
