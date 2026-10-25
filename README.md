<img src="site/assets/logo.svg" width="72" alt="omaterm logo">

# omaterm

**A terminal workspace for running coding agents side by side.** Projects,
sessions and split panes in one native window, with the status of every
agent at a glance, what each one changed in its last turn, and a Git
panel, editor and browser pane next to your shells.

The name stands for **Oh My Terminal**.

**[Website](https://fachrihawari.github.io/omaterm/)** ·
**[Download](https://github.com/fachrihawari/omaterm/releases/latest)** ·
**[Changelog](CHANGELOG.md)**

![omaterm with an agent waiting for permission beside a dev server](site/assets/hero.png)

## Install

On Linux:

```sh
curl -fsSL https://github.com/fachrihawari/omaterm/releases/latest/download/install.sh | sh
```

This installs the app in `~/.local/omaterm.app` and adds it to your app
launcher. It needs no root, and it updates itself: a new release shows up
as an update in the app.

Builds for macOS, Windows and Linux ARM64 are on the
[releases page](https://github.com/fachrihawari/omaterm/releases/latest).

## Features

### See what your agents are doing

Each session shows whether it is working, needs you, failed or is done.
This works for Claude Code, Codex, Gemini, OpenCode, Pi and any other
program that reports its status. When a session needs you while you look
away, you get a notification. Press `Ctrl+Shift+J` to jump to it.

### What did that turn change?

In the Git tab, Last turn (`Ctrl+Shift+A`) lists what an agent changed in
its last turn, apart from everything else you haven't committed. You can keep
or revert each hunk.

![The Git tab's Last turn, listing the files an agent edited in its last turn](site/assets/changes.png)

### Projects, sessions and split panes

The sidebar lists each project with its sessions. Switching projects keeps
the other project's shells running. You can split a pane right
(`Ctrl+Shift+R`) or down (`Ctrl+Shift+D`), resize panes by their dividers
and zoom one (`Ctrl+Shift+Enter`). To move a pane, drag its header onto
another pane.

![Three split panes: an agent, a dev server and a test run](site/assets/split.png)

### Sessions keep running

Sessions keep running after you close the window, and they are back when
you open it again. After a reboot, Claude Code, Codex, OpenCode and Pi
panes resume the same conversation. `omaterm daemon install-service` runs
the session daemon as a systemd user service, so your sessions also come
back when you log in.

### From your phone

Web access shows your sessions in a browser: your projects and sessions in
a sidebar, which agents are working or need you, and any pane as a live
terminal you can type into, with a key bar for Esc, Ctrl and the arrows on a
phone. Turn it on with **Web Access** in the command palette, or
`omaterm web on`. While a browser shows a pane, the pane takes the browser's
size; the window gets its size back when the browser leaves.

omaterm serves it on `127.0.0.1:7370`. To reach it from your phone, carry it
with a tunnel you already trust:

```sh
tailscale serve --bg 7370                      # your tailnet only
cloudflared tunnel --url http://127.0.0.1:7370 # a public https URL
```

Or serve it on your local network with **Web Access on Your Network**, or
`omaterm web on --network` (`--local` goes back). That is plain HTTP, so
use it only on networks you trust.

A browser sees nothing until you pair it: **Web Access: Pair a Device**
shows a QR code, and `omaterm web pair` prints one in the terminal. Each code
works once, for ten minutes. `omaterm web devices` lists paired browsers and
`omaterm web revoke` signs them out.

### Git, diffs and an editor in the same window

The Git panel lets you stage, commit, stash, fetch, pull and push. There is
also a commit graph, side-by-side and inline diffs (including images), a
file tree, and an editor with syntax highlighting and Markdown preview.

| Git panel | Diff |
| --- | --- |
| ![The Git panel with staged changes](site/assets/git.png) | ![A side-by-side diff](site/assets/diff.png) |
| **Commit graph** | **Editor** |
| ![The commit graph with merges](site/assets/graph.png) | ![The editor with a Go file open](site/assets/editor.png) |

### Themes, dark and light

omaterm has dark and light themes, which you pick from the command
palette. The window, terminals and editors share one palette, and its
colors are checked for contrast so text stays readable. On
[Omarchy](https://omarchy.org), omaterm uses the active Omarchy theme and
follows when you switch, with no restart needed.

| Tokyo Night (an Omarchy theme) | Lupine, light (an Omarchy theme) |
| --- | --- |
| ![omaterm in the Tokyo Night theme](site/assets/theme-tokyo.png) | ![omaterm in a light theme](site/assets/theme-light.png) |

### Browser panes

Press `Ctrl+Shift+O` to open a web page next to your terminals, or run
`omaterm split --url localhost:5173`. With "Let Agents Control the App" on (a palette setting), agents can
read, click, type into and screenshot the page with `omaterm browser`.

### The `omaterm` command

Agents and scripts can drive your sessions with the `omaterm` command:

```sh
omaterm split --down -- go test ./...   # run in a new pane below
omaterm wait <pane> --exit              # wait for it to finish
omaterm read <pane>                     # read its screen
omaterm send <pane> "y" --enter         # type into it
omaterm diff --turn                     # what the last agent turn changed
```

`omaterm help` lists every command. `omaterm skill setup` teaches Claude
Code, Codex, Pi and OpenCode to use it. It also lets omaterm tell which
conversation runs in which pane.

## Keyboard shortcuts

| Action | Keys |
| --- | --- |
| Command palette | `Ctrl+Shift+P` |
| Go to file | `Ctrl+P` |
| Search in project | `Ctrl+Shift+F` |
| New terminal | `Ctrl+T` |
| Split right / down | `Ctrl+Shift+R` / `Ctrl+Shift+D` |
| Zoom pane | `Ctrl+Shift+Enter` |
| Go to the session that needs you | `Ctrl+Shift+J` |
| Files / Git / Changes / Info | `Ctrl+Shift+E` / `G` / `A` / `I` |
| Toggle sidebar / inspector | `Ctrl+B` / `Ctrl+Shift+B` |
| Open a browser pane | `Ctrl+Shift+O` |
| All shortcuts | `Ctrl+Shift+/` |

On macOS, use `Cmd` in place of `Ctrl`.

## Building from source

omaterm is written in Go with [mygo](https://github.com/egoist/mygo).

```sh
go test ./...          # tests
bun install            # mygo-cli, for the dev loop and packaging
bun run dev            # run with reload (mygo dev)
bun run build          # build the app (mygo build)
```

To refresh the screenshots in this README and on the website, run
`SITE_SHOTS=1 go test -run TestSiteShots .`. It renders them into
`site/assets`. See [RELEASING.md](RELEASING.md) for how releases are
cut.

## License

[MIT](LICENSE)

## Credits

omaterm builds on other people's work:

- **[Kero](https://github.com/egoist/kero)** by EGOIST (MIT). Much of
  omaterm is adapted from Kero:
  - the session engine: the control protocol, the PTY, OSC status and
    session code, and git turn snapshots
  - the `omaterm` command and its agent hooks, plugins and skill
  - browser panes, pane layout and elevation

  Each such source file names the Kero file it comes from.
- **[herdr](https://github.com/herdrdev/herdr)** (Apache-2.0): the agent
  detection rules, through Kero.
- **[MyGo](https://github.com/egoist/mygo)** (MIT): the app framework.
  **[Ghostty](https://github.com/ghostty-org/ghostty)**'s libghostty-vt
  (MIT) does the terminal emulation.
- **[Lucide](https://lucide.dev)** (ISC) and Feather (MIT): the
  interface icons.
- **[Simple Icons](https://simpleicons.org)** (CC0): the program logos,
  which are their owners' trademarks.

omaterm also learned from projects it shares no code with:

- **[Rex](https://superlogical.com)** by Superlogical: card panes, pane
  headers and status at a glance, and OSC 7501, the Program Status
  Protocol omaterm reads agents' status with.
- **[Tern](https://stencil.so/tern)** by Stencil: how an agent terminal
  should look and feel.
- **[OpenCode](https://opencode.ai)**: its web mode gave omaterm the idea
  of web access.
- **[Omarchy](https://omarchy.org)**: the desktop omaterm was first made
  for, and the themes it follows.

Their license texts, and those of the Go libraries omaterm uses, are in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). The file ships with
every release.
