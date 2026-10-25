# Settings window: design, 2026-10-25

Status: built, the first release of the window (2026-10-25). What goes on each page, in full: `docs/research/settings.md`. Decided with the user on 2026-10-25, while researching Solo (`docs/research/solo.md`, 6.2b and 6.12).

## As built
The code:
- `settingswin.go`: the window, sidebar, search, toolbar and building blocks;
- `settings_pages.go`: the pages;
- `settings.go`: the model and the `settingDefs` the palette shares.

Differences from the design below:
- **Dev URLs** is not built. It comes with the proxy, in a plan of its own, so the page is left out.
- **Notification sound** is a switch: Linux notifications sound or stay silent, with no choice of sound.
- **Open settings file** opens `settings.json` with the system's editor for it.
- **Terminal options without a setter in mygo** (cursor, blink, right click, clipboard reads, scrollback) apply as terminals are made. A session daemon's panes get theirs again at once: the app lets go of their terminals and shows them anew. Panes that run in the app get them in new sessions.
- **The daemon reads `settings.json`** for the shell, the scrollback and the search engine as it needs them. No new RPC was needed.
- **The gear** is at the right end of the status bar.
- **The "System" theme** is the default off Omarchy.
- **`omaterm settings [page]`** starts the app with `--settings=<page>`. An open app takes it over, so it works with the control socket off.

## Why
Settings live in the command palette today, as toggles (`overlays.go`, `paletteSources`). That was fine while every setting was a yes or no, but no longer:

- **Settings with no UI.** These are in `settings.json` (`settings.go`) and can only be changed by editing the file:
  - `daemon`: sessions outlive the window (`daemonhost.go:29`);
  - `restoreSessions` and `resumeAgents` (`restore.go:50-54`);
  - `gitStatus` (`changes.go:395`);
  - `controlSocket`: the `omaterm` command reaches the app (`main.go:37`, `daemonhost.go:66`);
  - `minimumContrast` (`terminal.go:233`).
- **Settings with no clear place.**
  - The theme mode is three palette commands, "Theme: Follow Omarchy", "Dark" and "Light" (`commands.go:75`). It is not a setting with a value you can see.
  - Agent integration is set up only from a one-time banner (`agentsetup.go`) or with `omaterm skill setup`.
- **Web access has a pairing dialog, but no device list.** Seeing paired devices and revoking one is CLI-only (`omaterm web devices`, `omaterm web revoke`).
- **New features need room.** The next ones are setup flows, not toggles:
  - **Dev URLs:** `https://myapp.localhost`, with a one-time privileged setup (solo.md 6.2b);
  - **Project processes:** trust for `omaterm.yml` (solo.md 6.1).

  A palette row can't show a checklist, explain what sudo will change, or list routes.
- **Dead setting:** `browserBlockAds` is declared but nothing reads it. Wire it up, or drop it, before it gets a switch.

## Goals
- Every setting in `settings.json` and `web.json`, plus the setup flows, is reachable without editing a file.
- Changes apply at once. There is no Save button. Settings that only take effect after a restart say so beside the control, and offer the restart.
- The palette toggles stay. They are the fast path, and they read and write the same `a.settings`, so the two never disagree.
- Plain words, the same as the palette ("Let Agents Control the App", not "Remote Control").

## Not goals
- **Editable keyboard shortcuts.** The table is fixed (`keybindings.go`). The Settings window may list the shortcuts read-only, linking to the shortcuts overlay.
- **Per-project settings.** These come with `omaterm.yml`, on the Projects page later.

## Shape
- **A separate window, a child of the main one:**

  ```go
  mygo.NewWindow(mygo.WindowOptions{
  	Title: "omaterm Settings", Width: 820, Height: 600, MinWidth: 680, MinHeight: 480,
  	Parent: a.win, StateKey: "settings",
  	BackgroundColor: theme.Hex(pal.Wash),
  	Content: ui.View(a.settingsView),
  })
  ```

  - **One at a time.** Opening it again focuses the open one (`a.settingsWin`). It closes with the main window, because it has a `Parent`.
  - **Why not an overlay in the main window:** a separate window keeps the terminals visible while you change appearance settings, so you see the result live. It can also stay open beside the app.
  - **Hyprland:** a window with a parent should float as a dialog. Test this on Omarchy. If it tiles, set a size hint or ask mygo for a dialog type.
- **Threading:** mygo builds every window's view on the main thread, so `settingsView` reads and writes `a.settings` directly.
  - After a change, call `a.applySettings()` as the palette does, then `a.win.Invalidate()` so the main window redraws.
  - `saveSettings` already writes atomically.
- **Opening it:**
  - **Ctrl+,** (`app.settings` in `keybindings.go`, global scope; `ui.KeyComma` exists, and nothing uses it);
  - **"Settings…"** in the palette;
  - a gear in the status bar, next to the existing icons;
  - **`omaterm settings [page]`**, through the control socket, so the docs, a toast or an agent can link to a page.

  A deep link opens straight to a page: `a.openSettings("dev-urls")`.
- **Layout:** like macOS System Settings. See "Look" below.
- **Platform look:** the same everywhere. omaterm already looks the same on all three OSes.

## Look: macOS System Settings' structure, omaterm's skin
Decided 2026-10-25. The window works like System Settings on macOS 13 and later: a sidebar with search, pages of grouped rows, drill-down subpages and sheets. It looks like omaterm, with the same wash, cards, rows, icons and elevation as the main window. It should feel like part of the app, not a macOS port, and it is the same on every OS.

```
┌───────────────────────┬──────────────────────────────────────────────┐
│ ● ● ●                 │  ‹ ›  Web Access                             │
│ ┌───────────────────┐ │                                              │
│ │ 🔍 Search          │ │  ╭────────────────────────────────────────╮  │
│ └───────────────────┘ │  │ [icon]  Web Access                     │  │
│                       │  │         Use your sessions from a phone │  │
│  ◇  General           │  │         or another computer.  Learn…   │  │
│  ◇  Appearance        │  ╰────────────────────────────────────────╯  │
│  ◇  Notifications     │                                              │
│                       │  ╭────────────────────────────────────────╮  │
│  ◇  Agents            │  │ Web Access                        (●─) │  │
│  ◇  Web Access     ◀  │  ├────────────────────────────────────────┤  │
│  ◇  Dev URLs          │  │ On your network                   (─○) │  │
│  ◇  Browser           │  ├────────────────────────────────────────┤  │
│                       │  │ Link   http://omarchy:7370      [Copy] │  │
│  ◇  About             │  ╰────────────────────────────────────────╯  │
│                       │  Only devices you pair can open it.          │
│                       │                                              │
│                       │  Paired Devices                              │
│                       │  ╭────────────────────────────────────────╮  │
│                       │  │ iPhone · last seen 2 min ago  [Revoke] │  │
│                       │  ├────────────────────────────────────────┤  │
│                       │  │ Pair a Device…                       › │  │
│                       │  ╰────────────────────────────────────────╯  │
└───────────────────────┴──────────────────────────────────────────────┘
```

- **The title bar is hidden.** Use `TitleBarStyle: mygo.TitleBarHidden`.
  - The sidebar runs to the top of the window, under the window controls: the traffic lights on macOS, GTK's buttons on Linux. Leave room with `c.TitleBar()`.
  - The content side has a toolbar row: back and forward arrows, then the page title in bold. This row drags the window (`DragWindow`).
- **Sidebar:**
  - **Search field at the top.** It filters pages and rows by their titles and keywords from `settingDef`. Matching rows are listed under their page, and choosing one opens the page and highlights the row for a moment, as macOS does. This replaces the earlier "no search box" non-goal: the palette already searches settings, but people expect it in this layout.
  - **Pages in groups,** separated by a little space and no headings: General, Appearance, Notifications | Agents, Web Access, Dev URLs, Browser | About.
  - **Rows look like session rows** (`sidebar.go`): 32 px high, radius 8, a 14 px Lucide icon in `pal.TextFaint`, and 13 px text. Hovered rows are `pal.Hover`. The selected row is `pal.RowSelected`, lifted by `chosen()`, with the same 110 ms color animation.
    - **Icons are not colored squares.** Monochrome icons keep the theme's look, especially under Omarchy themes.
    - Suggested icons: General `settings`, Appearance `palette`, Notifications `bell`, Agents `bot`, Web Access `smartphone`, Dev URLs `globe-lock`, Browser `globe`, About `info`.
  - **Group gaps,** where macOS has them, can use small labels in the style of the sidebar's "Projects" heading (11 px, weight 600, `pal.TextFaint`), or just space.
  - **Sidebar background:** the window's wash (`pal.Wash`), as in the main window, where the sidebar sits on the wash and the content sits on cards.
- **Content:** one big card on the wash, like a pane: `raised()`, radius as the pane cards, with the same gap from the window edges as `a.paneGap()`. Inside it is a scroll view whose content has a max width of 560 px, centered, with 20 px margins.
  - **Hero card,** at the top of pages that explain something (Web Access, Dev URLs, Agents): a big icon, the title, two lines of text and a "Learn more…" link to the website section.
  - **Grouped boxes** (inset grouped lists):
    - inside the content card, a group is a rounded box (radius 8) one step off the card, in `pal.Wash` or `pal.Input`, with a `pal.Border` hairline. This is how omaterm draws inputs and the palette's search row, so it doesn't become a card in a card with shadows;
    - rows are 36–44 px high, separated by hairlines inset from the left edge;
    - the label is on the left, with an optional muted second line;
    - the control is on the right.
  - **Group header:** a 12.5 px, weight 600 heading in `pal.TextMuted` above a group (as the project names in the sidebar), used only when the page has several groups.
  - **Group footer:** muted 11–12 px text under a group, explaining it. It is used where macOS puts its explanations, such as "Applies after you restart omaterm."
  - **Controls:**
    - **Switches** are small and on the right, using mygo's switch themed with `pal.Accent`.
    - **Choices** use a popup button with ⌃⌄ chevrons (mygo `select`). A segmented control is used only for 2–3 visual choices, such as the Theme row, which shows three small preview tiles (Omarchy, Dark, Light), as macOS shows Appearance.
    - **Buttons** use omaterm's own: `face()` for normal buttons ("Revoke", "Copy"), and `ui.PrimaryButton` for the main action, such as "Set Up…", as in the agent setup banner.
    - **Drill-down rows** end with a chevron `›` and open a subpage, with the back arrow in the toolbar. Examples: Paired Devices › a device, Agents › Claude Code.
    - **The ⓘ button** on a row opens a small sheet with the details, as macOS does. Example: what the Dev URLs setup changes.
- **Sheets:** confirmations, such as Remove Dev URLs, Revoke All and the setup's list of changes, are dialogs in omaterm's style, like the pairing dialog: `floating()` over a dimmed window. Use `ui.Modal` inside the settings window, not a separate window.
- **Window size:** 715 × 600 by default, like System Settings. The width can grow, but the content stays at its max width. The minimum is 600 × 420. The size is remembered with `StateKey: "settings"`.
- **Motion:** the page swaps without a slide, as macOS does; subpages push and pop with a short slide. Neither moves when Reduce Motion is on.

## Pages

### General
| Row | Control | Setting | Note |
|---|---|---|---|
| Keep sessions running when the window closes | switch | `daemon` | Applies after a restart; offer "Restart Session Daemon" (`daemon.restart`). Hidden on Windows. |
| Restore sessions at launch | switch | `restoreSessions` | |
| Resume agents in their own conversation | switch | `resumeAgents` | Disabled while restore is off. |
| Snapshot each agent turn (Last turn) | switch | `gitStatus` | Its only reader is the snapshot (`changes.go:395`); see docs/research/settings.md. |

### Appearance
| Row | Control | Setting |
|---|---|---|
| Theme | segmented: Omarchy · Dark · Light | `theme` via `setThemeMode`. Omarchy only shows on Omarchy. |
| Density | segmented: Comfortable · Compact | `density` |
| Background | segmented: Wash · Plain | `background` |
| Dim unfocused panes | switch | `dimUnfocused` |
| Minimum contrast | slider 1–7, plus "Automatic" | `minimumContrast` (0 = automatic) |
| Reduce motion | switch | `reduceMotion` |
| Focus follows mouse | switch | `focusFollowsMouse` |

### Notifications
| Row | Control | Setting |
|---|---|---|
| When a session needs you | switch | `notify.waiting` |
| When a session finishes | switch | `notify.done` |
| When a session fails | switch | `notify.error` |

Later, with solo.md 6.3: "When a process crashes", and a level per session.

### Agents
- **The integration.** For each agent omaterm knows (`internal/agent`): its name, whether its hook is installed (`cli.HookInstalled`), and **Set Up** or **Installed ✓**. Set Up runs `a.setupAgents`. The skill state is shown once for all agents.
- **Let Agents Control the App:** a switch for `remoteControl`. Explain it: the `omaterm` command may run the app's commands and drive browser panes, not only sessions.
- **Allow the omaterm command:** a switch for `controlSocket`. It applies after a restart. Turning it off warns that agents lose `omaterm`.

### Web Access
- **Status:** on or off (`setWeb`), "On your network" (`setWebNetwork`), and the link with Copy. A plain-HTTP warning shows when on the network without Tailscale or a URL.
- **Pair a Device…** opens the existing pairing dialog (`webPairDialog`) with its QR code.
- **Paired devices:** a list from `webStore().Devices()`, with name, paired date and last seen, and **Revoke** per row (`Store.Revoke`). The daemon already ends a revoked device's sockets within 5 s. Add **Revoke All**.
- **Advanced:** port and custom URL (`web.json`), set by the CLI today.

### Dev URLs (with solo.md 6.2b)
- **Setup checklist,** checked live, each row ✓ or ✗ with a short reason:
  - the proxy is running and serves 443;
  - the certificate is trusted by the system;
  - the certificate is trusted by Chrome and Firefox (NSS);
  - `*.localhost` names resolve.
- **Set Up…** shows exactly what will change before asking for anything. On Linux:
  - a sysctl file, `/etc/sysctl.d/50-omaterm.conf`, which lets local programs use ports 443–1023;
  - the CA in the system trust store;
  - the CA in the NSS databases, as you, with no password.

  Then it runs `omaterm proxy setup --system` through `pkexec`, so the desktop's own password dialog shows. On macOS it uses `osascript … with administrator privileges`; Windows needs no elevation.
  - **Without a polkit agent:** offer "Run in a Terminal", a session running `sudo omaterm proxy setup --system`.
  - **Progress and errors** show in the page, not only in a toast.
- **Remove** undoes everything (`omaterm proxy uninstall`), with the same confirmation.
- **Routes:** a live list of name, URL and the session or process behind it, with **Open** (browser pane) and **Copy**.

### Browser
- **Block ads:** a switch for `browserBlockAds`, once it is wired up (see Why).
- **Clear browsing data:** this exists only if the browser plugin supports it. Check first.

### About
- Version, update state and **Check for Updates** (the updater exists, see `updater_test.go`).
- Links to the website, the changelog and issues.
- Where the files are: settings, state and web, with **Reveal**.

### Projects (later, with solo.md 6.1)
For each project:
- `omaterm.yml` status;
- trusted commands, with **Revoke Trust**;
- autostart.

## Code
- **`settingswin.go`:**
  - `openSettings(page string)`;
  - the window and `settingsView`;
  - the sidebar, with search and page rows styled like session rows;
  - the toolbar, with back and forward and the subpage stack;
  - the macOS-style building blocks, which later pages reuse:
    - `settingsHero(icon, title, text, link)`;
    - `settingsGroup(header, footer, rows)`, a rounded box with inset hairlines between rows;
    - the rows `settingSwitch`, `settingPopup`, `settingButton` and `settingLink` (drill-down `›`);
    - `settingsSheet` for confirmations.
- **One file per larger page:** `settings_web.go`, `settings_agents.go`, and later `settings_devurls.go`.
- **`settings.go`** keeps the model. Add a small `requiresRestart` set, so the UI can mark those rows.
- **Shared toggles:** move the palette toggle table into a list of `settingDef{ID, Page, Title, Keywords, Get, Set}`, used by both the palette and the switches. Then a new boolean setting is added in one place.
- **State:** `a.settingsWin *mygo.Window` and `a.settingsPage string`. Remember the page in `ui.json` (`uiprefs.go`), so it reopens where you left it.

## Tests
- A `ui_test.go`-style test builds `settingsView` for each page with the fake backend. Toggling a switch changes `a.settings` and calls `applySettings`, and the palette's `On` agrees afterwards.
- `TestSettingsCoverAll` reflects over `Settings` and fails when a field has neither a row nor an entry in an explicit "no UI" list, so a new setting can't be forgotten.
- Web Access: revoke from the page removes the device, using the store in a temporary `XDG_CONFIG_HOME`.
- Screenshots for the website: add a `settings` scene to `site_shots_test.go`, with the Appearance page and the Web Access device list.

## Rollout
1. **The window and the simple pages:** shell, General, Appearance, Notifications, Agents, Web Access (with devices and Revoke), About. This is a release on its own.
2. **Dev URLs page,** with the proxy (solo.md 6.2b).
3. **Projects page,** with `omaterm.yml` (solo.md 6.1).

## Open questions
- **On Hyprland,** with no window decorations, the hidden title bar leaves the top of the sidebar empty. That is fine, but check that the toolbar row still lines up.
- **The gear icon** in the status bar: worth the space, or are Ctrl+, and the palette enough?
- **`browserBlockAds`:** wire it up (does the browser plugin support content rules? mygo has `content_rules.go`) or drop it.
