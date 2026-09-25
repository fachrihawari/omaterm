# OmaTerm — Implementation Milestones

## Overview

This directory contains the implementation plan for OmaTerm v0.1, broken into 9 vertical slices. Each milestone builds on the previous one and should be completed in order.

OmaTerm v0.1 targets a **usable terminal workspace** whose panes can be controlled semantically through a CLI — proving the architecture required for future AI-agent control.

## Milestone Index

| # | Milestone | Description | Key Crates |
|---|-----------|-------------|------------|
| 1 | [GPUI Boot](01-milestone-1-gpui-boot.md) | Window renders on Wayland/X11 | `apps/omaterm` |
| 2 | [Pane Tree](02-milestone-2-pane-tree.md) | Recursive split model + tests | `omaterm-core` |
| 3 | [Single Terminal](03-milestone-3-single-terminal.md) | PTY + alacritty + GPUI renderer | `omaterm-terminal`, `omaterm-ui` |
| 4 | [Multi Terminal](04-milestone-4-multi-terminal.md) | Pane leaves → sessions | `omaterm-terminal`, `omaterm-ui` |
| 5 | [Projects & Tabs](05-milestone-5-projects-tabs.md) | Sidebar, tab bar, focus | `omaterm-core`, `omaterm-ui` |
| 6 | [Persistence](06-milestone-6-persistence.md) | Versioned snapshots | `omaterm-state` |
| 7 | [Command Router](07-milestone-7-command-router.md) | Semantic command bus | `omaterm-core` |
| 8 | [IPC](08-milestone-8-ipc.md) | Unix socket server | `omaterm-ipc`, `omaterm-protocol` |
| 9 | [CLI](09-milestone-9-cli.md) | `omaterm` CLI binary | `omaterm-cli` |

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

- `OMATERM_AGENT_BLUEPRINT.md` — Authoritative specification (79 sections)
- `AGENTS.md` — AI agent instructions
- Blueprint §22 — First-Run Product Scope
- Blueprint §23 — Suggested Version Progression
- Blueprint §55 — MVP Acceptance Criteria
- Blueprint §68 — Implementation Sequence
- Blueprint §78 — First Implementation Objective
