# Agent-terminal landscape: Rex, Kero, Tern (research dossier, 2026-10-24)

## Context
The user asked for information on three new agent-oriented terminals, to inform omaterm's direction. omaterm is a Go app on egoist/mygo v0.5.5 with a split-tree of in-process PTYs (`terminal.go`), a projects sidebar (`state.go`, `sidebar.go`) and per-project process scanning (`service/process.go`). This file only collects research. No code changes.

---

## 0. At a glance

| | **Rex** (Superlogical) | **Kero** (egoist) | **Tern** (Stencil Labs) |
|---|---|---|---|
| Who | Mitchell Hashimoto (creator of Ghostty, co-founder of HashiCorp) plus ex-HashiCorp, Vercel and Poolside designers | EGOIST / Xinyu Zeng, essentially solo; also author of **mygo** | Can Bölük (can1357, author of omp) and Meehir Patel |
| Money | Seed led by Notable plus Amplify, with angels (Collison, Lütke, Rauch, …); amount undisclosed | None; MIT hobby project, no sponsor | Unknown |
| Status | Public test on macOS since 10-05; Linux is server-only (alpha) | v2.1.10; **ships on macOS, Windows and Linux** | Closed beta, waitlist; invites paused 10-19 until omp²; app at v0.9.5 |
| Source | Closed (only OSC 7501 and libghostty upstream work are open) | **MIT, everything** | App closed; SDK MIT |
| Stack | Go (server), Swift (Apple), Zig, libghostty | Go + mygo (purego, Metal/D3D11/GL), libghostty-vt | Rust, wgpu, tokio, iroh/QUIC, its own CSS/UI engine |
| Config | Lua 5.1 `init.lua`, read by the server | Ghostty-style `key = value`, 46 keys, hot reload | `settings.json` plus Luau plugins |
| Status signal | **Wrote OSC 7501** | herdr screen rules plus OSC 7501/133/9;4 | OSC 133/7/9;4, omp-native |
| Main extension point | CLI + JSON API + Lua events | CLI + agent skill | **TSP (native UI from programs)** + Luau plugins |
| Price | "Always free"; monetization plan TBD | Free | Not published |

**Common pattern in all three:** a daemon owns the PTYs and windows attach as clients. Each has a CLI with `send`/`read`/`wait`, an agent "skill" plus a SessionStart hook, agent resume after restart, and status shown as working/waiting/idle.

---

## 1. Rex (superlogical.com)

### Company and timeline
- Announced 2026-07-29 (mitchellh.com/writing/superlogical). It builds on MIT libghostty and sends shared work upstream. Ghostty stays non-profit.
- Founders: Mitchell Hashimoto, Jack Pearkes, Alasdair Monk, Hector Simpson. Hiring in LA, London and New York; currently remote. The jobs board is a TUI over `ssh superlogical.jobs`. Stack hires: Go, TypeScript, Swift and "native" for Windows.
- The GitHub org `superlogical` has 0 public repos.

| Date (2026) | Event |
|---|---|
| 07-20 | UI teaser |
| 09-02 / 09-08 / 09-14 | YouTube: architecture, memory, remote and CLI demos |
| 09-28 | OSC 7501 draft |
| 10-05 | Public test begins |
| 10-19 | API-compat system ships; 1,000 more invites; Linux test waves announced |
| 10-22 | iPhone client mockups |

- Version: "Rex Beta 0.1.0 (996)", updated through Sparkle. The changelog lives only in a beta Discord.

### Architecture
- Server-owned PTYs.
- **On attach:** a custom binary snapshot of the visible state, then a "ready" frame. After that the server **tees the raw PTY bytes to every client, which parse them with their own libghostty** ("N replica state machines"). Kero instead sends diffs.
- Input is serialized at the server. A client that drifts re-handshakes.
- Scrollback streams newest-first. Each client has its own viewport and selection. Each pane uses one connection.
- **Memory parking:**
  - Idle terminals are snapshotted to disk, encrypted, after 60 s. Unparking 64 MB takes about 200 µs.
  - Idle PTYs move from a dedicated thread to a shared epoll/kqueue.
  - Claimed about 400 KB per terminal against tmux's about 5 MB. Scrollback is about 64 MiB per terminal.
- **Transport:**
  - Local: Unix socket.
  - Remote: HTTP(S) with built-in tsnet Tailscale, Tailscale Serve, a tailnet address on `:7297`, or anonymous HTTP. The wire format is undisclosed.
  - "Full SSH replacement": the login server does a real PAM login from `/etc/rex/accounts.jsonc`. Authentication is by connection identity only for now; SSH keys are planned.
- **Control API:** JSON methods described by JSON Schema (`rex api list/describe/call`), at revision 4. Compatibility is handled by JSON-Pointer rewrite tries.
- Its own terminfo, `xterm-rex`, with an ssh_config fallback.
- Persistence across server restarts: **not documented**.

### Features
- **Docs** at `/rex/docs`, also as `.md`, `llms.txt` and `llms-full.txt`. Sections: config, shortcuts, key sequences and modes, custom actions, CLI, agents, Lua scripts and events, server API, the program-status protocol, a Linux server guide, and a community MCP.
- **Config:**
  - Lua at `~/.config/rex/init.lua`, read by the server. No file watching; reload with `rex config reload` and check with `rex config check`.
  - `rex.bind` takes tmux-style sequences (`ctrl+b>c`), modes and physical keys. `rex.action{}` adds custom palette actions.
- **CLI:** `--json`, exit codes 0–4 and 130, `send`, `send-key`, `capture` (text/HTML/VT), `wait`, `events`, `rex do` for Lua. Floating layers. `rex.host{}`.
- **Agents:** `rex skill setup` installs a skill and a hook for Claude, Codex, OpenCode and Pi. Remote Control must be turned on.
- **UI:** vertical tabs on the left, per-tab process icons (it detects Claude Code), ports per session, glass and compact modes, light mode with contrast correction.
  - No built-in browser.
  - Shortcuts: ⌘⇧P palette, ⌘⇧G go to directory.
- **Telemetry:** none. The daily update check can't be turned off. The server and CLI make no outbound calls.

### OSC 7501, the Program Status Protocol (Mitchell's spec; draft 09-28, revision 0.4 on 10-10)
- **Format:** `ESC ] 7501 ; key=value:key=value ESC \`.
- **Keys:**

| Key | Values |
|---|---|
| `state` | idle, working, done, blocked, error, clear |
| `kind` (with blocked) | permission, question, auth |
| `progress` | 0–100 |
| `app` | program name |
| `title`, `msg` | base64 |
| `id` | hierarchical, `/`-separated |

- Query support with `ESC]7501;?`.
- **Adopted by:** Claude Code 2.1.295, Codex, Amp, Factory, pi, Bubble Tea 2.1, Crush, OpenCode, cmux, libghostty, kitty (master), Kero.
- **tmux declined it** (#5713). There are libraries in Go (buildkite/osc7501), Python and Rust, and a tracker at hyeebeen/awesome-osc7501.
- **HN criticism:** too AI-specific, and it should be APC (Mitchell's answer: APC is poorly supported).

### Reception
- The launch thread on HN reached 796 points with mixed comments: Mitchell's track record versus objections to the VC and investor politics, and "what is it, exactly?"
- Reviews from Bitdoze, Codely and Kevin Patel are positive. Recurring complaints: macOS only, config doesn't hot-reload, and the SSH replacement is young.

---

## 2. Kero (kero.sh, github.com/egoist/kero)

### History
- **v1** (07-24 → 09-06): Swift/AppKit, GPLv3, macOS only, libghostty plus an optional Alacritty backend. It had a file tree, an editor and tabs. The launch line was "built with ~$2000 Claude credits in 5 days"; the Show HN got 7 points.
- **v2.0.0** (10-20): a full rewrite in Go on mygo, MIT, for macOS, Windows and Linux. 23 releases in 4 days, reaching v2.1.10 on 10-23. 188 commits, 185 of them by egoist.
- 1,126 stars, 106 forks, 31 open issues (Windows input, a Linux GTK crash, CJK fonts, a request for a file tree). The user base is heavily Chinese: a zh-Hans UI and `/zh` docs.

### Daemon (kerod)
Already covered in depth; key points:
- Each pane is a PTY from `/dev/ptmx` with `Setsid`, plus a **headless libghostty-vt** emulator.
- Clients get `terminal.Mirror` frames: a full first frame, then diffs batched 4 ms after output, held during mode 2026. Each client has an 8192-frame queue; a client that falls behind is dropped and re-attaches.
- **Socket:** Unix, umask 0177, `SO_PEERCRED` uid check, `kerod.lock`. Handshake is `Hello`/`Welcome` JSON at protocol version 3. Then either:
  - a **control** connection: line-delimited JSON-RPC with a `subscribe` push of the whole state, coalesced over 25 ms; or
  - an **attach** connection: binary frames `[kind][u32 len][payload]` with kinds o/i/r/z/c/k/t/m.
- **400 ms tick:** `TIOCGPGRP`, then `/proc/<pg>/comm` and `cmdline` (17 agent names, npm shim paths), then the cwd of the shell and of the foreground program (1.5 s), then status `look()`.
- Exits after 30 s idle unless running as a service (`kero daemon install-service`: systemd user unit with lingering, launchd or Task Scheduler).
- **Environment:** strips `CLAUDECODE`, `TMUX`, `VSCODE_*` and similar. Sets `TERM_PROGRAM=ghostty`, `KERO_SESSION`, `KERO_PANE` and `KERO_SOCKET`, and puts `~/.kero/bin` on PATH (a symlink to the CLI).
- **Persistence:**
  - `state.json` (atomic, 300 ms debounce) and `sessions/<id>.vt` from `term.History()` (every 10 s and on SIGTERM).
  - Restore replays the history, adds a dim "restored · session ended …" line, starts a fresh shell, and resumes agents only: `claude --resume`, `codex resume`, `pi`/`opencode --session`.
- **Command panes:** run as `$SHELL -l -i -c 'cmd; printf "\e]133;D;%s\a" $?; exec $SHELL -l'`.

### Status engine
- **Agent screen rules** in `internal/detect`: TOML manifests vendored from **herdr** (Apache-2.0), 13 agents. Rules have priority, a screen region, and contains/regex/line_regex conditions with all/any/not. The highest-priority match wins; with no match the agent is idle; `skip_state_update` freezes the state.
- **Other programs:** a spinner in the title means working.
- **Explicit reports:** an OSC scanner runs *before* the emulator, catching 7501 (strictly validated, at most 64 records, bidi characters removed), 133;A/D, 9;4 (agents only) and RIS. While any working or blocked record exists, screen inference is off.
- **Debouncing:** status changes only when the inferred state changes. Working → idle with no visible prompt must hold 700 ms. Typing clears "waiting" unless the dialog is still on screen. `Unread` is set if the session isn't being viewed.
- **Notifications:** sent for waiting, error, or done-while-unread. Suppressed if the user is "here" (window focused and less than 2 s idle). Dock badge and bounce are settings.

### Data model and RPC (`internal/proto`)
- `State{Projects, Sessions, Viewed}`, sent whole.
- A `Session` embeds its **lead pane**, chosen by urgency: waiting > error > exited-unread > unread > working > idle.
- `Pane` fields include Status, Records, Agent, AgentSession, ProcessDir, Git, Turn, ExitCode and CommandExit.
- `Layout` is a binary tree `{Pane} | {Row, Ratio, A, B}` with Split, Remove, Neighbor, Move and Swap.
- **RPC methods:** state, subscribe, create, split, close, pane.close, restart, rename, move, focus, zoom, layout, send, read, view, select, do, notify, wait, pane.agentSession, project.\*, git.changes/file/diff/revert, browser, pane.page, fs.\*, paste.file, clients, shutdown.

### Git
- **Turn snapshot:** when an agent goes idle → working, Kero copies `.git/index` to a temp file, runs `GIT_INDEX_FILE=tmp git add -A`, then `write-tree`. That gives a tree id that doesn't touch the user's index or refs.
- **Changes panel** (⌥⌘G) with three bases: Last Turn, Uncommitted, Branch. Per-hunk Revert (hash-checked, refused while an agent works) and Keep.
- Every git call uses `--no-optional-locks` and `GIT_TERMINAL_PROMPT=0`. Kero stats `.git` files once a second instead of watching them.
- Refresh: every 4 s while an agent works, 30 s otherwise. Pane titles show branch plus diffstat.
- **Not built yet** (`plans/git.md`): worktree sessions in `<repo>/.kero/worktrees/<branch>` and PR actions.

### App UI
- Backdrop "wash" (petal, mist, meadow, dusk or plain) with vibrancy. Left sidebar with project groups. A card holding the panes (8 px gaps, drag a pane by its title to move it). Optional Changes panel on the right.
- **Session rows:** bold when unread, a branch label, relative time (⌘ held shows ⌘1–9), a status icon:

| Status | Icon |
|---|---|
| working | spinning arc, or a progress arc when OSC 7501 gives a percentage |
| waiting | amber ring with a pulsing dot |
| error | red "!" |
| done | check |

- **Sidebar footer chips:** needs-you, working and done counts, each jumping to the next session. ⌘J goes to whichever session needs you.
- **Palette** (⌘P): fuzzy search over sessions, actions, settings pages and URLs.
- **Peek:** ⌘-hover an image path for a preview.
- **Browser panes:** the OS webview (WKWebView, WebView2, WebKitGTK), driven by `kero browser` through injected JS, with an adblock converter (ABP → WebKit content rules via mygo) and a favicon cache.
- "Elevation" styles copy Rex and macOS 27 (litEdge, layered shadows).
- **Shortcuts:**

| Action | Shortcut |
|---|---|
| New session | ⌘T |
| Split right / down | ⌘D / ⌘⇧D |
| Zoom | ⌘⇧↩ |
| Next / previous pane | ⌘] / ⌘[ |
| Move between panes | ⌘⌥ + arrows |
| Close | ⌘W |
| Sidebar | ⌘B |
| Restart pane | ⌘⇧R |
| Browser pane | ⌘⇧B |
| Find | ⌘F |

  On Windows and Linux, Kero adds Shift or uses Alt.
- **Config:** 46 Ghostty-style keys in `~/.config/kero/config`, including theme (any Ghostty theme), font, line-height 1.22, minimum-contrast 3, scrollback 4 MB, notify-on-\*, dock-badge, remote-control, git-status, diff-layout, browser-block-ads, keybind.

### Agent integration
- `kero skill setup` writes SKILL.md (the full text is in the research output) and hooks for Claude (`settings.json` SessionStart), Codex (`hooks.json`), a Pi extension and an OpenCode plugin.
- The hook **matches the pane by PID** through `pane.agentSession` and stores the agent's session ID for resume. It returns `additionalContext` telling the agent its session and pane, and not to rename the session.
- **CLI:** ls, new, split, send, key, prompt, read, wait, status, diff, rename, focus, close, restart, notify, report, projects, skill, daemon, do, actions, config, browser, clients.
- Exit codes: 0 ok, 1 failed, 2 usage, 3 not found, 4 kerod unreachable.

### Release, updates, telemetry
- A tag push builds signed and notarized artifacts for every platform through `go tool mygo build -upload`.
- mygo's updater does Ed25519-signed delta updates. The server build signs a message that binds version, OS and arch.
- No telemetry in the app; the website uses Umami.

### mygo (the framework omaterm also uses)
- MIT, 2.7k stars, about 70 tags from v0.1.1 (09-27) to v0.6.3 (10-23).
- Pure Go through purego: Metal, D3D11, GL (GtkGLArea), a CPU renderer, and WASM.
- Has a native UI toolkit (85 docs pages: lists, editor, **diff viewer**, markdown, charts, …), a webview, an updater and plugins.
- **Terminal plugin:** libghostty-vt loaded at run time.
- **`Mirror` and `History` landed in v0.6.0** (PR #547, 10-20), along with OnCopy, OnClear, MinimumContrast and headless color answers. **omaterm's v0.5.5 has OnNotify and Conn/Feed/Text/Snapshot, but not Mirror or History.** Kero pins mygo at commit 2fde45a, which is effectively v0.6.3.

### herdr (where Kero's detection rules come from)
- herdrdev/herdr: a Rust agent multiplexer, Apache-2.0, 45.5k stars, YC with a $6M seed, at v0.9.5.
- 23 detection manifests that update independently. Agents can also report directly through `herdr pane report-agent`.

---

## 3. Tern (stencil.so/tern, docs.stencil.so/tern, github.com/stencil-hq/tern-sdk)

### Company and status
- Stencil Labs, Inc. (Delaware): "New instruments for building software." Funding is unknown.
- Also makes **omp** (oh-my-pi): a fork of Pi, MIT, TypeScript on Bun with an 80k-line Rust core, 36k stars, 669 releases, one to three per day. Tern is "built for omp".
- **Timeline:**

| Date | Event |
|---|---|
| 09-29 | "Feature complete" |
| 10-05 | SDK published |
| 10-09 | Supports OSC 7501 |
| 10-19 | Invites paused until omp²; 1.0 slipped |
| 10-20 | v0.9.5 |

- Other repos:
  - `slab`: a design language for agents, one Rust kernel rendering to web, wgpu and TUI.
  - `dquickts`: QuickJS whose state can be snapshotted and resumed.
  - `vibemon`: KVM microVMs that speak the E2B API.
  - `wasm-grammars`.

### Platforms and stack
- The landing page lists Mac, iPhone and a web tab (WASM on WebGPU, `tern web serve` on port 8377 with a token).
- The docs and issues also show **Linux** (inotify, XDG paths, the Wayland portal), **Windows** (named pipe, ConPTY with OSC 877) and Android builds.
- **Stack:** Rust, wgpu 30, naga, tokio, rustls, ring, iroh 1.3 and noq (QUIC), harfrust, skrifa, ssh-key, plus Stencil's own text, term, ui and kit crates and **its own CSS engine**.
- **Speed:** 151 ms from cold start to a prompt (p95), 153 ms to reopen six tabs. No Chromium.

### Product surface
- **Daemon:** sessions survive quit and reboot; panes return in their directory with scrollback; omp resumes.
  - omp reports its session through OSC 1337 `SetUserVar=omp_session_file` and `omp_restore`. The daemon relaunches it with `omp --resume` or `--restore`.
- **Remote:** `tern remote serve` on port 8376 over QUIC. Discovers Tailscale peers and `~/.ssh/config` hosts, bootstraps over SSH, pins host keys, does Mosh-like predictive echo, and shows other clients' presence.
- **Blocks** (built-in apps):
  - Shell (⇧⌘T), Agent/omp (⇧⌘A), Browser (⇧⌘B), Files (⌘O; a Markdown document editor; a code editor with git gutter, multi-cursor and 1M-line files; hex; PDF; media).
  - Git (⌥⌘G; graph, line staging, AI commit).
  - SQLite (a port of DB Browser for SQLite), Jupyter, a kanban Board over TODO.md (⌥⌘K), a task manager with GPU columns, a flame-graph profiler, and screen sharing.
- **UI:** layouts Tiles, Studio, Rail and Float; window materials glass, clear, solid and islands; vertical tabs; picture-in-picture cards with fling physics; an attention inbox; a shell completion popover; native prompt styles (p10k, Starship, Pure, …); omp themes.
- **Carly:** a built-in assistant running on omp, with voice, tools, canvases, scheduled tasks and a heartbeat. Tap ⌘ twice to open it.
- **CLI:**
  - `tern split @focused right -- btop`, `run`, `wait --until prompt`, `capture`, `pip --corner`, `events --filter` (JSON lines).
  - `open [--wait|--preview]` (Tern sets `EDITOR` to `tern open --wait`), `shot` (headless PNG), `ctl` (a debug and control socket: tree, css, perf, trace), `plugin …`.
- **Directories:** macOS `~/Library/Application Support/Tern`; Linux `$XDG_CONFIG_HOME/tern` and `$XDG_STATE_HOME/tern`. Daemon socket `daemon.sock`, logs `tern-daemon.log`.
- **Pane environment:** `TERN_PANE`, `TERN_PANE_SOCKET`, `TERN_WINDOW_SOCKET`, `TERN_BLOB_DIR`, `TERM_PROGRAM=tern`, …

### Tern Surface Protocol (TSP): open, documented, with SDKs
- **Framing:** `ESC _ tsp ; <verb> [; k=v]* ; <json> ESC \`. Ends with ST only, never BEL. Unknown fields are ignored.
  - Program → terminal verbs: q (query), o (open), f (frame), b (blob), t (palette), s (stylesheet), x (close).
  - Terminal → program: r (reply), e (event).
  - Chunked above 64 KiB (`c=id`, `m=1`). A message is dropped above 24 MiB joined, or if it stalls for 1 s.
  - Blobs are SHA-256 addressed, at most 16 MiB.
- **Handshake:** send `hello{v:[1],app,features:[edit,undo,send]}` followed by DA1. If the reply comes before the DA1 answer, TSP is available.
  - The reply carries `kinds`, `features`, `apc` 65536, `credits` 2, `cols`, `cell`, `dark` and `reduceMotion`.
  - Fast path: when `TERM_PROGRAM=tern`.
  - Disabled under tmux (unless `@tern-tsp` is set), screen and zellij.
- **Surfaces:**
  - `inline`: owns the pane until the next prompt (OSC 133;A).
  - `screen`: covers the pane, like the alternate screen.
  - `flow`: stays in scrollback.
  - Each is anchored to one grid row. Regions: `main`, `dock` and `layer`. `adopt` reopens a closed surface; `listen:false` turns off events.
- **Ops:**
  - Tree and text: add, set (null deletes), text (append or replace), splice (UTF-16), move, del.
  - Display control: settle, focus, reveal, scroll, suspend, resume.
  - A bad op is rejected on its own.
- **Events:** ack, resize, theme, motion, visible, toggle, select, activate, action, change, focus, edit, undo, send, error, gone.
- **Flow control:** at most 2 unacknowledged frames; coalesce while waiting.
- **Retention:** 64 MiB per pane. Settled nodes are evicted, which sends `gone`. The daemon replays a snapshot on attach.
- **44–45 node kinds:**

| Group | Kinds |
|---|---|
| Layout | col, row, card, section, rule, spacer |
| Text | text, md (GFM, mermaid, math), code, diff, ansi, rows, math |
| Data | kv, table, tree, badge, kbd, icon, image |
| Lists | list, item, tabs, picker |
| Motion | spinner, shimmer, elapsed, rate, progress, meter, chart, effort |
| Inputs | editor, input |
| Chrome | status, seg, toast, overlay |
| Work | tool, agent, checklist, block, prefs |
| HTML | el (HTML subset, forms) |
| omp | media |

- **Styling:** CSS, scoped per surface, with variables `--t1..4`, `--accent`, `--ok`, `--warn`, `--bad`, `--card`, … Stencil's engine adds `spring()` easing and effects such as mesh-gradient and genie.
- **SDKs:** Rust, Python, **Go** (`github.com/stencil-hq/tern-sdk/go/tern`, Go 1.26, depends only on x/term and x/sys) and TypeScript.
  - Layers: wire, input/keys (kitty CSI u), a reconciler that diffs views into ops (LIS-based moves), Session/Surface with credits, and `Print`/`Ask`/`Plain` fallbacks.
  - The repo is a one-way mirror (18 commits, mostly from a bot). Its 140 issues work as the app's public bug tracker.
- **Adoption:** omp only (`packages/wire/src/tsp.ts`, since v18.4.4). There's also a third-party Zig client.

### Luau plugins
- `plugin.toml` (schema 1) plus `host.luau` and/or `window.luau`:
  - **Host half:** runs in the daemon, one worker thread per plugin, 2 s budget. Handles blocks, command lenses, the hooks `command_started`/`finished`, `cwd`, `title` and `pane_exited`, the `spawn` filter (rewrites a shell's program, args, cwd and env), and `tern.pane.write`/`list`.
  - **Window half:** 50 ms budget (4 ms for formatters). Handles commands, binds, overrides, routes for open and link, chrome formatters for tab, title and status, CSS, layout and window events.
  - Sandbox: 256 MiB, Luau JIT. **No permission system.**
- **Window `cx` APIs:** session.read/surface/event/settle, agents (start, ask, wait, transcript), git, db, notebook, board, docs, canvas, whiteboard, procs, screen, hosts, settings, layout. **173 built-in actions**, all overridable.
- **Install:** `tern plugin install github.com/...`, plus link, unlink and types.
- **Ecosystem:** theblazehen/awesome-tern (168 stars, about 40 plugins: kubectl, Grafana, jj, Spotify, …).

### Reception
- Nearly invisible on HN; there's no Tern thread. The only in-depth review is bitdoze, which calls it the most interesting terminal of the year and an IDE-shaped terminal, but notes it's closed, in beta and unpriced.
- omp's tracker has Tern bugs: GPU reset freezes, scroll jank, out-of-memory errors with no backpressure while detached.

---

## 4. Takeaways for omaterm
1. **Status is a convergent feature.** All three tools have it, and OSC 7501 is becoming the standard: Claude Code and Codex already emit it. The cheapest high-value step is to scan OSC 7501, 133 and 9;4 plus the title spinner, optionally adding herdr's Apache-licensed screen rules. Then show working/waiting/done in the sidebar and tabs, with notifications.
2. **Daemon persistence is expected now.** Kero is a reference implementation on the same framework, but it needs **mygo ≥ v0.6.0** for `Mirror` and `History`. Rex's other approach is to tee raw bytes to clients that each run their own emulator.
3. **Agent hooks and resume** don't need a daemon. A SessionStart hook can record the agent's session ID so a restored pane runs `claude --resume <id>`.
4. **Per-turn diffs** with a temporary `GIT_INDEX_FILE` plus `write-tree` are cheap and fit omaterm's existing `diff.go` and `graph.go`.
5. **Browser panes plus automation, as in Kero.**
   - **What:** a split pane showing an OS webview, with a toolbar (back, forward, reload, address bar, favicon), find, and the page restored on restart. A `browser` CLI lets agents drive it: `open`, `click`, `type` (`--enter`), `wait <selector|--text>`, `text`, `html`, `eval`, `screenshot`, `back`, `forward`, `reload`.
   - **How Kero does it:** injected JS waits for elements and dispatches real input events. Screenshots need the pane visible. Driving requires the app to be open plus a "Remote Control" setting. The agent skill teaches "`split --url localhost:3000`, then verify with `browser …`". Optional ad blocking converts EasyList into WebKit content rules.
   - **omaterm today:** mygo v0.5.5 already has `Window.NewWebView` (placed through `ui.WebView`), page script evaluation and `WebView.CapturePage()` (PNG). Content-rule compilation for ad blocking needs newer mygo.
   - **Prerequisite:** an app control socket and CLI (`omaterm browser …`). The agent CLI needs the same thing.
   - **Linux caveats:** WebKitGTK must be present. Native overlays (palette, menus) have to avoid the webview layer.
   - **Why it matters:** agents can check the UI they build inside omaterm while the user watches. Rex has no browser; Tern's is closed and tied to omp.
5b. **UI polish, in the style of Rex, Kero and Tern.** The user likes all three looks.
   - **omaterm today:** a flat dark "oma" palette (`theme.go`: `#0b0e12` background, hard borders `#252d38`), top tabs (`tabs.go`), a sidebar (`sidebar.go`), a status bar (`statusbar.go`), and panes in `terminal.go`.
   - **What they share, and what omaterm could adopt:**

     | Pattern | Description | Where it's from |
     |---|---|---|
     | **Panes as floating cards** | Rounded panes with 8 px gaps over a soft background "wash" (a gradient plus a glow, with vibrancy/translucency), instead of panes divided by lines | Rex, Kero `background = petal/mist/meadow/dusk/plain` |
     | **Per-pane header** | Program logo (claude, codex, node, vim, git, docker…), title plus cwd, git chip (`branch +40 −2`), status chip, and hover controls (split, zoom, close); drag a header to move or swap panes with an animated drop preview | Rex, Kero `panes.go` |
     | **Process-aware pill tabs or vertical session list** | Process icon plus title; host name in the title bar | Rex; Kero sidebar; Tern vertical tabs |
     | **Status visuals** | Spinning arc (or progress arc) while working; amber ring with a pulsing dot when it needs you; red "!" on error; check when done; bold name when unread; relative time; footer count chips (needs you, working, done) | Kero `side.go` |
     | **Elevation system** | `raised` / `chosen` / `floating` / `menu` levels; in dark mode a 1 px "lit from above" edge (18%→6%) plus three layered tight shadows (Rex's surface shadow); dim unfocused panes | Kero `elevation.go`, copying Rex |
     | **Command palette** | One fuzzy field over sessions, actions with shortcut hints, settings pages and URLs | Kero ⌘P, Rex ⌘⇧P, Tern ⌘K |
     | **Light and dark themes with contrast correction** | `minimum-contrast`; any Ghostty theme; `light:X,dark:Y` | Rex, Kero |
     | **Layouts and materials** | Tiles, Studio, Rail and Float layouts; glass, clear, solid and islands materials; compact density; picture-in-picture floating panes | Tern |
     | **Motion** | Sidebar slides with a 260 ms spring; pulsing waiting dot; animated drop preview; Tern's spring() easing | Kero, Tern |
     | **Fonts** | Bundled JetBrains Mono Nerd Font, line-height 1.22 (Kero); Geist UI font plus Berkeley Mono (Tern) | Kero, Tern |

   - **mygo support:** the ui package has rounded rects and shadows on the SDF renderer, motion and theming, and mygo has a glass plugin. Kero is proof that all of this is buildable on mygo. Its `elevation.go`, `theme.go`, `side.go` and `panes.go` (MIT) are a direct reference.
   - **Omarchy angle:** follow the active Omarchy theme's colors, and keep the "cards on a wash" look as an option, because Hyprland already adds gaps around windows.
   - **User decisions (2026-10-24):** wants **every** pattern in the table above. Look: **follow the active Omarchy theme** for colors, with the Rex/Kero/Tern structure on top. For now, only save the research; plan features later.
6. **Agent CLI:** `split`, `send`, `read`, `wait` and `report` over a local socket. This underpins #5 and copies the names agent skills already expect.
7. **TSP** is the boldest idea, and it has a ready Go SDK. But only omp emits it today, so it's a long-term bet.
8. **Positioning:** Kero already covers Linux with MIT code on the same stack. Rex and Tern have no Linux desktop client. omaterm's opening is being Omarchy-native and opinionated.
