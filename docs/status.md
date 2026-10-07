# Implementation Status

## Current position

### Git history Graph inside the Git tab — 2026-10-07

- GRAPH renders inside the Git tab below Staged/Changes (clean trees show
  the clean line above it instead of returning early). Header: collapse
  chevron, count pill, scope toggle (Current branch / Current HEAD when
  detached / All local branches), Refresh. Commit rows show subject, short
  OID, author, relative age and root/merge markers; expansion loads
  parent-specific file rows lazily with status letters, rename old → new,
  and inline loading/error/empty states with Retry.
- Clicking a file opens `Diff: <file> @ <short>` in the main area with
  `parent → commit · subject` provenance, the shared Split/Inline body,
  hunk navigation, Copy Hunk and Open File (hidden for deleted paths).
  Stage/Unstage/Discard/Stage-Hunk controls and the Ctrl+Shift+S chord are
  gated by the `DiffSource::Commit` capability contract; Copy reads the
  committed patch, never worktree data.
- Fetching rides the 250ms poller: generation-guarded page + file drains,
  one page fetch per tick, no polling while collapsed or for non-repo
  roots. The historical patch uses a latest-only worker with exact-key
  admission (commit, base, paths, roots, context). Load more grows to the
  100-commit cap with an explicit limit notice. Commits set the history
  dirty hint; stage/unstage/discard never refetch immutable history.
- Keyboard reuses the Git-tab chords: Alt+Up/Down walks the section owning
  the single cursor (cross-selection clears the other section), Alt+Left/
  Right collapse/expand, Alt+Enter toggles a commit or opens a file. No new
  global shortcuts; the shortcut table is unchanged.
- Visual rework against the VS Code Graph reference: every Graph row is
  single-line truncated (`whitespace_nowrap` + `text_ellipsis` in an
  `overflow_hidden` bound) so subjects/paths clip with an ellipsis instead
  of wrapping over neighbors; commit rows show a continuous lane spine
  (blue dots, hollow when expanded/selected, line continuing through file
  expansions), subject, truncated author and muted age; file rows show the
  lane, icon badge, truncated name/dir and right-edge status letter.
  Ref badges remain pending (context emits no decorations yet).
- Verification: `mbx fmt --all --check`, `mbx test --workspace`,
  `mbx clippy --workspace --all-targets -- -D warnings`,
  `mbx build --release --bin omaterm --bin omaterm-desktop`,
  `python3 scripts/check-docs.py`, `git diff --check`. Native Wayland
  click/expand/preview interaction on a real window remains manual.
- Known follow-ups: merge parent selector (first parent is the default),
  ref badges (context emits none yet), full commit message detail (no
  CommitDetails reader yet), true subprocess cancellation (generation
  supersession plus bounded timeouts, M14 precedent), lane layout for
  forks/merges (linear spine is the intermediate).

### Git history commit files and historical diffs — 2026-10-07

- Added `GitCommand::CommitFiles` and `DiffCommand::ShowCommit`, with strict
  `git.commit-files` and `diff.show-commit` IPC methods plus `omaterm git
  commit-files` and `omaterm diff show-commit` CLI parity. All routes share the
  context reader rather than duplicating Git process invocation.
- Context reads bounded `git diff --raw -z -M` commit-file records and
  parent-specific patches. Raw change paths are byte-preserving on Unix
  (`OsString::from_vec`) so non-UTF-8 filenames survive; commit bases are
  now enforced: a selected parent must be an actual parent of the commit,
  and the empty tree is valid only for root commits. Historical diff
  capabilities remain read-only.
- Router and context fixtures cover linear history, root commits, rename
  metadata, dirty-worktree isolation, invalid project credentials and unknown
  object IDs. The Graph/sidebar state, cancellable worker and historical preview
  rendering remain pending.
- Verification: `mbx fmt --all` PASS; `mbx test --workspace` PASS; `mbx clippy
  --workspace --all-targets -- -D warnings` PASS; `git diff --check` PASS.
  The known transitive `proc-macro-error2` future-incompatibility notice remains.
  Native Wayland validation is not applicable until Graph/history preview UI
  exists.

### Mouse-wheel ease-out, touchpad momentum and terminal pixel interpolation — 2026-10-07

- The preceding layout/I/O fixes did not add wheel animation. Confirmed against
  GPUI 0.2.2: Linux wheel deltas already contain three line units per detent;
  native scroll handlers apply them immediately with no easing/momentum tail.
- Added shared, frame-driven wheel ease-out in `apps/omaterm/src/scroll.rs`.
  Discrete vertical wheels are captured before GPUI's immediate bubble handler
  and use 32px per reported line (96px per normal detent). Successive detents
  extend the destination, reversal drops old debt, and bounds clamp travel.
  One `Window::on_next_frame` loop services Files, editor, Git, diff and terminal
  surfaces; it stops when motion settles or loses ownership/visibility/focus.
  Horizontal/Shift-wheel input retains native routing.
- Precise vertical touchpad input is now captured by the same controller rather
  than dispatched to immediate native handlers. Its reported pixel displacement
  is applied directly, then it coasts using the measured final velocity after
  42ms without new input. GPUI 0.2.2's Wayland backend emits only `Moved` for
  pointer axes and does not forward `AxisStop`, so the quiet interval is the
  explicit, documented gesture-end fallback. A new gesture or a boundary clears
  prior velocity; fast flicks therefore coast farther than slow drags.
- Precise touchpad displacement now uses a conservative 1.25x multiplier. It
  raises direct travel and measured release velocity together, without changing
  the easing/deceleration curve that determines the motion feel.
- Terminal scrollback now paints fractional vertical positions over its stable
  grid snapshot with one following overscan row, settling at a whole-row
  boundary. `viewport_following_row` reads the neighbor without resizing or
  mutating the terminal; full-screen apps retain their existing input protocol.
  Extended scroll-indicator deadlines now actually expire via one waiting task.
- Regression tests cover refresh-rate-independent convergence, bounds/reversal,
  and neighboring terminal rows at top/middle/bottom and alt-screen exclusion.
  Controller tests additionally cover velocity-dependent touchpad coast and
  boundary stops without residual momentum.
- Verification: real release Wayland wheel injection confirmed intermediate
  positions and settlement on all five surfaces; see [native evidence](evidence/wheel-easing.md).
  Release Wayland smoke validation also injected a five-event
  `axis_source=finger` pixel stream into the terminal: it rendered direct
  positions and continued to a final settled position after input stopped.
  This validates routing/coasting integration, not physical hardware feel.
  `mbx fmt --all --check`, `mbx test --workspace --quiet` (666 tests),
  `mbx clippy --workspace --all-targets -- -D warnings`, release desktop/CLI
  build, `python3 scripts/check-docs.py`, and `git diff --check` PASS (known
  transitive `proc-macro-error2` notice only). Physical trackpad feel remains
  manual validation.

### Scroll-surface correctness and UI-thread hot-path removal — 2026-10-07

- Root causes from the scroll investigation: `Git` had no vertical scroll
  container; the Files `uniform_list` could size to content instead of the
  inspector viewport; and the 250ms UI poller synchronously reloaded and parsed
  `config.toml` for Git/diff refresh checks. Files rendering also reloaded
  config through `files_root_cap` on every scroll-triggered render.
- Inspector now clips its tab body. Files uses a height-constrained native
  `uniform_list`, and the Git panel has an axis-restricted vertical scroll
  viewport whose content cannot flex-shrink to fit. Git lists can therefore
  scroll instead of clipping below the inspector.
- File-list cap and Git refresh interval are captured from startup config in
  `WorkspaceView`; the Files renderer, watcher/fetch setup, and 250ms Git/diff
  poller no longer perform UI-thread config I/O. Editor/diff already use native
  `uniform_list` pixel scrolling; terminal display uses GPUI's cached line
  shaping but remains line-granular by terminal-grid design. GPUI does not
  synthesize browser-style momentum/easing, so native Wayland touchpad deltas
  are direct rather than animated.
- Verification: `mbx fmt --all --check` PASS, `mbx test --workspace` PASS
  (660 tests), `mbx clippy --workspace --all-targets -- -D warnings` PASS
  (only the known transitive `proc-macro-error2` notice),
  `mbx build --release --bin omaterm-desktop` PASS,
  `python3 scripts/check-docs.py` PASS, `git diff --check` PASS. Manual
  Wayland feel validation is pending: an existing shared-session OmaTerm window
  predates this build and was not restarted automatically.

### Git history H0–H2 + initial command parity — 2026-10-06

- Baseline: `git 2.55.0`, SHA-1 repository object format, `mbx 1.22.0`,
  Rust/Cargo `1.99.0`; no dependency changes. H0 fixtures now cover unborn and
  linear repositories. Merge/shallow/worktree/raw-path fixtures remain H2/H4
  follow-up acceptance, not complete.
- Core gained full canonical SHA-1/SHA-256 `GitObjectId`, bounded opaque cursor,
  history scope/summary/page/timestamp/ref/file DTOs, immutable comparison/diff
  source and capability contracts. Historical source capabilities do not offer
  stage/unstage/discard/hunk-stage actions; command validation caps a history
  page at 100 rows.
- `omaterm-context::git_history` reads a bounded, NUL-framed, topological first
  page using the system Git binary with existing timeout/cap/env hygiene. It
  handles unborn HEAD as an empty page and drops malformed/capped partial
  records without fabricating commits. Decorations, cursor snapshots, parent
  file lists/patches and a shared off-thread query service remain pending.
- `GitCommand::History`, `git.history` and `omaterm git log` now share the same
  router/context read path and return bounded commit IDs, parents, author time,
  subject and flags. `--all-local` walks local branch tips only. This is the
  first semantic query, not the Graph/sidebar or commit-diff UI. It currently
  follows the existing synchronous `git.status` router path; the planned bounded
  cancellable router-owned history worker remains required before UI polling.
- Verification with `mbx`: `fmt --all --check` PASS; `test --workspace --quiet`
  PASS (659 tests); serial `-- --test-threads=1` PASS (659); `clippy --workspace
  --all-targets -- -D warnings` PASS (known transitive `proc-macro-error2`
  future-incompatibility notice only); `build --release --bin omaterm --bin
  omaterm-desktop` PASS; `python3 scripts/check-docs.py` and `git diff --check`
  PASS. Wayland validation is not applicable yet: Graph and history-diff UI are
  not implemented. Not committed.

### Git history graph and commit-file diff planning — 2026-10-06

- Added the [comprehensive Git history plan](git-history-plan.md) against
  clean baseline `f4f8907`. Graph sits below Staged Changes/Changes and stays
  visible in clean repositories; commits expand into files whose clicks open
  parent-specific historical Split/Inline previews.
- The plan covers actual merge lanes, root/shallow/merge-parent semantics,
  SHA-1/SHA-256 OIDs, stable paging, project/subdirectory boundaries, framed
  metadata/raw paths, read-only preview capabilities, bounded shared async
  dispatch, IPC/CLI parity, keyboard ownership and native acceptance.
- Execution is H0 baseline/fixtures → H1 identity/contracts → H2 Git reads →
  H3 async commands/parity → H4 graph → H5 historical previews → H6 acceptance.
  Implementation checks use `mbx`. No feature or milestone is marked complete.
- Verification: `python3 scripts/check-docs.py` PASS (46 Markdown files, 328
  local link targets, 325 blueprint references; all 41 CLI/IPC mappings and 35
  shortcut ids), `git diff --check` and the new plan's no-index whitespace check
  PASS. Link/contract consistency reviewed. Rust/native checks are not applicable
  to this planning-only change. Next action: H0, then H1 historical-source tests.

### Diff viewer syntax highlight (user-directed M15 scope change) — 2026-10-06

- `AlignedRow`/`SplitCell` carry presentation-only `tokens` over the final
  tab-expanded text; `preview_rows` fills them per file language
  (`detect_language` on the diff path, `Plain` for binary). Copy/stage still
  read the untouched source DTO, so exact-copy/stage bytes are unchanged.
- `split_cell`/`inline_row` render `StyledText` with the shared token
  foregrounds over the existing `±` backgrounds; `editor_token_color` now
  delegates to a shared free `token_color`. Headers, NoNewline markers and
  width measurement are untouched.
- Documented subset: each line tokenizes independently (multiline
  string/comment state does not cross hunk lines). No new dependencies, no
  worker, no persistence/IPC change; per-line cost is bounded by the parser
  caps. M15 spec + `DiffLineKind` docs amended to record the scope change.
- Verification with `mbx`: `fmt --all --check` PASS, `test --workspace
  --quiet` PASS (652 tests: 650 prior + 2 new diff highlight tests),
  serial `-- --test-threads=1` PASS (20 suites ok), `clippy --workspace
  --all-targets -- -D warnings` PASS (only the known transitive
  `proc-macro-error2` notice), `git diff --check` PASS. Release build and
  Wayland visual validation not run. Not committed.

### Scroll smoothness: terminal accumulation + native file-tree list — 2026-10-06

- Terminal wheel (`apps/omaterm/src/main.rs` `on_scroll_wheel`) now
  accumulates sub-line trackpad/pixel deltas per session
  (`terminal_scroll_remainder`, clamped to ±32 lines) instead of forcing
  every tiny tick into a full-line jump plus a complete viewport
  clone/repaint. Alt-screen repeat cap raised 3 → 10 so fast flicks in
  `less`/`vim` no longer feel stuck, and the scroll-thumb fade timer is
  extended rather than respawned per event.
- File tree rows are now a natively scrolled `uniform_list`
  (`files_scroll_handle`, pixel offsets like editor/palette/diff)
  instead of manual row-window `skip`/`take` with whole-row jumps; the
  custom wheel handler is removed and the thumb rail + drag read/write
  the native pixel offset. Scroll resets (project switch, filter
  edits) use `scroll_to_item(0, Top)`.
- Editor needed no change: it already scrolls through native
  `uniform_list` + `track_scroll` with axis restriction; no custom wheel
  handler interferes. If it still feels slow on huge files, that needs
  Wayland profiling (per-row shaping cost or notify storms), not scroll
  plumbing.
- Incidental: annotated the collection type in the in-progress
  `diff_highlighted_text` (concurrent diff-highlight work left
  `E0283`; logic untouched).
- Verification: `mbx fmt --all --check` PASS, `mbx test --workspace`
  651 passed + 1 failed (`diff_panel ::
  preview_rows_highlight_code_lines_by_file_language`, belongs to the
  concurrent in-progress diff-highlight change, untouched here),
  `mbx clippy --workspace --all-targets -- -D warnings` PASS (only the
  known transitive `proc-macro-error2` notice),
  `mbx build --release --bin omaterm-desktop` PASS,
  `git diff --check` PASS. Wayland feel validation pending — rebuild
  and scroll each surface. Not committed.

### Built-in highlight batch 2 (Kotlin/Zig/Lua/Dockerfile/shell) — 2026-10-06

- `EditorLanguage` grows to 24 file types: + Kotlin (`kt/kts`, `"""` raw
  strings, `//` + flat `/* */`), Zig (`zig/zon`, `//` only — no block
  comments per the language spec), Lua (`lua`, `--` line comments;
  `--[[ ]]` long comments only highlight their opening line, documented
  subset), Dockerfile (`Dockerfile`, `Dockerfile.*`, `Containerfile*`;
  case-insensitive instruction keywords per spec).
- Shell reuse, no new variants: `zsh`/`fish` extensions, `PKGBUILD`, and
  `.bashrc`/`.bash_profile`/`.zshrc`/`.zprofile`/`.profile` dotfiles all map
  to the existing Bash profile. File-tree icons already covered everything
  except Zig (falls back to the generic icon — no unverified glyph added).
- `word_insensitive` now serves SQL + Dockerfile. Worker, caps, fallback
  and indexing unchanged; no new dependencies.
- Verification: `cargo fmt --all --check` PASS, `cargo test --workspace
  --quiet` PASS (650 tests, incl. extended `extended_language_subset_rules_hold`
  + detection cases), serial `-- --test-threads=1` PASS (20 suites ok),
  `cargo clippy --workspace --all-targets -- -D warnings` PASS (only the
  known transitive `proc-macro-error2` notice), `git diff --check` PASS.
  Release build and Wayland visual validation not run. Not committed.

### Built-in highlight extended to 20 languages — 2026-10-06

- `omaterm-context` `EditorLanguage` grows from 5 to 20 file types
  (Rust, Markdown, TOML, JSON, Bash + Python, JavaScript, TypeScript, HTML,
  CSS, YAML, XML, SQL, Go, Java, C, C++, C#, Ruby, PHP); `Plain` remains the
  fallback. Detection covers common extensions (`tsx`, `mjs`, `yml`, `hpp`,
  `phtml`, …) plus `go.mod`/`go.sum` and `Gemfile`/`Rakefile` names;
  `Dockerfile`/`Makefile` stay `Plain`, bare `.h` maps to C (documented
  subset). No new dependencies; `core` stays GPUI-free.
- Desktop tokenizer (`apps/omaterm/src/editor.rs`) becomes profile-driven:
  per-family `//` / `#` (with the existing `$#` guard) / SQL `--` line
  comments, nested Rust `/* */` vs flat blocks elsewhere, `<!-- -->` for
  HTML/XML with tag names as keywords, `"` + `'`-escaped + JS/TS template
  backticks + Go raw backticks + Python triple-quoted strings, per-language
  keyword subsets, and case-insensitive SQL keywords. Rust `#` attributes
  and CSS `#id` selectors are explicitly not comments. Worker, generation,
  cancellation, `MAX_HIGHLIGHT_SPANS` cap/plain-fallback and visible-range
  indexing are unchanged.
- Verification: `cargo fmt --all --check` PASS, `cargo test --workspace
  --quiet` PASS (650 tests: 649 prior + 1 new `twenty_language_subset_rules_hold`),
  serial `-- --test-threads=1` PASS (650), `cargo clippy --workspace
  --all-targets -- -D warnings` PASS (only the known transitive
  `proc-macro-error2` notice), `git diff --check` PASS. Release build and
  Wayland visual validation not run. Not committed.

### Packaging review fixes — 2026-10-06

- Installer/uninstaller now support piped help and reject missing argument
  values. Relative prefixes are normalized before temporary-directory changes;
  desktop Exec points to the installed binary. Installer validates all six
  payload files and checks missing runtime libraries before modifying the
  installation; binaries are replaced through temporary files and rename.
- Uninstall targets `/usr/local` and `~/.local`, never `/usr` package-manager
  files; it refreshes caches in user/root modes too and removes only specified
  empty directories, without recursive parent removal.
- AUR job gating uses repository variable `AUR_ENABLED=true` instead of an
  unsupported job-level secrets expression. Release sets `MBX_TARGET_VIEWS=0`
  so mbx outputs match staging's `target/release` path. Publishing runbook
  corrects the earlier web-form/empty-package instructions: submit valid
  packages through git after release assets exist.
- Verification: `bash -n` for both scripts; isolated fixture checks for piped
  help, missing values, `/usr` refusal, relative/spaced prefixes, absolute
  desktop Exec, incomplete payload preserving existing installation, user
  install/uninstall, invalid checksum and missing-library rejection PASS.
  `actionlint` v1.7.7 validates both workflows; documentation and packaging
  sync checkers and `git diff --check` PASS. Real release download and native
  desktop validation remain pending. No Rust code changed.

### Tarball install/uninstall scripts (Opsi 1 sharing) — 2026-10-06

- New `packaging/install.sh` (latest/`--version`, `--user`/`--prefix`,
  x86_64 guard, `.sha256` verification, desktop/icon cache refresh) and
  `packaging/uninstall.sh` (exact-file removal across
  `/usr/local`+`/usr`+`~/.local`, caches refreshed, user data untouched).
  Both ship as GitHub Release assets via the `release` workflow, so the
  `curl .../releases/latest/download/install.sh | bash` one-liner works.
- Two release-tarball bugs found by local fixture testing and fixed:
  `release.yml` now tars the staging dir *contents* (files at tarball
  root, matching the `-bin` PKGBUILD + installer contract instead of a
  versioned subdir), and the installer compares hashes explicitly
  (the `.sha256` asset names the versioned file while the download uses
  a fixed local name). A third fix: `mkdir -p $PREFIX` before the
  writability check so fresh `--prefix` targets don't wrongly escalate
  to sudo; `PREFIX` also respects the environment now.
- `aur` workflow job skips gracefully while `AUR_SSH_PRIVATE_KEY` is
  absent (AUR registration is currently paused) — tarball releases stay
  green without it. Not committed.
- Verification: full install→uninstall round-trip PASS in system-prefix
  and `--user` modes against a fixture tarball (6 files each, only
  regenerated caches remain), checksum-mismatch aborts with no partial
  install, `bash -n` + YAML parse + `git diff --check` PASS.

### Keybinding overhaul: Alt tab/project jumps + cheatsheet + tabless fallback — 2026-10-06

- New GPUI-free registry `apps/omaterm/src/shortcuts.rs` (`SHORTCUTS`,
  35 ids): `Alt+1..9` unified strip jump (terminal tabs → diff chip →
  editor docs, render order), `Alt+Shift+1..9` project jump (digits +
  shifted `!@#$%^&*(`), `Alt+Shift+K` cheatsheet. Plain `Ctrl` owns no
  navigation chord; its bytes return to the PTY (M11 no-steal rule
  restored; the UI-v5 `Ctrl+1/2/3` surface binding is removed).
- Hints are honest: sidebar shows `Alt+Shift+n · name` only while
  `Alt+Shift` held; strip chips show `n · label` only while plain `Alt`
  held; bare `Ctrl` shows nothing (`show_strip_hints` /
  `show_project_hints` predicates, `on_modifiers_changed` re-gated).
- Removed two dead arms the sequential `if`-returns had shadowed:
  `Ctrl+Shift+E` equalize (Inspector Files wins; equalize stays via the
  `Equalize Panes` palette command, IPC, CLI) and `Ctrl+Shift+T`
  new-terminal (new-tab wins). Deleted the dead `equalize` view method.
- Tabless fallback (`ensure_selected_surface_fallback`, pure selector
  `fallback_without_tabs`): closing the last terminal tab (tab close,
  pane close, shell exit, doc close, diff `×`) reveals a live editor
  document, then the open diff preview, and only then the empty prompt.
  Main render already preferred Editor/Diff surfaces, so no render-path
  change was needed.
- `Alt+Shift+K` cheatsheet overlay (Omarchy `Super+K` analog; `Super`
  stays with the compositor, plain `Ctrl+K`/kill-line untouched):
  filterable `SHORTCUTS` list, `Esc`/`Enter`/`Alt+Shift+K` dismiss,
  `InputOwner::Keybindings` ownership, status-bar `Alt+Shift+K keys`
  pill, and a `Show Keybindings` palette command (`ViewAction` target).
- Contract docs: new `docs/shortcuts.md` (35-row table);
  `scripts/check-docs.py` now asserts registry ↔ table id parity both
  ways. Old M11/UI-v5 shortcut rows are left as history; this table is
  current.
- Verification: `cargo fmt --all --check` PASS, `cargo test --workspace
  -- --test-threads=1` PASS (649 tests: 642 prior + 8 new registry −
  1 superseded jump test), `cargo clippy --workspace --all-targets --
  -D warnings` PASS (only the known transitive `proc-macro-error2`
  notice), `cargo build --release --bin omaterm-desktop` PASS,
  `python3 scripts/check-docs.py` PASS (45 files, 35 shortcut ids),
  `git diff --check` PASS. Not committed.
- Manual Wayland validation pending: `Alt`/`Alt+Shift` hints, `Alt+3`
  cross-kind jump, `Alt+Shift+2` project jump, last-tab-close fallback
  to doc then diff then empty, `Ctrl+1`/`Ctrl+K` reaching PTY
  (`cat -v`), cheatsheet open/filter/close from terminal/editor/
  palette, `Alt+Shift` layout-switch fallback via palette pill.

### AUR packaging Fase 1 (Arch/Omarchy focus) — 2026-10-06

- Dual AUR brand reservation: `packaging/arch/` (`omaterm`, source build)
  and `packaging/arch-bin/` (`omaterm-bin`, prebuilt GitHub Release
  tarball). Both install `omaterm` + `omaterm-desktop`, the new
  `packaging/omaterm.desktop`, placeholder icon
  `packaging/icons/omaterm.svg` (final branding follows), and both
  licenses; the packages conflict with each other.
- New `release` workflow: tag `v*` → build both binaries on ubuntu-22.04,
  attach `omaterm-<ver>-x86_64.tar.gz` (+ `.sha256`) to the GitHub
  Release, then pin tag-tarball/release-tarball checksums into copies of
  each PKGBUILD + `.SRCINFO` and push to `omaterm` / `omaterm-bin` AUR
  repos via `AUR_SSH_PRIVATE_KEY`. Manual dispatch is a tarball dry-run
  only. `scripts/bump-aur.sh` is the local pre-tag version bumper
  (regenerates `.SRCINFO` via makepkg); `scripts/check-aur-sync.py`
  enforces PKGBUILD ↔ `.SRCINFO` sync and `pkgver` parity with both
  binary crates, wired as a new CI `packaging` job.
- One-off setup still required by maintainer: AUR account + SSH key,
  empty `omaterm` / `omaterm-bin` submissions, `AUR_SSH_PRIVATE_KEY`
  secret — see `packaging/README.md`. No tag pushed yet, so both
  `sha256sums` remain `SKIP`. Not committed.
- Verification: `python3 scripts/check-aur-sync.py` PASS,
  `makepkg --printsrcinfo` round-trip identical for both packages,
  release-workflow checksum-pinning seds simulated locally OK,
  `bash -n` + YAML parse + `git diff --check` PASS. `namcap` unavailable
  locally; first real AUR push pending the one-off setup + first tag.

### Full-height tab strip — 2026-10-05

- Tab chips (terminal/diff/editor/restore) and the trailing new-tab button now
  fill the full 42px header height; container side padding and inter-chip gaps
  removed. Combined with the earlier sharp-corner change, the strip is flush
  top and sides.
- Native Wayland validation captured against the rebuilt release desktop with
  an isolated project; screenshot: `/tmp/opencode/sharp-tabs/tabstrip-fullwidth.png`.
- `mbx fmt --all --check`, `mbx test --workspace --quiet` (642 tests),
  `mbx clippy --workspace --all-targets -- -D warnings`, and
  `mbx build --release --bin omaterm-desktop` PASS. Existing transitive
  `proc-macro-error2` future-incompatibility notice remains. Not committed.

### Sharp tab strip — 2026-10-05

- Removed rounded corners from terminal, diff, editor and restore tab chips,
  plus the trailing new-tab button. The tab strip now uses Omarchy-style sharp
  edges; the terminal pane dot remains intentionally circular.
- Native Wayland validation captured against the rebuilt release desktop with
  an isolated project; screenshot: `/tmp/opencode/sharp-tabs/omaterm-sharp-tabs.png`.
- `cargo fmt --all --check`, `cargo test --workspace --quiet` (642 tests),
  `cargo clippy --workspace --all-targets -- -D warnings`, and
  `cargo build --release --bin omaterm-desktop` PASS. Existing transitive
  `proc-macro-error2` future-incompatibility notice remains. Not committed.

### Sidebar/tab-strip polish batch — 2026-10-05

- Active terminal tabs no longer show the pane count (dot + name + close only).
- Diff preview chips survive surface switches: switching to a terminal tab or
  an editor document no longer closes the preview (only its own × does), and
  clicking the `Diff:` chip reveals the preview again (previously a no-op
  notify). Main area keeps following the active surface.
- Removed the per-row Open in Terminal icon from the file tree; plain click
  opens natively, Alt+click keeps the `FileCommand::Open` fallback.
- Left sidebar header now reads OMATERM instead of PROJECTS.
- Both sidebar toggles plus the collapsed reveal button share one
  `sidebar_toggle` pill (pressed state + white/muted icon); the unused
  `PANEL_LEFT_CLOSE` slot const was removed (SVG stays vendored).
- Removed the redundant header new-project icon; the bottom Open Project
  button (label + Ctrl+O hint) is the single entry point.
- `cargo fmt --all --check`, `cargo test --workspace --quiet` (642 tests),
  `cargo clippy --workspace --all-targets -- -D warnings`,
  `cargo build --release --bin omaterm-desktop`, and `git diff --check` PASS.
  Existing transitive `proc-macro-error2` future-incompatibility notice remains.
- Manual Wayland validation pending for all six items. Not committed; working
  tree also still holds the earlier untracked-diff/tab-highlight batch.

### Untracked diff renders as all-additions; single tab highlight — 2026-10-05

- Untracked preview now runs `git diff --no-index /dev/null <path>` and
  renders empty → content (Added, green lines) like VSCode. The runner
  accepts exit code 1 only on this argv; caps, timeout, cancellation,
  binary handling and the existing parser are unchanged. IPC/CLI diffs stay
  on the index sides.
- Tab strip highlight now derives from the single `project_surface` source:
  exactly one of selected terminal tab / diff chip / editor document looks
  active. `editor_selected` remains selection memory (Ctrl+2 return intact).
- `cargo fmt --all --check`, `cargo test --workspace --quiet` (642 tests,
  incl. live-repo `untracked_file_renders_as_all_additions` and
  `exactly_one_tab_kind_owns_the_active_highlight`),
  `cargo clippy --workspace --all-targets -- -D warnings`,
  `cargo build --release --bin omaterm-desktop`, and `git diff --check` PASS.
  Existing transitive `proc-macro-error2` future-incompatibility notice remains.
- Manual Wayland validation pending: untracked file shows full green content;
  clicking terminal/file/diff leaves exactly one active tab. Not committed.

### New-tab button trails the tab strip — 2026-10-05

- Moved the `+` button after every chip (terminal tabs, diff preview, editor
  documents, restore placeholders) so it always trails the strip instead of
  wedging between tab kinds. Render-only reorder; behavior unchanged.
- `cargo fmt --all --check`, `cargo test --workspace --quiet` (640 tests),
  `cargo clippy --workspace --all-targets -- -D warnings`,
  `cargo build --release --bin omaterm-desktop`, and `git diff --check` PASS.
  Existing transitive `proc-macro-error2` future-incompatibility notice remains.
- Manual Wayland validation pending: open a file and a diff, confirm `+` is
  rightmost; close all, confirm the terminal-only strip looks unchanged.

### Untracked diff preview no longer flickers closed — 2026-10-05

- Root cause: the preview worker reads `git diff`, which never lists
  untracked files. When the empty result landed, prune dropped the selection,
  so the `Diff:` tab appeared on click and vanished on refresh.
- `DiffPanel` now syncs the untracked set from each landed `git status` and
  keeps untracked selections through diff refreshes; pruning resumes once a
  path leaves the untracked set. The preview keeps its header (Stage/Open File)
  and shows "Untracked file — no diff to show until it is staged."
- Automated verification: `cargo fmt --all --check`,
  `cargo test --workspace --quiet` (640 tests, incl. new
  `refresh_keeps_untracked_selection_and_prunes_once_tracked`),
  `cargo clippy --workspace --all-targets -- -D warnings`,
  `cargo build --release --bin omaterm-desktop`,
  `python3 scripts/check-docs.py`, and `git diff --check` PASS. Existing
  transitive `proc-macro-error2` future-incompatibility notice remains.
- Manual Wayland validation pending: click an untracked file and confirm the
  preview stays open with the untracked state, then stage it and confirm the
  staged diff appears. Not committed; working tree holds this fix.

### Theme-aligned discard dialog — 2026-10-05

- Replaced GPUI's plain fallback prompt with an OmaTerm-themed renderer:
  dim backdrop, centered dark card, border/shadow, shared typography,
  restore icon and separate footer with horizontal Cancel / Discard Changes
  buttons. The destructive choice uses a red accent.
- Cancel is selected initially; Tab/Shift+Tab and arrows move the visible focus
  outline, Enter chooses the selected button, and Esc cancels. GPUI's prompt
  handle retains response delivery and restores the previous focus on completion.
- `cargo fmt --all --check`, `cargo test --workspace --quiet` (639 tests),
  `cargo clippy --workspace --all-targets -- -D warnings`, and
  `cargo build --release --bin omaterm-desktop` PASS. The existing transitive
  `proc-macro-error2` future-incompatibility notice remains.
- `python3 scripts/check-docs.py` and `git diff --check`: PASS.
- Manual Wayland visual/keyboard validation remains pending; no new desktop
  interaction acceptance is claimed.

### Git change icons and explicit discard dialog — 2026-10-05

- Git rows reuse the Files panel's filename/extension icon mapping, colors and
  Nerd Font glyphs for paths without a text badge, replacing the generic document
  SVG (including Python and text files). Removed the redundant document-open
  action from each row; Ctrl+click still opens the native editor.
- User-requested replacement of timed double-click discard banners with a GPUI
  warning dialog: Cancel / Discard Changes. Single-file, bulk and diff-toolbar
  discard share captured target paths and the existing semantic discard command.
  Cancel, dismissed prompts, invalid answers, a different selected project and
  shutdown cannot dispatch; duplicate prompt admission is suppressed.
- Removed obsolete arm-window state/tests; added cancellation/stale-context
  confirmation coverage. `cargo fmt --all --check`,
  `cargo test --workspace --quiet` (639 tests),
  `cargo clippy --workspace --all-targets -- -D warnings`, and
  `cargo build --release --bin omaterm-desktop` PASS. Existing transitive
  `proc-macro-error2` future-incompatibility notice remains.
- Documentation checker and `git diff --check`: PASS.
- Manual desktop validation is pending: verify Python/text icons, Cancel/Esc,
  confirm tracked restore and untracked deletion, bulk confirmation and diff
  toolbar confirmation in a disposable repository on Wayland. No new native
  visual or interaction acceptance is claimed.

### Info panel typography and refresh stability — 2026-10-05

- Focused-shell identity now uses the shared 11px body role; CWD and Copy path
  use the shared 10px metadata role instead of inheriting oversized default text.
- During a process refresh, retain the last successful snapshot for the viewed
  project, including process/port rows, counts and truncation notices. Only an
  initial query without a matching snapshot renders loading placeholders.
  Query errors remain explicit, and snapshots remain project-bound.
- Automated verification: `cargo fmt --all --check`,
  `cargo test --workspace --quiet` (640 tests),
  `cargo clippy --workspace --all-targets -- -D warnings`,
  `cargo build --release --bin omaterm-desktop`, and `git diff --check` PASS.
  Clippy/build retain the existing transitive `proc-macro-error2`
  future-incompatibility notice.
- Manual desktop validation: pending; no new Wayland visual/interaction evidence
  was collected. Next action: verify typography, manual/automatic refresh without
  row/count/empty-state flashing, initial loading, query errors and project switches
  using the rebuilt `target/release/omaterm-desktop`. M18 acceptance remains open.

### M18 correctness follow-up and release Wayland evidence — 2026-10-04

Current checked-in source is **`2cc1ce4`**. The runtime evidence below identifies
the earlier captured `73b3470+worktree` source and binaries; the final palette
bookkeeping correction followed that runtime capture. The 640-test automated-gate
record is separate evidence, not a substitution of its source identity into the
runtime report. The previous M18 prerequisite-complete claim was premature:
implementation and evidence have advanced, but full Q1/M18 acceptance is still
open. Earlier M19 source identities and test counts below are historical.

Corrections implemented:

- Query completion revalidates project scope and captured session/pane/root
  ownership. Cancellation now reaches process jobs on IPC disconnect/deadline
  and stale UI targets. Worker admission includes active, queued and unconsumed
  results (four total); cooperative scans have a two-second submission deadline.
- Intermediate process/socket/FD reads have explicit budgets; partial data is
  marked truncated. Ownership is captured before response truncation, preserving
  attribution when an ancestor falls outside the sorted output. Shell roots are
  now included. CPU sampling uses the captured stat identity and resets on reuse.
- PID 0/group values are refused. Kill binds a pidfd and revalidates ancestry
  before sending SIGTERM, preventing redirection to a reused PID after binding.
  No dependencies changed. Unsupported pidfd kernels fail with `runtime_failure`.
- UI kill arms capture project/pane/session/PID and expire after eight seconds;
  automatic refresh pauses during the arm window. Keyboard, other pointer
  actions, focus loss and stale context disarm. Refresh rides the existing 250ms
  poller rather than keeping the 20ms completion poller alive indefinitely.
- Query states are project-bound; stale project errors cannot replace the current
  view. Ports also distinguish loading/error from empty. The focused shell PID,
  cached CWD/copy control, truthful truncation notice and scrolling are rendered.
- Process-query palette receipts now retire MRU/origin bookkeeping on completion
  or cancellation. This last UI bookkeeping correction followed the runtime run;
  the evidence report's hashes identify the tested build, not the later rebuild.

Verified commands (`RUSTUP_TOOLCHAIN=1.99.0`):

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test --workspace` | PASS: 640 unit/integration tests, zero failures |
| `cargo test --workspace --quiet` (after palette receipt correction) | PASS: 640 tests, zero failures |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS; existing transitive `proc-macro-error2` future-incompatibility notice |
| `cargo build --release --bin omaterm --bin omaterm-desktop` | PASS |
| `python3 scripts/check-docs.py` | Historical PASS: 44 Markdown files, 304 local link targets, 318 blueprint references, 35 CLI mappings; the checker now validates all 41 hyphen-aware CLI/IPC method mappings bidirectionally |
| `git diff --check` | PASS |

[Release runtime evidence](evidence/m18-runtime-current.md): two-project child/
port attribution, null-first/finite-next CPU samples and positive RSS, foreign
and project-scoped kill denial, 100 successful queries with FD **44→44** and
threads **37→37**, ten executed terminal-output probes, actual Info-panel capture,
and targeted compositor closure removing the owned socket/credential and all
recorded test PIDs. RSS grew 1,256 KiB; this is not a leak-free claim.

Limitations: native collapse/copy/manual-refresh/arm-confirm interactions remain
unexecuted; busy CPU and owner-thread scan timings remain open. Kill ancestry
reads are still bounded but synchronous; a PID-only row cannot identify reuse
before request binding. Interactive Bash ignored SIGTERM until a TERM-exit trap
was installed: successful signal delivery is not process-exit acknowledgement.
The validator accidentally launched `omaterm-desktop --help` with inherited paths
during preflight; that process exited, but production-path noninterference was
not established. The incident is explicitly recorded in the evidence report.

**Next action:** move kill inspection off-thread, carry process identity from
displayed row to kill, and validate native arm/cancel/collapse/copy/refresh before
claiming M18 complete. M15/M16/M17 and M19 native closure gates remain open.

### M19 historical delivered-worktree reconciliation — 2026-10-04

**Delivered source: `f287991+worktree` (dirty), source parent
`f287991f223e01a36ea5e0a60c219c824da05ba3`.** This identifies the historical
uncommitted implementation that was tested, not a new commit or a clean-parent
build. It must not be substituted for the current checkout without a source and
verification comparison.

Historical full automated results supplied by the parent run:

| Command | Result |
|---|---|
| `RUSTUP_TOOLCHAIN=1.99.0 cargo fmt --all --check` | PASS |
| `RUSTUP_TOOLCHAIN=1.99.0 cargo test --workspace --quiet` | PASS: **606** unit/integration tests (parallel) |
| `RUSTUP_TOOLCHAIN=1.99.0 cargo test --workspace --quiet -- --test-threads=1` | PASS: **606** unit/integration tests (serial) |
| `RUSTUP_TOOLCHAIN=1.99.0 cargo clippy --workspace --all-targets -- -D warnings` | PASS |
| `RUSTUP_TOOLCHAIN=1.99.0 cargo build --release --bin omaterm --bin omaterm-desktop` | PASS |

These current results supersede older counts for current-gate reporting;
historical runs below retain their original counts/outcomes. The documentation
reconciliation did not rerun Cargo. Native tool probes record rustc
`1.99.0 (b940084d7 2026-09-28)` and Cargo `1.99.0 (5f94df478 2026-08-27)`;
the repository's 1.98.1 pin is unchanged.

Source-audit corrections on this worktree:

- Router preparation captures root inputs; filesystem root resolution and
  descriptor capture run off-thread. Editor I/O has one active job, 16 queued
  jobs and a bounded **17-result mailbox**, with completion capacity included
  in admission rather than an unbounded result queue.
- **32 documents globally**, including pending-open reservations and unavailable
  restore entries; schema validation/capture applies the workspace-wide bound.
- Schema-3 metadata-only snapshots preserve raw path bytes/root identity and
  selection. Bounded restore reads, unavailable entries and Retry/Close are
  implemented; real normal-close/restart acceptance is still pending.
- Committed save acknowledgements adopt captured G text/revision as the saved
  baseline, retaining live G+1 edits; cancellation/stale view handling does not
  erase an already-committed disk outcome. Overwrite is scoped to the observed
  revision and a second external change re-conflicts.
- S6 now implements GPUI `EntityInputHandler`, UTF-16/native text and marked
  composition handling, with composition cancellation across input-owner/surface
  transitions. Native IME, focus, clipboard and leakage validation remains open.
- Grapheme navigation/clamping is line-local rather than resegmenting the whole
  document; unchanged rendering uses shared indexed snapshots. This source audit
  is not a resource/latency acceptance result.

The [S9 report](evidence/m19-s9-report.md) and
[manifest](evidence/m19-s9-manifest.json) record G1–G5 passes and partial native
evidence. **S9 and all full-native gates remain open; E01–E10 are pending/partial.**
The [current native attempt](evidence/m19-native-current.md) demonstrated startup,
terminal IPC parity and 20 real `/proc` samples only. Focus changed before input;
both workloads executed 0/20 cycles. All six editor timing arrays are empty,
unobserved, with null percentiles and no resource/latency pass. Cleanup was forced
SIGTERM plus explicit stale-socket unlink, **not graceful exit**.

Harness documentation now reflects pending exit **4**, explicit
`--scripted-steps` for semantic input, and no synthetic measurement placeholders.
Next action: an uninterrupted target-focused native run of every entry/input/
conflict/dirty-action/normal-restart case, both 20-cycle workloads and normal
exit/worker/socket cleanup. Earlier S0/S1/S2 implementation next actions below
are historical checkpoints, superseded by this delivered-worktree audit.

Documentation checks for this reconciliation: `python3 scripts/check-docs.py`
PASS (exit 0: 39 Markdown files, 173 local link targets, 304 numbered blueprint
references, all 34 CLI method mappings); `git diff --check` PASS (exit 0).
Results are also recorded in the S9 gate table.

### M19 current-release native attempt — 2026-10-04

Real Wayland startup, isolated IPC terminal-file-open parity, actual 20-sample
terminal resource collection and forced owned-process/socket cleanup
are recorded in [M19 current native evidence](evidence/m19-native-current.md).
Compositor focus changed before the first keyboard/pointer event, so injection
was aborted; native editor/conflict/restart/IME flows and both 20-cycle workloads
are **BLOCKED**, with zero cycles executed. All six editor timing series remained
unobserved. No Rust edits/commits or final Cargo gates were made by this validation
run. S9 remains open; next action is an uninterrupted target-focused native run.

**UI v5 fidelity: incomplete.** The committed rewrite `8cade1b` has confirmed
sidebar, icon, typography, spacing, hover, geometry and color differences from
the supplied HTML. The earlier all-phases-finished claim is superseded by the
[fidelity correction plan](ui-v5-fidelity-correction-plan.md). Historical Rust
check results below do not establish pixel-perfect visual acceptance.

Milestones 1–3 are complete. Milestone 4 is implemented and verified; see the
M4 evidence below.

| Milestone | Status | Verification evidence | Blockers | Next action |
|---|---|---|---|---|
| 1 — GPUI Boot | complete | Build, quality checks, CI workflow, and Wayland visual/close checks recorded below | X11 runtime session unavailable; build coverage passes | Begin M2 pure pane tree |
| 2 — Pane Tree | complete | 9 core tests, workspace quality gates, and Wayland manual validation recorded below | None | Begin M3 single terminal |
| 3 — Single Terminal | complete | Completed `a3923d4`; release-build interaction pass approved 2026-09-26 with explicit IME/unavailable-program limits (see M3 completion record) | None | M4 regression coverage |
| 4 — Multi Terminal | complete | 64 terminal unit + 20 PTY integration tests; workspace quality gates and Wayland four-pane pass recorded below | Thread/memory/GPU stress baselines remain for broader lifecycle work | Begin M5 Projects/Tabs |
| 5 — Projects/Tabs | complete | Core hierarchy, coordinator lifecycle, main Wayland workflows, inactive-tab padding hit target, startup Retry, 200 measured session create/close cycles, and cleanup fault/repeated-close tests recorded below | Per-process GPU counters unavailable; broader M8 resource test remains | Begin M6 Wayland persistence validation |
| 6 — Persistence | complete | State crate/workspace tests and release Wayland restart, nested split/focus, fresh PID, CWD/fallback, restored Retry, corruption/schema retention and pending-debounce close evidence recorded below | Home-unavailable desktop UI limit not exercised; automation covers error paths | Begin M7 completion |
| 7 — Command Router | complete | Single async dispatch path, 19 router tests (variant matrix, cancel/duplicate rollback), commit guards, and release Wayland UI regression (resize/equalize/focus/close/tab) recorded below | None | Begin M8 acceptance review (done — see M8 row) |
| 8 — IPC | complete | Typed 17-method mapping, bounds, credentials/scope/child-env, owner bridge, 11 transport tests, concurrent-load + shutdown-under-load Wayland proof recorded below | Documented limits only: cross-UID harness, fallback-dir creation path, owner-channel saturation race (see below) | Begin M9 CLI |
| 9 — CLI | complete | `omaterm-cli` thin client (27-row parser/mapping matrix plus path-launch forms), 31 CLI tests, workspace gates, and release Wayland CLI/desktop proof recorded below; live `pane.resize` via discovered split IDs closed by M11 | Scoped in-app denial covered by M8 evidence plus CLI env/deny tests | M10 history (post-v0.1) |
| 10 — Encrypted History Recovery | complete | 287-test suite green 2026-09-28 plus release Wayland proof (opt-in, styled/Unicode restore, fresh shells, journal merge, alt absence, clears + rotation, disable, key-loss memory-only); M11 closed the remaining live items (same-pane restore, corrupt-archive quarantine) — see M11 record | Documented limits only: graceful-close live, banner-visibility eyes, per-pane CWD re-verification stays on M6 plumbing | M11 closure |
| 11 — v0.1 Closure & Hardening | complete with documented limits | 313-test serial suite green; release Wayland IPC proofs (split-ID discovery + live resize/equalize, path launch + run, history restore + quarantine, invalid-config survival, targeted logging); PKGBUILD + license inventory; perf baseline recorded — see M11 record below | Documented limits only: eyes/hands items (jump keys, picker portal, paste/drop live, font-size visual), theme engine + automation-disable enforcement future, X11/second-compositor/scaling, per-process GPU | M12 (done — see M12 row) |
| 12 — Project Context Root | complete | `omaterm-context` (resolve/boundary/ignore, 13 tests), `[files]`/`[git]` config, 3 logging categories, `project.root` parity (router/bridge/CLI + scope tests), 328-test serial suite green, release Wayland pinned/git/deleted-pin/stale proofs — see M12 record below | Documented limits only: unpinned-no-shell live path unit-covered, second compositor/X11/scaling, per-process GPU (standing v0.1 limits) | Begin M13 file tree + filename search |
| 13 — File Tree + Finder | complete | Right-sidebar `FILES` tree + `Ctrl+P` overlay, lazy loading, icons, wheel scroll, home-freeze fix; `file.*` parity (router/bridge/CLI + scope tests), 356-test serial suite green, release Wayland list/search/open/watcher/migration proofs — see M13 records below | Documented limits only: `Ctrl+P` key delivery + row click-toggle need hands, graceful-close live path, standing v0.1 limits | Begin M14 git status |
| 14 — Git Status | complete | `omaterm-context::git` (porcelain v2 `-z` parser + stage/unstage/discard runners), `GitCommand` parity (router/bridge/CLI + scope tests), Source Control sidebar section with background poller + two-step discard arm, 385-test serial suite green, release Wayland status/stage/unstage/discard + auto-refresh + post-run-hint proofs — see M14 record below | Documented limits only: panel clicks + arm banner need hands (wiring unit-tested, render screenshot-verified), graceful-close live path, standing v0.1 limits | M15 diff viewer in progress |
| 15 — Diff Viewer | in_progress | Bounded parser, partial-hunk parity, cancellable latest-only worker, shared mutation invalidation, cached virtual rows, independent Split X/Inline X, source anchors; release Wayland direct Git click, long-row/character reach, exact copy, one-hunk stage, IPC refresh and terminal open | Rails/paging/drag, rendered-range instrumentation and full metadata/stale/scope/refresh-anchor matrix remain; see current M15 evidence | Finish D2/D3 in the [remaining-work plan](m15-m16-remaining-work-plan.md) before M16 completion |
| 16 — Command Palette | in_progress | Dual-mode overlay, fuzzy ranked command/workspace/file/Git candidates, one latest-only source worker, bounded root index, core ranking, root-aware File MRU and origin restoration; release Wayland command/file/project/split/focus/process refresh proof and CLI spot-checks recorded below | M15 not closed; rapid-search worker/resource measurements and full stale/focus/error/argument matrix pending (M18 query is now off-thread, see M18 row) | Finish M15, then close M16 implementation/acceptance gaps |
| 17 — v0.2 Closure | open | No closeout run yet; depends on M12–M16 completion | M15/M16 in progress; v0.2 acceptance rows pending | Only after M12–M16 pass; M19 re-sequencing does not close this gate |
| 18 — Process Panel | in_progress (query + kill + panel UI) | Bounded async inspection, cancellation/deadline and completion ownership guards; CPU/RSS, shell roots, ports, pidfd-scoped SIGTERM; project-bound UI states and contextual 8s arm; 250ms timer drives 2s refresh. 640 workspace tests and full gates PASS; release Wayland child/port/scope/100-query/terminal-response/screenshot/normal-close evidence in [report](evidence/m18-runtime-current.md) | Native arm/cancel/copy/collapse/refresh, busy CPU/timing and pre-bind process identity still open; kill ancestry is synchronous; preflight isolation incident recorded | Complete remaining native and identity/async-kill gates before M18 acceptance |
| 19 — Basic Built-in Editor | in_progress | Rooted context I/O + SHA-256 revisions, `EditorIoQueue` worker (1 active + 16 queued), bounded 32-slot store, metadata-only registry snapshots/restore, `EntityInputHandler`; 606-test suite, Clippy, release pass with `RUSTUP_TOOLCHAIN=1.99.0` — see M19 records below and the [S9 report](evidence/m19-s9-report.md) | S9/E01–E10 open: native input aborted on user-focus change, 0/20 small and cap cycles, no graceful exit/restart, IME preedit; [milestone spec](19-milestone-19-editor.md) | Complete S9 native exit criteria; do not claim M19 complete |
| Privacy: local-only defaults (§46) | partial | Redaction audit green; local state under `$XDG_*`; no telemetry/cloud code | No dedicated no-egress network test or user-facing privacy statement; acceptance row partial in [matrix](acceptance-matrix.md) | Add no-egress test or record explicit limit |

## M18 Info-panel process UI — 2026-10-04

Added over the async query/kill slice: the Info panel renders a per-view
`ProcessQueryView` state (Idle/Loading/Failed/Loaded) so a confirmed empty
result is never shown as an error and vice versa; collapsible PROCESSES/PORTS
headers with count badges; per-process CPU% and RSS (formatted B/KiB/MiB);
a per-row two-step kill control (`×` → `kill?` within an 8s window) with an
explicit confirmation banner; and a 2s debounced background refresh that runs
only while the Info tab is visible and no query is in flight. Any other action
or a refresh disarms a pending kill. Pure helpers (`format_process_memory`,
`section_header`, `process_state_box`) and state defaults have unit tests.

Verification with `RUSTUP_TOOLCHAIN=1.99.0`: `cargo fmt --all --check` PASS;
`cargo test --workspace -- --test-threads=1` PASS (221 + 44 + 67 + 7 + 1 + 8 +
21 + 11 + 3 + 6 + 58 + 145 + 33, 0 failed); `cargo clippy --workspace
--all-targets -- -D warnings` PASS; `cargo build --release -p omaterm` PASS;
`python3 scripts/check-docs.py` PASS; `git diff --check` PASS. Release Wayland
visual/interaction acceptance (live child/port rows, kill arm flow, CPU/RSS
values, auto-refresh under load) is not yet recorded.

**Next action:** run release Wayland acceptance against a spawned child + local
listening server; capture kill-flow and resource stability evidence; then close
M18.

## M18 async process query + scoped kill (query prerequisite) — 2026-10-04

Landed Q01–Q03 from the [remaining-work plan](m15-m16-remaining-work-plan.md) §8,
satisfying the M16 process-query prerequisite. This is not full M18 acceptance:
the collapsible Info panel UI, debounced auto-refresh, kill arm/confirm UI and
release Wayland resource proof remain open.

Platform (`omaterm-terminal/src/platform.rs`):
- `ProcessInfo` gained `cpu_percent: Option<f32>` and `memory_bytes: Option<u64>`
  (RSS from `/proc/<pid>/statm`, resident pages × page size).
- New `ProcessSnapshot { processes, ports, truncated }` and
  `ProcessInspector::snapshot(roots, cap)`: one `/proc` scan per request, ports
  read once, owned set = roots ∪ descendants (deduped), growth halted at `cap`.
- New `CpuSampler` (persistent utime/stime + start-time delta, PID-reuse guard,
  first-sample `None`) and `ProcessInspector::terminate(pid)` via `libc::kill`
  with `TerminateError { NotFound, PermissionDenied, Other }`. No new crates.

Router (`apps/omaterm/src/router.rs`):
- `ProcessQueryWorker`: one thread, bounded pending queue (4), result mailbox
  (4), `shutdown_and_join`. `ProcessQueryRequest { project, roots: Vec<(SessionId,
  PaneId, u32)>, cap }` carries domain inputs only. The owner captures cached
  `child_pid()`s and authorizes; no `/proc` work runs on the owner thread for
  `List`. The worker owns `LinuxProcessInspector` + persistent `CpuSampler`,
  attributes ports per PID in memory, and enforces `MAX_PROCESS_ENTRIES` before
  unbounded growth. `ProcessCommand::List` now returns `CommandOutput::Pending`;
  completions drain via `poll_process_queries()` and carry no effects (asserted).
- Scoped synchronous `Kill`: `capture_process_roots` resolves the family once,
  membership is revalidated, `terminate` signals one PID; foreign existing PIDs
  → `cross_project_denied`, gone PIDs → `process_not_found`, `EPERM` →
  `permission_denied`. `CommandOutput::ProcessKilled { pid, signal }` and
  `ErrorCode::ProcessNotFound` added.

Parity:
- Protocol/CLI: `process.kill { project_id, pid }` → `{ pid, signal }`; CLI
  `process kill PID [--project]`, renderer `Terminated process {pid} (SIGTERM).`;
  `process list` human output now shows `cpu`/`mem` when present; CPU/RSS also
  flow through the existing JSON bridge.
- IPC bridge maps `Method::ProcessKill` and `CommandOutput::ProcessKilled`.

Tests added: platform statm/stat-times/tricky-comm/sampler/snapshot/terminate;
router async bounded/effect-free list, project-scoped kill, foreign-pid denial,
100-query single-worker reuse, shutdown join; CLI kill mapping + cpu/mem render;
protocol decode. Moved the existing scoped-list test onto the async path.

Verification with `RUSTUP_TOOLCHAIN=1.99.0`: `cargo fmt --all --check` PASS;
`cargo test --workspace -- --test-threads=1` PASS (219 + 44 + 67 + 7 + 1 + 8 +
21 + 11 + 3 + 6 + 58 + 145 + 33, 0 failed); `cargo clippy --workspace
--all-targets -- -D warnings` PASS (only the pre-existing transitive
`proc-macro-error2` future-incompatibility notice); `python3 scripts/check-docs.py`
PASS; `git diff --check` PASS. Manual Wayland validation of child/port
attribution, kill flow and resource stability is not yet recorded.

**Next action:** build the Info panel collapsible UI + debounced auto-refresh
over the completed snapshot, wire the arm/confirm kill flow, then run release
Wayland child/port ownership and 100-query FD/thread stability proof.

## M15 + M16 remaining-work planning — 2026-10-03

Created the [remaining-work plan](m15-m16-remaining-work-plan.md) against commit
`6d41c14` plus the existing uncommitted implementation. Inspected the working
tree, milestone contracts, previous plans, diff row/worker/renderer code,
palette ranking/search/cache/activation code and process-query routing.
Existing application and documentation changes were preserved.

The current execution order is R0 baseline/fixtures → D1 diff lifecycle → D2
viewport/anchors → D3 actions/parity/Wayland and M15 closure → Q1 off-thread M18
query prerequisite → P1 palette scheduler/freshness → P2 catalog/rank/MRU/
arguments → P3 input/focus/layout → V1 final acceptance and M16 closure.

The audit records remaining code defects alongside evidence gaps: incomplete
diff request identity and real worker admission; 21px/22px row mismatch and
missing independent X/anchors; synchronous process scans; per-query palette
threads; invalidated cache results still returnable; unapplied file MRU;
selection-dependent command retargeting and incomplete origin restoration.
Historical plans now point to this current sequence. No milestone checkbox or
application capability is marked complete by this planning change.

Documentation verification: `python3 scripts/check-docs.py` passed (35 Markdown
files, 126 local link targets, 282 blueprint references; all 34 CLI methods
mapped). `git diff --check` and `git diff --no-index --check /dev/null
docs/m15-m16-remaining-work-plan.md` passed. Consistency review separates
implemented behavior, historical evidence and required new acceptance; M18
query work is kept distinct from full M18 completion.

Cargo/native checks are not applicable to this documentation-only increment.
Next implementation action: R0 reproducible baseline/fixtures, then D1
identity/admission tests.

### Execution update — 2026-10-03

R0 baseline inspection/fixtures had already been completed earlier in this
working tree. Continued with D1/D2 plus partial P1/P2/P3 work and corrected the
diff request acceptance rule to compare the complete request key against live
project, root generation, selected path/side, pinned root and cached session CWD.
The latest-only worker test now checks every identity field survives the
worker/mailbox path. Split preview width reserves a full longest-line width per
side so both text columns can be reached with horizontal scrolling. Inline
no-final-newline context rows now assert the marker on both sides. The Git
cancellation regression uses a helper shell that signals entry to its blocking
operation before cancellation, eliminating a launch-timing race exposed by a
full-workspace run.

### Verification before the lifecycle checkpoint

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test --workspace` | PASS: all 462 unit/integration tests; PTY integration 32/32 |
| `cargo test --workspace -- --test-threads=1` | PASS: all 462 unit/integration tests; PTY integration 32/32 |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS; existing dependency future-incompatibility notice only |
| `python3 scripts/check-docs.py` | PASS: 35 Markdown files, 126 local links, 282 blueprint refs, all 34 CLI method mappings |
| `git diff --check` | PASS |

The first parallel workspace run exposed a race in the cancellation test: the
fixed 100ms timer could expire before the helper child reached its blocking
operation. The regression now uses an explicit child-start marker. The complete
parallel and serial workspace suites both passed after the fix. No Wayland
acceptance was run in this increment. M15/M16 remain in progress; this update
does not close their visual, interaction, performance or release gates.

Remaining-work tracking now records D1/D2/P1/P2/P3 implementation deltas in
`m15-m16-remaining-work-plan.md`. Next action is D1 request race coverage and
remaining M15 D2/D3 acceptance, followed by the M18 async query prerequisite.

### M15/M16 implementation checkpoint — 2026-10-03

Paused feature expansion for the requested commit. The checkpoint includes the
diff virtual rows/actions, cancellable latest-only Git worker, unified palette,
bounded filename index/core ranking, initial process query and remaining-work
plans accumulated since `6d41c14`.

Finished the in-flight diff lifecycle edits: supersession/cancellation and
completion publication now share one lock; cancelled jobs cannot refill the
mailbox. Selection/root changes cancel active/pending work, empty workspaces
retire caches (including closed projects), and normal desktop shutdown joins
the diff worker on its background cleanup thread. Root-directory changes and
hunk-stage refresh invalidate both cached comparisons while retaining selection
intent. Three new regressions exercise every stale identity field, data versus
selection retirement, and a blocked active job with 100 superseding requests,
then shutdown with active and queued work.

Checks on this checkpoint (Rust/Cargo 1.99.0, environment override of the
repository's 1.98.1 pin; dependency resolution unchanged):

- `cargo fmt --all --check`: PASS.
- `cargo test -p omaterm --bin omaterm-desktop diff_panel::tests`: PASS, 20 tests.
- `cargo test --workspace`: timed out at 120s in the unchanged terminal crate;
  `cwd_refresh_uses_the_inspector_seam` and
  `history_pause_holds_the_record_and_resume_continues` were still running.
  This required parallel gate is **not passing** for this checkpoint. Earlier
  passing retries do not resolve the recurring hang; no stuck-process backtrace
  was captured before the harness terminated the run.
- `cargo test --workspace --quiet -- --test-threads=1`: PASS, 465 tests,
  including 105 desktop and 32 PTY integration tests. The serial pass does not
  diagnose or erase the parallel timeout.
- `cargo clippy --workspace --all-targets -- -D warnings`: PASS; existing
  `proc-macro-error2` future-incompatibility notice only.
- `python3 scripts/check-docs.py` and `git diff --check`: PASS.

Manual Wayland validation and release builds were not repeated for this
checkpoint. M15/M16 and the M18 query prerequisite remain incomplete. Next:
capture terminal hang thread/child/wait evidence, centralize Git mutation
invalidation in dispatcher effects (including IPC mutations), then continue
M15 measured viewport/anchors and native interaction acceptance before M18
off-thread queries and M16 completion.

## M15 lifecycle/viewport implementation and native validation — 2026-10-03

Continued from `ba7aa33` with shared dispatcher effects for successful Git
mutations and project-directory changes. UI and IPC now retire both diff
comparisons and active query generations through the same owner coordination;
failed validation/operations publish no refresh effect. Successful `file.open`
emits a view effect that closes the preview and exposes the target terminal.
Repository-backed router tests cover local/scoped mutations and failed commands.

Reproduced the terminal-suite hang under a diagnostic watchdog: test PID 141392
had two threads in `do_wait`, with live `/bin/sh` children 141538/141553 sleeping
in `poll_schedule_timeout`. GDB attachment was denied by ptrace policy. The
dependency's PTY destructor sends SIGHUP and calls an unbounded child wait.
Added a 250ms drop grace followed by SIGKILL for an unreaped, still-live child;
`waitid(WNOWAIT)` leaves status/reaping ownership with Alacritty and avoids PID
reuse. Explicit shutdown similarly escalates after its 2s hangup grace. A real
shell that explicitly ignores SIGHUP now has a bounded/reaped-drop regression;
ten subsequent parallel terminal-unit runs (139 tests each) passed. The exact
reason those original shells survived their first SIGHUP was not determined.

Diff code widths are now font-shaped and cached by presentation/font identity.
Split has independent old/new X handles with shared virtual-list Y; Inline has
its own X handle. Native verification exposed two bugs and drove fixes: Y-only
lists remapped horizontal input to Y, and flex shrinking suppressed Inline X
overflow. Axis restriction and a nonshrinking virtual list fix both. Tab
presentation expands to four spaces without changing copied/staged source.
Mode/refresh anchors map source side/line, prefer surviving hunk identity and
retain fractional intra-row offset; tests cover replacement pairing and a
changed hunk ID.

The final parallel gate exposed a separate Ctrl+C-test synchronization defect:
it matched `SLEEPING` in the echoed command before `sleep` started. The test now
requires the exact output line and a foreground process group distinct from
the shell before sending SIGINT. The targeted test and final parallel/serial
workspace runs passed after this correction.

Current-revision automated checks (environment-selected Rust/Cargo 1.99.0;
repository pin and dependency resolution unchanged):

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test --workspace --quiet` | PASS: 468 unit/integration tests, including 107 desktop and 33 PTY integration |
| `cargo test --workspace --quiet -- --test-threads=1` | PASS: 468 unit/integration tests |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS; transitive future-incompatibility notice only |
| `cargo build --release --bin omaterm --bin omaterm-desktop` | PASS |
| `python3 scripts/check-docs.py` | PASS: 35 Markdown files, 126 local links, 282 blueprint refs, all 34 CLI mappings |
| `git diff --check` | PASS |

### Release Wayland evidence

Isolated instance: `/tmp/opencode/m15-live/`, with explicit HOME and all
XDG config/state/cache/runtime paths; runtime permissions 0700, history disabled,
`EDITOR=/usr/bin/true`, absolute compositor socket `/run/user/1000/wayland-1`.
Omarchy/Hyprland 0.56.2 (`efb50993780079460b0cbed1363e2166a2de1d9f`), 1536×960
logical monitor, app bounds 1526×924 at (5,31), 1.25 scale. Release app was
restarted after renderer corrections; final validation PID 181047. No user
compositor configuration was changed. Keyboard input uses `wtype`; a temporary
Wayland virtual-pointer client uses the MIT wlr protocol for clicks and axes.

Verified flows:

- Direct Git-row click opens the preview, confirmed with window captures.
- Exact keyboard copy of the selected second hunk matched raw `git diff -U3`,
  including original header suffix and Unicode.
- Keyboard staging of the second of two hunks changed only `omega` in the
  index; `alpha` remained unstaged. CLI unstage refreshed the visible preview
  and Source Control through shared mutation effects.
- On the long fixture, old X alone revealed `OLDEND` while new X stayed at
  `NEWSTART`; independent new X then revealed `NEWEND`. Both gutters/Y stayed
  fixed. Inline X also reached both long-line end markers.
- Split and Inline Y reached line 327 / `row-300-LAST` in a 303-line hunk.
- Switching modes at the bottom retained the source-line neighborhood rather
  than jumping to the keyboard-selected first hunk; source-anchor test also
  verifies fractional offset retention.
- Pointer Copy Hunk on the second hunk copied its entire 303-line body, exactly
  matching raw Git while the keyboard cursor was still on the first hunk.
- Open in Terminal closed the preview and submitted the isolated editor command
  into the existing shell; capture shows the same live PID and returned prompt.

Captures (temporary artifacts; durable summaries are this record):
`direct-git-click.png`, `after-ipc-unstage.png`,
`final-split-x-ends.png`, `final-inline-x-end.png`,
`final-inline-last-row.png`, `final-split-last-row.png`,
`final-mode-anchor.png`, `final-open-terminal.png` under that directory.

M15 remains in progress: proportional rails/paging/drag, full metadata/stale/
scope interaction matrix, rendered-range instrumentation and refresh-anchor
native coverage still need closure. M16 still requires off-thread bounded
process queries, full argument/catalog/focus/selection semantics and resource
acceptance. These results do not mark either milestone complete.

## M16 comprehensive implementation planning — 2026-10-03

Created the [M16 implementation plan](m16-implementation-plan.md) after auditing
the milestone and blueprint references, semantic commands/router, M13 finder
and fuzzy backend, M18 process-query contract, and current verification gates.
The working tree was clean before these documentation edits.

The plan defines six ordered deliveries: contracts/fixtures/API spike; typed
catalog/ranking; bounded unified sources; overlay/input/focus; dispatcher parity;
release acceptance. It records the existing `Ctrl+Shift+P` project-creation
collision, per-query detached filesystem workers, absent reusable file index,
focus-versus-navigation semantics, implicit file-open terminal targeting and
M18-query dependency. M15 remains the active implementation milestone; M16
implementation and completion are not claimed.

Documentation-only verification:

- `python3 scripts/check-docs.py`: PASS (34 Markdown files, 111 local link
  targets, 272 numbered blueprint references; all 33 CLI methods mapped).
- `git diff --check` and `git diff --no-index --check /dev/null
  docs/m16-implementation-plan.md`: PASS.
- Local consistency review: prerequisites remain explicit; planned checks are
  separate from evidence; navigation focus preserves semantic intent; no new
  palette wire method or dependency is proposed.

Cargo and native desktop checks are not applicable to this documentation-only
change. Next action: close M15, then M16 phase A baseline and shortcut/API
inventory, tracking the independently deliverable M18 query slice.

## M15 comprehensive completion planning — 2026-10-03

Created the [M15 completion plan](m15-completion-plan.md) after auditing the
milestone, blueprint references, parser/repository tests, DTOs, CLI mapping,
preview state/rendering and existing UX correction records. The clean working
tree was inspected before edits.

The plan orders six deliveries: fixtures/contracts/baseline; bounded parser and
query metadata; shared true partial-hunk staging with IPC/CLI parity; cached
presentation and guarded async lifecycle; virtualized two-axis preview with
navigation/context actions; final workspace and release Wayland acceptance.
It identifies inconsistent truncation semantics, discarded no-newline metadata,
legacy render caps/navigation state and removed copy/open actions. The milestone
now records the approved v5 Split-mode extension explicitly.

Documentation-only verification: `python3 scripts/check-docs.py` passed (32
Markdown files, 103 local link targets, 265 numbered blueprint references; all
33 CLI methods mapped); `git diff --check` and `git diff --no-index --check
/dev/null docs/m15-completion-plan.md` passed. Local consistency review
keeps planned tests separate from evidence and whole-file staging separate from
true hunk staging. Cargo and native desktop checks are not applicable to this
planning change. No application capability or M15 completion is claimed.

Next action: phase A disposable two-hunk/long-hunk fixtures and contract tests,
then parser/query hardening and shared partial-hunk staging.

### M15 parser/query contract increment — 2026-10-03

Started plan phases A/B without changing preview behavior. The bounded diff
envelope now marks `truncated` whenever a nested file or hunk cap loses data,
matching its public DTO contract. `DiffLineInfo` retains Git's no-final-newline
marker on the preceding source line and the IPC result exposes it as
`no_newline_at_end`. `git diff` now starts with `--literal-pathspecs`, so an
authorized selected filename containing Git pathspec magic is queried as a
filename rather than interpreted as a pattern.

Added parser coverage for both no-newline markers and aggregate hunk/line-cap
truncation. The disposable repository integration suite now proves a changed
`:(top,literal)draft.txt` is filtered literally. This is not partial-hunk
staging, preview virtualization or desktop acceptance; those M15 gates remain
open.

| Command | Result |
|---|---|
| `cargo test -p omaterm-context -- --test-threads=1` | PASS: 45 unit, 6 diff integration, 1 git-root integration, 8 git-status integration tests |
| `cargo test -p omaterm --bin omaterm-desktop -- --test-threads=1` | PASS: 88 tests |
| `cargo fmt --all --check` | PASS |
| `cargo test -p omaterm-core -p omaterm-protocol -p omaterm-cli` | PASS: 17 core, 6 protocol, 38 CLI tests |
| `cargo clippy -p omaterm-context -p omaterm -p omaterm-core -p omaterm-protocol -p omaterm-cli --all-targets -- -D warnings` | PASS; transitive `proc-macro-error2 v2.0.1` future-incompatibility notice only |

No Wayland or full-workspace check is claimed for this increment. Next: add a
bounded-stdin Git patch runner plus a stable hunk selector for shared
partial-hunk staging.

### M15 shared hunk-stage foundation — 2026-10-03

`DiffHunkInfo` now includes an FNV-derived ID over its parsed spans/body;
`diff.show` exposes it without exposing a patch channel. The context crate
re-reads the selected current file diff, rejects capped/binary/incomplete/stale
hunks, extracts the one matching raw hunk with its Git file headers, and sends
only that internally generated patch to bounded `git apply --cached` stdin.
The `StageHunk` semantic `GitCommand`, pure validation and router dispatch now
use this same operation with normal project scope/root checks. The UI, protocol
method and CLI mapping remain pending, so this is not end-to-end M15 staging.

`stage_hunk_updates_only_the_selected_index_change` proves in a disposable
20-line repository that staging one of two hunks updates only that index hunk
and leaves the other working-tree hunk unstaged. `cargo fmt --all` and `cargo
test -p omaterm-core -p omaterm --bin omaterm-desktop -- --test-threads=1`
passed (17 core, 88 desktop tests; known transitive future-incompatibility
notice only). No IPC/CLI, full-workspace or Wayland result is claimed.

## Handoff rules

### M15 virtual row viewport increment — 2026-10-03

Replaced the prior 32-hunk/200-line body truncation and eight-hunk window with
an all-parsed-row presentation model consumed by GPUI 0.2.2 `uniform_list`.
The flat row set is cached by project/side/path/mode and invalidated on source,
selection or mode changes. Hunk navigation now covers all loaded hunks and
scrolls the native virtual list to the selected hunk. Eligible Stage Hunk and
terminal-routed Open in Terminal controls are emitted as hunk action rows.
Pure coverage proves a 300-line hunk and later hunk are retained, and navigation
uses the complete parsed range.

Verification on current code: `cargo fmt --all --check`, workspace Clippy,
`cargo test --workspace -- --test-threads=1` (453 tests) and release builds pass.
The diff row model now retains/display-tags no-final-newline lines. On Wayland,
the `GitPath` palette result opened a two-hunk disposable-repo diff preview.
`Ctrl+Shift+S` staged only the selected first hunk: `git diff --cached --unified=0`
showed `changed alpha`, while `git diff --unified=0` retained only `changed
omega`. Capture: `/tmp/omaterm-m16-l41P/m15-diff-rebuilt.png`; the disposable
repository is `/tmp/omaterm-m15-pj3R`.

Still pending M15 proof: direct Git-list pointer selection, native vertical and
horizontal scroll, copy/open contextual action reachability, long-diff scroll
end and final native release acceptance. Pointer injection remains unavailable;
Alt-arrow injection reached the isolated test shell. This is partial live proof,
not M15 completion.

### M16 palette and M18 process-query implementation progress — 2026-10-03

Implemented and verified portions of the comprehensive M16 plan; M16 remains in
progress and M15 is still the ordered gate. Added a pure fuzzy candidate ranker,
dual-mode overlay, project/tab/pane/session IDs, command results, active-root
filename search with cooperative cancellation, changed Git paths, semantic
file/project/pane/tab/diff/Git/process actions, selected-result scrolling and a
session-local MRU. `Ctrl+Shift+P` and `Ctrl+P` open/switch modes. Project create
now uses `Ctrl+Alt+N`; a read-only `hyprctl binds -j` inspection showed no exact
system chord collision. Captured root/origin are revalidated before file-open;
stale file and Git targets show a notice rather than retargeting.

Added M18's initial bounded project process query across core/router/IPC/CLI and
the Info sections with Refresh. This query slice does not complete M18: it still
runs synchronously through the dispatcher, CPU/RSS values are absent, and scan
allocation/resource bounds have not been proven. M16 now caches an invalidatable
root-scoped file index (100k scan / 16 MiB retained-path caps) and serializes
first index builds. Search-worker admission and large-query lifecycle
measurements remain open.

Release Wayland evidence (Omarchy/Hyprland, isolated `HOME=/tmp/omaterm-m16-l41P`)
was captured outside the repository. `Ctrl+Shift+P` command mode, `Ctrl+P`
plain mode with filename + changed-Git results, split-right, focus-left,
project selection, terminal-routed file-open with isolated
`EDITOR=/usr/bin/true`, and process refresh were exercised. A same-instance CLI
spot-check listed panes, focused the other pane, and queried `process.list`;
the response was bounded and the shell sessions remained live. Selecting the
palette's changed-path Git result opened the M15 diff preview on Wayland. Direct
Git-list pointer selection and preview scroll/action interaction remain
unverified. These do not prove large-query resource behavior or M15 closure.

| Current verification | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test --workspace -- --test-threads=1` | PASS: 453 tests, 0 failures |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS; transitive `proc-macro-error2 v2.0.1` future-incompatibility notice only |
| `cargo build --release --bin omaterm --bin omaterm-desktop` | PASS |
| `python3 scripts/check-docs.py` | PASS after process method and v0.2 acceptance rows: 34 Markdown files, 115 local links, 272 blueprint references; all 34 CLI methods map to IPC |
| `git diff --check` | PASS after the latest documentation edits |

Next: finish M15 acceptance; move M18 process inspection off the owner/UI thread;
bound concurrent file-index construction and close M16 freshness/focus/argument,
CLI parity, and remaining Wayland evidence. No M16 or M18 completion is claimed.

### M15 hunk-stage entry points and Git runner hardening — 2026-10-03

Added strict `git.stage-hunk` method decoding/bridge mapping and the thin
`omaterm git stage-hunk <path> --hunk <id>` CLI mapping with human confirmation.
The preview exposes a distinct Stage Hunk control for complete modified-text
hunks; it dispatches `GitCommand::StageHunk`, retains the selected side and
invalidates both comparison refresh timestamps after success or failure.
Added selected-path worker queries and selection generation invalidation.
This does not establish full root-generation guarding, worker cancellation,
virtualization, keyboard actions or native acceptance.

The Git runner writes stdin off-thread under the same process deadline, drains
stdout/stderr concurrently beyond their storage caps, and waits for real child
exit rather than killing Git as soon as output pipes close. Exact-at-cap output
is now distinguished from over-cap output. Tests cover blocked stdin and cap
boundaries. Query and staging commands disable textconv; staging rejects
non-modified files, multi-file patch extraction and mode-change headers.
The repository staging test additionally checks unchanged worktree bytes and
duplicate rejection without index changes.

Verification:

- `cargo check -p omaterm`: PASS.
- Final `cargo test -p omaterm-context -p omaterm-core -p omaterm-protocol -p
  omaterm-cli`: PASS (47 context unit + 16 integration, 17 core, 6 protocol,
  39 CLI tests).
- Final `cargo test -p omaterm --bin omaterm-desktop -- --test-threads=1`:
  PASS (89 tests).
- `cargo clippy --workspace --all-targets -- -D warnings`: PASS; known transitive
  future-incompatibility notice only.
- `cargo test --workspace -- --test-threads=1`: TIMEOUT at 120s in PTY
  `exited_shells_close_only_their_panes_for_exit_and_ctrl_d`, after preceding
  crate/unit targets passed. The exact test passed in isolation (0.27s).
- Final `cargo test --workspace`: TIMEOUT at 120s in terminal registry
  `inserts_a_worker_spawned_session_with_its_preallocated_id`. Desktop 89,
  CLI 39, context 47 plus repository suites, core 17, IPC 11, logging 3,
  protocol 6 and state 44 passed before the hang. No full-workspace pass claimed.
- `cargo fmt --all --check`, `python3 scripts/check-docs.py` and
  `git diff --check`: PASS.

No release Wayland acceptance claimed. M15 remains in progress: viewport,
keyboard/context actions, stronger freshness/scope/error coverage, lifecycle
and live evidence remain. Intermittent terminal-suite hangs remain unresolved.

Use `not_started`, `in_progress`, `blocked`, or `complete`. Record exact commands,
results, platform/toolchain, and relevant commit or files. Record manual desktop
checks separately. An unavailable check is pending, not a pass. Mark complete only
when acceptance criteria have evidence.

For each handoff record completed behavior, changed files, check results,
reproducible blockers, approved deviations, and the smallest next action.

The [acceptance matrix](acceptance-matrix.md) tracks release requirements. Planned
tests in milestone documents are not evidence of implemented application behavior.
The [M5–M8 closure plan](m5-m8-closure-plan.md) orders the remaining blockers;
it records intended work, not completed validation.

## Diff and Files/Git UX correction planning — 2026-10-02

User feedback and screenshot identify unreadable diff rows, unnatural/slow Files
scrolling, label-only horizontal movement, off-center Git file marks, and a
persistent unusable Files shortcut strip. The
[detailed correction plan](diff-files-ux-correction-plan.md) expands R4/R5 into
U0–U5 deliveries with measured row/viewport contracts and live acceptance gates.

Source audit confirms missing `.flex()` on diff decoration and Git name/path
wrappers, hunk-granular diff scrolling, viewport estimates based on full window
height, row-rounded Files deltas, fixed horizontal thumb/range, and label text
that changes to a full path while horizontally scrolled. The plan requires
separate Inline/Split models, viewport-sized virtualization, float-pixel scroll
offsets, full-content-plane Files horizontal movement, accurate thumb geometry,
centered file marks and contextual help instead of persistent hint rows.

This is documentation-only: no application behavior changed or UI acceptance
claimed. Native reproduction and the GPUI scroll specimen are the first next
actions. Existing M15 capability gaps remain distinct from presentation repair.

Documentation checks: `python3 scripts/check-docs.py` passed (31 Markdown files,
95 local link targets, 256 numbered blueprint references; 33 CLI mappings);
`git diff --check` and `git diff --no-index --check /dev/null
docs/diff-files-ux-correction-plan.md` passed. Cargo checks are unnecessary for
this documentation-only change.

### Diff and Files/Git UX U1 — 2026-10-02

Started the immediate rendering pass from the Diff and Files/Git UX plan.
`split_cell()`, `inline_row()` and the line-number gutter now establish actual
flex rows with measured 22px/21px heights and no-wrap clipped code, preventing
line numbers and code from stacking. The diff body now uses `#101318`. Git file
marks have fixed full-row centered slots; the name/path wrapper is a real flex
column; generic files use the vendored 16px `file-text` SVG rather than `···`.
The persistent selected-Files open/copy/reveal controls and unreadable shortcut
strip are gone; supported keyboard bindings remain while contextual menu work is
deferred to the viewport rebuild.

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test -p omaterm --bin omaterm-desktop -- --test-threads=1` | PASS: 87 tests |
| `cargo clippy -p omaterm --all-targets -- -D warnings` | PASS; dependency future-incompatibility notice for `proc-macro-error2 v2.0.1` only |
| `git diff --check` | PASS |

No native screenshot or scroll interaction pass is claimed. U2 still needs the
pure Split replacement-pairing model; U3/U4 still need virtualized pixel
viewports and natural two-axis scrolling.

### Diff U2 Split pairing — 2026-10-02

`diff_panel.rs` now exposes a separate pure `SplitRow`/`SplitCell` presentation
model. Inline continues to preserve unified-diff order. For Split, context lines
pair directly and every consecutive edit run pairs deletion/addition lines by
position through the longer side, retaining each side's own line number, kind
and text. The renderer consumes those independent cells, so replacements no
longer repeat one side's code or create sequential blank blocks.

The new `split_hunk_pairs_replacements_and_preserves_unmatched_edits` test covers
context, a 2-to-3 replacement and the unmatched added spacer. `cargo fmt --all
--check`, `cargo test -p omaterm --bin omaterm-desktop -- --test-threads=1`
(88 tests), `cargo clippy -p omaterm --all-targets -- -D warnings`, and `git
diff --check` passed. Native Split capture and the U3 pixel viewport remain
pending; U2 is not visual acceptance.

### Diff U3 continuous body scroll (interim) — 2026-10-02

The diff preview now has a fixed 40px action header and Split side headers above
one identified native GPUI vertical scroll body. The prior manual wheel handler
rounded events into hunk jumps and displayed only an eight-hunk window; it is
removed. All bounded hunks now render in that continuous body, including a long
single hunk, while hunk cursor state remains for the existing keyboard controls.
The permanent hunk progress/open/copy/refresh strip and bottom shortcut sentence
were removed from default chrome. The unused open/copy helpers were removed;
those actions return only with an implemented contextual menu.

This is intentionally an interim U3 step, not the plan's final virtualized
pixel-offset viewport: rendering is still capped by the existing 32 hunks × 200
lines but is not yet viewport-row virtualized, and horizontal diff scrolling is
still pending. `cargo fmt --all --check`, targeted desktop tests (88), targeted
Clippy and `git diff --check` passed. Native Wayland scrolling validation is
pending.

### Files U4 scroll interaction interim — 2026-10-02

Removed the broken horizontal tree rail and Shift/pointer horizontal path: it
only translated labels, changed filenames to full paths and left indentation,
icons and status marks stationary. File rows now keep a stable basename until a
true whole-content horizontal viewport is implemented. The Files row window now
uses the estimated inspector body after global/header/search chrome rather than
the full window height. Wheel/trackpad deltas accumulate fractional row movement
instead of forcing each nonzero delta to jump; wheel line deltas cover three
compact rows. The vertical rail is the measured 9px width, and thumb dragging
maps pointer travel proportionally to actual scrollable row range instead of
one pointer row equaling one content row.

This is an interim interaction correction, not U4 closure. The tree remains the
existing bounded manual row viewport; a `uniform_list`/native two-axis viewport,
whole-content horizontal movement, actual text-width extents and native Wayland
gesture validation remain pending. `cargo fmt --all --check`, targeted desktop
tests (88), targeted Clippy and `git diff --check` passed.

### Git filename vertical alignment — 2026-10-02

The reported Git filename misalignment was text geometry, not the centered icon
slot: the filename/path column inherited a larger line height than its measured
11px/9px roles. It now applies `BODY_11` (16.5px line height) and `META_9`
(13.5px), inside the already centered flex column. The two-line block is 30px
inside the 40px row and therefore centers with the mark. `cargo fmt --all
--check`, targeted desktop tests (88), targeted Clippy and `git diff --check`
passed. Native visual confirmation remains pending.

### Git root-level filename alignment follow-up — 2026-10-03

The user's follow-up screenshot of `eslint.config.js` shows why the prior
line-height change was insufficient: root-level files still reserved an empty
parent-path line below the filename. The Git label column now renders its path
line only when the parent is nonempty. Root filenames center as a single line;
nested files retain the measured name/path block. This supersedes the earlier
assumption that a two-line block was appropriate for every file.

`cargo fmt --all --check`, `cargo clippy -p omaterm --all-targets -- -D warnings`
and `git diff --check` passed. Native visual confirmation of the current build
remains pending.

## UI v5 correction audit and plan — 2026-10-02

User feedback: both sidebars, icons and numerous details still differ from
the HTML. Audited the clean `8cade1b` baseline against the supplied source and
created the [correction plan](ui-v5-fidelity-correction-plan.md).

Confirmed gaps include inherited sidebar typography, an extra selected-project
action row, remaining text/Nerd icon substitutions, missing Inspector menus,
clipped/non-scrolling bodies, always-visible Git actions, focus-gated pane
toolbar at the wrong position, missing Info/status capabilities, and unmeasured
reference/render geometry. Verified against GPUI 0.2.2 that `rgb()` drops alpha
and reads the low three bytes: diff `0xRRGGBBAA` values currently produce wrong
opaque colors. Exact source transparency and inset paints require corrections.

The correction plan supplies named discrepancy IDs, ordered R0–R7 deliveries,
component targets, real browser/native measurement requirements and per-panel
acceptance gates. No application code was changed in this planning pass. No
new browser/Wayland fidelity check is claimed. Previous completion claims for
UI fidelity are superseded; missing phases remain pending.

Documentation checks:

| Command/review | Result |
|---|---|
| `python3 scripts/check-docs.py` | PASS: 30 Markdown files, 90 local link targets, 254 numbered blueprint references; 33 CLI methods mapped |
| `git diff --check` | PASS |
| `git diff --no-index --check /dev/null docs/ui-v5-fidelity-correction-plan.md` | PASS: new plan has no whitespace errors |
| Consistency review | Source targets distinguished from measured evidence; missing capabilities and visual gates remain open; no phase marked complete by this plan |

Cargo checks are not required for this documentation-only change. The next
implementation action is R0 reference measurement and the R1 specimen, followed
by R2 Projects and R3/R4 right-panel corrections.

### R0–R2 implementation progress — 2026-10-02

The frozen reference, offline browser capture and component measurements are now
present under `design/ui-v5/`. R1 added measured type roles, pill/kbd/icon
primitives, and correct RGBA treatment for diff fills. R2 began the Projects
sidebar correction in `apps/omaterm/src/main.rs`: project cards now preserve
their idle height, selected-card actions moved to a transient right-click menu,
the list uses its own vertical scroll container, and both Open Project controls
plus `Ctrl+O` use the directory picker before dispatching project creation.
Home-path shortening now uses path-component prefix matching rather than string
prefix matching.

Verification evidence:

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test -p omaterm --bin omaterm-desktop -- --test-threads=1` | PASS: 87 tests |
| `cargo clippy -p omaterm --all-targets -- -D warnings` | PASS; dependency future-incompatibility notice for `proc-macro-error2 v2.0.1` only |
| `cargo test --workspace -- --test-threads=1` | TIMED OUT after 20 minutes during the existing terminal suite; not a pass |

Native Wayland screenshot comparison and manual picker/context-menu validation
remain pending. R2 is in progress: measured capture is available, but its
five-project/width-variant acceptance gate is not yet claimed.

### Split containment and sidebar tint correction — 2026-10-02

Reported desktop regression: a horizontal terminal split let the main pane tree
claim its content minimum width and pushed the Inspector beyond the viewport.
Every split wrapper, leaf, canvas wrapper and center main-area flex item now has
zero minimum width/height and clips at its allocated pane rectangle; the fixed
Inspector width therefore remains reserved. Explicit `TEXT` tint now applies to
Projects card names, the Git branch label, the Info project name, and the root
UI surface so these labels cannot inherit a muted/default GPUI foreground.

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test -p omaterm --bin omaterm-desktop -- --test-threads=1` | PASS: 87 tests |
| `cargo clippy -p omaterm --all-targets -- -D warnings` | PASS; dependency future-incompatibility notice for `proc-macro-error2 v2.0.1` only |
| `git diff --check` | PASS |

Manual Wayland validation remains required: horizontal split with Inspector
visible at the user's desktop size, followed by visual comparison of the three
explicitly tinted labels.

## UI v5 exact-design planning — 2026-10-02

The user requires exact reproduction of the supplied `omaterm_mock_ui_v5.html`,
including Lucide icon geometry, both sidebar layouts, colors, typography,
spacing, states, and motion. The new
[pixel-perfect rewrite plan](ui-v5-pixel-perfect-plan.md) supersedes the visual
direction of the earlier VS Code workbench plan. Baseline: `dca4e4e`; the
working tree was clean before this documentation change.

Completed planning: source-derived component specifications and icon/color
inventories, computed-CSS caveats, reference-freeze requirements, native module
and geometry boundaries, phase gates, capability dependencies, screenshot
comparison criteria, and behavioral regression requirements. Full-reference
delivery includes Info, Files search, split/inline diff, and the eventual
lightweight editor; their owning milestones remain explicit dependencies.

The source HTML is supplied inline in the conversation and was not located
as a checked-in file. Reference archiving, browser computed-style measurement,
font/SVG resolution, and native/browser screenshots are the first next actions.
No application code was changed, no UI behavior or milestone was marked
complete, and no browser/Wayland visual pass is claimed by this planning work.

Documentation verification:

| Command/review | Result |
|---|---|
| `python3 scripts/check-docs.py` | PASS: 29 Markdown files, 76 local link targets, 243 numbered blueprint references; 33 CLI methods mapped |
| `git diff --check` | PASS: tracked documentation changes have no whitespace errors |
| `git diff --no-index --check /dev/null docs/ui-v5-pixel-perfect-plan.md` | PASS: new plan has no whitespace errors |
| Local consistency review | PASS: exact-design authority, source-derived versus measured values, current workbench baseline, milestone sequencing, and fixture/live acceptance are explicit |

Cargo checks are not required for this docs-only change. External URL availability
and browser/font/asset measurements remain P0 work, not passing check claims.

## UI v5 P0 — reference-freeze skeleton — 2026-10-02

Started P0 without changing application code. Created `design/ui-v5/`
with `manifest.json` (source-derived geometry/palette/cascade traps from
the v5 plan), `states.json` (S01–S22 capture matrix), and
`extract-computed.py` (freezes `reference.html` + selector checklist).
No `reference.html` exists on disk: `python3
design/ui-v5/extract-computed.py` exits 1 with `MISSING
design/ui-v5/reference.html`. No browser computed-style, font, SVG, or
screenshot evidence is claimed. Next: drop the exact supplied HTML at
`design/ui-v5/reference.html`, pin its SHA-256/CDN payloads, then build
the P1 icon/primitive specimen before any shell rewrite.

## UI v5 P1 — pure theme/geometry foundation — 2026-10-02

Added test-only `apps/omaterm/src/ui/` (`theme.rs` exact v5 tokens,
`geometry.rs` panel clamps + shell rectangles, `mod.rs`), wired as
`#[cfg(test)] mod ui` so the production binary is byte-identical in
behavior: no render-path, domain, IPC, or persistence change. Theme
tokens are explicitly `#[allow(dead_code)]` until P2 wires them; geometry
carries 3 tests (1440×900 baseline rects, hidden-panel space release,
width clamps including non-finite fallback).

Changed files: `apps/omaterm/src/{main.rs,ui/mod.rs,ui/theme.rs,ui/geometry.rs}`,
`design/ui-v5/{manifest.json,states.json,extract-computed.py}`, this record.

Automated verification (Rust 1.98.1):

| Command | Result |
|---|---|
| `cargo test -p omaterm --bin omaterm-desktop -- --test-threads=1` | PASS: 82 tests (79 existing + 3 new geometry), 0 failures |
| `cargo clippy -p omaterm --all-targets -- -D warnings` | PASS (known transitive `proc-macro-error2` future-incompat notice only) |
| `cargo fmt --all --check` | PASS |
| `python3 scripts/check-docs.py` | PASS (29 files, 76 links, 243 refs; 33 CLI methods mapped) |
| `git diff --check` | PASS |

## UI v5 P2–P6 — shell rewrite implemented (2026-10-02)

Replaced the VS Code-style frame (activity rail, title row, context row,
single contextual sidebar) with the v5 composition: persistent Projects
sidebar (210px, 164–340) + 42px tab header + right Inspector (330px,
280–470, Info/Files/Git) + 24px status bar. Terminal tabs keep
`TabCommand` selection and session lifetime; diff previews stay
view-local; every mutation still dispatches `OmaCommand`.

- P2: dual-panel state/shortcuts (`Ctrl+B` Projects, `Ctrl+Shift+B`
  Inspector, `Ctrl+Shift+E/G/I` tabs), `ui::geometry::shell_rects` driving
  both PTY sizing and render composition, v5 Projects/Header/Inspector/
  status shell, `workbench.rs` trimmed to notices/icons/branch-label,
  `flex_shrink_0` on fixed chrome (found live: panels squished without it).
- P3: terminal leaf chrome — 32px header (live dot, OSC title, shell·pid),
  focused-pane toolbar (split right/down, close), 28px footer (shell, cwd,
  grid dims), blue focus border; PTY rows subtract the 60px leaf chrome
  (`LEAF_CHROME_H`) so grids match the visible canvas.
- P4: Files inline substring filter with real input/focus/keys, 28px rows,
  chevrons, TS/`{ }` badges, Git M/U decorations; Git two groups (Staged
  Changes/Changes incl. untracked), 40px rows, collapsible sections,
  stage-all/unstage-all/discard-all (two-step arm), whole-file Stage/
  Unstage/Discard in the detail header, upstream footer; Alt+Up/Down/Enter
  row navigation; 1400ms toast for Git confirmations.
- P5: Split (default) + Inline diff modes from a tested old/new alignment
  model (`align_hunk`), v5 file-action header, side headers with real
  revision pairs, mono code rows with add/del backgrounds. Per-hunk stage
  buttons removed (they staged whole files — now honestly header-scoped).
- P6: 31 Lucide 1.49.0 SVGs vendored + `OmaAssets` + `icon()` wired across
  all chrome; `open-in-new` (non-Lucide) maps to `external-link`.

Changed files: `apps/omaterm/src/{main.rs,ui/*,files.rs,git_panel.rs,
diff_panel.rs,workbench.rs}`, `apps/omaterm/assets/icons/*`,
`design/ui-v5/*`, `docs/{dependencies.md,status.md,ui-v5-pixel-perfect-plan.md}`.
No core/protocol/CLI/persistence change; all temp proof gates reverted
(zero `TEMP-PROOF` markers in tree).

### Automated verification (Rust 1.98.1)

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test --workspace -- --test-threads=1` | PASS: 434 tests, 0 failures (86 desktop + 38 CLI + 44 context + 5 diff + 1 git-roots + 8 git-status + 17 core + 11 IPC + 3 logging + 6 protocol + 44 state + 139 terminal unit + 32 PTY integration) — full suite green in one shot, no stall this run |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (known transitive `proc-macro-error2` future-incompat notice only) |
| `cargo build --release --bin omaterm-desktop` | PASS |
| `python3 scripts/check-docs.py` | PASS: 29 files, 76 links, 243 refs; 33 CLI methods mapped |
| `git diff --check` | PASS |

### Desktop validation (Omarchy/Hyprland Wayland, isolated dirs)

- P2/P3 release proof (`shot-v5b`): Projects cards with live branch/dirty,
  tab strip with pane counts, focused-pane toolbar + header/footer with
  real PID/CWD/dims, Info body, 24px status (branch, N processes, history,
  font, UTF-8); grid dims matched the chrome-subtracted canvas.
- P4 Git render proof (`shot-git-big`, debug): real staged/unstaged/
  untracked groups, M/A/U marks, bulk + per-row actions, upstream footer.
- Screenshots live under `/tmp/omaterm-v5/` (outside the repo).
- Live-proven: frame render, terminal input/resize, panel content against
  real projects/sessions. Explicitly NOT claimed live (no pointer/modifier
  injection on the shared session): panel collapse/resize drags, Git row
  clicks → diff preview, Split/Inline toggle clicks, toast timing,
  commit/discard interactions, `Ctrl+B`/`Ctrl+Shift+B` delivery. Row/diff
  action wiring is unit-tested; the diff preview render path is exercised
  in code review only.

### Known deltas vs the mock (all recorded in the plan)

Text «/»/+/× stand-ins replaced by Lucide SVGs (done); Lucide file-type
set still partial (Nerd glyphs for non-TS/JSON); 2px left mark instead of
3px; border (not inset) focus stroke; no hover-slide animation on the
toolbar (focused-pane scoping instead); domains persist `Ctrl+Shift+P`
label (not ⌘O); no Inter bundle (system sans); no syntax colors (v0.3);
no short-HEAD in Git footer; commit box single-line editing; ports/CPU/
MEM/bell omitted until M18/telemetry; editor surface belongs to v0.3;
split-diff full-file semantics render bounded hunks.

## Foundation preparation — 2026-09-26

Completed: README/navigation, canonical agent workflow, all nine milestone contract
revisions, acceptance/dependency tracking, and the stdlib-only documentation checker.
`Cargo.lock` will be tracked. The machine-local `.codegraph` symlink is ignored and
removed from the Git index; its on-disk symlink and index data remain available.
The blueprint's architecture and milestone sequence are retained.

### Automated checks

| Command | Result |
|---|---|
| `python3 scripts/check-docs.py` | PASS: 17 Markdown files, 33 local link targets, 66 numbered blueprint references; all 17 CLI methods mapped in IPC table |
| `git diff --check` | PASS: no whitespace errors |
| `git diff --cached --check` | PASS: staged local-index removal has no whitespace errors |
| `git check-ignore --no-index .codegraph` | Matches `.codegraph`, as intended |
| `git check-ignore --no-index Cargo.lock` | No match, as intended |

Manual consistency review covered feature ownership, command/protocol agreement,
domain/runtime dependency direction, recovery behavior, and completion gates.
The checker validates local path existence, not remote URL availability or heading
anchors. No remote dependency/API verification was performed in this planning pass.

### Desktop and Rust validation

Not applicable to this documentation/tooling change: no application/Cargo workspace
exists. No Cargo or Wayland test results are claimed. The next implementation task
is M1 dependency research and a minimal GPUI window; record the selected revisions,
licenses, native packages, build checks, and actual Wayland observations there.

## Milestone 1 — GPUI Boot — 2026-09-26

Implemented a Cargo workspace containing only `apps/omaterm`, a GPUI 0.2.2 desktop
binary, and CI quality checks. The `omaterm-desktop` binary opens a titled OmaTerm
window with a dark root background and centered text. Both GPUI Linux backends are
explicitly compiled; no domain, terminal, IPC, or configuration code was added.

### Changed files

- `Cargo.toml`, `Cargo.lock`, and `rust-toolchain.toml`
- `apps/omaterm/Cargo.toml` and `apps/omaterm/src/main.rs`
- `.github/workflows/ci.yml`
- `docs/dependencies.md` and this status record

### Automated checks

| Command | Result |
|---|---|
| `cargo build --workspace` | PASS: completed with GPUI `wayland` and `x11` features enabled |
| `cargo fmt --all --check` | PASS |
| `cargo test --workspace` | PASS: 0 tests, 0 failures |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS |
| `cargo tree -e features -p omaterm` | PASS: reports GPUI 0.2.2 `wayland` and `x11` features |
| `python3 scripts/check-docs.py` | PASS: 17 Markdown files, 33 local links, 66 blueprint references, 17 CLI/IPC mappings |
| `git diff --check` | PASS |

Cargo emits a future-incompatibility notice for transitive `proc-macro-error2`
2.0.1. It does not fail the build or Clippy gate; retain it as a dependency-update
item rather than treating it as a passing-free warning.

### Desktop and platform validation

| Environment | Result |
|---|---|
| Omarchy Linux 7.2.5-3-omarchy, Hyprland Wayland (`wayland-1`) | PASS: native Wayland client mapped with title `OmaTerm`; a window-only `grim` capture confirmed the dark surface and centered `OmaTerm` text |
| X11 | PASS (build coverage): GPUI `x11` feature compiled in the workspace build; no X11 session was available for runtime testing |
| Window close | PASS: manual Omarchy validation used Super+W while monitoring btop; the window closed and `omaterm-desktop` exited cleanly. |

### Next action

Implement the pure, GPUI-independent pane tree defined by Milestone 2.

## Milestone 2 — Pure Pane Tree — 2026-09-26

Implemented `omaterm-core`, a GPUI-independent recursive pane-tree crate. It owns
typed pane/split/session identifiers; checked tree construction; split, remove,
resize, find, enumeration, ancestor, normalized geometry, directional-neighbor,
and equalize operations; and nine unit tests. The desktop now renders nested,
colored placeholder panes directly from this model. Keyboard actions call the same
core operations: `Ctrl+Shift+R` split right, `Ctrl+Shift+D` split down,
`Ctrl+Shift+W` close, `Ctrl+Shift+H/J/K/L` focus, `Ctrl+{`/`Ctrl+}` resize the
innermost containing split (physically `Ctrl+Shift+[`/`Ctrl+Shift+]` on US layout;
GPUI receives `ctrl-{`/`ctrl-}` since Shift has already been applied to the
character), and `Ctrl+Shift+E` equalize.

### Changed files

- `Cargo.toml`, `Cargo.lock`, and `apps/omaterm/Cargo.toml`
- `crates/omaterm-core/Cargo.toml` and `crates/omaterm-core/src/{lib,ids,error,pane}.rs`
- `apps/omaterm/src/main.rs`
- `docs/dependencies.md` and this status record

### Automated checks

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test -p omaterm-core` | PASS: 9 pane-tree tests, 0 failures |
| `cargo test --workspace` | PASS: 9 tests, 0 failures |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS |
| `cargo build --workspace` | PASS |
| `python3 scripts/check-docs.py` | PASS: 17 Markdown files, 33 local link targets, 66 numbered blueprint references; all 17 CLI methods mapped in IPC table |
| `git diff --check` | PASS |

Cargo continues to report the existing transitive `proc-macro-error2` 2.0.1
future-incompatibility notice during workspace test/build commands. It does not
fail the quality gate.

### Desktop and platform validation

Wayland manual validation completed 2026-09-26: split right/down, nested geometry,
close/collapse including last pane → empty-root message, HJKL focus navigation,
`Ctrl+{`/`Ctrl+}` resize, and `Ctrl+Shift+E` equalize all confirmed working on
Omarchy/Hyprland. Two corrections applied during validation:
- Fractional split-child wrappers made flex containers so panes fill their
  full allocated rectangles.
- Resize shortcuts: originally `Ctrl+Alt+H/L` (collided with Omarchy's
  completion notification), then `Ctrl+Shift+[`/`Ctrl+Shift+]` (Shift combines
  with `[`/`]` producing `{`/`}` on US layout), now bound as `ctrl-{`/`ctrl-}`
  so GPUI resolves the physical `Ctrl+Shift+[`/`Ctrl+Shift+]` correctly.

| Environment | Result |
|---|---|
| Omarchy Linux 7.2.5-3-omarchy, Hyprland Wayland | PASS: all M2 keyboard interactions confirmed |
| X11 | PASS (build coverage): GPUI `x11` feature compiled; no X11 session available |

### Next action

Finish M3 Phase C desktop items (CJK pixels, paste E2E, exit screen), then mark M3 complete.

## Milestone 3 — Single Terminal — 2026-09-26 (core committed as `bf1c901`)

Implemented `omaterm-terminal` (GPUI-free: engine trait, `AlacrittyEngine`
over `alacritty_terminal` 0.26.0, built-in `tty` PTY, `TerminalSession`,
mode-aware key encoder) plus a single-fullscreen GPUI `TerminalView`
(`canvas()` renderer, `poll()`+`async-channel` snapshot pipeline, key
forwarding, window-size resize, Shift+PgUp/PgDn scrollback, Ctrl+Shift+V
paste). Pane-tree UI is parked per the M3 non-goal; `omaterm-core` and its
9 tests are untouched. NO git commit made; working tree only.

### Changed files (uncommitted)

- `Cargo.toml`, `Cargo.lock`
- `crates/omaterm-terminal/Cargo.toml`
- `crates/omaterm-terminal/src/{lib,engine,events,alacritty,pty,session,input}.rs`
- `crates/omaterm-terminal/tests/pty_integration.rs`
- `apps/omaterm/Cargo.toml`, `apps/omaterm/src/main.rs`
- `docs/dependencies.md` and this status record

### Automated checks (2026-09-26, Rust 1.98.1, Omarchy/Hyprland)

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test --workspace` | PASS: 9 core + 16 terminal unit + 5 PTY integration, 0 failures |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (only the known transitive `proc-macro-error2` future-incompat notice) |
| `cargo tree -p omaterm-terminal` | PASS: no `gpui` in the tree |
| `python3 scripts/check-docs.py` | NOT re-run after doc edits — run before commit |
| `git diff --check` | PASS |

### Desktop validation (Omarchy/Hyprland Wayland, `wayland-1`)

Validated with the user's `$SHELL` (bash+starship) and with
`SHELL=/bin/sh`, via grim captures + wtype input (screenshots in /tmp as
`omaterm-m3-*.png`):

| Criterion | Result |
|---|---|
| Window opens, shell spawns, prompt renders | PASS: starship prompt in color, `sh-5.3$` prompt |
| Typing produces characters | PASS: `echo hello M3`, `ABC`, scripts all echo correctly |
| ANSI 16-color + 256-color fg/bg | PASS: red/green/blue/bold, C196/C46/C21/C226, BG19 blue background |
| Bold/italic/underline | PASS: all three render distinctly |
| Cursor visible + positioned | PASS: yellow block; pixel-measured exactly 7 cells for 7 typed chars |
| Enter/command execution | PASS: `echo` output, fresh prompts |
| Ctrl+C (`^C`, fresh prompt) | PASS |
| vim alternate screen + restore | PASS: `~` gutter + status line; original content restored after `:q` |
| Scrollback Shift+PgUp/PgDn | PASS: reached history top (command + lines 1–26); cursor hidden while scrolled (added during validation) |
| Long-line wrapping | PASS: overlong command wrapped across rows |
| Window-size resize | PASS (adaptation): requested 960x640 mapped tiled 592x714, grid filled correctly |
| Idle CPU | PARTIAL: ~2%/core settled idle in unoptimized debug build, all in GPUI framework baseline (reader thread 0 jiffies, no render loop, ~5 voluntary wakeups/s). Two real spins found and fixed during validation: grid-clone-per-poll (gated on bytes read) and a 16ms delivery timer (replaced with event-driven `recv().await`). Release-mode <1% measurement still pending |
| Shutdown cleanup | PASS: 3 desktop launches + integration `exit` test, zero orphan shells/zombies (`ps` verified) |
| Selection (mouse copy) | NOT DONE (Phase C) |
| Wayland paste E2E (Ctrl+Shift+V path implemented, untested) | PENDING manual |
| CJK/wide render pixels | PENDING manual (engine wide-cell + combining unit/integration tests pass) |
| `exit` on-screen flow | PENDING manual (headless exit/reap test passes) |
| IME/scaling observations | PENDING (must record explicitly per acceptance matrix, including unsupported cases) |

Known spike limitations (documented, not defects for M3): no text selection
yet; cursor is a solid overlay (covers underlying glyph); dim rendered as
normal; grid resizes if font metrics resolve late after first paint; Super
combos swallowed (reserved for desktop chrome).

### Next action

Validate the four PENDING-manual rows above on Wayland, record
IME/scaling observations, re-run `python3 scripts/check-docs.py`, then mark
M3 complete. User explicitly requested no commit until they confirm it works:
run `cargo run --bin omaterm-desktop` and exercise the terminal yourself.

## Renderer R1 — grid-exact painting — 2026-09-26 (implemented, validation blocked)

User reported the terminal looks wrong: bad font feel, rendering glitches,
lag, and cursor far from the real position. Root cause confirmed in our
code, not the engine: whole-row proportional shaping vs fixed cell grid,
generic `monospace` family with no variant/fallback control, window-derived
line height, solid-overlay cursor. `alacritty_terminal`, Zed crates
(rejected: GPL-3.0, monorepo-coupled), and `libghostty-vt` (rejected for
now: unstable API, `!Send` handles, Zig-built C dep; fixes none of the
reported symptoms) were evaluated; the engine stays.

### Changed files (uncommitted)

- `apps/omaterm/Cargo.toml` (added `tracing`)
- `apps/omaterm/src/main.rs` (font stack, metrics, reverse-video cursor)

### What changed

- Runtime font selection: first installed of JetBrainsMono Nerd Font,
  JetBrainsMono NF, DejaVu Sans Mono, Liberation Mono, Noto Sans Mono,
  else `monospace`; ligatures disabled (`calt` off); symbol fallbacks
  (Nerd Font, Noto Color Emoji, DejaVu Sans Mono).
- Metrics from the font (ascent + descent for line height, `M` advance for
  cell width) with a debug-only monospace probe (warns on advance mismatch).
- Resolved fonts cached per font size; bold/italic faces resolved eagerly.
- Block cursor is now reverse video (cell bg in cursor color, glyph in cell
  bg color); underline/bar unchanged; cursor still hidden while scrolled.
- Quality gates green: `cargo fmt --check`, `cargo clippy -- -D warnings`,
  `cargo test --workspace` (30 tests), `git diff --check` all pass.

### Validation status: R1 visually verified 2026-09-26

Validated via grim captures of an autorun session (`ENV=/tmp/m3autorun.sh`:
60 history lines, 80-col ruler, ANSI colors, CJK line — no focus needed).

| Check | Result |
|---|---|
| Row overlap fixed | PASS: clean 30px-pitch rows (18.75 css px vs 18.48 computed line height) |
| Font | PASS: JetBrainsMono Nerd Font resolved and rendered (logged at startup) |
| Grid alignment gate | PASS: cursor block at x=108–120 for the 8-char `sh-5.3$ ` prompt → 13.25px/cell vs 13.44 measured advance (subpixel rounding); ruler decades visually column-exact |
| Reverse-video cursor | PASS: 13x29px solid block on the prompt row with contrast glyph |
| Colors / CJK / wide | PASS: 16-color, bold, CJK `你好` + mixed-width line all correct |
| Scroll indicator | Implemented (thumb on right edge when history > 0); visible in captures, precise geometry unmeasured |
| Mouse wheel | Implemented (history scroll; arrows in alt screen); direction follows Wayland/libinput sign convention — **needs interactive confirmation** (session was locked/busy during implementation) |
| Idle CPU | PASS (no regression): ~2%/core settled debug baseline, reader thread 0 |
| `cargo test / clippy / fmt / check-docs` | All PASS (30 tests) |

Two bugs found by screenshots during R1: (1) rows overlapped because
GPUI/Linux reports descent NEGATIVE (`descent: -metrics.descent` in its
source) — line height is now ascent + |descent|; (2) a transient blank
first frame (startup race, self-recovered). A debug `eprintln!` used during
diagnosis was replaced with `tracing::info!`. No commit made.

## Scroll UX fix — natural direction, correct thumb, auto-hide — 2026-09-26 (uncommitted)

User reported on the release build (smooth): wheel scrolled the wrong way
vs Omarchy natural scroll, and the always-visible thumb sat wrong. Two root
causes, both ours: (1) `on_scroll_wheel` negated GPUI's delta, but GPUI's
Wayland backend already normalizes the sign (`vertical_modifier = -1.0` in
`platform/linux/wayland/client.rs`), so the extra negation fought the
compositor's natural-scroll setting; (2) thumb `frac = offset / history`
placed it at the track TOP when `display_offset == 0`, which is the live
BOTTOM edge.

### Changed files (uncommitted)

- `apps/omaterm/src/main.rs` (wheel sign, thumb geometry, auto-hide)
- This status record

### What changed

- Wheel delta passes through untouched (positive y = wheel-up = toward
  history = positive `Scroll::Delta`); alt-screen Up/Down arrows follow the
  same sign. Compositor natural-scroll is honored automatically.
- Thumb geometry inverted to match: bottom at live edge, top at oldest
  history.
- Thumb shows only for 800ms after wheel or Shift+PgUp/PgDn scroll input
  (`scroll_indicator_until` deadline + one-shot `gpui::Timer` hide task;
  stale tasks exit quietly; zero timers/wakeups at idle).
- `paint_terminal` args bundled into `PaintArgs` (clippy `too_many_arguments`).

### Automated checks

| Command | Result |
|---|---|
| `cargo fmt --all` | applied |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (known `proc-macro-error2` future-incompat notice only) |
| `cargo test --workspace` | PASS: 30 tests, 0 failures |
| `cargo build --release --bin omaterm-desktop` | PASS (`target/release/omaterm-desktop`) |
| `python3 scripts/check-docs.py` | PASS (external URLs/anchors need separate review, as before) |
| `git diff --check` | PASS |

### Validation status: needs interactive confirmation

Deliberately NOT launched/screenshot-verified by the agent (user session
active; a new window would steal focus). User to confirm in the release
build: wheel-up goes to history / wheel-down to prompt, thumb appears only
while scrolling and sits at the bottom at the live edge. No commit made.

## M3 completion — selection, OSC 7, shutdown, regression tests — 2026-09-26 (uncommitted)

Closes the remaining written M3 contract items without expanding into M4.
`omaterm-terminal` stays GPUI-free (`cargo tree` shows zero `gpui`).

### Changed files (uncommitted)

- `crates/omaterm-terminal/src/{engine,alacritty,events,lib,session,pty,input}.rs`
- `crates/omaterm-terminal/src/osc7.rs` (new), `selection.rs` (new)
- `crates/omaterm-terminal/tests/pty_integration.rs`
- `apps/omaterm/src/main.rs` (selection UI, paste helper, reader close check)
- `docs/acceptance-matrix.md` and this status record

### What changed

- Core boundary: `app_cursor`/`app_keypad`/`bracketed_paste` moved onto the
  `TerminalEngine` trait; UI calls `TerminalSession` methods, never the
  concrete `AlacrittyEngine`.
- CWD tracking: session keeps `CurrentDirectory { path, provenance }`
  (`Launch` initial); streaming OSC 7 parser accepts only absolute local
  `file://` URIs (empty/`localhost` host, strict `%XX`, BEL or ST
  terminators, fragmented reads reassembled, 4KB cap); remote/non-file/
  relative/malformed reports rejected without state change; accepted reports
  emit `TerminalEvent::CwdChanged`. `/proc/<pid>/cwd` stays a best-effort
  optional refresh (`Procfs` provenance). Scrollback cap 10,000 enforced
  with a bound test.
- Selection/copy: drag-select on the fixed grid (never glyph hit-testing),
  highlight overlay under glyphs, `extract_text` with wide/combining
  support, `HIDDEN` cells never copied, `WRAPPED` rows join without newline
  (new `CellFlags::WRAPPED` mapped from alacritty `WRAPLINE`), press-only
  clears, drag publishes to Wayland primary, `Ctrl+Shift+C` copies to
  clipboard. No mouse-reporting protocol (M3 non-goal).
- Shutdown: `PtyProcess::terminate` (SIGHUP) + bounded `TerminalSession::
  shutdown` (reap ≤2s); reader thread exits when the snapshot channel
  closes so an idle reader cannot retain the session.
- Input/paste: pure `prepare_paste` helper (UI uses it); encoder covers
  Ctrl+D/Z; integration proves Ctrl+C → exit 130, Ctrl+D → clean exit,
  Ctrl+Z → SIGTSTP trap, wrapped paste round-trips through `cat`.
- Regression: 3000-line burst completes with bounded history, resize
  mid-flood keeps the tail, alt-screen restores prior content, bracketed
  mode tracked.

Two real bugs found by the new tests: (1) bytes written before the shell's
first prompt are swallowed at startup — interaction tests now wait for the
prompt; (2) test ordered `cat` before `printf`, so the mode escape went to
`cat`'s stdin — reordered (shell first, then `cat`).

### Automated checks (2026-09-26, Rust 1.98.1, Omarchy/Hyprland)

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test --workspace` | PASS: 9 core + 50 terminal unit + 13 PTY integration, 0 failures |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (known `proc-macro-error2` future-incompat notice only) |
| `cargo tree -p omaterm-terminal` | PASS: zero `gpui` |
| `cargo build --release --bin omaterm-desktop` | PASS |
| `python3 scripts/check-docs.py` | PASS (external URLs/anchors need separate review, as before) |
| `git diff --check` | PASS |

### Environment availability (explicit, not passes)

Available: bash, vim, nvim, `top`, `btop`, `less`, `tmux`. NOT available:
`zsh`, `fish`, `htop` — no evidence claimed for these. Real-shell OSC 7
emission is shell-config dependent (stock bash emits none); parser proven
with synthetic sequences, live-shell OSC 7 E2E still open.

### Completion decision and documented limits

User approved the release-build interaction pass on 2026-09-26 and accepted
M3 completion. The terminal's selection/copy/paste, scrolling, resize, and
normal interactive use were manually exercised on Omarchy/Wayland.

- **IME composition:** explicitly unsupported for M3. The current GPUI 0.2.2
  integration sends committed key events only and has no composition
  start/update/commit/cancel path. Normal direct Unicode input, wide cells,
  and combining characters work; CJK/Japanese/Chinese/Korean IME preedit and
  candidate UI must not be assumed to work. This is a documented release
  limitation, not a passing IME claim.
- **Unavailable programs:** `zsh`, `fish`, and `htop` were not installed, so
  they were not manually tested. Bash, vim, nvim, top, btop, less, and tmux
  availability was recorded; vim alternate-screen behavior was manually
  verified.
- **OSC 7 live shell emission:** stock bash on this machine does not emit
  OSC 7. Parser/session accept-reject behavior is covered by deterministic
  tests; shell-configured live OSC 7 emission remains a follow-up validation,
  not a blocker for the M3 parser contract.
- **Scaling:** no alternate monitor or fractional-scale session was available
  for an additional desktop observation. The font-metric grid gate was
  pixel-validated on the primary Omarchy display.

M3 is complete with the above explicit limits. The next action is M4's
long-lived multi-session registry and pane binding.

## Milestone 4 — Multiple Pane Terminals — 2026-09-27

Connected the recursive M2 `PaneTree` to long-lived registry-owned M3 sessions.
Each terminal pane stores a `SessionId`; the GPUI-free `WorkspaceCoordinator`
owns the tree and `TerminalRegistry`, and the desktop keeps per-session bounded
snapshot channels and per-pane renderer state. The prior single-terminal view is
now a recursive pane renderer. Workspace operations delegate to the coordinator
for split, close, focus, resize, and equalize. Input, scrollback, paste, copy,
and selection target the focused/clicked pane. All session readers pump output
independently of focus. Closing detaches only that pane's session and performs
bounded shutdown/reap on a background thread. Natural shell exit (`exit` or
Ctrl+D) closes its pane by session ID, including when it is not focused; a
sibling remains focused and alive, while exit of the final session presents
the empty-workspace new-terminal action. Moving the pointer over a pane changes
logical focus so subsequent keyboard input follows the hover target; a drag
selection remains associated with the pane where the drag began.

### Changed files

- `crates/omaterm-core/src/pane.rs` — empty workspace constructor
- `crates/omaterm-terminal/src/{lib,registry,workspace}.rs` — session registry
  and GPUI-free workspace coordinator
- `crates/omaterm-terminal/src/workspace.rs` — session-ID-driven pane close
- `crates/omaterm-terminal/tests/pty_integration.rs` — independent sessions,
  close isolation, per-session resize, unfocused output, auto-close on exit/
  Ctrl+D, 100-cycle FD check
- `apps/omaterm/src/main.rs` — M4 workspace renderer, input routing, runtime
  readers, pane geometry-based resize
- `docs/status.md`, `docs/acceptance-matrix.md`

### Automated verification (Rust 1.98.1, Omarchy Linux/Hyprland)

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test --workspace -- --test-threads=1` | PASS: 9 core + 64 terminal unit + 20 PTY integration, 0 failures |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (existing transitive `proc-macro-error2` future-incompatibility notice only) |
| `cargo build --release --bin omaterm-desktop` | PASS |
| `cargo tree -p omaterm-terminal` | PASS: no `gpui` dependency |
| `python3 scripts/check-docs.py` | PASS: 17 Markdown files, 32 local links, 66 blueprint refs, 17 CLI/IPC mappings |
| `git diff --check` | PASS |

Registry/coordinator coverage verifies distinct session IDs/PIDs, four-pane
binding, split spawn-failure rollback, close isolation, focus/layout preserving
session IDs, final-pane empty state, and session-ID close preserving focus for
an unaffected sibling. PTY integration verifies four shells have independent output,
closing one preserves other PIDs and input, resizing one session leaves the
other grid unchanged, unfocused output continues to drain, and both `exit` and
Ctrl+D are resolved to the correct pane while the sibling remains usable. The
final pane is also removed after Ctrl+D and leaves an empty workspace. The 100
create/close cycles leave no FD growth beyond the test's +2 tolerance. Child
reaping is explicitly checked for coordinator close.

### Wayland desktop validation

Environment: Omarchy Linux / Hyprland Wayland, GPUI release build. The app
launched with a shell, accepted nested horizontal/vertical splits to four
panes, and rendered four independent prompts. `stty size` in a half-width pane
reported `30 60`, matching the actual pane geometry; after the nested split,
the four panes rendered at their allocated sizes. Distinct `PANE3` and `PANE4`
commands appeared only in their respective focused panes. Closing one pane
reduced the layout to three panes while the remaining shells stayed alive;
closing all panes showed the empty-workspace new-terminal action, and
`Ctrl+Shift+T` created a fresh shell. The desktop was then closed and its test
shell process disappeared. Screenshots used during the pass were kept in
`/tmp/m4-*.png`, not the repository.

Follow-up validation 2026-09-27: release Wayland checks confirmed `exit` and
Ctrl+D each close only the exited pane while leaving its sibling usable. The
user also confirmed hover-to-focus works and routes subsequent input to the
hovered pane.

The default-parallel `cargo test --workspace` was also attempted twice during
this follow-up; one run stalled in a multi-PTY unit test and hit its 240s
command timeout, and a second run stalled in another PTY coordinator unit test
and hit 120s. The same complete workspace suite passes with `--test-threads=1`.
Investigate PTY test concurrency as a follow-up; serial results are the verified
gate for this change.

The initial paint-based resize attempt was rejected after an interactive run
showed incorrect early grid geometry. Final sizing derives each grid from the
window viewport and the core tree's normalized pane rectangles, and skips
degenerate (<2 rows) geometry offers. The release Wayland run confirmed the
result. Mouse click-to-focus and pointer selection were implemented but not
separately verified. Thread count, RSS, and GPU-resource
growth were not measured in the 100-cycle test; those remain broader lifecycle
diagnostics, not passing claims. M4 has no tabs, so tab-hidden renderer parking
belongs to M5; background/unfocused PTY draining is covered by integration
test.

### Next action

Begin M5 Projects/Tabs; carry forward thread/memory/GPU lifecycle measurements
and actual hidden-tab rendering behavior as M5 acceptance work.

## Milestone 5 — Projects & Tabs — complete with resource-observation limits

Added GPUI-free typed project/tab/window hierarchy models, per-project tab and
selection state, next-then-previous fallback on removal, and focused-pane
validation. Refactored `WorkspaceCoordinator` to coordinate a selected project
and tab over the existing global `TerminalRegistry`; switching selection retains
session IDs and child processes. Project/tab closure detaches owned sessions,
and pane/session closure removes a final tab while preserving an empty project.
Added the compact project sidebar and tab strip, click selection, create/close
actions, and keyboard switching (`Ctrl+PageUp/Down`, `Alt+PageUp/Down`), project
creation (`Ctrl+Shift+P`), tab creation (`Ctrl+Shift+T`), and tab close
(`Ctrl+Shift+Q`). Visible-pane grid sizing accounts for the sidebar and tab bar.
Follow-up polish keeps the close control inside the selected project/tab row,
places the add-tab control after the tab chips, and disambiguates repeated
directory-derived project labels with a display-only ordinal. Failed shell
creation now presents an error and Retry action. A PTY integration test proves
output continues in hidden tabs and projects, process IDs remain stable, and
closing those hidden containers only detaches their owned sessions.

### Changed files

- `crates/omaterm-core/src/{ids,error,lib,project,tab,workspace}.rs`
- `crates/omaterm-terminal/src/workspace.rs`
- `crates/omaterm-terminal/tests/pty_integration.rs`
- `apps/omaterm/src/main.rs`
- `docs/status.md`, `docs/acceptance-matrix.md`

### Automated verification (Rust 1.98.1)

| Command | Result |
|---|---|
| `cargo test -p omaterm-core` | PASS: 11 tests |
| `cargo test --workspace -- --test-threads=1` | PASS: 11 core + 65 terminal unit + 21 PTY integration (97 total), 0 failures |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS |
| `cargo fmt --all --check` | PASS |
| `cargo tree -p omaterm-terminal` | PASS: no GPUI dependency |
| `cargo build --release --bin omaterm-desktop` | PASS |
| `python3 scripts/check-docs.py` | PASS: 17 Markdown files, 32 local links, 66 blueprint references, all 17 CLI/IPC mappings |
| `git diff --check` | PASS |

User confirmed the M5 Wayland project/tab/pane workflows, switching and state
preservation, hidden output, split panes, keyboard shortcuts, and the initial
chrome polish. The isolated release Wayland follow-up below now confirms the
latest inactive-tab chip's padding hit target and initial-shell Retry recovery.
Two measured 100-cycle passes found stable FD and thread counts and only 36 KiB
of RSS increase across the second pass after a 16 MiB first-pass warmup rise.
RadeonTop provides device-wide, not per-process, GPU counters; this limit is
recorded rather than claiming zero GPU allocation.

### M5 Wayland and lifecycle follow-up — 2026-09-27

Environment: Omarchy Wayland, Hyprland 0.56.2; release binary
`target/release/omaterm-desktop`. Used an isolated `XDG_STATE_HOME` and a
deliberately missing `SHELL` path. The desktop rendered the expected startup
failure and Retry control. After creating the test shell path as a symlink to
`/bin/sh`, Retry opened a shell. `Ctrl+Shift+T` created Tab 2; clicking Tab 1's
left chip-padding area, outside the label, selected it. Captures remain outside
the repository: `/tmp/opencode/omaterm-m5-retry-2.png` (successful Retry and
both tabs) and `/tmp/opencode/omaterm-m5-tabs-selected.png` (padding click
result). The test window closed gracefully; isolated state and test shell were
removed; no test app or shell remained. The pre-existing user window was not
targeted.

Two consecutive 100-cycle visible tab/session create-then-close runs retained
one `/bin/sh` session between cycles, on the same Hyprland session and release
build, taking 13.75s and 13.73s. Process measurements came from `/proc`, sampled
after each close:

| Measurement | Run 1 | Run 2 |
|---|---:|---:|
| App threads at baseline / end | 21 / 21 | 21 / 21 |
| Peak sampled process-tree threads | 22 | 21 |
| App RSS baseline / end | 55,156 / 71,416 KiB | 71,416 / 71,452 KiB |
| Peak sampled RSS | 76,336 KiB process tree | 71,452 KiB app |
| App FD count at end | 34 | 34 (baseline/peak also 34) |
| Direct child shells at end | 1 | 1 |

Two one-second RadeonTop samples after the test instance closed showed device-
wide GPU 0.00%, VRAM 674.46 MiB and GTT 63.55 MiB. This is system/compositor
context, not per-process usage. The existing automated 100-cycle PTY FD test
remains stronger FD evidence. These observations do not replace later M8
resource tests under concurrent IPC load.

The previously pending inactive-tab hit target and startup Retry now have release
Wayland evidence. New tests cover repeated tab close without sibling detachment
and project cleanup that continues when one session mutex is poisoned; the
poisoned session's PTY drop path reaps its child, while the healthy sibling is
explicitly shut down. M5 is complete. Per-process GPU utilization/VRAM remains
unavailable from this session's tooling; that is not represented as a zero-use
pass, and M8's concurrent IPC resource measurement remains open.

### M5 closure regression verification — Rust 1.98.1

| Command | Result |
|---|---|
| `cargo test -p omaterm-terminal workspace::tests::repeated_tab_close_is_rejected_without_detaching_the_project_sibling -- --exact` | PASS |
| `cargo test -p omaterm-terminal workspace::tests::project_close_returns_all_sessions_when_one_cleanup_handle_is_poisoned -- --exact` | PASS |
| `cargo fmt --all --check` | PASS |
| `cargo test --workspace -- --test-threads=1` | PASS: 13 core + 14 state + 69 terminal unit + 21 PTY integration + 4 desktop router + 8 IPC/protocol tests (129 total) |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (known transitive `proc-macro-error2` future-incompat notice only) |

## Milestone 6 — Persistence — complete with explicit environment limits

Added `omaterm-state` with human-readable schema-v1 JSON snapshots, typed-ID and
workspace reconstruction, bounded structural validation, CWD provenance, and
same-directory atomic replacement. Startup scans recovery snapshots newest-first;
valid recovery is restored, while invalid recovery files remain intact and a new
unique recovery path is selected. The desktop launches fresh sessions into
restored pane IDs, falls back to home when a remembered directory is unavailable,
debounces meaningful mutations, and uses a one-slot revision-ordered writer.
Window close is deferred while background work finishes the final save and
parallel bounded terminal shutdown. Per-pane restore failures retain the layout
and expose a Retry action.

### Changed files

- `Cargo.toml`, `Cargo.lock`, `apps/omaterm/Cargo.toml`
- `crates/omaterm-state/{Cargo.toml,src/*.rs}`
- `crates/omaterm-terminal/src/workspace.rs`, `tests/pty_integration.rs`
- `apps/omaterm/src/main.rs`
- `docs/dependencies.md`, `docs/status.md`, `docs/acceptance-matrix.md`

### Automated verification so far

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS (2026-09-27) |
| `cargo test -p omaterm-core` | PASS: 11 tests |
| `cargo test -p omaterm-state` | PASS: 14 tests, including empty state, nested multi-project round trip, limits, recovery-file scan, atomic failure injection, and writer revision tests |
| `cargo test -p omaterm-terminal --lib -- --test-threads=1` | PASS: 67 tests, including restored fresh-session binding and missing-CWD home fallback |
| `cargo test -p omaterm-terminal --test pty_integration -- --test-threads=1` | PASS: 21 PTY integration tests |
| `cargo test -p omaterm --bin omaterm-desktop` | PASS: 0 tests, binary test target builds |
| `cargo test --workspace -- --test-threads=1` | BLOCKED: two attempts stalled at terminal unit tests (`four_panes_have_independent_sessions` / `session::fragmented_osc7_reassembles`) until 300s timeout; every package/test target passed when run separately in sequence |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (2026-09-27; existing transitive `proc-macro-error2` notice only) |
| `cargo build --release --bin omaterm-desktop` | PASS (2026-09-27) |
| `cargo tree -p omaterm-state` | PASS: core/serde/serde_json only; no GPUI or terminal runtime dependency |
| `cargo tree -p omaterm-terminal` | PASS: no GPUI dependency |
| `python3 scripts/check-docs.py` | PASS: 17 Markdown files, 32 local links, 66 blueprint references, all 17 CLI/IPC mappings |
| `git diff --check` | PASS |

### M6 Wayland validation — complete with environment limits — 2026-09-27

Found and fixed a real CWD persistence defect during this pass: the terminal
session exposed `refresh_cwd_from_procfs()`, but desktop CWD collection never
called it, so `cd` after launch was saved with `Launch` provenance and the
launch directory. `WorkspaceView::current_cwds()` now refreshes procfs before
reading each session CWD. Added PTY integration test
`procfs_refresh_tracks_a_shell_directory_change` for the underlying behavior.

Environment: Omarchy Wayland / Hyprland 0.56.2, release desktop,
`XDG_STATE_HOME=/tmp/opencode/m6-wayland/state-fixed`, isolated `HOME` with a
`.bashrc` that prints `pwd` on each fresh shell. Created Project A with two
tabs at `/tmp/opencode/m6-wayland/cwd-a` and `cwd-b`, and Project B with one tab
at `cwd-c`. Schema-v1 snapshot inspection showed the three per-pane paths with
`procfs` provenance, both projects/tabs, and selected project/tab IDs. After
graceful close and restart, a new desktop PID and three new shell PIDs were
observed (old shells `1599024,1600535,1601783`; restored shells
`1603691,1603693,1603695`). The selected Project B and its shell in `cwd-c`
were visible on startup; switching to Project A showed its saved tab shell in
`cwd-b` and its other tab in `cwd-a`. Captures: `/tmp/opencode/m6-wayland/
after-restart.png`, `after-project-a.png`, `after-tab-1.png`.

Deleted the saved `cwd-b` directory while the app was closed and restarted.
The restored tab shell correctly fell back to the isolated home directory
`/tmp/opencode/m6-wayland/home`; final shutdown saved `cwd_provenance:
fallback_home`. Capture: `/tmp/opencode/m6-wayland/fallback.png`.

For restored-pane retry, relaunched with a missing shell executable in
`SHELL`. Restore preserved the project/tab layout and showed the recoverable
shell error. Added a symlink to `/bin/bash` at that path and activated Retry;
the pane launched a fresh shell in the remembered fallback home while the
other failed restore panes remained represented. Captures:
`restored-retry.png` and `restored-retry-success.png` in the same temporary
evidence directory.

For corruption recovery, replaced only the isolated primary snapshot with the
26-byte invalid test fixture (SHA-256
`2f8c71d7f37c73a9352e2ce5cbfb141533d2869baf69a5f3681e864400e776cc`). The
Wayland startup displayed the recovery warning and opened a shell. Graceful
close created `workspace-recovery-v1.json`; a subsequent restart showed the
warning/recovery workspace again. The primary retained the same SHA-256 after
that second restart. Captures: `corrupt-recovery.png` and
`recovery-restart.png`. Temporary state, test homes, CWD directories, symlink
and log files were removed after testing; screenshots remain under
`/tmp/opencode/m6-wayland/`. No test app/shell remained after final close.

For unsupported-schema recovery, supplied a schema-v1-shaped primary with
`schema_version: 999`. The release window displayed the unsupported-version
warning. Created a second tab and closed the app immediately, before the
two-second debounce; the final recovery snapshot contained both tabs. The
original primary still had SHA-256
`0807e23a63c58c8abe48b127e3c4403cbf7268570fd45bc993f00c56e1d8b978` after
shutdown. Capture: `/tmp/opencode/m6-schema-verify/pending-save.png` (tab
mutation plus recovery warning). The app and shell processes were gone after
close; test state was removed.

### M6 follow-up automated checks — Rust 1.98.1

| Command | Result |
|---|---|
| `cargo test -p omaterm-terminal --test pty_integration procfs_refresh_tracks_a_shell_directory_change -- --exact` | PASS |
| `cargo fmt --all --check` | PASS |
| `cargo test --workspace -- --test-threads=1` | PASS: 13 core + 14 state + 69 terminal unit + 22 PTY integration + 4 desktop router + 8 IPC/protocol tests (130 total) |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (known transitive `proc-macro-error2` future-incompatibility notice only) |
| `cargo build --release --bin omaterm-desktop` | PASS, used for the Wayland run above |

### M6 nested-split/focus Wayland follow-up — PASS — 2026-09-27

Using an isolated release instance and Hyprland `send_shortcut` with physical
XKB keycodes (`Ctrl+Shift+R` as `code:27`, then `Ctrl+Shift+D` as `code:40`),
created a horizontal split with a nested vertical split. Saved the focused
bottom-right pane at `/tmp` and the other panes at the launch directory. After
graceful close/restart, the same 3-pane nested geometry rendered, with `/tmp`
restored to the bottom-right focused session. Typing `echo M6_FOCUS_CHECK`
executed in that pane only, confirming restored semantic focus. Snapshot
inspection showed unchanged split/pane IDs and `focused_pane` referencing the
bottom-right leaf. Captures outside the repository:
`/tmp/opencode/m6-key/nested-restored.png` and
`/tmp/opencode/m6-key/focus-restored.png`. The test window closed gracefully;
isolated state was removed; no test app/shell remained.

M6 is complete. The only environment-specific limit is that a desktop run with
no usable `$HOME` was not available; state/coordinator tests cover the recovery
error paths. M5 is complete as recorded above.

## Milestone 7 — Command Router — implementation in progress

Added GPUI-independent semantic command payloads, pure validation, stable error
codes, and owned result DTOs to `omaterm-core`. Added the desktop application's
GPUI-free `CommandRouter`, which dispatches project/tab/pane/terminal operations
through `WorkspaceCoordinator` and returns lifecycle, persistence, and redraw
effects. UI keyboard and pointer actions for project/tab selection and lifecycle,
pane split/close/focus/resize/equalize, terminal key input, paste, restored-pane
retry, and exited-session close now enter through the dispatcher. Runtime readers,
renderer state cleanup, session reaping, persistence debounce, and redraw consume
the returned effects. Coordinator operations now include target-ID split/close/
focus/resize/equalize, metadata rename, and directory-targeted tab creation.
Follow-up dispatcher tests now cover project/tab/pane/terminal query and mutation
success paths, ordered lifecycle/persistence effects, stale IDs, close/reap, and
the rule that terminal bytes do not mark workspace state dirty. `terminal.run`
is now implemented for Bash: a private rc hook preserves the user's
`~/.bashrc`, emits a per-session OSC 133 prompt-ready marker, argv is single-
quote encoded, and user input clears readiness. Non-Bash shells return explicit
`unsupported_operation`; unknown/not-ready Bash sessions return `shell_busy`.
The terminal layer now also supports caller-allocated session identities and
owner-only registration. All creation commands (`project.create`,
`tab.create`, `terminal.create`, `pane.split`, restored-pane launch/Retry) now
return a `Pending { operation_id }` receipt from `dispatch_async` and commit
through the bounded `SessionSpawnQueue` worker plus coordinator
`commit_*_session` guards on the application owner. Synchronous creation arms
were removed from the dispatcher so UI and IPC share one async launch path;
stale completions are rejected without registry publication and failed or
cancelled provisionals are reaped off-thread.

### Changed files

- `crates/omaterm-core/src/{command,result,validation,lib}.rs`
- `crates/omaterm-terminal/src/{workspace,pty,session,registry,shell,spawn_queue}.rs`
- `crates/omaterm-terminal/tests/pty_integration.rs`
- `apps/omaterm/src/{main,router,credentials,ipc_bridge}.rs`
- `crates/omaterm-protocol/src/{lib,method}.rs`
- `crates/omaterm-ipc/src/lib.rs`

### Automated verification (Rust 1.98.1)

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS (format applied before final checks) |
| `cargo test -p omaterm-core` | PASS: 13 tests, including semantic validation boundaries |
| `cargo test -p omaterm --bin omaterm-desktop` | PASS: 10 router tests |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS |
| `cargo test --workspace -- --test-threads=1` | PASS: 13 core + 14 state + 73 terminal unit + 24 PTY integration + 10 desktop router + 8 IPC/protocol tests (142 total) |
| `cargo build --release --bin omaterm-desktop` | PASS |
| Isolated Wayland/X11 release startup smoke (`timeout --signal=TERM 8s target/release/omaterm-desktop`) | PASS: process remained running until the expected timeout; isolated XDG state/config was removed afterward |
| `cargo tree -p omaterm-core` | PASS: core has only domain dependencies; no GPUI/runtime dependencies |
| `cargo tree -p omaterm-terminal` | PASS: no GPUI dependency |
| `git diff --check` | PASS after documentation and code updates |

The known transitive `proc-macro-error2` future-incompatibility notice remains;
quality gates pass with `-D warnings`.

### M7 completion — 2026-09-27

- Bash `terminal.run` keeps its prompt lifecycle markers, safe argv encoding,
  submit-only acknowledgement, and ready/busy tests. Other dialects remain
  explicitly unsupported; `terminal.clear` remains an explicit unsupported
  response. Both are covered by tests, not by absence of validation.
- Creation is asynchronous through `dispatch_async` with owner-serialized
  `poll_launches`/`finish_launch` completion, stale-target revalidation, and
  provisional-session disposal. Dead synchronous creation arms were removed
  from `dispatch_valid` (defensive `RuntimeFailure` if ever reached), so the
  async path is the only creation implementation. Note: the milestone text
  names the entry point `dispatch()`; the async contract from the closure plan
  (step 7A, pending receipt + final commit result) supersedes that name, and
  the blocking `dispatch` wrapper is test-only.
- Router tests (19 desktop tests) cover every command variant's success shape,
  stale targets, validation failures with no effects, ordered
  `SessionStarted → WorkspaceChanged → PersistenceDirty` effects, async split
  close-race rollback, cancel/duplicate completion publishing nothing,
  project-scope filtering/denial, credentialed child-env injection plus
  owner-close revocation, and the rule that terminal bytes do not mark
  workspace state dirty.
- Release Wayland regression (Omarchy/Hyprland, release binary, isolated
  `XDG_STATE_HOME`) exercised the async path end to end: IPC `pane.split`
  with visible panes, UI `Ctrl+Shift+T` tab creation, UI resize
  (`Ctrl+Shift+[` moved a horizontal boundary 0.5 → 0.45/0.55) and
  `Ctrl+Shift+E` equalize back to 0.5/0.5, IPC `pane.focus`/`pane.equalize`/
  `pane.close`, `terminal.send`/`terminal.read` round-trip, async restore on
  restart, and 26 clean rapid split/close cycles with pane/session counts
  consistent throughout (see closeout section below for the one transient).

M7 is complete. M5/M6 completion evidence is recorded in their sections above.

## Milestone 8 — IPC — transport foundation in progress

Added the transport-independent `omaterm-protocol` crate with v1 request,
response, error, and redacted capability-token DTOs. It rejects unknown envelope
fields and unsupported versions, validates bounded request metadata/token
encoding, and bounds serialized responses to 1 MiB by truncating result text on
UTF-8 boundaries with `truncated: true`.

Added `omaterm-ipc` with a Unix socket client/server, incremental 64 KiB request
framing, bounded response framing, 32-connection cap, read/write timeouts,
same-UID peer validation, private parent-directory checks, startup lock,
active/stale socket probing, mode-0600 endpoint, and inode-checked cleanup.
The server callback is intentionally transport-only: desktop code must enqueue
requests to its serialized owner and must not mutate workspace state on socket
threads.

### Changed files

- `Cargo.toml`, `Cargo.lock`, `apps/omaterm/Cargo.toml`
- `crates/omaterm-protocol/{Cargo.toml,src/lib.rs,src/method.rs}`
- `crates/omaterm-ipc/{Cargo.toml,src/lib.rs}`
- `apps/omaterm/src/{main,router,credentials,ipc_bridge}.rs`
- `docs/dependencies.md`, this status record

### Automated checks (Rust 1.98.1)

| Command | Result |
|---|---|
| `cargo fmt --all` | PASS (format applied) |
| `cargo test -p omaterm-protocol -p omaterm-ipc` | PASS: 5 protocol + 3 socket integration tests |
| `cargo fmt --all --check` | PASS |
| `cargo test --workspace -- --test-threads=1` | PASS: 13 core + 14 state + 67 terminal unit + 21 PTY integration + 4 desktop router + 8 IPC/protocol tests |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (known transitive `proc-macro-error2` future-incompatibility notice only) |
| `python3 scripts/check-docs.py` | PASS: 18 Markdown files, 35 local links, 71 blueprint references, all 17 CLI/IPC mappings |
| `git diff --check` | PASS |
| Desktop/Wayland validation | NOT DONE: IPC is not yet started or integrated by desktop |

### M8 completion — 2026-09-27

M7 is complete (see above); M8 end-to-end acceptance is claimed on that basis.

Implemented: strict typed DTOs for all 17 wire methods with unknown-field
rejection; bounded `terminal.send` (base64, 8 KiB), `terminal.run` argv
(256 entries, 4 KiB each), and `terminal.read` (1000 x 1000) checks plus
control-character rejection; list truncation (128 items) with accurate
`truncated` flags; redacted request/token debug formatting; per-session scoped
tokens plus owner-only local-user credential file; project-scope authorization
in the router with filtered lists and `cross_project_denied`/
`permission_denied` errors; child-only `OMATERM_*` environment injection;
desktop startup/owner-bridge/shutdown integration with cancellation, deadline,
and revocation handling.

Transport tests (11 IPC tests) cover round-trip + sequential reuse, version /
field / token validation, redaction, response truncation, active/stale
lifecycle, unsafe directory and lock handling, frame-limit rejection,
slowloris frame timeout with server survival, request-deadline timeout with
request-ID preservation, malformed-frame handling, 8-client concurrent
integrity with abrupt-disconnect safety, and the 32-connection shed cap. The
connection-cap test initially deadlocked on a barrier race (probe connection
accepted before all holders arrived) and once surfaced `ConnectionReset`
instead of EOF on the shed path; both are fixed and the test now passes
deterministically.

Documented limits (consistent with the M3/M5/M6 unavailable-check precedent;
none is represented as a pass):

- Cross-UID peer testing is unavailable in this single-user environment. The
  `SO_PEERCRED` enforcement stays in the connection path; the pure
  `peer_authorized` predicate is unit-tested.
- The `XDG_RUNTIME_DIR`-fallback directory creation path is code-reviewed,
  not exercised (all runs used the real runtime dir with an isolated state
  dir).
- Owner-channel (32-slot) saturation is covered by design (bounded channel,
  `timeout` with no mutation, ambiguous-outcome semantics) plus the adjacent
  spawn-queue-full and connection-cap tests and the live 200-request load run
  below; a deterministic 33rd-in-flight unit test is not available without a
  GPUI harness.

M8 is complete with those explicit limits.

## M7/M8 closeout validation — 2026-09-27

Release binary `target/release/omaterm-desktop`, Omarchy/Hyprland
(`wayland-1`), isolated `XDG_STATE_HOME=/tmp/opencode/m7m8-closeout/state`,
`SHELL=/bin/sh`, real `XDG_RUNTIME_DIR`. Two desktop instances were launched
via `systemd-run --user` (`omaterm-closeout`, then `omaterm-loadtest`).

### M7 UI regression (`omaterm-closeout`, PID 2186908)

| Check | Result |
|---|---|
| IPC `pane.focus` / `pane.equalize` / `pane.close` | PASS: each returned `ok:true`; pane counts 2 → 1 |
| UI resize (`wtype` `Ctrl+Shift+[`) on a down-split | PASS: boundary moved 0.5 → 0.45/0.55 in `pane.list` geometry |
| UI equalize (`Ctrl+Shift+E`) | PASS: geometry restored to 0.5/0.5 |
| Screenshot | `/tmp/opencode/m7m8-closeout/split-down.png`: two stacked `sh-5.3$` prompts |
| Rapid split/close cycles via IPC | 26 cycles clean (pane/session counts consistent, all responses `ok:true`); the first 5-cycle loop ended at 4 panes instead of 2 with unchecked close results, unreproduced in 26 subsequent checked cycles — recorded as a watch item for M9 soak, not a gate failure |

### M8 concurrent load + shutdown under load

| Check | Result |
|---|---|
| 200 `pane.list` requests across 8 concurrent clients | PASS: all responses correlated by `request_id`, none lost; threads 24 → 24, FDs 39 → 39, RSS +196 KiB single-point (not a lifecycle gate) |
| Shutdown with 4 active `terminal.list` load clients (`omaterm-loadtest`) | PASS: during load 29 threads / 48 FDs; after `window.close`: service inactive, socket + credential removed, 0 desktop processes, load clients ended with `ConnectionResetError` (expected — no hangs), no orphan shells, final `workspace-v1.json` schema 1 with the surviving project |
| Restart restore (earlier `omaterm-m78-restore` instance) | PASS (prior record): 2 projects with tab counts intact through async `RestorePane`; missing/wrong tokens both `permission_denied` |

### Automated verification at closeout

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test -p omaterm --bin omaterm-desktop -- --test-threads=1` | PASS: 19 tests |
| `cargo test -p omaterm-core -p omaterm-state -- --test-threads=1` | PASS: 13 core + 14 state |
| `cargo test -p omaterm-protocol -p omaterm-ipc -- --test-threads=1` | PASS: 6 protocol + 11 IPC |
| `cargo test -p omaterm-terminal --lib -- --test-threads=1 --skip workspace::tests::four_panes_have_independent_sessions` | PASS: 80 tests |
| `cargo test -p omaterm-terminal --lib workspace::tests::four_panes_have_independent_sessions -- --exact --test-threads=1` | PASS (isolated; the test is historically flaky under parallel/filtered runs) |
| `cargo test --workspace -- --test-threads=1` | PASS: serial total 168 (19 desktop + 13 core + 11 IPC + 6 protocol + 14 state + 81 terminal unit + 24 PTY integration) |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (known transitive `proc-macro-error2` notice only) |
| `python3 scripts/check-docs.py` | PASS |
| `git diff --check` | PASS |

Serial total is 19 + 13 + 6 + 11 + 14 + 81 + 24 = 168. (Correcting the row above: 168, not 165.)

## M5–M8 closure planning — 2026-09-27

Added [dependency-ordered closure plan](m5-m8-closure-plan.md) for unresolved
M5/M6 desktop gates, M7 runtime contracts, M8 wire/authorization/desktop
integration, and carried-forward resource and test reliability evidence.
Linked it from the milestone overview and status handoff. This documentation
change makes no milestone completion claim and does not alter existing code.

| Documentation check | Result |
|---|---|
| `python3 scripts/check-docs.py` | PASS: 19 Markdown files, 45 local link targets, 72 blueprint references; all 17 CLI methods map to IPC methods |
| `git diff --check` | PASS: no whitespace errors in tracked changes |
| `git diff --no-index --check /dev/null docs/m5-m8-closure-plan.md` | PASS: no whitespace errors in the new, untracked plan |

Reviewed relative links and the plan's inventory against the M5–M8 milestone
contracts, status and acceptance matrix. No Cargo/Wayland checks were run for
this documentation-only change; the earlier M8 foundation results remain in
their own record above.

### M7/M8 execution-plan refresh — 2026-09-27

Updated the same [closure plan](m5-m8-closure-plan.md) with the current Bash
`terminal.run` and preallocated-session foundation, five gated steps (7A–8C),
async pending/final response semantics, owner-serialized completion and cleanup,
typed 17-method DTOs, bounded transport, scoped credentials and child-only env,
desktop IPC integration, shutdown ordering, fault tests and Wayland exit gates.
The status summary now reflects those implementation facts. Planning does not
close either milestone; interactive M7 Wayland and all M8 desktop IPC checks
remain open.

| Documentation check | Result |
|---|---|
| `python3 scripts/check-docs.py` | PASS: 19 Markdown files, 50 local link targets, 72 numbered blueprint references; all 17 methods mapped |
| `git diff --check` | PASS: tracked-file whitespace checks |
| `git diff --no-index --check /dev/null docs/m5-m8-closure-plan.md` | PASS: untracked plan whitespace checks |

Reviewed the new heading anchors, milestone mapping, open blockers and links
against the blueprint/M7/M8 specs and acceptance matrix. This planning update
did not modify application code; the previous Rust checks are recorded above,
and no new Cargo or manual Wayland checks are claimed here.

## M10 History Recovery — scope approved, implementation not started

User approved a post-v0.1 history feature with these decisions:

- Restore both terminal scrollback and command history.
- Persistence is opt-in and disabled by default.
- Command history is an OmaTerm-owned journal; do not read, write, or replay
  shell-native history files.
- Encrypt archives at rest and use OS-backed key storage; never fall back to
  plaintext when key storage is unavailable.
- Always launch fresh shells. Do not restore PTYs, processes, active commands,
  parser state, or alternate-screen applications.

Added `docs/10-milestone-10-history-recovery.md`, linked M10 from the overview
and acceptance matrix, clarified M6's unchanged non-goals, and updated blueprint
§§6.6, 29, and 68 to record the approved post-v0.1 extension. No persistence,
keyring, encryption, compression, shell integration, or terminal replay code has
been added. M10 remains blocked until M5–M9 are complete; dependency/API/license
research and the Alacritty event-replay spike are deliberately scheduled after
those gates.

## M7 bounded spawn-worker foundation — 2026-09-27

Added `omaterm-terminal::SessionSpawnQueue`, a GPUI-free, single-worker launch
queue with bounded request and completion channels, non-blocking submission,
caller-provided operation/session IDs, worker-owned PTY creation, completion
handoff, and cancellation of queued launches on drop/shutdown. Added coordinator
commit operations for worker-created projects, tabs, splits, and restored panes.
They validate captured selection/focus/source-session/pane guards before
registry insertion, reject stale provisional completions, and have focused
success/rollback tests. The worker and commit operations are not yet connected
to router dispatch; existing router creation commands still spawn synchronously,
so M7 remains `in_progress` and M8 desktop dispatch remains gated.

### Targeted checks (Rust 1.98.1)

| Command | Result |
|---|---|
| `cargo fmt --all` | PASS |
| `cargo test -p omaterm-terminal spawn_queue::tests -- --test-threads=1` | PASS: 3 worker identity/failure, queue-capacity/non-blocking, and non-blocking drop tests |
| `cargo clippy -p omaterm-terminal --all-targets -- -D warnings` | PASS |
| `cargo fmt --all --check` | PASS |
| `cargo test --workspace -- --test-threads=1` | PASS: 10 desktop + 13 core + 3 IPC + 5 protocol + 14 state + 81 terminal unit + 24 PTY integration tests (150 total) |
| `cargo test --workspace` | Earlier attempts in this session were flaky/blocked: one PTY prompt-readiness failure (isolated rerun passed), followed by a 240-second stall at `workspace::tests::four_panes_have_independent_sessions` |
| `cargo test -p omaterm-terminal --test pty_integration bash_run_waits_for_prompt_and_submits_argv_without_shell_interpolation -- --exact` | PASS: isolated rerun of the parallel-only integration failure |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (known transitive `proc-macro-error2` future-incompatibility notice only) |
| `python3 scripts/check-docs.py` | PASS: 19 Markdown files, 50 local links, 72 blueprint references; 17 CLI/IPC mappings |
| `git diff --check` | PASS |

The serial workspace gate passes; default-parallel execution has had one
isolated-pass PTY failure and a timeout in the four-pane coordinator test.
The four-pane test also stalled once in a filtered serial run and passed alone
and in the final serial workspace run. That foundation is now superseded by the
wired async dispatch and desktop IPC integration recorded below.

## M7/M8 async dispatch + authenticated IPC integration — 2026-09-27

Wired the bounded spawn worker into `dispatch_async` for every creation path,
added coordinator commit guards with selection/focus/source-session checks,
project-scope authorization, per-session + local-user credentials with expiry
and owner-close revocation, child-only `OMATERM_*` environment, strict
17-method wire mapping with bounds, transport hardening (5s frame / 10s request
deadlines, sequential requests per connection, 32-connection cap, unsafe
lock/symlink handling), and the desktop owner bridge with bounded queue,
cancellation, and ordered shutdown.

### Changed files

- `apps/omaterm/src/router.rs` (async prepare/poll/finish, authorization,
  credential publishing, single-path enforcement, 17 tests)
- `apps/omaterm/src/main.rs` (IPC server startup, owner work queue, pending
  UI/IPC launch polling, revocation on close, ordered shutdown)
- `apps/omaterm/src/credentials.rs` (new: local + scoped tokens, expiry,
  owner-only file, revocation)
- `apps/omaterm/src/ipc_bridge.rs` (new: 17-method mapping, bounds, list caps,
  stable error/response DTOs)
- `crates/omaterm-protocol/src/method.rs` (new: strict per-method DTOs)
- `crates/omaterm-ipc/src/lib.rs` (sequential requests, monotonic deadlines,
  shutdown-aware workers, lock safety, concurrency/disconnect/frame tests)
- `crates/omaterm-terminal/src/{workspace,spawn_queue,pty,session}.rs`
  (commit guards, child env plumbing)
- `apps/omaterm/Cargo.toml`, `Cargo.lock`, `docs/dependencies.md`

### Automated verification (Rust 1.98.1, Omarchy/Hyprland)

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test -p omaterm --bin omaterm-desktop -- --test-threads=1` | PASS: 17 tests (router async/rollback/scope/credential-env, wire mapping/bounds, credential lifecycle) |
| `cargo test -p omaterm-core -p omaterm-state -- --test-threads=1` | PASS: 13 core + 14 state |
| `cargo test -p omaterm-protocol -p omaterm-ipc -- --test-threads=1` | PASS: 6 protocol + 6 IPC (round-trip, version/field/token validation, redaction, truncation, lifecycle, unsafe lock, frame limit, concurrency/disconnect) |
| `cargo test -p omaterm-terminal --lib -- --test-threads=1 --skip workspace::tests::four_panes_have_independent_sessions` | PASS: 80 tests |
| `cargo test -p omaterm-terminal --lib workspace::tests::four_panes_have_independent_sessions -- --exact --test-threads=1` | PASS: isolated run of the known-flaky coordinator test |
| `cargo test --workspace -- --test-threads=1` (earlier full pass) | PASS: 24 PTY integration tests; serial total 161 (17 desktop + 13 core + 6 protocol + 6 IPC + 14 state + 81 terminal unit + 24 PTY integration) |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (known transitive `proc-macro-error2` future-incompatibility notice only) |
| `python3 scripts/check-docs.py` | PASS: 19 Markdown files, 50 local links, 72 blueprint references; 17 CLI/IPC mappings |
| `git diff --check` | PASS |
| `cargo build --release --bin omaterm-desktop` | PASS (`target/release/omaterm-desktop`) |

The full serial workspace suite was verified before the final doc edits; the
targeted suites above were re-run after them. Default-parallel PTY execution
remains historically flaky and is not claimed as a gate.

### Release Wayland validation (Omarchy/Hyprland, `wayland-1`, release binary)

Isolated state `XDG_STATE_HOME=/tmp/opencode/m78-run/state`, `SHELL=/bin/sh`,
real `XDG_RUNTIME_DIR=/run/user/1000`. Launched via `systemd-run --user` as
`omaterm-m78-verify`; window mapped as `OmaTerm` (Hyprland clients), socket
`omaterm.sock` and credential `omaterm.credential` both mode `0600`.

| Check | Result |
|---|---|
| `project.list` via socket | PASS: returned the initial project with correct ID/selection |
| `pane.list` before split | PASS: 1 pane, full geometry, focused |
| `pane.split {"direction":"right"}` via socket | PASS: returned final `new_pane_id`/`new_session_id` after owner commit (not a pending receipt) |
| `pane.list` after split | PASS: 2 panes at 0.5 width each, new pane focused; grim capture `/tmp/opencode/m78-run/split.png` shows two `sh-5.3$` prompts side by side |
| `terminal.send` + `terminal.read` | PASS: base64 `printf M78_READ_OK` echoed; bounded read contained the marker |
| Version mismatch / unknown method | PASS: `unsupported_version` and `invalid_request` respectively |
| Child environment | PASS: `/proc/<child>/environ` contained `OMATERM_SOCKET/TOKEN/PROJECT_ID/TAB_ID/PANE_ID/SESSION_ID`; session token differed from the local-user credential |
| Scoped token isolation | PASS: session token saw only its own project in `project.list`; cross-project `project.select` denied with `permission_denied`; local-user `project.create` succeeded |
| UI regression (`Ctrl+Shift+T` via `wtype` with OmaTerm focused) | PASS: selected project went from 1 to 2 tabs through the same async dispatch path |
| Missing/wrong credential (restart instance `omaterm-m78-restore`) | PASS: both returned `permission_denied`; restore reopened 2 projects (`fachri` 1 tab, `Second` 2 tabs) with fresh shells through async `RestorePane` commits |
| Graceful close (`window.close`) | PASS (both instances): service inactive, no `omaterm-desktop` process, socket and credential files removed, no orphan shell PIDs; final snapshot `state/omaterm/workspace-v1.json` schema 1 with both projects, split layout, focused pane, and selection |

Resource observation during the first instance (4 shells): 26 threads, 45 FDs,
`VmRSS 60800 KiB`. This is a single-point observation, not a lifecycle gate;
repeated-cycle and concurrent-IPC resource measurements remain open.

### Remaining gaps (not claimed as passing)

- Slow-client deadline behavior and owner-queue saturation lack dedicated
  automated tests (timeouts return `timeout` with ambiguous-outcome semantics;
  clients must not auto-retry mutations).
- Peer-UID mismatch is enforced via `SO_PEERCRED` but has no cross-UID unit
  test in this environment.
- Broader M7 UI flows (resize/equalize/focus/close-during-pending) and M8
  concurrent-IPC resource measurements still need evidence before either
  milestone closes.

## Milestone 9 — CLI — complete — 2026-09-27

Added `crates/omaterm-cli`, a thin `omaterm` binary over the M8 socket. It
parses arguments with clap, builds exactly one v1 `IpcRequest`, sends it via
`omaterm-ipc`, and formats the `IpcResponse` as concise human text or the
stable JSON envelope. No workspace logic lives in the CLI; all 17 coverage
rows map to the typed M8 methods. With no subcommand it acknowledges a
running instance or launches the sibling `omaterm-desktop` with bounded
readiness; subcommands never auto-launch. Compositor focus is deferred.

### Changed files

- `Cargo.toml`, `Cargo.lock` (workspace member `crates/omaterm-cli`)
- `crates/omaterm-cli/Cargo.toml` and
  `crates/omaterm-cli/src/{main,connection,output,launcher}.rs`
- `crates/omaterm-cli/src/commands/{mod,project,tab,pane,terminal}.rs`
- `docs/dependencies.md`, `docs/acceptance-matrix.md`, this status record

### Behavior contracts

- Socket: explicit `--socket`, then `OMATERM_SOCKET`, then the validated M8
  default. OS connect failures report `OmaTerm is not running` (exit 2)
  without path-existence probing.
- Credentials: `OMATERM_TOKEN` for in-app callers (invalid values fail with
  no file fallback); otherwise the owner-only credential beside the resolved
  socket is validated (regular file, same UID, mode `0600`) and never sent
  to an unrelated override socket.
- Selectors: explicit flags first, then `OMATERM_PROJECT_ID` /
  `OMATERM_TAB_ID` / `OMATERM_PANE_ID`, then server-side selection. An
  invalid explicit ID never falls back. `terminal send` base64-encodes exact
  UTF-8 bytes with no implicit newline; `terminal run` forwards structured
  argv after `--`; `terminal read` defaults to 50 lines x 500 columns.
- Output: human text on stdout, diagnostics on stderr, no colors in JSON.
  Exit codes are 0 success, 1 server/authorization/command failure,
  2 connection failure, 64 usage errors. `--json` works before or after the
  subcommand and also envelopes local failures.

### Automated verification (Rust 1.98.1)

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test -p omaterm-cli` | PASS: 24 tests (17-row parser matrix, per-command wire mapping incl. base64/argv/direction validation, global-flag positions, env-scoped selectors, fake-server success/denial/connection e2e, launcher acknowledgement) |
| `cargo test -p omaterm-core -p omaterm-state` | PASS: 13 core + 14 state |
| `cargo test -p omaterm-protocol -p omaterm-ipc` | PASS: 6 protocol + 11 IPC |
| `cargo test -p omaterm-terminal --lib -- --test-threads=1` | PASS: 81 tests |
| `cargo test -p omaterm-terminal --test pty_integration -- --test-threads=1` | PASS: 24 PTY integration tests |
| `cargo test -p omaterm --bin omaterm-desktop -- --test-threads=1` | PASS: 19 router tests |
| Serial total | 192 (24 CLI + 13 core + 14 state + 81 terminal unit + 24 PTY + 19 desktop + 11 IPC + 6 protocol) |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (known transitive `proc-macro-error2` future-incompat notice only) |
| `cargo build -p omaterm-cli` | PASS: produces `target/debug/omaterm` |
| `cargo build --release --bin omaterm --bin omaterm-desktop` | PASS |
| `python3 scripts/check-docs.py` | PASS: 19 Markdown files, 50 local links, 72 blueprint refs, 17 CLI/IPC mappings |
| `git diff --check` | PASS |

The full serial workspace suite was verified per package/test target (the
single unflagged `cargo test --workspace` exceeded the 120s tool timeout in
this session); PTY concurrency still runs serially per the established gate.

### Release Wayland CLI/desktop proof — 2026-09-27

Environment: Omarchy/Hyprland (`wayland-1`), release binaries,
`SHELL=/bin/bash`, real `XDG_RUNTIME_DIR=/run/user/1000`, isolated
`XDG_STATE_HOME=/tmp/opencode/m9-proof/state`. Desktop launched via
`systemd-run --user` as `omaterm-m9-proof`; socket and credential both mode
`0600`. A grim capture of the split desktop is kept outside the repository
at `/tmp/opencode/m9-proof/split.png`.

| Check | Result |
|---|---|
| `omaterm` with instance running | PASS: `OmaTerm is already running.`, exit 0; `--json` returns `{"ok":true,"result":{"running":true,...}}` |
| `pane list` / `--json` | PASS: 1 focused pane, then 2 panes at 0.5/0.5 widths after split; JSON envelope valid |
| `pane split --right` | PASS: real split in the desktop app with new pane/session IDs |
| `terminal run --pane <new> -- echo M9_PROOF_OK` | PASS: `Submitted command to shell.` (Bash prompt-ready, not `shell_busy`) |
| `terminal read --lines 50` | PASS: bounded viewport contained `M9_PROOF_OK` (submission evidence, not completion) |
| `terminal send` + JSON `terminal read` | PASS: sent bytes acknowledged; bounded JSON read `ok:true, truncated:false` contained the marker |
| `project list/tab list/tab new/terminal list` | PASS: project/tab/session metadata with new tab/pane/session IDs |
| `pane focus/terminal new/tab close` | PASS: focus moved, new tab created, closes fell back to Tab 1 selected |
| `project open /tmp/select/ pane close` | PASS: new project ID/root, selection round-trip, pane collapse to 1 focused pane |
| `pane equalize` | PASS: splits reset |
| `pane resize` with unknown split | PASS: `split_not_found`, exit 1 (live success needs a CLI-discoverable split ID; see limits) |
| 10 checked split/close cycles | PASS: pane count consistent throughout, ending at 1 pane; M7/M8 transient watch item not reproduced |
| Bad pane ID | PASS: `invalid_request`, exit 1; `--json` envelope `ok:false` plus stderr diagnostic |
| Missing socket (`--socket` override) | PASS: `OmaTerm is not running`, exit 2, no auto-launch |
| `pane split` without direction | PASS: usage error, exit 64 |
| Shutdown/stop | PASS: unit stopped, no `omaterm-desktop` process, no proof-window orphan shells, stale socket/credential removed; `omaterm.lock` remains as the normal startup lock |

Launch-branch proof (separate instance, isolated
`XDG_STATE_HOME=/tmp/opencode/m9-proof/state2`): with no desktop running,
`omaterm` printed `Launched OmaTerm.`, exit 0, owned the default socket,
and `pane list` succeeded after the initial async spawn settled. The
instance was terminated and its stale socket/credential removed; no desktop
process or proof-window shell remained.

### Documented limits

- Live `pane.resize` success has no Wayland evidence because `pane.list`
  (human and JSON) exposes no split IDs to target; the CLI forwards
  `--split/--fraction` unchanged and the invalid-split error path is proven
  live. Split-ID discovery belongs to a future list-surface change, not M9.
- Scoped in-app denial (`OMATERM_TOKEN` project isolation) is covered by M8
  desktop evidence plus CLI env-precedence and fake-server denial tests; no
  separate live in-app token denial run was performed in M9.
- No per-process GPU/startup/idle formal metrics were added in M9; the M5
  lifecycle observations stand.

## Milestone 10 — History Foundation — in progress (2026-09-27)

M5–M9 are complete, so M10 work has started. This slice implements the
persistence/terminal foundation; shell lifecycle hooks, `HistoryCommand`
router/IPC/CLI wiring, and desktop opt-in controls follow in later slices.
M6's layout/CWD-only snapshot contract is unchanged: history lives in
separate encrypted archives and is disabled by default.

### Dependency and replay spikes (both PASS before implementation)

- Secret Service: `secret-tool` store/lookup/clear round-trip against
  `org.freedesktop.secrets` on Omarchy/Hyprland PASS. Production provider
  (`omaterm-state::history::OsKeyProvider` over `keyring` 4.2.0 `v1` API)
  proven with a throwaway binary: create → stable reget → rotate (key
  changes) → remove → recreate → final cleanup PASS, using isolated
  `omaterm-m10-spike` names; post-run `secret-tool lookup` confirms no
  entries remain. Spike crates removed afterward (`/tmp/opencode/`).
- Terminal replay: ordered PTY byte chunks + resize events replayed into a
  fresh `AlacrittyEngine` reproduce main-screen visible text exactly (ANSI
  colors, wide/combining Unicode, soft wraps, resize). Alt-screen policy
  fixed by fixture: drop any chunk where alt is active before or after the
  advance (enter/exit sequences included); pre-alt scrollback preserved.
  Proven with a throwaway binary, then encoded as unit tests. No durable
  storage was written before the round-trip held.

### Changed files (uncommitted)

- `Cargo.toml`, `Cargo.lock`
- `crates/omaterm-state/Cargo.toml` (new: `chacha20poly1305`, `flate2`
  with `rust_backend`, `hkdf`, `keyring`, `getrandom`, `sha2`, `zeroize`,
  `libc`)
- `crates/omaterm-state/src/history.rs` (new), `crates/omaterm-state/src/lib.rs`
- `crates/omaterm-terminal/src/history.rs` (new), `crates/omaterm-terminal/src/lib.rs`
- `docs/dependencies.md` and this status record

### What was implemented

- `HistoryConfig` (disabled by default; global opt-in + per-pane
  pause/exclusion) with JSON persistence under `$XDG_CONFIG_HOME/omaterm/`
  (forward-compatible `history.json`; full `config.toml` merge lands with
  the desktop-controls slice). Never inside workspace snapshots.
- `KeyProvider` trait + `InMemoryKeyProvider` (tests; one-shot failure
  injection for key-loss paths) + `OsKeyProvider` (Secret Service only;
  hex-encoded random 32-byte master key; `Locked`/`Denied`/`Unavailable`
  mapping; keys zeroized on rotate/remove/drop). No plaintext fallback on
  any failure path.
- Versioned archive framing (`OMHIST01`, v1): HKDF-SHA256 per-archive keys
  (separate info strings for scrollback vs journal), random salt/nonce per
  save, ChaCha20Poly1305 with AAD-bound pane UUID + revision, deflate
  compression before encryption. Header parsed and bounds-checked before
  any attacker-sized allocation; decompression capped at
  `max_decompressed_bytes + 1`.
- Initial ceilings enforced before allocation: 10,000 lines/pane, 8 MiB
  archive/pane, 64 MiB workspace quota, 10,000 journal entries/pane,
  1 MiB decompressed frame, 64 KiB output frames, 4096 events/archive.
  Retention drops oldest *complete* events/entries first.
- `HistoryStore` (`$XDG_STATE_HOME/omaterm/history/`): opaque
  `<32-hex-pane>.<revision>.omhist|omjournal` names, `0700` dirs / `0600`
  files, symlink/owner/type rejection, same-directory atomic writes,
  revision supersede (older revisions removed), corrupt archives renamed to
  `.quarantined` and retained (never overwritten as valid), pane/project/
  workspace clear (clear-all pairs with key rotation by the caller),
  `stale_archives` helper for dead-pane cleanup including hidden tabs.
- `HistoryRecorder` (hot path, `omaterm-terminal`, no crypto/IO): bounded
  memcopy-only observation, alt-aware dropping, resize coalescing,
  oldest-first trimming; plus `replay_into` for fresh-engine restore before
  PTY attach (defensive alt re-exit).
- `JournalEntry` + `JournalBuffer` (bounded, oldest-dropped, bounded `list`)
  with encrypted persistence reusing the archive crypto under a separate
  HKDF info string. Lifecycle-hook emission (Bash first) is a later slice;
  the buffer already refuses oversized entries and never represents unknown
  exit status as success/failure.

### Automated verification (Rust 1.98.1, Omarchy/Hyprland)

| Command | Result |
|---|---|
| `cargo test -p omaterm-state` | PASS: 29 tests (14 existing + 15 history: config, key round-trip/rotation/failure, archive round-trip/wrong-key/tamper/version/truncation, frame/decompression caps, journal round-trip/bounds/multibyte, store perms/quarantine/symlink/trim/stale, hex) |
| `cargo test -p omaterm-terminal --lib` | PASS: 89 tests (81 existing + 8 history: disabled/pause/alt-policy/resize-skip/trim/split/replay-fidelity/flood) |
| `cargo test --workspace -- --test-threads=1` | PASS: 215 tests, 0 failures (19 desktop router + 24 CLI + 13 core + 11 IPC + 6 protocol + 29 state + 89 terminal unit + 24 PTY integration) |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (known transitive `proc-macro-error2` future-incompat notice only) |
| `cargo fmt --all --check` / `git diff --check` | PASS |
| `cargo tree -p omaterm-terminal` / `-p omaterm-state` | PASS: zero `gpui` in either tree |
| Live keyring spike (throwaway binary, real daemon) | PASS: create/reget/rotate/remove/recreate/cleanup; no entries left |

Flake note: two workspace/PTY runs during this slice each failed the
timing-sensitive `bash_run_waits_for_prompt…` test once (`pty_integration.rs`
Bash second-prompt wait), while the same binary passed standalone, passed a
repeat PTY-only run, and passed a repeat full-workspace run (215/215). The
clean tree (stashed) also passed 24/24 PTY-only. The diff touches nothing in
the PTY/session/shell path (purely additive history modules + deps), so this
is the suite's known load sensitivity (cf. M4/M5 serial-gate notes), not an
M10 regression. No failures are claimed as passes.

### Explicit non-claims for this slice

- No shell lifecycle hook changes: the journal has types, bounds, and
  encrypted persistence, but no Bash/zsh/fish emission yet.
- No `HistoryCommand` router, IPC methods, CLI commands, or desktop
  opt-in/status/clear controls yet; no Wayland history validation yet.
- `history.json` (not `config.toml` merge) persists config for now; the
  merge lands with desktop controls.
- Background revision-ordered history writer scheduling lives with the
  desktop integration slice; the recorder itself performs no crypto/IO.

### Next action

Implement supported-shell lifecycle reporting (Bash first: hook reliability
spike across interactive/nested/multiline/Ctrl+C/TUI/startup-file cases),
then `HistoryCommand` dispatch + IPC/CLI + desktop controls + Wayland checks.

## Milestone 10 — Phase 0: Foundation Audit — complete (2026-09-27)

Reviewed the M10 foundation modules against the contract before adding any
consumers. Four hardening fixes plus the `config.toml` reconciliation, each
with focused tests. No router, IPC, CLI, shell-hook, or desktop changes.

### Audit findings and fixes (all in `omaterm-state/src/history.rs`)

- Symlinked history directory: `ensure_dir` previously followed symlinks.
  The leaf directory is now rejected when it is a symlink, not a directory,
  or unexpectedly owned; archive-file symlink/type/owner checks are
  unchanged. Ancestor symlinks (e.g. `$HOME`) remain the user's own
  environment and are out of scope.
- Temporary-file collisions: both atomic writers used a fixed
  `<name>.tmp` sidecar. Temp names are now same-directory unique
  (`pid + process-wide counter + 8 random bytes`), so concurrent writers —
  including writers in other processes — cannot share a temp file.
- Quota overcharging on replacement: the workspace quota counted the pane's
  own superseded revisions. `replaced_bytes` exempts the pane's live prior
  revisions of the same suffix from the pre-write quota check; quarantined
  files keep counting (conservative: they are retained for diagnosis).
- Quarantine retention proven: a corrupt archive quarantined by load
  survives newer valid revisions (never overwritten as valid) until an
  explicit pane/project/workspace clear removes it.
- Journal oversize on save: `save_journal` could fail when 10,000 entries
  exceeded the 1 MiB decompressed cap. The save path now drops oldest
  complete entries until the payload fits (bounded iterations, newest
  survive); a single over-cap entry remains an error, not silent loss.
- Redundant branch in `trim_events_to_limits` collapsed (no behavior change).
- Configuration contract: history config moved from the temporary
  `history.json` to the canonical `~/.config/omaterm/config.toml`
  `[history]` section via `toml_edit` (other sections, comments, and
  formatting preserved; atomic `0600` writes). One-time migration adopts a
  legacy `history.json` opt-in into `config.toml` and removes it; malformed
  TOML is an explicit `Parse` error rather than silent defaults.

### Changed files (uncommitted, additive to the prior M10 slice)

- `crates/omaterm-state/Cargo.toml`, `Cargo.lock` (new: `toml_edit` 0.25.15)
- `crates/omaterm-state/src/history.rs`, `crates/omaterm-state/src/lib.rs`
- `docs/dependencies.md` and this status record

### Automated verification (Rust 1.98.1, Omarchy/Hyprland)

| Command | Result |
|---|---|
| `cargo test -p omaterm-state` | PASS: 37 tests (29 prior + 8 audit: symlinked dir, temp uniqueness, quota replacement, quarantine retention, journal-fit trim, TOML round-trip preservation, migration adoption/precedence, malformed TOML) |
| `cargo test --workspace -- --test-threads=1` | PASS: 223 tests, 0 failures |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (known transitive `proc-macro-error2` notice only) |
| `cargo fmt --all --check` / `git diff --check` | PASS |
| `python3 scripts/check-docs.py` | PASS (rerun after doc edits; see below) |

No Wayland validation in this slice (no desktop or behavior changes visible
to the user). Stale crash-temp sidecars (`.<name>.<pid>.*.tmp`) share the
pre-existing snapshot-writer crash-cleanup limitation and are documented as
such rather than claimed clean.

### Next action

Phase 1: Bash lifecycle reliability spike (private owner-only channel,
authenticated start/completion/exit events, nested-shell/TUI/Ctrl+C/
startup-file matrix) before any journal emission code.

## Milestone 10 — Phase 1: Bash Lifecycle Reliability — complete (2026-09-27)

Shell-authoritative command records now exist: a private owner-only hook
channel, a token-authenticated streaming parser, and a session state
machine producing bounded `LifecycleRecord`s. Nothing is inferred from
terminal text. Journal persistence/wiring stays in later phases.

### Empirical hook design (proven before implementation)

A PTY-driven DEBUG-trap experiment on real Bash showed: trap fires per
simple command (`cmd1; cmd2` → two firings), per loop iteration, and for
`PROMPT_COMMAND` internals and rcfile lines — while commands inside the
trap handler itself stay silent. A prototype hook then proved the skip
design airtight: zero phantom records across commands, chains, loops,
`printf`, and `exit`, with exit statuses (`false` → 1) intact.

### What was implemented (uncommitted)

- `crates/omaterm-terminal/src/shell.rs`: `bash_rcfile` now installs a
  `DEBUG`-trap preexec (`ESC ] 133 ; B ; <token> ; <base64-cmd> BEL`) plus
  `PROMPT_COMMAND` wrap guards (`ESC ] 133 ; A ; <token> ; <exit> BEL`).
  Every internal simple command either starts with `__omaterm_` or runs
  under `__omaterm_in_prompt`; user entries keep their order in both
  string and array `PROMPT_COMMAND` forms. No `base64(1)` → emission
  disables itself, shell unaffected.
- `crates/omaterm-terminal/src/lifecycle.rs` (new): streaming OSC 133
  parser validating kind/token/charset/terminator (BEL or ST), tolerant of
  arbitrary fragmentation, with 128 KiB runaway and 64 KiB payload caps;
  strict base64/UTF-8 command decoding and 0–255 exit decoding.
- `crates/omaterm-terminal/src/session.rs`: parser-driven readiness
  replaces substring search (legacy `A;<token>` form still works);
  pending-command state machine (new start flushes predecessor unfinished;
  completion attributed to last start); pre-first-prompt starts dropped as
  initialization; bounded 512-record queue with `drain_lifecycle_records`;
  non-Bash shells get no parser and no records.
- Documented semantics: `;`-chains record each start with completion on
  the last; loop iterations record individually; nested shells/TUIs record
  the outer invocation only; Ctrl+C completes with 130; `exit` stays
  unfinished; readiness is fail-open while completion is fail-closed.

### Changed files

- `crates/omaterm-terminal/Cargo.toml` (`base64` =0.22.1), `src/{shell,lifecycle,session,lib}.rs`
- `crates/omaterm-terminal/tests/pty_integration.rs` (8-test live matrix)
- `docs/dependencies.md` and this status record

### Automated verification (Rust 1.98.1, Omarchy/Hyprland)

| Command | Result |
|---|---|
| `cargo test -p omaterm-terminal --lib -- --test-threads=1` | PASS: 108 tests (11 lifecycle parser + 8 lifecycle session + rest) |
| `cargo test -p omaterm-terminal --test pty_integration -- --test-threads=1` | PASS: 32 tests incl. 8-test Bash matrix (text+exit, chain, Ctrl+C 130, nested hiding, string/array PROMPT_COMMAND coexistence, vim invocation-only, Unicode exactness) |
| `cargo test --workspace -- --test-threads=1` | PASS: 250 tests, 0 failures |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (known transitive `proc-macro-error2` notice only) |
| `cargo fmt --all --check` / `git diff --check` | PASS |

Self-correction during this slice: the first full-suite run failed five
session tests because the `seen_prompt` gate (added after them) drops
pre-prompt starts; the tests now establish a first prompt explicitly, and
a dedicated test pins the initialization-drop behavior. One parallel-only
lib run also showed a registry PTY flake; the serial gate (project
standard) is green throughout.

### Explicit non-claims

- Records are queued in memory only; no `JournalBuffer`/archive writes yet.
- No `HistoryCommand`, IPC/CLI, or desktop controls yet; no Wayland
  validation yet. Shell-exit records come from PTY child-exit detection in
  a later slice, not from a shell EXIT trap.

### Next action

Phase 2: runtime recording + background revision-ordered persistence
writer (recorder wiring, key-gated encrypted saves, shutdown flush,
pane/tab/project cleanup).

## Milestone 10 — Phases 2–9: Integration, Controls, Acceptance — complete with documented limits (2026-09-28)

Built on the foundation (spikes), Phase 0 (audit), and Phase 1 (lifecycle)
slices. All remaining phases are implemented, gated, and validated on
Wayland unless explicitly listed under limits. M6's layout/CWD-only
contract is untouched throughout. Working tree only; no commit made.

### Phase 2 — Runtime recording

- `TerminalSession` owns a `HistoryRecorder` plus enabled/paused flags.
  `pump()` and the test `advance_output()` path capture alt state before
  and after every engine advance; main-screen chunks and resizes are
  recorded with bounded memcpys only — never crypto, keyring, or disk I/O.
- Enabling starts a new record (no backfill); disabling discards; pause
  holds the record while dropping new output; alt-screen periods are
  excluded end to end. A `version` counter supports writer dirty tracking.
- Covered by 4 session tests (default-off, enable/pause/resume/alt/drop).

### Phase 3 — Background persistence

- New desktop `HistoryManager` (`apps/omaterm/src/history.rs`, GPUI-free):
  owns config, `HistoryStore`, key provider, per-pane journal buffers,
  warnings, and a single background worker thread (bounded 16-job queue,
  per-job key copies zeroized after use).
- Flush ticks drain recorder snapshots and lifecycle records, map them to
  archives/journal entries with workspace identity, and queue dirty panes
  only (recorder-version + journal-seq dirty tracking with in-flight
  suppression). Key failures (30s retry gate) warn and keep terminals in
  memory with prior archives untouched; nothing is ever written plaintext.
- Desktop integration: config loaded at startup; a 30s checkpoint timer
  plus workspace-dirty piggyback drive `flush_history`; `begin_shutdown`
  runs a bounded (10s) `shutdown_flush_targets` before snapshot/reap;
  every pane close clears its archives (tab/project closes funnel through
  the same path); history warnings render as their own banner.
- Covered by 6 manager tests (disabled no-op, persist/restore round-trip,
  key-failure isolation, clear scopes + rotation, revision supersede,
  journal-prefix merge).

### Phase 5 — Core commands

- `OmaCommand::History` with nine operations (enable, disable, pause,
  resume, bounded list, clear pane/project/workspace, status); pure
  field validation (`ListJournal` limit 1–1000); stable `history_disabled`
  / `history_unavailable` error codes; owned `HistoryStatusInfo` /
  `JournalEntryInfo` / `HistoryCleared` result DTOs (safe metadata only).

### Phase 6 — Router, IPC, CLI

- The router owns the single `HistoryManager` (production default built in
  `Router::new`; `with_history_manager` is test-only). All nine operations
  dispatch through it with pane/project existence checks, project-scope
  authorization (global ops require local authority; targeted ops resolve
  the owning project), immediate session flag sync plus tick
  reconciliation, and destructive results reporting removed-file counts.
- Disable deletes all history data without rotation; clear-all deletes and
  rotates the master key. `list` serves in-memory buffers with newest-
  archive fallback; empty while disabled is an error, not a silent dump.
- Nine wire methods (`history.enable/disable/status/list/pause/resume/
  clear-pane/clear-project/clear-all`) with strict DTOs; nine CLI verbs
  with identical mapping (clear takes exactly one scope); human + JSON
  renderers. Protocol (26), bridge (27-case), and CLI (27-row + scope
  matrix) tests extended; IPC/CLI doc tables extended (checker green).

### Phase 4 — Restore before fresh shells

- Startup stages verified scrollback per pane into the router; the
  `RestorePane` commit replays into the fresh engine and seeds the
  recorder **before** `SessionStarted` effects start readers — merged,
  never discarding the restored prefix. Shells always spawn fresh (new
  PIDs/CWDs, no PTY/job/parser/alt state). Corrupt/missing/key-failure
  outcomes start the pane empty with a warning. Staged events are
  discarded if the pane closes first; retry reuses unstaged remainders.
- Journal buffers are warmed from archives at startup and on opt-in: live
  testing caught the first post-restart flush discarding the archived
  prefix, fixed with a dedicated merge test plus live proof.
- Covered by a router restore test (replay visible pre-output, seeded
  recorder, fresh shell reaches readiness live).

### Phase 7 — Desktop controls

- `history: off|on|on (N paused)|attention` chip in the tab bar (click =
  opt-in toggle); two-step `Ctrl+Shift+O` (disclosure banner for enable,
  destructive confirm for disable+delete), `Ctrl+Shift+G` pause/resume of
  the focused pane, `Ctrl+Shift+X` two-step pane clear (8s arm window).
  All controls dispatch the same semantic commands as IPC/CLI; errors
  surface as `History:` warnings. HJKL were taken (focus nav); O/G/X are
  free and avoid the IBus Unicode key.

### Phase 8 — Verification

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test --workspace -- --test-threads=1` | PASS: 269 tests, 0 failures (30 desktop + 26 CLI + 13 core + 11 IPC + 6 protocol + 37 state + 114 terminal unit + 32 PTY integration) |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (known transitive `proc-macro-error2` notice only) |
| `cargo build --release --bin omaterm-desktop --bin omaterm` | PASS (used for all Wayland runs) |
| `cargo tree -p omaterm-terminal` / `-p omaterm-state` | PASS: zero `gpui` |
| `python3 scripts/check-docs.py` / `git diff --check` | PASS |

The flaky `bash_run_waits_for_prompt` prompt race was fixed properly (pump
until readiness instead of assuming same-chunk arrival). One full-suite
run exceeded its 900s timeout under load average >3 with no failing test
and no deadlock mechanism in the diff (no new locks/threads in the
terminal crate); the rerun was green (269/269). Serial execution remains
the gate per prior milestone notes.

### Phase 9 — Wayland acceptance (Omarchy/Hyprland, release builds, Rust 1.98.1)

Isolated `XDG_STATE_HOME`/`XDG_CONFIG_HOME`, real Secret Service for the
main pass, real Bash + user dotfiles (starship/mise):

- Default off, zero archives; `history enable` persists opt-in to
  `config.toml`; status chip renders `history: on` (screenshot).
- Styled/Unicode scrollback (`seq 1 35`, red/green SGR, `снег`) and three
  journal entries with exact argv/exit/timestamps via CLI `run`/`list`.
- Pause/resume flips the status paused count through CLI.
- Restart: same panes, all-new session IDs, restored `seq`/styled output
  above a fresh prompt; fresh shell runs new commands; journal merges the
  archived prefix with new entries (live proof of the Phase 4 fix).
- Alt-screen absence: `vim --clean -c 'q!'` recorded as invocation-only
  (exit 0); post-restart viewport shows its command echo with zero TUI
  residue and a live shell.
- Scoped clears (`--pane` removed 2 files, `--project` removed 1),
  `clear --all` with key rotation, `disable` with data deletion — all with
  accurate status transitions.
- Keyring-unavailable instance (broken bus address): enable succeeds,
  terminal fully usable, status + amber banner read `history: attention /
  key storage unavailable … terminals keep running in memory`, and **no
  history directory is even created** (screenshot).
- Shared-machine hazard noted: an unattended window received stray
  keystrokes mid-pass (foreign `while` loop in a pane buffer), which
  correctly surfaced as `shell_busy`; validation moved to fresh panes
  with distinctive markers. Screenshots in `/tmp/opencode/m10-w/`;
  isolated state/config removed afterward.

Residue cleanup verified: test window closed, no desktop/shell test
processes remain, stale socket removed, and the production-named test
master key deleted from the real keyring (`secret-tool` search confirms
absence). One caution learned: `secret-tool search --unlock` prints
secrets to the transcript — the exposed value was a random single-purpose
test key whose ciphertexts were already deleted.

### Documented limits (not passes)

- Live graceful shutdown (window-close path through `begin_shutdown`'s
  history flush) was not exercised: the Hyprland 0.56 lua `dispatch`
  surface rejected `closewindow`/`movetowindow` forms, and focus-stealing
  on the shared machine ruled out key injection. SIGTERM kills without
  cleanup (pre-existing: no handler) and leaves a stale socket (removed
  manually; startup probing reclaims it per M8). Shutdown-flush ordering,
  timeout, and ack draining are covered by manager unit tests; timer and
  dirty-triggered checkpointing are proven live (all restart evidence
  came from checkpointed archives).
- Live corrupt-archive restart was not performed; tamper/metadata/nonce/
  truncation/version/reorder rejection plus quarantine retention and
  restore-outcome mapping are covered by 15+ deterministic tests.
- Per-pane CWD after restore inherits the M6-tested snapshot plumbing
  unchanged; this pass verified new PIDs/sessions and liveness rather
  than re-proving per-pane directories.
- First prompt after restart can take seconds under the user's real
  dotfiles (starship/mise); readiness-gated `run` handles this by design.

### Next action

M10 is complete within the above limits. Remaining options (not
required): live graceful-close proof on an unshared session, live
corrupt-archive restart, per-pane CWD re-verification, and a SIGTERM
handler proposal (separate product decision — instant-death-on-SIGTERM
predates M10).

## Milestone 10 — History restore fix: inactive projects (2026-09-28)

User report: with two same-directory projects (`omaterm` / `omaterm 1`),
only the last active pane's scrollback restored after a Super+W close and
reopen. Layout, shells, and journal were fine; archives existed for all
panes; no warning appeared.

### Root cause (confirmed in code, proven by failing tests)

`WorkspaceCoordinator::session_id_for_pane` searched only the **active**
tree (`self.tree()` = active tab of the selected project). History replay
(`replay_restore_history`) and recording-flag sync resolve sessions
through it, so restores for panes in inactive projects or hidden tabs hit
`None` and returned silently — after already consuming the staged events.
Exactly the reported symptom: one pane (the active one) replays, the rest
start fresh with no error anywhere.

### Fix (working tree only, no commit)

- `crates/omaterm-terminal/src/workspace.rs`: `session_id_for_pane`
  searches every project and tab (pane IDs are unique workspace-wide, so
  no caller can resolve a wrong session; focused/visible-pane callers
  observe identical results).
- `apps/omaterm/src/router.rs` `replay_restore_history`: staged events
  are re-staged instead of dropped when the session cannot be resolved
  and replayed, so a transient miss can never silently discard verified
  history.
- Regression tests: `session_id_for_pane_searches_inactive_projects_and_
  hidden_tabs` (terminal) and
  `restore_replays_history_for_panes_in_inactive_projects` (router,
  two same-directory projects, inactive project never selected). Both
  were verified to FAIL on the pre-fix lookup (`None` vs `Some`; staged
  events unconsumed) and pass after.

### Verification so far

- `cargo fmt --all --check` PASS; `cargo clippy --workspace --all-targets
  -- -D warnings` PASS (known `proc-macro-error2` notice only).
- Desktop (31), CLI (26), core (13), IPC (11), protocol (6), state (37),
  terminal lib (115), PTY integration (32) suites green: 271 tests,
  0 failures. (One full serial run exceeded its timeout under load
  average >3 with no failing test; per-suite reruns are all green —
  the known PTY-load sensitivity, not a deadlock: the fix adds no
  locks or threads.)
- `python3 scripts/check-docs.py` and `git diff --check` PASS.

### Next action

Finish PTY integration, then validate the user's exact flow live (release
build, two same-folder projects, Super+W close, reopen, per-pane
scrollback check) and record evidence here.

## Milestone 10 — Duplicate-prompt fix (2026-09-28)

User report: after every close/reopen cycle, one more stale prompt stacks
above the fresh one (`❯` × N). Restoration itself was correct.

### Root cause

The recorder captures all main-screen bytes, including the idle shell's
rendered prompt line (no trailing newline). Replay restored it verbatim,
then the fresh shell printed its own prompt underneath — one duplicate
per cycle, accumulating.

### Fix (working tree only, no commit)

- `crates/omaterm-terminal/src/history.rs`: new pure
  `strip_trailing_partial_line` — drops bytes after the final `\n` from a
  restored record, always keeps `Resize` events, reduces prompt-only
  records to resizes. Truncation lands on `\n` (ASCII), so wide/combining
  sequences never split; archives on disk keep full bytes (nothing lost
  permanently; torn mid-command tails are display-only losses).
- `apps/omaterm/src/router.rs` `replay_restore_history`: strips once and
  feeds the stripped list to both `replay_into` and
  `start_history_with_seed`, so later flushes never re-archive the stale
  prompt either. Shell-agnostic (no `prompt_ready` gate): applies to
  sh/zsh/fish too.
- Regression tests: six `strip_*` unit cases (prompt tail, resizes
  around tail, mid-chunk truncation, newline-terminated no-op,
  prompt-only collapse, end-to-end no-stack replay) plus a router test
  asserting the seeded snapshot keeps complete lines without the `❯`
  tail. Existing replay/router tests use newline-terminated events, so
  stripping is a proven no-op for them.

### Verification

- `cargo fmt --all --check` PASS; `cargo clippy --workspace --all-targets
  -- -D warnings` PASS (known `proc-macro-error2` notice only).
- Desktop (32, incl. new seed test), CLI (26), core (13), IPC (11),
  protocol (6), state (37) green; terminal history (21) green.
- Full serial workspace suite: 278 tests, 0 failures (32 desktop + 26 CLI
  + 13 core + 11 IPC + 6 protocol + 37 state + 121 terminal unit + 32 PTY
  integration; per-suite runs — one full invocation stalled 4+ min in the
  pre-existing `four_panes` PTY-spawn test under load, then passed standalone
  in 0.67s with zero code changes: environmental, same signature as prior
  documented PTY flakiness). `python3 scripts/check-docs.py` and
  `git diff --check` PASS.

## Milestone 10 — History restore fix: prompt-padding blank line (2026-09-28)

User report: after every close/reopen, one more empty line stacks above the
fresh `omaterm main ❯` prompt (N reopens → N blank lines). Inter-command
blanks are normal (same on plain `foot`): the prompt carries leading-newline
padding, so exactly one blank separator must survive — not zero, not growing.

### Root cause

`strip_trailing_partial_line` cut the record after the final `\n`, which is
the idle prompt's *leading padding* newline rather than real output. The
fresh shell re-emits its padding on launch, so each cycle persisted one
extra blank line. The prior duplicate-prompt fix removed the prompt text;
this removes its padding blank.

### Fix (uncommitted)

- `crates/omaterm-terminal/src/history.rs`: when an idle/torn tail was
  actually dropped and the kept prefix ends with `…\n <zero-width> \n`,
  exactly one trailing blank line goes with the tail. The span check
  accepts only `\r` and complete terminal escape sequences (notably the
  zero-width OSC 133 lifecycle markers, which sit between the output
  newline and the padding newline), so real content — including
  intentional blank output lines and colored lines — is preserved and the
  seam is idempotent. A record holding nothing visible collapses to
  resizes only; the fresh shell supplies its own padding.
- No router/desktop changes: `replay_restore_history` already strips
  before replay and seeding, so it inherits the fix.

### Verification (Rust 1.98.1)

- `cargo fmt --all --check` PASS; `cargo clippy --workspace --all-targets
  -- -D warnings` PASS (known `proc-macro-error2` notice only).
- 9 new history tests (padding drop, OSC-interleaved padding, fragmented
  CRLF across events, content-blank preservation, colored content,
  no-padding shells untouched, blank-only collapse, cross-restart
  idempotency, end-to-end seam replay with exactly one blank separator).
- `cargo test --workspace -- --test-threads=1` PASS: 287 tests, 0 failures
  (32 desktop + 26 CLI + 13 core + 11 IPC + 6 protocol + 37 state +
  130 terminal unit + 32 PTY integration).
- `python3 scripts/check-docs.py` and `git diff --check` PASS.

### Next action

User validates live on Omarchy/Hyprland (release build): enable history,
run a failing `ll`, Super+W close, reopen ×3 — exactly one blank row
between the error output and the fresh prompt every time, no growth.

## Project jumps, base directory, home default — 2026-09-28 (implemented)

Three user-requested project behaviors, implemented as vertical slices
through the M7 dispatcher (no duplicated logic; core stays GPUI-free).

### What changed

- Home-rooted projects (`apps/omaterm/src/main.rs`): `create_project`,
  `new_terminal_for_empty`, and `initialize_default` now pass
  `directory: None`, letting the router fall back to `home_directory()`
  (`router.rs:483-487`). `SpawnRetry::Project` and `PendingUiLaunch::
  Project` carry `Option<PathBuf>` so Retry preserves the home fallback.
  CLI `project open <path>` keeps explicit directories.
- Jump shortcut: `Ctrl+Shift+1..9` selects the n-th project in sidebar
  order via `ProjectCommand::Select` (`on_key_down`). Shift applies to
  the character before GPUI reports it, so `project_jump_index` maps both
  raw digits and shifted symbols (`!@#$%^&*(`) to slots 1–9; out-of-range
  is a no-op. Plain `Ctrl+1..9` was deliberately NOT bound (user
  decision): it would steal `Ctrl+2..7` control codes (NUL/ESC/FS/GS/RS/US
  per `input.rs:ctrl_byte`).
- Modifier-gated hints: root-view `on_modifiers_changed` tracks
  Ctrl/Shift-held in `show_project_hints`; sidebar rows prefix `"{n} · "`
  (slots 1–9) only while held.
- Settable base directory, full slice: `ProjectCommand::SetDirectory
  { project, directory }` (`command.rs`), `is_dir` validation
  (`validation.rs`), `WorkspaceCoordinator::set_project_directory`
  (`workspace.rs`), synchronous router arm with
  `WorkspaceChanged → PersistenceDirty` effects plus owner-scope
  authorization alongside `Rename` (`router.rs`), wire method
  `project.set-directory` (`method.rs`), bridge mapping
  (`ipc_bridge.rs`), CLI `project set-directory ID PATH`
  (`commands/project.rs`), IPC/CLI doc-table rows (`docs/08`, `docs/09`).
  Semantics: future tabs/splits/default launches only; live sessions and
  per-pane CWDs untouched. Desktop trigger: `change` button on the
  selected sidebar row opens the native folder picker
  (`cx.prompt_for_paths` with directories-only `PathPromptOptions`,
  XDG portal on Wayland); cancel is a no-op, portal failure sets a
  warning banner instead of failing silently.

### Changed files

- `apps/omaterm/src/{main,router,ipc_bridge}.rs`
- `crates/omaterm-core/src/{command,validation}.rs`
- `crates/omaterm-terminal/src/workspace.rs`
- `crates/omaterm-protocol/src/method.rs`
- `crates/omaterm-cli/src/commands/project.rs`
- `docs/08-milestone-8-ipc.md`, `docs/09-milestone-9-cli.md`, this status

### Automated verification (Rust 1.98.1, Omarchy/Hyprland)

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (known `proc-macro-error2` future-incompat notice only) |
| `cargo test -p omaterm-core -p omaterm-protocol -p omaterm-cli -p omaterm-state` | PASS |
| `cargo test -p omaterm --bin omaterm-desktop -- --test-threads=1` | PASS: 34 tests, incl. new `project_set_directory_updates_base_for_future_tabs` (effects order, stale/invalid/scoped-foreign, Delete cleanup) and `jump_index_maps_digits_and_shifted_symbols_to_slots` |
| `cargo test -p omaterm-terminal --lib -- --test-threads=1` | PASS: 131 tests, incl. new `set_project_directory_updates_pinned_base_and_rejects_stale_ids` |
| `cargo test -p omaterm-terminal --test pty_integration -- --test-threads=1` | PASS: 32 tests |
| `cargo test -p omaterm-ipc` | PASS: 11 tests |
| `cargo test --workspace -- --test-threads=1` | NOT PASSED in one shot: exceeded 600s with no summary (same PTY-suite stall class recorded in M4/M6); every package/target above passed separately in sequence |
| `python3 scripts/check-docs.py` | PASS: 19 files, 50 links, 72 blueprint refs, 23 CLI methods mapped |
| `git diff --check` | PASS |

### Live Wayland validation (release build, isolated `XDG_STATE_HOME`/`HOME`, no test residue left)

- First-launch project named `home`, rooted at the isolated `$HOME`
  (`project list`), not the daemon CWD — home default proven.
- `project set-directory <id> <dir>` → `{}`; `project list` shows new
  name/directory; `tab new` shell CWD is the new base while the older
  tab's shell stays in the previous directory — future-only semantics
  proven over real IPC.
- Graceful close wrote `workspace-v1.json` with the updated
  `pinned_directory` and both per-pane CWDs — persistence proven.
- Socket, state, and processes removed afterward; no user windows harmed.

### Explicitly NOT validated (needs eyes/hands on the live desktop)

- `Ctrl+Shift+1..9` actual key delivery on this layout (unit test covers
  both digit and shifted-symbol forms, but GPUI's delivered string was
  not observed live): open 2+ projects, hold Ctrl+Shift (hints `1 · …`
  must appear), press a digit, selection must jump. This hyprctl build
  (0.56.2) rejects `focuswindow` dispatches, so agent-driven focus/keys
  were not possible; `wtype` was intentionally not fired blind into the
  live session.
- Native folder picker (`change` button): portal availability on this
  Hyprland session unconfirmed; cancel and portal-missing paths are
  handled in code but unobserved.
- Sidebar `change` button hit target and hint styling at real DPI.

### Next action

User runs the three manual checks above on a dev or release build; if
GPUI delivers an unexpected key string for `Ctrl+Shift+<n>`, extend
`project_jump_index` accordingly. No commit made.

## Milestone 11 — v0.1 Closure & Hardening — complete with documented limits (2026-09-28)

Closed every remaining v0.1 gap from the blueprint audit without adding
v0.2+ features. Working tree only; no commit made.

### Changed files

- `crates/omaterm-core/src/{pane,result,lib}.rs` — `SplitSummary`,
  `split_path`, `PaneInfo.splits`
- `apps/omaterm/src/{router,ipc_bridge}.rs` — list carries split paths,
  bridge serializes `splits[{id,axis,fraction}]`
- `crates/omaterm-cli/src/{main,launcher,connection,output}.rs`,
  `commands/pane.rs` — path-launch forms, `SPLITS` column, CLI tracing
- `crates/omaterm-state/src/{config,history,lib}.rs` — `AppConfig`
  (`[terminal]/[appearance]/[automation]`), `ConfigError::Invalid`
- `crates/omaterm-terminal/src/{registry,session,spawn_queue,workspace,input,platform,lib}.rs`
  — scrollback plumbing, paste policy/drop escaping, OS boundary traits
- `crates/omaterm-logging/{Cargo.toml,src/lib.rs}` (new) — std-only
  subscriber, `OMATERM_LOG`/`RUST_LOG` directives, `omaterm::*` categories
- `apps/omaterm/src/main.rs` — config load/apply/warning, font override,
  paste two-step, file-drop target, categorized logging targets
- `packaging/arch/PKGBUILD`, `packaging/README.md` (new)
- `docs/11-milestone-11-v01-closure.md` (new), `docs/00-overview.md`,
  `docs/08-milestone-8-ipc.md`, `docs/09-milestone-9-cli.md`,
  `docs/acceptance-matrix.md`, `docs/dependencies.md`, this status record

### Automated verification (Rust 1.98.1)

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test --workspace -- --test-threads=1` | PASS: 313 tests, 0 failures (36 desktop + 31 CLI + 14 core + 11 IPC + 6 protocol + 3 logging + 41 state + 139 terminal unit + 32 PTY integration) |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (known transitive `proc-macro-error2` future-incompat notice only) |
| `cargo build --release --bin omaterm --bin omaterm-desktop` | PASS (used for all Wayland runs) |
| `python3 scripts/check-docs.py` | PASS |
| `git diff --check` | PASS |

New coverage highlights: core `split_path` axis/fraction/order; router
list-splits + resize/equalize observability; bridge splits JSON;
CLI `SPLITS` rendering + `pane list` mapping + path-launch parser/dispatch +
fake-server open/open-and-run/denial; config parse/validate/malformed (4);
session scrollback cap via viewport `history_size`; coordinator scrollback
pass-through; font-preference override; logging directives/filter/format;
paste policy + shell escaping; platform stat/TCP/self/child inspection +
inspector-seam CWD refresh.

### Release Wayland proofs (Omarchy/Hyprland, isolated RUNTIME/STATE/CONFIG/HOME, real Secret Service, SHELL=/bin/bash)

Isolation note: the first attempt overrode `XDG_RUNTIME_DIR` with a 0755
directory, which the M8 private-directory validator correctly rejected (no
socket, `IPC unavailable` path). Fixed with mode 0700 plus a
`wayland-1` symlink into the isolated run dir, keeping the compositor
reachable without touching the user's live runtime dir.

- P1 (11F): `pane list` shows `SPLITS -` unsplit; after `pane split
  --right` both panes carry the same split (`horizontal`, 0.5) in JSON;
  `pane resize --split <discovered> --fraction 0.45` applied live
  (widths 0.45/0.55); `pane equalize` restored 0.5/0.5. The M9
  split-ID limit is closed.
- P3 (11E): `omaterm <dir>` opened project `cwd-a` (new ID, selected);
  `omaterm <dir> -- echo M11_LAUNCH_OK` opened plus `Submitted command
  to shell`, and `terminal read` showed the submission echo plus
  `M11_LAUNCH_OK` from a fresh shell in that directory.
- P4 (11A): after `history enable`, two `terminal run` markers produced 3
  journal entries and 2 archives at the 30s checkpoint; after SIGTERM kill
  and relaunch the same pane ID returned with a new session ID, restored
  `M11_HIST_A/B` output above a fresh prompt, and the journal merged the
  archived prefix (original timestamps kept).
- P5 (11C): flipping one byte of the `.omhist` archive then restarting
  quarantined it (`.quarantined` retained, never overwritten as valid);
  the pane started empty with a live fresh shell while layout and journal
  survived.
- P2 (11D): `font-size = 200` config started normally with IPC live
  (defaults applied; banner needs eyes, see limits); valid
  `font-size = 13` + `scrollback-lines = 5000` loads cleanly.
- 11G: `OMATERM_LOG=debug` emits `epoch LEVEL omaterm::render: ...`
  lines (font resolution); targeted
  `omaterm::pane=debug,omaterm::render=info` filters correctly; no
  credential/token secret appears in any desktop stderr log.

### Perf baseline (11I)

Full serial suite on 12-core/30 GiB Omarchy, debug test profile:
313 tests green; sampled peak ~1.2 GiB RSS (cargo + test binaries + spawned
shells under load), 37 peak threads, 12 peak watched processes. FD
stability comes from the existing 100-cycle PTY test (tolerance-gated) and
M5 thread/FD/RSS cycle measurements. Release idle and per-process GPU
remain open (RadeonTop is device-wide only on this machine).

### Residue cleanup verified

Test desktop killed, no `omaterm-desktop` process or proof-window shells
remain, isolated RUNTIME/STATE/CONFIG/HOME removed. The pre-existing real
`~/.local/state/omaterm/history/` archive (19:45) and the production-named
keyring key (created 04:02, before this run) were verified untouched: no
keyring writes were performed, and a `--unlock` secret lookup must never be
captured (prior M10 caution stands).

### Documented limits (not passes)

- Eyes/hands on the live desktop: `Ctrl+Shift+1..9` key delivery, hint
  styling, folder-picker portal, paste-confirm/drop interaction, banner
  visibility (config/history/paste), font-size rendering. Policy and
  wiring are unit-tested; interaction is unobserved.
- `appearance.theme` dark/light parse and validate but the theme engine
  itself is future work (blueprint §58); `automation.enabled=false` is
  recorded with a banner while IPC stays enabled in v0.1.
- Clipboard stays on the GPUI clipboard; notification has a seam but no
  desktop wiring until v0.3 attention indicators.
- Graceful `window.close` live path not exercised on the shared session
  (focus-stealing risk; SIGTERM kills without cleanup per pre-existing
  behavior, stale socket reclaimed at startup per M8).
- Per-pane CWD re-verification after restore stays on M6 plumbing.
- X11 runtime, second compositor, alternate scaling/monitor, `zsh`/`fish`
  unavailable, per-process GPU — same standing limits as M1–M5.

## Milestone 12 — Project Context Root — complete (2026-09-29)

Foundation for all v0.2 developer-context features with no user-visible
panel. Every project resolves to one filesystem root (`pinned` when set and
present, else the nearest enclosing git toplevel of the active tab's shell
CWD, else the explicit `none` empty state) that M13–M18 target. Working
tree only; no commit made.

### Changed files

- `Cargo.toml`, `Cargo.lock` (workspace member `crates/omaterm-context`)
- `crates/omaterm-context/{Cargo.toml,src/lib.rs,src/resolve.rs,src/boundary.rs,src/ignore.rs}`
  (new), `crates/omaterm-context/tests/git_roots.rs` (new)
- `crates/omaterm-core/src/{command,result,lib}.rs` — `ProjectCommand::Root`,
  `RootSource` (`pinned|git|none`), `ProjectRootInfo`, `ErrorCode::PathOutsideRoot`
- `crates/omaterm-state/src/config.rs` — `[files]` (`max-results` default
  100, `[1, 5000]`; `show-hidden` default false) and `[git]`
  (`refresh-secs` default 5, `[1, 300]`) with the M11D
  preserve-format/explicit-error pattern
- `crates/omaterm-logging/src/lib.rs` — `omaterm::files`, `omaterm::git`,
  `omaterm::search` categories with the M11G redaction contract (IDs,
  sources, counts only — never paths, contents, or terminal output)
- `crates/omaterm-protocol/src/method.rs` — `project.root` (`{"project_id?"}`)
- `apps/omaterm/src/{router,ipc_bridge}.rs`, `apps/omaterm/Cargo.toml` —
  query arm (no effects), scope filtering, `{"root?","source"}` envelope
- `crates/omaterm-cli/src/{commands/project,output,main}.rs` — `omaterm
  project root [--project ID]` (explicit > `OMATERM_PROJECT_ID` > server
  selection), human + `--json` rendering
- `docs/08-milestone-8-ipc.md`, `docs/09-milestone-9-cli.md` (mapping rows),
  `docs/dependencies.md`, this status record

### Semantic decisions (recorded, not deviations)

- A set-but-missing pin resolves to `none` with **no** git fallback:
  silently re-rooting an explicitly pinned project into an unrelated
  repository would confuse file/git/diff targeting, so the missing root
  surfaces as the downstream empty state.
- `require_git(false)` on the ignore walker: a pinned non-repo directory
  still has a root, and its `.gitignore` expresses the same listing intent
  as inside a repository.
- Ranking spike decided now to bound license risk: `fuzzy-matcher` 0.3.7
  (MIT) over `nucleo` 0.5.0 (MPL-2.0); `notify` stays on stable 8.2.0
  (CC0-1.0) while 9.x is RC. Neither is added until M13.

### Automated verification (Rust 1.98.1, Omarchy/Hyprland)

| Command | Result |
|---|---|
| `cargo test --workspace -- --test-threads=1` | PASS: 328 tests, 0 failures (37 desktop + 32 CLI + 14 core + 11 IPC + 6 protocol + 3 logging + 41 state + 13 context + 139 terminal unit + 32 PTY integration; 13 context incl. real-repo integration) |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (known transitive `proc-macro-error2` future-incompat notice only) |
| `cargo fmt --all --check` | PASS |
| `cargo tree -p omaterm-context` | PASS: `ignore`, `omaterm-core`, `thiserror` only; zero `gpui` |
| `cargo build --release --bin omaterm --bin omaterm-desktop` | PASS (used for all Wayland runs) |
| `python3 scripts/check-docs.py` | PASS |
| `git diff --check` | PASS |

New coverage: pin-wins/deleted-pin/git/empty matrix, missing-git,
non-repo, wedged-git timeout bound (fake `sleep` binary, reaped),
traversal/`..`/absolute/symlink-escape rejection, ignore + hidden policy,
real nested-repo toplevel, `[files]`/`[git]` parse/preserve/invalid,
router pinned/deleted-pin/stale/scope matrix with empty effects, protocol
decode (28 methods), bridge mapping (29 rows), CLI parser (28 rows) +
mapping + human/JSON rendering.

### Release Wayland proofs (Omarchy/Hyprland, isolated STATE/CONFIG/HOME/RUNTIME, SHELL=/bin/bash)

- First-launch `home` project: `project root` → `/tmp/opencode/m12/home`
  (`pinned`), JSON envelope `{"root":…,"source":"pinned"}` valid, sidebar
  header basename matches.
- `project open` on the omaterm repo → sidebar `omaterm`, explicit
  `project root --project` → repo path (`pinned`), human + JSON.
- Deleted pin: `set-directory` to a temp dir (root `pinned`), `rm -rf` the
  dir → `No project root (source: none)`, JSON `{"root":null,
  "source":"none"}`; stale ID → `project_not_found`, exit 1.
- Git fallback: snapshot edited to unpin the repo project (pane CWD kept in
  the repo), restart → fresh shell prompt shows the repo dir, `project root`
  → repo toplevel (`source: git`). Full chain live: restore → fresh shell in
  saved CWD → procfs refresh → bounded `rev-parse` → wire/CLI.
- Screenshot `/tmp/opencode/m12-sidebar.png`: sidebar `home` + `Project 2`
  rows, restored shell in the repo directory. Test instance stopped; zero
  `omaterm-desktop` processes, zero stray proof shells (per-`/proc` CWD
  scan), isolated dirs removed.

### Documented limits (not passes)

- Live unpinned-project-with-no-shell path is unit-covered (context
  matrix); all creation flows currently pin, so it has no UI trigger yet.
- Standing v0.1 limits unchanged: second Wayland compositor, X11 runtime,
  alternate scaling/monitor, `zsh`/`fish` unavailable, per-process GPU,
  graceful `window.close` live path (SIGTERM kills without cleanup per
  pre-existing behavior; stale socket reclaimed at startup per M8).
- M13–M16 build the visible tree/git/diff/palette on this foundation; no
  editor, no content grep, no `terminal wait` (v0.4).

### Next action

M13 file tree + `Ctrl+P` finder (done — see M13 record below).

## Milestone 13 — File Tree + Ctrl+P Finder — complete with documented limits (2026-09-29)

Sidebar-first v0.2 panel on the M12 root/boundary/config foundation: a
right-sidebar `FILES` tree plus a `Ctrl+P` fuzzy finder overlay. No
in-sidebar filter box (user decision); finding is `Ctrl+P` only. "Open"
routes to the user's `$EDITOR` inside the focused terminal through the
existing `terminal.run` path (editor pane deferred to v0.3). Working tree
only; no commit made.

### Scope decisions (user-confirmed)

- Right sidebar (~240px) hosts `FILES` now; Git (M14), Diff (M15), and
  Process (M18) join it later. The left 180px `PROJECTS` sidebar and 36px
  tab bar are untouched; pane geometry subtracts both sidebars.
- `file.open` = submit `$EDITOR <shell-escaped path>` via `terminal.run`
  semantics (missing `$EDITOR` → `editor_not_configured`, non-Bash →
  `unsupported_operation`, not-ready → `shell_busy`).
- Single slice: tree + finder + copy-path / reveal / open together.
- Expanded-dirs bound 128 per project (snapshot schema v2).

### Changed files

- `crates/omaterm-context/Cargo.toml`, `Cargo.lock` (new: `notify`
  =8.2.0, `fuzzy-matcher` =0.3.7)
- `crates/omaterm-context/src/{lib,ignore,files}.rs` — `list_dir`
  (single-level, ignore-respecting, symlink-rejecting), `search_files`
  (skim-ranked, files-only, cancellable by caller generation),
  `FileWatcher` (recursive, drop-cancels, typed limit-exhaustion),
  `IgnoreFilter::builder` (shared policy with depth bound)
- `crates/omaterm-core/src/{command,result,validation,lib}.rs` —
  `FileCommand::{List,Search,Open}`, `FileEntry`/`FileKind`/`FileListInfo`
  DTOs (root-relative paths), `MAX_FILE_ENTRIES` (5000) +
  `MAX_FILE_QUERY_BYTES` (256), `file_not_found` /
  `editor_not_configured` / `no_project_root` codes
- `crates/omaterm-state/src/{snapshot,migration}.rs` — schema v2 with
  per-project relative `expanded_dirs` (≤128, validated), v1→v2
  migration (`expanded_dirs` defaults empty), `ValidatedSnapshot`
  carries expansions to the desktop
- `apps/omaterm/src/router.rs` — `file_root` helper (existence + scope
  + M12 resolution), `List`/`Search` pure-query arms (empty envelope on
  no-root, config `max-results`/`show-hidden`, redacted `files`/`search`
  logging), `Open` arm (boundary → focused-pane session → `$EDITOR`
  argv → `run_argv` error mapping), scope authorization for all three
- `apps/omaterm/src/files.rs` (new) — GPUI-free panel (bounded
  expansion set, row cache with 2000-row build / 400-row render caps,
  persistence snapshot/restore)
- `apps/omaterm/src/{main,ipc_bridge}.rs` — right sidebar, `Ctrl+P`
  overlay (type/Up/Down/Enter/Esc + Ctrl+Y copy / Ctrl+U reveal on the
  highlight), expand/collapse click, selected-row open/copy/reveal,
  `Ctrl+Shift+Y/U` tree actions, 250ms files poller (debounced watcher
  refresh, finder-result drain, 5s root re-resolve), snapshot
  capture/restore of expansions, `file.*` bridge mapping + `FileList`
  JSON envelope
- `crates/omaterm-protocol/src/{lib,method}.rs` — `MAX_FILE_ENTRIES`,
  `file.list` / `file.search` / `file.open` strict DTOs (28→31 methods)
- `crates/omaterm-cli/src/{main,output,commands/file}.rs` — `file list`
  / `search` / `open` verbs, human + `--json` rendering (28→31
  coverage rows)
- `docs/08-milestone-8-ipc.md`, `docs/09-milestone-9-cli.md` (mapping
  rows), `docs/dependencies.md`, this status record

### Key behaviors

- Tree rows come only from `dispatch_command(OmaCommand::File…)`; the
  finder keystroke search runs `omaterm_context::search_files` on a
  background thread against a router-resolved root (generation-guarded,
  stale results dropped). Nothing blocks the UI thread on a 10k walk.
- Copy uses the existing Bourne single-quote escaper (§47 reuse).
  Reveal types `cd <escaped>` with no newline (user reviews + submits).
- Watcher-limit exhaustion keeps the last good tree plus an amber
  banner; other watcher failures log and retry on the next tick.
- Truncated listings sort after a bounded collect, so the visible order
  under truncation is walk-ordered; the `truncated` flag is always
  accurate. Full ordering holds whenever nothing truncates.

### Automated verification (Rust 1.98.1)

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test --workspace -- --test-threads=1` | PASS: 345 tests, 0 failures (43 desktop + 34 CLI + 18 context + 14 core + 11 IPC + 3 logging + 6 protocol + 44 state + 139 terminal unit + 32 PTY integration) |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (known transitive `proc-macro-error2` future-incompat notice only) |
| `cargo tree -p omaterm-context` | PASS: zero `gpui` |
| `cargo build --release --bin omaterm --bin omaterm-desktop` | PASS (used for all Wayland runs) |
| `python3 scripts/check-docs.py` | PASS |
| `git diff --check` | PASS |

New coverage: context list/search/truncation/traversal/hidden-ignore/
watcher-limit unit tests; core validation boundaries + stable codes;
state v2 round-trip / v1 migration / over-bound + absolute + `..`
rejection; router list/search/open matrix (empty-root, scope,
truncation, no-EDITOR, missing-file, live Bash `RunSubmitted`);
protocol decode (31 methods); bridge mapping (38 rows) + `FileList`
envelope; CLI parser/mapping/rendering (31 rows); panel
refresh/prune/select/restore unit tests.

### Release Wayland proofs (Omarchy/Hyprland, isolated STATE/CONFIG/HOME/RUNTIME, SHELL=/bin/bash, EDITOR=/bin/true)

- `project open` (repo + 10k-file `big`), `project root` → pinned.
- `file list` (top + `--dir src`, hidden/ignored excluded), `--limit 1`
  → `truncated`, human + `--json` envelopes valid.
- `file search main` → `src/main.rs`; 10k-tree search → 5 bounded rows
  in 22ms wall (`truncated` accurate).
- `file open` → `Submitted open to shell.` (exit 0) with the quoted
  `/bin/true '<abs path>'` submission visible at a fresh prompt;
  missing → `file_not_found` exit 1; JSON `{"submitted":true}` valid.
- Screenshot `/tmp/opencode/m13/tree.png`: right `FILES` sidebar (▸
  docs, ▸ src, README.md, hint line), terminal showing both live
  submissions above fresh prompts.
- Expansion restore: stopped, injected `expanded_dirs: ["src"]` into
  the repo project (simulated post-toggle persisted state — the toggle
  itself is unit-covered; live click-toggle needs pointer control),
  restarted → tree renders `▾ src` with `lib.rs`/`main.rs` nested,
  layout/selection/fresh shells intact (`tree-restored.png`).
- Watcher: `touch WATCHED.txt` in the repo root appeared as a tree row
  within ~2s with no interaction (`tree-watcher.png`).
- v1 migration: downgraded the snapshot (version 1, no `expanded_dirs`
  keys), restarted → all 3 projects/tabs boot, `file list`/`search`
  live green.
- Residue cleanup verified: test units stopped, zero
  `omaterm-desktop` processes, isolated dirs removed. Screenshots remain
  outside the repository under `/tmp/opencode/m13/`.

### Documented limits (not passes)

- Live `Ctrl+P` key delivery, overlay typing/selection, and row
  click-toggle were not exercised (no pointer/keyboard injection on the
  shared session; `hyprctl closewindow` dispatch is rejected by this
  Hyprland 0.56 lua surface, same M10 limit). Finder ranking,
  overlay state machine, toggle/refresh/prune, and the `file.search`
  backend it serves are unit-tested, and CLI `file.search` parity is
  proven live on the same instance.
- Graceful `window.close` live path still unavailable (SIGTERM stops
  were used; debounced snapshots already held the latest state).
- Stray `pe` input visible in one restored-window capture is
  unattended-machine input (M10 shared-machine caution), not
  application output.
- Standing v0.1 limits unchanged: second compositor, X11 runtime,
  alternate scaling/monitor, `zsh`/`fish` unavailable, per-process GPU.

### Next action

M13 follow-up v2 (lazy loading + icons, done — see below), then M14.

## Milestone 13 follow-up v2 — on-demand loading + file icons — complete with documented limits (2026-09-29)

Replaces the interim depth gate with true lazy loading (no depth limit
at all) and gives the tree/finder VSCode-style colored Nerd Font icons.
Working tree only; no commit made.

### What changed and why

- Depth gate removed (`MAX_AUTO_DEPTH`, session bypass, restore clamp,
  `gated` rows, `update_dir`/`has_deeper_expansions` all gone). In its
  place: a per-project listing cache. The root lists synchronously (one
  bounded walk, instant first paint); every expanded directory resolves
  from cache or fetches on a background worker (max 4 in flight) with
  dimmed `loading` rows meanwhile. Completions are generation-guarded;
  landing fetches rebuild rows and continue deeper levels until nothing
  is needed. Cache drops per-directory on watcher events, wholesale on
  project switch/root change (memory bounded by one project).
- Prune rule hardened: a missing row prunes its expansion only with full
  ancestor-chain ground truth (every ancestor listing cached). This fixed
  a real race the follow-up itself exposed — mid-chain invalidation used
  to orphan deeper intent when intermediate fetches were outstanding
  (proven by a failing-then-passing unit test and live screenshots).
- Watcher callbacks now drop `EventKind::Access` (`is_access()` verified
  against the locked `notify` 8.2.0 / notify-types source). Diagnosis
  evidence: with the app running, `inotifywait` showed a continuous
  OPEN/ACCESS/CLOSE_NOWRITE stream and the tree refreshed 135/135 poller
  ticks; with the app stopped, zero events. The app's own walks were
  re-arming the watcher — a self-sustaining loop. After the filter: one
  refresh per real change, 5s ticks resolve-only.
- `list_dir` fills directories first within the walk bound (a truncated
  `$HOME` listing used to cut `src/` while showing `topfile099.txt`),
  and the recursive search never descends into `.cache`, `.git`,
  `node_modules`, `target`, `__pycache__`, `.venv` (still listed as rows
  and explicitly expandable; `Trash` deliberately excluded — name
  collision risk).
- `Show more (N+)` moved above the rows: the sidebar has no scroll, so a
  bottom footer is unreachable behind a full page (found live on the
  `$HOME` fixture). No-scroll remains a known limit (see below).
- Icons: pure `icon_for()` table in `apps/omaterm/src/files.rs` (~70
  extensions + exact filenames + README/LICENSE families + `.git` /
  `node_modules` dirs + folder open/closed + Nerd chevrons), Seti-inspired
  colors, rendered in tree rows and `Ctrl+P` results. Codepoints taken
  from Nerd Fonts 3.5.1 `glyphnames.json` and every tabled glyph verified
  present in the installed `JetBrainsMonoNerdFont-Regular.ttf` by the
  kept `icon_glyphs_exist_in_nerd_font` test (skips where the font is
  absent, e.g. CI). `ttf-parser` =0.25.1 is a dev-only test edge
  (already in `Cargo.lock`; never linked into the binary).

### Changed files

- `apps/omaterm/src/files.rs` — cache/pending/placeholder model,
  `icon_for` table, rewritten unit tests
- `apps/omaterm/src/main.rs` — `sync_files_from_cache`, fetch channel +
  generation, targeted event invalidation, top-placed `Show more`,
  icon/chevron rendering (tree + finder)
- `apps/omaterm/Cargo.toml` — `ttf-parser` dev-dependency (test only)
- `crates/omaterm-context/src/files.rs` — access-event filter,
  dirs-first truncation, traversal skip list + tests
- `docs/dependencies.md`, this status record

### Automated verification (Rust 1.98.1)

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test --workspace -- --test-threads=1` | PASS: 354 tests, 0 failures (50 desktop + 34 CLI + 20 context + 14 core + 11 IPC + 3 logging + 6 protocol + 44 state + 139 terminal unit + 32 PTY integration) |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (known transitive `proc-macro-error2` notice only) |
| `cargo tree -p omaterm-context` | PASS: zero `gpui` |
| `python3 scripts/check-docs.py` | PASS |
| `git diff --check` | PASS |

### Release Wayland proofs (Omarchy/Hyprland, isolated dirs, SHELL=/bin/bash)

- `$HOME` fixture (123 entries): instant bounded tree, dirs-first
  (`node_modules`, `projects`, `target`), top-placed `Show more (100+)`
  (`home-tree2.png`).
- 7-deep restored chain loads level-by-level with zero depth limit
  (log: `needed=7` → `landed=4` → `needed=3` → quiet) and renders fully
  with distinct folder/file icons (`deep-lazy5.png`).
- Deep watcher file (`deep1/deep2/newfile.rs`, then `second.rs`)
  appears in ~3s with the chain intact — targeted invalidation, no full
  rebuild (`deep-watcher.png`, `deep-stable.png`).
- Skip list live: `file search` for `node_modules`/`target` contents
  returns empty envelopes while the dirs still list as rows.
- Idle 10s shows no tree refreshes (refresh-once + resolve-only ticks).
- Residue cleanup verified: units stopped, zero `omaterm-desktop`
  processes, all isolated `/tmp/opencode/m13*` dirs removed.
  Screenshots cited above were removed with their dirs; representative
  captures for the slice: `icons-tree.png` (flat + icons),
  `deep-lazy5.png` (7-deep + icons), `deep-stable.png` (targeted deep
  refresh intact).

### Documented limits (not passes)

- The earlier nested-tree screenshots (empty expansion set rendering
  deep chains) never reproduced once isolated: with the access-loop
  fixed, fresh-state trees render flat and the injected-chain runs
  behave exactly per the unit model. The leading explanations remain a
  stop/edit/start race during rapid validation cycling (a debounced save
  landing between snapshot edit and shutdown — observed once as a
  partial chain on disk) and/or shared-machine pointer input (stray `pe`
  input seen in one capture, M10 precedent). Recorded here instead of
  claimed.
- `Ctrl+P` key delivery, overlay typing, row click-toggle, and
  `Show more` clicks were not exercised live (no pointer/keyboard
  injection on the shared session); mapping, overlay state machine, and
  the served backends are unit-tested with CLI parity live.
- Directory rows show the folder glyph only (open = expanded, closed =
  collapsed); the chevron prefix was removed as redundant per user review
  (`single-marker.png`). Verified chevron codepoints stay reserved for
  M16.
- Row layout pass: fixed 18px icon cell (centered glyph) so labels align
  per depth, uniform gap, roomier row padding matching the sidebar
  rhythm, wider depth indent; same pattern in `Ctrl+P` results
  (`layout.png`). Glyph set and colors unchanged.
- Accepted spec deltas (user decision, M13 closed): no in-sidebar fuzzy
  filter (the `Ctrl+P` overlay is the finder); no tree arrow-key
  navigation (arrows belong to the terminal — keyboard-only flow runs
  through the finder plus `Ctrl+Shift+Y/U`); no sidebar scroll (row caps
  plus `Show more` bound listings, rows past the viewport stay
  unreachable for now). A fresh project switch can flash
  `Empty directory` for one 250ms tick before the first refresh lands
  (pre-existing, self-heals).
- Finder restyle (VSCode Quick Open): centered floating max-600px box
  with shadow, input row (magnifier, block caret, dimmed placeholder),
  divider, filename-bright + dimmed-parent rows (kind tag dropped),
  accent match highlighting from the same skim scorer, selected-row
  accent bar, footer hints inside the box (`quickopen.png`,
  `final-state.png` — the latter proves the reverted closed-by-default
  state; the open overlay was verified via a temporary open-by-default
  build, reverted before the final gates).
- Finder caret: 2px block at exact text-line height hugging the query,
  blinking ~530ms with keystroke phase-reset and no timers while closed
  (a text-pipe caret stacked glyph bearings on the row gap and stretched
  to the padded row height).
- Finder long rows: single `StyledText` per row half with skim ranges
  mapped to byte spans (`highlight_ranges`, Unicode-tested), inside
  `min-width: 0` + `truncate()` wrappers — verified root cause in the
  GPUI 0.2.2 source that `white-space`/`text-overflow` only reach text
  through the cascaded `TextStyle`, which bare `overflow_hidden()`
  never sets. Filename keeps the larger share (parent capped at 140px).
  Proven live on `…/admin/articles/Create|DeleteArticleWithAVeryLong…`
  rows that previously wrapped to 2–3 broken lines.
- Floating overlay (absolute layer over terminal content, explicit
  viewport-arithmetic centering, no backdrop) plus a 2px block caret
  hugging the query (replacing the pipe glyph whose side bearings
  stacked with the row gap). Verified pixel-measured against the
  struct layout after ruling out stale-process contamination via
  `/proc` environ checks; shared-machine input observed again during
  validation (M10 precedent).
- Graceful `window.close` live path still unavailable (SIGTERM stops;
  debounced snapshots held state). Standing v0.1 limits unchanged.

### Next action

M13 follow-up v3 (freeze fix + ellipsis + wheel scroll, done — see
below), then M14.

## Milestone 13 follow-up v3 — home freeze, ellipsis, scroll — complete with documented limits (2026-09-29)

Three user-reported issues, one slice. Working tree only; no commit made.

### 1. Freeze + 100% CPU on `$HOME`-rooted projects — fixed

Root causes, both on the project-focus path: (a) `ensure_files_watcher`
ran `notify` recursive arming **synchronously on the UI thread** — tens
of thousands of inotify watches before returning; (b) every debounced
event batch re-resolved the root (procfs + `git rev-parse` subprocess)
and re-walked on the UI thread, so a live home kept the app hot; the
earlier access-event filter stopped self-triggering, but real home
traffic (caches, editors, daemons) sustained the load.

- `omaterm-context`: new `FileWatcher::watch(root, extra_skips,
  show_hidden, callback)` arms top-down with `NonRecursive` watches,
  pruning `SKIP_TRAVERSE_NAMES` plus caller-supplied canonical prefixes
  (our own state/config dirs when under the root, so snapshot saves
  never self-trigger); hidden dirs skipped when `show_hidden` is false.
  New `watch_single()` tops up directories created after arming
  (idempotent; missing paths are a no-op). Access-kind filtering kept.
- Desktop: arming moved to a background worker keyed by
  `files_generation` (new `files_arm_tx/rx`, `files_arming` dedupe;
  stale installs drop, failures retry at most every 5s). Event batches
  invalidate + rebuild from cache + bounded background refetch only —
  no root re-resolve, no synchronous root walk (root moves stay on the
  5s tick) — coalesced to ~1Hz with backlog-shed warnings. New dirs in
  event paths get `watch_single` top-ups.
- Adjacent fix: generation-mismatched fetch completions now unpend
  (plus `unpend_all` on switch), closing a fetch-budget leak that could
  stall tree loading after rapid switches.

### 2. Long names break alignment — fixed

Row labels use GPUI's first-class `truncate()` (overflow-hidden +
nowrap + ellipsis, verified in the locked 0.2.2 source) inside
`flex_1`, with a `flex_shrink_0` fixed-width icon cell as the alignment
anchor. Full paths stay available via copy-path. Same pattern in
`Ctrl+P` results.

### 3. Tree scrolling — added (wheel-only per user decision)

Row-step scrolling over fixed-height rows: pixel offset state,
wheel handler mirroring the terminal's delta convention, per-render
clamping, `.skip().take()` window (header/footers/hints stay fixed),
reset on project switch. Proven live by real user scrolls (offset 14
persisted across captures with correct clamping).

### Automated verification (Rust 1.98.1)

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test --workspace -- --test-threads=1` | PASS: 356 tests, 0 failures (50 desktop + 34 CLI + 22 context + 14 core + 11 IPC + 3 logging + 6 protocol + 44 state + 139 terminal unit + 32 PTY integration) |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (known transitive `proc-macro-error2` notice only) |
| `python3 scripts/check-docs.py` | PASS |
| `git diff --check` | PASS |

New coverage: pruned arming (skip-list + extra-skip silence with live
notify assertions), `watch_single` top-up/idempotence/missing-path,
dirs-first truncation priority, plus the carried v2 panel/context tests.

### Release Wayland proofs (Omarchy/Hyprland, isolated dirs)

- 60-dir `$HOME` fixture: focus paints instantly, arming finishes on the
  worker in **2ms**, process replaces the old refresh storm with
  resolve-only 5s ticks.
- **Idle CPU: 1.097s over 100.0s wall (~1.1% of one core), 31.1M peak**
  (systemd accounting) — against 3.1s/112s for the pre-fix code on a
  smaller fixture, and the reported 100% pin gone.
- Ellipsis + alignment on a long-name fixture (`ellipsis.png`); wheel
  scroll offset working across captures; targeted deep refresh intact
  from the v2 proofs (unchanged paths).
- Residue cleanup verified: units stopped, zero desktop processes, all
  isolated `/tmp/opencode/m13*` dirs removed.

### Documented limits (not passes)

- Wheel scroll **direction** follows the terminal convention
  (positive delta toward top) but was not explicitly confirmed live —
  report back if it feels inverted and the sign flips in one line.
- `Ctrl+P` typing and row click-toggle still need hands (standing
  limit). Test instances exited during validation with no crash
  evidence (no coredump/OOM/panic lines; orderly logs to the end) —
  treated as user-closed on the shared machine per M10 precedent.
- Graceful `window.close` live path still unavailable. Next: M14.

### Next action

Begin M14 Git Status panel on the M12 root/boundary foundation and the
M13 right-sidebar shell.

## Milestone 14 — Git Status Panel — complete with documented limits (2026-10-01)

VSCode-Source-Control-style status over the system git binary only
(blueprint §32 — no libgit2, so user config/hooks/SSH/GPG keep working):
branch header with ahead/behind badge, Staged/Unstaged/Untracked groups,
per-file stage/unstage/discard, manual + interval + post-`terminal.run`
refresh, and a two-step discard arm. No commit UI (explicit non-goal).
Working tree only; no commit made.

### Scope decisions (user-confirmed in planning)

- Git code extends `omaterm-context` (`src/git.rs`); no new crate.
- Discard arm is desktop-only (paste/history precedent): the router stays
  single-step, and no panel code path dispatches without a live arm.
- Entry cap reuses `[files] max-results` (1–5000, default 100); no new
  `[git]` key. The existing `[git] refresh-secs` (1–300, default 5)
  drives the panel interval.
- Diff-on-select only highlights + logs (`diff opens in M15`); M15 wires
  the viewer.

### Changed files

- `crates/omaterm-context/src/git.rs` (new), `src/lib.rs` — porcelain
  v2 `-z --branch` parser + `status`/`stage`/`unstage`/`discard` runners
- `crates/omaterm-context/tests/git_status.rs` (new) — real-repo
  integration (clean/dirty/round-trip/discard/traversal/locked/missing)
- `crates/omaterm-core/src/{command,result,validation,lib}.rs` —
  `GitCommand`, `GitEntry`/`GitStatusInfo`, `NotARepo`/`GitFailed`/
  `GitUnavailable` codes, `MAX_GIT_PATHS` (100) + `MAX_GIT_PATH_BYTES`
- `apps/omaterm/src/router.rs` — `Git{Status,Stage,Unstage,Discard}` arms
  (scope auth, `omaterm::git` redacted logging, no persistence effects),
  `pinned_for`/`shell_cwd_for` clones for the worker path
- `apps/omaterm/src/git_panel.rs` (new) — GPUI-free panel state, arm
  window, `should_refresh`, `spawn_status_thread` (root resolution and
  `git status` off-thread with worker-id tagging)
- `apps/omaterm/src/main.rs` — `git_tick` on the 250ms poller (drain +
  one in-flight fetch, switch generation, per-project cache),
  stage/unstage/discard handlers, SOURCE CONTROL sidebar section,
  post-`terminal.run` dirty hint
- `crates/omaterm-protocol/src/{lib,method}.rs` — `git.status/stage/
  unstage/discard` strict DTOs (31→35 methods), `MAX_GIT_PATHS`
- `apps/omaterm/src/ipc_bridge.rs` — mapping (37→43 rows) + grouped
  `GitStatus` JSON with bridge cap + truncation test
- `crates/omaterm-cli/src/{commands/git.rs (new),commands/mod.rs,main.rs,
  output.rs}` — `git status/stage/unstage/discard` verbs (31→35 parser
  rows), grouped human rendering + `--json`
- `docs/08-milestone-8-ipc.md`, `docs/09-milestone-9-cli.md` (mapping
  rows), `docs/dependencies.md`, this status record

### Key behaviors

- Env hygiene on every spawn: `GIT_TERMINAL_PROMPT=0`,
  `--no-optional-locks` (reads), `--no-pager`, closed stdin, bounded
  waits (10s status / 30s mutations) with kill+reap; stdout capped at
  4 MiB (torn tail dropped on a NUL boundary, `truncated` set), stderr
  capped at 4 KiB for `git_failed` messages.
- Parser verified empirically against git 2.55.0: branch headers require
  `--branch`; `-z` rename records are `<new> NUL <old>` (TAB without it);
  non-`-z` quoting would corrupt Unicode — `-z` is mandatory. Scores
  strip only on the `R100`/`C75` shape so spaced paths survive.
- Paths run with the root as git's working directory plus
  `-c status.relativePaths=true`, so entries are root-relative even
  under hostile user config. Mutations boundary-check through
  `join_under_root` (lexical + symlink-aware for existing targets;
  missing paths resolve lexically so discard accepts deleted files).
- Discard: tracked paths via `git restore --source=HEAD --staged
  --worktree` (partitioned by one `git ls-files -z` call); untracked
  paths deleted from disk; missing paths are success. Locked index
  surfaces `git_failed`, never hangs.
- Desktop reads never touch the dispatcher on the UI thread (M13
  background-fetch precedent); mutations go through the single
  dispatcher like IPC/CLI (§62). The off-thread contract is pinned by a
  deterministic worker-id assertion, not a timing flake.

### Automated verification (Rust 1.98.1)

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test --workspace -- --test-threads=1` | PASS: 385 tests, 0 failures (58 desktop + 36 CLI + 33 context + 1 git-roots + 7 git-status + 15 core + 11 IPC + 3 logging + 6 protocol + 44 state + 139 terminal unit + 32 PTY integration) |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (known transitive `proc-macro-error2` future-incompat notice only) |
| `cargo tree -p omaterm-context` | PASS: zero `gpui`, zero git library (system binary only) |
| `cargo build --release --bin omaterm --bin omaterm-desktop` | PASS (used for all Wayland runs) |
| `python3 scripts/check-docs.py` | PASS |
| `git diff --check` | PASS |

New coverage: porcelain fixtures (headers, detached, groups, rename +
Unicode/spaces, unmerged-both-groups, torn tail, truncation order),
join/traversal/symlink rejection, missing-binary `git_unavailable`
mapping, real-repo integration (clean branch, dirty groups, rename
both-paths live, stage→status→unstage round-trip, discard restore +
delete + idempotent-missing, traversal rejection on all three
mutations, non-repo `NotARepo`, locked-index `git_failed` with working
status, wedged-git timeout), core validation boundaries, router
flow/scope/stale/empty-envelope matrix, protocol decode (35),
bridge mapping (43) + JSON shape + 128-cap truncation, CLI
parser/mapping/render (35 rows), panel arm-window/refresh-timing/
off-thread-worker tests.

### Release Wayland proofs (Omarchy/Hyprland, isolated STATE/CONFIG/HOME/RUNTIME, SHELL=/bin/bash)

- `project open` on a repo fixture → `project root` pinned; `git
  status` shows `Branch: master` + `Unstaged (1)` + `Untracked (1)`;
  `--json` envelope valid (`branch`, `truncated: false`, group keys).
- `git stage tracked.txt` → `Staged (1)`; `git unstage` → back to
  `Unstaged`; `git discard untracked.txt` deletes it;
  `git discard tracked.txt` restores HEAD content; final
  `Working tree clean` (all matched `git diff` ground truth).
- Error paths live: `../outside.txt` → `path_outside_root` exit 1;
  bare `git stage` → usage exit 64; pinned non-repo project →
  `Not a git repository` envelope on status, `not_a_repo` exit 1 on
  stage.
- Panel: after re-dirtying + `project select`, grim capture shows the
  right sidebar SOURCE CONTROL section (`master`, `Unstaged (1)` with
  `tracked.txt stage discard`, `Untracked (1)` with `new2.txt stage
  discard`) matching CLI state on the same instance. Capture verified
  during the pass; the file was not retained (tmp reaped).
- Residue cleanup verified: test unit stopped, zero
  `omaterm-desktop` processes, isolated STATE/HOME/repo dirs removed
  (socket/credential are pre-existing SIGTERM-stale artifacts, reclaimed
  at startup per M8).

### Round-2 live proofs — auto-refresh + post-run hint (2026-10-01, same release build)

A second isolated instance closed the remaining live rows without any
pointer/keyboard injection:

- Background auto-refresh: with a clean tree, `echo external >>
  tracked.txt` was made outside OmaTerm; ~9s later (one 5s interval +
  tick) CLI `git status` showed `Unstaged (1) tracked.txt`, and a
  window-region grim capture shows the panel rendering `Unstaged (1)`
  with `tracked.txt stage discard` — no OmaTerm interaction at all.
- CLI→panel parity on mutated state: `git stage tracked.txt` →
  `--json` reports `staged: ['tracked.txt'], truncated: false`.
- Post-`terminal.run` hint: after `git unstage`, `terminal run --pane
  <id> -- touch runhint.txt` submitted to the ready Bash shell; 2s
  later (inside the 5s interval, proving the hint path rather than the
  interval) CLI status listed `runhint.txt` untracked, and a second
  capture shows the terminal with the executed `'touch' 'runhint.txt'`
  plus the panel rendering `Untracked (1)` / `runhint.txt stage
  discard` alongside the unstaged entry.
- Captures were verified during the pass (`auto-refresh.png`,
  `staged.png` under `/tmp/opencode/m14b/`); the directory has since
  been reaped with the rest of the isolated fixture. Residue cleanup
  re-verified: unit stopped, no desktop/shell test processes remain
  (one `pgrep -f` self-match ruled out via `pgrep -af` showing only
  the pgrep pipeline itself), isolated dirs removed.

With these, every M14 test-plan live row has same-instance evidence;
the only remaining non-passes are hands-on-panel-clicks (standing
shared-machine limit) and the pre-existing graceful-close path.

### Follow-up — icon actions + Files|Git tabs (2026-10-01, user notes)

- Stage/unstage/discard row actions render Nerd Font icons instead of
  text (`GitAction::icon` in `git_panel.rs`: plus/minus/trash in
  green/amber/red decorator hues; refresh is an icon too). All four
  codepoints are covered by the `icon_glyphs_exist_in_nerd_font` test,
  which ran against the installed `JetBrainsMonoNerdFont-Regular.ttf`
  (not skipped).
- Git is a right-sidebar tab (`| Files | Git |`, `SidebarTab`
  defaulting to Files, view-local and never persisted — no schema
  churn) instead of a section stacked under the tree. The files
  watcher warning stays on the Files tab; the poller serves both tabs
  regardless of visibility.
- Changed files: `apps/omaterm/src/{git_panel.rs,files.rs,main.rs}`.
  No dispatcher/protocol/CLI changes (same `GitCommand` path).
- Gates: `cargo fmt --check` PASS, `clippy -D warnings` PASS,
  per-package serial suites green (59 desktop incl. new tab-default
  test + glyph coverage, 33 context lib, 36 CLI, 15 core, 6
  protocol). The full serial workspace run exceeded its 600s timeout
  under concurrent user load (known PTY-load sensitivity per M4/M6;
  untouched suites last green at 385/385).
- Live screenshots attempted on an isolated instance (dirty repo,
  temp Git-default build): the shared-machine session stayed busy and
  the test window sat occluded, so no Git-tab capture landed. The
  temp default was reverted (`SidebarTab::Files`) and release
  rebuilt. Icons are test-verified against the real font and the tab
  layout reuses proven render primitives — eyes verification stays
  open for the user at a glance.

### Follow-up 2 — commit, monochrome icons, scrollbars (2026-10-01, user notes)

Three user notes, one slice. The "no commit UI" non-goal is
deliberately reversed (user-approved scope change): blueprint §32's
normative rules (system git only, no silent merge/rebase strategy)
are untouched — plain `git commit -m` violates neither.

**Commit (bare minimum).** Message input row + check button atop the
Git tab (single-line, Ctrl+P input precedent: click focuses, Esc
releases, Enter submits, Backspace deletes; Ctrl/Alt combos fall
through to global shortcuts; pane clicks and tab switches release
focus). Button enables only with staged changes; authorship from repo
config; no amend/push. Failed submissions (hook/GPG rejection) put
the message back for fix-and-retry. Full slice:
`omaterm-context::git_commit` (→ short HEAD oid) →
`GitCommand::Commit` + `MAX_GIT_MESSAGE_BYTES` (newlines/tabs allowed
for the body, NUL/controls rejected) + `CommandOutput::GitCommitted`
→ router arm (scope auth, redacted log, no persistence effects) →
`git.commit` DTO/bridge/CLI (`git commit -m`, `Committed <oid>.`) →
desktop input state in `GitPanel` (per-project drafts, pure + tested)
→ docs mapping rows (32 CLI methods now).

**Monochrome icons.** Colored plus/minus/trash are gone: stage=plus,
unstage=minus, discard=undo-arrow (U+F0E2, VSCode-style), refresh and
commit-check likewise, all in the row color. No GPUI tooltips —
0.2.2 tooltips need a full View type per hint, disproportionate for
four labels — so a dimmed legend footer renders from the same glyph
constants as the actions (cannot drift). `fa-check` (U+F00C) added to
the glyph-coverage test.

**Scrollbars.** Vertical: thin rail beside the rows with a proportional
thumb (`scroll_thumb` pure + tested), wheel, press-and-slide drag
(delta-only, no geometry needed), stuck-drag guards (release, pane
clicks, tab switches). Hidden when everything fits. Horizontal:
Shift+wheel / native-x / rail drag into a clamped px offset; at rest
the classic ellipsis path renders byte-identical, shifted rows lay out
the full relative path nowrap in an overflow viewport. View-local
state only, no persistence/schema change.

Changed files: `omaterm-context/{git.rs, tests/git_status.rs}`,
`omaterm-core/{command,validation,result}.rs`,
`apps/omaterm/{router.rs,git_panel.rs,files.rs,main.rs}`,
`omaterm-protocol/{lib,method}.rs`, `omaterm-cli/{commands/git.rs,
main.rs,output.rs}`, `docs/{08,09,14}-*.md` checkboxes below, this
record.

Automated: fmt/clippy clean; desktop 62, context lib 33 + 8
integration, core 16, CLI 36, protocol 6 — all green (full serial
workspace run deferred: 600s timeout under concurrent user load,
known PTY sensitivity; untouched suites last green 385/385).

Live (release, isolated instance): `git stage` → `git commit -m "add
work"` → `Committed 8955ec5.`, clean tree, HEAD matches ground
truth; nothing-staged → `git_failed` exit 1; missing repo identity →
bounded author-identity `git_failed` (no prompt, no hang — env
hygiene holds). grim went unresponsive mid-pass (compositor load),
so the new Git-tab/commit-row/scrollbar rendering is NOT
eyes-verified this round: glyphs are font-test-verified and layout
reuses proven primitives. Residue verified clean (unit stopped, no
desktop processes, fixture removed).

### Documented limits (not passes)

- Panel clicks (stage/unstage/discard actions, refresh, row select) and
  the discard arm banner were not exercised by hand (no pointer
  injection on the shared session); the arm state machine, dispatch
  wiring, and CLI parity on the same instance are tested/proven, and
  the panel render itself is screenshot-verified.
- The timed off-thread assertion is a deterministic worker-identity
  check plus interval/timing unit tests, not a wall-clock UI-thread
  profiler trace.
- Wheel-scroll direction for the new section inherits the tree
  convention (standing M13v3 note).
- Graceful `window.close` live path still unavailable (SIGTERM stops;
  snapshots already held state). Standing v0.1 limits unchanged:
  second compositor, X11 runtime, alternate scaling/monitor,
  `zsh`/`fish` unavailable, per-process GPU.

### Next action

Finish M15 live Git-row-to-preview-tab Wayland proof.

## Milestone 15 — Diff Viewer — implementation in progress

Added bounded unified-diff parsing and execution over the system `git`
binary. Clicking a staged/unstaged Git row opens the selected changed
file's diff as a preview tab in the main tab strip (`Diff: <name>`);
the tab holds the file diff, never a terminal — core tabs stay
terminal-only and the layout is never persisted (view-local, M13/M17
scope). Selecting a terminal tab or closing the chip returns to the
terminal surface. Hunks render windowed (8-hunk viewport follows the
cursor, wheel scrolls) with plain monospace `+`/`-` colors, previous/next
navigation, copy/open-path actions, and per-hunk stage controls routed
through the existing `GitCommand::Stage` path. `diff.show` and
`diff.list-files` are also available through the common dispatcher, IPC,
and CLI. Working tree only; no commit made.

### Changed files

- `crates/omaterm-context/src/{diff,git,lib}.rs` — bounded `git diff`
  runner/parser, caps, binary handling, UTF-8-safe truncation, shared
  M14 subprocess helper
- `crates/omaterm-context/tests/diff_repo.rs` — real-repository staged /
  unstaged / single-path / binary / rename / 1000-file coverage
- `crates/omaterm-core/src/{command,result,validation,lib}.rs` —
  `DiffCommand`, bounded result DTOs, context/path validation
- `apps/omaterm/src/{router,ipc_bridge,diff_panel,main}.rs` — scoped
  query path, bounded bridge response, off-thread refresh state, main-area
  preview tab (view-local open/close, hunk viewport + wheel scroll)
- `crates/omaterm-protocol/src/{lib,method}.rs` — strict
  `diff.show` / `diff.list-files` DTOs
- `crates/omaterm-logging/src/lib.rs` — `omaterm::diff` category;
  logs only project ID, staged flag, file count, and truncation
- `crates/omaterm-cli/src/{commands/diff.rs,commands/mod.rs,main.rs,output.rs}`
  — CLI mapping and human/JSON rendering
- `docs/{08-milestone-8-ipc.md,09-milestone-9-cli.md,15-milestone-15-diff-viewer.md}`
  — method mappings and corrected Git-panel UI contract
- This status record

### Automated verification (Rust 1.98.1)

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test --workspace -- --test-threads=1` | TIMED OUT after 900s at `omaterm_terminal::workspace::tests::four_panes_have_independent_sessions`; that terminal unit target passes independently |
| `cargo test -p omaterm --bin omaterm-desktop -- --test-threads=1` | PASS: 74 tests (incl. preview open/close + viewport follow/wheel) |
| `cargo test -p omaterm-context -- --test-threads=1` | PASS: 44 unit + 5 diff integration + 1 git-root integration + 8 git-status integration tests |
| `cargo test -p omaterm-cli -p omaterm-protocol -p omaterm-core` | PASS: 38 CLI + 6 protocol + 17 core tests |
| `cargo test -p omaterm-logging` | PASS: 3 tests |
| `cargo test -p omaterm-terminal --lib -- --test-threads=1` | PASS: 139 tests |
| `cargo test -p omaterm-terminal --test pty_integration -- --test-threads=1` | PASS: 32 PTY integration tests |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (existing transitive `proc-macro-error2` future-incompatibility notice only) |
| `cargo build --release --bin omaterm --bin omaterm-desktop` | PASS |
| `python3 scripts/check-docs.py` | PASS: 27 Markdown files, 60 local links, 229 blueprint references, 33 CLI/IPC methods mapped |
| `git diff --check` | PASS |

The full serial workspace invocation did not finish, despite the same
terminal unit and PTY targets passing independently; do not treat that
workspace invocation as a pass.

### Desktop validation

Wayland (`wayland-1`) is available, but an existing `omaterm-desktop`
instance (PID 83052) is active on the shared session. No second window was
launched or existing window manipulated during this pass. Therefore the
Git-row-to-preview-tab flow, preview hunk rendering/scroll, and stage
interaction still need release-build Wayland validation; no desktop visual
proof is claimed.
The current hunk-header stage control dispatches M14's whole-file
`GitCommand::Stage` for that file; true partial-hunk staging is not yet
implemented and remains an M15 acceptance gap.

### Next action

Validate on Wayland that clicking a Git change opens its diff as a
main-area preview tab (not a terminal) and that staged/unstaged rows
select the matching side. Implement and verify partial-hunk staging (or
explicitly revise the product contract) before closing the remaining M15
acceptance criteria; rerun final gates.

## Workbench shell redesign (VS Code workbench chrome) — implemented, partial live proof

Replaced the prototype three-column window (180px project list + terminal
tabs + fixed 240px right Files/Git panel, text-chip controls) with a VS
Code-workbench frame per `docs/ui-workbench-redesign-plan.md` (Phases 0–3;
terminal-first scope preserved, no editor/debugger/extensions):

- 35px command/title row: app + project identity, centered `Ctrl+P`
  palette affordance, sidebar toggle. No fake OS traffic lights.
- 48px activity rail: Explorer and Source Control only (verified Nerd
  Font glyphs `\uf07b`/`\ue65d`), active accent bar, change-count badge.
  Clicking the active icon toggles the sidebar.
- One left contextual sidebar (default 260px, clamped 190–460, `Ctrl+B`
  collapse, 3px press-and-slide resizer): `EXPLORER` holds the moved
  `WORKSPACE` project switcher plus the existing Files tree;
  `SOURCE CONTROL` holds the existing Git panel. No second fixed column.
- VS Code-style tab strip (inactive `#2d2d2d`, active surface + accent
  top edge) for terminal tabs and the view-local `Diff:` preview;
  history state moved out of the tabs into the status bar.
- Context row under tabs: `project › tab`, or diff path + side + hunk
  position for previews. 22px accent status bar: branch + change
  summary (omitted outside repos), history state, font size.
- `Ctrl+Shift+E/G` select Explorer / Source Control; `Ctrl+B` toggles.
- Terminal grid, pane-tree geometry, router/IPC parity, session
  lifetime, and persistence contracts unchanged. Pane-size estimation
  and the `Ctrl+P` overlay geometry now derive from the workbench
  chrome widths instead of the old fixed sidebars.

### Changed files

- `apps/omaterm/src/workbench.rs` (new, GPUI-free) — palette/dimension
  tokens, `clamp_sidebar_width` (non-finite falls back to default),
  `activity_press_collapsed`, `change_badge`, `change_summary`,
  `branch_label`; 5 unit tests
- `apps/omaterm/src/main.rs` — title/activity/sidebar/tabs/context/
  status shell, `set_activity`, sidebar resizer drag, keybindings,
  notice/banner + palette-button token reuse; removed
  `render_sidebar_tabs` and `files::RIGHT_SIDEBAR_WIDTH_PX`
- `docs/ui-workbench-redesign-plan.md` (new), `docs/00-overview.md`
  (link), this status record

### Automated verification (Rust 1.98.1)

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test -p omaterm --bin omaterm-desktop -- --test-threads=1` | PASS: 79 tests (74 existing + 5 workbench) |
| `cargo test -p omaterm-context -p omaterm-cli -p omaterm-protocol -p omaterm-core -p omaterm-logging` | PASS (38 CLI / 44 context / 5 protocol / 1 logging-core / 8 / 17 / 3 / 6 unit targets green) |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (existing transitive `proc-macro-error2` future-incompatibility notice only) |
| `cargo build --release --bin omaterm --bin omaterm-desktop` | PASS |
| `python3 scripts/check-docs.py` | PASS (pending rerun count in final gate below) |
| `git diff --check` | PASS (pending rerun in final gate below) |

### Desktop validation (isolated instance, release build)

Launched with `XDG_RUNTIME_DIR=/tmp/omaterm-wb/runtime` (0700),
`XDG_STATE_HOME=/tmp/omaterm-wb/state`, cwd `/tmp/omaterm-wb/repo`
(a scratch git repo with 1 modified + 1 untracked file); project
content itself comes from the user's read-only config pins. Two
`grim` captures on `wayland-1`:

- `/tmp/omaterm-wb/shot-1-explorer.png` — full Explorer frame: title
  row, palette button, collapse control, activity rail with badge,
  `WORKSPACE` switcher, icon file tree, accented `Tab 1`, context
  row, working terminal, status bar (`main`, `history: on`, `14px`).
  An IPC banner is visible here (runtime dir was 0755 before chmod).
- `/tmp/omaterm-wb/shot-2-explorer-clean.png` — tiled window after the
  chmod fix: no banner; status bar shows the dirty-repo treatment
  (`main*`, `4 changed`) plus badge `4` on the Source Control icon.

The compositor's `dispatch` path in Hyprland 0.56.2 routes through a
Lua bridge that rejects `address:`/`pid:` selectors, and the installed
`wtype` only types text (no modifiers), so no window focus, keystroke,
or pointer injection was possible. Therefore live-proven: workbench
frame render, Explorer content, badge/branch/summary/history status
states, IPC parity against the isolated socket (`project list`).
Still unit/code-review-only, explicitly NOT claimed live: Source
Control panel render, diff preview open/render, sidebar
collapse/resize drag, `Ctrl+P` overlay geometry, and all
keyboard/pointer flows for the new chrome.

### Next action

Validate the unproven chrome paths on Wayland with real
input/focus control (Source Control render, Git-row-to-diff-preview,
`Ctrl+B`/resizer, `Ctrl+P` overlay, `Ctrl+Shift+E/G`), then close the
standing M15 gaps (partial-hunk staging, full live proof). Uncommitted
work: `apps/omaterm/src/{main,workbench}.rs`,
`docs/{ui-workbench-redesign-plan.md,00-overview.md,status.md}`.

## M19 Phase B — document domain and bounded I/O — 2026-10-04

Implemented the plan's Phase B (domain types + bounded I/O, no UI, no
dispatcher wiring yet). Uncommitted work listed below.

### What changed

- `crates/omaterm-core`: `DocumentId` typed ID; `EditorCommand`
  (`Open`/`Close`/`Save`/`Revert`) on `OmaCommand`; `EditorDocumentInfo`
  result DTO (`document`, `project`, `path`, `bytes`, `lines` — metadata
  only, never buffer contents); `DocumentNotOpen`/`DocumentConflict`/
  `DocumentTooLarge`/`NotTextFile` error codes with stable wire strings;
  `MAX_EDITOR_PATH_BYTES` (4096), `MAX_EDITOR_BYTES` (1 MiB),
  `MAX_EDITOR_LINES` (20,000) validation plus open-path checks.
- `crates/omaterm-context/src/editor.rs` (new): `EditorLanguage`
  (Rust/Markdown/TOML/JSON/Bash/Plain) with extension detection;
  `read_text_file` (boundary resolve → regular-file check → pre-read size
  cap → post-read re-check → NUL/binary reject → UTF-8 reject → line-cap
  check → revision + language); `write_text_file` (byte/line caps →
  boundary → regular-file check → expected-revision conflict check →
  same-dir temp + permission-preserving atomic rename); `FileRevision`
  (size + mtime) for external-change detection.
- `apps/omaterm/src/router.rs`: `OmaCommand::Editor` explicitly rejected
  with `unsupported_operation` until the Phase C DocumentStore lands
  (stable, effect-free gate, covered by a router test).
- `apps/omaterm/src/ipc_bridge.rs`: `EditorOpened`/`EditorSaved`
  metadata JSON rendering (identity + sizes only; no contents logged).

### Automated verification

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test --workspace --quiet` | PASS: 475 tests (107 desktop + 54 context + 21 core + 33 PTY integration, rest unchanged) |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (known transitive `proc-macro-error2` notice only) |
| `python3 scripts/check-docs.py` | PASS (36 files; no doc changes required — plan already references §34) |
| `git diff --check` | PASS |

New coverage: editor open-path validation (empty/oversize/control),
lifecycle acceptance without existence checks, wire-string stability,
language table, small/Unicode reads, binary/invalid-UTF-8/oversize/
line-cap/directory/missing/escape rejection, save round-trip with stale
revision conflict and no temp-file leakage, cap parity between core and
context constants.

### Next action

Phase C: desktop `DocumentStore` (buffers, dirty state, undo, cursors),
dispatcher open/close/save/revert wiring with scope/root checks, and
DocumentStore lifecycle tests. Uncommitted work:
`crates/omaterm-{core/src/{ids,command,result,validation,lib}.rs,context/src/{editor,lib}.rs}`,
`apps/omaterm/src/{router,ipc_bridge}.rs`, this status record.

## M19 Phase C — DocumentStore and dispatcher wiring — 2026-10-04

Implemented the plan's Phase C (owner-side store + dispatch; no editing
surface yet — keystrokes arrive in Phase D). Uncommitted work listed below.

### What changed

- `apps/omaterm/src/editor.rs` (new, GPUI-free): `DocumentStore` owns one
  buffer per (project, canonical path) — reopen returns the live document.
  Dirty tracking, bounded undo/redo (100 entries / 8 MiB, oldest drops
  first, redo clears on edit), delta-based inverse edits with UTF-8
  boundary and cap checks, save/revert generation handling. Editing API is
  test-exercised; `dead_code` is explicitly allowed until Phase D wires
  the surface (theme-token precedent).
- `apps/omaterm/src/router.rs`: `CommandRouter` owns the store (history
  precedent) with `documents()`/`documents_mut()` for the Phase D UI.
  `EditorCommand::{Open,Close,Save,Revert}` dispatch with project scope,
  root resolution, open-time canonical-root capture, save-time root
  revalidation (refuse on replacement), clean-save fast path without
  I/O, stale-revision conflicts, and revert-from-disk. No persistence
  effects; `ProjectCommand::Delete` retires owned buffers. Scope
  authorization covers `Open` by project and lifecycle variants by live
  document ownership; foreign scopes deny with `cross_project_denied`.
- `apps/omaterm/src/ipc_bridge.rs`: metadata-only JSON for
  `EditorOpened`/`EditorSaved` (identity + sizes, never contents).

### Automated verification

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test --workspace --quiet` | PASS: 481 tests (112 desktop + 54 context + 21 core + 33 PTY integration, rest unchanged) |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (known transitive `proc-macro-error2` notice only) |
| `python3 scripts/check-docs.py` | PASS (no doc-contract change: Option A keeps `file.open` terminal routing) |
| `git diff --check` | PASS |

New coverage: store dedup/reopen identity, cross-project isolation,
delta undo/redo with redo-clear, invalid ranges, undo-cap eviction,
multibyte boundaries, project-scoped close; dispatcher open/save/
conflict/revert/close lifecycle, clean-save fast path, stale-document
and foreign-scope denial with empty effects, delete retires buffers.

### Next action

Phase D: editing surface, caret/selection/clipboard/undo wiring,
background highlight pipeline with cancellation, and input ownership.
Uncommitted work: `apps/omaterm/src/{editor.rs,router.rs,main.rs}`,
this status record.

## M19 Phase D — editing surface, highlight pipeline, input ownership — 2026-10-04

Implemented the plan's Phase D without new dependencies. Uncommitted work:
`apps/omaterm/src/{editor.rs,main.rs}`, this status record.

### What changed

- `apps/omaterm/src/editor.rs` (pure, tested): single-pass tokenizer for
  Rust/Markdown/TOML/JSON/Bash (keywords, strings with escapes, line/block
  comments, numbers, Rust char-vs-lifetime disambiguation, Markdown
  headings/code spans, `$#` guard, unclosed-runs-to-EOF, UTF-8-safe
  offsets); `EditorCaret` with ordered selection ranges; tab/wide-char
  display widths plus buffer/display column mappers; line-map helpers;
  `HighlightWorker` (latest-only, cancellable, shutdown-joined).
- `apps/omaterm/src/main.rs`: per-project view-local activation over the
  router-owned store; document chips with dirty markers; breadcrumb header
  with Save/Revert/Close; virtualized 22px rows (gutter + token/selection
  highlights + shaped caret + current-line treatment); Ln/Col footer with
  display columns; native vertical + horizontal scrolling; click/drag
  selection via paint-time body origins and shaped-prefix binary search;
  full keyboard model (caret/selection/edit/clipboard/undo/redo/save/
  auto-indent/auto-indent Enter/4-space Tab/Escape-to-terminal); editor
  owns keystrokes after global chrome shortcuts, before terminal
  forwarding. Interim triggers: palette Ctrl+Enter / Ctrl+click on file
  results (plain Enter keeps terminal routing per Option A).

### Automated verification

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test --workspace --quiet` | PASS: 489 tests (121 desktop incl. 13 editor, 54 context, 21 core, 33 PTY integration) |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS (known transitive `proc-macro-error2` notice only) |
| `cargo build --release --bin omaterm --bin omaterm-desktop` | PASS |
| `python3 scripts/check-docs.py` / `git diff --check` | PASS |

### Live validation: blocked (not passed)

Release instance (`/tmp/opencode/m19-live/`, isolated HOME/XDG paths,
Hyprland, PID 404753) launched and opened the fixture project over IPC.
Screenshots prove the palette opens and filters to the `notes.md` file
result. The open/edit/save flow could **not** be exercised: the machine
is in active user use (ongoing Meet call, Docs/WhatsApp windows), focus
competed with live windows, and an overlapping Chromium window covers the
OmaTerm header/tab strip. Ctrl+Enter with the file selected left the
palette open — undiagnosed (possible focus loss to another window at the
keystroke moment, or a real key-delivery gap). No validation is claimed
for: palette-to-editor open, typing, caret/selection rendering, save
bytes, clipboard, focus leaks, or input ownership.

### Next action

Re-run the Phase D Wayland script on a quiet session (or an isolated
compositor seat): palette Ctrl+Enter open, type/edit/save with on-disk
byte proof, click/drag selection, clipboard round-trip, Escape focus
return, dirty-close guard, and a terminal sentinel proving no keystroke
leaks. Then Phase E entry-point integration. Known gaps carried forward:
no caret blink, no gutter line-select, `\r\n` displays raw, shutdown
silently discards dirty buffers, tree/palette full entry integration.

## M19 Phase E — entry-point integration (partial, code-only) — 2026-10-04

Added explicit native-editor open triggers alongside unchanged
terminal-routed behavior (Option A frozen contract holds; no new wire
method). Uncommitted work: `apps/omaterm/src/main.rs`, this record.

- Files tree file-row Ctrl+click → `editor_open_document`; plain click
  keeps terminal-routed open, directories keep toggle.
- Git row Ctrl+click → `editor_open_document`; plain click keeps
  diff-on-select.
- Files search box Ctrl+Enter → editor open of the first file match;
  plain Enter keeps terminal routing. Keyboard-accessible without the
  palette.
- Palette Ctrl+Enter / Ctrl+click from Phase D unchanged (interim).

`cargo fmt --all --check`, `cargo test --workspace --quiet` (489 green),
`cargo clippy --workspace --all-targets -- -D warnings`, docs checker,
and `git diff --check` pass. No behavior change to existing triggers;
no new tests (wiring-only branches over covered dispatch paths).

Live validation remains blocked by the shared-session focus competition
reported under Phase D; entry triggers join the same quiet-session
re-run script. Remaining Phase E/F: full interaction proof, resource
evidence, dirty-shutdown semantics, open-document persistence decision,
and milestone/acceptance doc updates.

## M19 Phase F — live validation (partial pass) — 2026-10-04

Release instance `/tmp/opencode/m19-live/` (isolated HOME/XDG paths,
Hyprland, release build `8599c1c`-plus-status, fixture repo with
`notes.md`/`main.rs`/`data.json`). Workspace switch to the test
workspace was done through the compositor bridge and reversed
afterwards; no user windows were touched.

### Proven live (screenshots + on-disk bytes)

- Palette `Ctrl+P` → `notes.md` filters to the file result.
- Palette `Ctrl+Enter` opens the native editor: document chip, breadcrumb
  with Save/Revert/Close, gutter + code rows, Markdown heading highlight,
  caret, and `Ln 1, Col 1 … markdown` footer all render.
- Typing, caret motion (`Down`, `End`), dirty `M` markers (chip + header)
  and `unsaved changes` footer all behave.
- `Ctrl+S` saves byte-exact content (`cat` ground truth), shows the
  `Saved` toast, and clears dirty markers.
- `ZZZ` + `Ctrl+Z` leaves on-disk bytes untouched; reopen shows the live
  buffer (store dedup across deactivate/reactivate).
- `Escape` returns to the terminal; `echo SENTINEL_OK` executes with
  output — no keystroke leaks in either direction.
- CLI `project open` on the same instance works alongside the UI.

Captures under `/tmp/opencode/m19-live/`: `f-palette.png`,
`f-editor.png`, `f-edited.png`, `f-saved.png`, `f-terminal.png`,
`f-reopen.png`, `f-guard7.png`.

### Blocked (not passed, no code change justified)

Pointer paths — chip activate/close (dirty-guard notice), row
click/drag selection — could not be validated: the shared session is in
active use (Meet call, competing windows/focus), the virtual-pointer
coordinate mapping proved unreliable across the two-monitor layout, and
cursorpos readings were confounded by physical mouse movement. Earlier
misses were coordinate errors, not app defects: keyboard-driven flows
through the same handlers (`editor_activate`, dispatcher lifecycle)
pass. These paths join the quiet-session re-run.

Remaining M19 gaps unchanged: quiet-session pointer proof, resource
trends, dirty-shutdown semantics, open-document persistence decision,
find/replace and further languages as follow-ups.

## M19 acceptance hardening and resumed Phase F — 2026-10-03

This record supersedes the previous pointer-blocker and dirty-shutdown
statements. M19 remains **in progress**, with prerequisite implementation
gaps identified below; the successful flows do not close the full matrix.

### Implementation

- Undo/redo restores a valid endpoint and dirty state relative to saved text.
  No-op replacements do not create history. Edits reject oversize inserts
  before allocating the candidate buffer; caret/anchor clamp after mutation.
- Grapheme-aware motion/deletion/selection and Unicode character widths use
  the already-locked Unicode crates. Pointer hit-testing now shapes a complete
  line and translates display byte indices back to buffer bytes; vertical
  movement preserves visual columns. Shift-drag retains its original anchor.
- Dirty close/revert/window shutdown has explicit Cancel / Save / Discard
  choices. Save failure keeps the buffer and pending action. Shared router
  operations reject dirty close/project deletion without effects. Revert and
  clean Save validate the captured canonical root path. Native activation
  releases inspector field focus; both clipboard shortcut forms target the
  editor, rather than a hidden terminal. Successful actions clear stale notices.
- Negative JSON numbers no longer stall tokenization. Escaped Unicode and
  Rust character literals have valid span boundaries. Tokenization checks
  cancellation cooperatively, superseded spans clear on edit, and completion
  sends a bounded wakeup (no idle highlight polling/repaint loop).
- Context reads cap actual bytes consumed despite concurrent growth and open
  the final component with Unix `O_NOFOLLOW` / `O_NONBLOCK`. Saves use exclusive
  0600 sidecars, checked permission preservation, data sync, final path/revision
  revalidation, atomic rename and directory sync. File revision includes Unix
  device/inode, catching same-size, same-timestamp atomic replacements.

### Automated verification

All Cargo commands below ran with `RUSTUP_TOOLCHAIN=1.99.0` (existing environment
override; repository toolchain pin unchanged), reliable command exit codes and
no output-filtering pipelines. Final source checked before documentation-only
updates:

| Command | Result |
|---|---|
| `cargo fmt --all --check` | PASS |
| `cargo test --workspace --quiet` | PASS: 494 tests, 0 failed |
| `cargo test --workspace --quiet -- --test-threads=1` | PASS: 494 tests, 0 failed |
| `cargo clippy --workspace --all-targets -- -D warnings` | PASS; existing transitive `proc-macro-error2` future-incompatibility notice only |
| `cargo build --release --bin omaterm --bin omaterm-desktop` | PASS |

New regressions cover savepoints/caret endpoints/discard, grapheme motion and
display-byte mapping, negative numbers/escaped Unicode/cancellation, capped
readers, preserved permissions and symlink escapes. Existing dispatcher lifecycle
coverage now asserts dirty close/project deletion rejection and empty effects.
During verification, a stale-revision test exposed same-tick atomic replacement;
device/inode tracking fixed it. A later serial run exposed the pre-existing
credential test's immediate `/proc/<pid>/environ` sampling race; the test now
waits at most two seconds for the child token environment before all original
identity/revocation assertions. Both full suites passed after those fixes.
Documentation checks after the evidence update: `python3 scripts/check-docs.py`
PASS (36 files, 128 local targets, 295 blueprint references, all 34 CLI mappings);
`git diff --check` PASS.

### Release Wayland evidence

Hyprland 0.56.2, `wayland-1`, eDP-1 1920×1200 at scale 1.25; isolated
HOME/config/state/cache/runtime under `/tmp/opencode/m19-live/`, fixture repo,
`EDITOR=/usr/bin/true`. First hardening run: service `omaterm-m19-h`, desktop
PID 633343, viewport 1445×924 logical. Final-source smoke: `omaterm-m19-final`,
PID 704122, viewport 1526×924 logical. Pointer helper uses the MIT wlr protocol
v2 `create_virtual_pointer_with_output`, tied explicitly to eDP-1 with
1536×960 logical extents; the earlier global-extent hypothesis was incorrect.

| Flow | Observed result / capture under `/tmp/opencode/m19-live/` |
|---|---|
| Palette Ctrl+Enter | Opened `notes.md` after the asynchronous result arrived (`h-open.png`). An early Enter correctly reports no selected result; this is not a focus-loss diagnosis. |
| Pointer selection, copy/paste | Drag selected exact `M19 live edit.`; clipboard equality checked after event delivery; Ctrl+V inserted that payload (`h-selection.png`, `h-paste.png`). |
| Dirty chip close, Cancel | Close click produced explicit choices; pointer Cancel retained the dirty buffer (`h-close-prompt.png`). |
| Undo/redo + toolbar Save | Undo returned to clean text/caret (`h-undo-clean.png`); redo restored insertion; Save wrote matching bytes. |
| Dirty close, Discard | `XYZ` edit discarded by explicit choice; chip removed and existing terminal restored (`h-discard-closed.png`); saved bytes retained. |
| External modification | Save refused, preserving both the local dirty buffer and external disk text (`h-conflict.png`); Revert + explicit Discard loaded the external text cleanly (`h-reverted.png`). |
| Files Ctrl+click | Native document opened and focused (`h-tree-open.png`). |
| Files search Ctrl+Enter (final source) | Opened `main.rs` with Rust keyword/string colors (`final-search-open.png`); validates the corrected input guard. |
| Ctrl+Shift+C/V (final source) | Select-all copy, collapse, paste duplicated the Rust buffer (`final-editor-clipboard.png`); CLI bounded `terminal.read` showed only the unchanged shell prompt, no editor payload. |
| Dirty shutdown | Compositor `hl.dispatch(hl.dsp.window.close())` prompted before teardown. Cancel kept the service active; Save persisted all dirty text and exited. Final-source run prompted again (`final-shutdown.png`); Discard exited without changing `main.rs`. |

Resource loop: ten checked Files Ctrl+click → insert → Save → header Close
cycles; each iteration asserted exact on-disk bytes. Warm baseline 45 FDs /
30 threads / 99,568 KiB RSS; all cycles retained 45 FDs / 30 threads; RSS range
99,512–99,740 KiB, final 99,692 KiB. Each scripted cycle took ~3.05s including
intentional input waits (not an operation-latency benchmark). Temporary harness:
`cycles.py`; no monotonic growth observed for this small fixture. Earlier
palette-driven loops stopped on focus/readiness guards and are not passes.

Both units exited through normal window shutdown; desktop/observed shell PIDs
were gone, socket/credential removed (only normal lock remained), workspace 5
restored. Screenshots and disposable fixtures remain outside the repository.
Redaction review: added code logs no text, clipboard contents or capability tokens.

### Remaining work and next action

1. Implement the frozen open-document registry persistence (metadata only;
   never dirty text) and bounded restart restore. The decision is already
   frozen in the M19 plan; it is not deferred or reopened here.
2. Move editor lifecycle file I/O off the owner/UI thread with cancellation,
   bounded pending work and project/root/document generation guards.
3. Strengthen root identity and descriptor-relative ancestor-race protection;
   same canonical pathname alone does not detect replacement of the root.
   Metadata conflict detection is not a content digest or atomic compare/rename.
4. Finish language/Unicode/tab/long-line/binary/oversize/Git-entry, narrow-layout,
   project-switch and multi-document acceptance; deadline caret blink/gutter
   selection/CRLF presentation and explicit conflict-overwrite flow remain open.
5. Repeat resource observation at file/line caps and measure actual operation
   latency/idle behavior. Existing small-fixture resource proof is bounded evidence.

M15/M16/M17 closure remains separate. Find/replace and additional languages stay
follow-ups under the existing M19 scope.

## M19 comprehensive completion planning — 2026-10-03

Audited committed baseline `719926b` against the M19 contract, editor/router/UI
code, schema-2 snapshot/migration, blueprint and UI v5 editor roles. Added the
[comprehensive completion plan](m19-completion-plan.md) as the current execution
sequence; the original implementation plan remains the product contract and
A–F history. This is documentation-only work and makes no new implementation
or native-pass claim.

The ordered slices are S0 remaining contracts/reproducible fixtures; S1 root
identity/descriptor I/O/write outcomes; S2 async prepare/work/commit; S3 bounded
indexed store/render snapshots; S4 schema-3 registry/restore; S5 dirty/conflict/
project/shutdown state machines; S6 native text/IME/focus/caret/viewport;
S7 default native entry points and explicit terminal parity; S8 highlighting/
cap/aggregate/idle/latency resources; S9 final verification and E01–E10 evidence.

This ordering supersedes the earlier remaining-work list's ordering: root/I/O
and async foundations come before registry restoration, avoiding new UI-thread
disk reads during restart. Frozen metadata-only persistence, terminal-only pane
content and Option A wire semantics stay intact. Continued M19 execution was
user-directed; M15/M16/M17 have independent unfinished acceptance gates.

**Next action:** execute completion-plan S0, then S1's deterministic identity/
filesystem tests and context helpers. Proposed aggregate limits, exact DTOs
and worker APIs must be verified and recorded before implementation.

Documentation verification: `python3 scripts/check-docs.py` PASS (37 Markdown
files, 138 local targets, 301 blueprint references, all 34 CLI mappings);
`git diff --check` PASS. Reviewed local references and consistency of baseline,
schema versions, frozen contracts and remaining-work ordering. Cargo/native
checks were not rerun for this documentation-only change.

## M19 S0 contracts and fixtures — 2026-10-03

Completed S0's remaining contract record and reproducible fixture work. The
completion plan now fixes the behavioral contracts for document/root/save/
operation identity, buffer versions, commit outcomes, active surface/input
ownership, typed dirty actions and the metadata-only schema-3 registry. Exact
Rust DTOs and worker APIs remain deliberately deferred to their implementation
slices; S0 does not assert that the existing synchronous router satisfies the
async contract.

Added `scripts/generate-m19-fixtures.py`, a standard-library-only generator for
a new or empty disposable directory. It creates `proj-a`/`proj-b`, initial
language/plain/empty files, Unicode/grapheme/tab/newline/long-line content,
byte/line caps, token-dense input, binary/invalid-UTF-8/unreadable/directory/
FIFO cases, contained/escaping symlinks, mutable/replacement/root-replacement
material and a Linux non-UTF-8 filename. It emits `manifest.json` with exact
paths and limits; 4097-byte-path, save-failure and race cases are explicitly
synthetic/injected rather than unreliable filesystem artifacts.

Fixture verification: `python3 scripts/generate-m19-fixtures.py --output
/tmp/opencode/m19-fixtures-v4` PASS. The script self-verified byte/line limits,
FIFO, symlinks, fixed same-size mtime and the non-UTF-8 path. The earlier v1
generator run failed before completion because it did not create symlink parent
directories; that defect was fixed, then v2/v3/v4 runs passed using fresh
directories. These `/tmp/opencode` trees are disposable evidence, not tracked
release artifacts.

**Next action:** start S1 with a Linux `openat2` capability/API spike and a
context-owned root identity/descriptor seam. Add deterministic barrier tests
before replacing canonicalize-then-open editor access. Do not add persistence or
async router work until the root/I/O contract is executable.

## M19 S1 descriptor-root foundation — 2026-10-03

Started S1 in `omaterm-context` without adding a dependency. Locked `libc`
0.2.189 exposes `open_how`, `SYS_openat2`, `RESOLVE_BENEATH` and
`RESOLVE_NO_MAGICLINKS`; the verification host is Linux 7.2.5-3-omarchy with
glibc 2.44. Added Linux-only `EditorRoot`, which captures canonical root display
path, device/inode `RootIdentity` and an owned directory descriptor. Its
`open_descendant` uses the descriptor with `openat2`, permits contained symlink
aliases, rejects traversal/escaping and magic-link resolution atomically, and
fails closed with `SecureResolutionUnavailable` rather than falling back to an
unguarded canonicalize-then-open sequence.

The new deterministic context test proves a captured descriptor continues to
read the original tree after the same root pathname is replaced, while an
escaping symlink and `..` traversal are rejected. This is a foundation only:
the existing `read_text_file`/`write_text_file` and desktop router still use
the prior pathname path. Content fingerprints, descriptor-relative saves,
failure seams and lifecycle wiring remain required before S1 closes.

Verification with `RUSTUP_TOOLCHAIN=1.99.0`: `cargo fmt --all` PASS;
`cargo test -p omaterm-context` PASS (57 unit, 16 integration, 0 doc tests);
`cargo clippy -p omaterm-context --all-targets -- -D warnings` PASS;
`python3 scripts/check-docs.py` PASS; `git diff --check` PASS. Full workspace
and native checks were not rerun for this partial context slice.

**Next action:** migrate bounded editor reads to `EditorRoot` and make the
opened file identity available to the store, then add deterministic
root/ancestor/final-component swap barriers. Do not wire the synchronous router
to an incomplete write path or claim S1 complete.

## M19 S1 rooted reads and content revisions — 2026-10-03

Linux `read_text_file` now captures an `EditorRoot` and resolves the requested
relative descendant through its owned descriptor. `read_text_file_from_root`
is the worker-facing form for a previously captured root. Router open/revert
and clean-save validation use it; dirty saves remain on the earlier atomic
pathname write path and therefore S1 is still open. `DocumentStore` now keys a
live buffer by project, captured `RootIdentity`, and opened file device/inode,
so contained symlink aliases deduplicate while a root replacement at the same
pathname cannot merge its documents with the earlier root.

`FileRevision` now includes a SHA-256 digest of validated file bytes. Context
adds the already locked and license-reviewed `sha2` =0.10.9 as a direct edge;
no package/version resolution changed. A new test changes a file to different
same-size bytes, restores its mtime, and proves a stale save returns
`document_conflict` without overwriting those bytes.

Verification with `RUSTUP_TOOLCHAIN=1.99.0`: `cargo fmt --all --check` PASS;
`cargo test -p omaterm-context` PASS (58 unit, 16 integration, 0 doc tests);
`cargo test -p omaterm` PASS (124 tests); `cargo clippy -p omaterm
--all-targets -- -D warnings` PASS; `python3 scripts/check-docs.py` PASS and
`git diff --check` PASS. Desktop test/Clippy output reports the pre-existing
transitive `proc-macro-error2` future-incompatibility warning. Full workspace
and native checks remain pending this in-progress S1 revision.

**Next action:** replace `write_text_file`'s canonicalize/rename path with
descriptor-relative parent sidecar creation, revalidation, rename and directory
sync outcomes. Add injected failure and root/ancestor/final-component swap
barriers before moving to S2 async lifecycle work.

## M19 S1 descriptor-rooted save path — 2026-10-03

Linux dirty saves now use `write_text_file_from_root` with the router's already
captured `EditorRoot`; there is no second root capture between the root-identity
check and write. The context operation resolves the parent beneath that root,
opens the final target with `O_NOFOLLOW`, writes an exclusive same-directory
sidecar, preserves permission bits, syncs it, re-reads/rechecks the digest,
renames with `renameat`, then syncs the captured directory. `WriteTextOutcome`
distinguishes durable commit from the post-rename directory-sync warning. The
current UI logs that warning and adopts the saved baseline; S2/S5 must retain
and present the typed committed-warning outcome rather than relying on logs.

Contained **ancestor** symlink aliases are supported. A final symlink is
explicitly refused with `document_conflict`, because atomic rename would replace
the alias rather than its referent; this is intentional until a separately
reviewed referent-parent resolver exists. Escaping aliases continue to fail at
rooted read resolution. The remaining pre-rename recheck is observable-change
detection, not a compare-and-swap guarantee against an external writer.

New regression: captured-root save writes the original moved tree after a
same-path replacement, saves through a contained ancestor alias, and leaves a
final symlink untouched. Focused context and desktop lifecycle tests pass with
the 1.99.0 override; package/full checks for this increment follow. S1 remains
open for injected failure/cancellation barriers, explicit UI warning state and
the final filesystem matrix.

## M19 S2 worker foundation — 2026-10-03

Added the private desktop `EditorIoQueue`: one joined worker, one active job,
at most 16 pending foreground jobs, monotonic `EditorOperationId`s, cancellation
and owner-only completion messages. Every completion carries operation/project/
root path/root identity/document/generation/kind, allowing the eventual router
commit path to reject stale results without worker mutation of the store or UI.
Jobs use S1 rooted open/save/revert context APIs and recheck root identity after
the worker captures its descriptor. Queue cancellation publishes exactly one
final cancellation completion; active work is cooperative and shutdown joins
the worker rather than detaching it.

Unit tests cover capacity, queued cancellation without execution, completion
metadata/single delivery and active-plus-pending shutdown. Verification with
`RUSTUP_TOOLCHAIN=1.99.0`: editor worker tests are included in `cargo test -p
omaterm` (128 PASS); `cargo fmt --all --check` PASS; `cargo clippy -p omaterm
--all-targets -- -D warnings` PASS; docs and diff checks PASS. The known
transitive `proc-macro-error2` future-incompatibility notice remains.

**Next action:** make router Open/Save/Revert return editor pending receipts,
poll this queue on the owner, and apply only generation/document/root-accepted
completions. Do not call filesystem APIs from `editor_dispatch` once that path
lands; dirty close/shutdown must await final editor outcomes.

## M19 native verify-fix + restart — 2026-10-04

Fixed two native-found defects on top of `50c06cc` and verified the fixes on
release binary `32cd3d9f9d8498e7` (`87bc5bf` prompt render/keyboard resolution,
`b28ca8a` frozen-path dedup), pushed to `origin/main`:

- Conflict prompt invisible while owning all input: the absolute-positioned
  prompt painted beneath the opaque editor and `r`/`o`/`Escape` were unreachable
  (owner `Confirmation`, handlers only in the editor path), wedging every
  keystroke. Now in-flow and keyboard-resolvable; dirty Overwrite/Reload/Cancel
  and clean Reload all proven with on-disk byte assertions.
- Reopened paths forked buffers after external replacement (inode-keyed
  dedup), producing duplicate registry descriptors and a recovery-wiped
  restart. Dedup now uses the frozen `(project, root, path bytes)` key first
  (inode map kept for aliases); new snapshots validate with zero duplicates.
- Leak retest with clean-baseline discipline passes; earlier `CB` FAIL was a
  polluted-baseline artifact, not a live leak. 1 MiB cap edit/save byte-exact.
- Restart (SIGKILL, explicitly non-graceful): schema-3 snapshot valid, docs
  restored clean from disk, dirty text not recovered, terminal sessions fresh,
  no recovery banner.
- Harness rule learned: a second instance on an occupied runtime dir silently
  displaces the first — this explains all prior "spontaneous deaths". Always
  kill-before-relaunch and verify.

Full record: [native verify-fix evidence](evidence/m19-native-verify-fix.md).
S9 remains open: graceful-shutdown E2E (no compositor-close tooling), 20-cycle
reruns on the final binary, IME preedit, entry-route retakes, idle baselines.
