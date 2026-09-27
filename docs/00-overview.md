# OmaTerm — Implementation Milestones

## Overview

This directory contains the implementation plans for OmaTerm v0.1 and approved
post-v0.1 work. The v0.1 release consists of nine ordered vertical slices; later
milestones may start only after those prerequisites are complete.

OmaTerm v0.1 targets a **usable terminal workspace** whose panes can be controlled semantically through a CLI — proving the architecture required for future AI-agent control.

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
- For the remaining M5–M8 gaps and carried-forward validation, follow the
  [dependency-ordered closure plan](m5-m8-closure-plan.md); milestone specs
  remain the acceptance contracts.
- Documentation-only work requires link/consistency review and `git diff --check`.
  Run `python3 scripts/check-docs.py` from the repository root for local link targets
  and numbered blueprint references plus CLI/IPC table coverage; external
  URLs/heading anchors need review.

## Milestone Index

| # | Milestone | Description | Key Crates |
|---|-----------|-------------|------------|
| 1 | [GPUI Boot](01-milestone-1-gpui-boot.md) | Window renders on Wayland/X11 | `apps/omaterm` |
| 2 | [Pane Tree](02-milestone-2-pane-tree.md) | Recursive split model + tests | `omaterm-core` |
| 3 | [Single Terminal](03-milestone-3-single-terminal.md) | PTY + alacritty + GPUI renderer | `omaterm-terminal`, `omaterm-ui` |
| 4 | [Multi Terminal](04-milestone-4-multi-terminal.md) | Pane leaves → sessions | `omaterm-terminal`, `omaterm-ui` |
| 5 | [Projects & Tabs](05-milestone-5-projects-tabs.md) | Sidebar, tab bar, focus | `omaterm-core`, `omaterm-ui` |
| 6 | [Persistence](06-milestone-6-persistence.md) | Versioned snapshots | `omaterm-state` |
| 7 | [Command Router](07-milestone-7-command-router.md) | Semantic commands + runtime coordination | `omaterm-core`, desktop coordination |
| 8 | [IPC](08-milestone-8-ipc.md) | Unix socket server | `omaterm-ipc`, `omaterm-protocol` |
| 9 | [CLI](09-milestone-9-cli.md) | `omaterm` CLI binary | `omaterm-cli` |
| 10 | [Encrypted History Recovery](10-milestone-10-history-recovery.md) | Opt-in encrypted scrollback and OmaTerm command journal; fresh shells | `omaterm-terminal`, `omaterm-state`, desktop, IPC, CLI |

M10 is post-v0.1 and does not change M6's layout/CWD-only persistence contract.
It is blocked until M5–M9 and its dependency/replay spikes are complete.

## Version Roadmap

```
v0.1 — Terminal Workspace        ← Milestones 1–9 (this plan)
v0.2 — Developer Context          (file tree, git, diff, command palette)
v0.3 — Agent Awareness             (process recognition, status, notifications)
v0.4 — Agent Automation            (spawn, prompt, wait, delegated panes)
```

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
- Blueprint §22 — First-Run Product Scope
- Blueprint §23 — Suggested Version Progression
- Blueprint §55 — MVP Acceptance Criteria
- Blueprint §68 — Implementation Sequence
- Blueprint §78 — First Implementation Objective
