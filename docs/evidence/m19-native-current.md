# M19 current-release native validation — 2026-10-04

**Result: BLOCKED / partial evidence. S9 remains open.** A fresh release desktop
rendered in the real Wayland session, but compositor focus changed before the
first keyboard/pointer event. Injection was aborted. No native editor workload,
small-file cycle or cap-file cycle executed. The PASS observations below have
narrow scopes and do not close any complete E01–E10 requirement.

## Revision and environment

- Workspace: `/home/fachri/Projects/personal/omaterm`.
- HEAD: `f287991f223e01a36ea5e0a60c219c824da05ba3`, with pre-existing Rust/state/
  metrics/script changes. Initial working-tree status is in `launch.json`.
- No Rust edits or commits were made by this validation run.
- Parent command observed running:
  `RUSTUP_TOOLCHAIN=1.99.0 cargo test --workspace --quiet -- --test-threads=1 && RUSTUP_TOOLCHAIN=1.99.0 cargo build --release --bin omaterm --bin omaterm-desktop`.
  The release Cargo/rustc processes disappeared before launch and the desktop
  binary had a fresh mtime, `1791085161.396308`. Parent exit status/full build log
  was not captured here; this record does not certify the parent's Rust gates.
- Desktop: `target/release/omaterm-desktop`, SHA-256
  `00c2fae678853dc64a9c495c4c785cae73e145fd3556b32f9591ec23a1a2589e`.
- CLI: `target/release/omaterm`, SHA-256
  `f6f8c4fd5ff1d8fff03af53732ee2993df41fccbe23678a44792236b26043534`.
- Tool probes with `RUSTUP_TOOLCHAIN=1.99.0`: rustc `1.99.0 (b940084d7 2026-09-28)`
  and Cargo `1.99.0 (5f94df478 2026-08-27)`, preserved in `commands.jsonl`.
- Linux `7.2.5-3-omarchy`, Hyprland `0.56.2`, compositor commit
  `efb50993780079460b0cbed1363e2166a2de1d9f`.
- Target output: `eDP-1`, 1920×1200 physical, scale 1.25, 1536×960 logical,
  desktop origin (2048,0). App bounds: (2819,31), 760×924 logical; a real narrow
  tiled window. The initial terminal footer showed 14px text. Exact resolved
  font family was not independently measured.
- Desktop PID `1664916`, `/proc` start ticks `8542657`; the driver also checked
  `/proc/1664916/exe` before actions. Compositor socket:
  `/run/user/1000/wayland-1`; isolated IPC socket:
  `/tmp/opencode/m19-native-current/runtime/omaterm.sock`.

Artifact root **A** is `/tmp/opencode/m19-native-current`. The app received only:

```text
HOME=A/home
XDG_CONFIG_HOME=A/config
XDG_STATE_HOME=A/state
XDG_CACHE_HOME=A/cache
XDG_DATA_HOME=A/data
XDG_RUNTIME_DIR=A/runtime
PATH=/usr/bin:/bin
LANG=C.UTF-8
SHELL=/usr/bin/bash
EDITOR=/usr/bin/true
RUST_BACKTRACE=1
WAYLAND_DISPLAY=/run/user/1000/wayland-1
OMATERM_METRICS_PATH=A/metrics.json
```

Runtime permissions were 0700. `environment.json` records the expanded values.
The only app config written was `A/config/omaterm/config.toml`:

```toml
[terminal]
shell = "/usr/bin/bash"
[history]
enabled = false
```

Disposable fixtures were generated afresh under `A/fixtures`; no user's config
was edited. The fixture manifest and generator contract distinguish real disk
fixtures from injected/race test cases. Those cases were not exercised here.

## Driver contract and focus blocker

Inspected `/tmp/opencode/m19-live/cycles.py`, `pointer3.c`, prior fixture manifests
and artifact directory. The historical cycle script hardcodes PID `633343`,
waits for focus to return and does not guard pointer calls. `pointer3` is a raw
output-bound virtual pointer; it implements neither PID guarding nor the new
`omaterm-m19-helper` handshake. It was not blindly passed to the acceptance script.

The current acceptance script requires `--help`, `--m19-describe` and
`--m19-step`, exact-PID checks before every event and real outcome assertions.
It also explicitly leaves the conflict/restart/IME matrix pending. Collection
alone is not acceptance; the resource script accepts empty timing series as
unobserved, with null percentiles instead of manufactured samples.

External-only guarded keyboard/pointer binaries were prepared. The keyboard
uses the MIT-licensed `atx/wtype` source; the pointer wraps the inspected
`pointer3` source. Both wrappers check `hyprctl activewindow -j` immediately
before every key/modifier or pointer motion/button protocol request and exit
on PID mismatch. They are raw guarded tools, not implementations of the new
semantic helper protocol. Their input paths were **not executed** in this run.

Initial compositor queries confirmed the desktop PID. An attempted window
maximize command, `hyprctl dispatch fullscreen 1`, returned exit 7 with:

```text
error: [string "return hl.dispatch(fullscreen 1)"]:1: ')' expected near '1'
```

This compositor expects a newer Lua dispatcher contract. Before a corrected
attempt could run, the driver's next focus query found foot PID `2713`
instead of desktop PID `1664916` and raised `FOCUS LOST: injection aborted`.
A subsequent independent query found Chromium PID `210031`. At cleanup,
focus had returned to the app; injection was not resumed.
Neither the corrected dispatcher nor the subsequent keyboard/pointer sequence
executed. The run did not refocus the app or resume injection. Native case and
cycle counts are zero. The user's new focus was left alone.

## Executed evidence

| Observation | Result | Evidence / limits |
|---|---|---|
| Fresh release app creates real Wayland window, shell and isolated IPC socket | PASS | Exact PID/executable and bounds in `commands.jsonl`; `startup-app.png` shows real rendered terminal/chrome |
| Focus-loss rejection before native input | PASS | Initial app PID queries followed by Chromium PID mismatch; `cases.jsonl`; no guarded input invocation in command ledger |
| Disposable project creation over IPC | PASS | `project open A/fixtures/proj-a --name m19-current` returned project/tab/pane/session IDs |
| Existing `file.open` terminal contract | PASS | Returned `submitted:true`; bounded `terminal read` shows quoted `/usr/bin/true` and fixture path followed by returned shell prompt |
| Terminal identity across IPC file open | PASS | Before/after `pane list` retained pane `f79929f0-cf6c-4013-9913-137de9eaa57a` and session `ffa2bc2c-6da9-4fc1-aa35-bbede7c9c7db`; no native-editor parity claimed |
| Real process resource collection | PASS (collection only) | 20 `/proc` samples with app-produced `metrics.json`; no resource budget or latency pass |
| Acceptance-script and resource-script self-tests | PASS (tooling only) | Both `--self-test` commands exited 0; mock/synthetic self-test data is not native/performance evidence |
| Owned process/socket cleanup | PASS (forced cleanup only) | `cleanup.json`; no normal-exit claim |

## Native acceptance register

| Case | Result | Blocker / missing evidence |
|---|---|---|
| Native open → edit → save, entry routes and dedup | BLOCKED | Focus lost before first input |
| Editor versus terminal/diff input ownership and sentinels | BLOCKED | IPC terminal submission is not native ownership proof |
| Clean external-change Reload/Cancel | BLOCKED | No native document opened |
| Dirty conflict Reload/Overwrite/Cancel and re-conflict | BLOCKED | No native document opened |
| Dirty close Save/Discard/Cancel | BLOCKED | No dirty buffer created |
| Dirty shutdown / hidden-document decisions | BLOCKED | No native dirty workload; cleanup used a signal |
| Open-document registry normal restart / fresh terminal sessions | BLOCKED | No document registry populated or native normal exit/restart |
| Unicode commits, clipboard, grapheme movement, IME/preedit | BLOCKED | Input stopped; a running session IME alone does not prove app composition support |
| 20 small-file cycles | BLOCKED | Executed 0/20 |
| 20 cap-sized cycles | BLOCKED | Executed 0/20 |
| All-language/fallback, long-line/scroll/selection/narrow editor visuals | BLOCKED | Only initial terminal/chrome rendered |

E01–E10 remain unclosed: E01–E03 have no new domain/pending/cap proof; E04–E06
have no native registry/dirty/IME proof; E07 has only the explicitly scoped IPC
terminal observation; E08 has no highlight captures; E09 has only an idle
baseline; E10's final Rust/regression gates were not run by this validator.

## Actual resources and timings

`terminal-idle-resources.json` sampled the terminal-active app with two idle
shells and no open documents. Focus was lost before collection and was back on
the app at cleanup; focus was not monitored per resource sample. This is a
terminal baseline with uncontrolled focus, not a dedicated unfocused-window,
visible-editor idle, blink or hidden-editor proof.

| Metric | Actual observation |
|---|---|
| FD count, 20 samples | 41 throughout |
| Threads, 20 samples | 36 throughout |
| RSS | 72,036,352–72,044,544 bytes |
| CPU, 19 measured intervals | p50 0.9994%, p95 3.9975%, max 3.9976% of one CPU |
| Editor workers / queue / active / pending / results | 2 / 0 / 0 / 0 / 0 |
| Retained document bytes / token memory / rendered editor rows | 0 / 0 / 0 |

All six app timing series (`open_enqueue_to_ready`, `edit_to_frame`,
`edit_to_highlight`, `save_to_commit`, `close_to_retirement`,
`restart_to_usable`) contained **zero observations**. p50/p95 are null;
`latency_pass` is null. Command-ledger elapsed durations measure subprocesses,
not application editor latencies. No synthetic timing was supplied. Kernel,
compositor and launch load are recorded in `launch.json`; no editor latency or
regression budget can be evaluated from this baseline.

## Reproducible commands and artifacts

Setup commands executed (generated files remain under `/tmp/opencode`):

```bash
git clone --depth 1 https://github.com/atx/wtype.git /tmp/opencode/m19-wtype
cc /tmp/opencode/m19-pointer-guard.c /tmp/opencode/m19-live/virtual-pointer.c -o /tmp/opencode/m19-pointer-guard $(pkg-config --cflags --libs wayland-client)
# cwd: /tmp/opencode/m19-wtype
wayland-scanner client-header protocol/virtual-keyboard-unstable-v1.xml virtual-keyboard-unstable-v1-client-protocol.h
wayland-scanner private-code protocol/virtual-keyboard-unstable-v1.xml vk.c
cc guarded.c vk.c -o /tmp/opencode/m19-keyboard-guard $(pkg-config --cflags --libs wayland-client xkbcommon)
# cwd: workspace root
python3 /tmp/opencode/m19-native-driver.py launch
```

The driver launched the absolute desktop path using `subprocess.Popen` with
the exact allowlisted environment above. `commands.jsonl` records expanded
argv, return codes, output and measured subprocess durations for subsequent
IPC calls, focus queries, fixture generation, tool probes and these checks:

```bash
python3 scripts/run-m19-wayland-acceptance.py --self-test
python3 scripts/m19-resource-bench.py --self-test
python3 scripts/m19-resource-bench.py --pid 1664916 --proc-root /proc --metrics-json /tmp/opencode/m19-native-current/metrics.json --output /tmp/opencode/m19-native-current/terminal-idle-resources.json --samples 20 --interval-seconds 1
python3 /tmp/opencode/m19-native-driver.py 'cleanup()'
```

Other artifacts: `environment.json`, `launch.json`, `hashes.json`,
`fixtures/manifest.json`, `metrics.json`, `samples.jsonl`, `cases.jsonl`,
`cleanup.json`, `desktop.log` (empty), `startup-app.png` and `capture-note.json`.
The initial full-output screenshot was cropped to the verified app rectangle
and its original removed to exclude the neighboring user window. The Pillow
crop attempt failed because Pillow was unavailable; the actual crop used
`magick ... -crop 950x1155+964+39 +repage ...`, preserving only app pixels.
Both attempts are recorded. Hashes tie the fixture/scripts/helpers and inspected
desktop source files to this run; future builds require fresh hashes/evidence.

Documentation checks: `python3 scripts/check-docs.py` passed (39 Markdown files,
145 local links, 307 blueprint references and all 34 CLI mappings);
`git diff --check` passed. The new report's `git diff --no-index --check /dev/null
docs/evidence/m19-native-current.md` produced no whitespace diagnostics (exit 1
for the differing files). `documentation-checks.json` preserves exact results;
`report.json` summarizes the blocked run and hashes retained artifacts. The
validator's generated Python bytecode cache was removed from the workspace.

## Cleanup and next action

The owned desktop received SIGTERM after executable verification. Its two shell
children, PIDs `1664952` and `1668298`, and the desktop were absent afterward;
no SIGKILL escalation was needed. The socket remained as an unserved filesystem
entry after the signal, was verified non-serving and explicitly unlinked.
The isolated runtime has no remaining app socket. **This is forced cleanup,
not graceful shutdown acceptance.** No keyboard/pointer helper process was
started. Disposable state/fixtures and evidence were retained under A.

Next: rerun native interactions during an uninterrupted, target-focused session;
verify the compositor's current window-dispatch contract before using it, then
execute and assert each editor/conflict/restart/IME case and both 20-cycle
workloads. Keep S9 open until the full register and final gates have evidence.
