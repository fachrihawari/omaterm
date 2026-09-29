//! Transport-to-domain conversion on the desktop owner. Socket threads never
//! resolve selectors or access workspace state.
use base64::Engine;
use omaterm_core::{
    CommandContext, CommandOutput, CommandResult, HistoryCommand, OmaCommand, PaneCommand,
    PaneContent, PaneId, ProjectCommand, ProjectId, SessionId, SplitDirection, SplitId, TabCommand,
    TabId, TerminalCommand,
};
use omaterm_protocol::{
    IpcRequest, IpcResponse, MAX_ARG_COUNT, MAX_ARGUMENT_BYTES, MAX_JOURNAL_ENTRIES,
    MAX_READ_COLUMNS, MAX_READ_LINES, MAX_SEND_BYTES, method::Method,
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
        ];
        assert_eq!(cases.len(), 29);
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
