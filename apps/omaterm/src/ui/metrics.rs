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

/// 10px muted section/group headings (PROJECTS, PROJECT, group labels).
pub const HEADING_10: TypeRole = role!(10.0, FontWeight::NORMAL, 15.0);
/// 12px project/inspector names (active project name is MEDIUM).
pub const NAME_12: TypeRole = role!(12.0, FontWeight::NORMAL, 18.0);
pub const NAME_12_MEDIUM: TypeRole = role!(12.0, FontWeight::MEDIUM, 18.0);
/// 9px paths, badges, counts, kbd hints.
pub const META_9: TypeRole = role!(9.0, FontWeight::NORMAL, 13.5);
/// 11px body labels (tabs, rows, inputs, buttons).
pub const BODY_11: TypeRole = role!(11.0, FontWeight::NORMAL, 16.5);
/// 11px medium (Commit button).
pub const BODY_11_MEDIUM: TypeRole = role!(11.0, FontWeight::MEDIUM, 16.5);
/// 10px metadata (branch, pane headers, status, diff controls).
pub const META_10: TypeRole = role!(10.0, FontWeight::NORMAL, 15.0);
/// 12px terminal tabs and diff code.
pub const TAB_12: TypeRole = role!(12.0, FontWeight::NORMAL, 18.0);
/// 600-weight file-type marks (TS, `{ }`).
pub const BADGE_600: FontWeight = FontWeight::SEMIBOLD;

use gpui::{Div, prelude::Styled as _, px};

/// Apply a measured type role to a text container.
pub fn text_role(div: Div, role: TypeRole) -> Div {
    div.text_size(px(role.size_px))
        .font_weight(role.weight)
        .line_height(px(role.line_height_px))
}
