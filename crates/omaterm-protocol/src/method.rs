//! Strict v1 method parameters. Target resolution and authorization belong to
//! the desktop owner, not the wire or socket transport.
use serde::Deserialize;
use serde_json::Value;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct MethodError(pub &'static str);

#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Empty {}

#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ProjectCreate {
    pub directory: Option<String>,
    pub name: Option<String>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ProjectSelector {
    pub project_id: String,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ProjectSetDirectory {
    pub project_id: String,
    pub directory: String,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ProjectSetActiveRepo {
    pub project_id: String,
    pub repo: String,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct OptionalProject {
    pub project_id: Option<String>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TabCreate {
    pub project_id: Option<String>,
    pub name: Option<String>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TabSelector {
    pub tab_id: String,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct OptionalTab {
    pub tab_id: Option<String>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PaneSelector {
    pub pane_id: String,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PaneSplit {
    pub pane_id: Option<String>,
    pub direction: String,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PaneResize {
    pub split_id: String,
    pub fraction: f32,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TerminalCreate {
    pub project_id: Option<String>,
    pub directory: Option<String>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TerminalSend {
    pub pane_id: String,
    pub data: String,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TerminalRun {
    pub pane_id: String,
    pub argv: Vec<String>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TerminalRead {
    pub pane_id: String,
    pub lines: Option<usize>,
    pub columns: Option<usize>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct HistoryList {
    pub pane_id: String,
    pub limit: Option<usize>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FileList {
    pub project_id: Option<String>,
    pub dir: Option<String>,
    pub limit: Option<usize>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FileSearch {
    pub project_id: Option<String>,
    pub query: String,
    pub limit: Option<usize>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FileOpen {
    pub project_id: Option<String>,
    pub path: String,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GitStatus {
    pub project_id: Option<String>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GitHistory {
    pub project_id: Option<String>,
    pub scope: Option<String>,
    pub limit: Option<usize>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GitCommitFiles {
    pub project_id: Option<String>,
    pub commit: String,
    /// Full parent OID, or `"empty_tree"` for a root commit.
    pub parent: Option<String>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GitBranchList {
    pub project_id: Option<String>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GitBranchCreate {
    pub project_id: Option<String>,
    pub name: String,
    pub start: Option<String>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GitBranchCheckout {
    pub project_id: Option<String>,
    pub name: String,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GitBranchDelete {
    pub project_id: Option<String>,
    pub name: String,
    pub force: bool,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GitBranchRename {
    pub project_id: Option<String>,
    pub old: String,
    pub new: String,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GitSyncFetch {
    pub project_id: Option<String>,
    pub remote: Option<String>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GitSyncPull {
    pub project_id: Option<String>,
    pub remote: Option<String>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GitSyncPush {
    pub project_id: Option<String>,
    pub set_upstream: Option<bool>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GitStashList {
    pub project_id: Option<String>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GitStashPush {
    pub project_id: Option<String>,
    pub message: String,
    pub untracked: Option<bool>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GitStashIndex {
    pub project_id: Option<String>,
    pub index: usize,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GitBlame {
    pub project_id: Option<String>,
    pub path: String,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct DiffShowCommit {
    pub project_id: Option<String>,
    pub commit: String,
    /// Full parent OID, or `"empty_tree"` for a root commit.
    pub parent: Option<String>,
    pub old_path: Option<String>,
    pub path: String,
    pub context_lines: Option<u8>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GitPaths {
    pub project_id: Option<String>,
    pub paths: Vec<String>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GitCommit {
    pub project_id: Option<String>,
    pub message: String,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct GitStageHunk {
    pub project_id: Option<String>,
    pub path: String,
    pub hunk_id: u64,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct DiffShow {
    pub project_id: Option<String>,
    pub path: Option<String>,
    pub staged: Option<bool>,
    pub context_lines: Option<u8>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct DiffListFiles {
    pub project_id: Option<String>,
    pub staged: Option<bool>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ProcessList {
    pub project_id: Option<String>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ProcessKill {
    pub project_id: Option<String>,
    pub pid: u32,
}

#[derive(Clone)]
pub enum Method {
    ProjectList(Empty),
    ProjectCreate(ProjectCreate),
    ProjectSelect(ProjectSelector),
    ProjectSetDirectory(ProjectSetDirectory),
    ProjectRoot(OptionalProject),
    ProjectRepos(OptionalProject),
    ProjectSetActiveRepo(ProjectSetActiveRepo),
    TabList(OptionalProject),
    TabCreate(TabCreate),
    TabClose(TabSelector),
    PaneList(OptionalTab),
    PaneSplit(PaneSplit),
    PaneClose(PaneSelector),
    PaneFocus(PaneSelector),
    PaneResize(PaneResize),
    PaneEqualize(OptionalTab),
    TerminalList(Empty),
    TerminalCreate(TerminalCreate),
    TerminalSend(TerminalSend),
    TerminalRun(TerminalRun),
    TerminalRead(TerminalRead),
    HistoryEnable(Empty),
    HistoryDisable(Empty),
    HistoryStatus(Empty),
    HistoryList(HistoryList),
    HistoryPause(PaneSelector),
    HistoryResume(PaneSelector),
    HistoryClearPane(PaneSelector),
    HistoryClearProject(ProjectSelector),
    HistoryClearAll(Empty),
    FileList(FileList),
    FileSearch(FileSearch),
    FileOpen(FileOpen),
    GitStatus(GitStatus),
    GitHistory(GitHistory),
    GitCommitFiles(GitCommitFiles),
    GitBranchList(GitBranchList),
    GitBranchCreate(GitBranchCreate),
    GitBranchCheckout(GitBranchCheckout),
    GitBranchDelete(GitBranchDelete),
    GitBranchRename(GitBranchRename),
    GitSyncFetch(GitSyncFetch),
    GitSyncPull(GitSyncPull),
    GitSyncPush(GitSyncPush),
    GitStashList(GitStashList),
    GitStashPush(GitStashPush),
    GitStashApply(GitStashIndex),
    GitStashPop(GitStashIndex),
    GitStashDrop(GitStashIndex),
    GitBlame(GitBlame),
    GitStage(GitPaths),
    GitStageHunk(GitStageHunk),
    GitUnstage(GitPaths),
    GitDiscard(GitPaths),
    GitCommit(GitCommit),
    DiffShow(DiffShow),
    DiffShowCommit(DiffShowCommit),
    DiffListFiles(DiffListFiles),
    ProcessList(ProcessList),
    ProcessKill(ProcessKill),
}

impl Method {
    pub fn decode(name: &str, params: Value) -> Result<Self, MethodError> {
        let params = if params.is_null() {
            serde_json::json!({})
        } else {
            params
        };
        macro_rules! decode {
            ($ty:ident, $variant:ident) => {
                serde_json::from_value::<$ty>(params)
                    .map(Self::$variant)
                    .map_err(|_| MethodError("invalid or unknown method parameter"))
            };
        }
        match name {
            "project.list" => decode!(Empty, ProjectList),
            "project.create" => decode!(ProjectCreate, ProjectCreate),
            "project.select" => decode!(ProjectSelector, ProjectSelect),
            "project.set-directory" => decode!(ProjectSetDirectory, ProjectSetDirectory),
            "project.root" => decode!(OptionalProject, ProjectRoot),
            "project.repos" => decode!(OptionalProject, ProjectRepos),
            "project.set-active-repo" => decode!(ProjectSetActiveRepo, ProjectSetActiveRepo),
            "tab.list" => decode!(OptionalProject, TabList),
            "tab.create" => decode!(TabCreate, TabCreate),
            "tab.close" => decode!(TabSelector, TabClose),
            "pane.list" => decode!(OptionalTab, PaneList),
            "pane.split" => decode!(PaneSplit, PaneSplit),
            "pane.close" => decode!(PaneSelector, PaneClose),
            "pane.focus" => decode!(PaneSelector, PaneFocus),
            "pane.resize" => decode!(PaneResize, PaneResize),
            "pane.equalize" => decode!(OptionalTab, PaneEqualize),
            "terminal.list" => decode!(Empty, TerminalList),
            "terminal.create" => decode!(TerminalCreate, TerminalCreate),
            "terminal.send" => decode!(TerminalSend, TerminalSend),
            "terminal.run" => decode!(TerminalRun, TerminalRun),
            "terminal.read" => decode!(TerminalRead, TerminalRead),
            "history.enable" => decode!(Empty, HistoryEnable),
            "history.disable" => decode!(Empty, HistoryDisable),
            "history.status" => decode!(Empty, HistoryStatus),
            "history.list" => decode!(HistoryList, HistoryList),
            "history.pause" => decode!(PaneSelector, HistoryPause),
            "history.resume" => decode!(PaneSelector, HistoryResume),
            "history.clear-pane" => decode!(PaneSelector, HistoryClearPane),
            "history.clear-project" => decode!(ProjectSelector, HistoryClearProject),
            "history.clear-all" => decode!(Empty, HistoryClearAll),
            "file.list" => decode!(FileList, FileList),
            "file.search" => decode!(FileSearch, FileSearch),
            "file.open" => decode!(FileOpen, FileOpen),
            "git.status" => decode!(GitStatus, GitStatus),
            "git.history" => decode!(GitHistory, GitHistory),
            "git.commit-files" => decode!(GitCommitFiles, GitCommitFiles),
            "git.branch-list" => decode!(GitBranchList, GitBranchList),
            "git.branch-create" => decode!(GitBranchCreate, GitBranchCreate),
            "git.branch-checkout" => decode!(GitBranchCheckout, GitBranchCheckout),
            "git.branch-delete" => decode!(GitBranchDelete, GitBranchDelete),
            "git.branch-rename" => decode!(GitBranchRename, GitBranchRename),
            "git.fetch" => decode!(GitSyncFetch, GitSyncFetch),
            "git.pull" => decode!(GitSyncPull, GitSyncPull),
            "git.push" => decode!(GitSyncPush, GitSyncPush),
            "git.stash-list" => decode!(GitStashList, GitStashList),
            "git.stash-push" => decode!(GitStashPush, GitStashPush),
            "git.stash-apply" => decode!(GitStashIndex, GitStashApply),
            "git.stash-pop" => decode!(GitStashIndex, GitStashPop),
            "git.stash-drop" => decode!(GitStashIndex, GitStashDrop),
            "git.blame" => decode!(GitBlame, GitBlame),
            "git.stage" => decode!(GitPaths, GitStage),
            "git.stage-hunk" => decode!(GitStageHunk, GitStageHunk),
            "git.unstage" => decode!(GitPaths, GitUnstage),
            "git.discard" => decode!(GitPaths, GitDiscard),
            "git.commit" => decode!(GitCommit, GitCommit),
            "diff.show" => decode!(DiffShow, DiffShow),
            "diff.show-commit" => decode!(DiffShowCommit, DiffShowCommit),
            "diff.list-files" => decode!(DiffListFiles, DiffListFiles),
            "process.list" => decode!(ProcessList, ProcessList),
            "process.kill" => decode!(ProcessKill, ProcessKill),
            _ => Err(MethodError("unknown method")),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn all_methods_decode_and_reject_unknown_parameters() {
        let cases = [
            ("project.list", serde_json::json!({})),
            (
                "git.stage-hunk",
                serde_json::json!({"path":"a.txt","hunk_id":42}),
            ),
            ("project.create", serde_json::json!({"directory":"/tmp"})),
            ("project.select", serde_json::json!({"project_id":"id"})),
            (
                "project.set-directory",
                serde_json::json!({"project_id":"id","directory":"/tmp"}),
            ),
            ("project.root", serde_json::json!({})),
            ("project.repos", serde_json::json!({})),
            (
                "project.set-active-repo",
                serde_json::json!({"project_id":"id","repo":"api"}),
            ),
            ("tab.list", serde_json::json!({})),
            ("tab.create", serde_json::json!({"name":"test"})),
            ("tab.close", serde_json::json!({"tab_id":"id"})),
            ("pane.list", serde_json::json!({})),
            ("pane.split", serde_json::json!({"direction":"right"})),
            ("pane.close", serde_json::json!({"pane_id":"id"})),
            ("pane.focus", serde_json::json!({"pane_id":"id"})),
            (
                "pane.resize",
                serde_json::json!({"split_id":"id","fraction":0.5}),
            ),
            ("pane.equalize", serde_json::json!({})),
            ("terminal.list", serde_json::json!({})),
            ("terminal.create", serde_json::json!({})),
            (
                "terminal.send",
                serde_json::json!({"pane_id":"id","data":"AQ=="}),
            ),
            (
                "terminal.run",
                serde_json::json!({"pane_id":"id","argv":["true"]}),
            ),
            (
                "terminal.read",
                serde_json::json!({"pane_id":"id","lines":20}),
            ),
            ("history.enable", serde_json::json!({})),
            ("history.disable", serde_json::json!({})),
            ("history.status", serde_json::json!({})),
            (
                "history.list",
                serde_json::json!({"pane_id":"id","limit":20}),
            ),
            ("history.pause", serde_json::json!({"pane_id":"id"})),
            ("history.resume", serde_json::json!({"pane_id":"id"})),
            ("history.clear-pane", serde_json::json!({"pane_id":"id"})),
            (
                "history.clear-project",
                serde_json::json!({"project_id":"id"}),
            ),
            ("history.clear-all", serde_json::json!({})),
            ("file.list", serde_json::json!({})),
            ("file.search", serde_json::json!({"query": "main"})),
            ("file.open", serde_json::json!({"path": "src/main.rs"})),
            ("git.status", serde_json::json!({})),
            (
                "git.history",
                serde_json::json!({"scope":"current_head","limit":50}),
            ),
            (
                "git.commit-files",
                serde_json::json!({"commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}),
            ),
            ("git.stage", serde_json::json!({"paths": ["a.txt"]})),
            ("git.unstage", serde_json::json!({"paths": ["a.txt"]})),
            ("git.discard", serde_json::json!({"paths": ["a.txt"]})),
            ("git.commit", serde_json::json!({"message": "hello"})),
            (
                "diff.show",
                serde_json::json!({"path": "src/main.rs", "staged": true, "context_lines": 5}),
            ),
            (
                "diff.show-commit",
                serde_json::json!({"commit": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "path": "src/main.rs"}),
            ),
            ("diff.list-files", serde_json::json!({"staged": false})),
            ("process.list", serde_json::json!({})),
            ("process.kill", serde_json::json!({"pid": 123})),
            ("git.branch-list", serde_json::json!({})),
            (
                "git.branch-create",
                serde_json::json!({"name": "feature", "start": "main"}),
            ),
            (
                "git.branch-checkout",
                serde_json::json!({"name": "feature"}),
            ),
            (
                "git.branch-delete",
                serde_json::json!({"name": "feature", "force": false}),
            ),
            (
                "git.branch-rename",
                serde_json::json!({"old": "feature", "new": "topic"}),
            ),
            ("git.fetch", serde_json::json!({})),
            ("git.pull", serde_json::json!({})),
            ("git.push", serde_json::json!({})),
            ("git.stash-list", serde_json::json!({})),
            ("git.stash-push", serde_json::json!({"message": "wip"})),
            ("git.stash-apply", serde_json::json!({"index": 0})),
            ("git.stash-pop", serde_json::json!({"index": 0})),
            ("git.stash-drop", serde_json::json!({"index": 0})),
            ("git.blame", serde_json::json!({"path": "src/main.rs"})),
        ];
        assert_eq!(cases.len(), 60);
        for (name, params) in cases {
            assert!(Method::decode(name, params.clone()).is_ok(), "{name}");
            let mut unknown = params;
            unknown
                .as_object_mut()
                .unwrap()
                .insert("unknown".into(), serde_json::json!(true));
            assert!(Method::decode(name, unknown).is_err(), "{name}");
        }
        assert!(Method::decode("unknown", serde_json::json!({})).is_err());
    }
}
