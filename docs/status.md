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
| 5 — Projects/Tabs | complete | Core hierarchy, coordinator lifecycle, main Wayland workflows, inactive-tab padding hit target, startup Retry, 200 measured session create/close cycles, and cleanup fault/repeated-close tests recorded below | Per-process GPU counters unavailable; broader M8 resource test remains | Begin M6 Wayland persistence validation |
| 6 — Persistence | complete | State crate/workspace tests and release Wayland restart, nested split/focus, fresh PID, CWD/fallback, restored Retry, corruption/schema retention and pending-debounce close evidence recorded below | Home-unavailable desktop UI limit not exercised; automation covers error paths | Begin M7 completion |
| 7 — Command Router | complete | Single async dispatch path, 19 router tests (variant matrix, cancel/duplicate rollback), commit guards, and release Wayland UI regression (resize/equalize/focus/close/tab) recorded below | None | Begin M8 acceptance review (done — see M8 row) |
| 8 — IPC | complete | Typed 17-method mapping, bounds, credentials/scope/child-env, owner bridge, 11 transport tests, concurrent-load + shutdown-under-load Wayland proof recorded below | Documented limits only: cross-UID harness, fallback-dir creation path, owner-channel saturation race (see below) | Begin M9 CLI |
| 9 — CLI | not_started | None | Requires M8 | Complete CLI/desktop proof flow |
| 10 — Encrypted History Recovery | not_started | User selected both scrollback and an OmaTerm-owned command journal, explicit opt-in, and encrypted archives | Post-v0.1; requires M5–M9 completion, supported shell lifecycle integration, and verified Linux keyring/terminal replay dependencies | Complete M5–M9 before the M10 dependency and replay spikes |

## Handoff rules

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
