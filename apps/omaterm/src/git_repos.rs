//! M20 multi-repo chrome plan: the pure render decisions the Git tab makes
//! from the router-owned depth-1 scan.
//!
//! GPUI-free. The router owns the scan cache and the poller
//! (`omaterm_core::repos`); this module only turns one scan into the rows,
//! active mark and overflow count the renderer draws. Render code itself is
//! not unit-testable, so this matrix is.

pub use omaterm_core::repos::{RepoScan, next_poll_index, should_scan};

use omaterm_core::RepoEntry;

/// Pure render plan for the VS Code-style chrome (M20): the bare facts a
/// renderer needs — whether the chrome shows at all, the visible rows in
/// scan order, which row is active, and how many rows the cap hides.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ChromePlan<'a> {
    /// Repo entries to render, in scan order, capped at `MAX_SECTIONS`.
    pub rows: Vec<&'a RepoEntry>,
    /// Name of the active repository, when the scan knows one.
    pub active: Option<&'a str>,
    /// Hidden count beyond the sectional cap.
    pub overflow: usize,
}

pub fn repo_chrome(scan: Option<&RepoScan>) -> ChromePlan<'_> {
    let Some(scan) = scan else {
        return ChromePlan {
            rows: Vec::new(),
            active: None,
            overflow: 0,
        };
    };
    // Single-repo projects (and non-repos) render nothing: their layout
    // is byte-identical to pre-M20.
    if scan.repos.len() < 2 {
        return ChromePlan {
            rows: Vec::new(),
            active: None,
            overflow: 0,
        };
    }
    // Every rendered row is a scan prefix, and the active entry is a
    // rendered row by construction (first-sorted index 0), so no filter
    // is needed to marry the two.
    let rows: Vec<&RepoEntry> = scan.visible_repos().iter().collect();
    let active = scan.active_entry().map(|entry| entry.name.as_str());
    ChromePlan {
        rows,
        active,
        overflow: scan.overflow_count(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use omaterm_core::repos::MAX_SECTIONS;
    use std::path::PathBuf;
    use std::time::Instant;

    fn scan(names: &[&str], root: &str) -> RepoScan {
        RepoScan {
            root: Some(PathBuf::from(root)),
            repos: names
                .iter()
                .map(|name| RepoEntry::new(*name, PathBuf::from(format!("{root}/{name}"))))
                .collect(),
            active: names.first().map(|name| name.to_string()),
            scanned_at: Some(Instant::now()),
            ..RepoScan::default()
        }
    }

    /// The chrome matrix the renderer cannot test itself: nothing shows
    /// for 0/1 repos, the cap hides the rest, and the active mark tracks
    /// the saved-else-first-sorted rule.
    #[test]
    fn chrome_plan_shows_only_multi_repo_highlights_and_caps() {
        // No scan: nothing.
        let hidden = repo_chrome(None);
        assert!(hidden.rows.is_empty());
        // Empty and single-repo scans: nothing.
        assert!(repo_chrome(Some(&RepoScan::default())).rows.is_empty());
        assert!(repo_chrome(Some(&scan(&["solo"], "/mono"))).rows.is_empty());
        // Two repos: both rows, first-sorted active, no overflow.
        let two = scan(&["api", "web"], "/mono");
        let plan = repo_chrome(Some(&two));
        assert_eq!(
            plan.rows
                .iter()
                .map(|entry| entry.name.as_str())
                .collect::<Vec<_>>(),
            ["api", "web"]
        );
        assert_eq!(plan.active, Some("api"));
        assert_eq!(plan.overflow, 0);
        // Overflow: capped rows, hidden count, and the saved-else-
        // first-sorted default for a stale saved name.
        let names: Vec<String> = (0..40).map(|i| format!("r{i:02}")).collect();
        let mut big = RepoScan {
            root: Some(PathBuf::from("/root")),
            repos: names
                .iter()
                .map(|name| RepoEntry::new(name.clone(), PathBuf::from(format!("/root/{name}"))))
                .collect(),
            active: Some("gone".into()),
            scanned_at: None,
            ..RepoScan::default()
        };
        let capped = repo_chrome(Some(&big));
        assert_eq!(capped.rows.len(), MAX_SECTIONS);
        assert_eq!(capped.overflow, 40 - MAX_SECTIONS);
        assert_eq!(capped.active, Some("r00"));
        big.active = Some("r00".into());
        assert_eq!(repo_chrome(Some(&big)).active, Some("r00"));
    }
}
