# Milestone 12 — Project Context Root

> Foundation for all v0.2 developer-context features: every project resolves
> to one filesystem root that file-tree, git, diff, search, and the process
> panel can target.

**Status:** Complete — see [status.md](status.md) for the live record and evidence. The `## Goals` list is the delivered scope; `## Acceptance Criteria` items with documented limits stay unchecked.

## Product Contract

M12 introduces no user-visible panel. It provides the project-root
resolution, filesystem boundary, and configuration sections that M13–M18
build on. UX follows VSCode conventions (workspace-folder root per project);
Kero's project/directory behavior is the behavioral reference. No GPL code
is incorporated (project license: MIT OR Apache-2.0).

## Goals

- [x] Deterministic root per project: `pinned_directory` when set, else the
  nearest enclosing git repository of the active tab's shell CWD (blueprint
  §31). Non-repo projects without a pin resolve to "no root" (explicit empty
  state downstream, never an error).
- [x] Filesystem boundary seam: `canonicalize_under_root()` rejects
  `path_outside_root` before any read; no `cfg(target_os)` spread (blueprint
  §50).
- [x] Config sections `[files]` (`max_results`, `show_hidden`) and `[git]`
  (`refresh_secs`) with the M11D preserve-format/explicit-error pattern
  (blueprint §61).
- [x] Logging categories `files`, `git`, `search` added to `omaterm-logging`
  with the M11G redaction audit (blueprint §45).
- [x] Query parity: `project.root` wire method + `omaterm project root` CLI.

## Prerequisites

- M11 complete (config, logging, OS-boundary, packaging patterns).

## Deliverables

### New crate: `omaterm-context` (lean; split only when boundaries demand it)

- `resolve_root(pinned, active_cwd)`: pin wins; else bounded
  `git rev-parse --show-toplevel` off the UI thread with timeout; agent
  worktree re-rooting is deferred.
- `canonicalize_under_root(root, user_path)`: symlink-aware, rejects
  escapes with `path_outside_root`.
- Ignore policy: `.gitignore` + `.ignore` semantics via the `ignore` crate
  (spike first: license MIT/Unlicense verified, Omarchy behavior checked);
  `show_hidden` toggles dotfile inclusion.
- `OmaCommand::Project(ProjectCommand::Root)` (query, no effects) plus
  `project.root` (`{"project_id?"}` → `{"root?", "source": "pinned|git|none"}`).

### Spike record (before feature code, M10 precedent)

- `ignore` (walking), `notify` (watching, M13), `nucleo`/`fuzzy-matcher`
  (M13 ranking): exact versions, API fit, license expressions
  (permissive only — no GPL/AGPL), transitive review in `docs/dependencies.md`.

## Test Plan

- Unit: pin-wins matrix, git-toplevel fallback, missing git binary, timeout,
  traversal/`..`/symlink-escape rejection, ignore + hidden policy.
- Integration: real repos in `/tmp` (repo, nested repo, non-repo, deleted
  pin), scope filtering for project credentials.
- Desktop live (release, isolated state): `project root` CLI matches the
  sidebar header; deleted-pin project shows the empty-root state.

## Acceptance Criteria

- [ ] Root resolution matrix green incl. non-repo empty state
- [ ] Traversal rejection tests green; no UI-thread blocking (timed)
- [ ] `project.root` parser/mapping/e2e + JSON envelope + exit codes
- [ ] Spike versions/licenses recorded; `Cargo.lock` tracked
- [ ] Quality gates: `cargo fmt --check`, serial `cargo test --workspace`,
  `clippy -- -D warnings`, `check-docs.py`, `git diff --check`

## Non-Goals

- No visible tree/git/diff UI (M13–M15). No editor (v0.3, blueprint §34).
- No content grep (`rg`-style text search is a later milestone).
- No `terminal wait` (v0.4). No Ghostty/macOS/Windows.

## References

- Blueprint §2 (paths, `OMATERM_` prefix), §23 (v0.2), §26 (async),
  §30–§31 (persistence, project directory), §43–§45 (output, errors,
  logging), §50 (OS boundary), §61–§66 (config, shared logic, IDs, bounds,
  versioning, cancellation)
