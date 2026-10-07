# Native wheel easing verification

Tested the release build from `2c4a8bd` plus the scrolling working-tree changes
on Hyprland 0.56.2, Wayland, a 60Hz display at 1.25 scale. This is animation
behavior evidence, not a general renderer performance acceptance result.

## Method

- Isolated config, state, runtime socket and disposable Git repository under
  `/tmp/opencode/wheel-j3l64xjs`; 80 untracked files, 300 lines per editor file.
- Launched the rebuilt release desktop with a dedicated Bash terminal. Opened
  the fixture project through the real CLI, printed 300 terminal lines, opened
  a file and its real untracked diff through native pointer actions.
- Sent `zwlr_virtual_pointer_v1` wheel events (`axis_source=wheel`, discrete
  detent + frame) through Wayland. Confirmed target-window focus before each
  input. Clicks/wheels were native events, not semantic scroll simulations.
- Collected content-free `omaterm::render` animation traces and final screenshots.
  Temporary injector and orchestration: `/tmp/opencode/scroll-pointer.c` and
  `/tmp/opencode/validate-wheel.py`.

## Observations

| Surface | Updates for one detent | First position | Settled position | First-to-last interval |
|---|---:|---:|---:|---:|
| Terminal | 16 | +4.01px | +92.40px (four grid rows) | 250ms |
| Editor | 15 | −14.12px | −96px | 234ms |
| Git change list | 15 | −8.52px | −96px | 234ms |
| Diff preview | 9 | −15.01px | −96px | 235ms |

Files produced two 16-update sequences (down 96px, then a return-to-top wheel
clamped at 0). The final captures show a partially visible file row, editor
line 5 at the upper edge, scrolled Git header/content, and a scrolled diff.
Normal frames were approximately 16.6ms apart for the first four surfaces;
the Split diff often needed approximately 33ms between updates. Traces observe
application animation positions, not compositor scanout timestamps or GPU latency.

Each sequence reached `continuing=false` at its exact destination. The terminal
renderer interpolated within rows using a following overscan row. No PTY resize
or session replacement is part of the animation.

## Synthetic touchpad integration — 2026-10-07

The current release worktree was also exercised with five native virtual-pointer
Wayland pixel-axis events using `axis_source=finger`, followed by `axis_stop`.
GPUI 0.2.2 maps each event to `ScrollDelta::Pixels` with `TouchPhase::Moved`
and does not expose `axis_stop`; OmaTerm therefore starts coasting after its
42ms no-input interval. On the terminal, the preceding wheel sequence settled
at `92.40px`; the finger stream produced intermediate positions through
`276.55px` and settled at `277.20px`, with a total of 18 rendered frames after
the first finger event. This confirms that pixel input is captured, followed
directly, and continues after input stops. It is an integration smoke test, not
a substitute for subjective physical-trackpad validation.

## Cleanup and limits

The owned test window (PID 2813288) closed through the compositor, the process
exited, and the isolated socket/credential were removed; only `omaterm.lock`
remained in its runtime directory. The previously focused window was restored.

Earlier harness attempts failed before input due to outdated Hyprland dispatcher
syntax and an incorrect CLI response key; a later first-input attempt exercised
only the terminal because the fixture project/tab coordinates were wrong. The
successful run above corrected these issues and asserted all five target names
in real animation traces. Earlier partial runs are not five-surface acceptance.

Physical mouse/trackpad feel, IME during animation, simultaneous output floods,
horizontal-wheel behavior and full-screen TUI mouse protocols were not exercised
by this native run. No broad latency/resource-budget pass is claimed.
