# Changelog

All notable changes to OmaTerm are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[SemVer](https://semver.org/) (`0.x` pre-1.0: minor bumps carry features).

## [0.4.0] — 2026-10-09

A multi-repo release: a project root holding several repositories now gets
VS Code-style Source Control treatment, and every Git surface — status,
history, branches, stash, sync, blame and diffs — follows the active
repository instead of always reading the project root. Plus terminal input
and history-restore polish.

Full diff: https://github.com/fachrihawari/omaterm/compare/v0.3.0...v0.4.0

### Multi-repo projects (M20)

- **Depth-1 discovery.** A project root that is not itself a repo resolves
  to its direct child repositories (root `.git` dir or worktree gitfile);
  the root itself wins the fast path when it is a repo. Bounded
  readdir+stat scan, no git subprocess, off the UI thread.
- **VS Code-style Git tab.** A single collapsible `Repositories` group
  header over one plain row per repository (name, muted branch, dirty dot
  or `clean`); the active row is highlighted and its full Source Control
  body renders below. Selecting another repository is a semantic command,
  so the panel, IPC and CLI share one validation path.
- **One active repository for all Git work.** The router owns the depth-1
  scan cache and a `git_root` chokepoint, so `git.*` methods, `omaterm git`
  and the desktop workers all target the active repository. The Files panel
  keeps the project root.
- **Per-repository state.** Status, history/Graph, branch lists, stash
  drafts/list, blame and both diff paths are keyed by repository, so
  switching repositories never shows another repository's stale rows and
  switching back restores each surface. The depth-1 scan refreshes one
  repository per poller tick.
- **Active repo persists** across restart (snapshot v4) with stale-name
  fallback to the first-sorted repository; the `Repositories` list keeps
  its collapse state for the session.
- New commands: `omaterm project repos [--json]` and
  `omaterm project set-repo <name>`, wired as `project.repos` and
  `project.set-active-repo`.

### Terminal & UX

- **Shift+Enter** — modified Enter now encodes Kitty CSI-u so terminals and
  TUIs distinguish newline from submit; plain Enter stays a bare CR.
- **Mouse forwarding** — left press/release forward SGR mouse reports to
  mouse-aware TUIs when DECSET mouse + SGR are enabled; Shift+click still
  selects.
- **History restore fidelity** — compaction no longer collapses differential
  readline redraws, and the equivalence check requires scrollback multiset
  coverage so restored lines cannot come back mangled.
- **Inspector Info** drops the duplicated project card and third branch
  switcher (the Git tab and status bar remain).
- **Stash group** hides when empty and shows push-only input on a dirty tree
  with no entries.

### Fixed

- The CLI test harness shares one process-wide environment lock across the
  connection and launcher tests, removing a rare parallel-run flake where
  `OMATERM_SOCKET`/`OMATERM_TOKEN` clobbered each other.

## [0.3.0] — 2026-10-09

A theme-and-platform release: a full light palette with live system
follow and palette switching, Windows support (build, IPC, shells), and
a security audit batch covering Git, search, history, and IPC. Terminal
grid reconciliation is reworked so panes never render stale sizes.

Full diff: https://github.com/fachrihawari/omaterm/compare/v0.2.0...v0.3.0 (20 commits).

### Theme system

- **Light palette** alongside the frozen dark one, driving chrome,
  terminal defaults, ANSI colors, and OSC 10/11/12 replies from one
  source; `COLORFGBG` follows the palette and the cursor color reaches
  the renderer.
- **`system` follows the desktop live**: Omarchy theme switches apply
  without a restart (portal `color-scheme` → GPUI appearance, with a
  per-frame reconcile that also heals the startup race).
- **Palette commands** (`Preferences: Toggle Theme`, explicit Dark /
  Light / Follow System) apply instantly — chrome repaints, sessions get
  a foreground-group SIGWINCH redraw, snapshots refresh — and persist to
  `config.toml` with a comment-preserving writer.
- Editor and diff views inherit the shared tokens (contrast-verified);
  file icons get a light ink transform.

### Terminal correctness

- Hybrid grid reconciliation (synchronous render-time pass plus
  paint-time canvas refinement) with a shared Resize / HealSnapshot /
  Steady decision, so closing a sibling or zooming can never latch a
  small grid with blank space again.
- Inverse/DIM render against the actual cell background, wide-glyph
  cursor widths, concealed-glyph width preservation, mouse-aware wheel
  scrolling for full-screen apps, and a `terminal_key` map that fixes
  Windows space handling.

### Windows support

- Windows build target (console-free desktop launch), Unix/Windows IPC
  split over named pipes with per-user ACL identity, and PowerShell /
  Command Prompt / Git Bash selection with a palette picker, persisted
  `terminal.shell`, and per-pane shell restore across restarts.

### Git & history

- New `git_input` / `git_sync_panel`: owned background workers with
  shutdown joins, elapsed-time pending labels, and worker-disconnect
  recovery instead of stuck spinners.
- Restore backlog drains large workspaces in waves instead of failing
  past the launch-queue bound; repaint-run compaction keeps animated
  redraws from bloating scrollback (replay-verified, original kept on
  any doubt); per-pane clear epochs stop queued saves from resurrecting
  deleted history.

### Security fixes

- Git remote option injection rejected (`--upload-pack=...` no longer
  accepted as a remote) at validation, runner, and IPC bridge layers.
- Terminal search no longer panics on case-folding length changes
  (e.g. `İ`); folded offsets map back to original char indices.
- Git timeouts now reap the whole process tree (setsid + killpg) and
  join pipe readers instead of leaking threads on hook descendants.
- Windows named-pipe writes are deadline-bounded so a wedged client
  cannot wedge workers or shutdown.

## [0.2.0] — 2026-10-08

A Git-workspace release: the sidebar Git tab grows history, branches,
stash, sync and blame, while the terminal, files, editor and overall UI
get substantial polish. CLI/IPC keep full parity for the new Git surface.

Full diff: `v0.1.0...v0.2.0` (47 commits).

### Git tab

- **History graph** with commit rows (subject, short OID, author, age,
  root/merge markers), lazy per-commit file expansions, parent-specific
  historical diff previews (read-only Split/Inline), scope toggle
  (current branch / all local branches), and load-more paging to 100.
- **Branch picker** on every branch surface (Git header, Inspector pill,
  status bar, palette): checkout/create/rename/delete, upstream tracking
  icons, two-step delete, auto-close on checkout.
- **Stash group**: push with message + untracked toggle, Pop/Apply/Drop
  rows with two-step drop.
- **Fetch/Pull/Push buttons** run off the UI thread with per-op spinners,
  toasts, and honest errors (`no_upstream`, auth, offline, diverged,
  non-fast-forward) — no more UI hangs.
- **Blame gutter** with toggle, commit badges and parent info.
- **Commit box** (staged changes, single-line draft with blinking caret),
  per-file stage/unstage, explicit discard confirmation dialog.
- Clean trees hide the commit UI; upstream label removed as redundant.

### Terminal

- **Find in scrollback** (`Ctrl+Shift+F`) with match navigation.
- **Pane zoom** (`Alt+Z`-family chords) and **terminal font zoom**.
- Smooth wheel ease-out, touchpad momentum, and fractional scroll
  rendering; grid-only panes (header/footer chrome removed, split/close
  toolbar floats over the grid).

### Files & editor

- File tree and Git rows use the vendored Lucide icon set; dotfiles shown
  by default; Alt-chord keyboard navigation; inline tree filter removed
  (`Ctrl+P` is the sole finder).
- Built-in syntax highlighting extended to 24 languages; diff views get
  per-language highlighting that never alters staged/copied bytes.

### UI system

- Centralized theme tokens, measured type roles, unified corner radii,
  hover states without layout shift, Lucide chevrons/dots, floating
  stackable toasts (success/error), scrolling tab strip, bounded overlay
  lists with keyboard follow, unified header icon cluster.
- `Ctrl+Shift+W` now closes the active surface (editor document, diff
  preview, or terminal pane).

### CLI / IPC

- Full parity for the new surface: `git log/history`, branch
  list/create/checkout/delete/rename, stash list/push/show/apply/pop/drop,
  fetch/pull/push, blame, `diff show-commit` — same dispatcher path as the
  UI, with human and `--json` output. Wire-method matrix covers all 44
  methods.

### Fixes

- Project context menu as window-level overlay; dual-clipboard selection
  with honest copy feedback; plain-`f`/digit jump keys; blame CLI path
  handling; commit-input caret position and blink.

## [0.1.0] — 2026-10-01

Initial release: terminal-first workspace (GPUI desktop + `omaterm` CLI),
projects/tabs/panes, persistence, command router, Unix-socket IPC, Git
status panel, diff viewer groundwork, and AUR/tarball packaging.
