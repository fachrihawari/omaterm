# What goes in the Settings window: research, 2026-10-25

This follows up `docs/design/settings-window.md`. It has three parts:
- an inventory of everything a user could configure in omaterm and mygo today, whether it lives in a file, an env var, the CLI, the palette, or is hardcoded;
- a survey of the settings in Solo, Ghostty, Warp, WezTerm, iTerm2, Zed, VS Code, Conductor, Claude Code desktop, Cursor and Vibe Kanban;
- the result: a page map, with what to build first.

Priorities:
- **P1:** the first release of the window.
- **P2:** soon after, or once its feature exists.
- **Later:** worth it, but needs a feature first.
- **File:** stays in `settings.json` only.
- **No:** not a setting.

## 1. Things found on the way (fix before or with the window)
- **`gitStatus` is misnamed.** Its only reader gates the agent-turn snapshots behind "Last turn" (`changes.go:395`). The Git status in pane headers and the inspector (`gitwatch.go`) ignores it. Rename the row "Snapshot each agent turn (Last turn)", or make it gate `gitwatch` too.
- **`reduceMotion` is half-wired.** Only `sidebar.go:32` and `statusicon.go:56` read it. These animations ignore it:
  - the pane spring (`layout.go:239`);
  - the drop preview (`layout.go:308`);
  - the veil fade (`layout.go:221`);
  - the row color animation (`sidebar.go:207`);
  - the browser glow (`browser.go:367`).
- **`browserBlockAds` is dead.** Nothing reads it. mygo has `PageOptions.ContentRules` (`mygo/content_rules.go`), so it can be wired, but omaterm would have to ship a filter list. Decide: wire or drop.
- **There is no Omarchy detection.** Off Omarchy, the default `theme: "omarchy"` silently means Dark (`themewatch.go:26`). Add a **System** mode (`mygo.Theme.IsDark()`), and show "Omarchy" only when an Omarchy theme dir exists.
- **The editor and diff ignore the terminal font.** They get only `Font{Size}` (`editor.go:45,63`, `diff.go:536`), although `fonts.go:10` says they share it.
- **The terminal font is read once,** at startup (`state.go:432`). An Omarchy font change needs a restart.
- **Zoom keys may fight the terminal plugin.** Its own Ctrl+=/−/0 keybinds may win while a terminal has focus (`terminal/view.go:617-628`). `OnZoom` is unset, so zooming may change one terminal and not be saved. This has not been confirmed at runtime.
- **The `mygo.json` identifier is still `com.example.omaterm`.** Fix it before the About page shows it.
- **The design doc's mockup shows port 7399;** the default is **7370** (`internal/web/server.go:413`). It is fixed in the design doc.

## 2. Page map

### General
| Row | Today | Priority |
|---|---|---|
| Keep sessions running when the window closes (`daemon`) | file | P1 |
| Start the session daemon at login (systemd user unit) | CLI `daemon install-service` | P1, Linux |
| Restore sessions at launch, resume agents | file | P1 |
| Snapshot each agent turn (`gitStatus`, renamed) | file | P1 |
| Command-line tool: installed at `~/.local/bin/omaterm`, Install/Remove | palette, CLI | P1 |
| Confirm before closing a session with a running program or agent | not built: Ctrl+W closes at once (`tabs.go:201`) | P2 |
| Open links from terminals in: browser pane / system browser | not built: `OpenLink` unset | P2, with clickable paths (solo.md 6.4) |
| Default editor for "Open in…" (omaterm, VS Code, Zed, Cursor, a custom command, with Test) | not built | Later (idea from Solo and Vibe Kanban) |
| Clear saved scrollback (`~/.local/state/omaterm/sessions/*.vt`) | none | P2 |

### Appearance
| Row | Today | Priority |
|---|---|---|
| Theme: Omarchy, System, Dark, Light, as preview tiles | palette commands; System is new | P1 |
| Density, background, dim unfocused panes | palette | P1 |
| Reduce motion, once fully wired | palette | P1 |
| Show system load (CPU and memory in the status bar) | always on | P2 |
| UI font and size | hardcoded Inter 13 | No |

### Terminal (new page)
omaterm sets only the font, theme, minimum contrast and padding of mygo's `terminal.Options` (`plugins/terminal/terminal.go:75-284`).
- **Daemon mode:** the daemon's terminal is the real one (`daemon.go:310`), so every row here must reach the daemon too, through settings.json or the protocol.
- **Applying changes:** each row needs a setter in `applySettings`, which today re-applies only contrast.

| Row | mygo option | Default | Priority |
|---|---|---|---|
| Font: follow Omarchy/Ghostty, or custom family | `Font.Family` | Ghostty's `font-family`, else JetBrainsMono Nerd Font | P1 |
| Font size, moved from ui.json | `Font.Size` | 13 (8–32) | P1 |
| Line height, ligatures | `Font.LineHeight`, `Font.Features` | unset | P1 |
| Cursor: block, bar or underline, and blink | `Cursor`, `NoBlink` | blinking block | P1 |
| Minimum contrast | `MinimumContrast` | auto | P1 |
| Copy on select | `CopyOnSelect` | off | P1 |
| Right click: menu or paste | `RightClick` | menu | P2 |
| Programs may read the clipboard: never or ask | `ClipboardRead`, `OnClipboardRequest` | deny | P2 |
| Scrollback size | scrollback limit | 10 MB | P2 (daemon plumbing) |
| Bell: off, visual or notify | `OnBell` | does nothing (unset) | P2 |
| Shell (default: your login shell) | `shellcmd.go:17` | `$SHELL` as a login shell | P2 |
| Option as Alt | `OptionAsAlt` | off | P2, macOS only |
| Padding, image memory limit | `Padding`, image limit | 10×6, 320 MB | File |

### Editor (new page)
mygo `editor.State` (`ui/editor/state.go`); omaterm sets only the buffer, theme and font size.

| Row | Default | Priority |
|---|---|---|
| Font: same as terminal | unset, so system mono | P1, as a bug fix with no row |
| Tab size, indent (overrides the language) | language, else 4 | P2 |
| Word wrap | off | P2 |
| Line numbers, whitespace, wrap guides, auto-close brackets | mygo defaults | P2, the few people change |
| Open Markdown in preview | always (`editor.go:62`) | P2 |
| Diff: split or unified (from ui.json), wrap, context lines | split | P2 |

### Notifications
| Row | Today | Priority |
|---|---|---|
| Needs you / finished / failed | palette | P1 |
| Test notification button | none | P1 (from Solo) |
| Sound per kind, or none | `NotificationOptions.Silent` unused; the daemon's `notify-send` has no sound | P2 |
| Don't notify for the session you're looking at | does this already (`notice.go`) | P1, as text only |
| Show notifications from programs (OSC 9/777) | always on (`notify.go:46`) | P2 |
| A command ran longer than N seconds (non-agent panes) | not built | Later (Ghostty, Warp) |
| A process crashed | not built | Later (solo.md 6.3) |
| Level per project or session: all, important, off | not built | Later |

### Agents
| Row | Today | Priority |
|---|---|---|
| Integration per agent: hook installed?, Set Up / Remove, where it wrote | palette, CLI `skill setup/remove/status` | P1 |
| Recognized agents (17, `internal/agent/agent.go:19`), read-only, with which have hooks (Claude, Codex, Pi, OpenCode) | none | P1 |
| Let agents control the app | palette | P1 |
| Allow the `omaterm` command (control socket), applies after a restart | file | P1 |
| Default agent for "New Session", per project override | not built | Later (Vibe Kanban, Conductor) |
| Agent commands and default flags (`claude --model …`), with Test | not built; resume commands fixed (`agent.go:132`) | Later (Solo's agent tools) |
| Auto-rename sessions from the agent's work | not built | Later (Solo) |

### Worktrees (new page, later)
omaterm doesn't create worktrees yet, but every agent app surveyed does. This is where its settings would go:
- base folder and branch prefix;
- base branch, and fetch first;
- files to copy, such as `.env*`;
- setup and archive scripts;
- a port per worktree;
- archive on merge.

It also ties into Dev URLs, which give each worktree its own subdomain. Treat it as a feature to research on its own, not a settings row.

### Web Access
| Row | Today | Priority |
|---|---|---|
| On, on your network, link, Pair a Device… | palette | P1 |
| Paired devices: last seen, expires in 30 days, Revoke, Revoke All | CLI only | P1 |
| Port (7370) and public URL | CLI only | P1, under Advanced |
| "Reach it from elsewhere…" sheet: Tailscale and cloudflared steps (`cli/web.go:152`) | CLI help | P2 |

### Dev URLs
As in solo.md 6.2b and the design doc: the setup checklist, Set Up / Remove, and routes. Like Laravel Herd, also show the certificate's expiry and a Renew button.

### Browser
| Row | Today | Priority |
|---|---|---|
| Search engine: DuckDuckGo, Google, Kagi, Brave, or custom | hardcoded DuckDuckGo (`internal/proto/url.go:27`) | P1 |
| Block ads | dead | P2, if wired |
| Clear browsing data | `mygo.App.ClearBrowsingData()` exists, unused | P1 |
| User agent, default zoom | mygo has them | File |

### Keyboard
The table is fixed (`keybindings.go:162`). Show it read-only, searchable, with a link to the shortcuts overlay. Custom bindings are a feature of their own (later). One extra row: "Ctrl+, opens Settings".

### Files
| Row | Today | Priority |
|---|---|---|
| Show hidden files in the file tree | dotfiles skipped (`state.go:824`) | P2 |
| Excluded folders (node_modules, dist, …) | fixed | File |

### About
| Row | Today | Priority |
|---|---|---|
| Version, Check for Updates, last check | palette | P1 |
| Check automatically, install automatically | updater's `SetAutomaticChecks/Downloads`, unused | P1 |
| Where things are: settings, state, web, saved scrollback, service unit, hooks; Reveal and Open settings file | none | P1 (Warp and Solo have "open settings file") |
| Website, changelog, issues, credits | none | P1 |

## 3. What other apps taught (survey summary)
- **Common to nearly all terminals:**
  - font family, size and line height;
  - theme;
  - cursor;
  - padding;
  - scrollback;
  - copy on select;
  - Option as Alt;
  - default shell;
  - confirm before close;
  - dim unfocused splits;
  - bell;
  - a keybinding editor.

  omaterm has almost none of these in its UI today. The Terminal page closes that gap.
- **Only in agent apps:**
  - agent definitions (command, flags, health check);
  - a default agent;
  - per-kind notification sounds;
  - worktree defaults;
  - setup and archive scripts;
  - approvals and allowlists (Warp, Cursor).
- **Approvals: no.** omaterm runs agents in terminals, and the agents enforce their own permissions. omaterm shouldn't add another layer.
- **Solo's ideas worth copying:**
  - **Test** buttons beside anything that runs a command or makes a sound;
  - a sound picker with None;
  - "don't play for the process you're looking at";
  - external tool settings (editor, terminal, browser) with `$PROJECT_DIR`;
  - per-feature on/off switches.
- **Warp's ideas worth copying:**
  - "Open settings file" in the footer;
  - notification kinds that match ours (Complete, Request, Error);
  - "long command finished after N seconds".
- **macOS conventions:**
  - Ctrl/Cmd+, opens Settings;
  - changes apply immediately;
  - the title is the page name;
  - Restore Defaults per page;
  - search across settings.
- **Laravel Herd** has the closest privileged-setup flow to Dev URLs: a toggle per site, OS password prompts, and certificates listed with "Valid Until". Nobody else surveyed does local HTTPS.

## 4. Suggested first release (window P1)
- **Pages:** General, Appearance, Terminal (font, size, line height, ligatures, cursor, contrast, copy on select), Notifications (with Test), Agents, Web Access (devices and Revoke), Browser (search engine, clear data), About (updates, files, links).
- **Also, as fixes:**
  - the System theme mode;
  - wiring `reduceMotion` fully;
  - the editor using the terminal font;
  - renaming `gitStatus`;
  - the identifier;
  - deciding on `browserBlockAds`.
- **Plumbing:**
  - terminal settings reach the daemon;
  - `applySettings` gets setters for each terminal option;
  - font size moves from ui.json to settings.json, migrated once.

Then:
- **P2:** Editor and Files pages, the rest of Terminal, sounds.
- **With their features:** Dev URLs, Projects, Worktrees.

## Sources
- Code: file:line references above, checked against `dev` on 2026-10-25.
- Solo: https://soloterm.com/api/v1/docs/settings/overview, /agents/setting-up-tools, /settings/tools-editors-terminals, /notifications/bell-sounds
- Ghostty: https://ghostty.org/docs/config/reference
- Warp: https://docs.warp.dev/getting-started/customizing-warp, /agents/autonomy/agent-permissions, /agents/capabilities/agent-notifications/
- WezTerm: https://wezterm.org/config/lua/config/index.html · iTerm2: https://iterm2.com/documentation-preferences.html
- Zed: https://zed.dev/docs/terminal · VS Code: https://code.visualstudio.com/docs/terminal/advanced
- Conductor: https://www.conductor.build/docs/reference/settings/reference
- Claude Code: https://code.claude.com/docs/en/settings, https://code.claude.com/docs/en/desktop
- Cursor: https://cursor.com/docs/agent/run-modes · Vibe Kanban: https://vibekanban.com/docs/settings/general
- Laravel Herd: https://herd.laravel.com/docs/macos/sites/securing-sites
