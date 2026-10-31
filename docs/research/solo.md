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

## 6. What fits omaterm (deep pass, 2026-10-25)

Sources for this pass:
- every page of Solo's docs, through its Markdown API (`soloterm.com/api/v1/docs/<section>/<page>`; search at `/api/v1/docs/search?q=`);
- a map of omaterm's code at 0.8.5.

The verdict for each idea:
- **Adopt:** worth having in omaterm.
- **Adapt:** worth having, done omaterm's way.
- **Skip:** not for omaterm.

### 6.1 Project processes: **adopt, and do it better than Solo** (size L)
This is the headline. Solo's `solo.yml` is a manifest of a project's dev processes, which humans and agents share. omaterm can do the same and add what Solo can't:
- **Processes run in the session daemon.** They keep running with the window closed. As a systemd service they start at login, before any window opens. Solo stops every process when it quits.
- **They live as panes.** Status, scrollback, the Info panel's process and port lists, web access and the CLI all apply to them without new plumbing.

**Proposed shape**, by name (file format to decide, see 6.10):
- **The file:** `omaterm.yml` at the project root, committed. Entries are keyed by display name:
  - `command`
  - `dir` (relative, must stay inside the project)
  - `env` (string map)
  - `autostart` (default **false**; Solo defaults to true, but omaterm's stance is "programs are not run again, unasked", `restore.go:3-7`)
  - `restart: always | on-failure | never`
  - `watch: [globs]` (restart when a matching file changes)
  - `port` (optional; the port that means "ready")
- **Local processes:** stored in `state.json` beside the project, not committed. They can be moved to or from the file, as in Solo.
- **The sidebar:** each project gets a **Processes** group under its sessions. Each row shows:
  - state: running, starting, stopped, failed, untrusted, or restart paused;
  - a port badge with Open;
  - single-key actions (start/stop `S`, restart `R`).

  Project-level actions: Start all, Stop all, Restart running.
- **Trust,** copied from Solo, which got it right:
  - Keyed by a hash of the variant: command, dir, env, restart and watch, with the watch patterns sorted.
  - Stored only on this machine, for example `~/.config/omaterm/trust.json` beside `web.json`.
  - A rename keeps its trust, and so does a branch switch back to a known variant.
  - Untrusted commands never start, by any route. Stop always works.
- **Sync:** watch the file and compare hashes. When it changes, show a "omaterm.yml changed" banner with a **Sync** button. Syncing never starts or restarts anything by itself, and reverting the file clears the banner.
- **Supervisor in the daemon:**
  - Restart after an exit, capped at 10 in 60 s, then "Restart paused" with a notification.
  - A manual Stop sticks.
  - A banner line is written into the pane at each restart.
  - Watch restarts use `mygo.WatchFiles` (Recursive, Ignore, Debounce). Unlike Solo, honor an ignore list: `.gitignore`, `node_modules` and `.git` by default, via `service/discover.go`'s `matchGitGlob`, which already handles `**`.
- **Detection when adding a project:** suggest commands from the files a project already has. Nothing runs and nothing is written until the user picks.
  - Procfile / Procfile.dev
  - `package.json` scripts (package manager from `packageManager`, then lockfiles)
  - Makefile / justfile / Taskfile targets
  - Laravel (serve, queue:listen, logs, schedule)
  - Rails, Django, Go
  - `docker compose up`
  - an existing **`solo.yml`**, imported, which makes switching from Solo trivial

**Pre-work found in the code map.** These are bugs or gaps today, worth fixing on their own:
- `restart` drops the pane's command and starts a bare shell (`host_local.go:631-644`, `offline.go:128-141`).
- `model.Pane.Command` is in the schema but never saved or read (`workspace.go:222`).
- The app ignores `CreateParams.Env` (`host_local.go:437`); the daemon honors it.
- An exited pane closes its tab (`terminal.go:217-227`). A supervised process needs its pane kept, showing "exited (code 1)".
- The app removes daemon panes it doesn't know about when it attaches (`daemonhost.go:77-89`). Supervised panes must be known to both.
- A command runs inside an interactive shell, and `133;D` is seen once (`shellcmd.go:52-68`, `pane.go:462-470`). Supervising cleanly wants the command run directly (`Exec`, today used by tests only), so its exit is the pane's exit.

### 6.2 Services and ports: **adopt** (size M; the Open button alone is S)
- **Fix the stub:** the Info panel's port **Open** button only shows a toast (`inspector.go:216-220`). Open the port in a browser pane (`a.openBrowser`, `browser.go:213`), or in the system browser with Alt.
- **Ports per pane:** the processes under a pane's shell (`service/process.go` `childrenMap` / `descendants`), shown as a port badge on the process or session row.
- **Move the port scan into the daemon.** The `service` package has no UI, so `ls`, the CLI and web access can all use it.
- **New command:** `omaterm services [--wait <name|port>] [--timeout]`. It lists the project's URLs and waits until one accepts connections (TCP connect, then optionally HTTP). Solo's `wait_for_bound_port` is its most recommended tool for agents.
- **Port names:** stop using `/etc/services` names for dev ports. 8080 shows as "http-alt" today. Use the process name instead ("bun", "vite").

### 6.2b Named HTTPS URLs for dev servers: **build in, portless-style** (M overall; phase 1 is S–M)
**Built in 0.12.0 (phase 1).** The proxy, CA and setup flow, with the Settings page. It differs from the plan below:
- The URL is the name before `.localhost` (`url: shop` is `https://shop.localhost`; up to three dotted parts), not `<process>.<project>.localhost`. Two processes can't share a name: the first gets it.
- The CA has a critical name constraint, `.localhost` only, and the privileged step refuses any other certificate.
- Linux and macOS. Windows is not built.
- Not built: worktree subdomains, `omaterm new --url`, a flag table for tools that ignore `PORT`, the optional `/etc/hosts` block, and the `PORTLESS_URL` rule. `PORT` is the only way a server is told its port.
- Where: `internal/devurl` (CA, proxy, setup), the daemon's `proxy.status` and `proxy.reload`, `omaterm proxy`, and `settings_devurls.go`.

[portless](https://github.com/vercel-labs/portless) (Vercel Labs) runs a dev server behind a proxy at a stable `https://<name>.localhost`:
- it gives the app a free port (4000–4999) through `PORT`, and adds `--port`/`--host` for tools that ignore `PORT` (Vite, Astro);
- it makes a local CA, trusts it, and serves HTTP/2 on 443;
- each linked git worktree gets its own subdomain (`fix-ui.myapp.localhost`).

It works with omaterm today with no code (`command: portless` in a process, or `omaterm new -- portless`). Still, omaterm should build the same thing into the daemon rather than lean on portless.
- **Memory is not the reason.** Measure it before quoting numbers. Expect a Node proxy of about 40–60 MB, plus a Node wrapper of about 40–50 MB that stays alive for each app. That is small next to the dev server itself.
- **Dependency:** it needs Node 24+, which many Go, Rails, Laravel or Django users don't have. It is pre-1.0, and its state format may change. It needs sudo for 443 and for the CA.
- **Integration:** omaterm would have to guess the URL from outside (`PORTLESS_URL` in `/proc/<pid>/environ`).
- **Built in, it is nearly free.** The daemon is already resident, so the proxy is a feature of it: a few MB and no new process. Go has almost everything:

  | Need | In Go |
  |---|---|
  | Reverse proxy, including WebSocket upgrades for HMR | `httputil.ReverseProxy` |
  | HTTP/2 | `net/http` with TLS |
  | Local CA and per-name certificates | `crypto/x509` and `tls.Config.GetCertificate`, minted on demand |
  | CA install | ported from mkcert (Go, BSD): Arch, Debian, Fedora, macOS Keychain, Windows, and NSS for Chrome and Firefox on Linux |

- **What omaterm gains:**
  - A process takes `url: myapp` beside `port:`. The daemon picks the port, sets `PORT` (or the flag), registers the route and serves `https://myapp.localhost`. Nothing has to be guessed.
  - omaterm knows which worktree a session is in, so worktree subdomains come for free. This is the parallel-agents case: three agents in three worktrees run three dev servers with no port clashes, each in its own browser pane.
  - The sidebar badge, the browser pane, the Info panel and `omaterm services --wait web` all show and use the URL, not a random 4xxx port.
  - It works for every stack (`rails s`, `php artisan serve`, `go run`), not just Node.
- **Hard parts:**
  - **Port 443 needs privilege on Linux.** Do not use `setcap`: updates replace the binary and drop it. Use an opt-in sysctl, `net.ipv4.ip_unprivileged_port_start`, through a one-time sudo. macOS has let users bind 443 since Mojave.
  - **CA install** needs sudo once, and testing per distro. This is the bulk of the work.
  - **`*.localhost` resolution:** browsers and curl resolve it to loopback themselves. Tools that use the system resolver may not without systemd-resolved, so an optional `/etc/hosts` block may be needed.
  - **Tools that ignore `PORT`** need a small flag table, shared with detection (6.1).
  - **WebKit browser panes** must trust the CA. Test this.
  - **Phones:** `*.localhost` only resolves on the machine. A phone over web access can't open it. Say so in the docs.
- **Decided (2026-10-25): clean URLs.** The URL is `https://myapp.localhost`, with no port. A one-time sudo is fine, as long as it goes through a proper UI and not a bare terminal prompt.
- **The one-time setup, per OS:**

  | | Port 443 | Trust the CA | Names resolve |
  |---|---|---|---|
  | **Linux** | `/etc/sysctl.d/50-omaterm.conf` sets `net.ipv4.ip_unprivileged_port_start=443`. The UI says this lets any local program use ports 443–1023. | System store (`trust anchor` or `update-ca-certificates`), plus NSS (`~/.pki/nssdb`, Firefox profiles) as the user, with no sudo | Browsers and curl handle it; an `/etc/hosts` block is optional |
  | **macOS** | No root needed since Mojave | `security add-trusted-cert` (admin) | System resolver |
  | **Windows** | No admin needed | `certutil -addstore -user Root`, which shows the system's confirm dialog, with no admin | Browsers handle it |

- **How it is elevated:**
  - Linux uses `pkexec` (polkit), so the desktop's own password dialog shows (hyprpolkitagent on Omarchy).
  - macOS uses `osascript … with administrator privileges`.
  - Windows needs no elevation.
  - The privileged part is `omaterm proxy setup --system`, a small, auditable step with no network access.
  - **Fallback when there is no polkit agent:** open a session running `sudo omaterm proxy setup --system`. omaterm is a terminal, so this is natural.
  - Undo with `omaterm proxy uninstall`, from a button as well.
- **UI: a Settings window** (see 6.12), with a "Dev URLs" page:
  - a live checklist (proxy on 443, certificate trusted by system, Chrome and Firefox, names resolve), each item ✓ or ✗;
  - **Set Up…**, which lists exactly what will change, then asks for the password;
  - **Remove**;
  - the routes: name → URL → process or session, each with Open (browser pane) and Copy.
- **Phases:**
  1. The proxy, CA and setup flow, with the Dev URLs page. A process can use `url:`, or `omaterm new --url myapp -- npm run dev` before `omaterm.yml` exists.
  2. Worktree subdomains, `omaterm services --wait`, and badges in the sidebar and Info panel.
- **Keep portless working:** when a process has `PORTLESS_URL`, show that URL, so portless users aren't forced to switch.

### 6.3 Crash and exit notifications: **adopt** (S–M)
- **Today:** a dev server that crashes is silent. Nothing notifies on Exited or on a non-zero `CommandExit` (`notice.For`, `notice.go:33-46`), and the pane vanishes.
- **Proposed:**
  - a "Process failed: web (exit 1)" notification;
  - a palette toggle "Notify When a Process Fails";
  - with 6.1, an "auto-restart paused" notification;
  - Solo's per-project and per-session notification level (All / Important / None) as a later refinement.

### 6.4 Clickable file paths: **adopt** (S)
- **Today:** mygo's terminal already finds file paths printed as text, including `path:line:col`, and URLs. It hands them to `Options.OpenLink` on Ctrl+click (`mygo/plugins/terminal/terminal.go:230-239`). omaterm doesn't set it, so Ctrl+click on a path does nothing.
- **Proposed:** open the file in omaterm's editor at that line. Open `localhost` URLs in a browser pane, and other URLs in the system browser.
- **Why:** agents print paths constantly. Solo has this (it opens an external editor); omaterm can open its own.

### 6.5 Waiting on several panes: **adopt** (S)
- **What:** `omaterm wait a b c --any|--all [--for idle] [--timeout]`. It prints which pane(s) finished, and exits non-zero on a timeout.
- **Why:** the shell analogue of Solo's `timer_fire_when_idle_any/all`.
- **Cost:** the client multiplexes calls and the server serves each request on its own goroutine, so it can be built in the CLI on today's `wait`.
- **Watch out:** a session target means its focused pane; make it mean its Lead pane for waits.

### 6.6 Safer typing into agents: **adapt** (S–M)
Solo's input queue protects a human who is typing at the same moment an agent or timer sends text. Adopt three of its parts for `omaterm send`, `prompt` and web access's compose:
1. Send text as a bracketed paste when the program enabled it, then Enter once the program has read it.
2. Hold automated input while the user typed in that pane within the last 2 s.
3. Hold it while the program reports *blocked* (OSC 7501 waiting).

Without these, an agent's `omaterm prompt` can land in the middle of the user's half-typed line.

### 6.7 Parent and child sessions: **adopt** (M)
- **What:**
  - A session started by `omaterm new` or `split` from inside a pane records that pane as its parent. The CLI sends `$OMATERM_PANE`, as `RenameParams.By` already does. Agents running away from a pane fall back to `SO_PEERCRED` plus `FindAgentPane`.
  - The sidebar nests children under the parent, as "+N" when collapsed. The pane header shows "started by …".
  - Closing a parent asks about its children.
  - `omaterm ls` shows the tree.
- **Watch out:** `workspace.go state()` already drops session fields silently (AgentNamed, Focus, Zoomed). The new field has to be saved, and those should be too.

### 6.8 An MCP server: **adapt, after 6.5–6.7** (M)
- **What:** `omaterm mcp` is a stdio JSON-RPC server, hand-rolled (no library needed). Its tools mirror the CLI: `ls`, `new`, `split`, `send`, `prompt`, `read`, `wait`, `status`, `report`, `services`, `browser`, `diff --turn`. `omaterm skill setup` registers it, the way Solo's "Connect Solo" runs `claude mcp add`.
- **Why later:** the skill and CLI already reach every agent with a shell. Solo's own numbers show agents mostly read output, check status and type, which omaterm's CLI covers. MCP adds typed tools and discoverability; it isn't new capability.

### 6.9 Small UX ideas: **adopt selectively** (S each)
- **Session summary for free:**
  - When Claude Code prints its end-of-turn recap line, show it as the session's subtitle in the sidebar, the palette and web access.
  - When OSC 7501 carries a `msg`, show that instead.
  - Never send terminal content to an LLM; this is the line omaterm holds against Solo's auto-summaries.
- **Favorites and focus jump:**
  - Star a session.
  - Ctrl+Shift+J ranks: needs you, then unread favorites, then unread, then running favorites.
- **CPU and memory on a row only above a threshold** (Solo: 30% CPU / 500 MB per process). The sidebar stays quiet until something runs away.
- **Palette scope syntax:** `project > action`, e.g. `api > restart web`, once processes exist.
- **Injected identity:** omaterm already sets `OMATERM_SESSION` and `OMATERM_PANE`. Add `OMATERM_PROJECT` and `OMATERM_KIND` (shell, agent or process), as Solo's `SOLO_*` variables do.

### 6.10 Skip
- **Scratchpads, todos, locks, key-value and prompt templates.**
  - **Why not:** this is a whole second product: Solo has 40-odd MCP tools for it. Claude Code, Codex and others have their own plans, todos and memory, and omaterm already gives agents files and Git.
  - **Revisit:** if users ask for cross-agent handoff notes, start with a single project scratchpad file in the repo.
- **LLM auto-summaries:** they cost tokens, send screens to a provider, and Solo itself calls them "triage, not evidence". 6.9's recap line covers most of the value for free.
- **Timers that inject a turn into an agent:** `wait --any/--all` from the agent's own shell covers it without omaterm typing into agents unasked.
- **Workspaces as separate windows:** project grouping may be worth it later. Multiple windows mean a different app structure.
- **An HTTP API with tokens:** omaterm has its socket, the CLI and web access.

### 6.11 Open decisions
- **File format:**
  - **YAML** (`omaterm.yml`): familiar from docker-compose and GitHub Actions, and `solo.yml` imports cleanly. Costs one dependency, `gopkg.in/yaml.v3`.
  - **TOML** (`omaterm.toml`): no new dependency (BurntSushi/toml is in); fewer people know it.
  - **Leaning:** YAML, for familiarity and the Solo import.
- **Autostart default:** off (safe, matches omaterm's stance) or on (as in Solo).
- **Proxy URL scheme** (6.2b): `<process>.<project>.localhost`, or just `<project>.localhost` for the main web process. The port is decided: 443, with clean URLs.
- **No-daemon mode** (setting off, or Windows): processes run in the app instead, with the same supervisor code but only while the window is open.

### 6.12 A Settings window: **build** (M), decided 2026-10-25
Today, settings are palette toggles (`overlays.go` `paletteSources`). Several have no UI at all:
- `Daemon`, `RestoreSessions`, `ResumeAgents`, `GitStatus`, `BrowserBlockAds` and `ControlSocket`;
- `MinimumContrast`;
- the theme mode, and agent hook setup.

Setup flows such as Dev URLs (6.2b), pairing and devices for web access, and later trust for `omaterm.yml` (6.1) need more room than a palette row or an overlay.
- **Shape:** a child window (`mygo.NewWindow` with `Parent: a.win`, `StateKey: "settings"`, native `Content`). It opens with Ctrl+, from the palette ("Settings…") and from the app menu.
  - A sidebar of pages, and the page on the right.
  - Changes apply at once. There is no Save button.
  - Test that Hyprland floats it as a dialog.
- **Pages:**
  - **General:** sessions survive closing (daemon), restore sessions, resume agents, Git status.
  - **Appearance:** theme (Omarchy, dark or light), density, background, dim unfocused panes, minimum contrast, reduce motion, focus follows mouse.
  - **Notifications:** needs you, finished, failed; later, crashes and notification levels (6.3).
  - **Agents:** hooks and skill setup per agent (`agentsetup.go`), "Let Agents Control the App", control socket.
  - **Web Access:** on or off, local or network, the link and QR code, Pair a Device, and paired devices with Revoke.
  - **Dev URLs:** 6.2b.
  - **Browser:** block ads.
  - **Projects:** processes and trust, once 6.1 lands.
- **Keep the palette toggles.** They are quick, and they stay in sync because both read `a.settings`.

## 7. Suggested order
1. **Quick wins (one release):**
   - the port Open button (6.2);
   - clickable paths (6.4);
   - crash notifications (6.3);
   - `wait --any/--all` (6.5);
   - restart keeping its command, and the app passing Env (6.1 pre-work).
2. **Project processes (6.1) with services (6.2):** the feature that changes what omaterm is for. Phases:
   - (a) the file, parser and sidebar group, with manual start and stop;
   - (b) trust and sync;
   - (c) supervisor (restart, watch);
   - (d) detection and Solo import;
  - (e) Dev URLs (6.2b): the proxy on 443 with its setup in the Settings window, then worktree subdomains.
   The Settings window (6.12) comes first. It is useful alone, and Dev URLs and process trust need it.
3. **Agents working together:** parent and child sessions (6.7) and safer typing (6.6), then the MCP server (6.8).
4. **Polish:** recap subtitles, favorites, CPU and memory badges, notification levels (6.9, 6.3).

## 8. Where omaterm already leads
- Sessions and, after 6.1, processes survive quitting the app and come back at login.
- Web access from a phone has shipped; Solo's remote viewing is unreleased.
- Git, diffs, Last turn, the editor, and browser panes that agents drive.
- Open source and free with no project limits; native rendering; Omarchy themes.
- **Positioning:** Solo just reached Linux, as an x86-64 AppImage, and charges past 4 projects. omaterm stays the free, open, Linux-first option. omaterm doesn't take ideas from Solo yet, so it isn't credited on the site. Credit it once 6.1 ships.
