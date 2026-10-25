# Solo (soloterm.com): research dossier, 2026-10-25

## Context
The user asked to learn about Solo, another desktop app for coding agents, to inform omaterm's direction. It follows the Rex, Kero and Tern dossier in [agent-terminals.md](agent-terminals.md). Sources:
- soloterm.com
- /docs, /docs/projects/solo-yml, /docs/mcp-tools, /docs/workflows/agent-orchestration
- /changelog
- /terminal

This file only collects research. No code changes.

---

## 0. At a glance

| | **Solo** | **omaterm** (0.8.4) |
|---|---|---|
| Who | Aaron Francis (faster.dev, Database School, VP Marketing at Laravel); Try Hard Studios, LLC | Fachri Hawari |
| Pitch | "The meta-harness for coding agents." "A layer above the harnesses you already use." | A terminal workspace for running coding agents side by side |
| Source | Closed; only the Nix flake (`github:soloterm/nix`) is public | MIT, everything |
| Price | Free: every feature, 4 projects and 20 processes. Pro: $99/yr, unlimited, 3 devices. Team seats: $99 → $69/seat/yr by volume | Free |
| Stack | Tauri with the system webview. Own Rust terminal core with a WebGL2 renderer | Go plus mygo (native GPU), libghostty-vt |
| Platforms | macOS 11+, Windows 10 1809+ (WSL profiles), Linux x86-64 AppImage since v0.11.1 (2026-10-18) | Linux first; macOS, Windows |
| Agents | Claude Code, Codex, OpenCode, Pi, Gemini CLI, Amp, Copilot CLI, Kimi, Antigravity, Muse, custom | Claude Code, Codex, Gemini, OpenCode, Pi (status), plus any CLI |
| Control | MCP server (stdio), localhost HTTP API with a bearer token, `solo` CLI | `omaterm` CLI over a Unix socket, agent skill and hooks |
| Persistence | Processes stop when Solo quits. It adopts matching processes after a crash. Agents resume | Session daemon: sessions outlive the window and resume after a reboot |
| Remote | "In development": a headless host and an iOS client share the protocol, but no network transport has shipped | Web access through a tunnel or the LAN, with pairing |

Both wrap the CLI agents already installed, with no prompt proxy and no API keys passing through. Both show working, idle, waiting and error status, with notifications and resume.

---

## 1. Product shape
The landing page sells a progressive path:
1. **Day one:** a terminal with vertical tabs and project shells.
2. **As you settle in:** save the project's commands, group projects into workspaces, and stop rebuilding layouts.
3. **When ready:** agents spawn, control and orchestrate terminals, commands and shared context through MCP, HTTP or the CLI.

Daily-work features:
- a ⌘K command palette with scoped search (destinations, actions, agents, terminals, prompt templates);
- workspaces that group projects, each in its own window;
- an Activity view: running work, subprocesses, CPU, memory, ports, kill;
- favorite processes with jump shortcuts;
- clickable file paths;
- light, dark or system theme.

Telemetry is limited usage events through Aptabase, plus Sentry. Optional auto-summaries send a terminal snapshot to an LLM summarizer the user picks.

---

## 2. `solo.yml`: the project's processes
- Commit it to share command definitions with the team. Personal commands stay local, and a command can be moved between local and shared. Runtime state, terminals, agents and credentials never go in it.
- Solo detects commands for Laravel, Node, Next, Nuxt, Rust, Django, Ruby, Go, .NET, Elixir, Svelte, Spring, Remix, Astro, FastAPI and Flask.
- Humans and agents share the running processes instead of each starting their own `npm run dev`.

Schema:
- **Top level:** `name`, `icon` (a path), `icon_initials` (1–2 characters), `processes`.
- **Per process** (keyed by display name):

| Key | Default | Meaning |
|---|---|---|
| `command` | — | the shell command |
| `working_dir` | project root | relative, must stay inside the project |
| `auto_start` | `true` | start when the project opens |
| `auto_restart` | `false` | restart after it exits on its own (rate-limited); a manual stop sticks |
| `restart_when_changed` | — | glob patterns; a change restarts it |
| `env` | — | string-to-string map |

**Trust:**
- New or changed commands from the file are blocked until reviewed and trusted.
- Trust is stored locally per exact configuration. A rename with the same command keeps its trust.
- Projects can opt into trusting future changes automatically.
- The file is limited to 1 MB.

**Environment:**
- On Unix, commands get the login shell's environment, merged with the command's own `env`.
- PATH problems have their own troubleshooting page.

---

## 3. Orchestration primitives
These are exposed through MCP. The HTTP API and CLI cover most of them too.

- **Agent spawning:** a lead agent lists agent tools, spawns workers (across vendors, e.g. Codex → Claude), sends input, reads output and waits. Children nest under their parent in the sidebar. `close_process` closes only its target and detaches the target's children.
- **Scratchpads:** durable, project-scoped Markdown, edited by humans in the app and read and written by agents. A setting turns them on.
- **Linked todos:** tags, blockers (dependencies), locks and comments. A setting turns them on.
- **Prompt templates:** a reusable, agent-independent library, with placeholders, inserted with a keystroke. Off by default.
- **Timers:** schedule a future user turn for an agent, e.g. "wake me when all current workers are idle". This replaces polling.
- **Key-value:** project-scoped JSON. Off by default.
- **Coordination:** lease locks.
- **Services:** find the project's localhost URLs and wait until a dev server is ready.
- **Output:** saved terminal output and discovered ports.

MCP tool groups: project, workspace, services, process, bulk, output, agent and terminal, coordination, setup and support, scratchpads, todos, prompt templates, timers, key-value, prompts and resources.

Usage claimed for July 2026: 72,556 MCP calls. The top three are `get_process_output` (14,514), `get_process_status` (13,117) and `send_input` (7,235). Agents mostly read output, check status and type.

**The recommended workflow** (/docs/workflows/agent-orchestration):
1. The lead interviews the user, one question at a time.
2. It writes the plan to a scratchpad.
3. It turns the plan into todos with blockers.
4. It spawns workers for todos that aren't blocked, each owning separate files.
5. It gives each worker a narrow prompt: the objective, the files it owns, what not to touch, and what to report back.
6. It watches the workers' statuses and reads their real output.
7. It waits on idle timers instead of polling.
8. It integrates one lane at a time.
9. It saves each worker's handoff to a todo or scratchpad, then closes the worker.

Auto-summaries are for triage only.

---

## 4. The terminal core (soloterm.com/terminal, offered for licensing)
- **Architecture:** a Rust core runs beside each process and owns the screen, modes and scrollback. Viewers only paint, with WebGL2 and a glyph atlas, falling back to Canvas 2D.
- **Protocol:**
  - snapshots, then MessagePack frame patches coalesced to about 60/s per viewer, plus history deltas;
  - many viewers per session, each with its own sequence numbers and credit-based flow control: at most 8 messages or 1 MiB in flight;
  - a viewer that falls too far behind is sent a fresh snapshot;
  - on reattach it resumes if the gap is at most 256 patches, else it gets a snapshot.
- **Memory:** 10,000 lines of scrollback in 256-line pages. Viewers keep about 2,000 lines. Raw output is capped at 10 MiB.
- **Emulation:** alternate screen, mode 2026, bracketed paste, SGR mouse, focus events, Kitty keyboard, OSC 8 and 52 (52 is copy only), true color, wide characters. No Sixel or Kitty graphics.
- **Testing:** cases imported from Alacritty, libvterm, xterm.js and vttest, plus property fuzzing and recordings of lazygit, tmux and vim.
- **Compared with others:**
  - Solo replaced xterm.js v6 after about seven weeks.
  - It says libghostty-vt lacks a patch protocol, backpressure, reattach fences and multi-client delivery.
  - omaterm builds those on top of libghostty-vt in its daemon: `terminal.Mirror` frames, and a slow client is dropped and re-snapshotted.

---

## 5. Release history (from /changelog; earliest visible is v0.7.1)

| Version | Date | Notable |
|---|---|---|
| 0.7.1 | 2026-05-14 | Copilot and Kimi CLIs; HTTP API and CLI cover processes, projects, todos, scratchpads |
| 0.8.2 | 2026-06-05 | Windows/WSL execution profiles; prompt templates; wider API, CLI and MCP |
| 0.9.3 | 2026-06-21 | One-click "Connect Solo" for agent CLIs; favorite processes |
| 0.10.0 | 2026-08-27 | Workspaces with their own windows; redesigned palette; Antigravity; images in scratchpads and todos; lower input latency |
| 0.11.1 | 2026-10-18 | **Linux** AppImage; Muse; resume prompts after quit or crash; terminal output about 2× faster; terminal input through HTTP, CLI and routines |

Solo ships about once a month. omaterm shipped 14 releases in its first three weeks.

---

## 6. Takeaways for omaterm
1. **Declared project processes** (a `solo.yml` analogue) are the biggest gap.
   - **What:** a committed file listing the dev server, workers, log tails and tunnels, with auto-start, auto-restart, restart-on-change and env, and trust review for new or changed commands.
   - **Why:** humans and agents share one running `npm run dev`.
   - **Fit:** the Info panel already lists processes and ports, and the daemon keeps processes alive past the window, which Solo can't.
   - **Agent side:** `omaterm services` finds the URL and waits until it's ready.
2. **Waiting on many panes at once.**
   - **What:** `omaterm wait` for any or all of several panes to go idle (Solo's idle timers).
   - **Why:** agents that split work across workers stop polling.
   - **Cost:** cheap; `session.Await` exists.
3. **Parent and child agents.**
   - **What:** a session created from another pane (whose `$OMATERM_PANE` is set) nests under it in the sidebar. Closing the parent asks about the children.
4. **An MCP server.**
   - **What:** expose the existing CLI surface (`ls`, `new`, `split`, `send`, `read`, `wait`, `report`, `browser`) as MCP tools, for agents that prefer tools to shell commands.
   - **Why:** Solo's numbers show reading output, checking status and typing dominate, and omaterm's CLI already covers all three.
5. **Later, if at all:**
   - **Scratchpads, todos and prompt templates:** agents have their own todo and memory systems, and these widen omaterm's scope a lot.
   - **Workspaces as windows:** a separate question.
   - **LLM auto-summaries:** send terminal content to a provider, against omaterm's local-only stance.
6. **Where omaterm already leads, and should say so:**
   - sessions that survive quitting the app;
   - web access from a phone, shipped (Solo's remote is unreleased);
   - Git, diffs, Last turn, the editor, and browser panes that agents drive;
   - open source and free with no project limits;
   - native rendering;
   - Omarchy themes.
7. **Positioning.** Solo just reached Linux, as an AppImage, and charges past 4 projects. omaterm stays the free, open, Linux-first option. omaterm doesn't take ideas from Solo yet, so it isn't credited on the site.
