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
pub mod git_branch;
pub mod git_history;
pub mod git_sync;
pub mod ignore;
pub mod resolve;

pub use boundary::{ContextError, canonicalize_under_root};
pub use diff::{
    DEFAULT_DIFF_CONTEXT_LINES, DiffRequest, MAX_DIFF_BYTES, MAX_DIFF_CONTEXT_LINES,
    MAX_DIFF_FILES, MAX_DIFF_HUNKS_PER_FILE, MAX_DIFF_LINE_BYTES, MAX_DIFF_LINES_PER_HUNK,
    git_diff, git_diff_cancellable, git_stage_hunk, parse_diff,
};
pub use editor::{
    EditorError, EditorFile, EditorLanguage, FileRevision, MAX_EDITOR_BYTES, MAX_EDITOR_LINES,
    RootIdentity, canonical_document_path, detect_language, read_text_file, write_text_file,
};
#[cfg(target_os = "linux")]
pub use editor::{
    EditorRoot, WriteTextOutcome, read_text_file_cancellable, read_text_file_from_root,
    read_text_file_from_root_cancellable, write_text_file_from_root,
    write_text_file_from_root_cancellable,
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
pub use git_branch::{
    MAX_BRANCHES, git_ahead_behind, git_branch_checkout, git_branch_create, git_branch_delete,
    git_branch_list, git_branch_rename,
};
pub use git_history::{
    MAX_GIT_COMMIT_FILES, MAX_GIT_COMMIT_FILES_BYTES, MAX_GIT_HISTORY_AUTHOR_BYTES,
    MAX_GIT_HISTORY_BYTES, MAX_GIT_HISTORY_COMMITS, MAX_GIT_HISTORY_SUBJECT_BYTES, git_commit_diff,
    git_commit_files, git_commit_parents, git_history, parse_history_log,
};
pub use git_sync::{GIT_SYNC_TIMEOUT, GitSyncReport, git_fetch, git_pull, git_push};
pub use ignore::IgnoreFilter;
pub use resolve::{
    GIT_TOPLEVEL_TIMEOUT, git_toplevel_of, git_toplevel_of_with, resolve_root, resolve_root_with,
};
