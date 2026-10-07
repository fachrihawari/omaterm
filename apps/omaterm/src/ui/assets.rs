//! Vendored Lucide icons (lucide-static 1.49.0, ISC) plus the GPUI
//! asset source that serves them.
//!
//! Every SVG is the exact upstream 24×24 stroke geometry
//! (`fill=none stroke=currentColor stroke-width=2 round caps`); the
//! native tint comes from the element's text color, never from edited
//! paths. `open-in-new` is not a Lucide name (404 at pin time) and maps
//! to `external-link`; see the P6 manifest note.

use std::borrow::Cow;

use gpui::{AssetSource, Result, SharedString};

/// Lucide asset names keyed by UI slot. (`panel-left-close.svg` stays
/// vendored in `ICONS` for future use; no slot references it right now.)
pub const PANEL_LEFT: &str = "panel-left";
pub const PANEL_RIGHT: &str = "panel-right";
pub const FOLDER_PLUS: &str = "folder-plus";
pub const CLOSE: &str = "x";
pub const PLUS: &str = "plus";
pub const SEARCH: &str = "search";
pub const INFO: &str = "info";
pub const FOLDER: &str = "folder";
pub const FOLDER_OPEN: &str = "folder-open";
pub const FOLDER_GIT: &str = "folder-git-2";
pub const FILE_TEXT: &str = "file-text";
pub const GIT_BRANCH: &str = "git-branch";
pub const GIT_COMPARE: &str = "git-compare-arrows";
pub const EXTERNAL: &str = "external-link";
pub const COLUMNS: &str = "columns-2";
pub const ROWS: &str = "rows-2";
pub const TRASH: &str = "trash-2";
pub const REFRESH: &str = "refresh-cw";
pub const UNDO: &str = "undo-2";
pub const STAGE: &str = "plus";
pub const UNSTAGE: &str = "minus";
pub const CHEVRON_DOWN: &str = "chevron-down";
pub const CHEVRON_RIGHT: &str = "chevron-right";
pub const CHEVRON_LEFT: &str = "chevron-left";

macro_rules! icon_bytes {
    ($name:literal) => {
        (
            concat!("icons/", $name, ".svg"),
            include_bytes!(concat!("../../assets/icons/", $name, ".svg")).as_slice(),
        )
    };
}

/// Compile-time Lucide payloads served by [`OmaAssets`].
const ICONS: &[(&str, &[u8])] = &[
    icon_bytes!("panel-left-close"),
    icon_bytes!("panel-left"),
    icon_bytes!("panel-right"),
    icon_bytes!("folder-plus"),
    icon_bytes!("x"),
    icon_bytes!("plus"),
    icon_bytes!("minus"),
    icon_bytes!("search"),
    icon_bytes!("info"),
    icon_bytes!("folder"),
    icon_bytes!("folder-open"),
    icon_bytes!("folder-git-2"),
    icon_bytes!("file-text"),
    icon_bytes!("file-code-2"),
    icon_bytes!("git-branch"),
    icon_bytes!("git-compare-arrows"),
    icon_bytes!("globe-2"),
    icon_bytes!("external-link"),
    icon_bytes!("columns-2"),
    icon_bytes!("rows-2"),
    icon_bytes!("rotate-cw"),
    icon_bytes!("ellipsis"),
    icon_bytes!("more-horizontal"),
    icon_bytes!("trash-2"),
    icon_bytes!("refresh-cw"),
    icon_bytes!("undo-2"),
    icon_bytes!("chevron-down"),
    icon_bytes!("chevron-right"),
    icon_bytes!("chevron-left"),
    icon_bytes!("split-square-vertical"),
    icon_bytes!("radio"),
    icon_bytes!("bell"),
];

/// One Lucide icon at an explicit logical size and tint. Size maps the
/// 24-unit viewBox (a 2-unit stroke is ~1.17px at 14px — never forced to
/// a 2px screen stroke); tint flows through the text color.
pub fn icon(name: &'static str, size_px: f32, color: u32) -> gpui::Svg {
    use gpui::prelude::Styled as _;
    gpui::svg()
        .path(format!("icons/{name}.svg"))
        .w(gpui::px(size_px))
        .h(gpui::px(size_px))
        .text_color(gpui::rgb(color))
}

/// GPUI asset source over the embedded Lucide payloads.
pub struct OmaAssets;

impl AssetSource for OmaAssets {
    fn load(&self, path: &str) -> Result<Option<Cow<'static, [u8]>>> {
        Ok(ICONS
            .iter()
            .find(|(name, _)| *name == path)
            .map(|(_, bytes)| Cow::Borrowed(*bytes)))
    }

    fn list(&self, path: &str) -> Result<Vec<SharedString>> {
        Ok(ICONS
            .iter()
            .filter(|(name, _)| name.starts_with(path))
            .map(|(name, _)| SharedString::from(*name))
            .collect())
    }
}

#[cfg(test)]
mod tests {
    use super::ICONS;

    #[test]
    fn every_vendored_icon_is_a_tint_safe_stroke_svg() {
        assert!(!ICONS.is_empty());
        for (name, bytes) in ICONS {
            let text = std::str::from_utf8(bytes).expect("svg must be UTF-8");
            assert!(text.contains("viewBox=\"0 0 24 24\""), "{name}");
            assert!(text.contains("stroke=\"currentColor\""), "{name}");
            assert!(text.contains("stroke-width=\"2\""), "{name}");
        }
    }
}
