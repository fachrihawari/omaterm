# Milestone 7 — Command Router

> Move all workspace mutations behind a semantic command layer. UI and future CLI/agents share the same dispatcher.

**Status:** Complete — see [status.md](status.md) for the live record and evidence. The `## Goals` list is the delivered scope; `## Acceptance Criteria` items with documented limits stay unchecked.

## Overview

This is one of the most architecturally important milestones. Every workspace mutation — splitting panes, creating tabs, switching projects, sending terminal input — must route through a semantic `OmaCommand` dispatcher.

This ensures that UI actions, CLI commands, and future AI agents all use the exact same code path. No duplicate business logic.

## Goals

- [x] `OmaCommand` enum defined in `omaterm-core`
- [x] `CommandRouter` dispatcher implemented
- [x] All UI actions migrated to dispatch commands
- [x] Command results returned for both sync UI and async IPC
- [x] Unit tests for command validation and dispatch

## Prerequisites

- Milestone 6 complete (persistence working)

## Deliverables

### Updated `omaterm-core`

```
crates/omaterm-core/src/
├── command.rs          # OmaCommand enum + sub-commands
├── validation.rs       # Pure command validation
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
    RunCommand { session: SessionId, argv: Vec<String> },
    ReadVisible { session: SessionId, max_lines: usize, max_columns: usize },
    Clear { session: SessionId },
    List,
}
```

### Command Router

Place the runtime dispatcher in application coordination (initially a module in
the desktop/UI crate), not in the core crate. Keep it free of GPUI-specific APIs
and unit-test it with fake terminal/state adapters. Core owns command types,
validation, and domain operations; terminal depends on core IDs; state converts
core into snapshots. Core must not import terminal/state or application crates.

The arrows in the blueprint describe conceptual flow; the concrete Cargo graph is:

```text
desktop/UI coordination → core, terminal, state, IPC (when introduced)
terminal → core
state → core
IPC → protocol
CLI → protocol, IPC
protocol → core (only if reusing pure IDs/error types)
core → domain dependencies only
```

Protocol types must not depend on the transport crate. Map protocol requests to
domain commands at application ingress, avoiding circular dependencies.

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
    TerminalOutput { text: String, truncated: bool },
    PaneSplit { new_pane: PaneId, new_session: SessionId },
    ProjectCreated { project: ProjectId },
    TabCreated { tab: TabId, pane: PaneId, session: SessionId },
}

pub struct CommandError {
    pub code: ErrorCode,
    pub message: String,
    pub details: Option<ErrorDetails>,
}

pub enum ErrorCode {
    ProjectNotFound,
    TabNotFound,
    PaneNotFound,
    SessionExited,
    PermissionDenied,
    CrossProjectDenied,
    InvalidRequest,
    UnsupportedOperation,
    ShellBusy,
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

Before this milestone, UI actions invoke shared application/core operations. After:

```rust
// Before (shared application operation)
fn on_split_right(&mut self) {
    self.coordinator.split_pane(self.focused_pane(), Right);
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

### Execution Contract

One application owner serializes workspace mutations. Background tasks return
effects/results to that owner rather than mutating GPUI state or competing on a
workspace mutex. Revalidate target/session existence after asynchronous work.
Cancellation and failed session creation use M4 rollback rules. Dispatch must not
block UI rendering while launching/reaping a process or writing a snapshot.

Carry caller context (`LocalUser` initially; scoped credentials in M8) separately
from command payloads. M8 resolves and authorizes targets on the server, then
dispatches once. Queries return owned snapshots; failures leave domain invariants
intact. Emit persistence notifications only for successful logical changes, not
each terminal byte. Terminal CWD events use the same state-change coordination.

Migrate keyboard/pointer actions, close flows, terminal input/paste, selection of
projects/tabs, resize, and reads. Session cleanup remains an application effect;
the core algorithms remain independently testable.

Implement the `terminal.run` contract in [M9](2026-09-26-09-milestone-9-cli.md): structured argv,
tested shell encoding, and explicit shell readiness before submission. Introduce
minimal optional shell lifecycle integration here if M3 lacks authoritative prompt
state; unknown/busy shells reject submission. Do not delay this behavior until the
CLI adds its parser. Terminal Create creates a new single-terminal tab in the
specified project and returns tab/pane/session IDs.

The type snippets above omit some metadata for brevity. Actual create/list/read
results must meet the M9 coverage table and M8 bounded-output contracts, including
project roots, parent IDs, viewport dimensions, and truncation indicators.

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
- [ ] Core validation tests and application dispatcher tests cover all command paths
- [ ] Failure injection, stale targets, scope-ready caller context, and ordered effects tested
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
