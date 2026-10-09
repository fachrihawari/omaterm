# OmaTerm — Implementation Milestones

## Overview

This directory contains the implementation plans for OmaTerm and approved
follow-up work. Milestone specs and execution plans are date-prefixed
(`YYYY-MM-DD-…`, creation date) so the folder reads chronologically; the
living index and reference docs (`00-overview.md`, `status.md`,
`acceptance-matrix.md`, `dependencies.md`, `shortcuts.md`,
`blueprint-coverage-map.md`) keep stable names because other docs link to
them by path.

OmaTerm v0.1 targeted a **usable terminal workspace** whose panes can be
controlled semantically through a CLI — proving the architecture required for
future AI-agent control. It shipped as nine ordered vertical slices (M1–M9)
plus a post-v0.1 history milestone (M10) and a closure milestone (M11).
Milestones 12–20 then delivered the developer-context surface now released as
`0.2.0`–`0.4.0`.

## Execution and Completion Gates

Start with [status.md](status.md), then the current milestone and its blueprint
references. Acceptance is evidence-based: planned checks are not passes. Follow
the canonical workflow in [AGENTS.md](../AGENTS.md).

- Complete slices in order; introduce only the crates needed by the active slice.
- Before M7, shared core operations and application coordination own mutations;
  M7 unifies entry points behind the dispatcher without duplicating algorithms.
- Snippets are illustrative. Explicit behavioral contracts guide implementation;
  verify dependency APIs before selecting exact Rust signatures.
- Run workspace format/test/Clippy and milestone-specific checks. Record real
  Wayland results separately. CI does not certify desktop behavior.
- Map requirements to [acceptance-matrix.md](acceptance-matrix.md), and record
  toolchain/dependency evidence in [dependencies.md](dependencies.md).
- Milestones 1–20 are implemented and tagged across `0.1.0`–`0.4.0`. The
  M5–M8 closure checklist is retained as history; the open work is the native
  validation matrix tracked in [status.md](status.md) and
  [acceptance-matrix.md](acceptance-matrix.md) (M15/M16/M17/M18/M19, and the
  M20 2-repo Wayland matrix).
- Documentation-only work requires link/consistency review and `git diff --check`.
  Run `python3 scripts/check-docs.py` from the repository root for local link targets
  and numbered blueprint references plus CLI/IPC table coverage; external
  URLs/heading anchors need review.

## Milestone Index

| # | Milestone | Description | Key Crates |
|---|-----------|-------------|------------|
| 1 | [GPUI Boot](2026-09-26-01-milestone-1-gpui-boot.md) | Window renders on Wayland/X11 | `apps/omaterm` |
| 2 | [Pane Tree](2026-09-26-02-milestone-2-pane-tree.md) | Recursive split model + tests | `omaterm-core` |
| 3 | [Single Terminal](2026-09-26-03-milestone-3-single-terminal.md) | PTY + alacritty + GPUI renderer | `omaterm-terminal`, `apps/omaterm` |
| 4 | [Multi Terminal](2026-09-26-04-milestone-4-multi-terminal.md) | Pane leaves → sessions | `omaterm-terminal`, `apps/omaterm` |
| 5 | [Projects & Tabs](2026-09-26-05-milestone-5-projects-tabs.md) | Sidebar, tab bar, focus | `omaterm-core`, `apps/omaterm` |
| 6 | [Persistence](2026-09-26-06-milestone-6-persistence.md) | Versioned snapshots | `omaterm-state` |
| 7 | [Command Router](2026-09-26-07-milestone-7-command-router.md) | Semantic commands + runtime coordination | `omaterm-core`, desktop coordination |
| 8 | [IPC](2026-09-26-08-milestone-8-ipc.md) | Unix socket server | `omaterm-ipc`, `omaterm-protocol` |
| 9 | [CLI](2026-09-26-09-milestone-9-cli.md) | `omaterm` CLI binary | `omaterm-cli` |
| 10 | [Encrypted History Recovery](2026-09-27-10-milestone-10-history-recovery.md) | Opt-in encrypted scrollback and OmaTerm command journal; fresh shells | `omaterm-terminal`, `omaterm-state`, desktop, IPC, CLI |
| 11 | [v0.1 Closure & Hardening](2026-09-28-11-milestone-11-v01-closure.md) | Live closeout M10, config, launch-args, resize IDs, logging, paste/drop, OS boundary, perf, packaging | `apps/omaterm`, `omaterm-cli`, `omaterm-state`, docs/packaging |
| 12 | [Project Context Root](2026-09-29-12-milestone-12-project-context.md) | Project root resolution, fs boundary, `[files]/[git]` config | `omaterm-context` (new), `omaterm-core` |
| 13 | [File Tree](2026-09-29-13-milestone-13-file-tree.md) | Sidebar tree + fuzzy filename search, terminal-routed open | `omaterm-context`, desktop, IPC, CLI |
| 14 | [Git Status](2026-09-29-14-milestone-14-git-status.md) | VSCode-style status + stage/unstage/discard via system git | `omaterm-context`, desktop, IPC, CLI |
| 15 | [Diff Viewer](2026-09-29-15-milestone-15-diff-viewer.md) | Hunk list + per-hunk stage | desktop, IPC, CLI |
| 16 | [Command Palette](2026-09-29-16-milestone-16-palette.md) | Fuzzy palette over semantic commands + file index | desktop |
| 17 | [v0.2 Closure](2026-09-29-17-milestone-17-v02-closure.md) | Live closeout, baselines, redaction audit, acceptance rows | docs, desktop, tests |
| 18 | [Process Panel](2026-09-29-18-milestone-18-process-panel.md) | Kero-parity Info panel: processes + ports + scoped kill | `omaterm-terminal`, desktop, IPC, CLI |
| 19 | [Basic Built-in Editor](2026-10-04-19-milestone-19-editor.md) | Native open/edit/highlight/save; no LSP/IDE scope | `omaterm-core`, `omaterm-context`, desktop |
| 20 | [Multi-Repo Support](2026-10-09-20-milestone-20-multi-repo.md) | Depth-1 repo scan, active repo, VS Code-style Git sections | `omaterm-context`, `omaterm-core`, `omaterm-state`, desktop, IPC, CLI |

M10 is post-v0.1 and does not change M6's layout/CWD-only persistence contract.
It is blocked until M5–M9 and its dependency/replay spikes are complete.

M11 closes the remaining v0.1 gaps (M10 live proof, general config, CLI
launch-args, resize discoverability, logging, paste/drop, OS boundary, perf
baseline, packaging/license inventory). It adds no v0.2+ features; `terminal
wait` stays deferred (future agent work, versioned `0.x.x`) with only an API
design note.

M12–M17 deliver v0.2 Developer Context (VSCode-inspired file tree, git,
diff, palette; Kero is the behavioral reference, no GPL code): M12 is the
project-root/boundary foundation with the 3rd-party crate spikes, M13–M16
are the panels (sidebar-first), M17 is the v0.2
closeout. M18 adds the Kero-parity process Info panel (per focused pane,
ports + scoped SIGTERM kill) and may land any time after M12. M19 is the
basic built-in editor (blueprint §34: open/edit/highlight/save, no LSP or
IDE scope), tracked by its [milestone spec](2026-10-04-19-milestone-19-editor.md) and
implementation plan. M20 adds multi-repo project support (depth-1 repo
scan, active repo, every Git surface follows it), tracked by its
[milestone spec](2026-10-09-20-milestone-20-multi-repo.md). Blueprint §23
allows the lightweight editor into v0.2 or v0.3; version assignment stays a
product decision outside this plan.
Project license is MIT OR Apache-2.0 (`LICENSE-MIT`, `LICENSE-APACHE`).

**Sequencing note:** M17 (v0.2 closure) blocks the v0.2 release claim and
depends on M12–M16 completion. The `0.2.0`–`0.4.0` tags shipped the
developer-context surface, but the M15/M16/M17/M18/M19 native validation
gates and the M20 2-repo Wayland matrix remain open. Shipping a tag is not
evidence those gates passed; their acceptance stays open in
[status](status.md) and the [acceptance matrix](acceptance-matrix.md), and
M19/M20 must not mask them.

Section-level ownership for the whole blueprint — including intentionally
deferred and unowned sections — is tracked in the
[blueprint coverage map](blueprint-coverage-map.md).

## Version Roadmap

```
v0.1 — Terminal Workspace          ← Milestones 1–11  (tag 0.1.0)
v0.2 — Developer Context            (file tree, git, diff, palette,
        process panel, editor, multi-repo) ← Milestones 12–20
        tags 0.2.0 · 0.3.0 (theme/Windows) · 0.4.0 (multi-repo)
0.x.x — Agent Awareness             (blueprint §38–§42; not yet scheduled)
0.x.x — Agent Automation            (spawn, prompt, wait, delegated panes)
```

Blueprint §23's suggested `0.3`/`0.4` agent milestones predate the shipped
tags and are not reused. All future agent work is versioned `0.x.x` until it
is scheduled; never promise it under a concrete version number.

## Success Criteria for v0.1

The first major technical milestone is achieved when this flow works reliably:

```bash
# Start OmaTerm
omaterm

# List panes
omaterm pane list

# Split pane through semantic command
omaterm pane split --right

# Run command in new pane
omaterm terminal run --pane <id> -- cargo test

# Read bounded output
omaterm terminal read --pane <id> --lines 50
```

If this works, OmaTerm has proven the architecture for future agent control.

## Key References

- [OMATERM_AGENT_BLUEPRINT.md](../OMATERM_AGENT_BLUEPRINT.md) — Authoritative specification (79 sections)
- [AGENTS.md](../AGENTS.md) — AI agent instructions
- [Blueprint Coverage Map](blueprint-coverage-map.md) — every blueprint section mapped to its owning milestone, with open/deferred/unowned gaps made explicit
- [UI v5 Fidelity Correction Plan](2026-10-02-ui-v5-fidelity-correction-plan.md) — historical: audited sidebar/icon/detail mismatches and correction gates (landed; see [DESIGN.md](../DESIGN.md))
- [UI v5 Pixel-Perfect Rewrite Plan](2026-10-02-ui-v5-pixel-perfect-plan.md) — historical: exact mockup reproduction, assets/measurements, phased delivery, screenshot acceptance (landed)
- [Diff and Files/Git UX Correction Plan](2026-10-03-diff-files-ux-correction-plan.md) — historical: diff row alignment/virtualization, two-axis scrolling, centered Git icons, contextual Files help (landed)
- [M15 Completion Plan](2026-10-03-m15-completion-plan.md) — historical: hunk staging, bounded queries, virtualized preview, release gates
- [M15 Full Resolution Plan](2026-10-03-m15-full-resolution-plan.md) — historical: tree inventory and closure sequence
- [M16 Implementation Plan](2026-10-03-m16-implementation-plan.md) — historical: unified command/file palette, typed catalog, bounded search, focus, parity
- [M15 + M16 Remaining-Work Plan](2026-10-03-m15-m16-remaining-work-plan.md) — historical: diff closure, async process queries, palette lifecycle/catalog/focus
- [M19 Comprehensive Completion Plan](2026-10-03-m19-completion-plan.md) — historical: root identity, async I/O, bounded store, registry persistence, lifecycle/input/entry
- [Git History Graph and Commit File Diffs Plan](2026-10-06-git-history-plan.md) — historical: Graph below Source Control, merge lanes, expandable commit files, parent-specific diffs
- [VS Code Workbench UI Redesign Plan](2026-10-02-ui-workbench-redesign-plan.md) — superseded visual direction; retained implementation history
- Blueprint §22 — First-Run Product Scope
- Blueprint §23 — Suggested Version Progression
- Blueprint §55 — MVP Acceptance Criteria
- Blueprint §68 — Implementation Sequence
- Blueprint §78 — First Implementation Objective
