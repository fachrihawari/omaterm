# Milestone 4 — Multiple Pane Terminals

> Wire pane tree leaves to long-lived TerminalSession instances. Verify terminals survive focus and layout changes.

## Overview

This milestone connects the pane tree (Milestone 2) with the terminal subsystem (Milestone 3). Each pane leaf in the tree gets its own `TerminalSession`. The critical validation is that terminal sessions are **long-lived** — they must survive tab switches, focus changes, and pane layout modifications.

## Goals

- [ ] Each pane leaf owns a `TerminalSession`
- [ ] Splitting a pane creates a new terminal in the new pane
- [ ] Closing a pane terminates its terminal correctly
- [ ] Terminal sessions survive focus changes
- [ ] Terminal sessions survive layout changes (other panes split/close)
- [ ] Hidden terminals continue draining PTY output
- [ ] Focus navigation between terminal panes works
- [ ] Keyboard input goes to the focused terminal only

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
    pub fn create(&mut self, config: TerminalConfig) -> SessionId;
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
- [ ] No file descriptor leaks after creating and closing 20 terminals
- [ ] No zombie processes after closing terminals
- [ ] `cargo test` passes

## Non-Goals

- No project/tab model (all panes in one flat workspace)
- No persistence
- No IPC/CLI
- No copy/paste between panes
- No pane drag-and-drop

## References

- Blueprint §6.4 — Long-Lived Terminal Sessions
- Blueprint §10.4 — TerminalSession
- Blueprint §28 — Hidden Pane Behavior
- Blueprint §53 — Resource Lifecycle Tests
- Blueprint §68, Slice 4 — Multiple Pane Terminals
