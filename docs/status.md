# Implementation Status

## Current position

Milestone 1 is complete. Next: begin
[Milestone 2](02-milestone-2-pane-tree.md).

| Milestone | Status | Verification evidence | Blockers | Next action |
|---|---|---|---|---|
| 1 — GPUI Boot | complete | Build, quality checks, CI workflow, and Wayland visual/close checks recorded below | X11 runtime session unavailable; build coverage passes | Begin M2 pure pane tree |
| 2 — Pane Tree | not_started | None | Requires M1 | Implement tree invariants and tests |
| 3 — Single Terminal | not_started | None | Requires M2 | Validate PTY/parser/rendering spike |
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
