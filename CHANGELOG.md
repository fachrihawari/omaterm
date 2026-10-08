# Changelog

All notable changes to OmaTerm are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[SemVer](https://semver.org/) (`0.x` pre-1.0: minor bumps carry features).

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
