# Milestone 17 — v0.2 Closure & Hardening

> M11-style closeout for the Developer Context release: turn every
> "complete with limits" row into Wayland evidence or an explicit limit,
> update baselines for the new fs/git/diff/search load, and extend the
> acceptance matrix. Adds no v0.3+ features.

**Status:** Open (no closeout run yet; depends on M12–M16) — see [status.md](status.md) for the live record and evidence.

## Product Contract

M17 blocks the v0.2 release claim, not new features. It closes live-proof
gaps from M12–M16, records formal perf/resource baselines under the new
workload (watchers, git refresh, large diffs, fuzzy search), re-audits
logging redaction for the new categories, and updates packaging only if new
native deps appeared (none expected — shortlist is pure Rust).

## Goals

- [ ] Live proof for every M12–M16 fix still marked unit-only.
- [ ] Formal baseline (workload/duration/HW/build-mode recorded):
  startup, release idle, 10k-file tree walk + filter, git refresh on a
  large repo, 1000-file diff render, 100-cycle project-switch FD/threads/
  RSS, concurrent IPC load incl. new methods. Growth across cycles, not
  single high-water marks (blueprint §52–§53).
- [ ] Redaction re-audit: tokens, passwords, clipboard, file contents,
  diff bodies, git stderr never in default logs.
- [ ] Manual matrix closed or explicit: second Wayland compositor / X11 if
  available, non-git dir, binary-heavy repo, `zsh`/`fish` if installed —
  unavailable stays a limit, never a pass (M3/M5/M6 precedent).
- [ ] Acceptance matrix v0.2 rows + `docs/status.md` evidence for all slices.

## Prerequisites

- M12–M16 complete and verified.

## Deliverables

- Wayland proof runs (release, isolated state, captures in `/tmp`):
  tree/search/git/diff/palette flows on the same instance + CLI parity
  spot-checks.
- Baseline table in `docs/status.md`; updated `acceptance-matrix.md`;
  `docs/dependencies.md` delta (new crates from spikes, if any).
- `v0.2` proof flow (same instance): `file.search` → `git.status` →
  `diff.show` → palette action → `process.list` (M18 if landed).

## Test Plan

- Re-run full serial suite + new stress tests (watcher burst,
  refresh-under-load, diff flood, switch cycles).
- `cargo fmt --check`, `clippy -- -D warnings`, `cargo build --release`,
  `python3 scripts/check-docs.py`, `git diff --check`.

## Acceptance Criteria

- [ ] All M12–M16 acceptance boxes checked with linked evidence
- [ ] Baseline table complete with workload records; no invented thresholds
- [ ] Redaction audit green; manual matrix closed or explicit
- [ ] Quality gates green; `Cargo.lock` tracked

## Non-Goals

- No editor, agent awareness/automation, `terminal wait` (v0.3/v0.4),
  Ghostty/macOS/Windows, themes engine.

## References

- Blueprint §45, §48–§49, §52–§54, §59, §61, §67, §70
