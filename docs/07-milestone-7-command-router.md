# Milestone 7 — Command Router

> Move all workspace mutations behind a semantic command layer. UI and future CLI/agents share the same dispatcher.

## Overview

This is one of the most architecturally important milestones. Every workspace mutation — splitting panes, creating tabs, switching projects, sending terminal input — must route through a semantic `OmaCommand` dispatcher.

This ensures that UI actions, CLI commands, and future AI agents all use the exact same code path. No duplicate business logic.

## Goals

- [ ] `OmaCommand` enum defined in `omaterm-core`
- [ ] `CommandRouter` dispatcher implemented
- [ ] All UI actions migrated to dispatch commands
- [ ] Command results returned for both sync UI and async IPC
- [ ] Unit tests for command validation and dispatch

## Prerequisites

- Milestone 6 complete (persistence working)

## Deliverables

### Updated `omaterm-core`

```
crates/omaterm-core/src/
├── command.rs          # OmaCommand enum + sub-commands
├── router.rs           # CommandRouter dispatcher
└── result.rs           # CommandResult type
```

### Command Types

```rust
// command.rs
pub enum OmaCommand {
    Project(ProjectCommand),
    Tab(TabCommand),
    Pane(PaneCommand),
    Terminal(TerminalCommand),
}

pub enum ProjectCommand {
    Create { name: Option<String>, directory: Option<PathBuf> },
    Delete { project: ProjectId },
    Select { project: ProjectId },
    List,
    Rename { project: ProjectId, name: String },
}

pub enum TabCommand {
    Create { project: ProjectId, name: Option<String> },
    Close { tab: TabId },
    Select { tab: TabId },
    List { project: ProjectId },
    Rename { tab: TabId, name: String },
}

pub enum PaneCommand {
    Split { target: PaneId, direction: SplitDirection },
    Close { pane: PaneId },
    Focus { pane: PaneId },
    FocusDirection { direction: SplitDirection },
    Resize { split: SplitId, fraction: f32 },
    Equalize { tab: TabId },
    List { tab: Option<TabId> },
}

pub enum TerminalCommand {
    Create { project: ProjectId, directory: Option<PathBuf> },
    SendBytes { session: SessionId, data: Vec<u8> },
    RunCommand { session: SessionId, command: String },
    ReadVisible { session: SessionId, max_lines: usize, max_columns: usize },
    Clear { session: SessionId },
    List,
}
```

### Command Router

```rust
// router.rs
pub struct CommandRouter {
    // holds references to workspace, terminal registry, etc.
}

impl CommandRouter {
    pub fn dispatch(&mut self, command: OmaCommand) -> CommandResult;
}

// result.rs
pub enum CommandResult {
    Ok(CommandOutput),
    Err(CommandError),
}

pub enum CommandOutput {
    Unit,
    ProjectList(Vec<ProjectInfo>),
    TabList(Vec<TabInfo>),
    PaneList(Vec<PaneInfo>),
    TerminalList(Vec<TerminalInfo>),
    TerminalOutput(String),
    PaneSplit { new_pane: PaneId, new_session: SessionId },
}

pub struct CommandError {
    pub code: ErrorCode,
    pub message: String,
}

pub enum ErrorCode {
    ProjectNotFound,
    TabNotFound,
    PaneNotFound,
    SessionExited,
    PermissionDenied,
    InvalidRequest,
    Timeout,
}
```

## Architecture Notes

### The Single Path Rule

```
                  ┌───────────┐
                  │  GPUI UI  │
                  └─────┬─────┘
                        │ constructs OmaCommand
                        ▼
               ┌─────────────────┐
               │ CommandRouter   │  ← single implementation
               └─────────────────┘
                   ▲           ▲
                   │           │
            omaterm CLI     AI Agent
            (future M9)    (future v0.4)
```

Every consumer builds an `OmaCommand`. The router dispatches it. There is ONE implementation of "split pane right" — not a UI version and a CLI version.

### Migration Strategy

Before this milestone, UI actions directly mutate workspace state. After:

```rust
// Before (direct mutation)
fn on_split_right(&mut self) {
    self.workspace.current_tab_mut().root.split(focused, Right, new_pane);
    self.terminal_registry.create(...);
}

// After (command dispatch)
fn on_split_right(&mut self) {
    let cmd = OmaCommand::Pane(PaneCommand::Split {
        target: self.focused_pane(),
        direction: SplitDirection::Right,
    });
    self.router.dispatch(cmd);
}
```

### Error Codes

All errors use stable `ErrorCode` variants. Agents and CLI parse these codes — never rely on error message text.

## Implementation Steps

1. **Define `OmaCommand` enum** with all sub-command variants
2. **Define `CommandResult`** and `CommandError` types with error codes
3. **Implement `CommandRouter`** — dispatches commands to workspace/terminal operations
4. **Migrate project operations** — create, delete, select, list, rename
5. **Migrate tab operations** — create, close, select, list
6. **Migrate pane operations** — split, close, focus, resize
7. **Migrate terminal operations** — create, send, read
8. **Update all GPUI event handlers** to construct and dispatch commands
9. **Write unit tests** for command dispatch (without GPUI)
10. **Verify persistence** still works through command path

## Acceptance Criteria

- [ ] All UI actions go through `CommandRouter.dispatch()`
- [ ] No direct workspace mutation from UI event handlers
- [ ] `CommandRouter` is testable without GPUI
- [ ] Error codes are stable and well-defined
- [ ] `ProjectCommand::List` returns correct project info
- [ ] `PaneCommand::Split` creates pane + terminal through single dispatch
- [ ] `TerminalCommand::ReadVisible` returns bounded text
- [ ] `cargo test -p omaterm-core` covers all command dispatch paths
- [ ] Existing functionality works identically after migration
- [ ] Persistence triggers correctly from command dispatch

## Non-Goals

- No IPC yet (commands are dispatched in-process only)
- No CLI yet
- No agent commands
- No undo/redo
- No command history

## References

- Blueprint §15 — Command Architecture
- Blueprint §15.1 — Example Command Types
- Blueprint §17 — The CLI Must Not Automate the UI
- Blueprint §44 — Error Design
- Blueprint §62 — No Duplicate Business Logic
- Blueprint §68, Slice 7 — Command Router
