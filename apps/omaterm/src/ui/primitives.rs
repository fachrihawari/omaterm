//! Shared chrome primitives measured from the frozen v5 reference (R1).
//!
//! - `cmd_box`: the `.cmd` control — 1px transparent border, 7px radius,
//!   hover `#182029` bg / `#27313d` border / white foreground over 120ms
//!   (GPUI has no transition API; the end state applies instantly).
//! - `pill`: bordered count/status chip on `#151a21`.
//! - `kbd`: keyboard-hint badge with inset bottom highlight.
//!
//! Padding variants follow the measured cascade: controls carrying the
//! Tailwind `p-1.5` class resolve to 6px (equal-specificity source order),
//! bare `.cmd` controls resolve to 5px vertical / 7px horizontal.

use gpui::{Div, div, prelude::Styled as _, px, rgb};

use super::assets;
use super::theme;

/// Pill chip: `#151a21` fill, 1px `#2c3643` border. Callers add their
/// measured radius, padding and type role.
pub fn pill() -> Div {
    div()
        .border_1()
        .border_color(rgb(theme::PILL_BORDER))
        .bg(rgb(theme::PILL_BG))
}

/// Keyboard-hint badge: 4px radius, 6px/2px padding, inset bottom light.
/// (The source `.kbd` bottom inner highlight has no GPUI equivalent and
/// is recorded; geometry and colors match.)
pub fn kbd() -> Div {
    div()
        .rounded(px(4.0))
        .border_1()
        .border_color(rgb(0x313B48))
        .bg(rgb(0x161B22))
        .px(px(6.0))
        .py(px(2.0))
}

/// Icon slot with exact box size, no flex shrink, and source tint.
/// Hover recolors to white, matching `.cmd:hover` (the SVG paint follows
/// the element's own text color, so the parent tint is not inherited).
pub fn cmd_icon(asset: &'static str, size_px: f32, color: u32) -> gpui::Svg {
    use gpui::prelude::InteractiveElement as _;
    assets::icon(asset, size_px, color).hover(|s| s.text_color(rgb(0xFFFFFF)))
}
