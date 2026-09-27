# Closure plan — Milestones 5–8 and carried-forward gates

This is an execution checklist, not verification evidence. **The remaining work
is M7 followed by M8**; M5 and M6 are complete. Follow the milestone
contracts in [M5](05-milestone-5-projects-tabs.md),
[M6](06-milestone-6-persistence.md),
[M7](07-milestone-7-command-router.md), and [M8](08-milestone-8-ipc.md),
the [blueprint](../OMATERM_AGENT_BLUEPRINT.md) §§18–20 and 64–68, and the
[acceptance matrix](acceptance-matrix.md). Record actual commands, results,
environment, failures, and separate Wayland observations in
[status.md](status.md); only that evidence can close a milestone. The
working-tree `omaterm-protocol`/`omaterm-ipc` crates plus the desktop owner
bridge, credentials, and mapping are implemented and Wayland-proven (see
status.md 2026-09-27 integration record); the remaining items below are fault
tests, resource evidence, and final acceptance.

## Inventory and dependency order

| Gate | Current evidence / gap | Must precede |
|---|---|---|
| M1–M3 | Complete, with recorded X11-runtime, alternate-scale, IME, unavailable-shell/TUI and live OSC 7 limits | No reimplementation gate; carry available observations into M6/M7 validation |
| M4 | Complete four-pane/PTY behavior; measured FD cycles pass. Thread, process, RSS and GPU lifecycle baselines remain qualitative; default-parallel PTY test runs previously stalled | Resource release evidence in M8; investigate test concurrency if it recurs |
| M5 | Core/hidden-session tests and main Wayland workflows pass. Release Wayland verifies inactive-tab chip padding and startup Retry; two 100-cycle UI session runs recorded thread/FD/RSS; repeated-close and poisoned-cleanup tests pass. Per-process GPU counters are unavailable; concurrent IPC resource tests remain M8 work | M5 complete; proceed to M6 Wayland validation |
| M6 | Release Wayland verifies multi-project/tabs, fresh PIDs, nested split/focus, per-session procfs CWD, home fallback, restored-pane Retry, corrupt/unsupported warning and primary retention, and close-before-debounce recovery save. Procfs refresh omission was fixed and has a PTY regression test | M6 complete with unavailable-home environment limit recorded; proceed to M7 |
| M7 | Complete 2026-09-27: async dispatch, 19 router tests, commit guards/rollback, single-path enforcement, Wayland UI regression (see status.md closeout record) | Done; M9 may proceed |
| M8 | Complete 2026-09-27 with documented limits: 17-method DTOs, bounds, credentials/scope/child-env, owner bridge, 11 transport tests, concurrent load + shutdown-under-load proof (see status.md closeout record) | Done; M9 may proceed |

Do **not** treat previously approved M1–M3 limitations as new M8 blockers:
X11 runtime when unavailable, unsupported IME composition, absent zsh/fish/htop,
alternate-monitor scaling, and shell-dependent live OSC 7 emission remain
documented limits. `terminal.run` does require a real ready supported shell,
and M6 CWD validation must use a reproducible live signal or record the actual
provenance/fallback; a synthetic parser test alone does not prove desktop restore.
M9 and post-v0.1 M10 are outside this closure plan.

### Remaining execution order and handoff contracts

| Step | Deliverable | Depends on | Evidence needed to move on |
|---|---|---|---|
| 7A | Non-blocking launch and owner-side commit/cancel for every creation path | M6 logical restore, existing session-ID/registry insertion foundation | Deterministic queue-full, spawn-failure, stale-close and shutdown tests; UI stays responsive |
| 7B | Complete shared router/effect coverage and real desktop regression | 7A | Router validation/results/effect tests, release Wayland interactions, M7 criteria in [M7](07-milestone-7-command-router.md) and [status](status.md) |
| 8A | Typed v1 methods and bounded wire/transport | 7B contracts; may be developed independently, **not integrated** until M7 closes | All 17 method DTOs, exact-limit and transport fault tests |
| 8B | Credentials, target authorization and child-only environment | 7A IDs and 8A DTOs | Two-project isolation, token lifecycle, local-user file and permission tests |
| 8C | Desktop listener/owner bridge, lifecycle and real client proof | 7B, 8A, 8B | Concurrent E2E, Wayland visible split/read, clean shutdown and resource evidence |

Use one completion condition for asynchronous mutations: a UI command can
observe an accepted/pending operation, but IPC must wait for the **final**
success/error (including the created IDs) within its deadline. A pending receipt
is not proof that a shell spawned or a pane changed. Specify this in the core
result and wire mapping before migrating creation tests; on timeout or disconnect
after possible commit, report an ambiguous outcome and never replay automatically.

## Phase 1 — Close M5 desktop and lifecycle gates

1. In an isolated Wayland test state, verify clicking **anywhere** on the final
   inactive tab chip selects it without accidentally closing/creating a tab;
   exercise keyboard project/tab selection, focus and pane resize after switching.
   If the target fails, correct the hit region in `apps/omaterm/src/main.rs`
   without changing the shared selection operations.
2. Reproduce first-start shell failure with a controlled one-shot spawn failure
   and isolated state directory; confirm visible failure, Retry after the
   injected failure is removed, and no phantom session. A permanently invalid
   `SHELL` can test the failure display, but changing that environment variable
   outside a running process cannot prove an in-process Retry. Include
   existing-restored-pane Retry separately in M6. Verify close of last
   tab/project and focus fallback.
3. Test repeated tab close, partial project cleanup failure and sibling session
   cleanup. Coordinator tests reject stale second close and inject a poisoned
   session mutex while confirming all project sessions detach, the healthy
   sibling is explicitly shut down and the poisoned child is reaped through PTY
   drop. Keep hidden-session ownership coverage from the existing PTY test.

**Exit:** M5 acceptance items work on Omarchy/Wayland, lifecycle tests pass, and
`status.md`/the acceptance matrix cite dated observations. This phase is now
complete; M5 is marked complete with the per-process GPU telemetry limit
explicitly documented.

## Phase 2 — Close M6 restore, persistence and shutdown gates

1. Use a dedicated `XDG_STATE_HOME` for desktop tests; create two projects,
   multiple tabs, nested panes and distinct CWDs. Save/close/restart; compare
   project/tab/pane/split/focus IDs and selection, confirm **fresh** shell PIDs
   and correct restored directory per pane. Test a live `cd`/OSC 7 path when
   available; record `/proc`/launch provenance and fallback explicitly.
   The initial M6 run exposed that procfs CWD refresh was not called from the
   desktop snapshot path. `WorkspaceView::current_cwds()` now refreshes each
   session before capture; `procfs_refresh_tracks_a_shell_directory_change`
   prevents recurrence.
2. Remove a saved directory and verify home fallback with visible/reportable
   provenance. Inject a failed shell during restore: layout and other sessions
   survive, failed pane stays retryable, and Retry binds only a new session to
   its original pane ID. Exercise home-unavailable behavior if reproducible.
3. For missing, corrupt, unsupported-version and invalid snapshots, verify the
   warning/recovery path. After autosave, final shutdown and **another** restart,
   compare bytes of the original primary/recovery files: no overwrite of damaged
   inputs. Confirm chosen recovery destination, human-readable schema-v1 JSON,
   ordered debounced autosave and latest revision. Preserve originals during
   fault injection. Wayland testing now confirms corrupt and unsupported schema
   warnings, corrupt-primary hash retention after a second restart, and a
   two-tab recovery snapshot when closing before the autosave debounce.
4. Close while work is pending and while terminals run (including a hidden
   tab); observe final save result and bounded reap, no orphan processes, and
   clean UI close. A close-before-debounce Wayland pass flushed the latest
   two-tab recovery snapshot and left no test shell/app; active workload/pending
   save stress remains. Fix only reproducible discrepancies in `omaterm-state`,
   coordinator or desktop shutdown.

**Exit:** M6 acceptance scenarios have automated coverage where sensible and
dated Wayland observations; M5 is complete. This phase is complete with the
unavailable-home environment limit recorded. A serial workspace test pass did
not substitute for the desktop restart checks.

## Phase 3 — Finish M7 before enabling M8 dispatch (steps 7A–7B)

### 7A — Launch off-thread; commit only on the application owner

1. Keep `omaterm-core` GPUI-free and the existing `CommandRouter` in application
   coordination. Define a typed pending operation/completion result and effects
   contract shared by UI and future IPC. At dispatch, validate the command,
   resolve the launch directory, capture target IDs/content and selection guard,
   preallocate project/tab/pane/session identities where applicable, and enqueue
   a *bounded*, non-blocking spawn request. On full/closed queue, return a stable
   error with **no** tree/registry/persistence mutation. Use the existing
   `TerminalSession::new_with_id` and `TerminalRegistry::insert` foundation.
2. Use one bounded worker (or an equivalently bounded executor) to create PTYs;
   it must not touch GPUI, the coordinator, registry or persistence. Return a
   provisional session or a spawn error through a bounded completion channel.
   Have GPUI hand completion back to the **same owner** that calls `dispatch`;
   serialize completion, effects and UI commands there. Reject duplicate/late
   completions using operation IDs. Avoid blocking on channel send, PTY shutdown
   or worker join on the UI thread.
3. Add prepare/commit operations to `WorkspaceCoordinator` for `project.create`,
   `tab.create`, `terminal.create`, `pane.split` and restored-pane start/Retry.
   Before commit, revalidate the owning project/tab, target pane and its original
   content/session, intended selection/focus where required, cancellation and
   app shutdown. Restore may complete while another tab is selected, but only
   into its original still-empty pane. A failed or stale split must preserve the
   original tree and focus; no uncommitted session is published in the registry.
   Decide and test how a newly created but uncommitted project/tab behaves if
   selection changes while the worker runs (cancel it rather than stealing focus).
4. On successful commit, attach the session and publish exactly ordered
   `SessionStarted → WorkspaceChanged → PersistenceDirty` effects for logical
   creation; start the reader/poller **after** registry insertion. If restore
   only rebinds an existing logical pane, preserve M6 snapshot/CWD semantics and
   mark persistence dirty only for a genuine changed logical state/CWD. On
   failure/staleness, remove the pending operation and terminate/reap the
   provisional PTY off-thread; retain an empty restored pane with Retry where
   appropriate. Prevent duplicate Retry while launch is pending.
5. On shutdown, reject new operations, cancel queued work, drain or discard
   completions and reap any provisional children before final cleanup. Ensure a
   blocked completion send cannot deadlock shutdown. Follow the M6 final-save
   contract for committed state; no pending logical placeholders may become a
   persisted phantom terminal.

**7A tests:** injected slow/failing spawn; full/closed queue; repeated request or
completion; close target project/tab/pane during spawn; change selection/focus;
restore different tab while pending; spawn succeeds after target vanished; exit
during pending create; stable IDs, PIDs and reaped orphan count; existing terminal
input/render remains responsive during a deliberately slow spawn. Test owner
effects and persistence ordering without requiring GPUI in the router suite.

### 7B — Complete semantic parity and desktop verification

1. Audit every UI mutation/terminal input/paste/selection/focus/resize/close,
   startup restore and shell-exit cleanup for the same dispatch/effect path.
   Keep actual tree algorithms in core/coordinator, not handler-specific copies.
   Query results must be owned snapshots; successful logical changes trigger
   persistence once, while query/terminal bytes and rejected commands do not.
2. Extend the existing ten router tests into a command coverage matrix: every
   variant's successful result shape (including IDs/parent metadata), bad input,
   stale target, injected runtime failure, order and absence of effects. Include
   async split/restore rollback, requested directory, hidden sessions and
   bounded `terminal.read`. Keep stable `ErrorCode` mappings and test no side
   effects on failure. `terminal.clear` remains explicitly unsupported unless
   an accepted UI behavior requires it.
3. Keep the implemented Bash-only `terminal.run` contract: integrated private
   rcfile, authoritative prompt marker, argv-safe quoting, busy/unsupported
   errors and submit-only acknowledgement. Check startup with a real user Bash
   configuration, busy/partial-input transition, quote/Unicode/metacharacter
   cases, and private rcfile cleanup. Do not infer completion from screen text;
   document unavailable shell dialects as unsupported. Preserve literal
   `terminal.send` bytes (no implicit Enter).
4. Build release and interact in a real Omarchy/Wayland session using isolated
   XDG state: initial create/Retry, project/tab/split/close/focus/equalize/resize,
   keyboard input and paste, hidden output, restoration/CWD and close under
   pending spawn. Compare IDs/PIDs before/after selection and restart. Record
   visual/interaction observations separately from the existing eight-second
   startup smoke, which proves only that the binary stayed running.

**M7 exit:** all [M7 criteria](07-milestone-7-command-router.md#acceptance-criteria)
are backed by tests and interactive Wayland evidence in `status.md`; required
workspace quality gates pass. M5/M6 are already complete. M8 scope enforcement
will extend `CommandContext::LocalUser`; never treat a client-supplied ID as
proof of authority.

## Phase 4 — Finish the M8 wire/transport contract (step 8A)

1. Evolve `crates/omaterm-protocol/src/lib.rs` into strict typed v1 parameter
   and result DTOs for **all 17** methods in the [M8 method table](08-milestone-8-ipc.md#method-mapping).
   Define each method's required/optional fields, defaults, selector rules and
   exact result JSON with round-trip fixtures; deny unknown params. Map wire
   methods and domain results at the desktop owner ingress, not in
   `omaterm-ipc`, preserving `IPC → protocol` and pure core. Convert UUID
   strings into typed IDs; resolve implicit project/tab/pane selectors only
   after authorization (explicit invalid/stale IDs never fall back). For
   `terminal.send` decode base64 to at most 8 KiB and resolve pane → a live
   session; `terminal.run` carries argv arrays and reports submission, not
   completion. Return stable code/message/optional details, UUID result fields,
   parent metadata, viewport dimensions and truncation. Unknown methods/params
   and malformed selectors return `invalid_request`; unsupported versions return
   `unsupported_version`. Add these codes centrally where appropriate.
2. Enforce all documented limits **before** expensive work: 64 KiB request
   including newline, 8 KiB decoded send, ≤1000 read lines/columns, ≤256 argv
   entries and ≤4096 bytes each, structured-name/ID limits and control-character
   rejection; responses including escaped JSON/newline ≤1 MiB. Cap list item
   count and text construction *before* cloning/serializing, with deterministic
   UTF-8-safe truncation and accurate `truncated` flags. The current response
   implementation clones/re-serializes an oversized value and halves the longest
   string; replace that with bounded construction so huge lists and text cannot
   allocate unboundedly. Reject unknown envelope/parameter fields, and ensure
    request debug/log formatting cannot expose params or tokens. The current
    derived `IpcRequest: Debug` prints params (which may contain secrets) even
    though the token field itself is redacted; replace it with a safe summary.
    Preserve `request_id` on valid parsed errors; define a consistent
    uncorrelatable malformed-frame response. Do not imply request-ID
    deduplication or retry ambiguous mutations.
3. In `crates/omaterm-ipc`, make incomplete-frame **5 seconds total**, and
   request **10 seconds total** including queue wait, execution and response;
   current per-read timeout and parse-only deadline do not satisfy that.
   Enforce one sequential in-flight request/response per connection, ≤32
   connections, a bounded pending-owner queue, predictable overload errors,
   bounded worker/waiter lifetime, disconnect cancellation and prompt shutdown.
   Define when each deadline starts, measure using a monotonic clock across all
   phases, and ensure a slowloris peer cannot restart a timeout with each byte.
   Avoid blocking the desktop while joining a worker that awaits desktop work.
   Client reads/writes/connect should use corresponding bounds. The existing
   callback is transport-only and may block a connection worker; its owner
   queue must be bounded before the desktop server is exposed.
4. Harden endpoint lifecycle: validate private directory, ownership, symlinks,
   unsafe fallback and endpoint type; hold startup lock while probing stale vs
   active and binding. Avoid opening/chmod-ing an untrusted pre-existing lock
   file before validating its type/owner/permissions. Bind owner-only socket,
   validate `SO_PEERCRED`, and on shutdown unlink only this server's endpoint;
   a second desktop must not take over an active socket. Test fallback creation,
   active/stale/raced startup, wrong owner, symlink, and replacement between
   probe and cleanup. Do not mutate an unsafe existing lock file via chmod.

**8A tests:** parameter/result round trips for all 17 methods; missing/unknown
fields and invalid selectors, versions and request-ID preservation; exactly-at/
one-over each frame and field cap; multi-byte UTF-8, JSON-escaped text, large
lists, base64 errors and redacted Debug; incomplete/slow frames and slow writes,
 sequential requests on one connection, 32-client cap, transport overload,
disconnect/cancellation/deadline, peer mismatch, endpoint/lock races and inode-
checked cleanup. Use fault-controlled time and worker barriers where possible,
not sleeps as the only assertion.

**Exit:** protocol/transport tests demonstrate exact boundaries, encoding,
escaping, UTF-8, timeout/slowloris/oversized frames, sequential requests,
concurrency, disconnects and clean socket ownership. Passing the current
generic `test.ping` callback test alone is insufficient.

## Phase 5 — M8 credentials, desktop owner bridge and observable IPC (steps 8B–8C)

### 8B — Credential lifetime and scope enforcement

1. Initialize socket path and credential coordination **before** default or
   restored shells launch. Preallocate project/tab/pane/session IDs; generate at
   least 256 CSPRNG bits per-session, bound token to project + originating
   pane/session, pass `OMATERM_SOCKET`, `OMATERM_TOKEN`, `OMATERM_PROJECT_ID`,
   `OMATERM_TAB_ID`, `OMATERM_PANE_ID`, `OMATERM_SESSION_ID` via child-only PTY
   env. Issue fresh tokens on restore, revoke on pane/session/tab/project close
   and shell exit; define and test token expiry and dispose tokens if spawn
   fails. Do not persist or log secrets. Extend `TerminalConfig`/PTY spawn to
   accept explicit child-only environment; never set process-wide env. Reserve
   identities and generate tokens on the owner before the worker starts, but
   publish the capability only after a successful owner commit. Dispose of
   failed/stale provisional credentials without making them usable. Keep a
   credential registry keyed by **token identity**, not by client-supplied IDs.
2. Create separate unpredictable owner-only **local-user** credential in the
   private runtime directory, atomically and safely, tied to this instance;
   never inject it into child shells. In-app requests require their scoped
   token; missing/expired/wrong tokens fail without local-user fallback.
   Authenticate same-UID peers as a supplement, not as global authority. Record
   how the external client discovers the local-user credential without exposing
   it to a child shell; verify no token/params in Debug, tracing or error text.
3. Extend `CommandContext` and central router authorization: for project-scoped
   credentials, filter project/tab/pane/terminal lists and deny explicit foreign
   project, tab, pane, split or session targets (`cross_project_denied`). Forbid
   global project creation/selection outside scope; authorize implicit selectors
   within scope. Revalidate token, ownership and session existence immediately
   before commit/effects, including after async creation. Prevent selection
   side-effects on denied or failed operations. Cover *all* 17 methods with a
   scope matrix: local-user versus project scope, implicit versus explicit
   targets, list filtering, global project create/select, pane/terminal lookup
   and cross-project split. Explicit foreign targets must fail with stable
   `cross_project_denied` rather than leaking their data. Define an injectable
   monotonic expiry policy and test two-project isolation, expiry, revocation,
   stale token after restore and ownership change with headless tests.

**8B tests:** valid project token sees only its project across every list;
foreign IDs and implicit selection fail or resolve within its scope; missing,
wrong, expired and revoked tokens cannot fall back to local-user privilege;
local-user credential stays out of PTY child env; spawned Bash receives exactly
its own IDs/socket/token; close, shell exit, rollback, stale completion and
restart revoke old tokens; default/fallback runtime directories are owner-only.

### 8C — Desktop bridge, final response and shutdown

1. Start `IpcServer` automatically in `apps/omaterm`, hand accepted work to a
   bounded owner queue and use the **same** `CommandRouter`/effect consumer as UI.
   Do not mutate GPUI state or terminal registry on connection threads. Check
   cancellation/deadline before dispatch; after commit report an ambiguous
   timeout honestly (no replay). When closing: stop ingress; cancel queued
   work/waiters; settle committed effects; save latest logical state (surface
   failure without skipping cleanup); revoke tokens; terminate/reap children;
   close clients; remove only owned socket/credential files; release window.
   Ensure the owner loop drains requests fairly alongside GPUI events and spawn
   completions. For async creates, reply only after commit or failure; check
   token, deadline, cancellation and target **again** immediately before commit.
   Await listener shutdown off the GPUI thread to avoid a server-worker → owner
   deadlock. Define startup failure UI/reporting behavior without launching
   children with unusable socket coordinates.
2. In an actual Omarchy/Wayland desktop process, use an external protocol client
   (M9 CLI not needed yet) to query `pane.list` and compare with UI, send
   `pane.split` and observe the new visible pane/shell, test `terminal.read`
   bounds, inspect scoped versus local-user responses across two projects,
   then close desktop and verify endpoint/credential cleanup. Exercise busy,
   hidden and restored sessions while doing this. Confirm no data races or
   regressions under concurrent clients. Use the protocol client from
   `omaterm-ipc` or a small test harness, not an M9 CLI implementation.

**8C tests:** real socket → owner → router/effects → response for all 17
methods (success and representative failures), read/list parity with in-process
results, async split/create final IDs and UI visibility, concurrent scoped and
 local-user clients, bounded owner-queue saturation, disconnect before and after commit,
shutdown with queued and active clients, no blocked worker/PTY/zombie left,
persisted state correct after restart, and owned socket/credential cleanup.

**Exit:** every M8 acceptance criterion in the milestone spec has automated
and manual evidence. Only then mark M8 complete and proceed to M9.

## Carried-forward release evidence and verification discipline

- For the M4/M5/M8 resource row, record baseline and post-cycle FD, thread,
  process/zombie and RSS counts plus GPU observation during reproducible
  visible/hidden create/close cycles (including IPC). Capture workload,
  duration, build mode, hardware and tolerance; investigate **growth across
  cycles**, not a single retained RSS high-water mark. Existing 100-cycle FD
  evidence stays valid, but does not establish thread/GPU behavior.
- For test reliability, try the required `cargo test --workspace` without
  flags. Earlier default-parallel PTY runs stalled while serial runs passed;
  isolate/reproduce and fix concurrency or interference if it recurs. Record
  timeouts as failures/pending, and keep serial test evidence separate.
- For every Rust completion change, run `cargo fmt --all --check`,
  `cargo test --workspace`, `cargo clippy --workspace --all-targets -- -D warnings`,
  milestone-targeted unit/integration tests, `python3 scripts/check-docs.py`
  and `git diff --check`. For UI work also run the release build and an actual
  Wayland desktop session. Record exact results and unavailable checks in
  `status.md`; update acceptance rows only with verified evidence.
- Track `Cargo.lock` and record actual dependency revisions, license evidence,
  toolchain/API verification and native needs in `dependencies.md` for any new
  token-generation, base64, or IPC dependency. Complete transitive license
  inventory as a release follow-up; the existing `proc-macro-error2` future
  incompatibility notice is documented, not a Clippy pass failure.

**Smallest next action:** begin M9 CLI against the proven socket (M9 spec:
`crates/omaterm-cli` with `clap`, coverage table, human + `--json` output).
M5–M8 are complete with the documented limits in `status.md`; that file holds
all completion evidence. One watch item for M9 soak: a single transient +2
pane count during an early rapid split/close loop (unchecked close results),
unreproduced in 26 subsequent checked cycles — re-open M7 if it recurs with
captured failing responses.
