# v0.1 Acceptance Matrix

The [blueprint](../OMATERM_AGENT_BLUEPRINT.md), especially §§22, 48, 52–55, and 78,
defines release intent. This matrix assigns verification to the existing slices.
Every row is **pending** until linked evidence is recorded in [status.md](status.md).
Unit tests do not substitute for real desktop observations.

| Requirement | Owner | Automated verification | Manual verification | Evidence |
|---|---|---|---|---|
| Reliable Linux startup, close, background/text | M1 | Workspace checks, Wayland/X11 builds | Omarchy/Hyprland launch/close | Pending |
| Verified dependencies and reproducible toolchain | M1 onward | Locked build and CI checks | Record native packages and GPU environment | Pending |
| Recursive splits in all four directions | M2 | Nested topology, locality, unique IDs | Placeholder and real-terminal splits | Pending |
| Remove/collapse, last-child behavior | M2, M4–M5 | Empty-root and focus invariants | Close final pane/tab/project | Pending |
| Focus, pane resize, equalize | M2, M5 | Neighbor ties, finite fractions, minimum geometry | Keyboard/pointer navigation and resize | Pending |
| Real shell and normal user configuration | M3 | PTY input/output/exit tests | bash, zsh, fish where available; user prompt config | Pending |
| Correct terminal rendering/input | M3 | ANSI, cursor, wide/combining Unicode fixtures | Unicode prompt, modifiers, scaling, monitor changes | Pending |
| Alternate screen and interactive TUIs | M3 | Mode transitions and restoration | tmux, vim/neovim, htop/btop or equivalents | Pending |
| Clipboard and selection | M3 | Selection coordinates and bracketed-paste encoding | Wayland copy/paste including multiline text | Pending |
| Input method behavior | M3 | Composition paths where GPUI tests permit | IME composition/commit/cancel; record unsupported cases | Pending |
| Bounded scrollback and resize | M3 | History cap, resize, viewport tests | Scroll, output flood, repeated resize | Pending |
| Long-lived independent terminals | M4 | Stable IDs/PIDs across layout/focus changes | Four or more independent panes | Pending |
| Cleanup without leaks or zombies | M4, M5, M8 | Spawn rollback, cancellation, reap tests | Repeated 100-session cycles, FD/thread/process/memory/GPU observations | Pending |
| Default/multiple projects and independent tabs | M5 | Create/select/close and fallback tests | Sidebar, tab bar, keyboard switching | Pending |
| Hidden terminals keep processing output | M4–M5 | Hidden output progress and stable identity | Sustained output while switching projects/tabs | Pending |
| Logical state returns after restart | M6 | Round-trip including names, sidebar, focus/selections | Restart with multiple projects, tabs, splits | Pending |
| Fresh shells in remembered directories | M3, M6 | CWD/fallback, new identity, partial spawn failure | `cd`, save, restart; confirm directory and fresh process | Pending |
| Recovery preserves original state | M6 | Invalid/unknown snapshots, interrupted writes, ordered saves | Recovery message and retained original | Pending |
| Shared semantic UI/CLI operations | M7–M9 | Domain/dispatcher tests and end-to-end parity | Same actions through UI and CLI | Pending |
| Scoped authorization | M8 | Two-project isolation, revocation, filtered lists, local-user access | In-app CLI cannot target another project | Pending |
| Versioned bounded IPC | M8 | Round-trips, frame/response limits, timeout/disconnect tests | External client against actual desktop | Pending |
| CLI coverage and machine output | M9 | Every M9 table row, JSON errors, exit codes, selectors | Commands from inside/outside OmaTerm | Pending |
| CLI launch and architecture proof | M9 | Both binaries, supported-shell run/read sequence | Blueprint §78 flow from authorized shell | Pending |
| Ordered shutdown | M6, M8 | Stop ingress, save, cleanup, socket ownership tests | Exit with busy/hidden terminals; inspect resources | Pending |
| Performance baseline | M1, M3–M5 | Repeatable workloads with recorded settings | Startup, idle CPU, latency, throughput, memory/session, resize/scroll | Pending |

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
