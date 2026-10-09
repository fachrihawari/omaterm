//! Measured UI type roles from the frozen v5 reference (R0).
//!
//! Sizes/weights/line-heights below are browser-measured values at the 16px
//! root (see `design/ui-v5/component-measures*.json`), not Tailwind class
//! names. Line heights marked "normally" were 1.5× in the reference; the
//! measured pixel value is authoritative.
//!
//! Letter-spacing (1.2px section tracking, 1px group tracking) has NO
//! equivalent in GPUI 0.2.2 (`Styled` and `TextRun` expose no tracking
//! field — verified against the locked source). Uppercase labels render
//! without tracking; this is a genuine framework gap, recorded here rather
//! than silently ignored.

use gpui::FontWeight;

/// UI label role with measured size, weight and line height.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct TypeRole {
    pub size_px: f32,
    pub weight: FontWeight,
    pub line_height_px: f32,
}

macro_rules! role {
    ($size:expr, $weight:expr, $lh:expr) => {
        TypeRole {
            size_px: $size,
            weight: $weight,
            line_height_px: $lh,
        }
    };
}

// The names keep their v5 origin; the sizes are one step larger than the
// mock. At 9–11px the chrome read as fine print next to a 14px terminal.

/// Section/group headings.
pub const HEADING_10: TypeRole = role!(11.0, FontWeight::NORMAL, 16.0);
/// Project/inspector names (active project name is MEDIUM).
pub const NAME_12: TypeRole = role!(13.0, FontWeight::NORMAL, 19.0);
pub const NAME_12_MEDIUM: TypeRole = role!(13.0, FontWeight::MEDIUM, 19.0);
/// Paths, badges, counts, kbd hints.
pub const META_9: TypeRole = role!(10.0, FontWeight::NORMAL, 14.0);
/// Body labels (rows, inputs, buttons).
pub const BODY_11: TypeRole = role!(12.0, FontWeight::NORMAL, 18.0);
/// Medium body (Commit).
pub const BODY_11_MEDIUM: TypeRole = role!(12.0, FontWeight::MEDIUM, 18.0);
/// Metadata (branch, status, diff controls).
pub const META_10: TypeRole = role!(11.0, FontWeight::NORMAL, 16.0);
/// Terminal tabs and diff code.
pub const TAB_12: TypeRole = role!(13.0, FontWeight::NORMAL, 19.0);
/// 600-weight file-type marks (TS, `{ }`).
pub const BADGE_600: FontWeight = FontWeight::SEMIBOLD;

use gpui::{Div, prelude::Styled as _, px};

/// Apply a measured type role to a text container.
pub fn text_role(div: Div, role: TypeRole) -> Div {
    div.text_size(px(role.size_px))
        .font_weight(role.weight)
        .line_height(px(role.line_height_px))
}

/// Chained role application for builders that already hold a `Div`
/// (e.g. `.px_3().role(metrics::META_10)`). Identical to `text_role`.
pub trait DivRole {
    fn role(self, role: TypeRole) -> Self;
}

impl DivRole for Div {
    fn role(self, role: TypeRole) -> Self {
        text_role(self, role)
    }
}

/// `div().id(..)` chains yield `Stateful<Div>`; roles apply there too so
/// call sites never reorder builders around typing.
impl DivRole for gpui::Stateful<Div> {
    fn role(self, role: TypeRole) -> Self {
        self.text_size(px(role.size_px))
            .font_weight(role.weight)
            .line_height(px(role.line_height_px))
    }
}
