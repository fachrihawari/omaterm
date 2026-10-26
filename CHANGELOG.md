# Changelog

The section of a version becomes the release notes of its update, so each
one is written for the user: what changed, and why it matters.

## 0.9.8

### Files

- **The Files tab shows every file, and dims what git ignores**, as
  VS Code does. Dotfiles such as `.github` and `.env` were missing, and
  ignored files such as build output looked like source. `.git` stays
  hidden, and an ignored folder such as `node_modules` is read only as
  you open it.

## 0.9.7

Things that worked, but not quite right.

### Git

- **Every file name works in the Git panel.** A name with a space or an
  accent couldn't be staged or discarded, and git's warnings showed as
  files.
- **Merge conflicts show as conflicts**, marked "!", with Mark Resolved.
  They looked untracked, and Discard did nothing to them.
- **Each diff tab shows its own diff**, named for its file. All of them
  were "Diff", and showed whichever diff was opened last.

### Files and the editor

- **Quick open and the Files tab show the files as they are now.** A
  file an agent made didn't appear until a restart. Opening a file that
  is gone says so.
- **Alt+Up and Alt+Down move lines in the editor**, rather than
  switching sessions.
- **A very large file or picture shows a note** rather than filling
  memory: a log of gigabytes, or a picture claiming 30000×30000 pixels,
  could get the app killed.

### Smaller things

- **Closing a pane leaves the keyboard in the one next to it**, and
  Ctrl+W with "No file open" on screen no longer closes a session.
- **Settings' text fields behave:** Escape closes the window again
  after a field disappears, the web port doesn't refill while you clear
  it, and a half-typed shell isn't used.
- **The Info tab's open button for a port opens its page.** The
  editor's two buttons that did nothing are gone, and the Keyboard page
  shows Alt+1 to Alt+9 rather than the first two.
- **Cyrillic text no longer confuses session status**, and a theme's
  palette is read right when it repeats a color.

## 0.9.6

No more hangs, and what you see stays true.

### Nothing hangs

- **Global search returns** when it finds more than it shows. A broad
  search used to show nothing at all.
- **`omaterm wait` ends when its session closes**, rather than waiting
  for ever.
- **Committing no longer freezes the window** while the repo's
  pre-commit hooks run, and neither do checkout, the branch list or
  turning web access on and off.
- **Reconnecting to the session daemon keeps trying until it connects.**
  After 10 seconds it gave up, said it was connected, and left every
  pane blank.

### What you see stays true

- **Sessions made from your phone or the `omaterm` command while the
  app was reconnecting are kept**, and a session you closed meanwhile
  stays closed.
- **A session you close always ends**, even when the session daemon was
  slow or away. Its programs used to run on unseen.
- **The session on screen stays read** after the daemon reconnects.
- **The branch shown follows the repo** after a `git switch` in a
  terminal.
- **Git says why it failed:** a hook's verdict, a missing `user.email`,
  rather than "exit status 1".
- **Edits you make in `settings.json` while omaterm runs are kept.** A
  zoom or a theme change wrote over them. A wrong value now passes over
  that one setting, rather than resetting them all.
- **A command answered "busy" never runs later.** An agent that tried
  again made a second session.

### Steadier underneath

- **A program printing notifications in a loop** shows one every 5
  seconds, not thousands.
- **A slow phone on web access** no longer makes the session daemon
  hold hundreds of megabytes of screens.
- **The session daemon doesn't quit while a command is under way.**
- **Paired devices stay revoked**, however many omaterm processes change
  web access at once.
- **Setting up the agent integration keeps a symlinked settings file**
  a dotfiles manager tracks.
- **Sockets that don't fit their folder** go in a folder only you can
  open, not the shared `/tmp`.

## 0.9.5

Nothing lost, and no crashes: a second look at what could go wrong.

### Your work is safer

- **Unsaved edits are never lost unasked.** Closing an editor's tab with
  its ×, quitting, closing the window and Restart Now now ask first. They
  used to drop your changes without a word.
- **The editor follows its file on disk.** A file an agent changes while
  it's open now shows the change, and saving no longer writes your old
  text over theirs: if both changed, omaterm asks. Saving can no longer
  leave a file empty when the disk is full.
- **Discard and stash Drop ask first.** Both are one click on an icon
  that appears next to Stage, and can't be undone. A quick second Drop no
  longer drops the wrong stash.
- **Removing a project says what it ends:** a running agent or server,
  and unsaved files. Close Pane in the palette asks when a program still
  runs, as the pane's × does.
- **Turning "Start the session daemon at login" on or off asks first.**
  It restarts the daemon, which ends the programs your sessions run.
- **"Run Without It" no longer resumes each agent twice.** The other
  version's daemon still runs your sessions; a banner says so.
- **Agents can't end every session.** With "Let agents control the app"
  on, `omaterm do` could restart the session daemon, set up the agent
  integration, install the command or pair a device. It can't any more.

### omaterm stays up

- **The session daemon no longer crashes when a pane is started twice
  at once**, which could happen at login, taking every session with it.
- **No crash when the last project is removed while it loads.**
- **A project's Git status, files and branch stay with it.** Switching
  projects quickly could show the last one's changes, so Stage and
  Commit acted on its repository.
- **A closed session's scrollback is gone from disk for good** with the
  session daemon too, and old scrollback no session holds is cleaned up.
- **Built with Go 1.27.3**, which fixes vulnerabilities web access could
  be reached through.

## 0.9.4

Polish: omaterm looks like one app from corner to corner.

- **One dialog style.** Confirmations in the main window, sheets in
  Settings and the Pair dialog share one width, place, title and button
  size, with red for the button that destroys something.
- **One header for every view.** An editor, a picture, a diff and the
  Git graph have a terminal pane's header: as tall, with its title, so
  switching tabs no longer moves the content.
- **Changed files read alike** in the Git tab, Changes and a commit's
  files: the file's icon, its name with its folder beside it, and its
  status letter at the end, in rows of one height.
- **The Git graph's columns line up** in every row, wherever a branch
  opens.
- **The command palette's titles start at one edge;** sessions and
  projects name their kind as commands and settings do.
- **Banners are built alike:** the agent setup offer shows a bot, and
  putting something off reads "Not Now" everywhere.
- **Long branch names use the room there is** in the status bar and the
  Info tab before they're cut.
- **Light themes:** pane cards have a hairline edge, and the focused
  pane of a split shows its ring.
- **Smaller things:** folders come first in the file tree; the Git
  graph shows no dash before its count is known; the branch picker's
  "current" badge is legible on the chosen row; a markdown preview
  doesn't show a caret position; the phone key bar's keys have room
  around their labels; Settings' "Open settings file" lines up with the
  pages above it.

## 0.9.3

A steadier main window, and fewer surprises.

### Dialogs and windows

- **A confirm dialog acts on what it asked about.** A session closed or
  a tab moved from the command line while "Close this tab?" was open
  could make it close a different one, possibly a running session.
- **Dialogs come first.** The command palette or a picker no longer
  opens over a dialog waiting for your answer, and a dialog that opens
  closes the picker that was up.
- **Pickers dim what is behind them,** and global search keeps its full
  height while it has few results.
- **Narrow panes keep Close and Zoom** in reach; the git and status chips
  shrink first.
- **Tabs that don't fit scroll** with the shown one kept in view, and
  "+" stays put. They used to be cut off with "+" out of reach.
- **The sidebar footer fits a narrow sidebar.**
- **No stray shell inside a diff or an editor tab.** One was started
  there, out of sight, and kept running.
- **The status bar's process and port items open the Info tab.**
- **A session rename is kept when you click away,** as Enter keeps it.
- **The diff header reads plainly:** "Index → Working Tree" or
  "Staged: HEAD → Index".

### Sessions

- **Restarting a session runs its command again,** rather than leaving
  a bare shell: a dev server comes back as a dev server.
- **A program that fails says so:** its session shows as failed with
  "<command> exited (code N)", and a shell that dies on its own tells you
  before its session goes.
- **The omaterm command says why it can't reach omaterm** (permission,
  or the app not answering) rather than only that it couldn't.
- **A closed session's scrollback is gone from disk for good.** A save
  running just as it closed could write it back.
- **The Info tab stops refreshing when you leave it.**

### Housekeeping

- **Files left by the first omaterm are removed:** its old workspace
  files and its shell histories. Your `config.toml` is left alone.
- **The unused "block ads" setting is gone.**

## 0.9.2

This release is about your sessions being safe, and omaterm staying up.

### Your sessions are safer

- **A damaged sessions file no longer wipes your sessions.** If omaterm
  couldn't read its sessions file, it started empty, ended every running
  session (and its scrollback) as unknown, and deleted the file as the
  window closed. It now keeps a copy, leaves the file and every session
  alone, and tells you.
- **A project whose folder is missing keeps its sessions.** When a
  project's folder was gone at launch (an unplugged drive, a rename), the
  project and its sessions were dropped. It now shows as "folder missing",
  its sessions keep running, and it comes back with the folder.
- **Each session reopens as you left it:** the pane you were in, the
  zoomed pane, and the session each project showed. omaterm used to open
  on the first pane of the first session.
- **A name you give a session sticks.** An agent that had named the
  session first renamed it back at its next conversation.
- **Agents resume after the session daemon restarts.** Without the
  systemd service, a daemon restart brought agents back as bare shells.
- **Sessions made from your phone while omaterm was reconnecting** are no
  longer dropped.
- **Upgrading from 0.8 keeps your text size and diff layout.**

### omaterm stays up

- **If omaterm crashes, it keeps the report** and tells you at the next
  launch, with Show Report. Settings › About lists the reports.
- **The session daemon survives a bug in one request,** rather than ending
  every session with it, and a paired device can no longer exhaust its
  memory.
- **A session daemon that stopped answering no longer freezes the window.**
- **No more crash** when showing a project after one of its sessions was
  closed from the command line or a shell exited in it; such sessions
  also no longer stay behind, dead.
- **Status keeps updating after "Restart Sessions".** It froze for the
  rest of the run.
- **Programs reading the clipboard no longer hang** while the window is
  closed.

### Settings

- **Keyboard lists and filters every shortcut itself.** "All Keyboard
  Shortcuts" opened a dialog in the main window, under Settings.
- **Text fields take what you type with Enter,** and Escape puts back
  what they had; switching windows no longer applies a half-typed value.
- **Confirmations that remove something are red,** as elsewhere.
- **Choosing Custom… for the search engine** keeps the current one until
  your URL works.
- **Search lands on every setting it finds,** and Enter opens it.
- The window fits at its smallest size, toasts show in it, and closing and
  reopening it quickly keeps it open.

## 0.9.1

### Closing Settings no longer closes omaterm

Closing the Settings window with its close button, Escape or Ctrl+W
crashed omaterm, taking the main window with it. It now closes only
Settings.

### Settings sits with the sidebar's buttons

The gear moved from the right end of the status bar, where it was easy
to miss, to the bottom of the sidebar, before Open Project and the
command palette.

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
