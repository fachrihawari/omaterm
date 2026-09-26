# Implementation Status

## Current position

Application implementation has not started. There is no `Cargo.toml`, application
source, or verified GPUI dependency. Next: [Milestone 1](01-milestone-1-gpui-boot.md).

| Milestone | Status | Verification evidence | Blockers | Next action |
|---|---|---|---|---|
| 1 — GPUI Boot | not_started | None | Dependency/platform validation pending | Select GPUI revision and create minimal workspace |
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
