//! Legacy workbench helpers still in use by notices and the Git branch row.
//!
//! The VS Code-style shell (activity rail, contextual sidebar, title and
//! context rows, tab strip) is retired: UI v5 (`ui::theme`, `ui::geometry`)
//! owns the frame. What remains here are notice-severity colors, the two
//! Nerd Font glyphs still rendered pending Lucide SVG vendoring (P2c), and
//! the branch-label helper. Everything else was deleted with the old frame.

/// Notice strip severity colors (existing banner semantics).
pub const WARN_BG: u32 = 0x3F321D;
pub const WARN_TEXT: u32 = 0xFDE68A;
pub const ERROR_BG: u32 = 0x3F1D1D;
pub const ERROR_TEXT: u32 = 0xFCA5A5;

/// Branch label with a dirty marker (`main*`). `None` when the project is
/// not a repo so the status bar omits git rather than inventing metadata.
pub fn branch_label(branch: Option<&str>, dirty: bool) -> Option<String> {
    branch.map(|name| {
        if dirty {
            format!("{name}*")
        } else {
            name.to_string()
        }
    })
}

#[cfg(test)]
mod tests {
    use super::branch_label;

    #[test]
    fn branch_label_marks_dirty_and_omits_non_repo() {
        assert_eq!(branch_label(None, false), None);
        assert_eq!(branch_label(None, true), None);
        assert_eq!(branch_label(Some("main"), false), Some("main".to_string()));
        assert_eq!(branch_label(Some("main"), true), Some("main*".to_string()));
    }
}
