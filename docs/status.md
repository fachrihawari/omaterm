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
| 9 — CLI | complete | `omaterm-cli` thin client (27-row parser/mapping matrix plus path-launch forms), 31 CLI tests, workspace gates, and release Wayland CLI/desktop proof recorded below; live `pane.resize` via discovered split IDs closed by M11 | Scoped in-app denial covered by M8 evidence plus CLI env/deny tests | M10 history (post-v0.1) |
| 10 — Encrypted History Recovery | complete | 287-test suite green 2026-09-28 plus release Wayland proof (opt-in, styled/Unicode restore, fresh shells, journal merge, alt absence, clears + rotation, disable, key-loss memory-only); M11 closed the remaining live items (same-pane restore, corrupt-archive quarantine) — see M11 record | Documented limits only: graceful-close live, banner-visibility eyes, per-pane CWD re-verification stays on M6 plumbing | M11 closure |
| 11 — v0.1 Closure & Hardening | complete | 313-test serial suite green; release Wayland IPC proofs (split-ID discovery + live resize/equalize, path launch + run, history restore + quarantine, invalid-config survival, targeted logging); PKGBUILD + license inventory; perf baseline recorded — see M11 record below | Documented limits only: eyes/hands items (jump keys, picker portal, paste/drop live, font-size visual), theme engine + automation-disable enforcement future, X11/second-compositor/scaling, per-process GPU | M12 (done — see M12 row) |
| 12 — Project Context Root | complete | `omaterm-context` (resolve/boundary/ignore, 13 tests), `[files]`/`[git]` config, 3 logging categories, `project.root` parity (router/bridge/CLI + scope tests), 328-test serial suite green, release Wayland pinned/git/deleted-pin/stale proofs — see M12 record below | Documented limits only: unpinned-no-shell live path unit-covered, second compositor/X11/scaling, per-process GPU (standing v0.1 limits) | Begin M13 file tree + filename search |

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

Begin M13 file tree + filename search on the M12 root/boundary/config
foundation with the recorded `ignore` + `notify` 8.2.0 + `fuzzy-matcher`
spike versions.
