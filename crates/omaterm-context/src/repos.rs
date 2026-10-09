//! Depth-1 multi-repo discovery (M20).
//!
//! ```text
//! root itself is a repo  → [root] (fast path, no scan)
//! root holds child repos → one entry per direct child containing `.git`
//! no repos anywhere     → [] (downstream empty state, never an error)
//! ```
//!
//! Only one level is scanned (VS Code `repositoryScanMaxDepth` / Zed
//! default parity). A child counts as a repo when `<child>/.git` exists
//! as a directory (normal repo) or as a file (linked worktree /
//! submodule gitfile). The scan performs bounded `read_dir` + stat
//! calls only — no git subprocesses — and must run off the UI thread
//! like every other context query.

use std::path::Path;

use omaterm_core::{ProjectReposInfo, RepoEntry};

/// Upper bound on directory entries inspected per scan. Repos beyond
/// the cap are ignored; the desktop surfaces an overflow row past 32.
pub const MAX_SCAN_ENTRIES: usize = 256;

/// Scan with defaults (hidden directories skipped).
pub fn scan_repos(root: &Path) -> Vec<RepoEntry> {
    scan_repos_with(root, MAX_SCAN_ENTRIES, false)
}

/// Scan `root` for repositories with explicit bounds.
pub fn scan_repos_with(root: &Path, max_entries: usize, show_hidden: bool) -> Vec<RepoEntry> {
    if !root.is_dir() {
        return Vec::new();
    }
    if is_repo_dir(root) {
        return vec![RepoEntry::new(repo_name_of(root), root.to_path_buf())];
    }
    let mut repos = Vec::new();
    let Ok(entries) = std::fs::read_dir(root) else {
        return repos;
    };
    for (seen, entry) in entries.flatten().enumerate() {
        if seen >= max_entries {
            break;
        }
        let name = entry.file_name();
        let display = name.to_string_lossy();
        if !show_hidden && display.starts_with('.') {
            continue;
        }
        let Ok(file_type) = entry.file_type() else {
            continue;
        };
        if !file_type.is_dir() && !file_type.is_symlink() {
            continue;
        }
        let path = entry.path();
        if is_repo_dir(&path) {
            repos.push(RepoEntry::new(display.into_owned(), path));
        }
    }
    repos.sort_by(|left, right| left.name.cmp(&right.name));
    repos
}

/// Full multi-repo resolution: the untouched single-root result plus the
/// depth-1 scan beneath it and the effective active repo name
/// (last-saved when still present, else first-sorted, else `None`).
pub fn resolve_repos(
    pinned: Option<&Path>,
    active_cwd: Option<&Path>,
    saved_active: Option<&str>,
) -> ProjectReposInfo {
    let resolved = super::resolve::resolve_root(pinned, active_cwd);
    let Some(root) = resolved.root else {
        return ProjectReposInfo {
            root: None,
            source: resolved.source,
            repos: Vec::new(),
            active_repo: None,
        };
    };
    let repos = scan_repos(&root);
    let active_repo = match saved_active {
        Some(saved) if repos.iter().any(|entry| entry.name == saved) => Some(saved.to_string()),
        _ => repos.first().map(|entry| entry.name.clone()),
    };
    ProjectReposInfo {
        root: Some(root),
        source: resolved.source,
        repos,
        active_repo,
    }
}

/// A directory is a repo when `.git` exists as a dir (normal clone) or
/// a file (linked worktree / submodule gitfile).
fn is_repo_dir(path: &Path) -> bool {
    let dot_git = path.join(".git");
    dot_git.is_dir() || dot_git.is_file()
}

/// Display name for a repo path: final component, falling back to the
/// full lossy path when no file name exists (e.g. filesystem root).
fn repo_name_of(path: &Path) -> String {
    path.file_name()
        .map(|name| name.to_string_lossy().into_owned())
        .unwrap_or_else(|| path.to_string_lossy().into_owned())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::PathBuf;

    fn unique_base(tag: &str) -> PathBuf {
        std::env::temp_dir().join(format!("omaterm-m20-repos-{}-{}", std::process::id(), tag))
    }

    fn make_repo(path: &Path, gitfile: bool) {
        std::fs::create_dir_all(path).unwrap();
        let dot_git = path.join(".git");
        if gitfile {
            std::fs::write(&dot_git, "gitdir: /elsewhere/worktrees/x\n").unwrap();
        } else {
            std::fs::create_dir_all(&dot_git).unwrap();
        }
    }

    fn fixture(tag: &str) -> PathBuf {
        let root = unique_base(tag);
        let _ = std::fs::remove_dir_all(&root);
        make_repo(&root.join("api"), false);
        make_repo(&root.join("gateway"), false);
        make_repo(&root.join("web"), false);
        make_repo(&root.join("linked"), true);
        std::fs::create_dir_all(root.join("plain")).unwrap();
        std::fs::create_dir_all(root.join(".hidden-repo/.git")).unwrap();
        // Depth-2 nesting is out of scope: must NOT be discovered.
        make_repo(&root.join("plain/nested"), false);
        // Bare files are never repos.
        std::fs::write(root.join("notes.txt"), "x").unwrap();
        root
    }

    fn names(entries: &[RepoEntry]) -> Vec<&str> {
        entries.iter().map(|entry| entry.name.as_str()).collect()
    }

    #[test]
    fn discovers_depth_one_repos_sorted_and_skips_the_rest() {
        let root = fixture("discover");
        let repos = scan_repos(&root);
        assert_eq!(names(&repos), ["api", "gateway", "linked", "web"]);
        for entry in &repos {
            assert!(entry.path.starts_with(&root));
        }
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn hidden_repos_opt_in_with_show_hidden() {
        let root = fixture("hidden");
        let repos = scan_repos_with(&root, MAX_SCAN_ENTRIES, true);
        assert_eq!(
            names(&repos),
            [".hidden-repo", "api", "gateway", "linked", "web"]
        );
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn root_repo_takes_the_fast_path_without_scanning_children() {
        let root = unique_base("single");
        let _ = std::fs::remove_dir_all(&root);
        make_repo(&root, false);
        // A child repo must not shadow the root fast path.
        make_repo(&root.join("inner"), false);
        let repos = scan_repos(&root);
        assert_eq!(repos.len(), 1);
        assert_eq!(repos[0].path, root);
        assert_eq!(repos[0].name, repo_name_of(&root));
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn missing_or_file_roots_resolve_empty() {
        let missing = unique_base("no-such-dir");
        let _ = std::fs::remove_dir_all(&missing);
        assert!(scan_repos(&missing).is_empty());

        let file = unique_base("file-root");
        let _ = std::fs::remove_dir_all(&file);
        std::fs::create_dir_all(file.parent().unwrap()).unwrap();
        std::fs::write(&file, "x").unwrap();
        assert!(scan_repos(&file).is_empty());
        let _ = std::fs::remove_file(&file);
    }

    #[test]
    fn entry_cap_bounds_the_scan() {
        let root = fixture("cap");
        let repos = scan_repos_with(&root, 2, false);
        // Two entries inspected: alphabetical readdir order is not
        // guaranteed, so only the bound itself is asserted.
        assert!(repos.len() <= 2);
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn resolve_repos_applies_saved_then_first_sorted_default() {
        let root = fixture("resolve");
        let info = resolve_repos(Some(&root), None, None);
        assert_eq!(info.root, Some(root.clone()));
        assert_eq!(info.source, omaterm_core::RootSource::Pinned);
        assert_eq!(names(&info.repos), ["api", "gateway", "linked", "web"]);
        assert_eq!(info.active_repo.as_deref(), Some("api"));

        let kept = resolve_repos(Some(&root), None, Some("web"));
        assert_eq!(kept.active_repo.as_deref(), Some("web"));

        let stale = resolve_repos(Some(&root), None, Some("gone"));
        assert_eq!(stale.active_repo.as_deref(), Some("api"));

        let missing = unique_base("no-such-dir");
        let _ = std::fs::remove_dir_all(&missing);
        let empty = resolve_repos(Some(&missing), None, Some("api"));
        assert_eq!(empty.root, None);
        assert!(empty.repos.is_empty());
        assert_eq!(empty.active_repo, None);
        let _ = std::fs::remove_dir_all(&root);
    }
}
