# Milestone 5 — Projects & Tabs

> Add Project and Tab domain models, project sidebar, tab bar, and focus model.

**Status:** Complete — see [status.md](status.md) for the live record and evidence. The `## Goals` list is the delivered scope; `## Acceptance Criteria` items with documented limits stay unchecked.

## Overview

This milestone introduces the full workspace hierarchy: `Workspace → Project → Tab → PaneTree`. Users can organize work into multiple projects, each with multiple tabs, each with its own pane tree.

## Goals

- [x] `Project` and `Tab` domain types in `omaterm-core`
- [x] `Workspace` model aggregating projects
- [x] Project sidebar UI (left panel)
- [x] Tab bar UI (top of pane area)
- [x] Multiple projects with independent tab sets
- [x] Tab switching preserves terminal state
- [x] Project switching preserves tab/terminal state
- [x] Focus model: selected project → selected tab → focused pane → focused terminal

## Prerequisites

- Milestone 4 complete (multiple terminals in pane tree)

## Deliverables

### Updated `omaterm-core`

```rust
// workspace.rs
pub struct Workspace {
    pub windows: Vec<WorkspaceWindow>,
}

pub struct WorkspaceWindow {
    pub id: WindowId,
    pub projects: Vec<Project>,
    pub selected_project: Option<ProjectId>,
}

// project.rs
pub struct Project {
    pub id: ProjectId,
    pub custom_name: Option<String>,
    pub pinned_directory: Option<PathBuf>,
    pub tabs: Vec<Tab>,
    pub selected_tab: Option<TabId>,
}

impl Project {
    pub fn display_name(&self) -> &str;
    pub fn add_tab(&mut self, tab: Tab);
    pub fn remove_tab(&mut self, id: TabId) -> Result<()>;
    pub fn select_tab(&mut self, id: TabId) -> Result<()>;
}

// tab.rs
pub struct Tab {
    pub id: TabId,
    pub custom_name: Option<String>,
    pub root: PaneNode,
    pub focused_pane: PaneId,
}
```

### New UI Components in `apps/omaterm/src/ui/`

```
sidebar.rs     — Project list (left panel)
tab_bar.rs     — Tab strip (top of content area)
window.rs      — Orchestrates sidebar + tabs + pane layout
```

### Layout

```
┌──────────────────────────────────────────────────────────────┐
│ OmaTerm                                                      │
├──────────────┬───────────────────────────────────────────────┤
│              │ Tab 1  │ Tab 2  │ Tab 3  │  +                 │
│  Projects    ├───────────────────────────────────────────────┤
│              │                                               │
│  ● my-app    │           Pane Tree Content                   │
│    backend   │                                               │
│    frontend  │                                               │
│              │                                               │
└──────────────┴───────────────────────────────────────────────┘
```

## Architecture Notes

### Hierarchy and Failure Contracts

- First launch creates one default project rooted at a valid launch directory
  (otherwise `$HOME`), one tab, and one shell. Surface spawn failures as an exited/
  failed pane with retry rather than claiming a working terminal exists.
- New tabs start in the project's pinned directory. Splits inherit the target's
  last confirmed CWD, with project directory then home fallback. Validate paths
  before spawn; report fallbacks. CWD tracking is established in M3.
- Closing the final pane closes its tab; closing the final tab leaves an empty
  project with a new-tab action. Closing the final project leaves a welcome/empty
  workspace with a new-project action. Do not spontaneously recreate shells.
- After close, select the next surviving sibling in list/traversal order, otherwise
  the previous sibling. Within a nonempty tab `focused_pane` always references a
  leaf. Empty parent selections are `None`; no input may target stale sessions.
- Closing a tab/project schedules cleanup of all owned sessions through the same
  M4 lifecycle coordinator, including hidden/exited sessions. Test partial cleanup
  failures, repeated close, and focus fallback at every hierarchy level.
- Switching hierarchy selection changes visibility and semantic focus, not session
  ownership. Test sustained output in hidden tabs/projects and unchanged child PIDs.
- Create/close operations remain shared application/core operations before M7.
  Add keyboard project switching and keyboard pane resize alongside tab navigation.

### Focus Chain

```
Workspace
  └── selected_project: ProjectId
        └── selected_tab: TabId
              └── focused_pane: PaneId
                    └── TerminalSession (via PaneContent::Terminal(SessionId))
```

Keyboard input resolves through this chain to reach the correct terminal.

### Terminal Lifetime Across Tabs

Switching tabs MUST NOT destroy terminals in the previous tab. All `TerminalSession` instances remain alive in the `TerminalRegistry`. Only the renderer visibility changes.

### Project Directory

Initially, a project's root directory is just `pinned_directory`. Auto-detection via git (finding nearest `.git` from active terminal CWD) is a future feature.

## Implementation Steps

1. **Add `Workspace`, `Project`, `Tab` to `omaterm-core`**
2. **Refactor pane tree** to live inside `Tab`
3. **Implement project sidebar** GPUI component
4. **Implement tab bar** GPUI component
5. **Wire project selection** — clicking project shows its tabs
6. **Wire tab selection** — clicking tab shows its pane tree
7. **Focus chain** — keyboard input resolves through workspace → project → tab → pane
8. **New project** — keyboard shortcut or sidebar action to create project
9. **New tab** — keyboard shortcut or tab bar action to create tab
10. **Close tab** — removes tab, handles last-tab-in-project edge case
11. **Test tab switching** — verify terminals survive
12. **Test project switching** — verify all terminals survive

## Acceptance Criteria

- [ ] Can create multiple projects
- [ ] Each project has independent tabs
- [ ] Each tab has its own pane tree
- [ ] Project sidebar shows all projects
- [ ] Tab bar shows tabs for selected project
- [ ] Switching projects preserves terminal state in all projects
- [ ] Switching tabs preserves terminal state in all tabs
- [ ] Focus resolves correctly after project/tab switch
- [ ] Keyboard shortcuts work: new project, new tab, close tab, next/prev tab
- [ ] `omaterm-core` still has no `gpui` dependency
- [ ] `cargo test -p omaterm-core` passes
- [ ] Default/empty workspace, last-child close, and spawn/cleanup failures tested
- [ ] Hidden projects/tabs drain output with unchanged session and child identity
- [ ] Project switching and pane resizing are keyboard-accessible on Wayland

## Non-Goals

- No persistence (next milestone)
- No Git integration
- No file tree
- No project search
- No tab drag reordering
- No auto-detect project from git

## References

- Blueprint §6.2 — Project → Tab → Pane Tree
- Blueprint §12 — Workspace Domain Model
- Blueprint §14 — Focus Model
- Blueprint §31 — Project Directory Model
- Blueprint §36 — Keyboard-First Interaction
- Blueprint §57 — Design Language
- Blueprint §68, Slice 5 — Projects/Tabs
