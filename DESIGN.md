# OmaTerm desktop design contract

The existing native GPUI component system in `apps/omaterm/src/ui/` owns
geometry, metrics, primitives, icons, and colors. The UI v5 references under
`design/ui-v5/` and the UI plans in `docs/` establish the existing component
anatomy. This UI/UX audit extends that system with a light palette and explicit
input and asynchronous operation states.

## Colors

Use shared semantic palette roles for every surface, foreground, border,
selection and feedback state. Dark retains the existing v5 palette. Light uses
pale slate shell `#eef1f5`, white panels, terminal `#fafbfd`, primary text
`#202936`, secondary text `#3c495a`, muted text `#596779`, blue `#175fbd`,
and darker semantic accent inks. Foreground and background must be paired;
white is reserved for filled accent buttons rather than selected text on pale
surfaces. Terminal defaults, ANSI base colors and OSC query replies must agree
with the selected palette. Explicit application RGB colors remain authoritative.

## Typography and spacing

Use the established `ui::metrics` typography roles and `ui::geometry` dimensions.
Retain the terminal monospace font settings. Lists use bounded viewports with
scrolling; viewport limits must never truncate the underlying result set.
Long paths and labels truncate within flex bounds while meaningful controls
remain reachable. Desktop narrow layouts must preserve usable terminal space.

## Components and interaction

Use the existing primitives for buttons, tabs, rows, icon controls and keyboard
hints. Exactly one visible surface or field owns typing. Transient overlays
take precedence; invisible fields must not receive keys. Native editor input
and manual keyboard routing must agree on the same owner. File drops must not
write to a terminal hidden behind another surface.

## Feedback and motion

Long operations retain a readable verb while pending, disable conflicting
actions, and report success or actionable errors. A static unlabeled icon does
not convey progress. Completion, failure and disconnected workers must release
the pending state. Project changes must not misattribute an operation.
Transient notices use the existing bounded toast stack; persistent warnings
must not conceal terminal rows or controls.

## Accessibility and verification

Keyboard navigation must reach the complete shortcut, branch and file lists.
Focus returns to a visible owner after dismissing overlays. Use semantic colors
with readable contrast in both themes; inspect native captures after changes.
Source review and unit tests do not establish rendered acceptance. Record every
surface and pending verification explicitly in the audit evidence.

## Accepted limitations

The palette is selectable at runtime (`Preferences: Toggle Theme`, explicit
Dark / Light / Follow System, or `appearance.theme` in `config.toml`); `system`
follows the desktop appearance live. A running TUI that cached its OSC 10/11/12
reply at startup may need a shell restart to pick up a new palette — new panes
are always exact. This audit does not certify all surfaces until native
interaction and visual evidence has been collected against the final build.
