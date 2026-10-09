//! Multi-repo scan state shared by the router, the desktop poller and the
//! Git tab chrome (M20).
//!
//! The router owns the cache ([`install`] / [`clear`]) so the panel, the
//! `git.*` wire methods, `omaterm git *` and a future agent all answer for
//! the same active repository — no duplicated scan state that can drift
//! (blueprint §62). The type is GPUI-free: only domain facts and the pure
//! decisions the renderer and poller make from them.

use std::collections::HashMap;
use std::path::PathBuf;
use std::time::Instant;

use crate::ids::ProjectId;
use crate::result::{ProjectReposInfo, RepoEntry, RootSource};

/// VS Code-style rows rendered per project at most; the overflow row
/// names the remainder. Keeps a pathological checkout from flooding the
/// panel.
pub const MAX_SECTIONS: usize = 32;

/// A completed depth-1 scan for one project.
#[derive(Debug, Clone, Default)]
pub struct RepoScan {
    /// Resolved project root the scan ran against (`None` = absent root).
    pub root: Option<PathBuf>,
    /// Where the root came from (pinned / git / absent), echoed on query.
    pub source: RootSource,
    /// Repositories in scan order (sorted by name).
    pub repos: Vec<RepoEntry>,
    /// Effective active repo name (saved selection when still present,
    /// else the first-sorted default).
    pub active: Option<String>,
    /// When the scan completed (staleness source for the poller).
    pub scanned_at: Option<Instant>,
}

impl RepoScan {
    /// The active repo entry, following the saved-else-first-sorted rule.
    pub fn active_entry(&self) -> Option<&RepoEntry> {
        match self.active.as_deref() {
            Some(saved) => self
                .repos
                .iter()
                .find(|entry| entry.name == saved)
                .or(self.repos.first()),
            None => self.repos.first(),
        }
    }

    /// Repos to render as rows, honouring the display cap.
    pub fn visible_repos(&self) -> &[RepoEntry] {
        let cap = self.repos.len().min(MAX_SECTIONS);
        &self.repos[..cap]
    }

    /// How many repos the cap hid, if any.
    pub fn overflow_count(&self) -> usize {
        self.repos.len().saturating_sub(MAX_SECTIONS)
    }

    /// Identity for one scanned entry: `None` when the entry is the
    /// project root itself (a root repository), else its directory name.
    pub fn key_repo_of<'a>(&'a self, entry: &'a RepoEntry) -> Option<&'a str> {
        if self.root.as_ref().is_some_and(|root| &entry.path == root) {
            return None;
        }
        Some(entry.name.as_str())
    }
}

/// Replace a project's cached scan with a freshly completed one.
pub fn install(
    cache: &mut HashMap<ProjectId, RepoScan>,
    project: ProjectId,
    info: ProjectReposInfo,
) {
    let entry = cache.entry(project).or_default();
    entry.root = info.root;
    entry.source = info.source;
    entry.repos = info.repos;
    entry.active = info.active_repo;
    entry.scanned_at = Some(Instant::now());
}

/// Clear one project's scan (project deleted, or root changed so the next
/// tick rescans).
pub fn clear(cache: &mut HashMap<ProjectId, RepoScan>, project: ProjectId) {
    cache.remove(&project);
}

/// Whether the Git poller should (re)scan: never scanned, a repo list
/// older than the interval, or an explicit bust (root change, manual
/// refresh, project switch back to the tab).
pub fn should_scan(
    cached: Option<&RepoScan>,
    bust: bool,
    interval: std::time::Duration,
    now: Instant,
) -> bool {
    if bust {
        return true;
    }
    let Some(scan) = cached else {
        return true;
    };
    match scan.scanned_at {
        Some(at) => now.duration_since(at) >= interval,
        None => true,
    }
}

/// Which repo the next status worker polls: round-robin across the
/// scanned repos so a monorepo with N repositories costs N ticks of one
/// bounded git call each instead of N calls in one tick. Single-repo
/// projects (and root repos) always return the only candidate.
pub fn next_poll_index(repo_count: usize, cursor: usize) -> usize {
    if repo_count == 0 {
        0
    } else {
        cursor % repo_count
    }
}

#[cfg(test)]
mod tests {
    use super::*;

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

    #[test]
    fn active_entry_follows_saved_then_first_sorted() {
        let mut scan = scan(&["api", "web"], "/root");
        assert_eq!(scan.active_entry().unwrap().name, "api");
        scan.active = Some("web".into());
        assert_eq!(scan.active_entry().unwrap().name, "web");
        // A stale saved name (repo removed) falls back to first-sorted.
        scan.active = Some("gone".into());
        assert_eq!(scan.active_entry().unwrap().name, "api");
        scan.active = None;
        assert_eq!(scan.active_entry().unwrap().name, "api");
    }

    #[test]
    fn sections_cap_and_overflow() {
        let names: Vec<String> = (0..40).map(|i| format!("r{i:02}")).collect();
        let scan = RepoScan {
            root: Some(PathBuf::from("/root")),
            repos: names
                .iter()
                .map(|name| RepoEntry::new(name.clone(), PathBuf::from(format!("/root/{name}"))))
                .collect(),
            active: Some("r00".into()),
            scanned_at: None,
            ..RepoScan::default()
        };
        assert_eq!(scan.visible_repos().len(), MAX_SECTIONS);
        assert_eq!(scan.visible_repos()[0].name, "r00");
        assert_eq!(scan.overflow_count(), 40 - MAX_SECTIONS);
        assert!(scan.active_entry().is_some());
    }

    #[test]
    fn key_repo_distinguishes_root_repo_from_child_repo() {
        let root_repo = RepoScan {
            root: Some(PathBuf::from("/workspace")),
            repos: vec![RepoEntry::new("workspace", PathBuf::from("/workspace"))],
            active: Some("workspace".into()),
            scanned_at: None,
            ..RepoScan::default()
        };
        let entry = &root_repo.repos[0];
        assert_eq!(
            root_repo.key_repo_of(entry),
            None,
            "the project root itself is never a named child repo"
        );
        let child = scan(&["api", "web"], "/mono");
        assert_eq!(child.key_repo_of(&child.repos[0]), Some("api"));
    }

    #[test]
    fn install_and_clear_replace_per_project_scans() {
        let mut cache = HashMap::new();
        let project = ProjectId::new();
        install(
            &mut cache,
            project,
            ProjectReposInfo {
                root: Some(PathBuf::from("/root")),
                source: crate::result::RootSource::Pinned,
                repos: vec![RepoEntry::new("api", PathBuf::from("/root/api"))],
                active_repo: Some("api".into()),
            },
        );
        assert_eq!(cache[&project].repos.len(), 1);
        assert!(cache[&project].scanned_at.is_some());
        clear(&mut cache, project);
        assert!(!cache.contains_key(&project));
    }

    #[test]
    fn should_scan_covers_missing_stale_bust_and_fresh() {
        let now = Instant::now();
        assert!(should_scan(
            None,
            false,
            std::time::Duration::from_secs(30),
            now
        ));
        let fresh = RepoScan {
            scanned_at: Some(now),
            ..RepoScan::default()
        };
        assert!(!should_scan(
            Some(&fresh),
            false,
            std::time::Duration::from_secs(30),
            now
        ));
        assert!(should_scan(
            Some(&fresh),
            true,
            std::time::Duration::from_secs(30),
            now
        ));
        assert!(should_scan(
            Some(&fresh),
            false,
            std::time::Duration::from_secs(30),
            now + std::time::Duration::from_secs(31)
        ));
        let never = RepoScan::default();
        assert!(should_scan(
            Some(&never),
            false,
            std::time::Duration::from_secs(30),
            now
        ));
    }

    #[test]
    fn round_robin_covers_every_repo() {
        assert_eq!(next_poll_index(0, 3), 0);
        let seen: Vec<usize> = (0..5).map(|cursor| next_poll_index(3, cursor)).collect();
        assert_eq!(seen, vec![0, 1, 2, 0, 1]);
    }
}
