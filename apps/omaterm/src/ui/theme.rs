//! Exact v5 color tokens, verbatim from `omaterm_mock_ui_v5.html`.
//!
//! Values are sRGB hex literals. Alpha-bearing surfaces use separate
//! opacity constants — never bake parent opacity over text/icons.

pub const BG: u32 = 0x0B0E12;
pub const BG2: u32 = 0x0F1318;
pub const PANEL: u32 = 0x11161C;
pub const PANEL2: u32 = 0x161C24;
pub const PANEL3: u32 = 0x1B222C;
pub const BORDER: u32 = 0x252D38;
pub const BORDER2: u32 = 0x303A48;
pub const TEXT: u32 = 0xD7DDE5;
pub const TEXT2: u32 = 0xBCC5D0;
pub const MUTED: u32 = 0x7F8A99;
pub const MUTED2: u32 = 0x596474;
pub const BLUE: u32 = 0x5AA9FF;
pub const BLUE2: u32 = 0x2F81F7;
pub const CYAN: u32 = 0x61D8DF;
pub const GREEN: u32 = 0x63D58D;
pub const YELLOW: u32 = 0xE9C66D;
pub const ORANGE: u32 = 0xF3A85F;
pub const RED: u32 = 0xFF6F6F;
pub const PURPLE: u32 = 0xC792EA;
pub const LIME: u32 = 0xB9F263;

pub const HEADER_BG: u32 = 0x0D1014;
pub const ACTIVE_TAB_BG: u32 = 0x141A21;
pub const SELECTED_PROJECT_BG: u32 = 0x18202A;
pub const CMD_HOVER_BG: u32 = 0x182029;
pub const CMD_HOVER_BORDER: u32 = 0x27313D;
pub const ROW_HOVER_BG: u32 = 0x171D25;
pub const TREE_SELECTED_BG: u32 = 0x1B2430;
pub const PILL_BG: u32 = 0x151A21;
pub const PILL_BORDER: u32 = 0x2C3643;
pub const SCROLLBAR_THUMB: u32 = 0x343E4B;
pub const EDITOR_BG: u32 = 0x101318;
pub const EDITOR_SIDE_HEADER_BG: u32 = 0x0F1216;
pub const INFO_CARD_BG: u32 = 0x141920;
pub const INFO_ICON_BOX_BG: u32 = 0x10151A;
pub const GIT_BADGE_BG: u32 = 0x222B36;
pub const COMMIT_HOVER_BG: u32 = 0x3D8BF8;
pub const TERMINAL_CURSOR: u32 = 0x95D7FF;
/// Plain white for pressed toggle icons and `cmd:hover` foreground.
pub const WHITE: u32 = 0xFFFFFF;
/// Keyboard-hint badge fill and border (see `primitives::kbd`).
pub const KBD_BG: u32 = 0x161B22;
pub const KBD_BORDER: u32 = 0x313B48;
/// Active editor line number (brighter than `LINE_NO`); hue reserved for
/// the full syntax palette.
pub const ACTIVE_LINE_NO: u32 = 0x768193;
/// Comment-token approximation until the full syntax palette lands.
pub const COMMENT_TOKEN: u32 = 0x6A9955;
/// Finder match-highlight accent (VSCode-style); hue reserved vs `BLUE2`.
pub const MATCH_ACCENT: u32 = 0x4C9AFF;
/// Editor selection wash as HSLA components (GPUI `hsla` has no hex form).
pub const SELECTION_HSLA: (f32, f32, f32, f32) = (0.591, 0.92, 0.578, 0.35);

pub const LINE_NO: u32 = 0x515D6D;
pub const LINE_NO_ADD: u32 = 0x5D8A67;
pub const LINE_NO_DEL: u32 = 0x8B5E5E;
/// rgba(46,160,67,.12) as 0xRRGGBBAA.
pub const DIFF_ADD_BG: u32 = 0x2EA0431F;
/// rgba(75,190,104,.8) inset mark.
pub const DIFF_ADD_MARK: u32 = 0x4BBE68CC;
/// rgba(248,81,73,.12) as 0xRRGGBBAA.
pub const DIFF_DEL_BG: u32 = 0xF851491F;
/// rgba(248,81,73,.82) inset mark.
pub const DIFF_DEL_MARK: u32 = 0xF85149D1;

/// Opacity of the terminal header background (`#0f1318` at 90%).
pub const PANE_HEADER_BG_OPACITY: f32 = 0.90;
/// Opacity of the floating pane toolbar background.
pub const PANE_TOOLBAR_BG_OPACITY: f32 = 0.95;
/// Inset focus-stroke alpha on the active pane.
pub const PANE_FOCUS_STROKE_ALPHA: f32 = 0.34;
/// Inset top-accent alpha on the active top tab.
pub const TAB_ACTIVE_TOP_ACCENT_ALPHA: f32 = 0.9;

/// Pack an `0xRRGGBB` literal with float alpha into `0xRRGGBBAA` for
/// `gpui::rgba`. `gpui::rgb()` drops the low byte and forces opaque, so
/// translucent surfaces must go through here.
pub fn with_alpha(rgb: u32, alpha: f32) -> u32 {
    (rgb << 8) | (alpha.clamp(0.0, 1.0) * 255.0).round() as u32
}

#[cfg(test)]
mod tests {
    use super::{BG2, DIFF_ADD_BG, PANE_HEADER_BG_OPACITY, with_alpha};

    #[test]
    fn with_alpha_packs_exact_rgba_bytes() {
        assert_eq!(with_alpha(BG2, PANE_HEADER_BG_OPACITY), 0x0F1318E6);
        assert_eq!(with_alpha(0x141A21, 0.95), 0x141A21F2);
        assert_eq!(with_alpha(0x2EA043, 0.12), DIFF_ADD_BG);
        assert_eq!(with_alpha(0x123456, 2.0), 0x123456FF);
        assert_eq!(with_alpha(0x123456, -1.0), 0x12345600);
    }
}
