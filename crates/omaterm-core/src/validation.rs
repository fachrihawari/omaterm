use crate::{
    CommandError, DiffCommand, EditorCommand, ErrorCode, FileCommand, GitCommand, HistoryCommand,
    OmaCommand, PaneCommand, ProjectCommand, TabCommand, TerminalCommand,
};

pub const MAX_READ_LINES: usize = 1_000;
pub const MAX_READ_COLUMNS: usize = 1_000;
pub const MAX_SEND_BYTES: usize = 8 * 1024;
pub const MAX_ARG_COUNT: usize = 256;
pub const MAX_ARG_BYTES: usize = 4 * 1024;
/// Bounded journal listing: same 1000-entry ceiling as viewport reads.
pub const MAX_JOURNAL_ENTRIES: usize = 1_000;
/// Bounded file listing/search envelope (M13). Matches the
/// `files.max-results` ceiling so a local socket never becomes an
/// unbounded memory interface (blueprint §64).
pub const MAX_FILE_ENTRIES: usize = 5_000;
/// Largest accepted `file.search` query: generous for a filename pattern,
/// small enough to keep matching bounded.
pub const MAX_FILE_QUERY_BYTES: usize = 256;
/// Bounded git mutation fan-out (M14): at most this many explicit paths per
/// `stage`/`unstage`/`discard` call, so one wire request stays one bounded
/// subprocess batch (blueprint §64).
pub const MAX_GIT_PATHS: usize = 100;
/// Largest accepted single git path in bytes. Generous for deep trees,
/// small enough to keep argv bounded.
pub const MAX_GIT_PATH_BYTES: usize = 4 * 1024;
/// Largest accepted commit message in bytes. Generous for a summary plus
/// body, small enough to keep the subprocess argv bounded.
pub const MAX_GIT_MESSAGE_BYTES: usize = 4 * 1024;
/// First history page cap. Continuation/cached-history bounds are enforced by
/// the router-owned history service once pagination lands.
pub const MAX_GIT_HISTORY_LIMIT: usize = 100;
/// Largest accepted `diff.show` context size (`-U` lines). Generous for
/// review, small enough to keep one wire response bounded.
pub const MAX_DIFF_CONTEXT_LINES: u8 = 10;
/// Largest accepted editor document path in bytes (M19). Matches the git
/// path ceiling: generous for deep trees, small enough to keep argv bounded.
pub const MAX_EDITOR_PATH_BYTES: usize = 4 * 1024;
/// Largest accepted git branch name / start revision in bytes (C9.1).
/// `check-ref-format` / `rev-parse --verify` stay authoritative; this only
/// rejects garbage before dispatch.
pub const MAX_GIT_BRANCH_BYTES: usize = 255;

/// Branch-name shape check shared by the branch arms.
fn is_branch_ref(name: &str) -> bool {
    !name.is_empty() && name.len() <= MAX_GIT_BRANCH_BYTES && !name.chars().any(char::is_control)
}
/// Largest accepted editor document in bytes (M19, blueprint §64). Checked
/// before allocation on both read and save; oversize files are rejected
/// with `document_too_large`, never partially loaded.
pub const MAX_EDITOR_BYTES: usize = 1024 * 1024;
/// Largest accepted editor document in lines (M19). Checked while splitting
/// on `\n` so a many-short-line file cannot exhaust memory line by line.
pub const MAX_EDITOR_LINES: usize = 20_000;

/// Pure field validation. Target existence and authorization are checked by
/// the application owner immediately before dispatch effects are applied.
pub fn validate(command: &OmaCommand) -> Result<(), CommandError> {
    let invalid = |message: &str| Err(CommandError::new(ErrorCode::InvalidRequest, message));
    let valid_name = |name: &str| !name.trim().is_empty() && !name.chars().any(char::is_control);
    match command {
        OmaCommand::Project(ProjectCommand::Create { name, directory }) => {
            if name.as_deref().is_some_and(|n| !valid_name(n)) {
                return invalid("project name must be non-empty and contain no control characters");
            }
            if directory.as_ref().is_some_and(|path| !path.is_dir()) {
                return invalid("project directory must exist and be a directory");
            }
        }
        OmaCommand::Project(ProjectCommand::Rename { name, .. })
        | OmaCommand::Tab(TabCommand::Rename { name, .. }) => {
            if !valid_name(name) {
                return invalid("name must be non-empty and contain no control characters");
            }
        }
        OmaCommand::Project(ProjectCommand::SetDirectory { directory, .. }) => {
            if !directory.is_dir() {
                return invalid("project directory must exist and be a directory");
            }
        }
        OmaCommand::Tab(TabCommand::Create { name, .. }) => {
            if name.as_deref().is_some_and(|n| !valid_name(n)) {
                return invalid("tab name must be non-empty and contain no control characters");
            }
        }
        OmaCommand::Pane(PaneCommand::Resize { fraction, .. }) => {
            if !fraction.is_finite() || !(0.1..=0.9).contains(fraction) {
                return invalid("split fraction must be finite and between 0.1 and 0.9");
            }
        }
        OmaCommand::Pane(PaneCommand::ResizeFocused { amount }) => {
            if !amount.is_finite() || amount.abs() > 0.8 {
                return invalid("resize amount must be finite and bounded");
            }
        }
        OmaCommand::Terminal(TerminalCommand::Create { directory, .. }) => {
            if directory.as_ref().is_some_and(|path| !path.is_dir()) {
                return invalid("terminal directory must exist and be a directory");
            }
        }
        OmaCommand::Terminal(TerminalCommand::RestorePane {
            directory, shell, ..
        }) => {
            if directory.as_os_str().is_empty() || (!directory.is_dir() && directory.exists()) {
                return invalid("restore directory must be a valid directory path");
            }
            if shell.as_ref().is_some_and(|program| {
                program.is_empty() || program.len() > 4096 || program.chars().any(char::is_control)
            }) {
                return invalid("restore shell must be a bounded program path");
            }
        }
        OmaCommand::Terminal(TerminalCommand::SendBytes { data, .. }) => {
            if data.len() > MAX_SEND_BYTES {
                return invalid("terminal input exceeds the 8 KiB command limit");
            }
        }
        OmaCommand::Terminal(TerminalCommand::RunCommand { argv, .. }) => {
            if argv.is_empty() || argv.len() > MAX_ARG_COUNT {
                return invalid("argv must contain 1 to 256 arguments");
            }
            if argv
                .iter()
                .any(|arg| arg.len() > MAX_ARG_BYTES || arg.chars().any(char::is_control))
            {
                return invalid(
                    "each argument must be at most 4096 bytes and contain no control characters",
                );
            }
        }
        OmaCommand::Terminal(TerminalCommand::ReadVisible {
            max_lines,
            max_columns,
            ..
        }) if *max_lines == 0
            || *max_lines > MAX_READ_LINES
            || *max_columns == 0
            || *max_columns > MAX_READ_COLUMNS =>
        {
            return invalid("read bounds must be between 1 and 1000 lines/columns");
        }
        OmaCommand::History(HistoryCommand::ListJournal { limit, .. })
            if *limit == 0 || *limit > MAX_JOURNAL_ENTRIES =>
        {
            return invalid("journal list limit must be between 1 and 1000 entries");
        }
        OmaCommand::File(FileCommand::List { limit, .. }) => {
            if limit.is_some_and(|n| n == 0 || n > MAX_FILE_ENTRIES) {
                return invalid("file list limit must be between 1 and 5000 entries");
            }
        }
        OmaCommand::File(FileCommand::Search { query, limit, .. }) => {
            if query.is_empty()
                || query.len() > MAX_FILE_QUERY_BYTES
                || query.chars().any(char::is_control)
            {
                return invalid(
                    "file search query must be 1 to 256 bytes with no control characters",
                );
            }
            if limit.is_some_and(|n| n == 0 || n > MAX_FILE_ENTRIES) {
                return invalid("file search limit must be between 1 and 5000 entries");
            }
        }
        OmaCommand::File(FileCommand::Open { path, .. }) if path.as_os_str().is_empty() => {
            return invalid("file path must not be empty");
        }
        OmaCommand::Editor(EditorCommand::Open { path, .. }) => {
            if path.as_os_str().is_empty()
                || path.as_os_str().len() > MAX_EDITOR_PATH_BYTES
                || path.to_string_lossy().chars().any(char::is_control)
            {
                return invalid(
                    "editor path must be non-empty, at most 4096 bytes, with no control characters",
                );
            }
        }
        OmaCommand::Git(GitCommand::Stage { paths, .. })
        | OmaCommand::Git(GitCommand::Unstage { paths, .. })
        | OmaCommand::Git(GitCommand::Discard { paths, .. }) => {
            if paths.is_empty() || paths.len() > MAX_GIT_PATHS {
                return invalid("git paths must contain 1 to 100 entries");
            }
            if paths.iter().any(|path| {
                path.as_os_str().is_empty()
                    || path.as_os_str().len() > MAX_GIT_PATH_BYTES
                    || path.to_string_lossy().chars().any(char::is_control)
            }) {
                return invalid(
                    "each git path must be non-empty, at most 4096 bytes, with no control characters",
                );
            }
        }
        OmaCommand::Git(GitCommand::StageHunk { path, hunk_id, .. }) => {
            if path.as_os_str().is_empty()
                || path.as_os_str().len() > MAX_GIT_PATH_BYTES
                || path.to_string_lossy().chars().any(char::is_control)
            {
                return invalid(
                    "git hunk path must be non-empty, at most 4096 bytes, with no control characters",
                );
            }
            if *hunk_id == 0 {
                return invalid("git hunk id must be non-zero");
            }
        }
        OmaCommand::Git(GitCommand::Commit { message, .. })
            if message.is_empty()
                || message.len() > MAX_GIT_MESSAGE_BYTES
                || message
                    .chars()
                    .any(|char| char.is_control() && char != '\n' && char != '\t') =>
        {
            // Newlines/tabs separate summary from body; every other
            // control character (notably NUL, which argv cannot carry)
            // is rejected.
            return invalid(
                "commit message must be 1 to 4096 bytes with no control characters besides newline/tab",
            );
        }
        OmaCommand::Git(GitCommand::History { limit, .. })
            if *limit == 0 || *limit > MAX_GIT_HISTORY_LIMIT =>
        {
            return invalid("git history limit must be between 1 and 100 entries");
        }
        OmaCommand::Git(GitCommand::BranchCreate { name, start, .. }) => {
            if !is_branch_ref(name) {
                return invalid(
                    "git branch name must be 1 to 255 bytes with no control characters",
                );
            }
            if start.as_ref().is_some_and(|start| !is_branch_ref(start)) {
                return invalid(
                    "git branch start must be 1 to 255 bytes with no control characters",
                );
            }
        }
        OmaCommand::Git(
            GitCommand::BranchCheckout { name, .. } | GitCommand::BranchDelete { name, .. },
        ) => {
            if !is_branch_ref(name) {
                return invalid(
                    "git branch name must be 1 to 255 bytes with no control characters",
                );
            }
        }
        OmaCommand::Git(GitCommand::BranchRename { old, new, .. }) => {
            for name in [old, new] {
                if !is_branch_ref(name) {
                    return invalid(
                        "git branch name must be 1 to 255 bytes with no control characters",
                    );
                }
            }
        }
        OmaCommand::Git(
            GitCommand::SyncFetch { remote, .. } | GitCommand::SyncPull { remote, .. },
        ) => {
            if remote.as_ref().is_some_and(|remote| !is_branch_ref(remote)) {
                return invalid("git remote must be 1 to 255 bytes with no control characters");
            }
        }
        OmaCommand::Git(GitCommand::StashPush { message, .. }) => {
            if message.is_empty()
                || message.len() > MAX_GIT_MESSAGE_BYTES
                || message.chars().any(char::is_control)
            {
                return invalid("stash message must be 1 to 4096 bytes with no control characters");
            }
        }
        OmaCommand::Git(GitCommand::Blame { path, .. }) => {
            if path.as_os_str().is_empty()
                || path.as_os_str().len() > MAX_GIT_PATH_BYTES
                || path.to_string_lossy().chars().any(char::is_control)
            {
                return invalid(
                    "blame path must be non-empty, at most 4096 bytes, with no control characters",
                );
            }
        }
        OmaCommand::Diff(DiffCommand::ShowCommit {
            old_path,
            path,
            context_lines,
            ..
        }) => {
            if *context_lines > MAX_DIFF_CONTEXT_LINES {
                return invalid("diff context lines must be between 0 and 10");
            }
            for candidate in old_path.iter().chain(std::iter::once(path)) {
                if candidate.as_os_str().is_empty()
                    || candidate.as_os_str().len() > MAX_GIT_PATH_BYTES
                    || candidate.to_string_lossy().chars().any(char::is_control)
                {
                    return invalid(
                        "diff path must be non-empty, at most 4096 bytes, with no control characters",
                    );
                }
            }
        }
        OmaCommand::Diff(DiffCommand::Show {
            path,
            context_lines,
            ..
        }) => {
            if *context_lines > MAX_DIFF_CONTEXT_LINES {
                return invalid("diff context lines must be between 0 and 10");
            }
            if path.as_ref().is_some_and(|path| {
                path.as_os_str().is_empty()
                    || path.as_os_str().len() > MAX_GIT_PATH_BYTES
                    || path.to_string_lossy().chars().any(char::is_control)
            }) {
                return invalid(
                    "diff path must be non-empty, at most 4096 bytes, with no control characters",
                );
            }
        }
        _ => {}
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{GitHistoryScope, PaneId, ProjectId, SessionId, SplitId, TabId};

    #[test]
    fn validates_fraction_names_and_bounded_terminal_commands() {
        assert!(
            validate(&OmaCommand::Pane(PaneCommand::Resize {
                split: SplitId::new(),
                fraction: f32::NAN
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::Project(ProjectCommand::Create {
                name: Some("  ".into()),
                directory: None
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::Project(ProjectCommand::SetDirectory {
                project: ProjectId::new(),
                directory: std::env::temp_dir().join("omaterm-no-such-dir"),
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::Terminal(TerminalCommand::ReadVisible {
                session: SessionId::new(),
                max_lines: 1001,
                max_columns: 10
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::Terminal(TerminalCommand::SendBytes {
                session: SessionId::new(),
                data: vec![0; MAX_SEND_BYTES + 1]
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::Terminal(TerminalCommand::RunCommand {
                session: SessionId::new(),
                argv: vec![]
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::File(FileCommand::List {
                project: ProjectId::new(),
                dir: None,
                limit: Some(0),
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::File(FileCommand::Search {
                project: ProjectId::new(),
                query: String::new(),
                limit: None,
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::File(FileCommand::Open {
                project: ProjectId::new(),
                path: std::path::PathBuf::new(),
            }))
            .is_err()
        );
    }

    #[test]
    fn git_commit_rejects_empty_oversize_and_nul_messages() {
        let project = ProjectId::new();
        assert!(
            validate(&OmaCommand::Git(GitCommand::Commit {
                project,
                message: String::new(),
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::Git(GitCommand::Commit {
                project,
                message: "x".repeat(MAX_GIT_MESSAGE_BYTES + 1),
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::Git(GitCommand::Commit {
                project,
                message: "bad\0message".into(),
            }))
            .is_err()
        );
        // Summary + body with newline/tab passes.
        assert!(
            validate(&OmaCommand::Git(GitCommand::Commit {
                project,
                message: "subject\n\nbody\twith tab".into(),
            }))
            .is_ok()
        );
    }

    #[test]
    fn git_blame_rejects_empty_and_control_paths() {
        let project = ProjectId::new();
        assert!(
            validate(&OmaCommand::Git(GitCommand::Blame {
                project,
                path: std::path::PathBuf::new(),
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::Git(GitCommand::Blame {
                project,
                path: std::path::PathBuf::from("src/main.rs"),
            }))
            .is_ok()
        );
    }

    #[test]
    fn git_stash_push_rejects_empty_and_control_messages() {
        let project = ProjectId::new();
        assert!(
            validate(&OmaCommand::Git(GitCommand::StashPush {
                project,
                message: String::new(),
                untracked: false,
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::Git(GitCommand::StashPush {
                project,
                message: "bad\nmessage".into(),
                untracked: false,
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::Git(GitCommand::StashPush {
                project,
                message: "wip".into(),
                untracked: true,
            }))
            .is_ok()
        );
        assert!(validate(&OmaCommand::Git(GitCommand::StashList { project })).is_ok());
    }

    #[test]
    fn git_branch_names_reject_empty_oversize_and_control() {
        let project = ProjectId::new();
        assert!(
            validate(&OmaCommand::Git(GitCommand::BranchCheckout {
                project,
                name: String::new(),
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::Git(GitCommand::BranchDelete {
                project,
                name: "x".repeat(MAX_GIT_BRANCH_BYTES + 1),
                force: false,
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::Git(GitCommand::BranchRename {
                project,
                old: "ok".into(),
                new: "bad\nname".into(),
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::Git(GitCommand::BranchCreate {
                project,
                name: "feature".into(),
                start: Some(String::new()),
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::Git(GitCommand::BranchCreate {
                project,
                name: "feature".into(),
                start: None,
            }))
            .is_ok()
        );
        assert!(validate(&OmaCommand::Git(GitCommand::BranchList { project })).is_ok());
    }

    #[test]
    fn git_mutations_reject_empty_oversize_and_control_paths() {
        let project = ProjectId::new();
        // Empty path list.
        assert!(
            validate(&OmaCommand::Git(GitCommand::Stage {
                project,
                paths: vec![],
            }))
            .is_err()
        );
        // Over fan-out.
        assert!(
            validate(&OmaCommand::Git(GitCommand::Discard {
                project,
                paths: vec![std::path::PathBuf::from("a"); MAX_GIT_PATHS + 1],
            }))
            .is_err()
        );
        // Control character in a path.
        assert!(
            validate(&OmaCommand::Git(GitCommand::Unstage {
                project,
                paths: vec![std::path::PathBuf::from("bad\npath")],
            }))
            .is_err()
        );
        // Empty single path.
        assert!(
            validate(&OmaCommand::Git(GitCommand::Stage {
                project,
                paths: vec![std::path::PathBuf::new()],
            }))
            .is_err()
        );
        // Boundary: exactly the fan-out cap with ordinary paths passes, and
        // status carries no fields to reject.
        assert!(
            validate(&OmaCommand::Git(GitCommand::Stage {
                project,
                paths: vec![std::path::PathBuf::from("src/main.rs"); MAX_GIT_PATHS],
            }))
            .is_ok()
        );
        assert!(validate(&OmaCommand::Git(GitCommand::Status { project })).is_ok());
    }

    #[test]
    fn git_history_limit_is_bounded_before_any_git_process_can_run() {
        let project = ProjectId::new();
        for limit in [0, MAX_GIT_HISTORY_LIMIT + 1] {
            assert!(
                validate(&OmaCommand::Git(GitCommand::History {
                    project,
                    scope: GitHistoryScope::CurrentHead,
                    limit,
                }))
                .is_err()
            );
        }
        assert!(
            validate(&OmaCommand::Git(GitCommand::History {
                project,
                scope: GitHistoryScope::AllLocalBranches,
                limit: MAX_GIT_HISTORY_LIMIT,
            }))
            .is_ok()
        );
    }

    #[test]
    fn diff_show_rejects_over_context_and_control_paths() {
        let project = ProjectId::new();
        assert!(
            validate(&OmaCommand::Diff(DiffCommand::Show {
                project,
                path: None,
                staged: false,
                context_lines: MAX_DIFF_CONTEXT_LINES + 1,
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::Diff(DiffCommand::Show {
                project,
                path: Some(std::path::PathBuf::from("bad\npath")),
                staged: false,
                context_lines: 3,
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::Diff(DiffCommand::Show {
                project,
                path: Some(std::path::PathBuf::from("src/main.rs")),
                staged: true,
                context_lines: MAX_DIFF_CONTEXT_LINES,
            }))
            .is_ok()
        );
        assert!(
            validate(&OmaCommand::Diff(DiffCommand::ListFiles {
                project,
                staged: false
            }))
            .is_ok()
        );
    }

    #[test]
    fn editor_open_rejects_empty_oversize_and_control_paths() {
        let project = ProjectId::new();
        assert!(
            validate(&OmaCommand::Editor(EditorCommand::Open {
                project,
                path: std::path::PathBuf::new(),
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::Editor(EditorCommand::Open {
                project,
                path: std::path::PathBuf::from("bad\npath"),
            }))
            .is_err()
        );
        let mut oversize = String::from("a");
        oversize.push_str(&"b".repeat(MAX_EDITOR_PATH_BYTES));
        assert!(
            validate(&OmaCommand::Editor(EditorCommand::Open {
                project,
                path: std::path::PathBuf::from(oversize),
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::Editor(EditorCommand::Open {
                project,
                path: std::path::PathBuf::from("src/main.rs"),
            }))
            .is_ok()
        );
        // Lifecycle variants address a document ID; existence is
        // owner-checked, so validation accepts them unconditionally.
        let document = crate::DocumentId::new();
        assert!(validate(&OmaCommand::Editor(EditorCommand::Close { document })).is_ok());
        assert!(validate(&OmaCommand::Editor(EditorCommand::Save { document })).is_ok());
        assert!(validate(&OmaCommand::Editor(EditorCommand::Revert { document })).is_ok());
    }

    #[test]
    fn editor_error_codes_have_stable_wire_strings() {
        assert_eq!(ErrorCode::DocumentNotOpen.as_str(), "document_not_open");
        assert_eq!(ErrorCode::DocumentConflict.as_str(), "document_conflict");
        assert_eq!(ErrorCode::DocumentTooLarge.as_str(), "document_too_large");
        assert_eq!(ErrorCode::NotTextFile.as_str(), "not_text_file");
    }

    #[test]
    fn accepts_boundary_values_and_empty_optional_names() {
        assert!(
            validate(&OmaCommand::Pane(PaneCommand::Resize {
                split: SplitId::new(),
                fraction: 0.1
            }))
            .is_ok()
        );
        assert!(
            validate(&OmaCommand::Project(ProjectCommand::Create {
                name: None,
                directory: Some(std::env::temp_dir())
            }))
            .is_ok()
        );
        assert!(
            validate(&OmaCommand::Project(ProjectCommand::SetDirectory {
                project: ProjectId::new(),
                directory: std::env::temp_dir(),
            }))
            .is_ok()
        );
        assert!(
            validate(&OmaCommand::Tab(TabCommand::Create {
                project: ProjectId::new(),
                name: None
            }))
            .is_ok()
        );
        assert!(
            validate(&OmaCommand::Pane(PaneCommand::Focus {
                pane: PaneId::new()
            }))
            .is_ok()
        );
        assert!(
            validate(&OmaCommand::Tab(TabCommand::Rename {
                tab: TabId::new(),
                name: "Tests".into()
            }))
            .is_ok()
        );
    }
}
