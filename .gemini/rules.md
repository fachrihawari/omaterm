# OmaTerm — Gemini Agent Rules

## Project Context

OmaTerm is a Rust + GPUI terminal-first developer workspace for Linux (Omarchy). Read `AGENTS.md` at project root for full context. Read `OMATERM_AGENT_BLUEPRINT.md` for the authoritative specification.

## Critical Rules

1. **Read `AGENTS.md` first** before making any changes.
2. **Never add `gpui` as a dependency to `omaterm-core`**. Domain logic must be testable without a GUI window.
3. **Use semantic commands** for all workspace mutations. UI and CLI must share the same `OmaCommand` dispatcher. Never simulate keyboard shortcuts for automation.
4. **Terminal sessions are long-lived**. Changing tabs or pane layout must NOT recreate the terminal. `TerminalSession` survives UI changes.
5. **Recursive pane tree** — use `PaneNode` binary tree, never a flat matrix.
6. **Work in vertical slices** — follow the milestone order in `docs/`. Don't skip ahead.
7. **Run tests** — `cargo test`, `cargo clippy`, `cargo fmt --check` before finishing any task.
8. **Use typed IDs** — `PaneId(Uuid)`, not raw `Uuid` passed around.
9. **Preserve the blueprint** — don't reopen framework or product decisions unless you hit a concrete technical blocker with evidence.

## Coding Style

- `rustfmt` formatting (standard)
- Zero `clippy` warnings
- `thiserror` for library errors, `anyhow` in binaries only
- `tracing` for structured logging
- Unit tests in `#[cfg(test)] mod tests` blocks
- Integration tests in `tests/` directories

## Non-Goals (Do NOT Build)

- LSP, debugger, refactoring engine
- Extension marketplace
- Cloud accounts, analytics
- Browser panes
- SSH/Docker manager UI
- Full IDE features

## Milestone Docs

Implementation plans are in `docs/01-*.md` through `docs/09-*.md`. Read the relevant milestone doc before starting work on that phase.
