//! Git branch-label formatting shared by workspace chrome.

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
