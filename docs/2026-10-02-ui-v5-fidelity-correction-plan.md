# UI v5 Fidelity Correction Plan

> **Historical plan (landed, superseded direction).** Retained for the audited
> discrepancy register and design history; the shared design contract lives in
> [DESIGN.md](../DESIGN.md).

> Baseline: `8cade1b`. Objective: correct the committed native UI to match
> `omaterm_mock_ui_v5.html`, particularly both sidebars, icons, typography,
> spacing, colors, and interaction states. This is the next execution plan.

## 1. Completion reset and required result

The previous implementation is an **approximate shell**, not an accepted
pixel-perfect reproduction. The earlier assertion that all phases were
finished was incorrect. Passing Rust tests does not certify visual fidelity.

The [original exact-design plan](2026-10-02-ui-v5-pixel-perfect-plan.md) remains the
design specification. This document identifies what the implementation
actually missed and orders the fixes. It supersedes prior completion claims
for UI fidelity, without invalidating recorded functional test results.

Audit conditions: working tree clean at start; code inspected at `8cade1b`;
reference is the HTML supplied in the conversation. No new desktop/browser
screenshots were taken during this planning pass. Findings below are
code-confirmed differences or explicitly marked measurement tasks.

Required outcome:

- Same viewport, scale, fonts, content and interaction state produce matching
  native/reference sidebar, header, pane and overlay templates.
- Exact Lucide paths, icon boxes, control order, transparent borders, radii,
  font sizes/weights, line heights, spacing, alpha, hover and motion apply.
- Production uses real state; reference fixtures use deterministic source
  strings/counts. A fixture is not proof that telemetry or an editor works.
- Unresolved differences remain open. Recording a "known delta" does not
  approve a substitution or justify marking the phase complete.

## 2. Why the current implementation diverged

1. **Reference measurement was skipped.** `reference.html`, resolved CSS,
   computed-style output, font manifest and browser goldens remain absent.
   `design/ui-v5/extract-computed.py` checks file presence; it does not extract
   browser styles or verify the stored hash against actual file contents.
2. **Tokens were added without enforcing component metrics.** Much of both
   sidebars inherits GPUI defaults instead of the HTML's 9/10/11/12px scale,
   line height, weight and tracking. Root text color is not explicitly set
   to `#d7dde5`.
3. **Tailwind classes were replaced with similar-looking GPUI helpers.**
   Generic padding, radius, shadow and border helpers replaced measured
   values. Their actual metrics must be checked against GPUI 0.2.2.
4. **Vendoring icons was mistaken for matching every icon slot.** There are
   31 assets, but Projects still renders a text `+`, Git uses `···` for many
   files, Files/Quick Open still use Nerd Font glyphs, and some source
   controls are absent. Parent layout and alignment also affect icons.
5. **Focus was substituted for hover.** The pane toolbar remains visible on
   the focused pane; Git actions have no source hover fade.
6. **Geometry tests describe the intended layout, not rendered bounds.**
   `shell_rects()` does not prove the nested flex tree has those bounds.
   Files receives full window height for virtualization; leaf grid sizing
   ignores inside borders/padding; notices change actual canvas height.
7. **Capability gaps were called finished.** Info process/port lists, status
   telemetry/bell, multiline editing, editor presentation and several menus
   remain missing or placeholders. M15's outstanding contract is still open.

## 3. Code-confirmed discrepancy register

Priority: **P0** affects the whole UI or invalidates fidelity checks;
**P1** directly affects the reported sidebars/icons; **P2** completes adjacent
surfaces. Locations are function names in `apps/omaterm/src/main.rs` unless
otherwise noted. Baseline line numbers are evidence, not stable APIs.

| ID | Priority | Current evidence | Required correction |
|---|---|---|---|
| REF-01 | P0 | No checked-in reference/captures; extraction script is a presence check | Archive supplied source, freeze dependencies/fonts, measure bounds and capture browser goldens |
| TYPE-01 | P0 | Projects, header, Info, Inspector labels and status lack explicit type metrics | Exact per-role sizes/line heights/weights/tracking; UI font independent of terminal config |
| STYLE-01 | P0 | `.p(6)`, `.rounded_sm/md`, `.shadow_lg`, solid accents substitute for source styles | Measured shared primitives with explicit pixels/alpha/shadow |
| COLOR-01 | P0 | `diff_row_decor()` passes `0xRRGGBBAA` constants to `rgb()` | Alpha-preserving conversion and exact compositing |
| GEO-01 | P0 | Actual nested flex bounds not compared with `shell_rects()` | Verify rendered root/panel/header/body/status rectangles |
| LEFT-01 | P1 | Selected card adds `change ×` row (6145–6188) | Two-row card only; existing actions move to out-of-flow context menu |
| LEFT-02 | P1 | Projects header `.child("+")` (6262) | Exact 14px `folder-plus` SVG with source command hit box |
| LEFT-03 | P1 | Generic radius; inactive `BG` border; no hover/glow/name weight difference | 8px card radius, transparent inactive border, hover, selected-dot glow, active500/inactive400 name |
| LEFT-04 | P1 | Project list uses `overflow_hidden()` | Independent vertical scrolling; fixed header/footer |
| LEFT-05 | P1 | Open Project uses 4px gaps/padding and long inherited-size badge | 8px padding/gaps,11px label,9px badge, exact keyboard-badge borders/shadow |
| LEFT-06 | P1 | Header/footer open buttons call default `create_project()` | Real folder-open flow through shared operations and correct shortcut label |
| RIGHT-01 | P1 | Inspector labels/badge inherit type; trailing menu missing | 11px tabs,9px badge,14px icons, exact underline and right-pushed ellipsis |
| RIGHT-02 | P1 | Info labels/card text inherit defaults; generic radius | Explicit 10px tracked headings,12px name/10px path/9px pill,8px cards |
| RIGHT-03 | P1 | Info Processes/Ports are placeholder paragraphs | Actual count headings and process/port row templates with M18 scoped data |
| RIGHT-04 | P1 | Info clips body; Git lacks independent scrolling/footer anchoring | Scrollable bodies; fixed tab row, commit region and Git footer |
| ICON-01 | P1 | Missing SVG slots; `···`/Nerd marks replace source file icons | Explicit source slot-to-asset table; source file-text, TS/JSON marks, menu/status icons |
| ICON-02 | P1 | Vendored release pinned after an unfrozen `lucide@latest` reference | Verify generated reference SVGs; no unexplained path/alias substitutions |
| FILE-01 | P1 | Every file has extra14px spacer; all rows use8px gaps | Directory6px gaps; exact nested file offsets; top-level spacers only where present |
| FILE-02 | P1 | Text badges lack600 weight; selected text becomes white; generic glyph fallback | Source TS/JSON styling, inherited selected text, README16px blue file-text |
| FILE-03 | P1 | Full viewport height passed to Files tree (6896) | Visible rows from actual body minus search/footer/paging areas |
| FILE-04 | P1 | Search is end-appending text Div without caret/selection/paste | Proper native text input; exact32px layout and inset focus stroke |
| FILE-05 | P1 | Extra action/hint rows and non-reference scrollbars | Auxiliary actions in menus; source scrollbar geometry and paint |
| GIT-01 | P1 | Menus/dropdown absent; uniform `gap_3` replaces mixed margins | Restore control slots and exact12px/8px vertical rhythm |
| GIT-02 | P1 | Commit remains single-line/end-appending | Multiline caret/selection/paste,56px minimum and exact button/dropdown geometry |
| GIT-03 | P1 | Actions always visible; no row hover; label/path wrapper lacks `.flex()` | Real two-line column; reserved action width;100ms fade; source hover |
| GIT-04 | P1 | Group menus/Changes seam/margin/HEAD missing | Exact32px groups, count/action layout, fixed upstream+short-HEAD footer |
| TAB-01 | P2 | No source minima; panel bg inactive;2px accent; no horizontal scroll | Source154/130/185px minima, transparent inactive bg,inset1px accent, overflow |
| TAB-02 | P2 | Add button before diff; compare icon missing; `Diff:` label | Compare14px green, `Diff · name`, source ordering |
| TAB-03 | P2 | Terminal and diff both active; terminal click deletes preview | Preview existence separate from activation; exactly one active chip |
| PANE-01 | P2 | Toolbar y39/focus-gated; no shadow/transition | y7/right8; hover-only120ms slide/fade; correct toolbar actions |
| PANE-02 | P2 | Solid full border; no body padding; all panes have footer | Inset alpha.34 focus, source seams/padding, primary-fixture footer only |
| PANE-03 | P2 | Heights31/27; global60px subtraction; columns ignore borders; notices consume height | Verified border-box32/28; per-leaf canvas/PTY/input rectangles |
| DIFF-01 | P2 | Old bg; bad color conversion;2px layout mark; decor wrapper lacks `.flex()` | `#101318`, translucent3px inset marks, correct row layout |
| DIFF-02 | P2 | Inline54+54 gutters rather than measured46+46; code wraps; extra controls | Correct gutters/line heights, nowrap/horizontal scroll, source chrome |
| STATUS-01 | P2 | Session count labeled processes; history/font replace ports/CPU/MEM/bell | Source order/type/icons with real scoped counts/telemetry |
| TOAST-01 | P2 | Centered in main rather than window; wrong radius/shadow; no transition | Root center,bottom38,radius8, exact shadow/180ms transition/1400ms hold |

Editor/syntax remain capability dependencies for full-reference completion.
They do not block correcting the existing sidebars and controls.

## 4. R0 — Rebuild the reference and acceptance harness

### Deliverables

1. Materialize the HTML already supplied in the conversation as
   `design/ui-v5/reference.html`. Do not require the user to re-supply available
   text. Hash the archived transcription; if original file bytes later differ,
   record that comparison rather than claiming byte identity.
   **Done 2026-10-02:** archived from `/home/fachri/Downloads/omaterm_mock_ui_v5.html`
   (identical twin at `/home/fachri/omaterm_mock_ui_v5.html`), SHA-256
   `cad67d42a421a29a5d118aa5944c2c0e6dc3046baa25be06e2b8b61edb1b1ad5`.
2. Keep the original immutable and make an offline capture copy. Pin Tailwind/
   Lucide payloads, generated SVGs, selected fonts/revisions, browser build,
   scrollbar behavior, viewport and DPR.
   **Done 2026-10-02:** `capture.html` + `vendor/tailwind-cdn.js` +
   `vendor/lucide-1.50.0.min.js`; `lucide-version.json` records the
   latest-vs-vendored skew. All 31 vendored 1.49.0 icons are shape-identical
   to the 1.50.0 render (`icon-diff.json`); only `open-in-new` is genuinely
   absent (browser renders an empty slot).
3. Replace the extraction stub with actual browser automation/DevTools
   extraction: `getComputedStyle`, `getBoundingClientRect`, pseudo-element
   underline, selected/hover/focus styles, resolved font evidence and SVG DOM.
   Recompute SHA-256, rather than merely checking a non-null manifest field.
4. Capture 1440×900/DPR 1 first; add 1024×768, 800×600, panel minima/maxima/hidden
   states and DPR2 after the baseline passes.
5. Add deterministic fixture data to **the production components**: source
   project/tab/file labels, counts, mixed three-pane geometry and selected states.
   No separate hand-drawn fixture renderer that bypasses production bugs.
6. Native capture must record actual window bounds, compositor scale/output,
   visibility and build revision. Do not reuse stale `grim -g` coordinates or
   full-desktop captures containing unrelated windows and notifications.
7. Emit read-only native component bounds and compare them with the browser
   manifest before judging rasterized text and SVGs.

### Cascade/scaling rules

- `.cmd` follows generated Tailwind utilities. Measure its final padding;
  expected 5px vertical/7px horizontal, 1px transparent border, 7px radius.
  `.p(6)` and a small radius helper do not establish equivalence.
- Use explicit logical pixels after freezing the source 16px root. Check GPUI
  helper/rem values; similarly named helpers are not a cross-framework contract.
- Browser CSS px maps to GPUI logical px; physical pixels scale with DPR/
  compositor. A 1.25× capture of a 210px logical sidebar is not a width defect.
- `.terminal-glow` replaces the grid image; opaque panes may hide it. Paint
  the measured visible result rather than a new decorative grid/glow.
- Measure Preflight effects on `pre`, button, input and textarea font/metrics.
  Match the actually resolved font; the HTML never loads Inter/JetBrains files.

**Exit:** source archived, computed styles populated, font/icon provenance
verified, repeatable native/browser capture pairs exist. The original P0/P1
visual gates remain incomplete until these artifacts exist.

## 5. R1 — Shared typography, exact primitives and icon slots

Create desktop `ui/metrics.rs` and `ui/primitives.rs` beside theme/assets/
geometry. Apply them during small presentation extractions; retain the
application coordinator, semantic dispatcher and session registry.

### Explicit type metrics

| Role | Size | Weight/line-height/tracking |
|---|---:|---|
| Root | reference-resolved UI face | text `#d7dde5`; explicit line height |
| Projects heading | 10px |400, normally15px,1.2px tracking,uppercase |
| Project name | 12px | active500/inactive400,normally18px |
| Project branch/path | 10px/9px | muted,normally15/13.5px |
| Open Project label/badge | 11px/9px | text2/muted,normally16.5/13.5px |
| Inspector tabs/count | 11px/9px | normally16.5/13.5px |
| Info heading | 10px | uppercase,1.2px tracking,normally15px |
| Info name/path/branch | 12px/10px/9px | normally18/15/13.5px |
| Process/port/file/Git label | 11px | normally16.5px |
| Git path/status | 9px/10px | normally13.5/15px |
| TS/JSON mark |11px Files,10px Git |600 |
| Git group heading |10px |uppercase,1px tracking,normally15px |
| Pane/status metadata |10px |normally15px |

"Normally" values derive from CSS1.5; R0 measured values take precedence.
Apply metrics at label level. Match weight/tracking, not only uppercase text.
UI text must not inherit terminal font configuration.

### Shared primitives

- `cmd_button`: measured icon box/padding, transparent border,7px radius,
  hover `#182029/#27313d/white`,120ms ease; source pressed state where used.
- `pill`: bg `#151a21`,border1px `#2c3643`,per-slot padding/radius/text.
- `kbd`: bg `#161b22`,border `#313b48`,bottom `#1d232b`,inset white/.03,
  radius4px,padding6×2px.
- `row`: measured size/gaps/baseline; independent selected/hover/focus paint
  without reflow.
- `icon`: exact12/14/16px box,no flex shrink,explicit tint/baseline alignment.
  Hover foreground must recolor the SVG: fixed child tint cannot stay muted
  while the source `.cmd:hover` makes all content white.
- `focus_inset`/`diff_mark`: non-layout overlays; consume zero content space.
- `shadow`: exact offset/blur/spread/alpha rather than `shadow_lg`.

GPUI0.2.2 exposes arbitrary `corner_radii`, `border_widths`, color fields and
canvas painting. Absence of a shortcut method is not proof that7px rounding,
3px marks or inset strokes are impossible. Verify locked APIs with a specimen
before declaring an actual framework blocker.

### Immediate color bug

`gpui::rgb()` reads the low three bytes and returns alpha 1. Consequently,
`rgb(0x2EA0431F)` produces opaque `#a0431f`, and `rgb(0xF851491F)` produces
opaque `#51491f`, instead of translucent green/red. Correct `diff_row_decor()`
and audit all RGBA constants passed to RGB helpers.

Prefer typed RGB/alpha colors with exact float alpha rather than encoding .12
as 8-bit 0x1f (.12157). Test conversion/blending behavior, not duplicate constants.

### Icon correction map

| Slot | Required rendering |
|---|---|
| Projects top/bottom open |folder-plus14px,muted/text2 respectively |
| Projects hide/reveal |panel-left-close/panel-left16px |
| Inspector toggle |panel-right16px,pressed white/inactive muted |
| Inspector Info/Files/Git |info/folder/git-branch14px |
| Inspector/Git group/menu |ellipsis14px,measured source hit box |
| Info project |folder-git-2 16px blue within28px box |
| Info ports/links |globe-2/external-link14px |
| Files directory |chevron14px plus folder/open16px yellow |
| Files README |file-text16px blue; no Nerd substitute |
| Files/Git TS and JSON |source text marks,600 weight,not replacement SVGs |
| Git refresh/stage/unstage/discard |refresh-cw/plus/minus/undo-2 14px |
| Commit dropdown |chevron-down14px inside32×32px |
| Terminal toolbar |columns-2/rows-2/rotate-cw/ellipsis or trash-2,14px |
| Diff chip/header |git-compare-arrows14/16px green |
| Global status |git-branch/radio/bell12px |

`open-in-new` is unresolved in the pinned Lucide release. Compare the frozen
reference's actual generated result. Any alias to external-link must be an
explicit exception, not silently certified as the exact original icon.

**Exit:** primitive specimens match browser crops for text, cmd/pill/kbd,
selected card,focus stroke,Git row and icon sizes. Vendored assets alone are
not this gate.

## 6. R2 — Left Projects sidebar

### Exact layout

- Width 210px including right border, min 164/max 340, body H−24.
- Header 42px, padding 8px, bottom border 1px; hide icon 16px, PROJECTS after
  8px, right-pushed folder-plus 14px. Remove the remaining text `+`.
- List padding 8px, gap 4px, vertical auto-scroll. Cards cannot shrink to fit a
  crowded list; header and bottom Open Project remain fixed.
- Card radius 8px, border 1px, padding 10px; first row dot 8px/gap 8px/name
  12px/branch 10px pushed right; second row path 9px, left 16px, top 4px.
- Active background `#18202a`, border2, name weight 500, dot glow 0/0/10px
  green/.24. Inactive transparent border/background, name weight 400; hover
  `#171d25`. Transparent borders reserve 1px without painting a dark rectangle.
- Remove `change ×` from selected card. Both actions remain in a keyboard-
  accessible context menu outside the idle layout. Selection cannot increase
  card height.
- Footer border 1px, padding 8px; button 32px, radius 6px, border2, padding 8px,
  gap 8px; icon 14px, label 11px, badge 9px. At 210px the fixture cannot truncate
  Open Project to "Open P".

### Behavior

- Both Open Project controls open a real directory picker and dispatch the
  existing selected-directory operation. Cancellation changes nothing;
  portal unavailable becomes a genuine error state. Default New Project
  stays separately accessible.
- Fixture shortcut label is the source `⌘O`; native Linux label reflects an
  actual tested binding in the same 9px template. Any content delta remains
  explicit; it cannot justify a large badge that crowds the label out.
- Shorten home paths by components (`Path::strip_prefix`), not text prefix:
  `/home/user-other` must not be abbreviated from `/home/user`.
- Cache Git metadata by actual project/root. Unknown inactive-project data
  is not proof of clean/no-branch state.
- Modifier project-jump hints are an additional transient capture, not a
  change to the idle fixture.

**Exit:** five-project fixture matches at 210px; 164/340 variants match source
ellipsis/overflow; list scrolls independently; selection/hover preserve card
height; picker/context actions work.

## 7. R3 — Right Inspector frame and Info

### Frame/header

- Width 330px, min 280/max 470, inside left 1px border; starts at y=42.
- Header 40px, padding 4px; Info/Files/Git order; labels 11px, icons 14px,
  icon-label gap 6px, horizontal tab padding 12px.
- Active white, underline 2px/left-right inset 9px/bottom 0/blue/full radius.
  Git badge 9px, padding 6px, left margin 4px.
- Restore trailing 14px ellipsis, auto-right, bottom margin 4px; real menu with
  supported refresh/copy-root/panel actions, separately captured when open.
- Fixed headers/footers resist flex shrink. Do not fix clipping by shrinking
  icons/panels or adding automatic collapse absent from the source.
- Resize tracks/cancels at window level: leaving the 4px strip/releasing
  elsewhere cannot leave a stuck drag. Transparent at rest, blue2 on hover,
  column-resize cursor; current drag-only highlight is not equivalent.

### Info

- Independently scrollable, padding 12px, 16px between sections.
- Headings 10px with tracking, 8px before cards.
- Project card radius 8px, border 1px, background `#141920`, padding 12px.
- Folder box 28px, radius 6px, border2, background `#10151a`, 16px blue icon.
- Name 12px, path 10px, branch 9px; no empty branch pill.
- Process/Port headings have count pills; radius 8px/border 1px groups;
  row text 11px, padding 12px horizontal/10px vertical, 1px interior seams.
- Process row: 6px live dot +8px margin, name, PID after 8px, metrics auto-right.
- Port row: 14px blue globe +8px margin, port, muted owner after 8px,
  external-link 14px where appropriate.

M18 supplies real scoped process ownership/lifecycle, off-thread CPU/RSS and
port attribution. Preserve its focused-pane contract; project aggregation
requires a deliberate domain-contract change. Fixture data exercises the same
templates; production must use real data or genuine empty/error states.
Milestone-placeholder paragraphs cannot satisfy the Info design gate.

**Exit:** frame/header/project card match, all Inspector bodies scroll;
full Info remains pending until real count/process/port/metric/link flows
have release Wayland evidence.

## 8. R4 — Files and Git detail correction

### Files

1. Proper keyboard-owned caret, selection, navigation, paste, delete and escape
   input; bounded filtering and stale-project guards remain.
2. Wrapper padding 8px, bottom border 1px; input 32px/radius 6px/background
   `#151a21`, padding 8px; search 14px, text 11px with its own 8px horizontal
   padding; inset blue2 focus.
3. Map source row structure literally: directory gap 6px/chevron 14px/folder
   16px; nested blocks indent 16px. Nested file rows gap 8px without the added
   marker spacer. Top-level JSON/README have the source-specific 14px spacer.
4. Rows 28px, body vertical padding 4px, source hover/selection; remove rounded
   corners entirely (measured tree-row radius is 0). TS/JSON weight 600/colors
   and README 16px blue file-text.
5. Visible range derives from actual body after header/search/borders/paging;
   bounded rows plus overscan; retain lazy cache/watcher generation guards.
6. Match frozen scrollbar renderer: WebKit branch 9px, thumb `#343e4b`,
   transparent track/full radius. Use actual content/viewport; minimum thumb height must
   not break its travel-range calculation.
7. Move persistent open/copy/reveal/hint rows to contextual menus/help.
   Truncation/watcher errors are separately captured native states.

### Git

1. Header padding 12px, source rhythm branch →12px→ draft →8px→ button; current
   uniform `gap_3` is wrong between draft and Commit.
2. Branch icon 16px purple, label 11px, sync pill 9px, refresh/ellipsis 14px.
3. Draft 11px, minimum 56px, padding 8px, radius 6px; multiline Unicode caret/
   selection/paste. Preserve drafts per project/on failure; enforce byte cap
   before multibyte insertion.
4. Commit 32px/11px/weight 500, blue2 → hover `#3d8bf8`; dropdown 32×32px
   with 14px chevron, gap 8px; menu options backed by real supported behavior.
5. Groups 32px, 10px tracked uppercase, chevron 14px/count 9px; staged minus/
   ellipsis; Changes plus/undo/ellipsis. Changes includes untracked, top 1px
   seam +4px margin.
   Keep count/action slots when empty.
6. Rows 40px, padding 12px, gap 8px; name 11px/path 9px in a real
   `.flex().flex_col()` wrapper; replace `···` fallback with consistent file marks.
7. Reserve measured action width; opacity 0 at rest, hover/focus →1 over 100ms
   ease; hidden pointer targets disabled without reflow; row hover `#171d25`.
8. Selection identity is `(project, group, path)`, not path alone. Partly staged
   files must open the correct side through mouse and keyboard.
9. Keep centralized Git operations/confirmation policy; armed states are
   separate captures. Bulk targets/root are captured and revalidated.
10. Footer fixed at bottom, border 1px, padding 12px, 10px muted, upstream left/
    real short HEAD right. Add bounded shared query/result support for HEAD,
    not render-time Git.

**Exit:** Files/Git default/hover/focus captures match; real input, scrolling,
Git actions and staged/working-tree targeting pass live validation.

## 9. R5 — Adjacent header, panes, diff, status and overlays

After the reported sidebars pass:

- Tabs: 12px labels, source 154/130/185px minima, transparent inactive
  background, correct icons/status; inset top 1px blue/.9, no 2px layout border.
  Add button after preview chips; compare 14px green and `Diff · filename`;
  horizontal scrolling.
- Preview exists independently of active surface; exactly one active chip;
  terminal selection preserves inactive preview.
- Panes: stable reference presentation roles (primary fixture footer only)
  with explicit live policy. Footer presence cannot follow hover/focus, which
  would repeatedly resize shells.
- Header 32px/footer 28px include borders. Verify border-box sizing before
  using 31+1/27+1. One inner rectangle drives canvas, PTY sizes and mouse origins.
- Toolbar top 7px/right 8px, hover-only 120ms ease fade/−3px slide, exact container
  border/radius/shadow. Hovered and focused pane are different state. Missing
  Restart/overflow semantics remain open shared-operation dependencies.
- Inset focus 1px/.34, structural seams once, body padding 20px horizontal/16–20px
  vertical; preserve terminal selection/cursor/grid correctness.
- Diff: fix alpha, 3px non-layout marks, background `#101318`; Split gutter
  54px, Inline 46+46px; `.flex()` wrappers, 22/21px rows, whitespace/nowrap/
  horizontal scroll. Move extra navigation/actions out of default source chrome.
- Reconcile M15 before further extension. Tests for basic numbering do not
  certify replacement pairing, full revisions, untracked content, syntax or
  partial-hunk staging. Unsatisfied milestone criteria remain open.
- Status: 10px text, 12px icons, padding 10px/gap 6px, source order/dark background. Registry
  session count is not selected-context process count. Real ports/CPU/RSS/bell
  required; history/font access moves to menus.
- Toast: root-window center, bottom 38px, radius 8px, 0/15/40px shadow black/.35,
  180ms ease, 1400ms hold reset per message. No callbacks alone does not prove
  pointer transparency; validate hit behavior.
- Hover foreground/tooltips/clock phases follow source; idle animation tasks
  end when hidden/settled. Notices must not silently invalidate canvas geometry.

Editor/syntax/unavailable capability slots remain on their owning roadmap
slices. Do not substitute a static editor for real document functionality.

## 10. Delivery order and code boundaries

Keep session ownership, recursive tree, dispatcher, PTY parsing, async workers,
CLI/IPC scopes and logical snapshots intact. Extract actual components rather
than starting another multi-thousand-line coordinator rewrite.

| Delivery | Main files | Required exit evidence |
|---|---|---|
| R0 reference/capture | `design/ui-v5/*`, capture tooling, fixture | Real manifests and repeatable native/browser pairs |
| R1 exact primitives | `ui/{metrics,primitives,theme,assets}.rs` | Matching specimens,color conversion regression |
| R2 Projects | `ui/projects.rs`,current handlers | Sidebar idle/selected/hover/scroll/width captures |
| R3 Inspector/Info template | `ui/{inspector,info}.rs` | Header/card matches; M18 data gate explicitly pending |
| R4 Files/Git | `ui/{files,git}.rs`,current panel models | Panel images and input/Git workflows |
| R5 adjacent surfaces | `ui/{tabs,terminal_pane,diff,status,overlays}.rs`,geometry | Surface state matrix and live regression |
| R6 capabilities | owning M15/M18/editor docs and shared operations | Actual Info/status/editor dependencies satisfied |
| R7 acceptance | captures/reports/status/matrix/dependencies | All required evidence plus user visual review |

Only add modules when extracting a concrete component. Render from immutable
presentation data and target-capturing listeners; no filesystem/Git/process
inspection during layout or paint.

Each reviewable change closes named discrepancy IDs with before/after evidence.
Do not automatically commit/push correction work without user instruction.

## 11. Acceptance matrix and quality checks

### Immediate sidebar review states

| Capture | Data/state |
|---|---|
| C01 | 1440×900 default: five projects, dev mixed three panes, Info three processes/four ports |
| C02 | Left 210px; each project selected; inactive/active hover |
| C03 | Left 164/340px, crowded scroll list, fixed header/footer |
| C04 | Right 330px Info/Files/Git header crops, glyph/underline coordinates |
| C05 | Right 280/470px, long names/paths, overflow, badges/control boundaries |
| C06 | Files source nesting/selection/README/TS/JSON decorations |
| C07 | Files input focus/caret/selection/paste and scrollbar offsets |
| C08 | Git staged two/Changes three, modified/added/untracked hover and hover-exit |
| C09 | Git multiline input, Commit/dropdown/menu, collapsed groups |
| C10 | Each sidebar hidden/restored/resized, cursor/drag release outside strip |

Then run original S01–S22 for tabs, pane hover/focus, both diff modes, status,
toast and editor when available. Native error/confirmation states have their
own captures and functional checks.

### Pass rules

1. Same fixture data, logical viewport, font resolution, scroll state, pointer and
   clock phase; fixture provenance distinct from live functional evidence.
2. Actual bounds agree with measured reference to ≤0.5 logical px at DPR 1;
   seams on identical physical pixels; geometry tests alone are insufficient.
3. Exact RGB/float alpha/radii/shadow/font metrics/icon paths in manifest;
   missing control, font substitution, changed row height/count, clipped essential
   label or wrong icon order fails.
4. PNG pairs, 50% overlay, absolute-difference heatmap, enlarged icon/text/seam
   crops. Review narrow renderer-edge differences; no broad masking.
5. Screenshot review of R2 and R3/R4 before sidebar completion; fix deviations
   before proceeding with adjacent polish.
6. Release Wayland: actual folder picker, context actions, input ownership,
   resize/scroll, Git target selection and session/PID survival on an isolated
   instance. Unavailable platform/input methods remain pending.

Implementation checks:

```bash
cargo fmt --all --check
cargo test --workspace
cargo clippy --workspace --all-targets -- -D warnings
cargo build --release --bin omaterm --bin omaterm-desktop
python3 scripts/check-docs.py
git diff --check
```

If default concurrency stalls, record failure and diagnose using
`cargo test --workspace -- --test-threads=1`; report distinct results.
Test meaningful geometry/focus/input/color/worker changes; literal style
constants alone do not need self-repeating unit tests.

## 12. Current status and first implementation action

| Area | Fidelity status |
|---|---|
| Reference freeze/capture | incomplete |
| Shared typography/metrics/controls | incomplete |
| Left Projects | mismatches confirmed; correction pending |
| Right Inspector/Info | mismatches confirmed; data dependency pending |
| Files/Git | approximate implementation; correction pending |
| Header/panes/diff/status/toast | approximate implementation; correction pending |
| Editor/syntax/full telemetry | capabilities pending |
| Full visual/live acceptance | not passed |

**First implementation action:** R0 archive/extract/capture, then R1 primitives.
**First visible correction:** R2 Projects, immediately followed by R3 Inspector
header/card and R4 Files/Git. Palette fixes alone do not satisfy the feedback.

## References

- [Original design specification](2026-10-02-ui-v5-pixel-perfect-plan.md)
- [Current status](status.md)
- [Blueprint](../OMATERM_AGENT_BLUEPRINT.md): §10, §12–§13, §25–§28,
  §30–§36, §50–§53, §62–§68.
- [M13 Files](2026-09-29-13-milestone-13-file-tree.md)
- [M14 Git](2026-09-29-14-milestone-14-git-status.md)
- [M15 Diff](2026-09-29-15-milestone-15-diff-viewer.md)
- [M18 Process Info](2026-09-29-18-milestone-18-process-panel.md)
- [Dependencies/licenses](dependencies.md)
- [Acceptance matrix](acceptance-matrix.md)
