//! File listing + filename search over the M12 project root (M13,
//! blueprint §§23, 26, 64).
//!
//! Every read resolves through [`crate::canonicalize_under_root`] first, so
//! `..` traversal, absolute escapes, and symlink escapes fail with
//! [`crate::ContextError::PathOutsideRoot`] before any directory is opened.
//! Listings respect [`crate::IgnoreFilter`] (`.gitignore` + `.ignore` +
//! hidden policy); symlinked directories are never descended into. Results
//! are bounded: callers pass a `limit` (validated to `1..=5000` by
//! `omaterm-core`), and the envelope carries an accurate `truncated` flag.
//!
//! Paths in [`FileListInfo`] are relative to the project root so the wire
//! form stays stable when the root moves. No `gpui` dependency; bulk
//! filesystem work runs off the UI thread with a caller-owned generation
//! counter for cancellation (stale generations are dropped by the caller).

use std::path::Path;

use fuzzy_matcher::FuzzyMatcher;
use fuzzy_matcher::skim::SkimMatcherV2;
use omaterm_core::{FileEntry, FileKind, FileListInfo};

use crate::{ContextError, IgnoreFilter, canonicalize_under_root};

/// Largest number of walker entries `search_files` scores before it stops
/// and reports truncation. Keeps a pathological repo from becoming an
/// unbounded memory interface while staying far above the 10k-file
/// acceptance tree.
pub const MAX_SEARCH_SCAN: usize = 100_000;

/// Directory names the recursive search never descends into (build
/// artifacts, caches, VCS internals). Matched on the single directory
/// name at any level, so a project-local `target/` or `node_modules/`
/// stays out of the walk wherever it sits. Skipped directories still
/// appear as tree rows (`list_dir` is single-level and cheap) and remain
/// explicitly expandable — only automatic descent is refused.
pub const SKIP_TRAVERSE_NAMES: &[&str] = &[
    ".cache",
    ".git",
    "node_modules",
    "target",
    "__pycache__",
    ".venv",
];

fn is_skipped_dir(entry: &ignore::DirEntry) -> bool {
    entry.depth() > 0
        && entry.file_type().is_some_and(|kind| kind.is_dir())
        && SKIP_TRAVERSE_NAMES
            .iter()
            .any(|name| entry.file_name().to_string_lossy() == **name)
}

/// Single-level directory listing under `root`.
///
/// `dir` is relative to the root (`None`/empty means the root itself);
/// absolute values must already lie inside the root. Missing directories
/// surface as [`ContextError::Io`] so callers map them to a normal
/// `file_not_found`, never an escape claim.
pub fn list_dir(
    root: &Path,
    dir: Option<&Path>,
    limit: usize,
    show_hidden: bool,
) -> Result<FileListInfo, ContextError> {
    let limit = limit.max(1);
    let target = match dir {
        Some(dir) if dir.as_os_str().is_empty() => std::fs::canonicalize(root)?,
        Some(dir) => canonicalize_under_root(root, dir)?,
        None => std::fs::canonicalize(root)?,
    };
    if !target.is_dir() {
        return Err(ContextError::Io(std::io::Error::new(
            std::io::ErrorKind::NotFound,
            "not a directory",
        )));
    }
    let canonical_root = std::fs::canonicalize(root)?;
    let filter = IgnoreFilter::new(show_hidden);
    // Directories win over files under truncation: a tree that cuts `src/`
    // while showing `topfile099.txt` is useless. The walk scans at most
    // twice the bound (bounded work even for pathological directories),
    // then output fills directories first and files second.
    let mut dirs: Vec<FileEntry> = Vec::new();
    let mut files: Vec<FileEntry> = Vec::new();
    let mut scanned = 0usize;
    let mut truncated = false;
    // Root the walker at the requested directory (parent ignore files still
    // apply via `parents(true)`); depth 1 keeps single-level listings.
    let walker = filter.builder(&target).max_depth(Some(1)).build();
    for item in walker {
        let item = match item {
            Ok(item) => item,
            Err(_) => continue,
        };
        let path = item.path();
        if path == target {
            continue;
        }
        let Ok(relative) = path.strip_prefix(&target) else {
            continue;
        };
        if relative.components().count() != 1 {
            continue;
        }
        let kind = classify(item.file_type());
        let Ok(relative_to_root) = path.strip_prefix(&canonical_root) else {
            continue;
        };
        scanned += 1;
        if scanned > limit * 2 {
            truncated = true;
            break;
        }
        let entry = FileEntry {
            path: relative_to_root.to_path_buf(),
            kind,
        };
        if kind == FileKind::Directory {
            if dirs.len() < limit {
                dirs.push(entry);
            } else {
                truncated = true;
            }
        } else if files.len() < limit {
            files.push(entry);
        } else {
            truncated = true;
        }
    }
    let mut entries = dirs;
    entries.append(&mut files);
    if entries.len() > limit {
        entries.truncate(limit);
        truncated = true;
    }
    entries.sort_by(|a, b| {
        rank_kind(a.kind)
            .cmp(&rank_kind(b.kind))
            .then_with(|| a.path.cmp(&b.path))
    });
    Ok(FileListInfo { entries, truncated })
}

/// Fuzzy filename search over the project root (the `Ctrl+P` backend).
///
/// Only non-directory entries are returned (the finder opens files;
/// directories are browsed in the tree). Matching uses the skim algorithm
/// over the relative path string; ties break on path order so results are
/// deterministic. An empty query returns an empty envelope (the overlay
/// shows a hint); core validation rejects empty queries before this runs.
pub fn search_files(
    root: &Path,
    query: &str,
    limit: usize,
    show_hidden: bool,
) -> Result<FileListInfo, ContextError> {
    let limit = limit.max(1);
    let canonical_root = std::fs::canonicalize(root)?;
    if query.is_empty() {
        return Ok(FileListInfo {
            entries: Vec::new(),
            truncated: false,
        });
    }
    let filter = IgnoreFilter::new(show_hidden);
    let matcher = SkimMatcherV2::default();
    let mut scored: Vec<(i64, FileEntry)> = Vec::new();
    let mut scanned = 0usize;
    let mut truncated = false;
    let walker = filter
        .builder(&canonical_root)
        .filter_entry(|entry| !is_skipped_dir(entry))
        .build();
    for item in walker {
        let item = match item {
            Ok(item) => item,
            Err(_) => continue,
        };
        if item.path() == canonical_root {
            continue;
        }
        scanned += 1;
        if scanned > MAX_SEARCH_SCAN {
            truncated = true;
            break;
        }
        let file_type = item.file_type();
        if file_type.is_some_and(|kind| kind.is_dir()) {
            continue;
        }
        let Ok(relative) = item.path().strip_prefix(&canonical_root) else {
            continue;
        };
        let candidate = relative.to_string_lossy();
        let Some(score) = matcher.fuzzy_match(&candidate, query) else {
            continue;
        };
        scored.push((
            score,
            FileEntry {
                path: relative.to_path_buf(),
                kind: classify(file_type),
            },
        ));
    }
    scored.sort_by(|a, b| b.0.cmp(&a.0).then_with(|| a.1.path.cmp(&b.1.path)));
    if scored.len() > limit {
        truncated = true;
    }
    scored.truncate(limit);
    Ok(FileListInfo {
        entries: scored.into_iter().map(|(_, entry)| entry).collect(),
        truncated,
    })
}

/// Skim score plus matched **char** indices for one candidate, using the
/// same matcher and ranking as [`search_files`]. The desktop reuses this at
/// render time to highlight matches; scoring stays single-sourced here.
pub fn fuzzy_match_indices(candidate: &str, query: &str) -> Option<(i64, Vec<usize>)> {
    SkimMatcherV2::default().fuzzy_indices(candidate, query)
}

fn classify(kind: Option<std::fs::FileType>) -> FileKind {
    match kind {
        Some(kind) if kind.is_symlink() => FileKind::Symlink,
        Some(kind) if kind.is_dir() => FileKind::Directory,
        Some(kind) if kind.is_file() => FileKind::File,
        _ => FileKind::Other,
    }
}

fn rank_kind(kind: FileKind) -> u8 {
    match kind {
        FileKind::Directory => 0,
        FileKind::File => 1,
        FileKind::Symlink => 2,
        FileKind::Other => 3,
    }
}

/// Filesystem watcher errors. Limit exhaustion (inotify `max_user_watches`)
/// keeps the last good tree plus a warning banner; every other failure is
/// reported without tree loss.
#[derive(Debug, thiserror::Error)]
pub enum WatchError {
    #[error("file watcher limit exhausted: {0}")]
    LimitExhausted(String),
    #[error("file watcher unavailable: {0}")]
    Unavailable(String),
}

pub fn is_limit_exhaustion_message(message: &str) -> bool {
    let lowered = message.to_lowercase();
    [
        "inotify",
        "too many",
        "enospc",
        "max_user_watches",
        "limit",
        "watch",
    ]
    .iter()
    .filter(|needle| lowered.contains(*needle))
    .count()
        >= 2
        || (lowered.contains("inotify") && (lowered.contains("space") || lowered.contains("files")))
}

/// Live watcher over one project root. Dropping the handle stops watching
/// (cancel-on-switch); the caller owns debounce, revision ordering, and
/// stale-generation filtering.
pub struct FileWatcher {
    _watcher: notify::RecommendedWatcher,
    /// Canonical directories with an active watch (dedupes top-ups).
    armed: std::collections::HashSet<std::path::PathBuf>,
}

impl FileWatcher {
    /// Watch `root` top-down without descending into skip-listed
    /// directories ([`SKIP_TRAVERSE_NAMES`]) or `extra_skips` (canonical
    /// absolute prefixes, e.g. our own state/config dirs when they sit
    /// under the root — their writes must never re-arm our own refresh).
    /// Hidden directories are skipped when `show_hidden` is false (their
    /// contents never display, so watching them is pure cost).
    ///
    /// New subdirectories created after arming are NOT watched
    /// automatically; callers top them up with [`FileWatcher::watch_single`]
    /// when an event path turns out to be an unwatched directory.
    /// Each event's absolute paths are forwarded; callers map paths to
    /// tree directories for targeted refreshes.
    pub fn watch(
        root: &Path,
        extra_skips: &[std::path::PathBuf],
        show_hidden: bool,
        notify_hit: impl Fn(Vec<std::path::PathBuf>) + Send + 'static,
    ) -> Result<Self, WatchError> {
        use notify::{Config, RecommendedWatcher, RecursiveMode, Watcher};
        let canonical = std::fs::canonicalize(root).map_err(|error| {
            WatchError::Unavailable(format!("cannot watch project root: {error}"))
        })?;
        let extra_skips: Vec<std::path::PathBuf> = extra_skips.to_vec();
        let walk_skips = extra_skips.clone();
        let mut watcher: RecommendedWatcher = RecommendedWatcher::new(
            move |result: Result<notify::Event, notify::Error>| {
                if let Ok(event) = result {
                    // Drop access-kind events: our own tree walks open and
                    // read directories, and reporting those reads back would
                    // schedule another walk — a self-sustaining refresh loop
                    // on an otherwise idle tree. Create/Modify/Remove/Move
                    // still flow through.
                    if event.kind.is_access() {
                        return;
                    }
                    // Own state/config writes under the root must never
                    // re-arm our own refresh.
                    let paths: Vec<std::path::PathBuf> = event
                        .paths
                        .into_iter()
                        .filter(|path| !extra_skips.iter().any(|skip| path.starts_with(skip)))
                        .collect();
                    if !paths.is_empty() {
                        notify_hit(paths);
                    }
                }
            },
            Config::default(),
        )
        .map_err(|error| map_notify_error(&error.to_string()))?;
        let mut armed = std::collections::HashSet::new();
        // Top-down arming walk: same ignore policy as listings, plus the
        // traversal skip-list and extra prefixes pruned before descent, so
        // caches, VCS internals, and build trees never cost watches.
        let filter = IgnoreFilter::new(show_hidden);
        let mut builder = filter.builder(&canonical);
        builder.filter_entry(move |entry| {
            if entry.depth() == 0 {
                return true;
            }
            if is_skipped_dir(entry) {
                return false;
            }
            !walk_skips.iter().any(|skip| entry.path().starts_with(skip))
        });
        let walker = builder.build();
        for item in walker {
            let Ok(item) = item else { continue };
            if !item.file_type().is_some_and(|kind| kind.is_dir()) {
                continue;
            }
            // `watch` is fallible per directory (vanishing temp dirs);
            // failures arm nothing and prune nothing — the directory
            // simply stays unwatched until a later top-up.
            if watcher
                .watch(item.path(), RecursiveMode::NonRecursive)
                .is_ok()
            {
                armed.insert(item.path().to_path_buf());
            }
        }
        if !armed.contains(&canonical) {
            return Err(WatchError::Unavailable(
                "project root itself could not be watched".into(),
            ));
        }
        Ok(Self {
            _watcher: watcher,
            armed,
        })
    }

    /// Arm a single directory (top-up for directories created after the
    /// initial walk). Missing paths are a no-op success; limit exhaustion
    /// reports like any other arming failure.
    pub fn watch_single(&mut self, path: &Path) -> Result<(), WatchError> {
        use notify::Watcher;
        let Ok(canonical) = std::fs::canonicalize(path) else {
            return Ok(());
        };
        if self.armed.contains(&canonical) {
            return Ok(());
        }
        match self
            ._watcher
            .watch(&canonical, notify::RecursiveMode::NonRecursive)
        {
            Ok(()) => {
                self.armed.insert(canonical);
                Ok(())
            }
            Err(error) => Err(map_notify_error(&error.to_string())),
        }
    }
}

fn map_notify_error(message: &str) -> WatchError {
    if is_limit_exhaustion_message(message) {
        WatchError::LimitExhausted(message.to_owned())
    } else {
        WatchError::Unavailable(message.to_owned())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn fixture_tree(name: &str) -> std::path::PathBuf {
        let root =
            std::env::temp_dir().join(format!("omaterm-m13-files-{}-{name}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir_all(root.join("src")).unwrap();
        std::fs::create_dir_all(root.join("docs")).unwrap();
        std::fs::write(root.join("src").join("main.rs"), b"fn main() {}").unwrap();
        std::fs::write(root.join("src").join("lib.rs"), b"").unwrap();
        std::fs::write(root.join("src").join("ignored.log"), b"x").unwrap();
        std::fs::write(root.join("README.md"), b"hi").unwrap();
        std::fs::write(root.join(".hidden"), b"x").unwrap();
        std::fs::write(root.join(".gitignore"), b"*.log\n").unwrap();
        let _ = std::fs::remove_dir_all(root.join(".git"));
        root
    }

    #[test]
    fn list_dir_ranks_dirs_first_and_hides_dotfiles() {
        let root = fixture_tree("list");
        let listed = list_dir(&root, None, 100, false).unwrap();
        assert!(!listed.truncated);
        let names: Vec<String> = listed
            .entries
            .iter()
            .map(|entry| entry.path.to_string_lossy().into_owned())
            .collect();
        assert!(names.contains(&"src".to_owned()));
        assert!(names.contains(&"README.md".to_owned()));
        assert!(!names.iter().any(|name| name == ".hidden"), "{names:?}");
        // Directories sort before files.
        let src_pos = names.iter().position(|name| name == "src").unwrap();
        let readme_pos = names.iter().position(|name| name == "README.md").unwrap();
        assert!(src_pos < readme_pos, "{names:?}");
        let shown = list_dir(&root, None, 100, true).unwrap();
        assert!(
            shown
                .entries
                .iter()
                .any(|entry| entry.path.as_os_str() == ".hidden"),
            "{shown:?}"
        );
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn list_dir_nests_and_truncates_with_an_accurate_flag() {
        let root = fixture_tree("nested");
        let nested = list_dir(&root, Some(Path::new("src")), 100, false).unwrap();
        let names: Vec<String> = nested
            .entries
            .iter()
            .map(|entry| entry.path.to_string_lossy().into_owned())
            .collect();
        assert!(names.contains(&"src/main.rs".to_owned()), "{names:?}");
        assert!(names.contains(&"src/lib.rs".to_owned()), "{names:?}");
        assert!(
            !names.iter().any(|name| name.ends_with("ignored.log")),
            "{names:?}"
        );
        let bounded = list_dir(&root, None, 1, false).unwrap();
        assert_eq!(bounded.entries.len(), 1);
        assert!(bounded.truncated);
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn list_dir_prioritizes_directories_under_truncation() {
        let root = std::env::temp_dir().join(format!("omaterm-m13-dirprio-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir_all(root.join("zebra-dir")).unwrap();
        for name in ["a.txt", "b.txt", "c.txt"] {
            std::fs::write(root.join(name), b"x").unwrap();
        }
        let bounded = list_dir(&root, None, 2, false).unwrap();
        assert_eq!(bounded.entries.len(), 2);
        assert!(bounded.truncated);
        assert_eq!(bounded.entries[0].path.as_os_str(), "zebra-dir");
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn list_dir_rejects_traversal_and_missing_paths() {
        let root = fixture_tree("escape");
        // A nonexistent escape target fails closed (Io) or as an explicit
        // escape; either way it must never resolve inside the root.
        let error = list_dir(&root, Some(Path::new("../outside")), 10, false).expect_err("escape");
        assert!(matches!(
            error,
            ContextError::PathOutsideRoot | ContextError::Io(_)
        ));
        let missing = list_dir(&root, Some(Path::new("no-such-dir")), 10, false);
        assert!(matches!(missing, Err(ContextError::Io(_))));
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn search_ranks_exact_matches_first_and_truncates() {
        let root = fixture_tree("search");
        let found = search_files(&root, "main", 100, false).unwrap();
        assert!(!found.entries.is_empty());
        assert_eq!(
            found.entries[0].path,
            std::path::PathBuf::from("src/main.rs"),
            "{found:?}"
        );
        assert!(
            found
                .entries
                .iter()
                .all(|entry| entry.kind != FileKind::Directory),
            "{found:?}"
        );
        let bounded = search_files(&root, "r", 1, false).unwrap();
        assert_eq!(bounded.entries.len(), 1);
        assert!(bounded.truncated);
        let empty = search_files(&root, "", 10, false).unwrap();
        assert!(empty.entries.is_empty() && !empty.truncated);
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn search_respects_hidden_and_ignore_policy() {
        let root = fixture_tree("policy");
        let hidden = search_files(&root, "hidden", 10, false).unwrap();
        assert!(hidden.entries.is_empty(), "{hidden:?}");
        let shown = search_files(&root, "hidden", 10, true).unwrap();
        assert_eq!(shown.entries.len(), 1, "{shown:?}");
        let ignored = search_files(&root, "ignored", 10, true).unwrap();
        assert!(ignored.entries.is_empty(), "{ignored:?}");
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn search_never_descends_into_skipped_dirs_but_lists_them() {
        let root = std::env::temp_dir().join(format!("omaterm-m13-skip-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir_all(root.join("node_modules").join("dep")).unwrap();
        std::fs::create_dir_all(root.join("target").join("debug")).unwrap();
        std::fs::write(root.join("node_modules").join("dep").join("index.js"), b"x").unwrap();
        std::fs::write(root.join("target").join("debug").join("binary"), b"x").unwrap();
        std::fs::write(root.join("src-main.rs"), b"x").unwrap();

        // The recursive search stays out of skipped subtrees entirely.
        let found = search_files(&root, "index", 100, true).unwrap();
        assert!(found.entries.is_empty(), "{found:?}");
        let found = search_files(&root, "binary", 100, true).unwrap();
        assert!(found.entries.is_empty(), "{found:?}");
        // ...while single-level listings still show the directories as rows
        // (explicit expansion keeps working through `list_dir`).
        let listed = list_dir(&root, None, 100, true).unwrap();
        let names: Vec<String> = listed
            .entries
            .iter()
            .map(|entry| entry.path.to_string_lossy().into_owned())
            .collect();
        assert!(names.contains(&"node_modules".to_owned()), "{names:?}");
        assert!(names.contains(&"target".to_owned()), "{names:?}");
        let expanded = list_dir(&root, Some(Path::new("node_modules/dep")), 100, true).unwrap();
        assert!(
            expanded
                .entries
                .iter()
                .any(|entry| entry.path.as_os_str() == "node_modules/dep/index.js"),
            "{expanded:?}"
        );
        let _ = std::fs::remove_dir_all(&root);
    }

    fn drain(rx: &std::sync::mpsc::Receiver<Vec<std::path::PathBuf>>) -> Vec<std::path::PathBuf> {
        let mut all = Vec::new();
        while let Ok(paths) = rx.try_recv() {
            all.extend(paths);
        }
        all
    }

    #[test]
    fn pruned_arming_skips_noisy_subtrees_but_reports_the_rest() {
        let root = std::env::temp_dir().join(format!("omaterm-m13-watch-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir_all(root.join("sub")).unwrap();
        std::fs::create_dir_all(root.join("node_modules").join("dep")).unwrap();
        std::fs::create_dir_all(root.join("owned")).unwrap();
        let (tx, rx) = std::sync::mpsc::channel();
        let _watcher = FileWatcher::watch(&root, &[root.join("owned")], false, move |paths| {
            let _ = tx.send(paths);
        })
        .expect("watcher arms");
        // A write under a watched directory reports within seconds.
        std::fs::write(root.join("sub").join("live.txt"), b"x").unwrap();
        let mut seen = false;
        let deadline = std::time::Instant::now() + std::time::Duration::from_secs(5);
        while std::time::Instant::now() < deadline {
            if drain(&rx).iter().any(|path| {
                path.strip_prefix(&root)
                    .is_ok_and(|relative| relative.starts_with("sub"))
            }) {
                seen = true;
                break;
            }
            std::thread::sleep(std::time::Duration::from_millis(50));
        }
        assert!(seen, "watched write must report");
        // Writes under skip-listed and extra-skipped subtrees stay silent.
        let _ = drain(&rx);
        std::fs::write(root.join("node_modules").join("dep").join("quiet.js"), b"x").unwrap();
        std::fs::write(root.join("owned").join("quiet.txt"), b"x").unwrap();
        std::thread::sleep(std::time::Duration::from_secs(1));
        let silent = drain(&rx);
        assert!(
            silent.iter().all(|path| {
                path.strip_prefix(&root).is_ok_and(|relative| {
                    !relative.starts_with("node_modules") && !relative.starts_with("owned")
                })
            }),
            "{silent:?}"
        );
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn watch_single_tops_up_directories_created_after_arming() {
        let root = std::env::temp_dir().join(format!("omaterm-m13-topup-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir_all(&root).unwrap();
        let (tx, rx) = std::sync::mpsc::channel();
        let mut watcher = FileWatcher::watch(&root, &[], false, move |paths| {
            let _ = tx.send(paths);
        })
        .expect("watcher arms");
        // A directory created after arming is unwatched until topped up.
        std::fs::create_dir_all(root.join("fresh")).unwrap();
        std::thread::sleep(std::time::Duration::from_millis(300));
        let _ = drain(&rx);
        std::fs::write(root.join("fresh").join("before.txt"), b"x").unwrap();
        std::thread::sleep(std::time::Duration::from_millis(500));
        assert!(drain(&rx).is_empty());
        watcher
            .watch_single(&root.join("fresh"))
            .expect("top-up arms");
        // Re-arming an armed directory is a no-op success.
        watcher
            .watch_single(&root.join("fresh"))
            .expect("re-arm is idempotent");
        std::fs::write(root.join("fresh").join("after.txt"), b"x").unwrap();
        let mut seen = false;
        let deadline = std::time::Instant::now() + std::time::Duration::from_secs(5);
        while std::time::Instant::now() < deadline {
            if drain(&rx)
                .iter()
                .any(|path| path.ends_with("fresh/after.txt"))
            {
                seen = true;
                break;
            }
            std::thread::sleep(std::time::Duration::from_millis(50));
        }
        assert!(seen, "topped-up write must report");
        // Missing paths are a no-op, not an error.
        watcher
            .watch_single(&root.join("no-such-dir"))
            .expect("missing top-up is fine");
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn limit_exhaustion_detection_matches_inotify_pressure() {
        assert!(is_limit_exhaustion_message(
            "Too many open files: inotify max_user_watches limit reached (ENOSPC)"
        ));
        assert!(!is_limit_exhaustion_message("permission denied"));
        assert!(!is_limit_exhaustion_message("inotify event received"));
    }
}
