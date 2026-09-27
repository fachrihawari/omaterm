# Implementation Status

## Current position

Milestones 1–3 are complete. Milestone 4 is implemented and verified; see the
M4 evidence below.

| Milestone | Status | Verification evidence | Blockers | Next action |
|---|---|---|---|---|
| 1 — GPUI Boot | complete | Build, quality checks, CI workflow, and Wayland visual/close checks recorded below | X11 runtime session unavailable; build coverage passes | Begin M2 pure pane tree |
| 2 — Pane Tree | complete | 9 core tests, workspace quality gates, and Wayland manual validation recorded below | None | Begin M3 single terminal |
| 3 — Single Terminal | complete | Completed `a3923d4`; release-build interaction pass approved 2026-09-26 with explicit IME/unavailable-program limits (see M3 completion record) | None | M4 regression coverage |
| 4 — Multi Terminal | complete | 64 terminal unit + 20 PTY integration tests; workspace quality gates and Wayland four-pane pass recorded below | Thread/memory/GPU stress baselines remain for broader lifecycle work | Begin M5 Projects/Tabs |
| 5 — Projects/Tabs | in_progress | Core hierarchy, coordinator lifecycle, full serial workspace suite and quality gates pass; user confirms main Wayland M5 workflows | Manually exercise startup Retry; confirm latest full-chip hit target; resource observations are qualitative only | Finish M5 manual checks before M6 completion |
| 6 — Persistence | in_progress | State crate tests and workspace compile/clippy pass; desktop restart validation pending | M5 has two remaining manual checks; recovery/shutdown Wayland validation pending | Complete restore and autosave validation |
| 7 — Command Router | not_started | None | Requires M6 | Unify application actions |
| 8 — IPC | not_started | None | Requires M7 | Implement scoped bounded transport |
| 9 — CLI | not_started | None | Requires M8 | Complete CLI/desktop proof flow |

## Handoff rules

Use `not_started`, `in_progress`, `blocked`, or `complete`. Record exact commands,
results, platform/toolchain, and relevant commit or files. Record manual desktop
checks separately. An unavailable check is pending, not a pass. Mark complete only
when acceptance criteria have evidence.

For each handoff record completed behavior, changed files, check results,
reproducible blockers, approved deviations, and the smallest next action.

The [acceptance matrix](acceptance-matrix.md) tracks release requirements. Planned
tests in milestone documents are not evidence of implemented application behavior.

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

## Milestone 5 — Projects & Tabs — implementation in progress

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
chrome polish. A later full-chip click-target adjustment was added after the
user reported that inactive tabs only responded on the label; that final hit
area still needs confirmation. For resource observation, RSS did not drop
after closing most projects/tabs, but remained unchanged through a repeated
create/remove cycle; no numeric readings or thread/GPU measurements were
provided. This is a stable high-water observation, not evidence of cycle-over-
cycle growth. The shell-start failure Retry state has not been manually
exercised; keep that edge case recorded as unverified. M5 is functionally
validated with this explicit edge-case limit.

## Milestone 6 — Persistence — implementation in progress

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
- `crates/omaterm-terminal/src/workspace.rs`
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

### Pending completion evidence

- Exercise restart, fresh shell PIDs, saved CWD, CWD fallback, recovery warning,
  primary/recovery-file retention, partial spawn retry, and deferred close/shutdown
  on Wayland. No manual desktop validation is claimed yet.
- M5 remains formally `in_progress`: the user-confirmed main workflows pass, but
  startup Retry and the final inactive-tab full-chip hit target were not manually
  confirmed in the prior handoff. M6 implementation is proceeding; do not mark
  M5 or M6 complete without those gates and their evidence.
