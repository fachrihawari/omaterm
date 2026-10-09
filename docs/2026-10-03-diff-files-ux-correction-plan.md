# Diff Viewer and Files/Git Interaction Correction Plan

> **Historical plan (largely landed).** The diff/files/Git interaction fixes
> it specifies shipped across v0.2.0–v0.4.0; M15's remaining native interaction
> matrix is tracked in [status.md](status.md). Retained for design history.

**Status:** largely landed; M15 native interaction matrix open.
**Date:** 2026-10-02.

## 1. Objective and authority

Make the diff readable and navigable, make Files scrolling behave like a real
two-axis content viewport, center Git file icons, and remove the unusable Files
shortcut strip. This is the detailed execution plan for the reported regressions
within [R4/R5 of the fidelity correction plan](2026-10-02-ui-v5-fidelity-correction-plan.md).

Architecture follows blueprint §33 (bounded, virtualized diff presentation),
§32 (explicit Git operations), and the existing shared dispatcher. Read
[M15](2026-09-29-15-milestone-15-diff-viewer.md) and [M13](2026-09-29-13-milestone-13-file-tree.md)
before implementation. The frozen `design/ui-v5/reference.html`, component
measurements and browser captures define visual targets. The user's latest
feedback defines the scrolling UX corrections below.

M15 originally deferred Split mode; the existing approved v5 presentation now
includes it. This plan repairs that existing view. Syntax highlighting,
full-revision document loading, and true partial-hunk staging remain separate
capability work. Plain, correctly aligned text must be usable first. Whole-file
Stage/Unstage must never be relabeled as hunk staging.

## 2. Audit: observed defects and source evidence

The supplied screenshot shows line numbers above code, oversized diff rows,
an almost empty right side during a replacement, the wrong body background,
extra navigation chrome, and misaligned Git file marks.

| ID | Confirmed implementation issue | Consequence |
|---|---|---|
| D01 | `split_cell()`/`inline_row()` call `.flex_row()` on the decoration without `.flex()` | Gutter and code stack instead of forming one line |
| D02 | Rows use minimum height, lack explicit code line height and preformatted whitespace | Inflated rows, wrapping, unstable blank lines/indentation |
| D03 | `align_hunk()` emits each deletion then each addition as a separate shared row | Replacement blocks leave excessive opposite-side blank space |
| D04 | `render_diff_preview()` scrolls by hunks; `scroll_preview()` clamps against eight visible hunks | A single long hunk cannot scroll to its bottom; Line deltas are divided by 24 as if pixels |
| D05 | Preview builds up to eight hunks × 200 lines instead of a viewport-sized row range | Large layout tree; caps masquerade as virtualization |
| D06 | Preview background is `#18181b`, not reference `#101318`; marks use a layout-affecting 2px border | Incorrect surface color and code alignment between changed/context rows |
| D07 | Hunk progress/open/copy/refresh strip is always visible | Extra chrome and less code space than reference |
| F01 | Files wheel input rounds to whole rows, forcing a minimum step for nonzero deltas | Subpixel trackpad motion becomes jerky; wheel feel depends on event units |
| F02 | Visible row count uses full window height, not actual tree viewport | Bottom rows can be unreachable; scrollbar range disagrees with visible content |
| F03 | Horizontal offset only translates the label; it swaps basename for full relative path | Icons/indentation stay still and content changes during scrolling |
| F04 | Horizontal thumb is fixed at 48px and range at `MAX_SCROLL_COLS_PX`; vertical drag uses pointer delta / row height | Scrollbar travel does not map to actual content distance |
| F05 | Search, rows, hints/actions and horizontal rail share an approximate body composition | Track heights and remaining row space are unreliable |
| G01 | Git name/path container calls `.flex_col()` without `.flex()`; mark wrapper has no fixed centered height | Filename/path rhythm and mark alignment are not reliably constrained |
| G02 | Generic file fallback is `···` | Text punctuation baseline is mistaken for an icon center |
| H01 | Persistent `Ctrl+P find · ^⇧Y copy · ^⇧U reveal` plus open/copy/reveal strips | Clutter consumes viewport and shortcut notation is hard to use |

Primary files: `apps/omaterm/src/main.rs`, `diff_panel.rs`, `files.rs`,
`git_panel.rs`, and `ui/{theme,metrics,primitives,assets}.rs`.

Do not infer diff data loss from the screenshot alone: verify the raw bounded
diff DTO and selected staged/working-tree side before blaming the parser.

## 3. Settled presentation and interaction contracts

### Diff layout

- Main background `#101318`; file action header 40px; Split side headers 28px.
- Split gutter 54px; Inline gutters 46px + 46px. Explicit 12px monospace,
  22px Split rows / 21px Inline rows according to frozen measurements.
- Gutter and code always share one baseline. Empty code is a real blank row.
- Preserve leading spaces, tabs and Unicode. Default is no soft wrapping;
  long lines are reached horizontally, never truncated destructively.
- Reserve gutter width; do not let line-number digits change column geometry.
  At cap boundaries verify the largest supported number fits.
- Addition fill RGB(46,160,67) at .12, deletion RGB(248,81,73) at .12;
  3px marks overlay the row and consume no content width. Use actual float
  alpha in paint/style where possible, rather than certifying byte-rounded
  constants as exact. No unintended row borders or zebra seams.
- Old/new columns have equal viewport shares with one divider. Headers remain
  fixed; only the code body scrolls. No diff width can push Inspector offscreen.
- Keep the reference action header. Move extra open/copy/refresh actions into
  a contextual menu; hunk navigation uses keyboard/menu affordances rather
  than a permanent extra strip. Real truncation/error notices remain visible.

### Split data semantics

Build separate presentation models:

1. **Inline:** preserve the original unified line order and both line numbers.
2. **Split:** context pairs old/new cells. For each consecutive edit block,
   collect deletions and additions and pair by index up to the larger count.
   Missing-side cells are spacers with identical row height.

Each Split side owns its own text, number and change kind. Never reuse one
`AlignedRow.text` for a paired replacement. Positional pairing is deterministic
presentation, not a claim of semantic/word similarity. Preserve source line
identity and hunk identity for navigation and any future selection.

Keep hunk separators and no-newline/binary/rename/mode-only/truncation states
explicit. This view presents bounded hunks, not fabricated full-file revisions.
Never fill gaps by pretending omitted unchanged lines were loaded.

### Scroll ownership

- A scroll offset is a float pixel distance, not a row/hunk index.
- GPUI Pixel deltas retain their magnitude and fractional part. Line deltas
  convert through a documented line-height factor once; do not divide Lines
  by a pixel constant. Establish sign conventions once for wheel, drag and keys.
- Use a native GPUI scroll owner wherever it satisfies virtualization and both
  axes. Do not run a manual wheel handler and native scrolling on the same axis.
- Prefer native device-delivered trackpad momentum. Do not add synthetic inertia
  or unconditional smoothing that produces drift, lag or double momentum.
- Split vertical scrolling is synchronized by one logical row offset; either
  side controls it. Horizontal offsets are independent per side, clamped to
  each side's measured content width. Inline has its own horizontal offset.
  This synchronization is a deliberate UX improvement over independent browser
  columns and must be documented in captures.
- Hunk navigation reveals the target row; it does not replace free scrolling.
  Switching Split/Inline preserves a source-line/hunk anchor where possible.
- Files horizontal scrolling moves the whole content plane: indentation,
  chevrons, icons, labels and status marks together. Search and panel header stay
  fixed. A filename remains a filename at every scroll offset.
- Hover/selection backgrounds cover the viewport width; hit testing follows the
  transformed content. No sticky icon column or sticky status badge.
- Scroll extent is actual content minus actual viewport. Preserve per-project
  offsets; clamp after resize/filter/collapse/refresh, revealing selected rows
  only for explicit navigation. Background refresh should not jump the view.

### Scrollbars

Use 9px chrome, transparent track, rounded `#343e4b` thumb as the reference target.
Show each axis only when it overflows; reserve its effect on the other axis once.

For a custom thumb, use one shared, tested geometry calculation:

```text
max_offset = max(content_extent - viewport_extent, 0)
thumb_size = clamp(track_extent * viewport_extent / content_extent,
                   minimum_thumb, track_extent)
travel = track_extent - thumb_size
thumb_position = offset / max_offset * travel
drag_offset_delta = pointer_delta / travel * max_offset
```

Guard zero extent/travel. Track click pages toward the pointer; thumb grab keeps
its initial relative grab position. Capture drag at window level, terminate on
release outside the rail, focus loss, hidden panel or shutdown. The thumb reaches
both ends exactly, including when the minimum thumb constraint applies.

### Git rows and Files help

- Git rows stay 40px, with a real flex-column name/path wrapper and explicit
  11px/9px metrics. A fixed icon box centers SVGs and TS/JSON text marks against
  the whole name/path block, including root files with no parent path.
- Replace punctuation fallback with an existing appropriate file SVG; verify
  SVG and text-badge baselines separately. Keep row action/status slots stable.
- Remove persistent shortcut and open/copy/reveal footer rows. Preserve actions
  in a keyboard-accessible context menu and contextual help/tooltips. Show
  readable Linux shortcut notation (`Ctrl+Shift+Y`, etc.), only for actual
  bindings and valid targets. Finder hint footer is a separate surface.
- Help, warnings and paging cannot be mixed into the scrolling row geometry.
  Keep true loading/truncation/watcher notices accessible and separately measured.

## 4. Implementation phases and exit gates

### U0 — Reproduce and prototype the viewport (first)

- Create deterministic fixtures: the screenshot's replacement-style JS diff,
  one 300-line hunk, many small hunks, long Unicode/tabbed lines, and a deep Files
  tree with long names. Include TS/JSON/README/JS Git rows and root-level files.
- Capture current native states plus reference crops at matching logical size,
  scale and font. Record actual body bounds and content extents.
- Prototype GPUI 0.2.2 `ScrollHandle`/`UniformListScrollHandle` and `uniform_list`
  (verified present in locked sources). Prove partial-row offset, shared Split Y,
  horizontal extent, measured viewport and scrollbar wiring with a small specimen.
  If uniform-list horizontal sizing fails, use a measured virtual row viewport
  with a single offset owner; record the concrete blocker before choosing it.
- Exit: reproducible fixtures and selected scrolling design, not guessed layout.

### U1 — Repair immediate row structure and sidebar details

- Enable actual flex on diff decoration/name-path wrappers; fixed row height,
  explicit line height, monospace, whitespace and no wrapping.
- Correct body/fill/mark paint and verify gutter/code alignment on blank lines.
- Center Git marks, replace fallback dots, remove persistent Files hints/actions
  while providing contextual access to the supported operations.
- Exit: native crops show one number and code on the same row, stable spacing,
  centered marks and a clean Files body. No claim of scrolling completion yet.

### U2 — Correct Split row model

- Add pure Split-row pairing without changing Inline order or core/wire DTOs.
- Flatten bounded hunks into presentation rows with source anchors, separator
  rows and truthful end-of-data notices. Rebuild once per data/mode generation,
  not by cloning every hunk and measuring every string every frame.
- Tests first: replacement counts 1:1, 1:3, 3:1; add-only/delete-only; context
  around edit blocks; multiple blocks/hunks; empty text; Unicode; truncation.
- Exit: old/new text and numbering agree with the raw Git fixture; replacement
  rows align and unmatched rows have exactly one opposite-side spacer.

### U3 — Diff scrolling and navigation

- Make headers fixed and code a bounded row-virtualized pixel viewport with a
  small documented overscan. Render work scales with viewport, not hunk count.
- Shared Split Y; separate Split X; Inline X/Y; proportional rails and drag.
- Navigate to hunk/source anchors; preserve offsets on mode switches and benign
  refreshes. Reject stale project/file/side generations as before.
- Exit: reach last row of a single long hunk and every loaded hunk; long lines
  remain readable; wheel, trackpad, keys and drag agree on position.

### U4 — Replace Files scrolling

- Remove manual row-rounded wheel and label-only horizontal implementations.
- Derive row window from measured tree body; use 28px rows with partial-row
  offset, bounded visible range/overscan and actual two-axis extents.
- Measure content width from indentation + icon/label/status boxes and resolved
  text widths. Cache width by row/font generation; update on loading, filtering,
  expansion and UI scale changes. Never use the constant 1600px cap as geometry.
- Reuse the verified viewport/scrollbar mechanics, preserving lazy loading,
  selection, watcher generation guards, file-open routing and input focus.
- Exit: whole-row horizontal movement, smooth pixel trackpad scrolling, natural
  wheel speed, correct thumbs, reachable last row, no jumps on cached refresh.

### U5 — Regression and visual closure

- Run full workspace gates, meaningful pure model/scroll-geometry tests and
  existing diff parser/Git/Files regressions. Timeouts stay failures to complete.
- Run native Wayland interaction and screenshot matrix below. Compare source
  fixtures separately from live-content fixtures; do not compare different files
  and call the pixel mismatch a rendering bug.
- Record commands, captures, viewport/font/scale, remaining exceptions and next
  actions in `docs/status.md`. Check each phase off only after its exit evidence.

## 5. Code boundaries

| Area | Intended ownership |
|---|---|
| `diff_panel.rs` | Pure Inline/Split rows, source anchors, selection/mode state |
| `files.rs`, `git_panel.rs` | Existing cached rows, selection and refresh state |
| `ui/diff_view.rs` (new when extracted) | Diff chrome, row painting and viewport |
| `ui/file_tree.rs` (new when extracted) | Files layout, row hit testing and viewport |
| `ui/scroll.rs` (if needed) | Shared scrollbar geometry/native-handle adapter |
| `ui/metrics.rs`, `theme.rs`, `assets.rs` | Explicit measured roles/paint/icons |
| `main.rs` | Coordinate callbacks to existing shared commands and workers |

Keep extraction incremental. No new crate, GPUI in core, render-time filesystem/
Git calls, session recreation, persistence schema or IPC change is required.
Scroll state is view-local; caps still bound incoming data before allocation.

## 6. Required acceptance matrix

| Scenario | Observable pass condition |
|---|---|
| Screenshot JS replacement, Split/Inline | Gutters/code inline, 22/21px pitch; replacements correctly paired in Split |
| One long hunk; >8 hunks | Last loaded row reachable without hunk swapping; no hidden data behind caps |
| Long lines, tabs, leading spaces, Unicode, empty lines | Preserved content; horizontal reach; no wrapping or extra-height rows |
| Binary, rename, deleted/added, mode-only, empty/error/loading | Truthful explicit state; no fake code or enabled invalid mutation |
| Same path partly staged and unstaged | Correct comparison and action target for the selected group |
| Split wheel on either side; Inline switch | Shared Y, independent X; anchor retained; headers do not scroll away |
| Mouse wheel + trackpad small deltas/momentum | Fractional movement retained; no forced per-event row jump or long lag |
| Horizontal Files scroll including Shift+wheel | Chevron/icon/name/status move together; text identity never changes; one axis at a time for Shift+wheel |
| Files short/long/deep/filter/collapse/refresh | Real extents; no rail when unnecessary; last row reachable; clamped stable offsets |
| Thumb minimum-size, track click, release outside | Exact end travel, correct page/drag mapping, no stuck drag |
| Files keyboard/context help | No permanent hint strip; actual actions discoverable and usable with valid targets |
| Git TS/JSON/README/JS rows, root and nested | Icon center aligns to 40px row; name/path metrics consistent; no fallback dots |
| Width/scale: 1440×900, 1024×768, 800×600; Inspector 280/330/470; scale 1×/2× where available | No Inspector displacement, correct body bounds, legible text and rails |
| Large bounded data set | Visible-row-limited render count; measurements cached; capture scroll/frame timings without inventing a performance pass |

Required commands after implementation:

```bash
cargo fmt --all --check
cargo test --workspace -- --test-threads=1
cargo clippy --workspace --all-targets -- -D warnings
python3 scripts/check-docs.py
git diff --check
```

Automated tests do not substitute for scrolling and screenshot acceptance.
Missing trackpad/scale hardware validation is recorded as pending, not passed.

## 7. First next action

Start U0 with a one-long-hunk and a long-name tree specimen; prove the GPUI
viewport contract. Deliver U1 next, then U2/U3 for a usable diff, U4 for Files,
and U5 for closure. Avoid another broad cosmetic pass without per-phase evidence.
