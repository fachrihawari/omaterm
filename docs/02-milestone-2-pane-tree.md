# Milestone 2 — Pure Pane Tree

> Implement the recursive pane tree model with full unit test coverage. Render placeholder panes in GPUI. No terminal yet.

## Overview

The pane tree is one of OmaTerm's most important architectural decisions. This milestone creates the `omaterm-core` crate with the recursive binary `PaneNode` model and all operations needed to manipulate it.

The pane tree code MUST be independent of GPUI and fully unit-testable.

## Goals

- [ ] `omaterm-core` crate created
- [ ] `PaneNode` recursive enum implemented
- [ ] Typed IDs: `PaneId`, `SplitId`
- [ ] All pane operations implemented and tested
- [ ] GPUI renders placeholder colored panes using the tree
- [ ] Pane focus navigation works
- [ ] Pane resize (split fraction) works

## Prerequisites

- Milestone 1 complete (GPUI window renders)

## Deliverables

### New Crate: `omaterm-core`

```
crates/omaterm-core/
├── Cargo.toml
└── src/
    ├── lib.rs
    ├── ids.rs          # WindowId, ProjectId, TabId, PaneId, SplitId, SessionId
    ├── pane.rs         # PaneNode, Pane, PaneContent, SplitAxis
    └── error.rs        # Core error types
```

### Key Types

```rust
// ids.rs
use uuid::Uuid;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub struct PaneId(pub Uuid);

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub struct SplitId(pub Uuid);

impl PaneId {
    pub fn new() -> Self { Self(Uuid::new_v4()) }
}

impl SplitId {
    pub fn new() -> Self { Self(Uuid::new_v4()) }
}

// pane.rs
pub enum SplitAxis {
    Horizontal,
    Vertical,
}

pub enum SplitDirection {
    Left,
    Right,
    Up,
    Down,
}

pub enum PaneNode {
    Pane(Pane),
    Split {
        id: SplitId,
        axis: SplitAxis,
        fraction: f32,
        first: Box<PaneNode>,
        second: Box<PaneNode>,
    },
}

pub struct Pane {
    pub id: PaneId,
    pub content: PaneContent,
}

pub enum PaneContent {
    Terminal(SessionId),
    Empty, // placeholder for this milestone
}
```

### Required Operations

```rust
impl PaneNode {
    /// Insert a new pane beside the target pane
    pub fn split(&mut self, target: PaneId, direction: SplitDirection, new_pane: Pane) -> Result<()>;

    /// Remove a pane and collapse its parent split
    pub fn remove(&mut self, target: PaneId) -> Result<Option<Pane>>;

    /// Change the split fraction
    pub fn resize(&mut self, split: SplitId, fraction: f32) -> Result<()>;

    /// Find a pane by ID
    pub fn find(&self, target: PaneId) -> Option<&Pane>;

    /// Find mutable pane by ID
    pub fn find_mut(&mut self, target: PaneId) -> Option<&mut Pane>;

    /// Enumerate all panes in the tree
    pub fn panes(&self) -> Vec<&Pane>;

    /// Resolve directional neighbor for focus navigation
    pub fn neighbor(&self, from: PaneId, direction: SplitDirection) -> Option<PaneId>;

    /// Equalize all split fractions in the tree
    pub fn equalize(&mut self);
}
```

## Architecture Notes

### Split Behavior

Splitting pane A to the right:

```
Before:  A

After:   horizontal
         ├── A
         └── B (new)
```

Nested split — splitting B right when tree is `vertical(A, B)`:

```
Before:  vertical
         ├── A
         └── B

After:   vertical
         ├── A
         └── horizontal
             ├── B
             └── C (new)
```

**NEVER** flatten into `A | B | C`. Each split only affects the targeted pane's rectangle.

### Remove Behavior

Removing a pane collapses its parent split:

```
Before:  horizontal
         ├── A
         └── B

Remove B: A (promoted to parent position)
```

### Dependency Rule

```
omaterm-core has NO dependency on gpui
```

The UI crate reads the tree and renders it. The core crate knows nothing about rendering.

## Implementation Steps

1. **Create `omaterm-core` crate** with `ids.rs`, `pane.rs`, `error.rs`
2. **Implement `PaneNode` enum** with `Pane` and `Split` variants
3. **Implement `split()` operation** — insert beside target in correct direction
4. **Implement `remove()` operation** — remove pane, collapse parent
5. **Implement `resize()`** — change split fraction with bounds checking
6. **Implement `find()`, `panes()`, `neighbor()`** — traversal operations
7. **Implement `equalize()`** — set all fractions to 0.5
8. **Write comprehensive unit tests** for all operations
9. **Update GPUI app** to render placeholder colored rectangles for each pane
10. **Wire keyboard shortcuts** for split/close/focus-navigation with placeholder panes

## Acceptance Criteria

- [ ] `cargo test -p omaterm-core` passes with comprehensive pane tree tests
- [ ] Split right creates `horizontal(existing, new)`
- [ ] Split down creates `vertical(existing, new)`
- [ ] Split left creates `horizontal(new, existing)`
- [ ] Split up creates `vertical(new, existing)`
- [ ] Nested splits work correctly (don't flatten)
- [ ] Remove collapses parent split
- [ ] Removing last pane is handled gracefully
- [ ] Resize clamps fraction to [0.1, 0.9]
- [ ] Focus navigation resolves correct neighbor
- [ ] GPUI renders colored placeholder panes matching the tree structure
- [ ] `omaterm-core` has zero dependencies on `gpui`
- [ ] `cargo clippy -p omaterm-core` has zero warnings

## Non-Goals

- No terminal rendering
- No PTY management
- No project/tab model (just panes)
- No persistence
- No IPC/CLI
- No drag-and-drop pane rearrangement
- No pane zoom

## References

- Blueprint §6.3 — Recursive Pane Layout
- Blueprint §12 — Workspace Domain Model
- Blueprint §13 — Pane Tree Operations
- Blueprint §14 — Focus Model
- Blueprint §25 — Dependency Direction
- Blueprint §62 — No Duplicate Business Logic
- Blueprint §63 — IDs Over Screen Coordinates
- Blueprint §68, Slice 2 — Pure Pane Tree
