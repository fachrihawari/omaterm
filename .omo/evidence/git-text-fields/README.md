# Git text field implementation evidence

Scope: `apps/omaterm/src/git_panel.rs`, new `git_input.rs`, `git_input_tests.rs`, `git_input_view.rs`, and commit/stash state, keyboard routing, render, and submit-extraction regions in `main.rs`. Existing unrelated shared-tree work preserved. Commit remains single-line, Enter submits per `docs/14-milestone-14-git-status.md` and the pre-existing panel contract.

## Verified behavior

Invocation A: `cargo test -p omaterm --bin omaterm-desktop git_input`.
Artifact: `tests.log`. Binary observable: process exit 0, five named tests passed.

| Criterion / exact scenario | Binary observable |
|---|---|
| Middle editing of `a👨‍👩‍👧‍👦éz`: Left, Backspace, ShiftLeft, replacement, Delete | `middle_edit_and_shift_selection_replace_whole_graphemes` passes; accent cluster and family emoji removed atomically, suffix preserved |
| Select `world` in `hello world`, collapse left, extend, cut, select all and replace | `selection_collapses_toward_motion_and_cut_deletes_only_selection` passes |
| Append three-byte CJK with only one byte remaining; replace selection with multiline/control paste; bound long CJK paste | `byte_cap_preserves_graphemes_and_replacement_capacity` passes; length stays <=4096, input remains valid UTF-8 and single-line |
| Delete empty draft, append combining accent, Backspace | `empty_deletion_and_inserting_combining_marks_keep_valid_caret` passes |
| Ctrl+A/C/X/V, Ctrl+Shift+C/V, ShiftLeft, Delete, Home/End versus Ctrl+P/Ctrl+Shift+G/Alt+1/Alt+Shift+K | `fields_capture_clipboard_editing_but_preserve_global_shortcuts` passes |

Invocation B: `cargo test -p omaterm --bin omaterm-desktop git_panel`.
Artifact: `panel-tests.log`. Binary observable: process exit 0, seven tests passed including commit draft take/restore and status worker real-repository scenario.

Invocation C: `git diff --check`.
Artifact: `diff-check.log`. Binary observable: process exit 0.

Source review and LOC measurement: `source-review.txt`. New state and adapter each below 200 nonblank/non-comment lines. Touched legacy modules lose scoped lines by extracting editing behavior. Single-responsibility split: pure draft editing versus GPUI interaction. No new dependency, unsafe, numeric casts, logging boundary, or production unwrap/expect.

## Pending final integration evidence

Root owns fresh release rebuild, full workspace/fmt/clippy gates, and native Wayland captures/interaction. This report does not claim native success. Required cases: both light/dark fields with caret and selection; long drafts remain bounded; native clipboard copy/cut/paste; click stash then commit and type to prove mutual focus; click Commit/Stash controls after editing; Escape/Tab releases input to visible surface.

Initial scoped compile in `tests-initial-failed.log` failed because concurrently introduced terminal renderer tests lacked imports; root fixed imports and the complete scoped invocations above were rerun. `fmt-initial.log` is an initial formatting diagnostic, not a passing gate.
