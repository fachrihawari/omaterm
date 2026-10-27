<div align="center">

<img src="site/assets/logo.svg" width="80" alt="">

# omaterm

**Oh My Terminal: a terminal workspace for running coding agents side by side.**

[![Latest release](https://img.shields.io/github/v/release/fachrihawari/omaterm?label=release&color=4c8bf5)](https://github.com/fachrihawari/omaterm/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-4c8bf5)](LICENSE)
![Linux, macOS, Windows](https://img.shields.io/badge/platforms-Linux%20%C2%B7%20macOS%20%C2%B7%20Windows-555)

[Website](https://fachrihawari.github.io/omaterm/) ·
[Download](https://github.com/fachrihawari/omaterm/releases/latest) ·
[Changelog](CHANGELOG.md)

</div>

![omaterm with Claude Code asking for permission beside a dev server](site/assets/hero.png)

Run Claude Code, Codex, Gemini, OpenCode and Pi in split panes, in one native
window. omaterm shows you which agent needs you, what each one changed in its
last turn, and keeps your sessions running after you close the window. Git,
an editor and a browser sit next to your shells.

## Highlights

- **Agent status at a glance.** Every session shows whether it is working,
  needs you, failed or is done, and notifies you when it needs you.
- **Last turn.** See what an agent changed in its last turn, apart from your
  own uncommitted work, and keep or revert each hunk.
- **Sessions that keep running.** Close the window and they carry on. After a
  reboot, agents resume the same conversation.
- **On your phone.** Pair a browser with a QR code, then watch and type into
  any session.
- **Everything next to the terminal.** A Git panel, commit graph, diffs, a
  file tree, an editor and browser panes.
- **Scriptable.** The `omaterm` command lets agents and scripts open panes,
  read them, type into them and wait for them.
- **Themes that follow your desktop**, dark or light, including
  [Omarchy](https://omarchy.org)'s themes, live.

## Install

**Linux** (x86-64 and ARM64):

```sh
curl -fsSL https://github.com/fachrihawari/omaterm/releases/latest/download/install.sh | sh
```

This installs the app in `~/.local/omaterm.app` and adds it to your app
launcher. It needs no root.

**macOS** (Universal) and **Windows** (x64): download the `.dmg` or the
`Setup.exe` from the
[latest release](https://github.com/fachrihawari/omaterm/releases/latest).
A `.deb` and `.tar.gz` for Linux are there too.

omaterm updates itself: a new release shows up as an update in the app.

### First steps

1. Open a project folder from the sidebar.
2. Start an agent in a session. Its status shows in the sidebar and in the
   pane's header.
3. Accept the offer to **set up the agent integration**, or run it from the
   command palette (`Ctrl+Shift+P`). It installs a small hook and a skill
   for your agents, so a restart resumes each one's own conversation and
   agents learn the `omaterm` command.
4. To use the `omaterm` command in your own shells too, run **Install Command
   Line Tool** from the palette. It links the command into `~/.local/bin`.

## A tour

### See which agent needs you

Each session shows whether it is working, needs you, failed or is done. This
works for Claude Code, Codex, Gemini, OpenCode, Pi and any program that
reports its status. When a session needs you while you look away, you get a
notification; `Ctrl+Shift+J` jumps to it.

### What did that turn change?

In the Git tab, **Last turn** (`Ctrl+Shift+A`) lists what an agent changed in
its last turn, apart from everything else you haven't committed. Keep or
revert each hunk.

![Last turn, listing the files an agent edited in its last turn](site/assets/changes.png)

### Projects, sessions and split panes

The sidebar lists each project with its sessions, and switching projects
keeps the other one's shells running. Split a pane right (`Ctrl+Shift+R`) or
down (`Ctrl+Shift+D`), zoom one (`Ctrl+Shift+Enter`), and drag a pane's
header to move it.

![Three split panes: an agent, a dev server and a test run](site/assets/split.png)

### Sessions keep running

Sessions run in a session daemon, so they keep going after you close the
window and are back when you open it. After a reboot, Claude Code, Codex,
OpenCode and Pi resume the same conversation.
`omaterm daemon install-service` runs the daemon as a systemd user service,
so your sessions come back when you log in.

### Git, diffs and an editor

Stage, commit, stash, fetch, pull and push from the Git panel. There is a
commit graph, side-by-side and inline diffs (images too), a file tree, and an
editor with syntax highlighting and Markdown preview.

| Git panel | Diff |
| --- | --- |
| ![The Git panel with staged changes](site/assets/git.png) | ![A side-by-side diff](site/assets/diff.png) |
| **Commit graph** | **Editor** |
| ![The commit graph with merges](site/assets/graph.png) | ![The editor with a Go file open](site/assets/editor.png) |

### Your sessions, from your phone

<img src="site/assets/web-phone.png" width="240" align="right" alt="A session on a phone, with a key bar for Esc, Tab, Ctrl and the arrows">

**Web access** shows your sessions in a browser: which agents are working or
need you, and any pane as a live terminal you can type into, with a key bar
for Esc, Ctrl and the arrows on a phone.

Turn it on in Settings, or with `omaterm web on`. It serves on
`127.0.0.1:7370`; reach it through a tunnel you already trust:

```sh
tailscale serve --bg 7370                       # your tailnet only
cloudflared tunnel --url http://127.0.0.1:7370  # a public https URL
```

`omaterm web on --network` serves it on your local network instead. That is
plain HTTP, so use it only on networks you trust.

A browser sees nothing until you pair it: **Pair a Device** in Settings, or
`omaterm web pair`, shows a QR code that works once, for ten minutes. Revoke
a device in Settings or with `omaterm web revoke`.

<br clear="right">

### Browser panes

`Ctrl+Shift+O` opens a web page next to your terminals, as does
`omaterm split --url localhost:5173`. With **Let Agents Control the App** on,
agents can read, click, type into and screenshot the page with
`omaterm browser`.

### Themes, dark and light

Pick a dark or light theme, or follow your desktop's. The window, terminals
and editor share one palette, checked for contrast so text stays readable.
On [Omarchy](https://omarchy.org), omaterm follows the active theme as you
switch it.

| Tokyo Night | Lupine (light) |
| --- | --- |
| ![omaterm in the Tokyo Night theme](site/assets/theme-tokyo.png) | ![omaterm in a light theme](site/assets/theme-light.png) |

### Settings

`Ctrl+,` opens the Settings window: theme, terminal, editor, notifications,
agents, web access and updates, with search, and changes apply as you make
them. They live in `~/.config/omaterm/settings.json`.

## The `omaterm` command

Agents and scripts drive your sessions with it:

```sh
omaterm split --down -- go test ./...   # run in a new pane below
omaterm wait <pane> --exit              # wait for it to finish
omaterm read <pane>                     # read its screen
omaterm send <pane> "y" --enter         # type into it
omaterm diff --turn                     # what the last agent turn changed
```

`omaterm help` lists every command. `omaterm skill setup` teaches Claude
Code, Codex, Pi and OpenCode to use it.

## Keyboard shortcuts

| Action | Keys |
| --- | --- |
| Command palette | `Ctrl+Shift+P` |
| Settings | `Ctrl+,` |
| Go to file | `Ctrl+P` |
| Search in project | `Ctrl+Shift+F` |
| New terminal | `Ctrl+T` |
| Split right / down | `Ctrl+Shift+R` / `Ctrl+Shift+D` |
| Zoom pane | `Ctrl+Shift+Enter` |
| Go to the session that needs you | `Ctrl+Shift+J` |
| Files / Git / Last turn / Info | `Ctrl+Shift+E` / `G` / `A` / `I` |
| Toggle sidebar / inspector | `Ctrl+B` / `Ctrl+Shift+B` |
| Open a browser pane | `Ctrl+Shift+O` |
| All shortcuts | `Ctrl+Shift+/` |

On macOS, use `Cmd` in place of `Ctrl`.

## Building from source

omaterm is written in Go with [MyGo](https://github.com/egoist/mygo).

```sh
go test ./...    # tests
bun install      # mygo-cli, for the dev loop and packaging
bun run dev      # run with reload
bun run build    # build the app
```

`SITE_SHOTS=1 go test -run TestSiteShots .` renders the screenshots in this
README and on the website into `site/assets`. [RELEASING.md](RELEASING.md)
covers how releases are cut.

## Credits

omaterm builds on:

- **[Kero](https://github.com/egoist/kero)** by EGOIST (MIT). Much of omaterm
  is adapted from it: the session engine (control protocol, PTY, OSC status,
  git turn snapshots), the `omaterm` command with its agent hooks, plugins
  and skill, and browser panes, pane layout and elevation. Each such source
  file names the Kero file it comes from.
- **[herdr](https://github.com/herdrdev/herdr)** (Apache-2.0): the agent
  detection rules, through Kero.
- **[MyGo](https://github.com/egoist/mygo)** (MIT), the app framework, and
  **[Ghostty](https://github.com/ghostty-org/ghostty)**'s libghostty-vt
  (MIT), the terminal emulation.
- **[Lucide](https://lucide.dev)** (ISC) and Feather (MIT) for the icons, and
  **[Simple Icons](https://simpleicons.org)** (CC0) for the program logos,
  which are their owners' trademarks.

And learned from, sharing no code:
**[Rex](https://superlogical.com)** by Superlogical (card panes, pane headers,
and OSC 7501, the Program Status Protocol omaterm reads agents' status with),
**[Tern](https://stencil.so/tern)** by Stencil (how an agent terminal should
look and feel), **[OpenCode](https://opencode.ai)** (whose web mode inspired
web access) and **[Omarchy](https://omarchy.org)** (the desktop omaterm was
first made for).

License texts for these and the Go libraries omaterm uses are in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md), which ships with every
release.

## License

[MIT](LICENSE)
