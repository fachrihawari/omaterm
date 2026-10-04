# Milestone 18 — Process Info Panel (Kero parity)

> Kero's Info-panel parity for the focused pane: shell identity + PID,
> directory + copy-path, collapsible PROCESSES (count badge) and PORTS
> (count badge) sections, manual refresh + debounced auto-refresh, and a
> scoped kill option. Kero is the behavioral reference; no GPL code is
> incorporated.

## Product Contract

A right-side Info panel bound to the **focused pane** (not project-wide
aggregation): shell name + `child_pid`, CWD + copy-path chip, process rows
(live-dot, name, PID, `CPU% · RSS`), port rows (port + owner process name,
attributed per-PID via fd-inode matching). Refresh: manual button plus a
2s debounced background refresh that never runs `/proc` scans on the UI
thread. Kill: per-row `×`, first press arms with a banner, second press
within 8s sends `SIGTERM` through `OmaCommand::Process::Kill { pid }`;
anything else disarms (paste/history two-step precedent). Killing the
shell itself is allowed — the pane then closes via the existing exit path.
Membership is revalidated immediately before signalling (`ESRCH` →
`process_not_found`, foreign project → `cross_project_denied`).

## Goals

- [ ] `ProcessInspector` += CPU sampler (`utime/stime` delta over the
  refresh interval) + RSS (`statm`); Linux adapter only, no `cfg` spread
  (blueprint §50). Spike decides hand-rolled vs `sysinfo` (MIT) behind
  the same seam — permissive licenses only.
- [ ] `ProcessCommand::{List, Kill}` via dispatcher; `MAX_PROCESS_ENTRIES
  = 512` validation; `process_not_found` error code; no
  `WorkspaceChanged`/`PersistenceDirty` effects (process state is
  ephemeral, never snapshotted).
- [ ] Full parity: `process.list [--project]` (bounded shell-family entries
  `[{pid,ppid,name,pane_id,session_id,ports,cpu_percent,memory_bytes}],
  truncated`) +
  `process.kill <pid>` → `{pid, signal: SIGTERM}`; human + `--json`.
- [ ] Panel UI: collapsible sections with badges, empty states ("no child
  processes", "no listening ports"), explicit error notices.

## Prerequisites

- v0.1 complete. M12 root semantics settled (ownership anchor). Independent
  of M13–M17 UI; M16 lists it as a searchable source once landed.

## Deliverables

- Platform: CPU/RSS inspection + `terminate(pid)` (`SIGTERM`) on the seam.
- Coordinator: `processes_for_project` (shell roots plus descendants and
  per-PID ports, deduped, PID-sorted, capped) + `kill_project_process`
  (membership check → signal → `ESRCH/EPERM` mapping).
- Router: `List` scope-filtered like `Terminal::List`; `Kill` ownership
  lookup with `CrossProjectDenied` on foreign PIDs.
- Protocol/bridge/CLI + output rendering (`process list` table, kill
  confirmation line).
- Desktop: Info panel + arm-confirm kill wiring via `dispatch_command`.

## Test Plan

- Unit: tricky `comm` parsing, listen-state filter, sampler delta math,
  validation bounds, scope-denial matrix.
- Integration: spawned `sleep` + python http server fixtures (ports
  attributed to the right PID), kill → reaped, kill-gone →
  `process_not_found`, foreign-project kill denied, 100-cycle list
  stability (FD/thread neutral).
- Desktop live: dev server shows child + port + owner; kill child keeps
  the shell alive; kill shell closes the pane; scoped in-app CLI cannot
  kill another project's PID.

## Acceptance Criteria

- [ ] Panel matches Kero element-for-element (header, directory, both
  sections with badges, refresh) on Wayland
- [ ] Kill flow (arm → SIGTERM → row gone) proven live incl. shell-kill
  pane close
- [ ] Wire/CLI parity tests green incl. JSON envelopes + exit codes
- [ ] No `/proc` work on the UI thread (timed); caps enforced pre-alloc
- [ ] Quality gates green

## Non-Goals

- No SIGKILL option, no CPU graphs/history, no Finder/VS Code chips
  (copy-path only), no project-aggregated view, no commit-adjacent
  actions. No `terminal wait` (v0.4). No Ghostty/macOS/Windows.

## References

- Blueprint §20–§21 (scope, approval), §23, §26, §43–§45, §50–§53
  (§51 process inspection — normative), §62–§66
