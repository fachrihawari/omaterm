# VS Code Workbench UI Redesign Plan

> Make OmaTerm feel like a polished VS Code workbench while preserving its
> terminal-first identity: terminals remain the primary work surface, and no
> editor, debugger, extension marketplace, or browser pane is introduced.

## Purpose

The current desktop window is functionally capable but visually reads as an
early prototype: projects occupy a narrow left column, Files/Git occupy a
separate fixed right column, controls are text chips, and the terminal lacks
the surrounding hierarchy expected from a developer workspace.

`vscode_ui_clone.html` is the visual reference for hierarchy, density,
contrast, tabs, sidebar behavior, diff treatment, and status information. It
is not a feature specification and must not cause v0.3 IDE features to be
implemented early.

## Product Boundary

In scope:

- A VS Code-like dark workbench chrome and interaction model.
- Activity rail, contextual sidebar, tab strip, primary terminal/diff surface,
  lightweight notices, and status bar.
- Existing Files, Git, project, terminal, and diff behavior rendered in the
  new shell.
- Keyboard and pointer access to every replacement control.
- Responsive minimum-width behavior and panel resizing/collapse.

Out of scope:

- Text-file editor, language services, debugger, extension marketplace,
  browser panes, or fake inactive activity items.
- New domain commands, IPC methods, persistence schema, or changes to
  terminal/session lifetime.
- Mimicking VS Code branding, proprietary assets, or copying its code.

## Non-Negotiable Constraints

- `omaterm-core` remains GPUI-free. This is a desktop rendering refactor, not
  a core-model rewrite.
- A terminal tab remains a long-lived terminal pane tree. Switching visual
  tabs must never recreate a session.
- A diff remains a view-local, read-only preview until a deliberate
  `PaneContent::Diff` design is approved. It is not persisted and does not
  become a terminal.
- Existing UI actions keep dispatching shared `OmaCommand` operations; no
  business logic moves into click handlers.
- Every new visual state has an empty, loading, error, focused, hover, and
  selected treatment. Keyboard equivalents and focus order are part of the
  acceptance criteria, not polish deferred to the end.
- Do not render features that do nothing. The activity rail initially exposes
  only Explorer and Source Control; later panels are added only with their
  underlying milestones.

## Target Workbench

```text
┌────────────────── command/title bar ──────────────────────────────────────┐
├──┬──────────── contextual sidebar ───────────┬──── primary work surface ──┤
│A │ EXPLORER / SOURCE CONTROL                  │ terminal tabs | diff tabs  │
│c │ workspace/project switcher                 ├───────────────────────────┤
│t │ file tree OR source-control groups         │ context/breadcrumb row     │
│i │                                             ├───────────────────────────┤
│v │                                             │ recursive terminal panes   │
│i │                                             │ or selected diff preview   │
│t │                                             │                            │
│y │                                             ├───────────────────────────┤
│  │                                             │ transient notices only     │
├──┴────────────────────────────────────────────┴───────────────────────────┤
│ git branch | sync/errors | focused pane | encoding/font/terminal metadata │
└────────────────────────────── status bar ─────────────────────────────────┘
```

The work surface is intentionally not an editor canvas. For terminal tabs the
context row identifies the selected project, tab, and focused pane. For a diff
preview it identifies the repository-relative path, staged/working-tree side,
and diff mode. This retains the mockup's workbench cues without misrepresenting
OmaTerm as an IDE.

## Visual System

Create one desktop-only `workbench_theme` module rather than scattering raw
hex values throughout `main.rs`. It owns named semantic tokens and constants;
components request roles such as `panel_background` or `tab_active`, never a
literal color.

Initial dark baseline, tuned from the supplied reference:

| Role | Value | Use |
|---|---:|---|
| Window/activity background | `#181818` | title region, activity rail |
| Sidebar background | `#1f1f1f` | Explorer and Source Control |
| Tab strip/panel background | `#252526` | tabs and secondary headers |
| Main surface | `#1e1e1e` | terminal and diff canvas |
| Raised/input surface | `#313131` | search and commit input |
| Subtle border | `#2b2b2b` | all structural seams |
| Hover | `#2a2d2e` | interactive rows |
| Selection | `#37373d` | selected sidebar row/tab state |
| Accent | `#007acc` | active tab top border, focus ring |
| Primary text | `#cccccc` | normal labels |
| Secondary text | `#8b8b8b` | metadata |
| Added/removed diff | existing semantic green/red | diff backgrounds and gutters |

Typography and density:

- Use the existing terminal font only for terminal and diff code lines.
- Use the system UI face for workbench chrome; 12px labels, 11px uppercase
  section headers, 13px tree rows, and 22px status bar.
- Standardize 28px sidebar rows, 35px tab/title rows, 24px section rows, 48px
  activity items, and a 1px border rhythm.
- Use a licensed icon set or existing project glyphs after a small dependency
  and licensing spike. Prefer MIT-licensed Codicons-compatible assets; record
  the chosen source/license in `docs/dependencies.md`. Do not substitute emoji
  or unrelated Unicode symbols for core controls.

## Implementation Plan

### Phase 0: Establish a Measurable Baseline

1. Capture current release-build screenshots at 1024x768, 1440x900, and a
   narrow 800px width for terminal, split-terminal, Files, Git, and diff.
2. Record layout defects against the target: duplicate sidebars, missing
   hierarchy, inconsistent control density, weak selected/focus state, tab
   ambiguity, and absent global status information.
3. Add a manual visual checklist to `docs/status.md`; screenshots are evidence
   only when captured from an isolated release instance.
4. Finish the existing M15 behavior and tests before changing its rendering
   substantially. The UI work must not mask the missing partial-hunk staging
   contract or the outstanding Wayland proof.

Exit criteria: a reviewed reference set and a checklist that lets each phase
be accepted or rejected without subjective "looks better" claims.

### Phase 1: Extract UI Shell and Design Tokens

1. Move the monolithic outer-window rendering in `apps/omaterm/src/main.rs`
   into private desktop modules: `workbench_theme`, `workbench_shell`,
   `activity_bar`, `context_sidebar`, `tab_strip`, and `status_bar`.
2. Keep `WorkspaceView` as the application coordinator. Components receive
   view-local state plus closures/listeners; they do not reach into core or
   duplicate router mutations.
3. Replace global spacing/color literals in chrome with theme roles. Terminal
   renderer colors remain independent until separately reviewed.
4. Add reusable primitives: icon button, section header, list row, tab, badge,
   resizer, notice strip, tooltip label, and focus-ring wrapper.
5. Preserve existing Files/Git rendering during extraction; this phase changes
   structure and visual primitives only, not panel behavior.

Exit criteria: no behavior regression, no raw chrome palette outside the theme
module, and the desktop target still passes its focused tests and Clippy.

### Phase 2: Replace the Window Frame

1. Add a 35px in-app command/title row. On Linux it shows the workspace name
   and an accessible command-palette affordance, not fake macOS traffic lights.
   Native window controls remain the compositor's responsibility.
2. Replace the 180px project column and fixed right sidebar with:
   - a 48px left activity rail;
   - one left contextual sidebar, default 260px and clamped to 190–460px;
   - the existing primary pane/diff surface filling the rest.
3. Put Explorer and Source Control in the activity rail. Clicking the active
   icon toggles the contextual sidebar; keyboard commands must provide the same
   show/select/toggle behavior.
4. Move project selection into the Explorer's top "WORKSPACE" section. It
   supports selected project, directory change, close project, and new project
   without consuming a permanent second sidebar.
5. Move current Files and Git content into that sidebar. Preserve the selected
   panel per window as view state; do not silently change snapshot schemas.
6. Add a visible, pointer-draggable 3px sidebar resizer with keyboard resize
   support and a collapse control. Clamp widths and preserve a usable main
   surface at narrow sizes.

Exit criteria: only one contextual sidebar is visible; Files/Git/project
actions remain reachable; sidebar collapse/resize works by pointer and
keyboard; split terminals retain their geometry and receive input.

### Phase 3: Rebuild Tabs and Work Surface Chrome

1. Render terminal and diff previews in one 35px tab strip with VS Code-like
   inactive, active, hover, close, dirty/attention, and keyboard-focus states.
   Tabs use icons plus concise labels; the active tab gets an accent top edge.
2. Keep real terminal tabs as `TabCommand` selections. Diff chips remain
   explicitly view-local preview entries and never impersonate core tabs.
3. Add a compact context row beneath tabs:
   - terminal: project name, tab name, focused pane identifier/count;
   - diff: file icon, relative path, working tree/staged state, diff controls;
   - empty/failure: clear primary action and explanation.
4. Move history state out of the tab strip into the status/notice area. It is
   application status, not a document tab.
5. Style divider and terminal pane focus state so a selected pane is obvious
   without overwhelming terminal content. Preserve all current split drag and
   keyboard resize behavior.

Exit criteria: terminal tab switching, close/new tab, diff open/close, pane
focus, and keyboard traversal are visually unambiguous and behaviorally
unchanged.

### Phase 4: Finish Explorer and Source Control Quality

1. Explorer gets a VS Code-style header, project-root section, tree indentation,
   icon alignment, hover/selected state, overflow-safe filenames, modified
   decorations, and collapsed auxiliary sections only where backed by data.
2. Source Control gets a header with refresh/menu actions, branch summary,
   grouped staged/unstaged/untracked files, count badges, concise status
   decorations, and clear disabled/loading/error states.
3. Keep destructive discard's existing two-step arm-confirm behavior visibly
   explicit. Never hide an armed destructive action in an icon-only control.
4. Opening a Git row activates the diff preview tab. The diff adds a proper
   file header, working-tree/staged label, hunk toolbar, line-number gutters,
   semantic add/remove/context backgrounds, bounded viewport indication, and
   stage affordance state.
5. Do not claim split-diff rendering or partial-hunk staging until data and
   command semantics actually support them. The supplied split diff is a later
   visual reference, not permission to fake it.

Exit criteria: dense panels remain scannable at 1024x768, binary/empty/error
states are clear, and all M13–M15 functions remain semantically identical.

### Phase 5: Status, Notices, and Interaction Polish

1. Add a 22px status bar. Left side: selected project's Git branch and change
   summary when available, plus errors/warnings. Right side: focused pane,
   terminal profile/font-size, and persistent state indicators that already
   exist. Omit unavailable values instead of displaying fabricated metadata.
2. Consolidate transient warnings (persistence, config, input, spawn, history)
   into a single notice strip with severity, concise copy, dismissal rules,
   and action buttons where an action exists.
3. Add consistent hover tooltips for icon-only controls, visible focus rings,
   selection colors meeting contrast requirements, and no mouse-only operation.
4. Audit focus routing with an active terminal: sidebar interaction must not
   leak typed input to a terminal; returning to the surface must restore the
   correct focused terminal pane.
5. At widths below the accepted minimum, collapse the context sidebar first;
   do not allow chrome to obscure terminal columns/rows. Record the supported
   minimum width after measurement.

Exit criteria: the chrome has an obvious information hierarchy, no orphaned
status chips, and keyboard/pointer focus behavior is predictable.

### Phase 6: Validation, Performance, and Documentation

1. Add focused state tests for activity-panel switching, sidebar collapse and
   width clamping, tab/diff preview interactions, focus restoration, and
   status/notice state mapping. Keep pure visual constants out of brittle unit
   snapshots.
2. Run required Rust gates: `cargo fmt --all --check`, `cargo test --workspace`,
   and `cargo clippy --workspace --all-targets -- -D warnings`. If the existing
   full-suite timeout persists, record it as a failure/blocker and run focused
   suites separately; never report the workspace gate as passed.
3. Perform isolated release Wayland validation at the three baseline sizes:
   Files, Git, Git-to-diff, tab switch, nested terminal splits, sidebar resize,
   keyboard-only navigation, empty project, and error notice flows.
4. Capture before/after screenshots using identical state and sizes. Add exact
   commands/results and desktop evidence to `docs/status.md`.
5. Update M13–M15 docs only for user-visible placement changes; update the
   acceptance matrix if the shell introduces a release requirement. Do not mark
   M17 complete merely because the chrome is improved.

## Delivery Order

Implement Phases 0–2 as one architecture-safe workbench-shell change, then
Phases 3–5 in separately reviewable changes. Do not interleave the shell
refactor with new M16 command-palette behavior, true hunk staging, or M18
process features. Each change should leave the application runnable and use
the same domain/router operations as before.

## Definition of Done

- At 1024x768 and 1440x900, the app has the target workbench hierarchy without
  overlap, clipped essential controls, or a second fixed sidebar.
- Explorer, Source Control, terminal tabs, diff preview, split panes, notices,
  and status bar have coherent active/hover/focus/empty/error states.
- Terminal-first behavior, session lifetime, pane-tree geometry, router/IPC
  parity, and persistence contracts are unchanged.
- No unimplemented buttons or decorative feature panels appear.
- Keyboard-only flows cover changing activity panel, selecting projects/files/
  Git rows/tabs, collapsing/resizing sidebar, opening/closing diff, navigating
  panes, and restoring terminal input focus.
- Automated checks and isolated Wayland evidence are recorded honestly.

## Risks and Mitigations

| Risk | Mitigation |
|---|---|
| Styling refactor changes terminal behavior | Keep terminal renderer and router untouched; validate pane focus, split, resize, and hidden-session output after each phase. |
| A visual tab abstraction leaks into core semantics | Make terminal tabs and view-local diff previews separate render paths with explicit types. |
| Main UI remains a growing `main.rs` | Extract desktop-only components during Phase 1 before changing frame layout. |
| Too-close VS Code clone expands scope into an IDE | Enforce product boundary; only render capabilities backed by OmaTerm. |
| New icon dependency creates license/supply-chain debt | Perform a small license/API spike first and record the exact choice; use no copied assets without approval. |
| Narrow displays degrade terminal usability | Measure at 800/1024 widths, collapse sidebar first, and document the supported lower bound. |

## References

- `vscode_ui_clone.html` — supplied visual hierarchy and interaction reference.
- `apps/omaterm/src/main.rs` — current desktop composition and router wiring.
- `docs/13-milestone-13-file-tree.md`, `docs/14-milestone-14-git-status.md`,
  `docs/15-milestone-15-diff-viewer.md` — existing developer-context behavior.
- `OMATERM_AGENT_BLUEPRINT.md` §12 — content extensibility boundary.
- `OMATERM_AGENT_BLUEPRINT.md` §25 — GPUI dependency direction.
- `OMATERM_AGENT_BLUEPRINT.md` §68 — vertical-slice implementation rule.
