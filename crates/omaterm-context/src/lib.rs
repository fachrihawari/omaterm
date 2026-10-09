//! Project context foundation (M12, blueprint §§30–31, 50).
//!
//! Every v0.2 developer-context feature (file tree, git, diff, search, the
//! process panel) targets one deterministic filesystem root per project.
//! This crate owns root resolution, the symlink-aware filesystem boundary,
//! and the `.gitignore`/`.ignore` policy. It introduces no user-visible UI.
//!
//! Dependency direction: `omaterm-context` → `omaterm-core` (domain types
//! only). There is no `gpui` dependency; resolution runs on the desktop
//! owner thread with a bounded git wait, never on socket threads.

pub mod boundary;
pub mod diff;
pub mod editor;
pub mod files;
pub mod git;
pub mod git_blame;
pub mod git_branch;
pub mod git_history;
pub mod git_stash;
pub mod git_sync;
pub mod ignore;
pub mod repos;
pub mod resolve;

pub use boundary::{ContextError, canonicalize_under_root};
pub use diff::{
    DEFAULT_DIFF_CONTEXT_LINES, DiffRequest, MAX_DIFF_BYTES, MAX_DIFF_CONTEXT_LINES,
    MAX_DIFF_FILES, MAX_DIFF_HUNKS_PER_FILE, MAX_DIFF_LINE_BYTES, MAX_DIFF_LINES_PER_HUNK,
    git_diff, git_diff_cancellable, git_stage_hunk, parse_diff,
};
#[cfg(target_os = "linux")]
pub use editor::read_text_file_cancellable;
pub use editor::{
    EditorError, EditorFile, EditorLanguage, EditorRoot, FileRevision, MAX_EDITOR_BYTES,
    MAX_EDITOR_LINES, RootIdentity, WriteTextOutcome, canonical_document_path, detect_language,
    read_text_file, read_text_file_from_root, read_text_file_from_root_cancellable,
    write_text_file, write_text_file_from_root, write_text_file_from_root_cancellable,
};
pub use files::{
    FileSearchIndex, FileWatcher, MAX_SEARCH_INDEX_BYTES, MAX_SEARCH_SCAN, WatchError,
    fuzzy_match_indices, is_limit_exhaustion_message, list_dir, search_files,
    search_files_cancellable,
};
pub use git::{
    GIT_MUTATION_TIMEOUT, GIT_STATUS_TIMEOUT, GitError, git_commit, git_discard, git_stage,
    git_status, git_unstage, join_under_root,
};
pub use git_blame::{GitBlame, GitBlameLine, MAX_BLAME_LINES, git_blame};
pub use git_branch::{
    MAX_BRANCHES, git_ahead_behind, git_branch_checkout, git_branch_create, git_branch_delete,
    git_branch_list, git_branch_rename,
};
pub use git_history::{
    MAX_GIT_COMMIT_FILES, MAX_GIT_COMMIT_FILES_BYTES, MAX_GIT_HISTORY_AUTHOR_BYTES,
    MAX_GIT_HISTORY_BYTES, MAX_GIT_HISTORY_COMMITS, MAX_GIT_HISTORY_SUBJECT_BYTES, git_commit_diff,
    git_commit_files, git_commit_parents, git_history, parse_history_log,
};
pub use git_stash::{
    GitStash, GitStashList, MAX_STASHES, git_stash_apply, git_stash_drop, git_stash_list,
    git_stash_pop, git_stash_push, git_stash_show,
};
pub use git_sync::{GIT_SYNC_TIMEOUT, GitSyncReport, git_fetch, git_pull, git_push};
pub use ignore::IgnoreFilter;
pub use repos::{MAX_SCAN_ENTRIES, resolve_repos, scan_repos, scan_repos_with};
pub use resolve::{
    GIT_TOPLEVEL_TIMEOUT, git_toplevel_of, git_toplevel_of_with, resolve_root, resolve_root_with,
};
