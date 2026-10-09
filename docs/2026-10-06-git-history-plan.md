# Git History Graph and Commit File Diffs — Implementation Plan

> **Historical plan (landed).** The Graph + commit-file diffs it designs
> shipped in v0.2.0. Live status in [status.md](status.md).

**Status:** landed (v0.2.0); retained as design history.
**Requested UX:** a VS Code-style Graph below the staged/unstaged file lists;
expand a commit to see its changed files, then click a file to see that commit's
diff in the main area. **Build/check runner:** `mbx`.

## 1. Goal and execution boundary

Deliver this complete vertical slice:

```text
Source Control                     Main area
  Branch / commit message
  Staged Changes
  Changes                          Diff: release.yml @ ed2fee4
  GRAPH                            Parent 06778cc → Commit ed2fee4
    ● feat(editor): ... [main]     [Split] [Inline] [Previous/Next Hunk]
    │
    ● feat(packaging): ...         - previous committed content
    │   release.yml          M     + selected committed content
    │   install.sh           A
    ● docs(packaging): ...
    Load more
```

The graph belongs inside the existing contextual Git inspector, below both
change groups. Use OmaTerm's theme, typography, icons and sharp tab strip; the
screenshot is the interaction reference rather than a request to duplicate the
entire VS Code workbench.

This is an additive Developer Context feature using M12 root resolution, M14
Git operations and M15 diff infrastructure. Implement the slices below in order.
M15/M16/M17/M18/M19 acceptance gaps remain tracked in [status](status.md); this
feature's acceptance does not close those milestones or establish a v0.2 release.
M15 source/action identity hardening in H1 is required before historical previews.

Architecture authority: blueprint §32 (system Git), §33 (bounded virtualized
diffs), §43–§45 (outputs/errors/logging), §61–§66 (separation, shared operations,
identity, limits, protocol and cancellation). Existing contracts:
[M14](2026-09-29-14-milestone-14-git-status.md), [M15](2026-09-29-15-milestone-15-diff-viewer.md),
[remaining work](2026-10-03-m15-m16-remaining-work-plan.md), [shortcuts](shortcuts.md).

## 2. Audited starting point

Inspected baseline: `f4f8907` on `main`, clean working tree before this plan.

| Existing component | Reuse / required extension |
|---|---|
| `apps/omaterm/src/git_panel.rs` | GPUI-free per-project status, selections, collapse and commit drafts; keep history state in a distinct `git_history_panel.rs` |
| `main.rs::render_git_panel` | Currently returns early for clean worktrees; compose Changes and Graph independently so clean repositories still show commits |
| `apps/omaterm/src/diff_panel.rs` | Split/Inline virtual rows, hunk navigation/copy, token spans, source anchors and a cancellable latest-only worker; add explicit historical source identity |
| `main.rs::render_diff_preview` | Reads Git selection and `show_staged`; toolbar assumes every diff can stage/unstage; derive path and capabilities from the selected preview source instead |
| `crates/omaterm-context/src/git.rs` | System-Git subprocess runner, capped stdout/stderr, deadlines and cancellation; extend for history queries |
| `crates/omaterm-context/src/diff.rs` | Unified patch parser and 4 MiB byte cap, metadata/binary handling; reuse for commit-to-parent patches |
| `omaterm-core::{GitCommand, DiffCommand, DiffInfo}` | Existing commands cover status/worktree/index, not commits; introduce typed OIDs and additive queries/results |
| `apps/omaterm/src/{router,ipc_bridge}.rs` | Shared dispatch, project authorization and effects; extend async query completion and scope checks |
| `crates/omaterm-protocol`, `crates/omaterm-cli` | Add method/CLI parity, serialization, bounds and human rendering |

Recent diff highlighting already supports the shared 24-language subset. Preserve
that presentation, including its per-line multiline-state limitation, and exact
source bytes for copying. Do not describe highlighting as absent in this plan.
`apps/omaterm/src/history.rs` is terminal history recovery, not Git history.

## 3. User interaction contract

### 3.1 Graph placement and list behavior

- Expanded Graph by default, even when the working tree is clean. Collapsing
  either Changes group must leave Graph visible. Graph collapse is project-local.
- Header: chevron, `GRAPH`, functional scope selector, Refresh. Initial scope is
  **Current branch (HEAD)**; second scope is **All local branches**. Detached HEAD
  labels the first scope `Current HEAD`. No decorative inactive toolbar controls.
- Current scope includes all ancestors reachable from HEAD, including merges.
  All-local scope uses local branch tips plus detached HEAD, deduplicated. Remote
  tracking refs and tags may decorate displayed commits but do not add traversal
  roots or trigger network operations.
- Fetch the first 50 commits. `Load more` appends the next page without changing
  selection, expansion or the top visible row. Disable it during an active page
  request. At the loaded-history cap, show a specific limit notice.
- Commit row: graph gutter, subject, bounded ref badges, author and relative time.
  Ellipsize to the sidebar width; tooltip/detail reveals hash, full bounded
  subject and absolute timestamp. Author/time take lower priority than subject.
- Row click selects and toggles expansion. Expanded child rows show filename,
  muted parent directory, shared file icon, and `A/M/D/R/C/T` status. Renames show
  old → new paths. Type/mode/submodule changes have explicit labels.
- File metadata loads lazily per expanded commit and selected parent. Loading or
  error rows stay inside that expansion; retries do not collapse neighboring rows.
- Several commits may be expanded within the cache budget. Collapsing does not
  close an already open preview. Cache eviction of offscreen metadata does not
  change the selected historical comparison.

### 3.2 Click a committed file → historical diff

1. Capture project, resolved repository/root identity, full commit OID, chosen
   parent OID (or empty-tree base), and old/new file paths from the row.
2. Activate the existing main-area Diff surface and chip, labeled
   `Diff: <filename> @ <short-oid>`. Immediately show the new request's loading
   state; never leave another file's body below the new header.
3. Dispatch a historical query for exactly that comparison; render its patch with
   the shared Split/Inline body, syntax spans, line numbers and source anchors.
4. Header: filename, `parent → commit`, subject/author/date and a collapsible
   bounded full-message detail. Split labels refer to the two committed versions,
   never to `INDEX` or `WORKING TREE`.
5. Offer Split/Inline, hunk navigation, Copy Hunk and Copy Commit Hash. Copy Hunk
   is enabled only for complete source hunks, never a truncated reconstruction.
   Hide all Stage, Unstage, Discard and Stage Hunk controls; keyboard handlers and
   palette activation must enforce the same capability gate.
6. An optional **Open Working File** action opens today's rooted editor file only
   if it exists under the project root. It is explicitly labeled; no historical
   line number is passed as a current-file line number. Removed/outside paths are
   disabled. Viewing a historical diff itself must not require a live file.

Use the existing one-preview-per-project model: another Git file click replaces
that project's preview, whether it came from Changes or Graph. Clicking terminal
or editor tabs preserves the preview; clicking the chip restores it. Closing its
`×` uses the current surface fallback. Do not add terminal/domain tabs for commits
or persist patch bodies/history selections.

### 3.3 Keyboard and focus

- Graph-focused Up/Down traverses visible commit/file rows; Left collapses or
  returns to the parent; Right expands; Enter toggles a commit or opens a file.
  Space toggles a commit. Focus and selection are distinct, visibly styled.
- Introduce a Git-history input owner; graph keys must not reach the PTY or the
  commit message field. A terminal click restores terminal input ownership.
- Escape in Graph returns focus to the active main surface; Escape in the diff
  follows existing preview behavior. The chip close button closes the preview.
- Keep `Alt+1..9` strip jumps and `Alt+Shift+1..9` project jumps working. Add any
  new global shortcut to the registry and [shortcut table](shortcuts.md) together;
  do not claim plain Ctrl navigation chords used by terminal applications.

### 3.4 Exceptional states

| Situation | Visible result |
|---|---|
| First query | Loading history; changes and terminals stay usable |
| Refresh | Keep matching last-good rows and scroll position with a small refreshing indicator |
| No project/root or non-repo | Existing explicit Git empty state, never endless loading |
| Unborn HEAD, no selected roots | `No commits yet` |
| Detached HEAD | History of that HEAD with truthful scope label |
| Shallow history | Boundary marker; missing ancestor is not presented as a root commit |
| Missing comparison parent | `Parent unavailable in this clone`; do not silently compare against an empty tree |
| Missing/pruned commit | Explicit unavailable state + Refresh/Retry; never retarget to current HEAD |
| Empty commit | `No files changed against selected parent` |
| Binary/LFS pointer/submodule | Binary notice, actual pointer text, or bounded gitlink metadata as applicable; no blob download |
| Mode-only/type change | Metadata summary, even with no textual hunks |
| Budget reached | Specific byte/file/ref/history/graph limit notice; pagination and truncation distinguished |
| Missing Git/deadline/failure | Bounded named error and Retry; unrelated source-control states stay intact |

## 4. Comparison semantics and repository boundary

**Ordinary commit:** compare its parent tree to its own tree.
**Root commit:** compare the repository's empty-tree OID to its tree. Obtain the
empty-tree OID with the installed Git/object format rather than a hardcoded SHA-1
constant. **Merge commit:** default to first parent; expose a parent selector
`Parent 1 … Parent N`. Both changed-file metadata and patch use the same parent.
Use ordinary two-tree unified diffs, not combined `@@@` diffs.

Resolve the selected project root and Git worktree/repository identity off-thread.
Linked worktrees must use their own HEAD/index context. Historical tree paths are
not current filesystem paths: validate raw path components lexically and against
the project-relative scope without canonicalizing a now-deleted file or following
a present-day symlink. Keep the existing stricter filesystem checks for **Open
Working File**. A committed symlink is compared as link text, never dereferenced.

For a project rooted inside a repository subdirectory, history traverses the
repository graph, but changed-file lists and patches expose only project-contained
paths. Explain a commit with no project-contained changes. Rename crossing the
boundary appears as an in-scope addition/deletion without disclosing outside
paths/content. Do not silently expand a scoped project to the repository root.
Bare repositories may return a named unsupported-root state under the current
worktree-based project model; no claim of bare-repo acceptance without tests.

## 5. Domain and semantic commands

Suggested GPUI-free types; signatures are design targets, not existing APIs:

```rust
GitObjectId                 // validated full SHA-1 or SHA-256 object ID
GitHistoryScope             // CurrentHead | AllLocalBranches
GitHistoryCursor            // bounded snapshot identity + consumed count
GitCommitSummary            // oid, parents, author, times, subject, refs, boundary
GitHistoryPage              // snapshot, commits, next_cursor, limit/truncation flags
GitCommitDetails            // summary + bounded body, message_truncated
GitCommitFile               // raw old/new paths, kind, modes, optional similarity
GitCommitFiles              // commit, base, files, truncated
GitCommitDiff               // commit, base, path pair, shared patch content, flags
```

Store author and committer timestamps with timezone information; use committer
time consistently for the list's relative date and label author time in details.
Ref decorations are typed (HEAD/local branch/remote-tracking/tag), not comma-split
display strings. File kinds cover mode/type/gitlink changes beyond the current
`DiffFileStatus` set. Decide whether to extend that enum or wrap metadata in H1.

Add shared queries:

- `GitCommand::History { project, scope, limit, cursor }`
- `GitCommand::CommitDetails { project, commit }`
- `GitCommand::CommitFiles { project, commit, parent }`
- `DiffCommand::ShowCommit { project, commit, parent, path, context_lines }`

The `parent` selector is validated against the selected commit's actual parents;
a nonparent is rejected. Omitted parent means first parent, or empty tree only for
a genuine root. Input uses full OIDs; UI abbreviations are display-only. Do not
accept arbitrary rev expressions, abbreviated ambiguous hashes or option-like
strings. Validate OID size against SHA-1/SHA-256 repositories and verify commit
object type. Existing stable errors cover scope, validation, timeout, cancellation
and Git failures; add centrally defined `commit_not_found`, `parent_unavailable`
and `history_changed` where necessary. Error messages are not parsing contracts.

## 6. Git I/O and parsing

Use the installed Git and existing capped runner. Argument vectors only; prompts
and pagers off, optional read locks off, external diff/textconv/color off. Prefer
raw `OsString` path arguments and `--literal-pathspecs` for metacharacter paths.
Share parsing/operations between UI and CLI; add cancellable batch-input support
to the runner if needed rather than opening a second unbounded subprocess path.

Command shapes to verify against the selected Git version in H0:

1. Resolve object format, HEAD, scope tips and root identity; read refs using
   separately framed `for-each-ref` fields. Resolve annotated tag decorations to
   their commit targets. Never parse `%D` by splitting commas.
2. Enumerate a fixed set of full tip OIDs in topological order using `git log` or
   `rev-list`, requesting OIDs/parents/structural numeric fields only. Do not use
   path-limited log traversal for graph edges; history simplification would hide
   actual parents. Probe one extra record to determine whether another page exists.
3. Read metadata through length-framed `git cat-file --batch` responses (OID,
   object type, byte length, exact body). Parse raw commit headers and the message
   separator. Keep the summary bounded; fetch full bounded body only for details.
   Honor declared encodings when supported and mark decoding fallback otherwise.
   Separator characters in subjects/messages cannot become extra commit records.
4. List one comparison with `git diff-tree -r --no-commit-id --name-status -z`
   between the chosen parent and commit. Add deterministic rename/copy settings;
   cap detection work (`-l` or equivalent) and mark exhausted detection rather
   than claiming every rename was found. Copy detection need not search unchanged
   files. Preserve two paths for `R`/`C` records; obtain mode/type/gitlink metadata
   from a matched bounded raw diff-tree query or equivalent structured records.
5. Fetch a textual patch using `git diff <base> <commit>` with explicit prefixes,
   bounded context and both old/new literal paths for renames. Use identical
   comparison/detection options to the file query and the existing patch parser.
   Root comparisons use the resolved empty tree; merges use the chosen parent.
   Do not rely on default `git show <merge>` behavior.

Paths retain raw bytes through core/context. For IPC reuse the project's existing
byte-preserving path representation (editor/root code) or introduce an explicit
round-trip-safe equivalent. Lossy text is only a label, never the operation key.
Escape control/newline characters in displayed paths/metadata. Reject malformed
or capped partial records; show truncation, never a plausible fabricated file.
Audit the shared patch parser's Git-quoted paths before reuse; use the raw file
metadata to bind patch content to the requested path pair, and extend decoding
where needed rather than substituting a lossy parsed header as the operation key.

## 7. Stable pagination and refresh

- Snapshot identity includes repository/root generation, scope and resolved tip
  OIDs; cursor references that service-retained snapshot and includes consumed
  count and a query/options version. Use an opaque bounded project-bound token,
  not all captured tips in the request. Evicted snapshots return `history_changed`.
  Never treat a full hash alone as a merge-graph pagination cursor or walk
  `before^` as though every history were linear.
- Append queries use the original tips and ordering, plus bounded skip/count.
  Deduplicate by full OID; keep graph lane continuation state across pages.
- Tip changes or unavailable old objects invalidate the snapshot. Return
  `history_changed` and show `History changed — Refresh`, rather than mixing new
  and old traversal pages. Bound skip work by the loaded-history cap and deadline.
- Refresh captures fresh tips and resets paging; preserve selected/expanded OIDs
  and a visible-row anchor when they survive. Removing an OID from the list does
  not retarget an existing immutable preview; it can remain pinned to that OID.
- Successful commit effects invalidate history/tips and decorations. Stage,
  unstage and discard invalidate their live comparisons but do not rerun the full
  immutable history. Check lightweight tips/refs on the existing visible Git
  refresh cadence to detect external commits/checkouts/resets; no terminal scraping.
- Do not keep subprocess polling active while Graph is hidden/collapsed. Refresh
  on reveal if the cached snapshot was invalidated or its refs check is stale.

## 8. Graph layout and virtualization

Deliver actual parent-edge lanes, including forks/merges, in the full feature;
a linear spine may be an H4 intermediate, not the final graph acceptance result.

- A pure `git_graph` layout module maps ordered commits + incoming lanes to row
  nodes/edges and outgoing lane state. Deterministic lane allocation, stable
  colors, explicit HEAD/ref dots and unresolved-page continuation markers.
- Missing shallow parents terminate in a boundary marker. More lanes than the
  display budget produce a truthful overflow marker/tooltip, not hidden ancestry.
- Flatten commit rows and expanded file/state rows into a cached virtual-list
  snapshot. Carry graph edges through the inserted child rows; file expansion
  must not disconnect the next commit or alter commit ancestry.
- Use native GPUI list scrolling, shared fixed-height row metrics and existing
  theme tokens. A bounded upper Changes region scrolls independently when long;
  Graph receives remaining inspector height with an accessible minimum. At short
  heights, both sections stay reachable via their scroll containers/collapse.
- Cache geometry by snapshot/expansion/width/theme identity. Draw only visible
  rows with overscan. No full list reconstruction or Git work in `render`.

## 9. Async ownership, preview identity and budgets

Use one reusable router-owned history query service; owner dispatch authorizes
and captures inputs, background work resolves roots/reads Git, completion
revalidates project/scope/identity and publishes the result. Return the existing
pending-command form where appropriate; UI, IPC and CLI share this path.

Admission must count active, queued and unconsumed completions. Coalesce identical
reads; supersede obsolete requests by operation owner (history page, expanded
commit details/files, selected preview), not indiscriminately across all owners.
A new preview must not drop a page request or another expanded commit's result.
Use bounded priority scheduling for the selected preview and retain actionable
busy/retry states on rejected admission. Reuse diff renderer/worker primitives
where suitable without maintaining two historical Git implementations.

Preview identity needs an enum, not another interpretation of `staged: bool`:

```rust
DiffSource::WorkingTree { path, untracked }
DiffSource::Index { path }
DiffSource::Commit { commit, base, old_path, path }
```

Request/result/cache/selection/scroll keys include project, root/repository
identity, generations, source and context. Presentation capabilities derive from
that source. `can_stage = !staged` is insufficient. Keep existing worktree wire
responses compatible; return historical provenance in a separate envelope rather
than tagging a historical `DiffInfo` as an ordinary unstaged result.

Cancel queued/active work on supersession, relevant collapse, root/scope switch,
project removal, request deadline or IPC disconnect. Cancellation and publication
must share synchronization so cancelled jobs cannot refill result mailboxes.
Shutdown cancels subprocesses, kills/reaps on deadline and joins the worker through
the existing background cleanup path. The owner never waits synchronously on Git.

Initial budgets (named constants; measure/tune with evidence in H6):

| Resource | Budget / behavior |
|---|---|
| Page | 50 default, 100 maximum; one extra record for `has_more` |
| Loaded history | 1,000 commits per viewed project; explicit stop notice |
| Cached projects | 4 LRU entries; selected preview held separately; clear on project close |
| Metadata output | 1 MiB per request, raw object declared size checked before allocation |
| Subject/author/message | 1 KiB / 256 bytes / 16 KiB; UTF-8-safe labels and truncation flags |
| Refs | 256 tips/refs captured, 4 visible badges per commit; explicit scope/ref limit notice |
| Cursor | 4 KiB maximum; resolves to a retained project-bound snapshot |
| Commit files | 500 entries, 1 MiB output, 16 cached comparisons per project |
| Cached file metadata | 8 MiB total; evict offscreen least-recently-used entries |
| Graph lanes | 16 displayed; overflow marker retains bounded truthful continuation |
| Query service | 1 active worker, 4 pending slots, 4 result slots; at most 5 admitted jobs total including unconsumed results |
| Deadlines/stderr | 10 seconds per read, 30 seconds overall multi-step request, existing 4 KiB stderr cap |
| Patch | Existing 4 MiB, 1,000 files, 256 hunks/file, 1,000 lines/hunk, 8 KiB/line; context 0–10 |

Account for parsed structures, graph edges, token spans and presentation copies,
not just raw stdout. Enforce a total retained-history byte budget (initially
16 MiB) in addition to item caps. The existing protocol caps requests at 64 KiB
and responses at 1 MiB. Limit wire results after serialization/escaping; output
within a raw Git cap can still be too large for IPC. Truncate at complete commit,
file or hunk boundaries with truthful flags, not at an arbitrary JSON byte. Verify
that contract in H1/H3 before promising CLI/desktop parity.
`has_more`, `truncated` and `limit_reached` have different meanings.

## 10. Protocol, CLI and documentation

Planned additive method mappings:

| CLI | IPC method | Result |
|---|---|---|
| `omaterm git log --project <id> --limit 50 [--cursor <cursor>] [--all-local]` | `git.history` | Bounded page with cursor and snapshot |
| `omaterm git commit-details <full-oid> --project <id>` | `git.commit-details` | Bounded message and metadata |
| `omaterm git commit-files <full-oid> --project <id> [--parent <full-oid>]` | `git.commit-files` | Parent-specific changed files |
| `omaterm diff show-commit <full-oid> --project <id> --path <path> [--parent <full-oid>]` | `diff.show-commit` | Provenanced historical patch |

Match existing optional-project resolution and `--json` conventions when defining
the final parser. Local/scoped commands use the same authorization. CLI cursor
output is usable for subsequent JSON pages; human output names the compared
parent, limits and errors. Do not imply `git show` shell syntax is passed through.

Update core validation/results/exports, protocol DTOs/round trips, router/bridge,
CLI mapping/rendering, and [IPC](2026-09-26-08-milestone-8-ipc.md)/[CLI](2026-09-26-09-milestone-9-cli.md)
tables together in H3. Methods listed here are planned, not currently supported.
Update acceptance/status/dependency records with actual results; no dependency
addition expected. Verify installed Git/version/object-format support during H0.

## 11. Dependency-ordered implementation slices

| Slice | Deliverables | Exit evidence |
|---|---|---|
| H0 — Baseline and fixtures | Record toolchain/Git/mbx/source versions; inspect worktree; create linear/root/merge/shallow/worktree/rename fixtures; probe command framing/options | Reproducible repositories, command results and compatibility notes |
| H1 — Identity and contracts | Write domain validation tests first; typed OIDs, provenance/parent/path types, bounds, central errors, preview-source/capability model | Invalid IDs/parents/paths/scopes rejected; no historical mutation path; current diff regression tests pass |
| H2 — System Git reads | Enumeration, stable cursor, framed metadata, refs, parent-specific files/patch, root/subdir rules and runner cancellation | Real-repo comparison parity, byte/path/metadata/cap tests, no leaked child processes |
| H3 — Shared async commands | Router service, scope/identity guards, protocol/bridge/CLI mappings and docs | Round trips, human/JSON parity, stale/deadline/disconnect/admission/shutdown tests |
| H4 — Graph and expansions | GPUI-free panel and lane layout, virtual Graph below Changes, scopes, expand/collapse, lazy files, pagination and focus | Lane/row/state tests; clean repo shows history; merge edges and page continuations render |
| H5 — Historical preview | File click through semantic query, shared Split/Inline body, provenance header/message, parent switch, read-only actions, strip/fallback integration | Correct committed content independent of worktree/index; rapid selection and mutation-gate tests |
| H6 — Native acceptance and polish | Wayland interactions, narrow/short sidebar, large repo/resource probes, screenshots, final gates and docs | Acceptance table evidence; native gaps explicitly pending until executed |

Suggested modules: `crates/omaterm-context/src/git_history.rs` for read operations,
`apps/omaterm/src/git_history_panel.rs` for view-local state,
`apps/omaterm/src/git_graph.rs` for pure lane geometry. Keep core free of GPUI and
add no new crate until a real module boundary needs one. Only extract a shared
diff-body renderer where needed; keep business logic out of rendering callbacks.

## 12. Verification matrix

| ID | Required scenario | Evidence |
|---|---|---|
| GH01 | Graph below both groups in clean/dirty projects; independent collapse and scrolling | Wayland screenshot + interaction |
| GH02 | Linear, branched, two-parent/octopus merges, identical timestamps, page-crossing edges and lane overflow | Pure graph fixtures + native graph capture |
| GH03 | Expand/collapse several commits, lazy file load, cache eviction, retry, empty commit | State tests + native interaction |
| GH04 | Click file shows exact selected parent → commit, not today's staged/worktree content | Real repository oracle + Split/Inline native capture |
| GH05 | Root all-additions, deleted/renamed/copied/type/mode files, binary/gitlink, no-final-newline and tabs | Backend/parser/render tests + targeted native cases |
| GH06 | Merge parent switch changes files/body together; shallow missing parent is explicit | Integration + native parent selector |
| GH07 | SHA-1/SHA-256, linked worktrees, subdir project scope, historic deleted/symlink/raw-byte paths | Real repos + scoped protocol tests |
| GH08 | Cursor append stable, changed tips/stale cursor explicit, refresh preserves surviving anchors | Integration/state tests + external checkout/commit native refresh |
| GH09 | No Stage/Unstage/Discard from toolbar, hunk keys, palette or history-row actions | Capability/activation tests + native keys; no Git mutation observed |
| GH10 | Rapid file/project/root switches, stale completion, page + expansion concurrency, 100 supersessions | Deterministic worker tests + resource probes |
| GH11 | Newline/control/separator metadata, encoding fallback, malformed/truncated batch records, huge object/message/line/ref sets | Framing/budget fixtures and bounded-output tests |
| GH12 | `git.history`/details/files/historical diff human/JSON parity, scope denial and frame budgets | Protocol round trips + CLI/integration tests |
| GH13 | Graph keys focus-owned; terminal input unaffected; chip survives surface changes; close/tabless fallback | Wayland keyboard/mouse validation |
| GH14 | Refresh/missing Git/timeouts are named states, no full-body/header mismatch or refresh flashing | Failure tests + native refresh capture |
| GH15 | Normal shutdown with active Git work, no retained test subprocesses/threads/FDs | Worker tests + native close/process evidence |

Performance capture: use a generated 10,000-commit repository (load at most the
1,000-commit cap), a 500-file commit, large capped patches and at least 20 native
open/switch/expand/collapse cycles. Record visible-row counts, main-thread render
times, click-to-loading, query duration, RSS/FD/thread counts and cleanup. Targets:
loading state by next frame, bounded visible-only elements, no UI-thread Git and
no accumulating worker/FD count. Report measured percentiles/limits separately;
do not substitute unexecuted timings with pass claims.

## 13. mbx gates and completion record

During implementation, run targeted meaningful tests per slice, then the required
workspace gates once the vertical slice is ready:

```bash
mbx fmt --all --check
mbx test --workspace
mbx clippy --workspace --all-targets -- -D warnings
mbx build --release --bin omaterm --bin omaterm-desktop
python3 scripts/check-docs.py
git diff --check
```

Use serial workspace testing when diagnosing concurrency failures; a serial pass
does not replace the workspace gate. Record toolchain overrides and actual mbx
artifact locations before native validation. `mise.toml` already wires Cargo to
mbx and CI uses mbx explicitly. No cache-setting or toolchain change is required
by this plan.

This documentation-only increment requires the documentation checker, link/
consistency review and diff whitespace check; Rust/native gates above are future
implementation requirements. Completion requires every GH acceptance row to have
evidence, plus updated [status](status.md) and [acceptance matrix](acceptance-matrix.md).
Keep outstanding failures and manual validation separate from automated passes.

**First implementation action:** H0 reproducible baseline/fixtures, then H1 typed
historical source/capability tests before any UI feature expansion.
