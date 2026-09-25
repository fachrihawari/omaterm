# Milestone 6 — Persistence

> Save workspace state to disk. Restore projects, tabs, pane layouts, and working directories on restart. Launch fresh shells.

## Overview

When OmaTerm restarts, the user should see their projects, tabs, and pane layout exactly as they left it — but with fresh terminal shells. This milestone implements versioned workspace snapshots.

## Goals

- [ ] `omaterm-state` crate created
- [ ] Versioned snapshot format (JSON, `schema_version: 1`)
- [ ] Serialize: projects, tabs, pane tree, split fractions, focused pane, working directories
- [ ] Auto-save on state changes
- [ ] Restore on startup: rebuild workspace → fresh shells in remembered directories
- [ ] Handle missing/corrupt snapshot gracefully (start fresh)

## Prerequisites

- Milestone 5 complete (projects and tabs working)

## Deliverables

### New Crate: `omaterm-state`

```
crates/omaterm-state/
├── Cargo.toml
└── src/
    ├── lib.rs
    ├── snapshot.rs     # Snapshot types (serializable)
    ├── store.rs        # Load/save logic
    └── migration.rs    # Schema version migration
```

### Snapshot Format

```json
{
  "schema_version": 1,
  "windows": [
    {
      "id": "...",
      "selected_project": "...",
      "projects": [
        {
          "id": "...",
          "custom_name": "my-app",
          "pinned_directory": "/home/user/Code/my-app",
          "selected_tab": "...",
          "tabs": [
            {
              "id": "...",
              "custom_name": null,
              "focused_pane": "...",
              "root": {
                "type": "split",
                "id": "...",
                "axis": "horizontal",
                "fraction": 0.5,
                "first": {
                  "type": "pane",
                  "id": "...",
                  "working_directory": "/home/user/Code/my-app"
                },
                "second": {
                  "type": "pane",
                  "id": "...",
                  "working_directory": "/home/user/Code/my-app/src"
                }
              }
            }
          ]
        }
      ]
    }
  ]
}
```

### Storage Location

```
$XDG_STATE_HOME/omaterm/workspace-v1.json
```

Fallback: `~/.local/state/omaterm/workspace-v1.json`

### Persist vs. NOT Persist

| ✅ Persist | ❌ Do NOT Persist |
|---|---|
| Project IDs, paths, names | PTY file descriptors |
| Tab IDs, names | Process handles |
| Pane tree structure | Raw renderer objects |
| Split fractions | GPUI entities |
| Focused pane | Thread/task handles |
| Terminal working directories | Socket handles |
| Selected project/tab | Scrollback content |
| Sidebar state | Terminal parser state |

### Restore Flow

```
startup
  ↓
load snapshot file
  ↓
deserialize → WorkspaceSnapshot
  ↓
rebuild Workspace, Projects, Tabs, PaneTrees
  ↓
for each pane:
  create TerminalSession
  launch shell in saved working_directory
  ↓
set focus to saved focused pane
```

## Architecture Notes

### Schema Versioning

Every snapshot starts with `"schema_version": N`. When the format changes:

```rust
match snapshot.schema_version {
    1 => load_v1(snapshot),
    _ => Err(UnsupportedVersion),
}
```

Future migrations transform v1 → v2 → v3 etc.

### Auto-Save Strategy

Save on meaningful state changes:
- Project created/deleted
- Tab created/deleted
- Pane split/closed
- Focus change (debounced)
- Working directory change

Debounce saves (e.g., at most once per 2 seconds) to avoid thrashing.

### Graceful Degradation

- Missing file → start with default workspace (one project, one tab, one pane)
- Corrupt JSON → log warning, start fresh
- Unknown schema version → log warning, start fresh
- Missing working directory → fall back to `$HOME`

## Implementation Steps

1. **Create `omaterm-state` crate**
2. **Define `WorkspaceSnapshot`** serializable types (serde)
3. **Implement `SnapshotStore::save()`** — serialize to JSON, write atomically
4. **Implement `SnapshotStore::load()`** — read, deserialize, validate version
5. **Add `to_snapshot()` / `from_snapshot()`** methods to domain types
6. **Wire auto-save** — trigger on state changes with debounce
7. **Wire restore on startup** — load snapshot → rebuild workspace → launch shells
8. **Test: save/restore round-trip** — state matches after restart
9. **Test: corrupt file handling** — graceful degradation
10. **Test: missing file handling** — clean first start

## Acceptance Criteria

- [ ] Closing and reopening OmaTerm restores the same project/tab/pane layout
- [ ] Terminals start fresh shells (not restored processes)
- [ ] Working directories are correct for each restored terminal
- [ ] Focused pane is restored
- [ ] Selected project and tab are restored
- [ ] Missing snapshot file → clean default workspace
- [ ] Corrupt snapshot file → clean default workspace + warning log
- [ ] Snapshot file is valid JSON and human-readable
- [ ] `schema_version` field is present
- [ ] `cargo test -p omaterm-state` passes

## Non-Goals

- No scrollback restoration
- No process restoration
- No cloud sync
- No multi-window state (single window for now)
- No configuration persistence (separate from workspace state)

## References

- Blueprint §6.6 — Persist Layout, Not Running Processes
- Blueprint §30 — Persistence
- Blueprint §29 — Terminal Scrollback (future)
- Blueprint §68, Slice 6 — Persistence
