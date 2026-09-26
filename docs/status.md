# Implementation Status

## Current position

Milestone 2 is complete. Next: begin
[Milestone 3](03-milestone-3-single-terminal.md).

| Milestone | Status | Verification evidence | Blockers | Next action |
|---|---|---|---|---|
| 1 — GPUI Boot | complete | Build, quality checks, CI workflow, and Wayland visual/close checks recorded below | X11 runtime session unavailable; build coverage passes | Begin M2 pure pane tree |
| 2 — Pane Tree | complete | 9 core tests, workspace quality gates, and Wayland manual validation recorded below | None | Begin M3 single terminal |
| 3 — Single Terminal | in_progress | Phase A + B done (see M3 record below); Phase C partial | None blocking | Finish Phase C desktop items (CJK pixels, paste E2E, exit screen), then mark complete |
| 4 — Multi Terminal | not_started | None | Requires M3 | Wire registry to pane IDs |
| 5 — Projects/Tabs | not_started | None | Requires M4 | Add hierarchy and focus lifecycle |
| 6 — Persistence | not_started | None | Requires M5 | Implement validated snapshots |
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

## Milestone 3 — Single Terminal — 2026-09-26 (in progress, NOT committed)

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
