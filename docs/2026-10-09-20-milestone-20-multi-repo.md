# Milestone 20 — Multi-Repo Project Support (VS Code style)

> A project root that contains several repositories gets VS Code-style
> treatment: a repo picker plus one collapsible section per repo in the
> Git tab, each with the full Git feature set. Discovery scans the
> project root plus one level of children (VS Code
> `repositoryScanMaxDepth` / Zed default parity).

**Status:** Implementation complete; native 2-repo Wayland validation pending — see [status.md](status.md) for the live record and evidence.

## Product Contract

Today a project resolves to exactly one filesystem root and the Git tab
renders exactly one repo (`resolve_root` in
`crates/omaterm-context/src/resolve.rs`, per-refresh
`resolve_root → git_status(root)` in
`apps/omaterm/src/git_panel.rs:412-466`). Monorepo-style checkouts
(root dir holding `api/`, `web/`, … each with its own `.git`) are
second-class: only the root itself is ever treated as a repo.

After M20, when the resolved project root is **not** itself a repo but
contains child repos one level down:

- the Git tab shows a **repo picker row** (repo name + count + chevron,
  VS Code Source Control convention) at the top;
- below it, one **section header per repository** showing that repo's
  branch and `+staged ~unstaged ?untracked` counts (or `clean`), with a
  collapse chevron on the active section;
- **the active repository's full body renders directly below the
  chrome** — branch row, Staged/Changes groups, commit box, stash, sync
  actions and the Graph, exactly the M14/M15/M18 body. Selecting another
  repository (picker row, dropdown row, or a section header) makes it the
  active repository through the semantic command, so its body replaces
  the previous one.

Single-repo, empty, and non-repo projects render exactly as today: no
picker, no sections, zero layout change.

Single-repo, empty, and non-repo projects render exactly as today: no
picker, no sections, zero layout change.

## Agreed Decisions (locked 2026-10-09)

- **Scan depth 1**: project root + direct children only. Deeper repos
  are a lazy-activation follow-up, not v1.
- **VS Code style UI**: picker + per-repo sections (not Zed's single
  flat list with header filter).
- **Files panel stays at the project root** in v1; only the Git tab
  gains repo switching.
- **Active repo selection**: last-saved if still present, else
  first-sorted repo name.
- **Lazy scan**: on first Git tab open / project-root change, not at
  `project open` time — zero cost for single-repo users.
- **No cross-repo operations** in v1: stage/commit/stash/sync/history
  always target one repo (blueprint §21 approval boundary unchanged).
- **Project scope unchanged** (blueprint §5.6/§20): repo selection is
  constrained to the caller's own project root;
  `path_outside_root`/`permission_denied` on escape.

## Goals

- [x] Depth-1 repo discovery in `omaterm-context` (fast path when the
  root itself is a repo; bounded readdir+stat scan otherwise).
- [x] `Project.active_repo` domain field + pure selection helpers in
  `omaterm-core`, no `gpui` dependency.
- [x] Snapshot schema v4 persisting `active_repo` (repo = child dir
  name, resolved against the live root so project dir moves don't
  break it); v1–v3 migration.
- [x] `ProjectCommand::ListRepos` / `SetActiveRepo` + `project.repos` /
  `project.set-active-repo` wire methods + CLI parity (traversal-
  rejected, project-scoped).
- [x] VS Code-style Git tab: picker row + per-repo section headers with
  per-repo panel state; one repo refreshed per poller tick; scan never
  on the UI thread.
- [x] Status bar shows the repository name before the branch when
  multi-repo.
- [ ] Native Wayland validation on a real 2-repo fixture (manual).

## Prerequisites

- M12 (root resolution), M14 (git status), M18-adjacent Git surfaces
  (stash/sync/history/graph) complete.

## Deliverables

### Phase A — Discovery (`omaterm-context`, no UI)

- `repos.rs`: `scan_repos(root) -> Vec<RepoEntry { name, path }>`:
  - root `/.git` exists (dir for normal repos, file for
    worktree/submodule gitfiles) → `[root]` fast path, no scan.
  - else `read_dir` root (cap 256 entries, skip hidden unless
    `show_hidden`, honor the existing `ignore` policy): child is a
    repo when `<child>/.git` exists as dir or file. No recursion, no
    content reads, no git spawns during the scan.
  - sorted by name; off-UI-thread only; target <200ms for 100
    children.
- `resolve_repos(pinned, active_cwd, saved_active) -> ProjectReposInfo
  { root, source, repos, active_repo }` composing the untouched
  `resolve_root` + `scan_repos` + core default-selection helper.
- Unit + integration tests: monorepo fixture (root + 3 child repos +
  plain dir + depth-2 repo correctly ignored), root-is-repo fast
  path, worktree gitfile, missing root → empty.

### Phase B — Domain + persistence (`omaterm-core`, `omaterm-state`)

- `Project.active_repo: Option<String>` (child dir name), default
  `None` in `Project::new`; pure helpers `set_active_repo(name,
  candidates)`, `resolve_active_repo(candidates)`,
  `default_repo(candidates)`; new `CoreError::UnknownRepo` on
  membership failure; shape check in `validate()`.
- DTOs: `RepoEntry { name, path }`, `ProjectReposInfo { root, source,
  repos, active_repo }` in `result.rs`, re-exported from `lib.rs`.
- Snapshot schema v3→v4: `ProjectSnapshot.active_repo`
  (`#[serde(default)]`), capture copies it, validate restores it;
  v1–v3 files decode with `None`; `deny_unknown_fields` retained.
- Tests: selection helpers (saved-missing falls back to
  first-sorted), membership rejection, snapshot round-trip with
  active repo, v3→v4 migration.

### Phase C — Commands / wire / CLI

- `ProjectCommand::ListRepos { project }` (query) and
  `SetActiveRepo { project, repo }` (mutation; name allow-listed
  against the live scan, `path_outside_root` on escape,
  project-scoped credentials).
- Wire `project.repos` / `project.set-active-repo`; CLI
  `omaterm project repos [--json]` + `omaterm project set-repo
  <name>`; git wire methods accept an optional `repo` override
  defaulting to the active repo (existing calls unchanged).
- Router + protocol + CLI parser/mapping/e2e tests.

### Phase D — Desktop Git tab (VS Code style)

- **One collapsible group header** `> Repositories · N repos` — the only
  chevron in the chrome, defaulting to expanded. Collapsing keeps the
  active repository's body visible (the list hides, the work does not).
- **Simple repo rows** beneath it: repository name, muted branch, and a
  dirty dot (or muted `clean`). The active row is highlighted; clicking a
  row switches the active repository. No per-row chevron, no dropdown,
  no `+staged ~unstaged ?untracked` counter text — those read as
  expand/collapse affordances and confuse which repository is active.
- **The active repository's full body renders below the chrome** —
  branch row, Staged/Changes groups, commit box, stash, sync actions and
  the Graph, exactly the M14/M15/M18 body. Selecting another repository
  makes it active through the semantic command, so its body replaces the
  previous one.
- Panels are keyed by `git_panel::RepoKey` (project + repo name, `None` =
  project root) so every repo keeps its own status, selection, collapse
  state, stash list and commit draft.
- Poller: refreshes **one repository per tick** (round-robin), so a
  monorepo with N repos costs N ticks of one bounded git call each
  instead of N unbounded calls per tick; the depth-1 scan refreshes on
  the Git tab opening, project switch, base-directory change, manual
  refresh, and a staleness window — always off the UI thread.
- Cap rendered rows at 32 with a `+N more repositories` overflow row.
- Status bar shows the repository name before the branch when
  multi-repo; sidebar cards stay identity-only (Oct-07 decision).
- Tests: section cap, active-repo identity, round-robin cursor and
  staleness unit tests in `git_repos`, per-repo isolation in
  `git_panel`, worker repo-root override; native Wayland on a 2-repo
  dirty fixture (switch → status/commit/stash/history follow) remains
  manual validation.

#### Chrome simplification (2026-10-09, user-directed)

The first Phase D render used a picker row (git icon + active repo name +
chevron + `N repos`) that opened a dropdown, plus a chevron on **every**
repository section header. Feedback: the `>`/`v` marks on the active
repository read as per-repo expand/collapse and made the active selection
ambiguous. Re-evaluated and simplified to the shape above:

| Before | After |
|---|---|
| Picker row + dropdown list | Single `Repositories` group header with a collapse chevron |
| Per-repo header with chevron + `+s ~u ?t` | Plain row: name, muted branch, dirty dot / `clean` |
| Active marked by a check inside the dropdown | Active marked by row highlight (and the body below) |
| `repo_picker_open: bool` | `repo_list_collapsed: bool` (Esc collapses; there is no dropdown to dismiss) |

Single-repo projects are untouched either way: the whole chrome returns
empty when `repos.len() < 2`.

#### Re-evaluation against the VS Code reference (2026-10-09, second pass)

The two reference captures nail down what VS Code really does, and what
we deliberately do **not** copy:

```text
VS Code (capture 1)                          VS Code (capture 2, dropdown open)
⌥ fortis-client ▾            2 repos         ⌥ fortis-backend ▾           2 repos
> fortis-backend feat/cms-roles     clean    ✓ fortis-backend
∨ fortis-client feat/...            clean      fortis-client                      ●
                                    ─────    ∨ fortis-backend feat/cms-roles     clean
                                             > fortis-client feat/...          +0 ~170
```

VS Code semantics: exactly one repository is expanded (`∨`) and it is
always the active one; the picker row names the active repository; the
dropdown carries `✓` plus a dirty dot. Our direction keeps the idea (one
active repository, highlight = body below) and drops the parts that
confused:

1. **No per-row chevron.** In VS Code the `∨` duplicates the highlight —
   the expanded row is the active row by definition. One chevron lives
   only on the `Repositories` group header (collapse the *list*, never a
   repository).
2. **No dropdown.** The row list is always visible under the header, so
   the picker's `✓`-in-dropdown function moves onto the rows themselves
   (highlight background). Nothing to open, nothing to dismiss.
3. **No numeric counters** (`+0 ~170` in capture 2). A dirty dot (or
   muted `clean`) carries the same signal at this density; exact counts
   live in the active repository's body.
4. **Branch stays** (muted, as in both captures) — it disambiguates
   checkouts of the same remote.

Locked chrome spec (multi-repo only):

```text
[chevron]  REPOSITORIES ........................... N repos
            fortis-backend   feat/cms-roles            clean
   [active] fortis-client    feat/cms-user-role-…         ●
   ── active repository's full M14 body below ──
```

Rendering parity with the sibling sections (added 2026-10-10): the chrome
speaks the same section grammar as STAGED CHANGES / CHANGES / GRAPH —
a 32px header with the chevron in a fixed 14px slot, uppercase
`META_10` muted title, the count in the sibling count pill, and 32px
rows indented under the header with a hairline between them (the last
hairline is also the chrome/body boundary). A **collapsed** header
appends the active repository name (`REPOSITORIES · fortis-client`,
title uppercase, name in its own case), because the highlight is hidden
and the body below it must stay identifiable — VS Code's picker row
carries the same identity. Both label rules live in
`git_repos::chrome_header_label` / `chrome_count_label` with unit tests;
render code stays free of formatting decisions.

Phase E consequences of the locked spec (the rest of Phase E stands):

- **E3 scope reduction:** collapsed rows need only status (branch, dirty
  bit). History, stash-list, branch-list, sync and diff workers only
  ever run for the **active** repository — no lazy per-repo expansion
  loading to build.
- **E4 addition:** a view-local `repo_list_collapsed: bool` per project
  (default expanded on the first multi-repo scan); Esc collapses the
  list; `repo_picker_open` and the dropdown branch are deleted.
- **E4 correction (2026-10-10):** Esc-only means Esc-only. Collapsing the
  list from `close_transient_menus` also collapsed it from the shell's
  root `on_any_mouse_down`, which bubbles *after* the group header's own
  toggle — so the click that expanded the list was immediately undone (and
  any unrelated left click collapsed it), leaving the chevron stuck with no
  way to reopen the list. The list now collapses only through
  `dismiss_transient_chrome` (Esc), gated by the pure
  `git_repos::esc_collapses_repo_list` predicate.
- **E7 addition:** a pure `repo_chrome(project, scan) -> ChromePlan`
  helper in `git_repos.rs` (visibility for 0/1/N repos, row order, active
  mark, 32-cap) with unit tests — render code itself is not unit-testable.
- **Cut list:** dropdown branch, per-row chevrons, `+s ~u ?t` counter
  text, the `CHECK` row-icon use.

## Test Plan

- Unit: scan fixtures, selection helpers, snapshot round-trip,
  command validation rejections.
- Integration: real monorepo in `/tmp`, stage→status round-trip per
  repo, CLI JSON matches panel.
- Desktop live (release, isolated state): 2-repo fixture, picker +
  sections, restart restores active repo; single-repo project shows
  no picker.

## Acceptance Criteria

- [ ] Monorepo root discovers exactly its depth-1 child repos;
  depth-2 ignored; root-is-repo unchanged
- [ ] Scan never runs on the UI thread (timed assertion, M14
  precedent)
- [ ] Active repo persists across restart; stale saved name falls
  back to first-sorted
- [ ] All Git features follow the active repo from panel and CLI
- [ ] Single-repo / non-repo rendering byte-identical to pre-M20
  (no picker, no sections)
- [ ] Quality gates: `cargo fmt --check`, `cargo test --workspace`,
  `clippy -- -D warnings`, `check-docs.py`, `git diff --check`

## Non-Goals

- No simultaneous full bodies for every repository: sections render
  their own summary header and only the active repository expands to
  its full M14 body. Per-repo panel state (status, selection, collapse,
  stash, commit draft) is already per repository, so N-way simultaneous
  expansion is a rendering follow-up rather than a model change.
- No depth >1 auto-scan; no lazy file-open activation (follow-up).
- No Files-panel per-repo roots (stays at project root).
- No cross-repo unified stage/commit/diff.
- No `project.root` breaking change; no libgit2 (blueprint §32).

## Phase E — every Git surface follows the active repository

> Status follows the active repo (Phase D). The remaining Git tab
> surfaces still resolve the **project root**, so they answer for the
> wrong repository in a monorepo. This phase removes the divergence by
> making the active repository the single root for all Git work, with
> one owner for discovery so the panel, the CLI and agents cannot drift.

### Audit — what resolves what today

| Surface | Root source | State key |
|---|---|---|
| Status rows (Phase D) | `StatusRequest.repo_root` from `repo_root_for` | `RepoKey` |
| Graph / history page | `main.rs:5853` → `(pinned, shell_cwd)` → `resolve_root` | `ProjectId` |
| Commit files (expansion) | `main.rs:5966` → `(pinned, shell_cwd)` | `ProjectId` + commit |
| Historical commit diff | `diff_panel.rs:1330` via `DiffRequestKey{pinned_root, active_cwd}` | `DiffRequestKey` |
| Worktree diff preview | `diff_panel.rs:1246` via `DiffRequestKey` | `DiffRequestKey` |
| Branch list | `main.rs:17588` → `(pinned, shell_cwd)` | `ProjectId` |
| Branch create/checkout/delete/rename | `router.rs:1173` `git_branch_mutation` → `file_root` | — |
| Stage / unstage / discard / commit / stage-hunk | `router.rs:1139` `git_mutation` → `file_root` | — |
| Stash list | `main.rs:15106` → `(pinned, shell_cwd)` | `ProjectId` + drafts |
| Stash push/pop/apply/drop | `router.rs:3245+` → `file_root` | — |
| Sync fetch/pull/push | `main.rs:9658` + `git_sync_panel.rs:82` → `(pinned, shell_cwd)` | one `PendingSync` |
| Blame | `router.rs:3222` → `file_root` | `(ProjectId, staged, path, mode)` |
| Wire `git.*` / CLI | same router arms → `file_root` | — |

`Router::file_root` (`router.rs:691`) is the single chokepoint for every
dispatcher-path git operation, and it always returns the **project**
root. That one line is the root cause of the divergence; the desktop
async workers repeat the same mistake by resolving `(pinned, active_cwd)`
themselves.

### Design decisions

1. **The router owns discovery.** The depth-1 scan cache moves from the
   view into `Router` (`install_repo_scan` / `invalidate_repo_scan`), and
   the view's existing off-thread scan worker installs its result there.
   One cache means the panel, `git_repo_*` queries, `omaterm git *` and a
   future agent all answer for the same repository — no duplicated scan
   state that can drift (the forbidden "separate logic in UI vs CLI"
   pattern, blueprint §62).
2. **`file_root` stays the Files root; git gets its own root.**
   `Router::git_root(context, project) -> Option<PathBuf>` returns the
   active repository path, falling back to the project root for
   single-repo and pre-scan states. Every `GitCommand` arm and every
   `DiffCommand` arm switches `file_root` → `git_root`. The Files panel,
   finder and watcher intentionally stay on `file_root` (agreed
   non-goal).
3. **Desktop async workers stop resolving roots themselves.** Each takes
   an explicit `repo_root` the view reads from the router, exactly like
   `git_panel::StatusRequest` already does. No worker calls `resolve_root`
   for git again.
4. **Per-repo state follows the key, not the project.** Panels whose maps
   are keyed by `ProjectId` are re-keyed to `git_panel::RepoKey`:
   history (pages, scope, expanded commits, in-flight, refreshed-at,
   generation guards), branch lists, stash drafts / focused / in-flight /
   `-u` arm, diff request keys and their caches, blame rows and
   visibility, and `PendingSync` (carries its repo root). Switching
   repository then cannot show another repository's stale rows, and
   switching back restores them.

### Steps

- [x] **E1 — router-owned discovery.** Move `repo_scans` into `Router`,
      keep the view's scan worker as the only producer, and expose
      `git_roots(project)`, `active_repo(project)`,
      `install_repo_scan(project, info)`, `invalidate_repo_scan(project)`.
      The view renders and polls through these instead of its own map.
- [x] **E2 — `git_root` chokepoint.** Add `Router::git_root` (active repo
      path, scope-checked) and repoint every git/diff arm: history,
      commit-files, stash*, branch*, sync*, blame, stage/unstage/
      discard/commit/stage-hunk, diff show/show-commit/list-files.
      Single-repo projects keep byte-identical behavior (same path, same
      empty envelopes).
- [x] **E3 — desktop workers take the repo root.** history page +
      commit-files threads, sync worker, stash-list worker, branch-list
      worker and `DiffRequestKey` gain the active repo root; the view
      passes `router.active_repo(project)`.
- [x] **E4 — re-key per-repo state to `RepoKey`** (list above). Includes
      clearing per-repo state on the project-switch sweep that already
      clears statuses, so memory stays bounded to one project.
- [x] **E5 — chrome that reads state.** The Git tab badge, status bar and
      any count that reads per-project git state must read the active
      repo's key (the badge currently reads `git_count` per project).
      The simplified chrome (group header + rows) reads the same
      `RepoKey` cache, so no extra plumbing is needed here.
- [ ] **E6 — wire/CLI parity (LOCKED: default-active only for v1).**
  Because every `git.*` method now flows
  through `git_root`, CLI and agents follow the active repository
  automatically. The optional explicit `repo` parameter on git
  wire methods plus `omaterm git status --repo <name>` is
  deferred to a follow-up (automation ergonomics, not required
  for the UX goal); see Execution Plan locked decisions.
- [x] **E7 — verification.** Router tests: a two-repo fixture where
      `git.stage` / `git.history` / `git.stash-list` / `git.branch-list`
      / `diff.show` all target the repository the panel shows, and a
      second repo is untouched; scope denial unchanged; single-repo and
      non-repo envelopes unchanged. Desktop unit tests for per-repo
      isolation of history/branch/stash/diff state. Full gates pass.
      **Remaining (manual):** native Wayland on the 2-repo fixture:
      switch repo → graph, stash, branch picker, sync buttons, commit
      and diff preview all answer for the new repo, and switching back
      restores each.

### Deferred from Phase D (unchanged)

- Simultaneous expanded bodies for every repository (per-repo state now
  exists, so this is rendering-only).
- Files panel per-repo roots (stays at the project root).
- Depth >1 scan and lazy file-open activation.

### Phase F — Graph + stash follow the active repo, chrome remembers itself

> Status bar, status rows and the body already follow the active repo.
> Two surfaces still answer for the project root: the **Graph**
> (history worker + `HistoryPanel`, keyed by `ProjectId`, resolving
> `(pinned, shell_cwd)` itself) and **stash** (the list worker resolves
> the project root while `git_panel`'s stash state is already per
> `RepoKey`; the drafts, keyboard focus and `-u` arm are still per
> `ProjectId`, so a draft follows you into the wrong repo). And the
> `Repositories` list forgets its collapse state on every restart and
> every root bust.

#### F1 — history/graph goes per-repo (largest item)

- `HistoryPanel.projects` re-keyed `ProjectId` → `RepoKey`, same
  mechanical change Phase D applied to `GitPanel` (pages, scope,
  expanded commits, selected commit, file caches). `set_scope` keeps
  clearing expansions per repo.
- `spawn_history_thread` / `spawn_commit_files_thread` take an explicit
  `repo_root: PathBuf` (resolved from the router's active repo, mirroring
  `git_panel::StatusRequest`) and their completion channels carry
  `RepoKey`. They never call `resolve_root` again.
- View fields `history_in_flight`, `history_files_in_flight`,
  `history_refreshed_at`, `history_generation` move from `ProjectId` to
  `RepoKey` (or `(RepoKey, commit)` for files). The project-switch sweep
  clears them per repo like it already clears statuses.
- Expanded commits and the Graph cursor are per-repo: switching repo and
  switching back restores the exact Graph view. Scope changes keep
  clearing expansions repo-locally.

#### F2 — stash finishes the job

- Stash-list worker takes the active repo root (same `repo_root`
  pattern); stale generations still drop on switch.
- `stash_drafts`, `stash_focused`, `stash_untracked` arm and
  `stash_in_flight` re-keyed `ProjectId` → `RepoKey`, so a draft, the
  keyboard owner and the `-u` arm all belong to the repo they were typed
  in.
- Stash push/pop/apply/drop already flow through `GitCommand` → the
  router's `git_root` once Phase E's E2 lands; until then they inherit
  the project root (recorded, not re-litigated). Phase F builds on E2
  and does not itself touch the router.

#### F3 — the list remembers its collapse state (LOCKED: session-scoped)

- `repo_list_collapsed` stays view-local (`HashSet<ProjectId>`);
  no snapshot field, no schema v5, no migration — the Execution
  Plan records this as the locked F3 decision.
- Rules: first scan of a project defaults to **expanded**; a manual
  collapse survives root busts, project switches and tab hops
  within the session (so `ProjectDirectoryChanged` clears the scan
  but no longer forgets the preference); `Esc` still collapses;
  restart resets to expanded alongside the volatile view state.

#### F4 — verification

- Router tests: two-repo fixture — `git.history` and `git.commit-files`
  target the repository the panel shows, the other repo is untouched;
  single-repo and non-repo envelopes byte-identical.
- Panel unit tests: per-repo isolation of Graph/expansion state and
  stash drafts/focus/`-u`; collapse preference round-trip (persisted or
  session-scoped per the F3 decision).
- Gates (`fmt`, workspace tests, clippy, release build, docs check,
  `git diff --check`), then native Wayland on the 2-repo dirty fixture:
  switch repo → status, graph, stash (list + draft + actions), branch
  picker, sync buttons, commit and diff preview all follow, switching
  back restores each, and the list keeps its last collapse state.

## Execution Plan — locked 2026-10-09

> Records the architect recommendations adopted as locked decisions,
> plus the ordered build sequence for the remaining E1–E7 / F1–F4
> work. Phases A–D + E5 are implemented (staged, uncommitted);
> E1–E4, E6–E7 and F1–F4 remain.

### Locked decisions

- **E1 cache semantics: cache + bounded live fallback.** `Router`
  owns `repo_scans: HashMap<ProjectId, (ProjectReposInfo, Instant)>`.
  `git_root` reads the cache; on cache miss it runs a synchronous
  bounded `scan_repos` (readdir + stat only, no git spawn, <200ms)
  as fallback so pre-scan states answer correctly. The view's
  existing off-thread scan worker is the sole producer and calls
  `install_repo_scan` on land. `SetDirectory` / `Delete` call
  `invalidate_repo_scan`.
- **Root-repo naming: single-repo `active_repo` stays `None`.**
  `resolve_repos` fast-path must normalize root-is-repo to
  `active_repo = None`; `SetActiveRepo` is rejected with
  `invalid_request` when `repos.len() < 2` (nothing to select).
  This keeps `RepoKey::root = None` consistent with the domain.
- **E6 `repo` override: OUT for v1.** Default-active routing via
  `git_root` covers the UX goal; CLI/agents follow the active repo
  automatically. Explicit `repo` param on `git.*` wire methods plus
  `omaterm git … --repo` is deferred as a follow-up (automation
  ergonomics, not correctness).
- **F3 collapse memory: session-scoped.** `repo_list_collapsed`
  stays view-local (`HashSet<ProjectId>`), default expanded on
  first multi-repo scan; manual collapse survives root busts,
  project switches and tab hops within the session; `Esc`
  collapses. No snapshot v5 field, no migration. Documented here
  per the spec's allowed shrinkage.
- **32-cap overflow: accept as v1 known gap.** Rows render the
  first 32 repos + `+N more`; repos 33+ still poll (round-robin)
  but are unreachable by click. A picker search is a follow-up.
- **Chrome docs refresh:** `render_git_panel` / `render_repo_chrome`
  comments must be updated to the locked chrome (group header
  only, no picker/dropdown/counters) as part of E5 cleanup.

### Ordered build sequence

1. **E1 — router-owned discovery** (`apps/omaterm/src/router.rs`,
   new cache type in `omaterm-core`, never an import of desktop
   `git_repos`): add `repo_scans` + `install_repo_scan` /
   `invalidate_repo_scan` / `git_roots` / `active_repo`. View scan
   worker becomes producer. Tests: router install / active-fallback
   / stale-invalidate unit tests.
2. **E2 — `git_root` chokepoint** (adjacent `router.rs:691`):
   `fn git_root(context, project) -> Result<Option<PathBuf>>`
   (scope-check → cached scan → `active_entry` path → containment
   check → fallback `file_root`). Repoint every git/diff arm:
   history, commit-files, stash*, branch*, sync*, blame,
   stage/unstage/discard/commit/stage-hunk, diff
   show/show-commit/list-files. Single-repo/non-repo envelopes
   byte-identical. Tests: two-repo fixture — all arms target the
   active repo, second repo untouched; traversal/foreign-scope
   denial unchanged.
3. **E3 — desktop workers take the repo root** (depends E1–E2):
   `spawn_history_thread` / `spawn_commit_files_thread`
   (`git_history_panel.rs`, `main.rs`), sync worker
   (`git_sync_panel.rs`, `main.rs`), stash-list worker, branch-list
   worker, `DiffRequestKey` / `CommitDiffKey` gain explicit
   `repo_root: PathBuf` + `RepoKey` on channels; view passes
   `router.active_repo(project)`; delete every `resolve_root` call
   in git workers.
4. **E4 — re-key per-repo state to `RepoKey`** (depends E3):
   `HistoryPanel.projects`, `history_in_flight` /
   `files_in_flight` / `refreshed_at` / `generation`,
   `branch_lists` / `in_flight`, `stash_drafts` / `focused` /
   `untracked` / `in_flight`, all `diff_panel` maps + scroll
   handles, `blame_rows` / `visible`, `PendingSync{repo_key,
   repo_root}`. All landing channels carry `RepoKey` + generation
   guards (fixes the `stash_tick` misattribution pattern: landing
   into the *current* key instead of the request key). Sweep
   clears per-repo keys. Tests: per-repo isolation for
   history/branch/stash/diff/blame/sync.
5. **F1/F2 — Graph + stash per-repo** (depend E3–E4, no router
   change): same mechanics scoped to Graph expansions/cursor and
   stash drafts/focus/`-u`, per the F1/F2 field lists above.
6. **E5 cleanup — chrome that reads state** (already done):
   verify badge/status/chrome all read `active_repo_key`; refresh
   the two stale doc comments; optional pure `active_git_count`
   helper + test.
7. **E7/F4 — verification.** Router two-repo fixture tests,
   panel isolation tests, existing `repo_chrome` plan tests.
   Gates per slice: `cargo fmt --all --check`, `cargo test
   --workspace`, `cargo clippy --workspace --all-targets -- -D
   warnings`, `python3 scripts/check-docs.py`, `git diff --check`
   (+ release build for UI slices). Then native Wayland on the
   2-repo dirty fixture: switch → status/graph/stash (list +
   draft + actions)/branch-picker/sync/commit/diff-preview follow,
   switch-back restores each, collapse persists per F3 rule,
   single-repo layout byte-identical, restart restores
   `active_repo` with stale-name → first-sorted fallback.

### Acceptance mapping

- Depth-1 / depth-2-ignore / root-fast-path → Phase A tests.
- Off-UI-thread scan → worker thread-id assertions (M14 precedent).
- Persist + stale fallback → Phase B tests + Wayland restart check.
- All-features-follow → E2/E7 + F4 router tests + Wayland switch matrix.
- Single-repo identical → chrome-hidden + E2 byte-identical tests.
- Gates → command list in step 7 above.

## Closeout Plan — 2026-10-09

> Ordered, dependency-aware plan for the remaining M20 closeout.
> Implementation of E1–E5 + F1–F3 is complete and green
> (`cargo check/test/fmt/clippy` all pass); what remains is evidence,
> one flake investigation, and the manual gate. Nothing is committed.

### Phase G — evidence + hygiene

1. **G1 — commit hygiene.** The working tree is split staged/unstaged
   and `crates/omaterm-core/src/repos.rs` is still untracked. Before any
   commit: re-read `git status`/`git diff`, stage the M20 changes as one
   coherent slice (or a small E/F-series set), keep the unrelated
   `.debug-journal.md` out, and do **not** commit without explicit user
   request. No secrets; `git diff --check` must stay clean.
2. **G2 — `docs/status.md` entry.** Append a dated "M20 Phase E + F"
   section recording: router-owned discovery (`CommandRouter::repo_scans`
   + `install_repo_scan`/`invalidate_repo_scan`/`repo_scan`/`active_repo`),
   the `git_root` chokepoint, worker repo-root threading (history/diff/
   sync/stash/branch via `HistoryRoots`/`RepoKey`), the per-repo re-key,
   the project-switch memory sweep, the F3 session-scoped collapse
   (root bust no longer forgets it), and the exact gate commands with
   their PASS results. Record the known deviations: E6 override deferred,
   32-cap reachability gap, and the unreproduced flaky run.
3. **G3 — `docs/acceptance-matrix.md`.** Add an M20 row under a new
   "v0.3" (or extend the v0.2 table) mapping the M20 acceptance
   criteria to automated evidence, with manual Wayland explicitly marked
   **pending** until G5. Do not upgrade any status without a linked
   observation.

### Phase H — verification hardening

4. **H1 — flake identification.** One full-suite run reported `48
   passed; 2 failed` in a 50-test binary and never reproduced across
   8+ runs. The repo already documents two order-dependent flakes
   (`osc_dynamic_color_queries`, `workspace::tests::
   four_panes_have_independent_sessions`). Reproduce by running that
   50-test binary (and the terminal suite) repeatedly with
   `--test-threads=1` and in parallel; if it is a known flake, cite it
   in `status.md`; if not, bisect by test name and report. **Do not**
   report the suite as a clean gate until the failure is explained or
   shown to be unrelated.
5. **H2 — gate refresh.** Re-run the full closeout gate set on the final
   tree and paste exact commands/results into `status.md`:
   `cargo fmt --all --check`, `cargo test --workspace`,
   `cargo clippy --workspace --all-targets -- -D warnings`,
   `cargo build --release --bin omaterm-desktop --bin omaterm`,
   `python3 scripts/check-docs.py`, `git diff --check`.

### Phase I — manual native gate (owner: user, on Omarchy/Wayland)

6. **I1 — 2-repo dirty fixture.** Create `/tmp` fixture keyed to an
   isolated state dir; root is a container with `api/` and `web/`, each
   its own repo, both dirty, distinct branches.
7. **I2 — switch matrix.** In a release build: open the Git tab →
   `Repositories` header + two plain rows with muted branches and dirty
   dots; switch active repo → status rows, Graph, stash (list + draft +
   actions), branch picker, sync buttons, commit and diff preview all
   follow; switch back → each surface restores.
8. **I3 — persistence.** Restart → `active_repo` restored; delete the
   saved repo dir and restart → falls back to first-sorted. Confirm
   single-repo and non-repo projects render exactly as pre-M20 (no
   chrome) and the `Repositories` list keeps its collapse state across
   a root bust within the session (F3).
9. **I4 — record evidence.** Capture commands, fixture, build mode, and
   observations in `status.md`; flip the E7/F4 Wayland checkbox and the
   M20 acceptance row only after this passes.

### Exit criteria

- All Phase G docs land with linked gate output.
- H1 either explains the flake or proves it unrelated.
- I1–I4 pass and are recorded.
- Then — and only then — M20 can be marked complete. Until I4, the
  milestone stays "implementation complete, manual validation pending".

## References
- Blueprint §5.6/§20 (project scope), §21 (approval), §31 (project
  directory), §32 (git strategy), §30 (persistence)
- [M12 Project Context Root](2026-09-29-12-milestone-12-project-context.md)
- [M14 Git Status](2026-09-29-14-milestone-14-git-status.md)
- [Status](status.md), [Acceptance Matrix](acceptance-matrix.md)
