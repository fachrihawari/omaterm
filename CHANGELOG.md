# Changelog

The section of a version becomes the release notes of its update, so each
one is written for the user: what changed, and why it matters.

## 0.9.0

### A Settings window

Press **Ctrl+,**, or click the gear at the right of the status bar, for a
window with every setting in one place. Search finds any of them, and
changes apply as you make them. `omaterm settings terminal` opens it on a
page.

Many settings had no place to change them but a file. They have one now,
and there are new ones:

- **Terminal:** the font (Omarchy's, or one you name), its size and line
  height, ligatures, the cursor's shape and blink, copy on select, what a
  right click does, whether programs may read the clipboard, the shell,
  how much scrollback a pane keeps, and what a program's bell does.
- **Editor:** the terminal's font, tab size, wrapping, line numbers, and
  whether Markdown opens as a preview.
- **Appearance:** a **System** theme follows your desktop's light or dark
  mode. It's the default where there is no Omarchy theme to follow.
- **Notifications:** with or without a sound, notifications from programs
  themselves, and a button to send a test one.
- **Agents:** each agent's integration, set up or removed in one click.
- **Web Access:** your paired devices, when each was last seen, and
  **Revoke** to sign one out. Pairing a device, the port and a public URL
  are here too.
- **Browser:** the search engine of the address bar (DuckDuckGo, Google,
  Kagi, Brave or your own), and clearing the browser panes' data.
- **About:** automatic updates, and where omaterm keeps its files.

The palette's settings are still there, and agree with the window.

### Ask before closing a running session

Closing a session, or a pane, where an agent or a program still runs now
asks first. Turn it off in Settings, General.

### Zooming a terminal sizes everything

Ctrl+= and Ctrl+- in a terminal, or Ctrl and the wheel, now change the
text of every terminal and editor together, as they do elsewhere in the
window, and the size is kept. Editors and diffs also take the terminals'
font now.

### Reduce Motion stops every animation

Panes no longer spring or fade with Reduce Motion on.

### Smoother scrolling in the browser on a phone

In web access, a flick keeps scrolling and slows down, in a shell and in
Claude Code alike. Suggestions and autocorrect of a phone's keyboard now
type what they change, not the whole line again.

## 0.8.5

### Scroll Claude Code in the browser on a phone

In web access on a phone, swiping over Claude Code, or any program that
draws its own screen, did nothing; only a plain shell scrolled. A swipe
now scrolls it, as a mouse wheel does on a computer.

## 0.8.4

### Your sessions in a browser

Turn on **Web Access** in the command palette, or run `omaterm web on`, and
your sessions open in a browser: projects and sessions in a sidebar, which
agents are working or need you, and any session as a live terminal you can
type into. On a phone it shows one at a time, with a key bar for Esc, Ctrl
and the arrows.

omaterm serves it on your machine only. Reach it from your phone through
`tailscale serve` or `cloudflared` (`omaterm web tunnel` shows how), or on
your local network with **Web Access on Your Network** or
`omaterm web on --network`. That one is plain HTTP, so use it on networks
you trust.

A browser sees nothing until you pair it: **Web Access: Pair a Device**
shows a QR code, and `omaterm web pair` prints one in the terminal. Each
code works once, for ten minutes. `omaterm web devices` lists paired
browsers, and `omaterm web revoke` signs one out and closes what it has
open.

While a browser shows a session, the terminal takes the browser's size.
The window gets its size back when the browser leaves.

### Changes is now part of the Git tab

The Changes tab repeated the Git tab's list of changed files. What only it
had, the files an agent changed in its last turn, is now a switch on the
Git tab: **All changes** or **Last turn**. Ctrl+Shift+A still opens it.

### "Remote Control" is now "Let Agents Control the App"

The setting lets agents run the app's commands and drive browser panes. It
had nothing to do with reaching omaterm remotely, so it has a clearer name,
and it's a setting in the palette like the others. If you had it on, it
stays on.

## 0.8.3

### No more "Data Access Blocked" on macOS

On macOS, omaterm could set off a "Data Access Blocked" notice: with your
home folder open as a project, it looked through ~/Library, where other
apps keep their data. It no longer reads that folder.

A first launch also opens no project now, rather than your home folder.
Open the folder you work in, and omaterm starts there.

### A process list for the project you're in

The Info panel's process list showed processes from every project, and
omaterm itself twice: one of those could be killed, and doing so ended
every session. Each project now lists what its own sessions run, and what
runs in its folder. omaterm's own processes no longer appear.

### Sessions named after your shell

A project's first session was called "dev". It now takes your shell's
name, like any new session, so it shows what's running in it.

## 0.8.2

### A new icon, and a home page

omaterm has a new icon: a prompt in a rounded frame, in the app's blue. It
shows in your app launcher and window list after this update.

omaterm also has a website, https://fachrihawari.github.io/omaterm/, with
screenshots of what it does and the one-line installer.

### Open source, with credit where it's due

omaterm is MIT licensed. The app now ships its license and the notices of
the work it builds on, such as Kero, Ghostty, MyGo, herdr and Lucide, in
LICENSE and THIRD_PARTY_NOTICES.md beside the app.

## 0.8.1

### A cleaner, more readable window

- Panes have round corners all the way around again.
- The highlighted row in the command palette, Go to File, the branch
  picker, search and the shortcuts list is readable in every theme, with
  the letters you typed in bold.
- The palette puts first what you name: "setup" finds Set Up Agent
  Integration, and a category ("setting", "git") lists its entries.
- Buttons that can't act yet, such as Commit with nothing to commit,
  stay legible instead of fading out.
- Narrow panes keep their title readable: the status chip and buttons
  make room instead of drawing over it. Banners stay short in a small
  window.
- The Git panel lines up its file rows, and names the repository in its
  footer.

## 0.8.0

### Your Omarchy theme, everywhere

omaterm takes its colors from the active Omarchy theme: the window, the
terminals and the editors. Switch themes and it follows within a moment,
no restart needed. Light themes are first-class, and the theme's colors are
checked for contrast, so text stays readable on any of them. Off Omarchy,
pick Dark or Light from the command palette.

### Projects and their sessions

The sidebar lists each project with its sessions under it, and switching
projects no longer ends the shells of the one you leave. Panes are cards
you can resize by their dividers, zoom (Ctrl+Shift+Enter), and move by
dragging a header onto another pane: drop it on an edge to split there,
or in the middle to swap.

### See what your agents are doing

Each session shows whether it is working, needs you, failed or is done,
for Claude Code, Codex, Gemini, OpenCode, Pi and other agents, and for any
program that reports its status. When one needs you while you look away,
a notification says so; click it, or press Ctrl+Shift+J, to go there. The
window title counts the sessions waiting on you.

### The omaterm command

Agents and scripts can drive your sessions: `omaterm split --down -- go
test ./...`, `omaterm wait <pane> --exit`, `omaterm read`, `omaterm send`
and more; `omaterm help` lists them. `omaterm skill setup` teaches Claude
Code, Codex, Pi and OpenCode to use it, and tells omaterm which
conversation runs in which pane. "Install Command Line Tool" in the
palette puts it on your PATH.

### Browser panes

Open a web page beside your terminals with Ctrl+Shift+O, or `omaterm split
--url localhost:5173`. With Remote Control on (in the palette), agents can
read, click, type into and screenshot it with `omaterm browser`.

### Sessions come back, and keep running

Sessions now keep running when you close the window, and the next launch
shows them as they went on. A session daemon runs them, and answers the
omaterm command while the window is closed: agents and scripts can still
make, split, drive and wait on sessions, and the window shows what they
did when it opens. `omaterm daemon status` shows the daemon, `omaterm
clients` who it serves, and `omaterm daemon install-service` runs it as a
systemd user service, which brings your sessions back when you log in
(with `loginctl enable-linger`, they keep running while you are logged
out). After an update, a banner offers to restart your sessions in the
new version; they come back with their scrollback, and agents resume
their conversation.

### Agents pick up where they left off

After a restart, a logout or a reboot, a pane that ran Claude Code, Codex,
OpenCode or Pi starts it again in its own conversation. With the agent
integration set up (`omaterm skill setup`, or Set Up in the banner that
offers it the first time an agent runs), that is its exact conversation;
without it, the last conversation of the pane's folder. And when an agent
needs you while omaterm's window is closed, a desktop notification still
tells you: Open shows its session.

### What did that turn change?

The Changes tab (Ctrl+Shift+A) shows what an agent changed in its last
turn, beside your uncommitted and branch changes, and lets you revert or
keep each hunk. `omaterm diff --turn` prints the same. Git status no
longer takes the repository's lock, so it never makes an agent's commit
fail.

## 0.7.0

### The app is omaterm

The project is omaterm, so the app now says so everywhere: the window title,
the menu entry, the configuration directory and the package names. Nothing
else changed: your projects, tabs and settings are read from the old
location the first time and kept from then on.

Install it with the release's own installer, which puts the app in
`~/.local/omaterm.app`:

```sh
curl -fsSL https://github.com/fachrihawari/omaterm/releases/latest/download/install.sh | sh
```

An install of 0.6.x stays where it is; remove it with the old
`install.sh --uninstall` if you want only one copy.

## 0.6.1

### Releases build themselves

A tag now builds every platform and uploads it, signed, to a draft
release: macOS, Windows, Linux and Linux ARM64. CI checks each push and
pull request, so a broken build is caught before it reaches a release.

## 0.6.0

### Updates from inside the app

omaterm can now update itself. It checks the signed releases of the project
in the background, once a day, and offers a newer version with its notes:
install it, skip that version, or be reminded later.

**Info → UPDATES** checks on demand and shows the running version, and
**Check for Updates…** in the command palette does the same. Only releases
signed with the project's key install, so a download from anywhere else is
refused.

### Simpler keybindings

The keybindings are now one short rule each way: **Ctrl** does things, and
**Shift** picks the other one of a pair, while **Alt** goes places.

- `Alt+1` … `Alt+9` jump to a tab, editor or diff; `Alt+Shift+1` … `Alt+Shift+9`
  switch project.
- `Ctrl+Shift+E`, `Ctrl+Shift+G` and `Ctrl+Shift+I` open the Files, Git and
  Info panels.
- `Ctrl+=`, `Ctrl+-` and `Ctrl+0` size the terminal and the editor together.
- `Ctrl+Shift+/` opens the searchable Keyboard Shortcuts dialog.

Chords, function keys and the second key per command are gone. Anything
without a key is still in the command palette.
