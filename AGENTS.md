# OmaTerm — Agent Instructions

> **Read this file first.** It is the primary entry point for any AI agent working on OmaTerm.

---

## What Is OmaTerm?

OmaTerm is a **terminal-first developer workspace** built in Rust with GPUI.

It is NOT an IDE. It is NOT a terminal emulator with tabs. It is a workspace where terminals are the primary interface, with an architecture designed from day one for future AI-agent automation via a semantic command bus and CLI/IPC control.

**Key identity:**
- **Oma** → Omarchy (Linux distribution)
- **Term** → Terminal
- Primary platform: Linux (Omarchy/Wayland first)
- Language: Rust
- UI framework: GPUI (from Zed)
- Terminal engine: `alacritty_terminal` behind an abstraction

---

## Authoritative Specification

The file `OMATERM_AGENT_BLUEPRINT.md` at the project root is the authoritative architecture specification. It contains 79 sections of detailed product and technical decisions.

**Do not reopen decisions** made in the blueprint unless you encounter a concrete technical blocker with evidence.

When the blueprint says **MUST**, **SHOULD**, or **MUST NOT**, interpret those literally.

The blueprint governs architecture; milestone documents define execution. Rust
snippets are illustrative unless identified as settled contracts. Verify APIs
against selected dependency versions before implementing them.

## Execution Workflow

1. Read `docs/status.md`, the current milestone, and its blueprint references.
2. Inspect existing code and working-tree changes before editing.
3. Implement the current vertical slice using shared domain operations.
4. Run applicable checks and record commands/results in `docs/status.md`.
5. Record manual desktop validation separately, plus blockers and next actions.

Before M7, UI handlers call shared core operations through application coordination.
M7 introduces the common dispatcher. Do not duplicate business logic in handlers.

Once a Cargo workspace exists, Rust milestone completion requires:

```bash
cargo fmt --all --check
cargo test --workspace
cargo clippy --workspace --all-targets -- -D warnings
```

Also run milestone-specific checks and actual Wayland validation for UI changes.
For documentation-only changes, check links, consistency, and `git diff --check`;
Cargo checks are unnecessary. Never report unavailable checks as passing.
Track `Cargo.lock`; record verified toolchain/dependency revisions and licenses in
`docs/dependencies.md`. Example versions do not establish a working MSRV.

---

## Architecture Overview

```
                               OmaTerm

┌──────────────────────────────────────────────────────────────┐
│                           GPUI                               │
│                                                              │
│ Project Sidebar     Tabs        Command Palette (later)      │
│                                                              │
│                  Recursive Pane Layout                        │
│       ┌────────────────┬────────────────┐                    │
│       │ Terminal A     │ Terminal B     │                    │
│       └────────────────┴────────────────┘                    │
└──────────────────────────────┬───────────────────────────────┘
                               │
                        semantic commands
                               │
┌──────────────────────────────▼───────────────────────────────┐
│                         Rust Core                            │
│                                                              │
│ Workspace → Project → Tab → PaneTree → TerminalSession       │
│                                                              │
│ Command Router  │  Terminal Registry  │  Persistence          │
└──────────────────────┬───────────────────────┬───────────────┘
                       │                       │
                Terminal subsystem          IPC Server
                       │                       │
              TerminalEngine              Unix socket
                       │                       │
            AlacrittyEngine           ┌────────┴────────┐
                       │              │                 │
                      PTY         omaterm CLI       AI Agent
                       │
                  bash/zsh/fish
```

### Crate Structure (Target)

```
omaterm/
├── Cargo.toml                  # Workspace root
├── crates/
│   ├── omaterm-core/           # Domain model, pane tree, commands
│   ├── omaterm-terminal/       # PTY, engine trait, alacritty impl
│   ├── omaterm-protocol/       # IPC wire types, request/response
│   ├── omaterm-ipc/            # Unix socket server & client
│   ├── omaterm-state/          # Persistence, snapshots, migration
│   ├── omaterm-ui/             # GPUI components, rendering
│   └── omaterm-cli/            # CLI binary (clap)
└── apps/
    └── omaterm/                # Desktop binary entry point
```

> **Note:** Start lean. Don't create all crates upfront — split when module boundaries become clear. A simpler initial structure (core, terminal, ui, cli) is acceptable.

### Dependency Direction (MUST follow)

```
omaterm-ui → omaterm-core → domain types (NO gpui dependency in core)
omaterm-terminal → terminal abstraction (NO gpui)
omaterm-cli → omaterm-protocol + omaterm-ipc
omaterm-ipc → omaterm-protocol (wire types do not depend on transport)
```

**NEVER** let `omaterm-core` depend on `gpui`. Pane tree algorithms, command types, and domain logic must be testable without a desktop window.

---

## Domain Model

### Workspace Hierarchy

```
Application
└── Window
    └── Projects[]
        └── Tabs[]
            └── PaneTree (recursive binary tree)
                └── PaneContent
                    └── Terminal(SessionId)
```

### Key Types

```rust
// Typed IDs — use newtypes, not raw UUIDs
struct WindowId(Uuid);
struct ProjectId(Uuid);
struct TabId(Uuid);
struct PaneId(Uuid);
struct SplitId(Uuid);
struct SessionId(Uuid);

// Recursive pane tree
enum PaneNode {
    Pane(Pane),
    Split {
        id: SplitId,
        axis: SplitAxis,
        fraction: f32,
        first: Box<PaneNode>,
        second: Box<PaneNode>,
    },
}

// Semantic commands
enum OmaCommand {
    Project(ProjectCommand),
    Tab(TabCommand),
    Pane(PaneCommand),
    Terminal(TerminalCommand),
}
```

### Terminal Architecture Layers

```
PtyProcess       — owns PTY master, child process, I/O, resize, exit
TerminalEngine   — VT parsing, screen state, cursor, modes, scrollback
TerminalRenderer — converts cells → GPUI draw ops
TerminalSession  — durable identity, survives UI changes
```

**Critical rule:** `terminal process lifecycle != terminal view lifecycle`. Changing tabs must NOT recreate the terminal.

---

## Coding Conventions

### Rust Style
- Follow standard `rustfmt` formatting
- Run `cargo clippy` before committing — zero warnings policy
- Use `thiserror` for library errors, `anyhow` only in binary entry points
- Prefer typed IDs (`PaneId`) over raw `Uuid` everywhere
- Use `tracing` for structured logging with categories: `workspace`, `pane`, `terminal`, `pty`, `render`, `ipc`, `cli`, `persistence`

### Naming
- Crate names: `omaterm-*` (kebab-case)
- Module names: snake_case
- Types: PascalCase
- Environment variables: `OMATERM_` prefix
- CLI binary: `omaterm`
- Config path: `~/.config/omaterm/config.toml`
- Socket path: `$XDG_RUNTIME_DIR/omaterm.sock`

### Testing
- **Unit tests** in the same file (`#[cfg(test)] mod tests`)
- Pane tree operations MUST have comprehensive unit tests
- Command validation MUST have unit tests
- Protocol serialization MUST have round-trip tests
- Integration tests in `tests/` directory for PTY, IPC, persistence

### Error Handling
- Define error codes centrally (e.g., `pane_not_found`, `session_exited`, `permission_denied`)
- Errors must contain: code, message, optional structured details
- Agents need stable codes — don't rely on error message text matching

---

## Forbidden Patterns

These are **prohibited**. Do not implement them under any circumstances:

| ❌ Forbidden | ✅ Do Instead |
|---|---|
| `omaterm-core` depending on `gpui` | Keep core pure domain logic, testable without GUI |
| Simulating keyboard shortcuts for automation | Use semantic commands through command router |
| Separate pane-split logic in UI vs CLI | Both go through the same `OmaCommand` dispatcher |
| Recreating terminal on tab switch | `TerminalSession` is long-lived, independent of views |
| Flat pane matrix as source of truth | Use recursive `PaneNode` binary tree |
| Guessing agent state from terminal text | Use explicit process identity / lifecycle events |
| Persisting PTY file descriptors | Persist logical state only; fresh shells on restore |
| Unbounded terminal reads for automation | Enforce `max_lines` and `max_columns` limits |
| Logging capability tokens / passwords | Never log sensitive data |
| Adding LSP, debugger, extensions, cloud | These are explicit non-goals |

---

## Implementation Sequence

Work in **vertical slices**. Each milestone builds on the previous one. See `docs/` for detailed milestone specifications.

| # | Milestone | Key Deliverable |
|---|-----------|-----------------|
| 1 | GPUI Boot | Window renders on Wayland/X11 |
| 2 | Pane Tree | Recursive split model + unit tests |
| 3 | One Terminal | PTY + alacritty_terminal + GPUI renderer |
| 4 | Multi Terminal | Pane leaves → TerminalSession, sessions survive layout |
| 5 | Projects/Tabs | Sidebar, tab bar, focus model |
| 6 | Persistence | Versioned snapshots, restore on restart |
| 7 | Command Router | Semantic OmaCommand bus, UI migrated |
| 8 | IPC | Unix socket server, JSON protocol v1 |
| 9 | CLI | `omaterm` binary with core commands |

**Do not skip ahead.** Each slice must be working and tested before starting the next.

---

## Version Roadmap (Beyond v0.1)

| Version | Focus |
|---------|-------|
| **0.1** | Terminal Workspace (Milestones 1–9) |
| **0.2** | Developer Context (file tree, git, diff, command palette) |
| **0.3** | Agent Awareness (process recognition, status, notifications) |
| **0.4** | Agent Automation (spawn, prompt, wait, delegated panes) |

---

## Key Files

| File | Purpose |
|------|---------|
| `OMATERM_AGENT_BLUEPRINT.md` | Authoritative 79-section specification |
| `AGENTS.md` | This file — agent entry point |
| `docs/00-overview.md` | Milestone index |
| `docs/status.md` | Progress, verification evidence, next action |
| `docs/acceptance-matrix.md` | Release requirements and verification |
| `docs/dependencies.md` | Toolchain, dependency versions, license inventory |
| `docs/01-*.md` through `docs/09-*.md` | Individual milestone specs |
| `rust-toolchain.toml` | Rust stable toolchain |
| `.editorconfig` | Editor settings |

---

## What To Do When Starting a Milestone

1. Read the corresponding `docs/XX-milestone-*.md` file
2. Re-read the relevant blueprint sections referenced in that doc
3. Create the minimal crate/module structure needed
4. Write tests first for domain logic (pane tree, commands, protocol)
5. Implement the deliverables
6. Run `cargo test`, `cargo clippy`, `cargo fmt --check`
7. Verify on Wayland (Omarchy) if the milestone involves UI
8. Document any deviations from the blueprint with rationale

---

## Questions You May Resolve Independently

- Exact crate names and internal module layout
- UUID vs ULID for identifiers
- Exact GPUI component structure
- Channel/async implementation details
- Exact snapshot filename
- Exact scrollback default value
- Exact error Rust types
- CLI argument parser choice (clap recommended)
- Internal builder patterns
- Minor visual spacing

## Questions That Require Evidence Before Changing

Do NOT change these without a concrete blocker + evidence + alternatives:

- Rust as the language
- GPUI as the UI framework
- Linux-first platform target
- Terminal-first product scope
- Recursive pane tree model
- Long-lived TerminalSession
- Semantic command bus architecture
- Unix-socket CLI/IPC architecture
- Project-scoped agent capabilities
- No browser panes in current scope
- No full IDE features

---

## License

**MIT OR Apache-2.0** — see `LICENSE-MIT` and `LICENSE-APACHE`. Do not copy GPL code (from Kero or Zed) without explicit license decision. Use Kero and Zed as behavioral/architectural references only.
