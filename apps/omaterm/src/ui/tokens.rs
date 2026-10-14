//! Shared visual tokens: radius, spacing, and control heights.
//!
//! Before this module the chrome used ad-hoc literals: five radius values
//! (7/4/6/8/5px) and nineteen padding values appeared directly at call sites,
//! so "the same" control could look different depending on which file drew it.
//! These constants give each recurring value one name, so a change is one edit
//! and a mismatch is visible in review.
//!
//! Values are the existing measured ones (see `docs/2026-10-02-ui-v5-*`), not
//! a new scale: the point is to name what shipped, not to redesign it.
//!
//! GPUI-free.

/// Corner radii, largest to smallest.
pub mod radius {
    /// Overlay cards and Settings group cards (8px).
    pub const CARD: f32 = 8.0;
    /// Menu boxes, primary chrome buttons, terminal find bar (7px).
    pub const CONTROL: f32 = 7.0;
    /// Tabs, segmented-control segments (6px).
    pub const TAB: f32 = 6.0;
    /// Segmented-control buttons, icon buttons, small chips (4px).
    pub const CHIP: f32 = 4.0;
}

/// Spacing steps used by padding and gaps.
pub mod space {
    /// 1px hairline separators and focus-ring insets.
    pub const HAIRLINE: f32 = 1.0;
    /// Tight inset (2px).
    pub const XXS: f32 = 2.0;
    /// Chip/icon inset (4px).
    pub const XS: f32 = 4.0;
    /// Icon-button inset (5–6px).
    pub const SM: f32 = 6.0;
    /// Row inset (8px).
    pub const MD: f32 = 8.0;
    /// Card inset (10px).
    pub const LG: f32 = 10.0;
    /// Section inset (12px).
    pub const XL: f32 = 12.0;
    /// Overlay inset (20px).
    pub const XXL: f32 = 20.0;
}

/// Control and row heights.
pub mod height {
    /// Overlay header rows and menu rows (28px).
    pub const MENU_ROW: f32 = 28.0;
    /// List rows in the file tree and search results (24px).
    pub const LIST_ROW: f32 = 24.0;
    /// Result rows in pickers, and the commit/stash composer strip (32px).
    pub const PICKER_ROW: f32 = 32.0;
}

#[cfg(test)]
mod tests {
    use super::*;

    /// The radii must stay strictly ordered so "bigger surface = bigger
    /// radius" remains true; a duplicate would collapse two roles into one.
    #[test]
    fn radii_are_distinct_and_ordered() {
        let mut radii = [radius::CARD, radius::CONTROL, radius::TAB, radius::CHIP];
        let original = radii;
        radii.sort_by(|a, b| b.partial_cmp(a).expect("finite radii"));
        assert_eq!(radii, original, "radii must be listed largest first");
        for window in radii.windows(2) {
            assert!(window[0] > window[1], "radii must be distinct");
        }
    }

    /// Spacing must be non-decreasing; a smaller step after a larger one would
    /// make the naming lie about relative size.
    #[test]
    fn spacing_steps_are_ordered() {
        let steps = [
            space::HAIRLINE,
            space::XXS,
            space::XS,
            space::SM,
            space::MD,
            space::LG,
            space::XL,
            space::XXL,
        ];
        for window in steps.windows(2) {
            assert!(window[0] <= window[1], "spacing steps must not decrease");
        }
    }

    /// Row heights must stay ordered so a picker row never looks tighter than
    /// a list row. Compile-time so a bad edit fails the build, not a test run.
    const _: () = {
        assert!(height::LIST_ROW < height::MENU_ROW);
        assert!(height::MENU_ROW < height::PICKER_ROW);
    };
}
