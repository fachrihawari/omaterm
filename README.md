# OmaTerm

A Linux-first, terminal-first developer workspace built with Rust and GPUI.
Projects, tabs, recursive split panes, and long-lived terminal sessions share a
semantic command layer designed for CLI control and future agent automation.

## Current status

This repository contains architecture and implementation plans. No Cargo workspace
or Rust application exists yet. Next: **Milestone 1 — GPUI Boot**.

## Start here

- [Agent instructions](AGENTS.md): development rules and workflow.
- [Architecture blueprint](OMATERM_AGENT_BLUEPRINT.md): authoritative decisions.
- [Implementation milestones](docs/00-overview.md): nine ordered vertical slices.
- [Project status](docs/status.md): progress, evidence, blockers, and next actions.
- [Acceptance matrix](docs/acceptance-matrix.md): v0.1 release verification.
- [Dependency inventory](docs/dependencies.md): selected versions and licenses.

## Development environment

The target is Linux, with Omarchy/Wayland first and X11 build support. The repository
requests Rust stable with `rustfmt` and `clippy`. Milestone 1 will verify the exact
toolchain, GPUI revision, and native dependencies. Build/run commands become
available when it creates the workspace. Commit the application `Cargo.lock`.

`.codegraph` is an optional, ignored local index, not a build dependency. Generated
indexes may reference absolute machine-local paths.

## License

The project license is undecided. Follow the blueprint's licensing requirements
before incorporating third-party implementation code.
