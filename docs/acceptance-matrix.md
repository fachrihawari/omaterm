# v0.1 Acceptance Matrix

The [blueprint](../OMATERM_AGENT_BLUEPRINT.md), especially §§22, 48, 52–55, and 78,
defines release intent. This matrix assigns verification to the existing slices.
Every row is **pending** until linked evidence is recorded in [status.md](status.md).
Unit tests do not substitute for real desktop observations.

| Requirement | Owner | Automated verification | Manual verification | Evidence |
|---|---|---|---|---|
| Reliable Linux startup, close, background/text | M1 | Workspace checks, Wayland/X11 builds | Omarchy/Hyprland launch/close | M1: Wayland launch, rendered window, and Super+W clean close verified 2026-09-26; X11 build passes |
| Verified dependencies and reproducible toolchain | M1 onward | Locked build and CI checks | Record native packages and GPU environment | M1: Rust 1.98.1, GPUI 0.2.2, `Cargo.lock`, native packages, and CI recorded 2026-09-26 |
| Recursive splits in all four directions | M2 | Nested topology, locality, unique IDs | Placeholder and real-terminal splits | M2: all four core directions PASS; M4: nested terminal panes PASS 2026-09-27 |
| Remove/collapse, last-child behavior | M2, M4–M5 | Empty-root and focus invariants | Close final pane/tab/project | M4: final-pane empty state/new-terminal Wayland PASS 2026-09-27; tabs/projects are M5 |
| Focus, pane resize, equalize | M2, M4–M5 | Neighbor ties, finite fractions, minimum geometry | Keyboard/pointer navigation and resize | M2 keyboard geometry PASS; M4 keyboard focus/resize and `stty size` pane grid PASS 2026-09-27; hover-to-focus confirmed by user 2026-09-27 |
| Real shell and normal user configuration | M3 | PTY input/output/exit tests (13 integration PASS); Ctrl+C/D/Z proven | bash + starship manual PASS; zsh/fish unavailable on this machine | Complete with unavailable-tool limit |
| Correct terminal rendering/input | M3 | ANSI, cursor, wide/combining Unicode fixtures (50 unit PASS); pixel-measured grid/cursor gate | User-approved Omarchy release validation; alternate scaling/monitor unavailable | Complete with scaling-observation limit |
| Alternate screen and interactive TUIs | M3 | Mode transitions + content restoration tests PASS | vim PASS; htop unavailable; other available TUI tools are follow-up coverage | Complete with unavailable-tool limit |
| Clipboard and selection | M3 | Selection normalization/extraction (11 tests), paste encoding, wrapped `cat` round-trip PASS; OSC 52 read refused by design | User-approved Wayland release validation | Complete |
| Input method behavior | M3 | Normal direct Unicode input tested; no GPUI 0.2.2 composition path integrated | IME composition/commit/cancel intentionally not supported | Complete with explicit unsupported IME limit |
| Bounded scrollback and resize | M3 | 10k cap test, 3k-line burst bounded, resize-mid-flood keeps tail PASS | User-approved release scroll/resize validation | Complete |
| Long-lived independent terminals | M4 | Stable IDs/PIDs, four-session output isolation, resize and focus-preserving coordinator tests | Four or more independent panes | M4: automated 4-session isolation and coordinator stability PASS; Wayland four-pane input isolation PASS 2026-09-27 |
| Cleanup without leaks or zombies | M4, M5, M8 | Spawn rollback, close isolation/reap, 100-cycle FD test; M5 repeated-close and poisoned-cleanup tests PASS | Repeated 100-session cycles, FD/thread/process/memory/GPU observations | M4 PTY 100-cycle FD test PASS (+2 tolerance). M5 release Wayland: two 100-tab create/close cycles (13.75s/13.73s); app threads 21→21 each, FDs 34→34, RSS 55,156→71,416 KiB first warm-up pass and 71,416→71,452 KiB second pass; one shell at end; app/shell absent after close. Poisoned mutex cleanup test returns all sessions and children are reaped. RadeonTop system-wide sample 0% GPU / 674.46 MiB VRAM, not attributable to app; per-process GPU and M8 concurrent IPC resource measurements pending |
| Shell exit closes its pane | M4 | `exit`/Ctrl+D close-by-session tests preserve sibling and final-empty state | Verify both exits in multi-pane Wayland window | M4: automated sibling-preservation/final-empty tests and Wayland `exit`/Ctrl+D pass 2026-09-27 |
| Default/multiple projects and independent tabs | M5 | Create/select/close coordinator test and project/tab fallback core tests PASS | Sidebar, tab bar, keyboard switching | User confirmed project/tab create/delete, numbering, switching, state preservation, pane splitting/shortcuts, and initial chrome polish; 2026-09-27 release Wayland test clicked inactive Tab 1 in padding beyond its label and confirmed selection |
| Shell-start failure and Retry recovery | M5, M6 | Spawn-failure rollback and restore retry tests | Initial shell Retry and restored-pane Retry on Wayland | M5 initial-shell Retry PASS 2026-09-27. M6 restored-pane Retry PASS 2026-09-27: restore with missing `SHELL` preserved layout, then retry after installing the shell path launched a fresh shell in the saved/fallback CWD |
| Hidden terminals keep processing output | M4–M5 | M4 unfocused PTY output PASS; M5 hidden tab/project output, stable PIDs, and isolated close integration test PASS | Sustained output while switching projects/tabs | Automated M5 hidden hierarchy test PASS; user confirmed hidden output continues while switching |
| Logical state returns after restart | M6 | State tests include nested multi-project round trip; workspace suite passes serially | Restart with multiple projects, tabs, splits | M6 release Wayland PASS 2026-09-27: two projects, multiple tabs, 3-leaf nested split, unchanged logical pane/split IDs, selected project/tab and focused bottom-right leaf restored; fresh shell PIDs verified |
| Fresh shells in remembered directories | M3, M6 | OSC 7 accept/reject/fragmented + procfs CWD refresh and fallback tests PASS | Live-shell OSC 7 emission is config-dependent (stock bash emits none) | M6 release Wayland PASS 2026-09-27: per-pane `cd` saved with `procfs`, restored fresh shells printed each CWD, deleted saved CWD restored under `$HOME` with `fallback_home`; initial test exposed and fixed missing desktop procfs refresh, with regression test |
| Recovery preserves original state | M6 | Corrupt primary retention, recovery-file scan, unique fallback path, atomic failure tests | Recovery message and original retention across restart | M6 release Wayland PASS 2026-09-27: corrupt primary warning shown, recovery file saved; same primary SHA-256 retained after another restart with warning |
| Shared semantic UI/CLI operations | M7–M9 | Domain/dispatcher tests and end-to-end parity | Same actions through UI and CLI | M7 complete 2026-09-27: single async path, 19 router tests (variant matrix, cancel/duplicate rollback), Wayland UI resize/equalize/focus/close/tab + IPC parity; M9 CLI still pending |
| Scoped authorization | M8 | Two-project isolation, revocation, filtered lists, local-user access | In-app CLI cannot target another project | M8 complete 2026-09-27 with documented limits (cross-UID harness, fallback-dir path); scope filtering/denial, expiry/revocation, child-only env verified in tests and live |
| Versioned bounded IPC | M8 | Round-trips, frame/response limits, timeout/disconnect tests | External client against actual desktop | M8 complete 2026-09-27 with documented limits (owner-channel saturation race); 11 transport tests + live 200-request load + shutdown-under-load proof |
| CLI coverage and machine output | M9 | Every M9 table row, JSON errors, exit codes, selectors | Commands from inside/outside OmaTerm | Pending |
| CLI launch and architecture proof | M9 | Both binaries, supported-shell run/read sequence | Blueprint §78 flow from authorized shell | Pending |
| Opt-in encrypted history recovery | M10 (post-v0.1) | Authenticated archive, replay, keyring failure, bounds, journal lifecycle tests | Restart, fresh shell, restored scrollback, opt-in/clear, keyring failure on Wayland | Planned; blocked until M5–M9 and history dependency/replay spikes are complete |
| Ordered shutdown | M6, M8 | Stop ingress, save, cleanup, socket ownership tests | Exit with busy/hidden terminals; inspect resources | M6 release Wayland graceful close persisted latest state, unsupported-schema close before debounce flushed both tabs to recovery, and no test app/shell remained. M8 2026-09-27: four release closes each removed owned socket + credential, cancelled pending work, flushed schema-v1 snapshot, reaped shells with no orphans, including one close under 4-client IPC load (29 threads/48 FDs during → clean) |
| Performance baseline | M1, M3–M5 | Bounded-channel flood + 3k-line burst complete (debug); ~2%/core settled idle in debug, reader thread zero | Release idle/startup and GPU observations | Partial: M5 two release 100-tab cycles measured threads/FD/RSS; second pass RSS +36 KiB, thread/FD counts stable. Device-wide RadeonTop idle was 0% GPU/674.46 MiB VRAM, not per-process. Formal startup/idle and per-process GPU metrics remain open |

## Platform evidence

Omarchy/Wayland is the primary completion gate. Record X11 build evidence and
runtime observations when an X11 session is available. Exercise another Wayland
compositor when available. List unavailable shells/TUIs/platforms explicitly.
For IME or other limitations, record reproduction, impact, and an explicit release
decision; absence of validation is not a passing result.

Record performance measurements with workload, duration, hardware, build mode,
visible/hidden session counts, and baseline tolerances. Avoid inventing universal
latency/memory thresholds before measurement. Investigate growth across repeated
lifecycle cycles rather than judging a single RSS value.

## Final proof

Use the sequence in [M9](09-milestone-9-cli.md). Check that split returns a new pane
ID, run acknowledges submission to a ready supported shell, and read returns
bounded viewport text. Validate effects in the same desktop instance. Output read
success is not evidence that the submitted command completed successfully.
