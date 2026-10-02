# OmaTerm UI v5 — Exact-Design Rewrite Plan

> User requirement: reproduce `omaterm_mock_ui_v5.html` faithfully, including
> icons, layout, colors, typography, spacing, borders, shadows, and interactive
> states. This is a measured native GPUI reproduction, not an inspired redesign.

## 1. Authority, baseline, and deliverable

The HTML supplied in the conversation is the visual reference. The blueprint
continues to govern architecture and milestone documents govern capability
delivery. This plan supersedes the visual direction of
[the VS Code workbench proposal](ui-workbench-redesign-plan.md).

Planning baseline: commit `dca4e4e`, `feat: Implement VS Code workbench UI
redesign`. The working tree was clean before this documentation change.
The source HTML was supplied inline; a filesystem search did not locate a
checked-in `omaterm_mock_ui_v5.html`. Phase P0 must archive that exact source
before implementation. Do not substitute `vscode_ui_clone.html`.

Deliver three concrete artifacts:

1. A frozen reference package: HTML, resolved CSS, SVGs, font manifest,
   computed-style/bounds manifest, screenshots, and state recipes.
2. A native GPUI UI implementing those measurements and real application
   behaviors through the existing semantic dispatcher.
3. A visual/behavioral evidence matrix identifying each reproduced state,
   each remaining milestone dependency, and every measured difference.

The endpoint includes Terminal, Info, Files, Git, split/inline diff, and the
lightweight editor presentation shown in the reference. Dependencies determine
delivery order; they do not remove these surfaces from the design target.
Full-reference fidelity is not complete while a target surface is absent.

### Exact-design rules

- Use the reference's Lucide SVG paths. Nerd Font, Codicon, emoji, and Unicode
  substitutions are not acceptable for its SVG icons.
- Preserve exact panel placement, control order, hit boxes, type scale, and
  selected/hover treatment. Do not add an activity rail, global title row,
  terminal breadcrumb row, blue status strip, or permanent extra toolbar.
- Use literal source color values and alpha, not visually similar colors.
- Preserve the mock's default state: both sidebars visible, Info selected,
  terminal view selected, mixed three-pane layout, pane 1 active.
- Do not add automatic panel collapse or breakpoint behavior: the HTML has
  none. Measure its actual overflow at narrow widths.
- Every visual change from the frozen reference must appear in a discrepancy
  register. A limitation cannot be relabeled as pixel-perfect completion.
- Real projects, commands, PIDs, paths, counts, and telemetry replace fixture
  content in normal use. Identical fixture content is required for comparisons.

## 2. Reference freezing and measurement protocol

### P0 package structure (proposed)

```text
design/ui-v5/
  reference.html                 # exact user-supplied source
  vendor/                       # pinned resolved Tailwind/Lucide payloads
  fonts/                        # reference fonts, when distribution permits
  manifest.json                 # viewport, scale, versions, fonts, hashes
  computed-styles.json           # per-selector geometry and effective styles
  states.json                    # reproducible reference state recipes
  icons/                        # exact generated SVGs and license notices
  captures/                     # approved reference captures
```

1. Preserve the original HTML byte-for-byte; store its SHA-256.
2. Resolve its CDN dependencies once, record response hashes and actual
   versions, then make an offline capture copy using those frozen responses.
   `lucide@latest` and the unversioned Tailwind CDN cannot be repeatable
   comparison inputs.
3. Record browser build, OS, viewport in CSS pixels, device-pixel ratio,
   zoom (100%), color profile, root font size, and scrollbar implementation.
4. Wait for CSS generation, `lucide.createIcons()`, and all used fonts before
   measuring. Verify network/console errors and unresolved icon placeholders.
5. Read `getBoundingClientRect()` and `getComputedStyle()` for each component
   and representative child; collect pseudo-elements separately.
6. Record actual font faces for UI text, terminal content, `pre` code, numeric
   badges, and fallback symbols. A family list is not proof of the chosen face.
7. Capture idle, hover, focus, active, hidden-panel, scrolled, and toast states
   with exact pointer positions, scroll offsets, and animation timestamps.
8. Keep original and capture-copy outputs side by side to confirm freezing
   introduces no rendering changes.

Primary viewport: **1440 × 900 at DPR 1**. Additional references: 1280 × 800,
1024 × 768, 800 × 600, and a DPR 2 capture at 1440 × 900 logical pixels.
Capture the application content rectangle, excluding compositor decorations
and outside-window shadows in both browser and GPUI evidence.

### CSS cascade traps that must be measured

| Source detail | Required interpretation |
|---|---|
| `.cmd { padding:5px 7px; border:1px solid transparent; border-radius:7px }` follows Tailwind | A `p-1.5` class does not establish the final padding. Freeze computed padding. At the ordinary equal-specificity cascade, `.cmd` wins. |
| `.cmd:hover` changes background, border, and foreground | Match the effective hover result, including on already-pressed panel toggles. |
| `.terminal-glow` follows `.subtle-grid` and assigns `background-image` | The glow declaration replaces the grid image. Do not paint a visible grid merely because both classes occur. |
| Pane backgrounds are opaque `#0f1318` plus a faint linear gradient | Parent glow may be completely covered by pane surfaces. Capture the actual visible composition. |
| `.line` specifies both `min-height:22px` and `line-height:22px` | Diff containers' `leading-[21px]` does not override `.line` rows. Inline grid rows without `.line` retain their own inherited line height. |
| Tailwind Preflight affects `button`, `input`, `textarea`, `pre`, and SVG | Reproduce its effective resets and font metrics; do not use GPUI defaults. `pre` may resolve its own monospace family. |
| Transparent borders occupy layout space; inset shadows do not | Preserve border-box dimensions and paint focus outlines without shrinking content. |
| Some `active` classes coexist with Tailwind text classes | Measure actual foreground, rather than inferring color from class order in HTML. |
| `split-handle-*`, drawer, and scrim rules have no corresponding default DOM elements | Do not insert visible 4px terminal gutters or a drawer into the reference's default layout. |
| `open-in-new` and other icon names depend on the resolved Lucide release | Verify generated SVG output/aliases. Record any unresolved reference icon; do not guess a replacement. |

Every value below is **source-specified or source-derived**, not a claim of
browser-measured evidence. P0 replaces derived assumptions with measured
values where CSS layout, font metrics, or the cascade changes the result.

## 3. Coordinate system and shell geometry

One CSS pixel maps to one GPUI logical pixel in the baseline. Use physical
pixel-aware snapping at paint boundaries while retaining fractional layout
positions. Avoid independent rounding of adjacent pane fractions.

```text
┌──── Projects: 210 ────┬─4─┬────── header: 42 across remainder ────────────┐
│ header: 42           │   ├───────────────┬─4─┬──── Inspector: 330 ──────┤
│                      │   │ terminal /   │   │ Info | Files | Git: 40   │
│ project cards        │   │ editor / diff│   ├──────────────────────────┤
│                      │   │             │   │ selected inspector body  │
│                      │   │             │   │                          │
│ Open Project footer  │   │             │   │                          │
├──────────────────────┴───┴───────────────┴───┴──────────────────────────┤
│ global status: 24, spanning the entire application                     │
└───────────────────────────────────────────────────────────────────────┘
```

For viewport `W × H`, default Projects width `L=210`, Inspector width `R=330`:

| Rectangle | x | y | width | height |
|---|---:|---:|---:|---:|
| Projects | 0 | 0 | L | H−24 |
| Projects resizer | L | 0 | 4 | H−24 |
| Main/right header | L+4 | 0 | W−L−4 | 42 |
| Main view | L+4 | 42 | W−L−4−R−4 | H−42−24 |
| Inspector resizer | W−R−4 | 42 | 4 | H−42−24 |
| Inspector | W−R | 42 | R | H−42−24 |
| Global status | 0 | H−24 | W | 24 |

At 1440 × 900: Projects `(0,0,210,876)`, top header `(214,0,1226,42)`,
main view `(214,42,892,834)`, inspector resizer `(1106,42,4,834)`,
Inspector `(1110,42,330,834)`, global status `(0,876,1440,24)`.

Important seams: Projects right border lies inside its 210px box; Inspector
left border lies inside its 330px box; resizers are additional 4px regions.
Header and status bottom/top borders are inside their fixed heights.
The top header extends over the Inspector, not merely over the terminal.

### Visibility and resize contracts

- Projects: default 210, minimum 164, maximum 340px.
- Inspector: default 330, minimum 280, maximum 470px.
- Both resizers are transparent at rest, `#2f81f7` on hover, column-resize cursor.
- Hiding a panel removes its resizer and space completely; showing restores
  its last nonzero width. State survives toggle within the running window.
- Projects hidden: reveal button appears before tabs, with 8px left and 4px
  right margins. Inspector toggle stays at the right edge of the header.
- Drag updates are based on window-local pointer coordinates, clamped to the
  source ranges; use the HTML's absolute left/right edge math.
- Pointer release outside the resizer ends the drag. Window blur/cancellation
  also clears native drag state and restores cursor/selection behavior.
- No source breakpoint silently changes panel preference. Any later adaptive
  behavior requires its own explicit design change.

## 4. Exact color and surface inventory

### Tailwind `oma` tokens

| Token | Exact sRGB value | Primary role |
|---|---|---|
| bg | `#0b0e12` | root/window |
| bg2 | `#0f1318` | terminal/main |
| panel | `#11161c` | Projects/Inspector/headers |
| panel2 | `#161c24` | secondary hover |
| panel3 | `#1b222c` | raised/toast |
| border | `#252d38` | structural seams |
| border2 | `#303a48` | selected/strong border |
| text | `#d7dde5` | body text |
| text2 | `#bcc5d0` | secondary primary text |
| muted | `#7f8a99` | metadata/inactive controls |
| muted2 | `#596474` | inactive project dot |
| blue | `#5aa9ff` | focus/active accent |
| blue2 | `#2f81f7` | buttons/resizer hover |
| cyan | `#61d8df` | terminal tokens |
| green | `#63d58d` | running/added |
| yellow | `#e9c66d` | modified/attention |
| orange | `#f3a85f` | strings/runtime |
| red | `#ff6f6f` | destructive text |
| purple | `#c792ea` | branch/syntax |
| lime | `#b9f263` | prompt |

### Additional literals: preserve, do not merge into nearby tokens

| Value | Role |
|---|---|
| `#0d1014` | main header and global status |
| `#141a21` | active top tab, pane toolbar, editor highlighted rows |
| `#18202a` | selected project and pressed panel toggle |
| `#182029` / `#27313d` | command hover background/border |
| `#171d25` | project/tree/Git-row hover |
| `#1b2430` | selected tree row |
| `#151a21` / `#2c3643` | pill/input background and pill border |
| `#161b22` / `#313b48` / `#1d232b` | keyboard badge bg/border/bottom border |
| `#343e4b` | scrollbar thumb |
| `#202731` | dormant split-handle style |
| `#101318` | editor/diff surface |
| `#0f1216` | editor footer and split-diff side header |
| `#141920` / `#10151a` | Info project card / folder-icon box |
| `#222b36` | Git inspector count badge |
| `#24181b` | destructive diff-button hover |
| `#3d8bf8` | Commit hover |
| `#515d6d` / `#768193` | ordinary / active editor line numbers |
| `#5d8a67` / `#8b5e5e` | added / deleted line numbers |
| `#519aba` / `#cbcb41` | TS/file-code / JSON text mark |
| `#9cdcfe` / `#569cd6` / `#dcdcaa` / `#808080` | code identifiers / keywords / functions / punctuation |
| `#e7ecf2` / `#e0e6ed` | terminal command / Vite heading |
| `#95d7ff` | reference terminal cursor |
| `#ffffff` | active and hovered labels |

### Alpha, shadows, and effects

- Terminal surface gradient: white at alpha `.006` at top, alpha `0` at bottom,
  over `#0f1318`.
- Active pane: inset 1px stroke `rgba(90,169,255,.34)`.
- Active top tab: inset top 1px `rgba(90,169,255,.9)`.
- Terminal header: `#0f1318` at `.90` opacity as a background color.
- Pane toolbar: `#141a21` at `.95` opacity as a background color.
- Pane shadow: `0 10px 30px rgba(0,0,0,.22)`.
- Pop shadow: `0 20px 55px rgba(0,0,0,.48)`; apply only to elements actually
  using the token, not every raised surface.
- Selected project dot glow: `0 0 10px rgba(99,213,141,.24)`; active dev-tab
  dot glow: same geometry, alpha `.28`.
- Code-line hover: white alpha `.016`.
- Diff add: `rgba(46,160,67,.12)` plus inset left 3px
  `rgba(75,190,104,.8)`.
- Diff delete: `rgba(248,81,73,.12)` plus inset left 3px
  `rgba(248,81,73,.82)`.
- Input focus: inset 1px `#2f81f7`; keyboard badge has inset bottom 1px white
  alpha `.03`.
- Native colors must use the same sRGB alpha composition. Parent opacity is
  not a substitute for a translucent background: it would fade text/icons too.

## 5. Typography, spacing, radii, and rendering

### Font contract

- UI stack: `Inter`, `ui-sans-serif`, `system-ui`, `-apple-system`,
  `BlinkMacSystemFont`, `Segoe UI`, `sans-serif`.
- Terminal stack: `JetBrains Mono`, `SFMono-Regular`, `Consolas`,
  `Liberation Mono`, `Menlo`, `monospace`.
- The HTML does not load Inter or JetBrains Mono. P0 must record what actually
  renders on the chosen reference machine; do not assume either is installed.
- Freeze the exact selected font files/revisions and licenses. Bundle fonts
  where permitted to make the native comparison repeatable. Any fallback
  platform needs an explicitly separate baseline.
- UI text must not inherit the user's terminal font/size. Existing Files row
  geometry currently derives from terminal font metrics; remove that coupling.
- Match font weight 400 normally, 500 for the selected project/Commit, 600 for
  TS/JSON marks and relevant terminal tokens.
- Explicitly set line height, baseline, clipping, letter spacing, and font
  fallback per measured component. Tailwind's usual inherited line height is
  1.5; pixel-derive only where computed styles confirm it.

| Element | Font size | Source line-height/spacing |
|---|---:|---|
| Project name / top-tab label / Info project name | 12px | inherited; normally 18px |
| Project path | 9px | inherited; normally 13.5px |
| Projects/Info section labels | 10px | uppercase; `.12em` tracking (1.2px) |
| Git group headers | 10px | uppercase; `.1em` tracking (1px) |
| Inspector tabs / Files / Git rows / Info rows / Commit | 11px | inherited; normally 16.5px |
| Git path / badges / shortcut badge | 9px | inherited; normally 13.5px |
| Pane metadata / footer / status / diff controls | 10px | inherited; normally 15px |
| Terminal pane 1 body | 12px | 1.72 = 20.64px |
| Terminal pane 2 body | 11.5px | 1.68 = 19.32px |
| Terminal pane 3 body | 11px | 1.6 = 17.6px |
| Editor code | 12.5px | `.line` rows: 22px |
| Split diff code | 12px | `.line` rows: 22px |
| Inline diff code | 12px | source container: 21px |
| Toast | 11px | inherited; measure |

Use source spacing scale: `.5=2`, `1=4`, `1.5=6`, `2=8`, `2.5=10`,
`3=12`, `4=16`, `5=20`, `8=32`px at the frozen 16px root.
Radii: `rounded=4`, `rounded-md=6`, `rounded-lg=8`, `.cmd=7`,
fully rounded dots/badges/thumbs. Top-tab rounding is top corners only.

## 6. Exact Lucide asset and placement inventory

Vendor the **generated SVG geometry from the frozen Lucide payload**, not
hand-drawn approximations. Preserve the 24 × 24 viewBox, transparent fill,
`currentColor` stroke, actual stroke width (normally 2), round caps/joins,
and path transforms. At 14px, a 2-unit SVG stroke scales to about 1.167px;
forcing every icon to a 2px screen stroke would be visibly wrong.

| Surface/control | `data-lucide` name | Box | Color |
|---|---|---:|---|
| Projects hide | `panel-left-close` | 16 × 16 | white |
| Projects reveal | `panel-left` | 16 × 16 | muted |
| Projects top open | `folder-plus` | 14 × 14 | muted |
| Open Project bottom | `folder-plus` | 14 × 14 | text2 |
| Tab close | `x` | 14 × 14 | muted |
| Editor tab | `file-code-2` | 14 × 14 | `#519aba` |
| Diff tab | `git-compare-arrows` | 14 × 14 | green |
| New tab | `plus` | 16 × 16 | muted |
| Header search | `search` | 16 × 16 | muted |
| Inspector toggle | `panel-right` | 16 × 16 | white when visible; muted otherwise |
| Pane split right/down | `columns-2` / `rows-2` | 14 × 14 | muted |
| Pane restart/more | `rotate-cw` / `ellipsis` | 14 × 14 | muted |
| Pane close | `trash-2` | 14 × 14 | muted |
| Editor breadcrumb | `chevron-right` | 12 × 12 | muted |
| Editor toolbar | `split-square-vertical` / `more-horizontal` | 14 × 14 | muted |
| Diff header | `git-compare-arrows` | 16 × 16 | green |
| Inspector tabs | `info` / `folder` / `git-branch` | 14 × 14 | tab foreground |
| Inspector menu | `ellipsis` | 14 × 14 | muted |
| Info project icon | `folder-git-2` | 16 × 16 | blue |
| Info port / browser open | `globe-2` / `external-link` | 14 × 14 | blue / muted |
| Files search | `search` | 14 × 14 | muted |
| Files directory chevron | `chevron-down` / `chevron-right` | 14 × 14 | muted |
| Files directory | `folder-open` / `folder` | 16 × 16 | yellow |
| Files README | `file-text` | 16 × 16 | blue |
| Git branch | `git-branch` | 16 × 16 | purple |
| Git refresh/menu | `refresh-cw` / `ellipsis` | 14 × 14 | muted |
| Commit dropdown | `chevron-down` | 14 × 14 | muted |
| Git group chevron | `chevron-down` | 14 × 14 | muted |
| Unstage / stage | `minus` / `plus` | 14 × 14 | muted |
| Discard / untracked delete | `undo-2` / `trash-2` | 14 × 14 | muted |
| Git open action | `open-in-new` | 14 × 14 | muted; verify resolution in P0 |
| Global branch / ports / notifications | `git-branch` / `radio` / `bell` | 12 × 12 | muted |

TS and JSON marks are styled **text**, not Lucide icons: `TS` in `#519aba`
and `{ }` in `#cbcb41`, weight 600. Status circles are painted circles, not
icon-font glyphs. The footer's green `●` is source text: retain its measured
glyph metrics in the reference fixture.

GPUI implementation: add a desktop asset source and one SVG icon component
with explicit logical size and tint. Verify GPUI 0.2.2's SVG scaling/tint API
against the locked dependency before choosing exact Rust signatures. Prefer
the existing SVG renderer if it preserves strokes. Record any required
dependency in [dependencies.md](dependencies.md) with exact revision/license.

## 7. Projects sidebar specification

- Full-height body above global status, background panel, 1px right border.
- Header height 42px, 8px horizontal padding, 1px bottom border.
- Hide command first, 8px gap to `PROJECTS`, open command pushed right.
- Project list: 8px all-side padding, vertical scroll, 4px between cards.
- Cards: full available width, 8px radius, 1px border, 10px horizontal and
  vertical padding; active `#18202a` with border2; inactive transparent border
  and hover `#171d25`.
- Card first row: 8px dot, 8px gap, name, right-aligned branch. Second row:
  16px left indent, 4px top margin, 9px muted path with ellipsis.
- Selected project name is weight 500; other project names are 400.
- Source-derived card height with ordinary inherited metrics: 57.5px
  (2px border + 20px padding + 18px first row + 4px gap + 13.5px path).
  Measure before making this a native constant.
- Bottom Open Project area: 1px top border, 8px padding, 32px button, 6px
  radius, border2, 8px horizontal padding/gaps; normally totals 49px.
- Bottom label 11px text2. Shortcut badge: 9px muted, 6px horizontal/2px
  vertical padding, 4px radius, exact `.kbd` treatment.
- `⌘O` is the literal visual fixture label. Native Linux shortcut labeling
  must reflect real bindings; record the content delta, do not alter its
  badge style or spacing unnoticed.
- Keep project close/change operations reachable in an overflow/context menu,
  rather than adding existing `change`/`×` text chips to the source card.

## 8. Main header and top-tab specification

- Header: 42px, `#0d1014`, 1px bottom border, no separate title row.
- Tab scroller fills available width, 4px horizontal padding, 4px gaps,
  bottom-aligned children. Its contents stop before the fixed right controls.
- Top tabs: 36px high, 12px horizontal padding, 8px internal gaps, top 6px
  corner radius, 12px UI labels. Measure the inner bottom alignment accounting
  for the header border; do not assume a 6px top offset.
- Source minimum widths: dev/with pane count 154px, api/tests 130px,
  editor 165px, diff 185px. These are minima, not forced widths.
- Active: `#141a21`, white, 1px inset top accent alpha `.9`; inactive tabs
  use source text2 and inherited/transparent background.
- Terminal status dot 8px. Active dev count `3 panes` is 12px muted and
  auto-pushed right before the 14px close icon.
- Every source tab has a close icon, including inactive ones. Avoid retaining
  the current selected-tab-only close treatment.
- New-tab control: 32 × 32px, 2px bottom margin, 6px radius, hover panel2.
  Icon 16px. It follows the document/terminal tab chips in source order.
- Right control group: 8px horizontal padding, 4px gap, fixed shrink behavior.
  Search precedes Inspector toggle. Ordinary 16px-icon `.cmd` is source-derived
  32 × 28px when the 5px/7px padding wins; confirm computed bounds.
- Panel toggle visible state: white, `#18202a`, border2. Hidden state: muted,
  transparent border/background. Preserve hover override from `.cmd:hover`.
- Exactly one visible surface is active. The reference script activates the
  first terminal chip even if api/tests is clicked; production must select the
  actual typed TabId, keeping the same active styling.

## 9. Terminal panes, toolbar, and grid geometry

### Default fixture layout

Recursive root splits left/right at 0.5; the right subtree splits top/bottom
at 0.5. At the baseline: left pane `(214,42,446,834)`, upper right
`(660,42,446,417)`, lower right `(660,459,446,417)`.
Source borders are 1px at the left-pane right edge and upper-right bottom
edge. No visible 4px terminal split handle appears in this HTML.

### Pane template

- Background and inset focus stroke match section 4. No outer card radius,
  margin, additional pane gaps, or permanent toolbar.
- Header: 32px total, 12px horizontal padding, bottom 1px border,
  `#0f1318/.90`, 10px labels. Dot 6px with 8px right margin; command text2;
  shell/PID muted with 8px left margin.
- Pane 1 body: 20px all-side padding; 12px/20.64px terminal typography.
- Pane 2 body: 20px horizontal, 16px vertical; 11.5px/19.32px typography.
- Pane 3 body: 20px horizontal, 16px vertical; 11px/17.6px muted typography.
- The fixture has a footer **only on pane 1**: 28px including 1px top border,
  12px horizontal padding, 10px muted text, shell, 12px gap to CWD,
  right-aligned `128×34`. Do not add footers to panes 2/3 during reproduction.
- Implement this source asymmetry as explicit presentation metadata in the
  fixture; do not tie footer presence to focus, since clicking pane 2 must
  not rearrange the layout. Establish/document real-session presentation
  defaults separately; full-fixture captures use the exact source roles.

### Hover toolbar

- Absolute top 7px/right 8px of the pane; overlaid, never consumes grid height.
- Container: 6px radius, border2, `#141a21/.95`, pane shadow, 4px horizontal
  and 2px vertical padding, 2px gap between commands.
- Pane 1 action order: columns-2, rows-2, rotate-cw, ellipsis.
- Pane 2/3 order: columns-2, rows-2, trash-2.
- At rest: opacity 0, translateY −3px, no pointer hit handling. Hover:
  opacity 1, translateY 0, pointer enabled, 120ms CSS `ease` timing.
- Reserve child geometry consistently through the transition; focus styling
  cannot enlarge or shift this toolbar. Native keyboard focus may expose it
  as an explicitly documented accessibility state, without changing idle.

### Terminal correctness and sizing

The HTML terminal bodies are proportional HTML layouts containing arbitrary
`mt-*` spacing and three different type scales, not an actual terminal grid.
Treat exact sample-body rendering and real PTY rendering as separate measured
validation cases. Never infer engine cursor positions from HTML text widths.

1. Extract the current `paint_terminal`, fixed-cell shaping, resolved fonts,
   selection, cursor styles, scrolling, and snapshot code into a presentation
   module with retained tests.
2. Derive one stable outer content rectangle from shell geometry. Recursively
   split rectangles through the core tree, accounting for inside borders.
3. Subtract each leaf's actual header/footer/padding from its outer rectangle.
   Use the same calculation for renderer clipping, mouse-cell origins, and
   PTY columns/rows; do not apply one global header deduction to every leaf.
4. Resize sessions in place only when dimensions change. Avoid sizing from
   transient canvas paint offers, preserving the existing stable-grid lesson.
5. Exclude toolbar overlay geometry from resize calculations. Match the
   canvas default background to `#0f1318` so the old `#18181b` does not cover
   the new surface; continue honoring application-requested ANSI backgrounds.
6. Reference cursor fixture: 8 × 15px, `#95d7ff`, vertical-align −2px,
   1050ms step blink at 50%. A live VT cursor remains tied to cell geometry
   and engine style. Record this distinction rather than hardcoding an 8px
   grid cell or breaking cursor text contrast.
7. Diagnostic fixture rendering may reproduce exact mock text/baselines,
   with an explicit fixture identity. It does not certify PTY body equivalence.
   Compare native PTY snapshots separately with fixed input, metrics, and
   verified rows/columns; document any unavoidable body-layout difference.

Production pane splits target the clicked PaneId through `PaneCommand::Split`;
they do not reset the whole layout as the mock's `applyLayout()` does.
Existing hover-to-focus behavior and the source's click-to-focus behavior
require an explicit focus-policy decision before pointer wiring changes.
Whichever policy is selected, hover toolbar actions cannot start text selection
or operate on a previously focused sibling.

## 10. Right inspector header and Info

### Header

- 40px high including bottom border; 4px horizontal padding; background panel.
- Info → Files → Git order, followed by right-pushed ellipsis command with
  4px bottom margin.
- Tabs: full height, 12px horizontal padding, 6px icon/text gap, 11px UI text.
- Active label white; inactive muted. Active underline: left/right 9px inside
  tab, bottom 0, height 2px, blue, fully rounded.
- Git badge: 4px left margin, `#222b36`, 6px horizontal padding, 9px text,
  fully rounded. Count must account for the same file occurring in both
  staged and working-tree groups without pretending those are unique files.
- Switching tabs changes Inspector content only. Width/visibility and selected
  center terminal/diff surface remain stable.

### Info body

- Vertical scroll, 12px padding, 16px gaps between Project/Processes/Ports.
- Section labels: 10px uppercase, 1.2px tracking, muted, 8px bottom margin.
- Project card: 1px border, 8px radius, `#141920`, 12px padding.
- Inner row gap 8px. Folder box: 28 × 28px, 6px radius, border2,
  `#10151a`, centered 16px blue folder-git-2.
- Name 12px; path 10px muted. Branch pill auto-right, 4px radius,
  6px horizontal/2px vertical padding, 9px muted text.
- Processes/Ports count pills auto-right, 6px horizontal padding, 9px text,
  fully rounded, `.pill` background/border.
- Process/port group boxes: 1px border, 8px radius, clipped row content.
- Rows: 12px horizontal/10px vertical padding, 11px font. Interior rows have
  1px bottom border; last row has none. Derive height from actual line metrics
  rather than rounding to an invented 36px row.
- Process dot 6px, 8px trailing space; process name; PID 8px away; CPU/RSS
  auto-right as `0% · 6 MB` formatting in fixture.
- Port row: 14px blue globe, 8px trailing space, port, muted service/owner
  after 8px, right external-link where the source supplies it.

M18 supplies background process/port inspection, ownership, lifecycle, and
CPU/RSS data. Its current contract is focused-pane-scoped; the mock visually
labels project context. Preserve the Project card and exact row presentation,
identify actual data scope in tooltip/state documentation, and do not silently
reinterpret the backend as project-aggregated. Any aggregation change needs
its own domain contract and scope tests.

## 11. Files inspector

- Search wrapper: 8px padding, bottom 1px border. Input: 32px, 6px radius,
  border, `#151a21`, 8px inner horizontal padding; 14px muted search icon;
  input text 11px with 8px horizontal padding and no native outline.
- This search box is part of the exact target. The earlier M13 user decision
  used Ctrl+P only: record this new instruction as a presentation requirement
  and revise the milestone's UI contract deliberately during delivery.
- Search uses existing bounded filename search; it is not content grep.
  Use one query/selection model for the Inspector input and finder results
  where appropriate, generation-cancelled and off the UI thread.
- Tree: 4px vertical padding, scrollable body, 11px labels, exactly 28px rows.
- Directory row: 8px horizontal padding, 6px gaps, 14px chevron,
  16px yellow folder. Child blocks indent by exactly 16px per nesting level.
- Restore explicit chevrons: existing folder-glyph-only rendering does not
  match the reference.
- File row: 8px horizontal padding/gaps; TS marker, filename, auto-right
  M/U decoration. Selected background `#1b2430`, hover `#171d25`.
- JSON/README top rows retain source 14px leading spacer, JSON text mark or
  file-text SVG. Filetype-specific expansion beyond the fixture uses a written
  mapping with the same aligned boxes; do not keep unrelated Nerd glyphs.
- Scrollbars: reference thumb `#343e4b`, transparent track, full radius;
  WebKit branch 9px wide/high. Firefox `thin` differs; freeze one renderer.
- Replace the current horizontal scrollbar's default-width estimate with
  measured actual viewport width. Recompute virtualization from the 28px UI
  row, not terminal font height, after every inspector resize.
- Preserve lazy listings, bounded expansions, watcher invalidation, no-root,
  missing-directory, loading, and truncation handling. Additional states reuse
  this visual system and are separately captured.
- Editor activation belongs to the future lightweight editor slice. Until
  it is delivered, existing terminal-routed file open is an explicitly partial
  capability; do not claim full Files/editor parity.

## 12. Git inspector

### Branch and commit area

- Container: 12px padding, bottom border, panel background.
- Branch row: 11px, 8px gaps, purple 16px git-branch, name, upstream pill
  `↑0 ↓0`, refresh command auto-right, ellipsis command.
- Pill: 4px radius, 6px horizontal/2px vertical padding, 9px muted.
- Commit input: 12px top margin, border, 6px radius, `#151a21`;
  textarea minimum 56px, 8px padding, 11px, no resize handle.
  Measure baseline/inline textarea layout: the wrapper's height cannot be
  assumed to equal 56px plus borders without browser confirmation.
- Commit row: 8px top margin, 8px gap. Main button flexes, 32px high,
  blue2/white, 11px weight 500, 6px radius, hover `#3d8bf8`.
- Adjacent dropdown: 32 × 32px, 6px radius, border2, hover `#18202a`,
  centered 14px muted chevron-down.
- Replace current end-appending single-line draft handling with a real native
  text input model supporting caret/selection/paste and multiline messages.
  Preserve per-project drafts and restore the draft after a failed commit.

### Change groups and rows

- Two visible source groups: Staged Changes and Changes. Untracked files are
  inside Changes, not a third permanently visible heading. Preserve backend
  group distinctions in typed row identities.
- Headers: 32px, 8px horizontal padding, 10px uppercase/.1em tracking,
  14px chevron with 4px trailing margin, count after 8px.
- Staged right actions: minus, ellipsis. Changes: plus, undo-2, ellipsis.
  Command gap 2px; actual padding governed by `.cmd` cascade.
- Changes header has top border and 4px top margin.
- Rows: 40px total height, 12px horizontal padding, 8px gaps, hover `#171d25`.
- Opener flexes with min-width zero, 8px icon/text gap; TS text at 10px/600;
  name 11px and path 9px muted, both ellipsized independently.
- Row actions take space even at opacity 0; hover fades them to 1 over 100ms.
  Do not let filenames reflow when actions appear.
- Staged actions: minus, open-in-new; modified actions: plus, undo-2;
  untracked actions: plus, trash-2. Status character at far right, 10px:
  yellow M, green A/U. Use real XY status rather than copying fixture letters.
- Footer: 1px top border, 12px padding, 10px muted; upstream left, short
  HEAD right. Current DTO lacks a guaranteed short-HEAD field: add a bounded
  semantic query/result extension before wiring this display.

Per-file and bulk stage/unstage/discard plus Commit must use the same router
as CLI/IPC. Existing two-step discard semantics remain a real interaction
state, styled within this system. The mock's DOM removal is not Git behavior.
Bulk operations need captured target paths, bounded batches, stale-root
validation, and post-operation status refresh; never build them by dispatching
against a changing global selection.

## 13. Diff view: both Split and Inline are delivery targets

- Diff background `#101318`. Top file-action header 40px including border,
  12px horizontal padding, panel background.
- Left cluster: 16px green git-compare-arrows, 8px gaps, filename 12px,
  scope 10px muted.
- Right cluster: 6px gaps, Stage File pill, Discard red button, divider
  (1 × 16px, 4px horizontal margin), Split then Inline.
- Stage pill/button: 10px, 10px horizontal/4px vertical padding, 6px radius.
- Active mode gets `.pill` and white; inactive mode muted with panel2 hover.
- Default Split. Left/right equal-width columns; left has 1px right border.
- Side headers: 28px, 12px horizontal padding, border-bottom, `#0f1216`,
  10px muted, exact side labels.
- Working-tree pair: INDEX ↔ WORKING TREE. Staged pair: HEAD ↔ INDEX.
  The mock only updates scope text on staged selection; native side headers
  must identify the actual compared revisions using the same design.
- Split content: 8px vertical padding; source min-content widths 610px left,
  650px right, horizontal scrolling. Each `.line` has 54px gutter, line-number
  text right-aligned with 14px trailing padding, 22px height.
- Inline content: 8px vertical padding, source minimum width 850px; old/new
  gutters 46px each, followed by code; 21px line-height on source grid rows.
- Diff colors, 3px add/remove inset marks, line-number colors, and source
  code span colors match sections 4–5.
- Plain-source comparisons must preserve whitespace and show old/new line
  numbers accurately. Added-only rows have no old number; deleted-only rows
  have no new number. Renames, binary files, no-newline markers, and truncated
  hunks need additional captured states.

The existing M15 contract explicitly defers side-by-side and syntax
highlighting. Deliver its bounded inline viewer and outstanding true partial
hunk semantics first. Then add a distinct **M15 fidelity extension**, updating
the milestone before implementing Split and code highlighting. This plan
includes both modes; deferral is sequencing, not permission to omit Split
from the final design.

Split rendering requires a tested old/new line alignment model with blank
spacer rows and bounded viewport rendering. Decide whether the detail view
shows bounded hunks or loads bounded full revisions; unified hunks alone do
not contain full source files. Resolve and document that before claiming a
full-file side-by-side view. Syntax tokenization runs off the UI thread with
language-to-color mappings; preserve the fixture's colors exactly.

Keep preview identity `(ProjectId, relative_path, staged)` separate from real
terminal TabId. A preview can remain open while inactive; current
`preview_open` conflates existence and activation. Split these states so a
terminal click returns to the terminal without deleting the preview chip.
File actions capture the preview identity, refresh both Git/diff models, and
never just change the header label without changing repository state.

## 14. Lightweight editor presentation and staged implementation

The editor shown in the HTML is part of the final visual plan, delivered
after the current developer-context milestones, in the blueprint's
lightweight editing slice. It does not add LSP, debugger, or extension scope.

- Editor tab: 165px minimum, file-code-2 14px in `#519aba`, 12px filename,
  10px yellow M, muted 14px x; same 36px tab template.
- Main background `#101318`.
- Breadcrumb/header: 36px, 12px horizontal padding, panel, bottom border;
  10px muted path parts with 4px gaps and 12px chevron-right separators;
  final filename text2. Toolbar pushed right, 4px gaps, exact SVGs.
- Code content: scrollable, 12.5px monospace, source minimum width 880px,
  12px vertical padding, 54px gutter, 22px lines, 14px number right padding.
- Fixture lines 9/10 use `#141a21`; their number color is `#768193`.
- Syntax colors and whitespace must match the source fixture; line hover
  white/.016.
- Footer: 28px, 12px horizontal padding, top border, `#0f1216`, 10px muted;
  `Ln 15, Col 2`, 16px gap to Spaces, 16px gap to UTF-8, language auto-right.
- Fixture caret: 1 × 16px, text color, −3px vertical alignment, 1000ms
  step blink at 50%.

Before functional editor delivery, define FileId/document ownership, dirty
state, save/reload, external changes, selection/caret, bounded loading,
undo/redo, Unicode behavior, and syntax worker cancellation. Route mutations
through shared semantic operations. A fidelity-only diagnostic view can
exercise this template earlier, but is explicitly not a working editor and
does not satisfy the product acceptance gate.

## 15. Status bar, toast, motion, and dormant overlays

### Global status

- 24px high, full window width, top border, `#0d1014`, 10px muted.
- Left order: branch button, process count, port count. Item horizontal
  padding 10px; inner gap 6px; branch icon 12px, source green dot/text glyph,
  radio icon 12px. Branch hover `#151a21`.
- Right-pushed group: CPU, MEM, UTF-8, bell. Each has 10px horizontal padding;
  bell 12px. Hover `#151a21`.
- No blue status background or extra always-visible history/font chips.
  Preserve existing history/font controls through contextual menu or overlay,
  with separate captured non-default states.
- Define telemetry scope explicitly (desktop process versus host totals),
  sampling interval, CPU denominator, RSS units, error/unavailable treatment.
  The HTML's CPU/MEM literals are fixture content, not a measurement contract.
- Bell must open a native notices list if displayed as an actionable button;
  source warnings/history/paste/spawn states remain reachable there. Place
  blocking Retry/confirmation prompts in a separately specified transient
  overlay; do not permanently shift reference chrome with stacked banners.

### Toast

- Fixed at window horizontal midpoint; bottom 38px (14px above status top).
- Background panel3, border2 1px, text, 8px vertical/12px horizontal padding,
  8px radius, 11px UI text.
- Shadow `0 15px 40px rgba(0,0,0,.35)`.
- Hidden: opacity 0 and +12px Y translation. Shown: opacity 1, translation 0;
  transition 180ms `ease`, stacking level equivalent to source z=200.
- New message replaces the current message and restarts a 1400ms hide timer.
  Pointer-transparent; it must not block pane selection.

### Remaining timing and layers

- `.cmd` and pane-toolbar transitions: 120ms `ease` (CSS cubic-bezier
  `.25,.1,.25,1`, not an arbitrary native easing curve).
- Git action fade: 100ms `ease`.
- Reference cursor visibility is stepped, not an opacity fade.
- Layers: pane toolbar 20; dormant scrim 50; dormant drawer 60; toast 200.
- Dormant drawer: top 42, bottom 24, width 260, left 0, panel, right border,
  shadow `12px 0 35px rgba(0,0,0,.26)`.
- Dormant scrim: top 42/bottom 24, black/.22, blur 1px.
- These dormant styles are inventory only: no visible drawer/scrim is present
  in the actual supplied DOM. Native overlays must not appear by inference.
- Use deadline-driven animations with no repaint loop once idle/hidden.

### Source keyboard contract and native focus routing

| Reference shortcut | Exact visible action | Native binding plan |
|---|---|---|
| Ctrl/Meta+B | toggle Projects | Ctrl+B on Linux; does not toggle Inspector |
| Ctrl/Meta+Shift+B | toggle Inspector | Ctrl+Shift+B; retains last selected Info/Files/Git tab |
| Ctrl/Meta+1 | show Terminal | Ctrl+1 selects the remembered real terminal tab and its pane tree |
| Ctrl/Meta+2 | show Editor | Ctrl+2 activates an actual editor document once its slice exists |
| Ctrl/Meta+3 | show Diff | Ctrl+3 activates a retained real diff preview, with no fake default file |

Header Search opens the existing Ctrl+P filename finder until M16 supplies
the general palette; its icon/position remain the source search control.
The Open Project button must perform an actual folder selection/create flow.
The literal reference shortcut badge does not establish an implemented
binding: choose and test the real Linux Open Project binding and register any
label delta. Audit the existing Ctrl+Shift+E/G workbench handlers before
retaining them: the old activity-panel behavior must not survive under an
unrelated control or shadow pane equalize. Existing split, pane focus,
equalize, tab/project navigation, copy/paste, and history operations stay
reachable through explicit tested bindings/menus.

Route consumed shortcuts before terminal encoding, but keep input ownership
explicit: typing, selection, and editing shortcuts inside native text inputs
cannot reach the PTY. Source Ctrl+2/3 with no real document/preview gets a
truthful unavailable notice in intermediate native delivery. Do not change
the core selected terminal tab just to emulate a view-local preview.

## 16. Native state, modules, and dependency boundaries

Start with private desktop modules, avoiding a crate split solely for styling:

```text
apps/omaterm/src/ui/
  mod.rs
  theme.rs                      # exact semantic + literal tokens
  metrics.rs                    # computed-style-derived sizing/type metrics
  assets.rs                     # frozen Lucide SVGs/font access
  primitives.rs                 # cmd/pill/kbd/tab/row/icon/input primitives
  shell.rs                      # Projects + header + center + Inspector + status
  state.rs                      # panel preferences, focus, active surface
  geometry.rs                   # shell/leaf/canvas/hit-test rectangles
  projects.rs
  tabs.rs
  terminal_pane.rs
  inspector.rs
  info.rs
  files.rs
  git.rs
  diff.rs
  status.rs
  overlays.rs                   # toast/notices/finder/confirmations
  fixture.rs                    # deterministic diagnostic presentation only
```

The lightweight editor module is added at its owning milestone. Existing
`files.rs`, `git_panel.rs`, and `diff_panel.rs` remain GPUI-free state/worker
models; their presentation moves into `ui/`. Split modules as their actual
boundaries become clear, rather than filling empty scaffolding up front.

### Existing code migration map

| Current location | Required change |
|---|---|
| `main.rs::render` | reduce to shell composition and view coordination; replace the activity/contextual-sidebar tree |
| `main.rs::render_title_bar`, `render_activity_rail`, `render_context_row` | retire their default UI output after equivalent underlying actions are reachable in source-position controls |
| `main.rs::render_sidebar_resizer`, `set_activity` | replace single sidebar state with two independently scoped panel states and drag handlers |
| `main.rs::render_node`, `render_leaf` | preserve recursion, use exact leaf chrome/overlays and common rectangle accounting |
| `main.rs::resize_panes_to_window` | replace activity/title/context subtraction with shell plus per-leaf inner rectangles |
| `main.rs::render_files_tree` and tree scrollbars | move rendering, restore chevrons/text marks, derive viewport from actual Inspector and fixed UI row metrics |
| `main.rs::render_git_panel`, `on_commit_key` | move rendering, implement proper multiline text-editing state, group/row/hover layout |
| `main.rs::render_diff_preview` | move rendering, separate preview activation/existence, add staged pair and later split alignment model |
| `main.rs::render_status_bar`, `render_ctrlp` | exact status composition; finder anchoring recalculated from new shell, without shifting default layout |
| `main.rs::on_key_down`, mouse handlers | implement independent panel shortcuts and explicit keyboard ownership/targeted pane commands |
| `workbench.rs` | migrate useful pure state/status helpers into UI modules, replace VS Code tokens, retire obsolete activity dimensions |
| `files.rs`, `git_panel.rs`, `diff_panel.rs` | retain bounded models/workers, evolve search/draft/preview identity only as required by semantic behavior |
| `router.rs`, `ipc_bridge.rs`, core/protocol/CLI | change only in named capability slices for new shared actions/results, with parity and scope tests |

Capture before/after behavior around each extraction. Do not replace the
application coordinator or session registry as a side effect of moving a
render method into a module.

State contract:

- Separate Projects preference and Inspector preference, each with visible,
  remembered width, and drag state. Inspector selected tab defaults to Info.
- Distinguish `ActiveSurface::Terminal(TabId)` from a typed preview key;
  do not string-match labels for selection or actions.
- Keep logical terminal-pane focus separate from keyboard-input ownership
  (terminal, Projects, Inspector list, text input, finder, preview, menu).
- A click in commit/search does not leave terminal keyboard forwarding live.
  Escape/close restores valid originating focus, without retargeting stale IDs.
- Frozen fixture state uses the exact source labels, status letters, row
  order, and counts. Production state derives from core/worker results.
- Panel preferences remain view-local initially, as in the HTML. Persistence
  across app launches is a separately versioned product change; retain current
  logical workspace and expansion persistence.
- UI commands continue through `OmaCommand` and `CommandRouter`. New restart,
  telemetry/HEAD queries, or editor actions require central validation/results
  and CLI/IPC parity where they mutate shared workspace/runtime state.
- Filesystem, Git, process inspection, and tokenization never move into render.
  Retain bounded caches and generation-guarded worker completion.
- TerminalSession ownership remains registry-based; hidden terminals still
  drain PTY output. Switching surfaces never creates a fresh shell.

### Capability dependencies that affect exact-reference completion

| Requirement | Existing foundation | Remaining delivery |
|---|---|---|
| Exact shell/tabs/panels/icons | current GPUI workbench | native presentation replacement |
| Files search box/chevrons/text marks | M13 lazy tree + filename matcher | inline input + precise tree restyle, documented UI-contract update |
| Git draft/rows/actions | M14 + current commit extension | multiline input, two groups, bulk action wiring, short HEAD |
| Info process/port cards | platform seams; M18 specification | complete M18 query/kill/live proof, then exact presentation |
| Pane restart | no verified Restart semantic command | transactional lifecycle command, async replacement/rollback tests |
| Ellipsis/dropdown actions | existing commands | design menus that expose supported actions without changing idle |
| Split/Inline diff + syntax | M15 bounded unified diff | close M15 gaps, fidelity extension for pair/row model and tokenization |
| Editor and editor tab | terminal-routed file open | owning lightweight-editor milestone after prerequisites |
| Status CPU/MEM + notices bell | logging/notices/platform seams | defined scoped sampling and notice-list presentation |

No full-fidelity milestone is signed off by removing a required control.
Intermediate runtime delivery may expose unavailable capabilities with explicit
disabled/error states, but they remain open acceptance items. Do not silently
reinterpret a restart as close-then-split or supply fake telemetry in normal use.

## 17. Implementation sequence and phase exit gates

Each phase leaves the app runnable; keep capability additions separate from
render extraction. Current M15 functional closure governs moving into new
capability slices; reference capture and behavior-preserving UI extraction can
proceed without treating M15 as complete.

### P0 — Freeze the reference and build the fidelity manifest

- Archive source, freeze dependencies/fonts, enumerate every SVG.
- Capture all reference states and computed styles; identify actual cascade,
  font, invalid-icon, and overflow results.
- Capture current native baseline at the same content sizes.
- Create the discrepancy register and DOM-selector → native-component map.

**Exit:** offline reference matches original; measured manifest, approved
captures, and reproducible recipes exist. No native pixel claim precedes this.

### P1 — Build exact assets, typography, and primitives

- Add Lucide assets, font resolution, exact palette/metrics.
- Build cmd/pill/kbd/dot/tab/row/input and shadow/focus rendering.
- Build isolated component captures on exact background colors, idle/hover.
- Verify SVG stroke scaling, fractional baselines, alpha composition, radii.

**Exit:** icon/primitives comparison passes; no font/glyph substitutions.

### P2 — Extract and replace the shell

- Extract shell rendering from `main.rs`, retaining router/lifecycle ownership.
- Replace activity/title/context regions with exact two-sidebar composition.
- Implement panel toggles/width memory, independent resizers, correct header
  span, Projects cards/footer, and global status background/height.
- Centralize geometry and correct scrollbar/finder viewport arithmetic.

**Exit:** exact default rectangles and hidden/resized states pass; no session
PID/identity changes after toggles. Source overflow behavior is documented.

### P3 — Tabs and terminal presentation

- Implement tab sizing, all close controls, typed active surface/preview state.
- Add exact leaf background, header, source fixture footer asymmetry, hover
  toolbar order/animation, and inset focus paint.
- Correct PTY size/hit-test arithmetic for padding/borders/leaf chrome.
- Carry required missing restart semantics as a dedicated follow-on capability
  slice with shared dispatch, rather than a click-handler workaround.

**Exit:** mixed-layout chrome matches; live terminal input/resize/selection,
hidden output, close isolation, and cursor/grid correctness pass separately.

### P4 — Exact Files/Git Inspector presentation

- Implement inspector header/underline/badge and vertical scroll bodies.
- Add Files search input, source icons/chevrons/text marks, exact 28px rows.
- Add Git branch/input/button area, exact 40px rows, two visible groups,
  hover action-space reservation, bulk actions, and footer data.
- Provide real text-editing input behavior and preserve worker generation
  guards, commit drafts, failure feedback, and discard confirmation.

**Exit:** Files/Git fixture captures match, real search and Git operations
agree with CLI, and focus ownership prevents terminal input leakage.

### P5 — Close M15, then deliver exact diff fidelity extension

- Complete current M15 acceptance gaps and release Wayland proof.
- Revise its extension contract for Split, paired line model, revision
  labels, syntax colors, scroll behavior, and bounded allocation.
- Render both source modes and mode/file actions with captured target identity.
- Keep source tab/toolbar geometry identical between staged/unstaged states.

**Exit:** Split/Inline captures pass; real staged/working-tree content and
file/hunk action scope verified. Whole-file staging is never labeled Stage Hunk.

### P6 — M18 Info and status/overlays completion

- Complete M18 process/port ownership, worker, sampling, and parity evidence.
- Render exact Info cards and populate status counters from defined real scope.
- Implement CPU/MEM data contract, browser-link launch, notices bell, menus,
  and exact toast timing/placement.
- Ensure availability/error states are truthful and preserve idle layout.

**Exit:** default Terminal+Info visual gate passes; actual process/port rows,
metrics, lifecycle actions, and toast behavior have live proof.

### P7 — Lightweight editor slice and final full-reference parity

- Complete applicable preceding milestones, then introduce editor operations
  and file/document ownership per blueprint, with tests before mutations.
- Implement exact editor tab/breadcrumb/code/footer/caret presentation.
- Preserve terminal-routed open where configured; document editor activation.

**Exit:** editor fixture passes and actual open/edit/save/dirty/external-change
flows work. Only now can every main surface in the source be certified.

### P8 — Full matrix, regression/performance, and handoff

- Run visual states at all frozen sizes/scales; fix every unregistered delta.
- Run mandatory quality gates and lifecycle/input regression coverage.
- Compare idle CPU, repaint count, RSS, FD/thread stability to baseline;
  no new idle animation loop, repeated Git/FS scans, or unbounded elements.
- Record exact commands/results, screenshots, font/assets licenses, and
  remaining unavailable platform observations in status/dependency docs.

**Exit:** every target surface, static state, live interaction, and required
check has evidence; no pending capability is hidden by a visual fixture pass.

## 18. Pixel-fidelity acceptance and screenshot comparison

### Required state matrix

| ID | Reproducible reference state |
|---|---|
| S01 | Default terminal, mixed 3-pane layout, Info, both panels visible |
| S02–S04 | Hover pane 1/2/3 toolbar separately; active pane border states |
| S05 | Projects hidden; reveal button and widened header/main |
| S06 | Inspector hidden; muted toggle and widened main |
| S07 | Both panels hidden |
| S08 | Projects widths 164/210/340; Inspector widths 280/330/470 |
| S09 | Alternate selected project, corresponding terminal labels/paths |
| S10 | Files selected, source expansion/selection/status marks |
| S11 | Files input focused and typed; tree overflow/scroll offsets from the frozen source (native filtering results are a separate live state) |
| S12 | Git selected with staged 2/changes 3 and badge 5 |
| S13 | Hover staged/modified/untracked Git rows independently |
| S14 | Commit input focused and multiline; Commit/dropdown hover |
| S15 | Working-tree Split diff, source file toolbar |
| S16 | Working-tree Inline diff |
| S17 | Staged Split/Inline; correct scope and Stage/Unstage/Discard state |
| S18 | Editor source tab/breadcrumb/highlighted rows/footer |
| S19 | Toast at 0/90/180/1400ms and after dismissal |
| S20 | Cursor visible/hidden phases at deterministic clock times |
| S21 | Header toggle/search/New-tab/close hover states |
| S22 | Source scrollbars with identical content overflow and offsets |

Also capture native-only behavior states: startup Retry, restore failure,
empty project/tab, no root/repo, binary/truncated diff, stale target, missing
Git/editor/font, process unavailable, confirmation armed, watcher limit,
and keyboard traversal. They must use the target design language; they have
separate evidence rather than being compared to nonexistent HTML states.

### Comparison protocol

1. Use the same viewport, scale, fonts, fixture strings, content order, pointer,
   scrollbar state, and clock phase. Never compare unrelated live terminal
   output to the mock's static sample.
2. Capture reference PNG and native PNG losslessly. No independent resizing,
   extra cropping inside the content rectangle, perspective, or palette edits.
3. Generate 50% overlay, absolute pixel-difference image, and enlarged crops
   of header seams, icons, text baselines, active strokes, and rounded cards.
4. Check rectangles/spacing before rasterization. Geometry at DPR 1 must
   match measured reference bounds to ≤0.5 logical pixel; painted 1px seams
   must land on the same physical pixels with no visible doubled edge.
5. Solid backgrounds must match exact RGB samples away from edges/text.
   Declared token values, alpha, border widths, radii, and icon paths must
   equal the manifest; screenshot similarity is not a substitute for this.
6. Text must use the same resolved face, size, weight, line height, letter
   spacing, wrapping, ellipsis points, and baseline. Match bounds to ≤0.5px
   and flag any changed glyph fallback or line break as a failure.
7. Compare identical SVGs at 12/14/16px using the same scale. Wrong glyph,
   stroke width, tint, alignment, or hit-box spacing fails regardless of a
   high overall image similarity score.
8. Cross-renderer antialiasing differences may occur even with identical fonts
   and paths. Keep narrowly labeled edge-only difference masks derived from
   known text/vector edges; never mask missing controls, broad regions, or
   geometry. Review unmasked differences and record the rasterization limit.
9. After P0, calibrate a reproducible automated per-region image threshold
   using actual browser/native rasterization. Do not invent an unmeasured
   universal SSIM percentage and call the task pixel-perfect.
10. Re-run only affected states after corrections, then the full final matrix.
    Use a per-state pass record; a matching S01 cannot certify S12–S18.

### Completion categories

- **Measured exact static design:** manifest/geometry/assets/colors match;
  reviewed screenshot diffs explain only narrow renderer edge differences.
- **Live behavior verified:** corresponding semantic/runtime workflow has
  release Wayland evidence, not merely diagnostic fixtures.
- **Partial / blocked:** missing surface/capability, unresolved font/icon,
  unmeasured state, unavailable input/scaling, or a failing required check.

Both the static and live categories are required for completed product UI.
Browser capture and Wayland evidence have not been produced by this planning
change; all screenshot criteria above are planned, not passing results.

## 19. Behavioral regression and required checks

Meaningful tests accompany geometry/state/input and domain changes, not literal
color getters or tests that merely restate constants:

- Independent visibility/width memory and non-finite/out-of-range width clamps.
- Shell rectangles across panel states; nested leaf rectangles and inside
  borders; exact canvas/PTY hit-test agreement at varied fonts/scales.
- Typed tab/preview activation, close fallback, stale target rejection,
  and preview retention while inactive.
- Keyboard ownership in search/commit/menu/finder versus live PTY forwarding;
  cancellation, focus restoration, and pointer drag ending outside controls.
- Git row identity includes group and path, including a partially staged
  file in both groups; bulk actions keep project/root/paths captured.
- Search and worker stale generations cannot populate a different project.
- Terminal PIDs/sessions/output survive view changes; pane close/restart
  cleanup and async rollback remain correct.
- Side-by-side line mapping, staged revision pairing, and bounded diff data;
  later editor Unicode/undo/save/external-change behavior.
- Process/port scope, CPU sampler math, and unavailable telemetry treatment.

Implementation quality gates:

```bash
cargo fmt --all --check
cargo test --workspace
cargo clippy --workspace --all-targets -- -D warnings
cargo build --release --bin omaterm --bin omaterm-desktop
python3 scripts/check-docs.py
git diff --check
```

The known PTY-suite stall requires explicit diagnosis/evidence. If the normal
workspace run fails/times out, record that invocation, then run the serial
suite (`cargo test --workspace -- --test-threads=1`) and relevant package
targets for diagnosis. Neither a timeout nor passing individual packages
constitutes a passing workspace invocation.

Manual release Wayland validation must include panels, hover controls, real
mouse drag outside bounds, tab/project/preview switching, clipboard and text
input, nested split/close/focus/resize, Files search, Git/diff operations,
Info/port actions, notices, and eventual editor workflows. Exercise supported
scales where available and list unavailable checks explicitly.

## 20. Handoff checklist and first implementation action

- [ ] Archive the exact supplied HTML and hash it.
- [ ] Freeze Tailwind, Lucide, selected fonts, and reference browser.
- [ ] Resolve computed styles/cascade and all SVG names; approve reference S01.
- [ ] Capture remaining source state matrix with bounds/style manifests.
- [ ] Build exact icon/font/primitives proof before rewriting the shell.
- [ ] Implement P2–P4 preserving domain/runtime behavior.
- [ ] Close M15 and deliver its fidelity extension.
- [ ] Deliver M18 Info/telemetry plus exact status/overlays.
- [ ] Deliver lightweight editor presentation at its owning milestone.
- [ ] Verify the full static/live matrix and record discrepancies honestly.

**Smallest next action:** P0 reference freeze and computed-style extraction,
then a GPUI specimen containing the exact 12/14/16px Lucide icons, cmd buttons,
active tab, project card, and Inspector underline beside their browser crops.
Do not begin another broad styling rewrite before those match.

## References

- [Agent workflow](../AGENTS.md)
- [Authoritative blueprint](../OMATERM_AGENT_BLUEPRINT.md): §10, §12–§13,
  §25–§28, §30–§36, §50–§53, §58, §62–§68.
- [Current status](status.md)
- [M13 File Tree](13-milestone-13-file-tree.md)
- [M14 Git Status](14-milestone-14-git-status.md)
- [M15 Diff Viewer](15-milestone-15-diff-viewer.md)
- [M16 Command Palette](16-milestone-16-palette.md)
- [M18 Process Info](18-milestone-18-process-panel.md)
- [Acceptance matrix](acceptance-matrix.md)
- [Dependencies/licenses](dependencies.md)
