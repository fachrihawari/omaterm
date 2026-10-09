# Milestone 13 — File Tree + Filename Search

> VSCode-Explorer-style file sidebar plus `Ctrl+P`-style fuzzy file search,
> scoped to the M12 project root. Kero's Files panel is the behavioral
> reference; no GPL code is incorporated.

**Status:** Complete with documented limits — see [status.md](status.md) for the live record and evidence. The `## Goals` list is the delivered scope; `## Acceptance Criteria` items with documented limits stay unchecked.

## Product Contract

A compact sidebar tree (collapsible folders, keyboard navigable) and a fuzzy
filename search box over the project root. Since the editor is deferred to
v0.3, "open" routes to the user's `$EDITOR` inside the focused terminal via
a stable `FileCommand::Open` (forwards-compatible with the future editor
pane). Copy-path uses shell-escaped absolute paths (blueprint §47 policy
reuse).

## Goals

- [x] Tree panel: expand/collapse, arrow-key navigation, live fuzzy filter,
  virtualized rendering (no thousands of GPUI nodes for large repos).
- [x] `notify`-backed refresh: debounced, revision-ordered, cancelled on
  project switch; inotify-limit exhaustion shows a warning banner and keeps
  the last good tree.
- [x] Actions: copy absolute path, reveal in terminal (`cd` via
  `terminal.send`), open in `$EDITOR` (via `terminal.run` semantics).
- [x] `FileCommand::{List, Search, Open}` through the dispatcher (blueprint
  §62); `file.list` / `file.search` wire methods + CLI with bounded
  `{entries[{path, kind}], truncated}` envelopes (default limit 100,
  server-enforced cap).
- [x] Persistence: bounded expanded-dirs per project in snapshot schema v2
  with a tested v1→v2 migration. `PaneContent` is untouched (still
  `Empty/Terminal`).

## Prerequisites

- M12 complete (root resolution, boundary, `[files]` config, spike record).
- `ignore` + `notify` + fuzzy crate selected in the M12 spike.

## Deliverables

- `omaterm-context`: `list_dir`, `search_files` (off-thread, cancellable,
  ignore-respecting, symlink-rejecting).
- Core: `FileCommand`, `MAX_FILE_ENTRIES` validation, `ProcessEntry`-style
  `FileEntry` result DTOs.
- Desktop: sidebar tree section, search input, action handlers — all via
  `dispatch_command`, no bespoke mutations.
- Protocol/bridge/CLI: `file.list {project_id?, dir?, limit}`,
  `file.search {project_id?, query, limit}`, `file.open {project_id?, path}`
  (terminal-routed); human + `--json` output.

## Test Plan

- Unit: ranking order, hidden/ignored handling, limit/truncation flags,
  traversal rejection, migration v1→v2.
- Integration: 10k-file generated tree (bounded time, no UI-thread block),
  watcher create/delete/rename burst, inotify-limit error path.
- Desktop live: big-repo browse + filter + copy-path + `file.search` CLI
  parity on the same instance.

## Acceptance Criteria

- [ ] Tree + search + all three actions work keyboard-only on Wayland
- [ ] 10k-file repo stays responsive; truncation flags accurate
- [ ] Every wire method has parser/mapping/e2e + JSON + exit-code tests
- [ ] Expanded-dirs survive restart; v1 snapshots migrate cleanly
- [ ] Quality gates green (fmt, serial tests, clippy, check-docs, diff-check)

## Non-Goals

- No editor pane, preview, dirty state, or highlighting (v0.3, §34).
- No content/text search. No git integration in the tree (icons/badges later).
- No `terminal wait` (v0.4). No Ghostty/macOS/Windows.

## References

- Blueprint §23 (v0.2), §26 (async), §30 (sidebar state), §35–§36 (palette,
  keyboard-first), §43–§45, §47 (safe paths), §52–§54 (perf, lifecycle,
  testing), §61–§66
