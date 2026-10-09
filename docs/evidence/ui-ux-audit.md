# UI/UX audit in progress — 2026-10-09

This audit preserves the user request for every desktop surface, regressions,
and a light theme. It is not a completion report. Source review has covered the
surface inventory below; native acceptance remains incomplete.

## Implemented findings

- Git remote operations retain readable Fetching/Pulling/Pushing labels and
  show project-scoped elapsed progress. Each task owns its receiver; completion,
  errors and worker disconnect release pending state. Full remote/upstream
  parameters and result detail survive. A disconnect regression failed before
  the fix and passed after it. Five scoped tests include real local-remote
  failure/retry/publish behavior; no external remote was changed.
- Shared light/dark palettes drive desktop surfaces, terminal defaults,
  ANSI colors and OSC color queries. Startup config or system appearance selects
  the palette. Shell `COLORFGBG` follows it; explicit application colors remain
  authoritative. README documents selection and restart behavior.
- Branch picker and stash fields have explicit input ownership. Hidden Git
  fields no longer consume typing; stash focus clears commit focus. Terminal
  search, clipboard routing and file drops respect the visible input owner.
  Modified mnemonic chords cannot discard or overwrite editor changes.
- Cheatsheet navigation and virtualization retain the complete shortcut list.
- Terminal PTY dimensions use deferred updates from actual painted canvas
  bounds, including search and persistent-banner space. Hidden panes retain
  their sessions and dimensions. Narrow desktop layouts reduce optional panel
  width while preserving panel minima and a terminal budget; desktop minimum
  window size is 640×400 logical pixels.
- Mouse drag state clears when the initiating button is released or the window
  deactivates (panel resize, terminal/editor selection, Files scrollbar).
- Finder, branch picker and cheatsheet float at window level, with bounded
  viewport height and shared body typography. Native testing proved the old
  main-column finder clipped result labels and wrapped help text excessively.
- Terminal rendering swaps explicit inverse colors and preserves blank-cell
  width for concealed glyphs rather than shifting following text left.
  DIM blends against the actual cell background; OSC cursor colors reach the
  renderer. Block and underline cursors cover wide glyphs.
- Git fields share grapheme-aware caret, selection, editing and clipboard
  behavior. Native selection replacement and copy/paste preserved the edited
  commit text. Staging and committing created that exact commit in the fixture.
- A terminal right-click now clears both Git field focus states before assigning
  terminal input. Pending remote Git workers remain owned through completion
  and asynchronous shutdown, so their bounded subprocess lifecycle is reaped
  before the window is removed.
- Three-or-more-pane layouts hide their floating pane toolbar; narrow terminal
  grids also hide it below 40 columns. This keeps the text area unobscured
  while keyboard and context-menu actions stay available.
- At the 640px supported desktop minimum, the shell defaults to unobstructed
  terminal panes. `Ctrl+B` and `Ctrl+Shift+E/G/I` reveal the requested Projects
  or Inspector panel as a single compact overlay; each remains reachable
  without forcing both sidebars into the terminal's minimum width. Native
  captures prove both the Git inspector and Projects overlay.
- Compact mode now gates both the computed input owner and direct Git/Files
  keyboard handlers, preventing a field or sidebar that is no longer rendered
  from consuming input. A guarded native focused-field resize confirms that
  post-resize text reaches the visible editor while the hidden commit field
  retains only its pre-resize text.

## Complete surface inventory and remaining evidence

| Surface | Required native coverage | Current evidence |
|---|---|---|
| Projects | open/select/cycle/delete/menu/resize/hide/restore | native selected sidebar and bounded context menu; restore of selected project proven; other actions pending |
| Header | terminal/editor/diff tab activation, close/new, overflow, numeric jumps | source review; light terminal header capture only |
| Terminal | split/resize/equalize/zoom, selection, clipboard, risky paste, search, wheel, alt screen, drops, exited/retry/restore | native 3-pane 640×400 compact layout, search query/count/Escape dismissal, zoom/restore, and safe multiline-paste arm/expiry proven; other cases pending |
| Files | loading/error/empty/tree expansion/keyboard/scrollbar/open/terminal-open | source review; live tree and compact 640px shortcut-overlay captures; other states pending |
| Git | commit/stash focus, stage/unstage/discard, graph/history/diff, remote pending/error/success | native field editing, stage-all, commit, graph, Push failure, delayed pending and successful local remote publication proven; other actions pending |
| Info | refresh, processes/ports, kill confirmation, overflow | native process kill arm renders a specific SIGTERM confirmation and Escape cancels without killing the shell; other states pending |
| Editor | open/edit/save/undo/selection/IME, dirty/conflict/reload/overwrite/cancel, lifecycle | native finder open/edit/save proven on disk; undo and dirty-close shown; Ctrl+D preserves decision; other states pending |
| Diff | modes, staged/worktree/history, hunk navigation/stage/copy/open, errors | native working-tree split diff opened from Git and Stage File updated the fixture index; other modes/actions pending |
| Floating UI | finder/palette, cheatsheet, branch picker, context menus, prompts, toast stack | native finder query, full keybindings sheet, branch picker and terminal/project context menus; wide/short finder and shortcut captures; dirty prompt shown; other states pending |
| Persistence and shutdown | restore identities/focus/expanded trees; dirty and pending-save exit | native project, editor, three-pane tree, replacement shells and staged worktree restore; Git worker ownership/shutdown unit coverage; other cases pending |

## Evidence locations and limits

Isolated fixture and logs: `/tmp/omaterm-ux-audit/`; config, state and runtime
directories are separate from the user's workspace. Native captures are under
`captures/`. Git task evidence is `.omo/evidence/git-sync-20261008/`; light
tests are `.omo/evidence/light-theme/`.

Initial captures show an unrelated desktop battery notification and therefore
are not final visual approval evidence. They do prove the narrow finder defect
in the unobscured region. Pointer click dispatch did not establish Push
activation; subsequent local-remote testing proved Push activation, delayed
progress, result delivery and the re-enabled control. Retake every
required state after the final source edit, at wide and narrow sizes in both
themes, then run independent read-only review. Do not infer full UI acceptance
from green Rust checks. Subsequent owned-window captures were placed below
the notification and used a per-window compositor opacity override, with no
desktop configuration change. Wide light Files/editor/shortcuts and short
finder/shortcuts captures are available. Native `COLORFGBG=0;15` is confirmed
by bounded terminal read. A focus change during a keyboard scenario invalidated
that scenario; subsequent input must first confirm the owned PID is active.

Current formatting, full workspace tests, Clippy with denied warnings, release
build, documentation links and whitespace checks pass. The final banner palette
edit requires fresh native captures before visual approval. User config now
selects light; installed `/usr/local/bin` executables remain the old build.

Fresh current-build captures include restored light and dark desktop surfaces;
both render the shared project, editor, inspector and terminal palette without
missing or light-only assets. Light remains the user's configured startup theme.

The independent source review identified that the original compact shell made
both sidebars unreachable at 640px and that the toolbar test only checked the
raw width threshold. The current build selects one compact panel at a time,
routes Git/Files field input only when that panel is rendered, and tests the
full toolbar visibility policy. The current release binary passed 288 desktop
unit tests and native compact Git and Projects shortcut captures on 2026-10-09.
Repeating `Ctrl+B` hides the Projects overlay and keeps it hidden after a
subsequent resize back to the wide shell.

Compact sidebar actions now share one state transition for keyboard and
pointer controls. Header affordances reflect the panel that is rendered, not
the remembered wide-shell preference; native captures prove the header's
Projects and Inspector buttons reveal their respective 640px overlays. Hidden
Inspector panes no longer own wheel capture or schedule Info polling.

Native remaining concerns include bounded
context menus, all picker states at short heights, Files/Info overflow, and
complete focus/clipboard/drag regression scenarios. Continue finding and fixing
defects as those surfaces are exercised.
