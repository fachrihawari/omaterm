//! Transport-to-domain conversion on the desktop owner. Socket threads never
//! resolve selectors or access workspace state.
use base64::Engine;
use omaterm_core::{
    CommandContext, CommandOutput, CommandResult, DiffCommand, FileCommand, GitCommand,
    HistoryCommand, OmaCommand, PaneCommand, PaneContent, PaneId, ProjectCommand, ProjectId,
    SessionId, SplitDirection, SplitId, TabCommand, TabId, TerminalCommand,
};
use omaterm_protocol::{
    IpcRequest, IpcResponse, MAX_ARG_COUNT, MAX_ARGUMENT_BYTES, MAX_DIFF_CONTEXT_LINES,
    MAX_FILE_ENTRIES, MAX_GIT_MESSAGE_BYTES, MAX_GIT_PATH_BYTES, MAX_GIT_PATHS,
    MAX_JOURNAL_ENTRIES, MAX_READ_COLUMNS, MAX_READ_LINES, MAX_SEND_BYTES, method::Method,
};
use serde_json::{Value, json};

use crate::router::CommandRouter;

fn invalid(id: &str, message: &str) -> Box<IpcResponse> {
    Box::new(IpcResponse::failure(
        id.to_owned(),
        "invalid_request",
        message,
    ))
}
fn parse_id<T>(raw: &str, make: impl FnOnce(uuid::Uuid) -> T) -> Result<T, &'static str> {
    uuid::Uuid::parse_str(raw)
        .map(make)
        .map_err(|_| "invalid UUID selector")
}
fn project(raw: &str) -> Result<ProjectId, &'static str> {
    parse_id(raw, ProjectId)
}
fn tab(raw: &str) -> Result<TabId, &'static str> {
    parse_id(raw, TabId)
}
fn pane(raw: &str) -> Result<PaneId, &'static str> {
    parse_id(raw, PaneId)
}
fn split(raw: &str) -> Result<SplitId, &'static str> {
    parse_id(raw, SplitId)
}
fn direction(raw: &str) -> Result<SplitDirection, &'static str> {
    match raw {
        "left" => Ok(SplitDirection::Left),
        "right" => Ok(SplitDirection::Right),
        "up" => Ok(SplitDirection::Up),
        "down" => Ok(SplitDirection::Down),
        _ => Err("invalid split direction"),
    }
}
fn text(raw: &str) -> Result<&str, &'static str> {
    if raw.len() > 4096 || raw.chars().any(char::is_control) {
        Err("structured field exceeds limit or contains a control character")
    } else {
        Ok(raw)
    }
}

/// Bounded commit message: 1–4096 bytes, newlines/tabs allowed for the
/// body, every other control character rejected (mirrors core
/// validation; the router re-validates).
fn git_message(raw: &str) -> Result<&str, &'static str> {
    if raw.is_empty()
        || raw.len() > MAX_GIT_MESSAGE_BYTES
        || raw
            .chars()
            .any(|char| char.is_control() && char != '\n' && char != '\t')
    {
        Err("commit message must be 1 to 4096 bytes with no control characters besides newline/tab")
    } else {
        Ok(raw)
    }
}

/// Bounded git path vectors: 1–100 entries, each at most 4 KiB with no
/// control characters (mirrors core validation; the router re-validates).
fn git_paths(raw: &[String]) -> Result<Vec<std::path::PathBuf>, &'static str> {
    if raw.is_empty() || raw.len() > MAX_GIT_PATHS {
        return Err("git paths must contain 1 to 100 entries");
    }
    raw.iter()
        .map(|path| {
            if path.is_empty() || path.len() > MAX_GIT_PATH_BYTES || path.chars().any(char::is_control) {
                Err("each git path must be non-empty, at most 4096 bytes, with no control characters")
            } else {
                Ok(std::path::PathBuf::from(path))
            }
        })
        .collect()
}

pub fn map_request(
    request: &IpcRequest,
    context: CommandContext,
    router: &CommandRouter,
) -> Result<OmaCommand, Box<IpcResponse>> {
    let id = &request.request_id;
    let method = Method::decode(&request.method, request.params.clone())
        .map_err(|error| invalid(id, error.0))?;
    let resolve_project = |raw: Option<String>| -> Result<ProjectId, &'static str> {
        raw.as_deref()
            .map(project)
            .transpose()?
            .or_else(|| match context {
                CommandContext::Project(scope) => Some(scope),
                CommandContext::LocalUser => router.selected_project_id(),
            })
            .ok_or("no selected project")
    };
    let resolve_tab = |raw: Option<String>| -> Result<TabId, &'static str> {
        raw.as_deref()
            .map(tab)
            .transpose()?
            .or_else(|| {
                let owner = match context {
                    CommandContext::Project(scope) => router.window().project(scope),
                    CommandContext::LocalUser => router.window().selected_project(),
                };
                owner.and_then(|project| project.selected_tab)
            })
            .ok_or("no selected tab")
    };
    let resolve_pane = |raw: Option<String>| -> Result<PaneId, &'static str> {
        raw.as_deref()
            .map(pane)
            .transpose()?
            .or_else(|| {
                let owner = match context {
                    CommandContext::Project(scope) => router.window().project(scope),
                    CommandContext::LocalUser => router.window().selected_project(),
                };
                owner
                    .and_then(|project| project.selected_tab())
                    .map(|tab| tab.focused_pane)
            })
            .ok_or("no focused pane")
    };
    let session = |raw: &str| -> Result<SessionId, &'static str> {
        let pane = pane(raw)?;
        router
            .window()
            .projects
            .iter()
            .flat_map(|p| &p.tabs)
            .find_map(|tab| tab.tree.find(pane))
            .and_then(|pane| match pane.content {
                PaneContent::Terminal(id) => Some(id),
                _ => None,
            })
            .ok_or("pane has no live terminal")
    };
    let command = (|| -> Result<OmaCommand, &'static str> {
        Ok(match method {
            Method::ProjectList(_) => OmaCommand::Project(ProjectCommand::List),
            Method::ProjectCreate(p) => OmaCommand::Project(ProjectCommand::Create {
                name: p.name.as_deref().map(text).transpose()?.map(str::to_owned),
                directory: p
                    .directory
                    .as_deref()
                    .map(text)
                    .transpose()?
                    .map(std::path::PathBuf::from),
            }),
            Method::ProjectSelect(p) => OmaCommand::Project(ProjectCommand::Select {
                project: project(&p.project_id)?,
            }),
            Method::ProjectSetDirectory(p) => OmaCommand::Project(ProjectCommand::SetDirectory {
                project: project(&p.project_id)?,
                directory: std::path::PathBuf::from(text(&p.directory)?),
            }),
            Method::ProjectRoot(p) => OmaCommand::Project(ProjectCommand::Root {
                project: resolve_project(p.project_id)?,
            }),
            Method::TabList(p) => OmaCommand::Tab(TabCommand::List {
                project: resolve_project(p.project_id)?,
            }),
            Method::TabCreate(p) => OmaCommand::Tab(TabCommand::Create {
                project: resolve_project(p.project_id)?,
                name: p.name.as_deref().map(text).transpose()?.map(str::to_owned),
            }),
            Method::TabClose(p) => OmaCommand::Tab(TabCommand::Close {
                tab: tab(&p.tab_id)?,
            }),
            Method::PaneList(p) => OmaCommand::Pane(PaneCommand::List {
                tab: Some(resolve_tab(p.tab_id)?),
            }),
            Method::PaneSplit(p) => OmaCommand::Pane(PaneCommand::Split {
                target: resolve_pane(p.pane_id)?,
                direction: direction(&p.direction)?,
            }),
            Method::PaneClose(p) => OmaCommand::Pane(PaneCommand::Close {
                pane: pane(&p.pane_id)?,
            }),
            Method::PaneFocus(p) => OmaCommand::Pane(PaneCommand::Focus {
                pane: pane(&p.pane_id)?,
            }),
            Method::PaneResize(p) => OmaCommand::Pane(PaneCommand::Resize {
                split: split(&p.split_id)?,
                fraction: p.fraction,
            }),
            Method::PaneEqualize(p) => OmaCommand::Pane(PaneCommand::Equalize {
                tab: resolve_tab(p.tab_id)?,
            }),
            Method::TerminalList(_) => OmaCommand::Terminal(TerminalCommand::List),
            Method::TerminalCreate(p) => OmaCommand::Terminal(TerminalCommand::Create {
                project: resolve_project(p.project_id)?,
                directory: p
                    .directory
                    .as_deref()
                    .map(text)
                    .transpose()?
                    .map(std::path::PathBuf::from),
            }),
            Method::TerminalSend(p) => {
                if p.data.len() > MAX_SEND_BYTES.div_ceil(3) * 4 {
                    return Err("terminal.send exceeds the byte limit");
                }
                let data = base64::engine::general_purpose::STANDARD
                    .decode(p.data)
                    .map_err(|_| "invalid base64 terminal bytes")?;
                if data.len() > MAX_SEND_BYTES {
                    return Err("terminal.send exceeds the byte limit");
                }
                OmaCommand::Terminal(TerminalCommand::SendBytes {
                    session: session(&p.pane_id)?,
                    data,
                })
            }
            Method::TerminalRun(p) => {
                if p.argv.is_empty()
                    || p.argv.len() > MAX_ARG_COUNT
                    || p.argv
                        .iter()
                        .any(|arg| arg.len() > MAX_ARGUMENT_BYTES || arg.contains('\0'))
                {
                    return Err("invalid or oversized argv");
                }
                OmaCommand::Terminal(TerminalCommand::RunCommand {
                    session: session(&p.pane_id)?,
                    argv: p.argv,
                })
            }
            Method::TerminalRead(p) => {
                let lines = p.lines.unwrap_or(100);
                let columns = p.columns.unwrap_or(200);
                if lines == 0
                    || columns == 0
                    || lines > MAX_READ_LINES
                    || columns > MAX_READ_COLUMNS
                {
                    return Err("terminal.read exceeds the viewport limit");
                }
                OmaCommand::Terminal(TerminalCommand::ReadVisible {
                    session: session(&p.pane_id)?,
                    max_lines: lines,
                    max_columns: columns,
                })
            }
            Method::HistoryEnable(_) => OmaCommand::History(HistoryCommand::EnablePersistence),
            Method::HistoryDisable(_) => OmaCommand::History(HistoryCommand::DisablePersistence),
            Method::HistoryStatus(_) => OmaCommand::History(HistoryCommand::Status),
            Method::HistoryList(p) => {
                let limit = p.limit.unwrap_or(100);
                if limit == 0 || limit > MAX_JOURNAL_ENTRIES {
                    return Err("history.list exceeds the journal limit");
                }
                OmaCommand::History(HistoryCommand::ListJournal {
                    pane: pane(&p.pane_id)?,
                    limit,
                })
            }
            Method::HistoryPause(p) => OmaCommand::History(HistoryCommand::PausePane {
                pane: pane(&p.pane_id)?,
            }),
            Method::HistoryResume(p) => OmaCommand::History(HistoryCommand::ResumePane {
                pane: pane(&p.pane_id)?,
            }),
            Method::HistoryClearPane(p) => OmaCommand::History(HistoryCommand::ClearPane {
                pane: pane(&p.pane_id)?,
            }),
            Method::HistoryClearProject(p) => OmaCommand::History(HistoryCommand::ClearProject {
                project: project(&p.project_id)?,
            }),
            Method::HistoryClearAll(_) => OmaCommand::History(HistoryCommand::ClearWorkspace),
            Method::FileList(p) => {
                let limit = p.limit.unwrap_or(100);
                if limit == 0 || limit > MAX_FILE_ENTRIES {
                    return Err("file.list exceeds the entry limit");
                }
                OmaCommand::File(FileCommand::List {
                    project: resolve_project(p.project_id)?,
                    dir: p
                        .dir
                        .as_deref()
                        .map(text)
                        .transpose()?
                        .map(std::path::PathBuf::from),
                    limit: p.limit,
                })
            }
            Method::FileSearch(p) => {
                let limit = p.limit.unwrap_or(100);
                if limit == 0 || limit > MAX_FILE_ENTRIES {
                    return Err("file.search exceeds the entry limit");
                }
                OmaCommand::File(FileCommand::Search {
                    project: resolve_project(p.project_id)?,
                    query: text(&p.query)?.to_owned(),
                    limit: p.limit,
                })
            }
            Method::FileOpen(p) => OmaCommand::File(FileCommand::Open {
                project: resolve_project(p.project_id)?,
                path: std::path::PathBuf::from(text(&p.path)?),
            }),
            Method::GitStatus(p) => OmaCommand::Git(GitCommand::Status {
                project: resolve_project(p.project_id)?,
            }),
            Method::GitStage(p) => OmaCommand::Git(GitCommand::Stage {
                project: resolve_project(p.project_id)?,
                paths: git_paths(&p.paths)?,
            }),
            Method::GitUnstage(p) => OmaCommand::Git(GitCommand::Unstage {
                project: resolve_project(p.project_id)?,
                paths: git_paths(&p.paths)?,
            }),
            Method::GitDiscard(p) => OmaCommand::Git(GitCommand::Discard {
                project: resolve_project(p.project_id)?,
                paths: git_paths(&p.paths)?,
            }),
            Method::GitCommit(p) => OmaCommand::Git(GitCommand::Commit {
                project: resolve_project(p.project_id)?,
                message: git_message(&p.message)?.to_owned(),
            }),
            Method::DiffShow(p) => {
                let context = p.context_lines.unwrap_or(3);
                if context > MAX_DIFF_CONTEXT_LINES {
                    return Err("diff.show exceeds the context-line limit");
                }
                OmaCommand::Diff(DiffCommand::Show {
                    project: resolve_project(p.project_id)?,
                    path: p
                        .path
                        .as_deref()
                        .map(text)
                        .transpose()?
                        .map(std::path::PathBuf::from),
                    staged: p.staged.unwrap_or(false),
                    context_lines: context,
                })
            }
            Method::DiffListFiles(p) => OmaCommand::Diff(DiffCommand::ListFiles {
                project: resolve_project(p.project_id)?,
                staged: p.staged.unwrap_or(false),
            }),
        })
    })()
    .map_err(|message| invalid(id, message))?;
    Ok(command)
}

pub fn response(request_id: String, result: CommandResult) -> IpcResponse {
    match result {
        CommandResult::Err(error) => {
            IpcResponse::failure(request_id, error.code.as_str(), error.message)
        }
        CommandResult::Ok(output) => IpcResponse::success(request_id, output_json(output)),
    }
}

fn output_json(output: CommandOutput) -> Value {
    const LIST_LIMIT: usize = 128;
    match output {
        CommandOutput::Unit => json!({}),
        CommandOutput::Pending { operation_id } => json!({"operation_id":operation_id}),
        CommandOutput::ProjectCreated {
            project,
            tab,
            pane,
            session,
        } => {
            json!({"project_id":project.0.to_string(),"tab_id":tab.0.to_string(),"pane_id":pane.0.to_string(),"session_id":session.0.to_string()})
        }
        CommandOutput::TabCreated { tab, pane, session }
        | CommandOutput::TerminalCreated { tab, pane, session } => {
            json!({"tab_id":tab.0.to_string(),"pane_id":pane.0.to_string(),"session_id":session.0.to_string()})
        }
        CommandOutput::PaneSplit { pane, session } => {
            json!({"new_pane_id":pane.0.to_string(),"new_session_id":session.0.to_string()})
        }
        CommandOutput::RunSubmitted => json!({"submitted":true}),
        CommandOutput::TerminalOutput {
            text,
            truncated,
            lines,
            columns,
        } => json!({"text":text,"truncated":truncated,"lines":lines,"columns":columns}),
        CommandOutput::ProjectList(items) => {
            let truncated = items.len() > LIST_LIMIT;
            json!({"projects":items.into_iter().take(LIST_LIMIT).map(|p| json!({"id":p.id.0.to_string(),"name":p.name,"directory":p.directory,"selected":p.selected,"tab_count":p.tab_count})).collect::<Vec<_>>(),"truncated":truncated})
        }
        CommandOutput::TabList(items) => {
            let truncated = items.len() > LIST_LIMIT;
            json!({"tabs":items.into_iter().take(LIST_LIMIT).map(|t| json!({"id":t.id.0.to_string(),"project_id":t.project.0.to_string(),"name":t.name,"selected":t.selected,"focused_pane_id":t.focused_pane.map(|p| p.0.to_string()),"pane_count":t.pane_count})).collect::<Vec<_>>(),"truncated":truncated})
        }
        CommandOutput::PaneList(items) => {
            let truncated = items.len() > LIST_LIMIT;
            json!({"panes":items.into_iter().take(LIST_LIMIT).map(|p| json!({"id":p.id.0.to_string(),"project_id":p.project.0.to_string(),"tab_id":p.tab.0.to_string(),"session_id":p.session.map(|s| s.0.to_string()),"focused":p.focused,"x":p.x,"y":p.y,"width":p.width,"height":p.height,"splits":p.splits.into_iter().map(|s| json!({"id":s.id.0.to_string(),"axis":match s.axis { omaterm_core::SplitAxis::Horizontal => "horizontal", omaterm_core::SplitAxis::Vertical => "vertical", },"fraction":s.fraction})).collect::<Vec<_>>()})).collect::<Vec<_>>(),"truncated":truncated})
        }
        CommandOutput::TerminalList(items) => {
            let truncated = items.len() > LIST_LIMIT;
            json!({"terminals":items.into_iter().take(LIST_LIMIT).map(|t| json!({"id":t.id.0.to_string(),"project_id":t.project.0.to_string(),"tab_id":t.tab.0.to_string(),"pane_id":t.pane.0.to_string(),"cwd":t.cwd,"title":t.title.map(|title| title.chars().take(256).collect::<String>()),"exited":t.exited,"columns":t.columns,"lines":t.lines})).collect::<Vec<_>>(),"truncated":truncated})
        }
        CommandOutput::HistoryStatus(status) => {
            json!({"enabled":status.enabled,"key_available":status.key_available,"warning":status.warning,"archive_files":status.archive_files,"archive_bytes":status.archive_bytes,"paused_panes":status.paused_panes})
        }
        CommandOutput::JournalEntries(items) => {
            let truncated = items.len() > LIST_LIMIT;
            json!({"entries":items.into_iter().take(LIST_LIMIT).map(|e| json!({"pane_id":e.pane,"project_id":e.project,"tab_id":e.tab,"command":e.command.chars().take(4096).collect::<String>(), "shell_dialect":e.shell_dialect,"working_directory":e.working_directory,"started_unix_secs":e.started_unix_secs,"finished_unix_secs":e.finished_unix_secs,"exit_status":e.exit_status})).collect::<Vec<_>>(),"truncated":truncated})
        }
        CommandOutput::HistoryCleared { removed_files } => {
            json!({"removed_files":removed_files})
        }
        CommandOutput::ProjectRoot(info) => {
            json!({"root":info.root,"source":info.source.as_str()})
        }
        CommandOutput::FileList(list) => {
            let truncated = list.truncated || list.entries.len() > LIST_LIMIT;
            json!({"entries":list.entries.into_iter().take(LIST_LIMIT).map(|entry| json!({"path":entry.path,"kind":entry.kind.as_str()})).collect::<Vec<_>>(),"truncated":truncated})
        }
        CommandOutput::GitStatus(status) => {
            fn render(entries: Vec<omaterm_core::GitEntry>) -> Vec<Value> {
                entries
                    .into_iter()
                    .map(|entry| {
                        json!({"path":entry.path,"renamed_from":entry.renamed_from,"x":entry.x.to_string(),"y":entry.y.to_string()})
                    })
                    .collect()
            }
            // Bridge wire cap mirrors the list surfaces: groups fill in
            // staged → unstaged → untracked order with an accurate flag.
            let mut remaining = LIST_LIMIT;
            let mut truncated = status.truncated;
            let mut staged = render(status.staged);
            let mut unstaged = render(status.unstaged);
            let mut untracked = render(status.untracked);
            for group in [&mut staged, &mut unstaged, &mut untracked] {
                if group.len() > remaining {
                    group.truncate(remaining);
                    truncated = true;
                    remaining = 0;
                } else {
                    remaining -= group.len();
                }
            }
            json!({"branch":status.branch,"upstream":status.upstream,"ahead":status.ahead,"behind":status.behind,"staged":staged,"unstaged":unstaged,"untracked":untracked,"truncated":truncated})
        }
        CommandOutput::GitCommitted { oid } => {
            json!({"oid":oid})
        }
        CommandOutput::Diff(info) => {
            // Bridge wire cap mirrors the list surfaces: files fill in
            // walk order with an accurate flag; hunks and lines are
            // additionally windowed so one file cannot blow the 1 MiB
            // response frame (the frame encoder stays the backstop).
            const MAX_BRIDGE_FILES: usize = 128;
            const MAX_BRIDGE_HUNKS: usize = 64;
            const MAX_BRIDGE_LINES: usize = 200;
            const MAX_BRIDGE_LINE_CHARS: usize = 2000;
            let mut truncated = info.truncated || info.files.len() > MAX_BRIDGE_FILES;
            let files = info
                .files
                .into_iter()
                .take(MAX_BRIDGE_FILES)
                .map(|file| {
                    truncated = truncated
                        || file.truncated
                        || file.hunks.len() > MAX_BRIDGE_HUNKS;
                    let hunks = file
                        .hunks
                        .into_iter()
                        .take(MAX_BRIDGE_HUNKS)
                        .map(|hunk| {
                            let mut hunk_truncated =
                                hunk.truncated || hunk.lines.len() > MAX_BRIDGE_LINES;
                            truncated = truncated || hunk_truncated;
                            let lines = hunk
                                .lines
                                .into_iter()
                                .take(MAX_BRIDGE_LINES)
                                .map(|line| {
                                    let line_truncated =
                                        line.text.chars().count() > MAX_BRIDGE_LINE_CHARS;
                                    if line_truncated {
                                        truncated = true;
                                        hunk_truncated = true;
                                    }
                                    json!({"kind":line.kind.as_str(),"text":line.text.chars().take(MAX_BRIDGE_LINE_CHARS).collect::<String>(),"no_newline_at_end":line.no_newline_at_end})
                                })
                                .collect::<Vec<_>>();
                            json!({"id":hunk.id,"old_start":hunk.old_start,"old_lines":hunk.old_lines,"new_start":hunk.new_start,"new_lines":hunk.new_lines,"lines":lines,"truncated":hunk_truncated})
                        })
                        .collect::<Vec<_>>();
                    json!({"path":file.path,"old_path":file.old_path,"status":file.status.as_str(),"binary":file.binary,"hunks":hunks,"hunk_count":file.hunk_count,"truncated":file.truncated})
                })
                .collect::<Vec<_>>();
            json!({"files":files,"truncated":truncated,"staged":info.staged})
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use omaterm_terminal::WorkspaceCoordinator;

    fn router() -> CommandRouter {
        CommandRouter::new(WorkspaceCoordinator::new(std::env::temp_dir()))
    }
    fn request(method: &str, params: Value) -> IpcRequest {
        IpcRequest {
            version: 1,
            request_id: "wire-test".into(),
            method: method.into(),
            params,
            token: None,
        }
    }

    #[test]
    fn all_methods_map_to_the_domain_or_reject_a_missing_selector() {
        let router = router();
        let id = PaneId::new().0.to_string();
        let cases = [
            ("project.list", json!({}), true),
            ("project.create", json!({"directory":"/tmp"}), true),
            ("project.select", json!({"project_id":id}), true),
            (
                "project.set-directory",
                json!({"project_id":id,"directory":"/tmp"}),
                true,
            ),
            ("project.root", json!({}), false),
            ("tab.list", json!({}), false),
            ("tab.create", json!({}), false),
            ("tab.close", json!({"tab_id":id}), true),
            ("pane.list", json!({}), false),
            ("pane.split", json!({"direction":"right"}), false),
            ("pane.close", json!({"pane_id":id}), true),
            ("pane.focus", json!({"pane_id":id}), true),
            ("pane.resize", json!({"split_id":id,"fraction":0.5}), true),
            ("pane.equalize", json!({}), false),
            ("terminal.list", json!({}), true),
            ("terminal.create", json!({}), false),
            ("terminal.send", json!({"pane_id":id,"data":"AQ=="}), false),
            ("terminal.run", json!({"pane_id":id,"argv":["true"]}), false),
            ("terminal.read", json!({"pane_id":id,"lines":5}), false),
            ("history.enable", json!({}), true),
            ("history.disable", json!({}), true),
            ("history.status", json!({}), true),
            ("history.list", json!({"pane_id":id,"limit":20}), true),
            ("history.list", json!({"pane_id":id,"limit":0}), false),
            ("history.pause", json!({"pane_id":id}), true),
            ("history.resume", json!({"pane_id":id}), true),
            ("history.clear-pane", json!({"pane_id":id}), true),
            ("history.clear-project", json!({"project_id":id}), true),
            ("history.clear-all", json!({}), true),
            ("file.list", json!({}), false),
            (
                "file.list",
                json!({"project_id": id, "dir": "src", "limit": 20}),
                true,
            ),
            ("file.list", json!({"limit": 0}), false),
            ("file.search", json!({"query": "main"}), false),
            (
                "file.search",
                json!({"project_id": id, "query": "main", "limit": 20}),
                true,
            ),
            ("file.search", json!({"query": "x", "limit": 0}), false),
            ("file.open", json!({"path": "src/main.rs"}), false),
            (
                "file.open",
                json!({"project_id": id, "path": "src/main.rs"}),
                true,
            ),
            ("git.status", json!({}), false),
            ("git.status", json!({"project_id": id}), true),
            ("git.stage", json!({"paths": ["a.txt"]}), false),
            ("git.stage", json!({"paths": []}), false),
            (
                "git.unstage",
                json!({"project_id": id, "paths": ["a.txt"]}),
                true,
            ),
            (
                "git.discard",
                json!({"project_id": id, "paths": ["a.txt"]}),
                true,
            ),
            ("git.commit", json!({"message": "hello"}), false),
            (
                "git.commit",
                json!({"project_id": id, "message": "hello"}),
                true,
            ),
            ("git.commit", json!({"message": ""}), false),
            ("diff.show", json!({}), false),
            (
                "diff.show",
                json!({"project_id": id, "path": "src/main.rs", "staged": true, "context_lines": 5}),
                true,
            ),
            ("diff.show", json!({"context_lines": 11}), false),
            ("diff.show", json!({"path": "bad\npath"}), false),
            ("diff.list-files", json!({}), false),
            (
                "diff.list-files",
                json!({"project_id": id, "staged": true}),
                true,
            ),
        ];
        assert_eq!(cases.len(), 52);
        for (method, params, valid) in cases {
            let result = map_request(&request(method, params), CommandContext::LocalUser, &router);
            assert_eq!(result.is_ok(), valid, "{method}");
        }
        let bad = map_request(
            &request("terminal.send", json!({"pane_id":"bad","data":"AQ=="})),
            CommandContext::LocalUser,
            &router,
        );
        assert_eq!(bad.unwrap_err().error.unwrap().code, "invalid_request");
    }

    #[test]
    fn wire_bounds_reject_oversize_and_control_characters() {
        let router = router();
        let pane = PaneId::new().0.to_string();
        // Oversize decoded send bytes.
        let big = base64::engine::general_purpose::STANDARD.encode(vec![b'x'; 9000]);
        let err = map_request(
            &request("terminal.send", json!({"pane_id": pane, "data": big})),
            CommandContext::LocalUser,
            &router,
        )
        .unwrap_err();
        assert_eq!(err.error.unwrap().code, "invalid_request");
        // Invalid base64.
        let err = map_request(
            &request("terminal.send", json!({"pane_id": pane, "data": "!!!"})),
            CommandContext::LocalUser,
            &router,
        )
        .unwrap_err();
        assert_eq!(err.error.unwrap().code, "invalid_request");
        // Empty and oversize argv.
        for params in [
            json!({"pane_id": pane, "argv": []}),
            json!({"pane_id": pane, "argv": vec!["x"; 300]}),
        ] {
            let err = map_request(
                &request("terminal.run", params),
                CommandContext::LocalUser,
                &router,
            )
            .unwrap_err();
            assert_eq!(err.error.unwrap().code, "invalid_request");
        }
        // Over-limit read dimensions.
        for params in [
            json!({"pane_id": pane, "lines": 0}),
            json!({"pane_id": pane, "lines": 5000}),
            json!({"pane_id": pane, "columns": 5000}),
        ] {
            let err = map_request(
                &request("terminal.read", params),
                CommandContext::LocalUser,
                &router,
            )
            .unwrap_err();
            assert_eq!(err.error.unwrap().code, "invalid_request");
        }
        // Control characters in structured names.
        let err = map_request(
            &request("project.create", json!({"name": "bad\nname"})),
            CommandContext::LocalUser,
            &router,
        )
        .unwrap_err();
        assert_eq!(err.error.unwrap().code, "invalid_request");
        // Invalid UUID selector.
        let err = map_request(
            &request("pane.close", json!({"pane_id": "not-a-uuid"})),
            CommandContext::LocalUser,
            &router,
        )
        .unwrap_err();
        assert_eq!(err.error.unwrap().code, "invalid_request");
    }

    #[test]
    fn response_preserves_stable_error_and_bounded_list_indicator() {
        let result = response(
            "id".into(),
            CommandResult::Err(omaterm_core::CommandError::new(
                omaterm_core::ErrorCode::CrossProjectDenied,
                "foreign",
            )),
        );
        assert_eq!(result.error.unwrap().code, "cross_project_denied");
        let projects = (0..129)
            .map(|_| omaterm_core::ProjectInfo {
                id: ProjectId::new(),
                name: "p".into(),
                directory: None,
                selected: false,
                tab_count: 0,
            })
            .collect();
        let result = response(
            "id".into(),
            CommandResult::Ok(CommandOutput::ProjectList(projects)),
        );
        assert_eq!(result.result.as_ref().unwrap()["truncated"], true);
        assert_eq!(
            result.result.unwrap()["projects"].as_array().unwrap().len(),
            128
        );
    }

    #[test]
    fn git_status_response_groups_entries_with_an_accurate_flag() {
        let entry = |path: &str| omaterm_core::GitEntry {
            path: std::path::PathBuf::from(path),
            renamed_from: None,
            x: 'M',
            y: '.',
        };
        let status = omaterm_core::GitStatusInfo {
            branch: Some("main".into()),
            upstream: None,
            ahead: 1,
            behind: 0,
            staged: vec![entry("a.txt")],
            unstaged: vec![entry("b.txt")],
            untracked: vec![entry("c.txt")],
            truncated: false,
        };
        let result = response(
            "id".into(),
            CommandResult::Ok(CommandOutput::GitStatus(status)),
        );
        let body = result.result.unwrap();
        assert_eq!(body["branch"], "main");
        assert_eq!(body["ahead"], 1);
        assert_eq!(body["staged"][0]["path"], "a.txt");
        assert_eq!(body["staged"][0]["x"], "M");
        assert_eq!(body["truncated"], false);

        // Bridge wire cap: 130 staged entries truncate to 128.
        let big = omaterm_core::GitStatusInfo {
            branch: None,
            upstream: None,
            ahead: 0,
            behind: 0,
            staged: (0..130).map(|i| entry(&format!("f{i}.txt"))).collect(),
            unstaged: vec![],
            untracked: vec![],
            truncated: false,
        };
        let result = response(
            "id".into(),
            CommandResult::Ok(CommandOutput::GitStatus(big)),
        );
        let body = result.result.unwrap();
        assert_eq!(body["staged"].as_array().unwrap().len(), 128);
        assert_eq!(body["truncated"], true);
    }

    #[test]
    fn diff_response_shapes_files_hunks_and_bridge_caps() {
        let line = |kind, text: &str| omaterm_core::DiffLineInfo {
            kind,
            text: text.into(),
            no_newline_at_end: false,
        };
        let info = omaterm_core::DiffInfo {
            files: vec![omaterm_core::DiffFileInfo {
                path: std::path::PathBuf::from("src/main.rs"),
                old_path: None,
                status: omaterm_core::DiffFileStatus::Modified,
                binary: false,
                hunks: vec![omaterm_core::DiffHunkInfo {
                    id: 1,
                    old_start: 1,
                    old_lines: 2,
                    new_start: 1,
                    new_lines: 2,
                    lines: vec![
                        line(omaterm_core::DiffLineKind::Context, "ctx"),
                        line(omaterm_core::DiffLineKind::Deletion, "old"),
                        line(omaterm_core::DiffLineKind::Addition, "new"),
                    ],
                    truncated: false,
                }],
                hunk_count: 1,
                truncated: false,
            }],
            truncated: false,
            staged: false,
        };
        let result = response("id".into(), CommandResult::Ok(CommandOutput::Diff(info)));
        let body = result.result.unwrap();
        assert_eq!(body["files"][0]["path"], "src/main.rs");
        assert_eq!(body["files"][0]["status"], "modified");
        assert_eq!(body["files"][0]["hunks"][0]["old_start"], 1);
        assert_eq!(body["files"][0]["hunks"][0]["lines"][1]["kind"], "deletion");
        assert_eq!(body["files"][0]["hunks"][0]["lines"][1]["text"], "old");
        assert_eq!(body["truncated"], false);

        // Bridge byte/line cap marks both the envelope and the specific
        // hunk whose body was shortened.
        let long = omaterm_core::DiffInfo {
            files: vec![omaterm_core::DiffFileInfo {
                path: std::path::PathBuf::from("long.txt"),
                old_path: None,
                status: omaterm_core::DiffFileStatus::Modified,
                binary: false,
                hunks: vec![omaterm_core::DiffHunkInfo {
                    id: 2,
                    old_start: 1,
                    old_lines: 1,
                    new_start: 1,
                    new_lines: 1,
                    lines: vec![line(
                        omaterm_core::DiffLineKind::Addition,
                        &"x".repeat(2500),
                    )],
                    truncated: false,
                }],
                hunk_count: 1,
                truncated: false,
            }],
            truncated: false,
            staged: false,
        };
        let long_response = response("id".into(), CommandResult::Ok(CommandOutput::Diff(long)));
        let body = long_response.result.unwrap();
        assert_eq!(body["truncated"], true);
        assert_eq!(body["files"][0]["hunks"][0]["truncated"], true);
        assert_eq!(
            body["files"][0]["hunks"][0]["lines"][0]["text"]
                .as_str()
                .unwrap()
                .len(),
            2000
        );

        // Bridge wire cap: 130 files truncate to 128 with the flag set.
        let many = |count: usize| omaterm_core::DiffInfo {
            files: (0..count)
                .map(|i| omaterm_core::DiffFileInfo {
                    path: std::path::PathBuf::from(format!("f{i}.txt")),
                    old_path: None,
                    status: omaterm_core::DiffFileStatus::Modified,
                    binary: false,
                    hunks: vec![],
                    hunk_count: 1,
                    truncated: false,
                })
                .collect(),
            truncated: false,
            staged: true,
        };
        let result = response(
            "id".into(),
            CommandResult::Ok(CommandOutput::Diff(many(130))),
        );
        let body = result.result.unwrap();
        assert_eq!(body["files"].as_array().unwrap().len(), 128);
        assert_eq!(body["truncated"], true);
        assert_eq!(body["staged"], true);
    }

    #[test]
    fn pane_list_response_carries_split_discoverability() {
        let pane = omaterm_core::PaneInfo {
            id: PaneId::new(),
            project: ProjectId::new(),
            tab: omaterm_core::TabId::new(),
            session: None,
            focused: true,
            x: 0.0,
            y: 0.0,
            width: 0.5,
            height: 1.0,
            splits: vec![omaterm_core::SplitSummary {
                id: SplitId::new(),
                axis: omaterm_core::SplitAxis::Horizontal,
                fraction: 0.5,
            }],
        };
        let result = response(
            "id".into(),
            CommandResult::Ok(CommandOutput::PaneList(vec![pane])),
        );
        let panes = result.result.unwrap()["panes"].clone();
        let splits = panes[0]["splits"].as_array().unwrap().clone();
        assert_eq!(splits.len(), 1);
        assert_eq!(splits[0]["axis"], "horizontal");
        assert_eq!(splits[0]["fraction"], 0.5);
        assert!(!panes[0]["id"].as_str().unwrap().is_empty());
        assert!(!splits[0]["id"].as_str().unwrap().is_empty());
    }
}
