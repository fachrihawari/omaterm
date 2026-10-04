# M18 current-worktree release runtime validation — 2026-10-04

**Result: real isolated Wayland/CLI evidence collected; M18 completion is not
established.** Process attribution, CPU/RSS sampling, project exclusions,
denials, 100-query stability, terminal responsiveness and targeted normal
shutdown were exercised. Default interactive Bash did **not** exit on SIGTERM;
the shell-exit/pane-close proof required an explicit TERM trap. Native panel
arm/confirm, copy-path, collapse and manual-refresh interactions were not driven.

## Source and binaries

Source parent: `73b34701f50b6a0de00a66151c7c9d1a9196852c` plus the existing
uncommitted changes in the three Rust files below. Tested the supplied release
binaries directly; this run did not rebuild or rerun Cargo gates. Source and
binary hashes were checked again after runtime shutdown and were unchanged.

| Input | SHA-256 |
|---|---|
| `git diff --binary HEAD` captured at launch | `ec7f0623626e652ddea52164409b1cfae6b419c04617c2455188727f27e7bc5d` |
| `apps/omaterm/src/main.rs` | `3cb4dee249a1c7f27e04fe176fbb8ebd9c5d71d55b269dd9ffa34414d02de9b3` |
| `apps/omaterm/src/router.rs` | `a879cf5f049cf700ef110a58ad486d4bae01b4942303e5c79ead2a1f8ede1eb6` |
| `crates/omaterm-terminal/src/platform.rs` | `e67f1afcc52ec926bf02c85c959a0412665e6d735c130c931fbb971202e8e521` |
| `Cargo.lock` | `e9c51f7915685cf5cd12f3191bbf0b7e9bbee1dea8c3ca3229dbb2c5663f810f` |
| `target/release/omaterm` (1,987,592 bytes) | `67f17b4a19e678a73129114c6566635ff33474f9398ec5d72503a2b7119c49c1` |
| `target/release/omaterm-desktop` (36,831,336 bytes) | `1789c51bdccba86f3ab057c8f9d4c1ef448ee398095cea62c9e117db0aebecca` |

No source edits or commits were made. The only repository addition from this
validation is this evidence document.

## Isolation and harness incident

Read `AGENTS.md`, the M18 milestone, status, blueprint process/performance/
lifecycle requirements, the M19 singleton-displacement evidence and the existing
Wayland acceptance harness's isolation/focus rules before the isolated run.

**Preflight isolation violation:** invoking `target/release/omaterm-desktop
--help` with the inherited environment launched the desktop rather than printing
help. The tool timed out after 120 seconds. A subsequent compositor/process check
showed no `omaterm-desktop` process/window remaining. Before that probe, the only
listed compositor client was the user's Foot window, PID 2794106. No production
CLI requests or production-path inspection/cleanup were performed. Production
config/state/socket effects of this accidental launch were not audited, so this
run cannot certify that production paths were untouched. The mistake is a
harness incident, not evidence of a product crash or a passing isolation gate.

All validation operations below used a fresh, unique directory:

```text
/tmp/opencode/m18-runtime-current-u7g1kgqc/
  home/     config/     state/     runtime/
  cache/    data/       repo-a/    repo-b/
```

The desktop environment was allowlisted: isolated `HOME` and all XDG homes,
`SHELL=/bin/bash`, a test PATH, `LANG=C.UTF-8`, `RUST_BACKTRACE=1`, an absolute
connection to the existing Wayland display and the Hyprland instance handle.
No inherited `OMATERM_*` credentials were passed to the external CLI or desktop.
Both repositories were initialized with `git init -q`. The runtime directory
was empty before the sole isolated desktop launch. No second instance was
launched into this runtime directory.

Actual paths, checked with filesystem metadata and successfully used by the CLI:

| Path under the isolated root | Type | Mode | UID |
|---|---|---|---|
| `runtime/` | directory | 0700 | 1000 |
| `runtime/omaterm.sock` | Unix socket | 0600 | 1000 |
| `runtime/omaterm.credential` | regular file | 0600 | 1000 |

None of those paths was a symlink. Credential contents were never printed or
recorded in command evidence. Every external CLI call used the explicit isolated
`--socket`; the in-shell CLI call also supplied that socket, using the shell's
actual project-scoped credential.

Runtime start: **2026-10-04T13:41:45Z**; final evidence summary:
**2026-10-04T13:49:54Z**. Native compositor: Hyprland **0.56.2**,
commit `efb50993780079460b0cbed1363e2166a2de1d9f`.
Owned desktop PID **2812019**, Wayland (`xwayland: false`), window address
**`0x5d653f9209e0`**. No keyboard/pointer injection was used; terminal commands
were delivered semantically through `terminal send --pane`.

Temporary harness: `/tmp/opencode/m18-runtime-validator.py`, SHA-256
`70c6411ce7525ab536b9dd185513487a589db4ef13a0efa2a234e972d6d649a6`.
Its script/fixture sources were created with `apply_patch`. Fixture SHA-256:
`65f970642b55038171178dcdb85d7892e32ef7b37589470235bb793e26058ead`.
Raw runtime outputs remain under the isolated root, including `metadata.json`,
`isolation.json`, `commands.jsonl`, `fixture-checks.json`, `stress.json`,
`kills.json`, `cleanup.json`, `summary.json` and the screenshot. Temporary
artifacts are local evidence, not committed attachments.

## Command outcomes and attribution

External calls used this prefix (abbreviated below as `CLI`):

```sh
target/release/omaterm \
  --socket /tmp/opencode/m18-runtime-current-u7g1kgqc/runtime/omaterm.sock \
  --json
```

Opened `repo-a` and `repo-b` as `M18-A` and `M18-B` using `project open PATH
--name NAME`. Each terminal ran the own fixture: a loopback Python HTTP server
on an OS-selected port, with a `sleep 300` descendant.

| Identity | Project A | Project B |
|---|---|---|
| Project | `e78877df-b9ba-4cb8-8c8b-7604d5dc574d` | `1c3d6705-3174-483d-8dae-928366b83370` |
| Pane | `756c5478-e4ed-4305-90d5-e427bc459517` | `9fc16343-5173-4559-a182-a54ee11a6cf1` |
| Session | `62680416-3411-4e05-9c56-e3b11c1c3315` | `f5ba5a77-0325-45d4-85b4-1647dcc3fe2f` |
| Bash PID | 2812739 | 2812752 |
| Python server PID | 2812774 | 2812800 |
| Sleep PID | 2812786 | 2812801 |
| TCP listening port | 41457 | 38921 |

`CLI process list --project A` and `--project B` both returned exit 0,
`ok: true`. Each included its own shell/server/sleep PIDs and excluded all three
foreign PIDs and the foreign port. A's server had `ppid: 2812739`; its sleep had
`ppid: 2812774`. Both children had A's exact pane/session attribution. Only
Python owned port **41457**; sleep's port list was empty.

A's new fixture PIDs were sampled while B was selected, avoiding A's Info
auto-refresh priming the new PIDs before the first explicit A query:

| A process | First `cpu_percent` | Next after 1.1 s | Next RSS bytes |
|---|---|---|---|
| Python 2812774 | `null` | `0.0` (finite) | 21,557,248 |
| Sleep 2812786 | `null` | `0.0` (finite) | 2,265,088 |

This proves null-first/finite-next behavior for idle fixtures and positive RSS;
it does not prove nonzero CPU accounting under a busy workload.

### SIGTERM and denial cases

| Operation | Observed outcome |
|---|---|
| `CLI process kill 2812800 --project A` | Exit **1**, `cross_project_denied`; B's server remained alive |
| Same request from inside A's shell using its own scoped token and explicit socket | `cross_project_denied`; viewport contained actual `SCOPED_EXIT=1`; B's server remained alive |
| `CLI process kill 0 --project A` | Exit **1**, `process_not_found` |
| `CLI process kill 2812786 --project A` | Exit **0**, `{pid: 2812786, signal: "SIGTERM"}`; sleep reached zombie state; A's shell stayed alive |
| `CLI process kill 2812774 --project A` | Exit **0**, SIGTERM; server and its terminated sleep descendant were subsequently absent from `/proc` |
| `CLI process kill 2812739 --project A`, default interactive Bash | Exit **0**, SIGTERM confirmation, but Bash was still alive after 600 ms |
| Same own-shell kill after installing `trap 'exit 0' TERM` through its pane | Exit **0**, SIGTERM; Bash disappeared and its pane was removed |

The Python fixture deliberately did not reap its sleep child itself. The initial
zombie observation proves child termination, not immediate row removal/reaping;
both fixture PIDs were gone after the parent was terminated. A normal parent
reaping path and UI row-removal timing were not independently established.

Before the shell-exit probe, A's pane was split through the semantic CLI. A had
two panes; after the TERM-trapped shell exited, its original pane ID was absent
and one A pane remained. This is conditional shell-exit/pane-close evidence,
**not** an unconditional pass for killing the default interactive shell.
Both B fixture PIDs were also terminated with own-project CLI calls before
desktop closure.

`commands.jsonl` contains **153 external CLI calls**: **151 exit 0**, and the
two expected exit-1 denial/validation calls. The in-shell scoped call is recorded
separately in `scoped-foreign.json` and its captured exit-code viewport.

## 100-query resource and responsiveness measurements

Ran **100 sequential** `CLI process list --project A` requests, with no
overlapping list calls. Every call succeeded, and each result stayed within
512 entries. Interleaved ten terminal send/read probes after queries
1, 11, 21, 31, 41, 51, 61, 71, 81 and 91. Each asserted the executed output
`M18_RESPONSIVE_NNN`, constructed with split `printf` arguments so echoed
input could not satisfy the assertion.

Actual `/proc/2812019/fd` counts and `/proc/2812019/status` values:

| Desktop resource | Before | After | Delta |
|---|---:|---:|---:|
| File descriptors | 44 | 44 | 0 |
| Threads | 37 | 37 | 0 |
| VmRSS (KiB) | 71,980 | 73,236 | +1,256 |

Process-list CLI wall times: **26.016 ms minimum**, **30.415 ms median**,
**59.849 ms maximum**. Executed-output send/read round trips ranged from
**20.227 to 54.252 ms**. These include CLI/IPC/polling overhead; they are not
frame-time or UI-thread scan timings. The observations establish FD/thread
neutrality over this workload, not RSS neutrality or long-term leak freedom.
All 100 query timings and ten responsiveness timings are in `stress.json`.

## Info-panel observation

Verified the visible client and active window both had the exact own PID and
address, then ran `grim -g '1201,71 330x854'` to capture only the right-side Info
panel. No focus command, keyboard or pointer event was sent.

Screenshot: `/tmp/opencode/m18-runtime-current-u7g1kgqc/info-panel.png`;
SHA-256 `a4d99d7fb374d95ff982ceeaf0573c446262e543d907c83ba1da9bab15c646a9`.
The image was read and visibly shows:

- Selected Info tab and M18-A project/root card.
- `bash · PID 2812739`, directory and `Copy path` chip.
- `PROCESSES · 3`, Refresh, live dots and per-row kill controls.
- Bash, Python and sleep rows with `0.0%` and positive displayed memory.
- `PORTS · 1` and `41457 · python3`.

This is real rendered state, not proof of pixel-level Kero fidelity or successful
copy/refresh/collapse/arm-confirm interaction. Those interactions remain open.

## Targeted normal closure and cleanup

The legacy command `hyprctl dispatch closewindow
address:0x5d653f9209e0` failed with exit **7**, Lua parse error. A quoted
string-dispatch form also failed with exit **7** (`expected a dispatcher`).
The compositor's dispatcher constructor was probed without executing it; the
working Hyprland 0.56 form was then used after rechecking exact address/PID:

```sh
hyprctl dispatch 'hl.dsp.window.close("address:0x5d653f9209e0")'
```

This is the targeted window-close dispatcher (the Lua equivalent of
`closewindow`); it returned exit **0**, `ok`. No broad window command or
desktop process signal was used to close the isolated instance.

Observed after closure:

- Desktop process and own compositor window gone.
- All nine recorded owned PIDs absent from `/proc`: **2812019, 2812049,
  2812739, 2812752, 2812774, 2812786, 2812800, 2812801, 2813800**.
- The app removed **`runtime/omaterm.sock`** and
  **`runtime/omaterm.credential`**; no manual unlink was performed.
- The runtime directory retained only **`omaterm.lock`**.
- Immediately before close: **44 FDs, 37 threads, 75,504 KiB VmRSS**.
- Post-close compositor clients contained the original Foot PID **2794106**,
  at its original geometry. No commands targeted that window.
- Isolated `desktop.log` was empty.

Process disappearance proves those processes' FD/thread resources were released;
this run did not instrument internal worker-join events or GPU accounting.

## Remaining limitations

- Preflight accidentally launched the desktop outside isolation; production-path
  noninterference cannot be certified for that probe.
- Default interactive Bash ignores SIGTERM. Signal delivery succeeds, but native
  default-shell closure remains unproven/failed in this fixture; only the
  TERM-trapped shell demonstrates the pane-close exit path.
- Native two-step kill arming/confirmation, disarming, collapse, manual Refresh
  and copy-path interaction remain unexecuted.
- No busy CPU workload, UI-thread `/proc` timing, long-idle memory trend, GPU
  counters, full process-cap workload or release quality gates were measured.

Documentation verification after adding this report:

- `python3 scripts/check-docs.py`: **PASS**, exit 0; 44 Markdown files,
  302 local link targets, 318 numbered blueprint references and all 35 CLI
  method mappings. External URLs/heading anchors were not independently checked.
- `git diff --check`: **PASS**, exit 0.

This evidence does not declare M18 complete.
