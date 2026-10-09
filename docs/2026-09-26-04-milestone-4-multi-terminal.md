# Milestone 4 — Multiple Pane Terminals

> Wire pane tree leaves to long-lived TerminalSession instances. Verify terminals survive focus and layout changes.

**Status:** Complete — see [status.md](status.md) for the live record and evidence. The `## Goals` list is the delivered scope; `## Acceptance Criteria` items with documented limits stay unchecked.

## Overview

This milestone connects the pane tree (Milestone 2) with the terminal subsystem (Milestone 3). Each pane leaf in the tree gets its own `TerminalSession`. The critical validation is that terminal sessions are **long-lived** — they must survive tab switches, focus changes, and pane layout modifications.

## Goals

- [x] Each pane leaf references a registry-owned `TerminalSession` by ID
- [x] Splitting a pane creates a new terminal in the new pane
- [x] Closing a pane terminates its terminal correctly
- [x] Terminal sessions survive focus changes
- [x] Terminal sessions survive layout changes (other panes split/close)
- [x] Hidden terminals continue draining PTY output
- [x] Focus navigation between terminal panes works
- [x] Keyboard input goes to the focused terminal only

## Prerequisites

- Milestone 2 complete (pane tree with placeholder rendering)
- Milestone 3 complete (single terminal working)

## Deliverables

### Terminal Registry

A registry that maps `SessionId` → `TerminalSession` and manages terminal lifecycle:

```rust
pub struct TerminalRegistry {
    sessions: HashMap<SessionId, TerminalSession>,
}

impl TerminalRegistry {
    pub fn create(&mut self, config: TerminalConfig) -> Result<SessionId>;
    pub fn get(&self, id: &SessionId) -> Option<&TerminalSession>;
    pub fn get_mut(&mut self, id: &SessionId) -> Option<&mut TerminalSession>;
    pub fn close(&mut self, id: &SessionId) -> Result<()>;
    pub fn list(&self) -> Vec<SessionId>;
}
```

### Updated `PaneContent`

```rust
pub enum PaneContent {
    Terminal(SessionId),  // now actually used
}
```

### Wiring: Pane Tree ↔ Terminal

```
PaneNode::Pane { content: Terminal(session_id) }
                    │
                    ▼
        TerminalRegistry.get(session_id)
                    │
                    ▼
            TerminalSession
              ├── PtyProcess
              └── TerminalEngine
```

## Architecture Notes

### Session Lifetime Rules

The registry owns runtime sessions; views and core pane leaves only reference IDs.
Coordinate operations at the application boundary, keeping core independent of
the registry implementation. For a split: validate target/geometry, prepare a
session, commit the new leaf, then publish state. If creation or insertion fails,
dispose of the provisional session and leave the original tree/focus intact.
Inject spawn/insertion failures in tests. Do not await process work on the UI thread.

Close or detected shell exit removes the leaf and updates focus through shared
domain operations, then tears down its session exactly once. Track cleanup
failures until reaped rather than silently dropping the last handle. Closing the
final M4 pane shows an empty workspace with a new-terminal action; M5 defines
hierarchy-aware close behavior.

```
✅ Splitting a pane    → new TerminalSession created
✅ Closing a pane      → TerminalSession destroyed, PTY closed, process terminated
✅ Switching focus      → TerminalSession stays alive (both focused and unfocused)
✅ Resizing layout      → TerminalSession stays alive, PTY gets resize signal
✅ Other pane closes    → unrelated TerminalSessions unaffected

❌ Tab switch           → MUST NOT destroy TerminalSession
❌ Focus change         → MUST NOT recreate TerminalSession
❌ Layout mutation      → MUST NOT restart shell process
```

### Rendering Optimization

```
Visible pane:    PTY drains + engine updates + renderer active
Hidden pane:     PTY drains + engine updates + renderer PARKED
```

All terminals drain PTY output. Only visible terminals pay rendering cost.

### Resource Cleanup Checklist

When a terminal is closed, verify:
- [ ] Child process is terminated (SIGTERM → SIGKILL after timeout)
- [ ] Shell/job process-group policy is explicit; unrelated processes are untouched
- [ ] Child exit is waited/reaped with bounded escalation, off the UI thread
- [ ] PTY master FD is closed
- [ ] Async read task is cancelled
- [ ] Engine memory is freed
- [ ] SessionId is removed from registry

## Implementation Steps

1. **Create `TerminalRegistry`** in `omaterm-terminal`
2. **Wire `PaneContent::Terminal`** — each pane leaf gets a real `SessionId`
3. **On split**: create new `TerminalSession` via registry, assign to new pane
4. **On close**: destroy `TerminalSession` via registry, clean up resources
5. **Focus model**: track which pane is focused, route keyboard input to its terminal
6. **Resize propagation**: when pane dimensions change, resize the PTY
7. **Hidden terminal optimization**: park renderer for non-visible terminals
8. **Test: multiple splits** — verify 4+ terminals running simultaneously
9. **Test: rapid split/close** — verify no resource leaks
10. **Test: focus switching** — verify input goes to correct terminal

## Acceptance Criteria

- [ ] Can create 4+ terminal panes via splitting
- [ ] Each pane runs an independent shell
- [ ] Typing goes to the focused terminal only
- [ ] Focus navigation (keyboard) works between panes
- [ ] Closing a pane kills only its terminal
- [ ] Remaining terminals continue working after a pane closes
- [ ] Terminal state is preserved when another pane is split/closed
- [ ] PTY output drains for all terminals (including unfocused)
- [ ] Repeated create/close cycles of 100 terminals show no growing FD/thread/process counts
- [ ] Record memory/GPU behavior, cleanup failures, and resource baseline/tolerances
- [ ] Spawn failure and rollback leave no orphan session, child, or dangling pane ID
- [ ] No zombie processes after closing terminals
- [ ] `cargo test` passes

## Non-Goals

- No project/tab model (all panes in one flat workspace)
- No persistence
- No IPC/CLI
- No shared cross-pane selection (normal clipboard copy/paste remains supported)
- No pane drag-and-drop

## References

- Blueprint §6.4 — Long-Lived Terminal Sessions
- Blueprint §10.4 — TerminalSession
- Blueprint §28 — Hidden Pane Behavior
- Blueprint §53 — Resource Lifecycle Tests
- Blueprint §68, Slice 4 — Multiple Pane Terminals
