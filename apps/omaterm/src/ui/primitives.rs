//! Shared chrome primitives measured from the frozen v5 reference (R1).
//!
//! - `cmd_box`: the `.cmd` control — 1px transparent border, 7px radius,
//!   hover `#182029` bg / `#27313d` border / white foreground over 120ms
//!   (GPUI has no transition API; the end state applies instantly).
//! - `kbd`: keyboard-hint badge with inset bottom highlight.
//!
//! Padding variants follow the measured cascade: controls carrying the
//! Tailwind `p-1.5` class resolve to 6px (equal-specificity source order),
//! bare `.cmd` controls resolve to 5px vertical / 7px horizontal.

use gpui::{Div, div, prelude::*, px, rgb};

use super::assets;
use super::metrics;
use super::theme;
use super::tokens::space;

/// Pill chip: `#151a21` fill, 1px `#2c3643` border. Callers add their
/// measured radius, padding and type role.
pub fn pill() -> Div {
    div()
        .border_1()
        .border_color(rgb(theme::colors().pill_border))
        .bg(rgb(theme::colors().pill_bg))
}

/// Keyboard-hint badge: 4px radius, 6px/2px padding, inset bottom light.
/// (The source `.kbd` bottom inner highlight has no GPUI equivalent and
/// is recorded; geometry and colors match.)
pub fn kbd() -> Div {
    div()
        .rounded(px(4.0))
        .border_1()
        .border_color(rgb(theme::colors().kbd_border))
        .bg(rgb(theme::colors().kbd_bg))
        .px(px(6.0))
        .py(px(2.0))
}

/// Icon slot with exact box size, no flex shrink, and source tint.
/// Hover recolors to white, matching `.cmd:hover` (the SVG paint follows
/// the element's own text color, so the parent tint is not inherited).
pub fn cmd_icon(asset: &'static str, size_px: f32, color: u32) -> gpui::Svg {
    use gpui::prelude::InteractiveElement as _;
    assets::icon(asset, size_px, color).hover(|s| s.text_color(rgb(theme::colors().white)))
}

/// One-line placeholder for a loading or empty panel state.
///
/// Every panel already rendered these the same way (`muted` text, body role,
/// small padding); naming it keeps a new panel from inventing a fourth
/// treatment. Error and warning states are deliberately **not** modeled here:
/// they need distinct colors per surface (a Git failure reads red, "git
/// unavailable" amber, a process-query failure red), and collapsing them into
/// one enum would lose that distinction.
pub fn panel_state(message: impl Into<String>, tone: StateTone) -> Div {
    let colors = theme::colors();
    let (color, role) = match tone {
        StateTone::Loading | StateTone::Empty => (colors.muted, metrics::BODY_11),
    };
    metrics::text_role(div(), role)
        .px(px(space::MD))
        .py(px(space::SM))
        .text_color(rgb(color))
        .child(message.into())
}

/// How a non-error panel state reads: work in progress, or nothing to show.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum StateTone {
    Loading,
    Empty,
}

/// Status dot: a filled circle marking liveness/HEAD state.
///
/// Replaces the stray `●` text glyphs (and their inconsistent 6/8px sizes) so
/// every dot is the same measured circle. `DOT_SIZE` is the one size; the
/// color carries the meaning.
pub const DOT_SIZE: f32 = 8.0;

pub fn status_dot(color: u32) -> Div {
    div()
        .w(px(DOT_SIZE))
        .h(px(DOT_SIZE))
        .flex_shrink_0()
        .rounded_full()
        .bg(rgb(color))
}

/// Apply the keyboard focus ring to a chrome control.
///
/// The ring is a real border, so it must only ever be applied to one element
/// at a time (`WorkspaceView::chrome_is_focused`) and the element must reserve
/// the same inset when unfocused, otherwise the label shifts by the border
/// width on focus. `reserve` adds a transparent border of equal width to the
/// unfocused state; callers that already carry a 1px border pass `false`.
///
/// Generic over the element type so a stateful (`Stateful<Div>`) control keeps
/// its identity through the call — `.id()` must survive for tooltips.
pub fn focus_ring<E>(element: E, focused: bool, reserve: bool) -> E
where
    E: gpui::Styled,
{
    let colors = theme::colors();
    let width = theme::FOCUS_RING_WIDTH;
    if focused {
        element.border(px(width)).border_color(rgb(colors.blue))
    } else if reserve {
        element
            .border(px(width))
            .border_color(gpui::rgba(0x00000000))
    } else {
        element
    }
}
