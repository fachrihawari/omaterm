//! Pure v5 shell geometry: panel widths, clamps, and rectangles.
//!
//! GPUI-free. One CSS pixel maps to one logical pixel at the baseline;
//! adjacent pane fractions must be snapped jointly at paint time, never
//! rounded independently here.

pub const PROJECTS_DEFAULT: f32 = 210.0;
pub const PROJECTS_MIN: f32 = 164.0;
pub const PROJECTS_MAX: f32 = 340.0;
pub const INSPECTOR_DEFAULT: f32 = 330.0;
pub const INSPECTOR_MIN: f32 = 280.0;
pub const INSPECTOR_MAX: f32 = 470.0;
pub const RESIZER: f32 = 4.0;
pub const HEADER_H: f32 = 42.0;
/// The session bar replaced the global status strip. Zero keeps old call
/// sites from reserving a row the shell no longer paints.
pub const STATUS_H: f32 = 0.0;

/// Clamp a Projects width; non-finite input falls back to default.
pub fn clamp_projects_width(width: f32) -> f32 {
    if !width.is_finite() {
        return PROJECTS_DEFAULT;
    }
    width.clamp(PROJECTS_MIN, PROJECTS_MAX)
}

/// Clamp an Inspector width; non-finite input falls back to default.
pub fn clamp_inspector_width(width: f32) -> f32 {
    if !width.is_finite() {
        return INSPECTOR_DEFAULT;
    }
    width.clamp(INSPECTOR_MIN, INSPECTOR_MAX)
}

/// Shell rectangles `(x, y, w, h)` for a viewport, in logical pixels.
/// Hidden panels (and their resizers) occupy zero space.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct ShellRects {
    pub projects: (f32, f32, f32, f32),
    pub projects_resizer: (f32, f32, f32, f32),
    pub header: (f32, f32, f32, f32),
    pub main_view: (f32, f32, f32, f32),
    pub inspector_resizer: (f32, f32, f32, f32),
    pub inspector: (f32, f32, f32, f32),
    pub status: (f32, f32, f32, f32),
}

pub fn shell_rects(
    viewport_w: f32,
    viewport_h: f32,
    projects_visible: bool,
    projects_width: f32,
    inspector_visible: bool,
    inspector_width: f32,
) -> ShellRects {
    let left = if projects_visible {
        clamp_projects_width(projects_width)
    } else {
        0.0
    };
    let right = if inspector_visible {
        clamp_inspector_width(inspector_width)
    } else {
        0.0
    };
    let left_resizer = if projects_visible { RESIZER } else { 0.0 };
    let right_resizer = if inspector_visible { RESIZER } else { 0.0 };
    let column_h = (viewport_h - STATUS_H).max(0.0);
    let main_x = left + left_resizer;
    let main_w = (viewport_w - main_x - right_resizer - right).max(0.0);
    let main_h = (column_h - HEADER_H).max(0.0);
    // Side columns run the full height. The session bar covers only the
    // terminal, so the inspector is not pushed down under it.
    ShellRects {
        projects: (0.0, 0.0, left, column_h),
        projects_resizer: (left, 0.0, left_resizer, column_h),
        header: (main_x, 0.0, main_w, HEADER_H),
        main_view: (main_x, HEADER_H, main_w, main_h),
        inspector_resizer: (
            viewport_w - right - right_resizer,
            0.0,
            right_resizer,
            column_h,
        ),
        inspector: (viewport_w - right, 0.0, right, column_h),
        status: (0.0, column_h, viewport_w, STATUS_H),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn default_rects_match_baseline_at_1440x900() {
        let r = shell_rects(1440.0, 900.0, true, 210.0, true, 330.0);
        assert_eq!(r.projects, (0.0, 0.0, 210.0, 900.0));
        assert_eq!(r.projects_resizer, (210.0, 0.0, 4.0, 900.0));
        assert_eq!(r.header, (214.0, 0.0, 892.0, 42.0));
        assert_eq!(r.main_view, (214.0, 42.0, 892.0, 858.0));
        assert_eq!(r.inspector_resizer, (1106.0, 0.0, 4.0, 900.0));
        assert_eq!(r.inspector, (1110.0, 0.0, 330.0, 900.0));
        assert_eq!(r.status, (0.0, 900.0, 1440.0, 0.0));
    }

    #[test]
    fn hidden_panels_release_space_and_resizers() {
        let r = shell_rects(1440.0, 900.0, false, 210.0, false, 330.0);
        assert_eq!(r.projects.2, 0.0);
        assert_eq!(r.projects_resizer.2, 0.0);
        assert_eq!(r.inspector.2, 0.0);
        assert_eq!(r.inspector_resizer.2, 0.0);
        assert_eq!(r.main_view, (0.0, 42.0, 1440.0, 858.0));
        assert_eq!(r.header, (0.0, 0.0, 1440.0, 42.0));
    }

    #[test]
    fn widths_clamp_to_usable_ranges() {
        assert_eq!(clamp_projects_width(0.0), PROJECTS_MIN);
        assert_eq!(clamp_projects_width(900.0), PROJECTS_MAX);
        assert_eq!(clamp_projects_width(f32::NAN), PROJECTS_DEFAULT);
        assert_eq!(clamp_inspector_width(0.0), INSPECTOR_MIN);
        assert_eq!(clamp_inspector_width(900.0), INSPECTOR_MAX);
        assert_eq!(clamp_inspector_width(f32::INFINITY), INSPECTOR_DEFAULT);
        let r = shell_rects(1440.0, 900.0, true, 0.0, true, 9999.0);
        assert_eq!(r.projects.2, PROJECTS_MIN);
        assert_eq!(r.inspector.2, INSPECTOR_MAX);
    }
}
