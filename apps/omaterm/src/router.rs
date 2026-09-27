use omaterm_core::{
    CommandContext, CommandError, CommandOutput, CommandResult, ErrorCode, OmaCommand, PaneCommand,
    PaneInfo, ProjectCommand, ProjectInfo, TabCommand, TabInfo, TerminalCommand, TerminalInfo,
};
use omaterm_terminal::workspace::ClosedSessions;
use omaterm_terminal::{ClosedPane, CoordinatorError, WorkspaceCoordinator};

pub enum CommandEffect {
    SessionStarted(omaterm_core::SessionId),
    SessionClosed(ClosedPane),
    PersistenceDirty,
    WorkspaceChanged,
}

pub struct DispatchOutcome {
    pub result: CommandResult,
    pub effects: Vec<CommandEffect>,
}

/// In-process semantic dispatcher. It deliberately contains no GPUI types;
/// the desktop owner applies returned effects and owns all UI state changes.
pub struct CommandRouter {
    coordinator: WorkspaceCoordinator,
}

impl std::ops::Deref for CommandRouter {
    type Target = WorkspaceCoordinator;
    fn deref(&self) -> &Self::Target {
        &self.coordinator
    }
}
impl std::ops::DerefMut for CommandRouter {
    fn deref_mut(&mut self) -> &mut Self::Target {
        &mut self.coordinator
    }
}

impl CommandRouter {
    pub fn new(coordinator: WorkspaceCoordinator) -> Self {
        Self { coordinator }
    }

    pub fn dispatch(&mut self, context: CommandContext, command: OmaCommand) -> DispatchOutcome {
        let _context = context;
        if let Err(error) = omaterm_core::validation::validate(&command) {
            return DispatchOutcome {
                result: CommandResult::Err(error),
                effects: vec![],
            };
        }
        let mut effects = Vec::new();
        let result = self.dispatch_valid(command, &mut effects);
        if matches!(result, CommandResult::Ok(_))
            && effects
                .iter()
                .any(|effect| matches!(effect, CommandEffect::WorkspaceChanged))
        {
            effects.push(CommandEffect::PersistenceDirty);
        }
        DispatchOutcome { result, effects }
    }

    fn dispatch_valid(
        &mut self,
        command: OmaCommand,
        effects: &mut Vec<CommandEffect>,
    ) -> CommandResult {
        use CommandOutput as Out;
        match command {
            OmaCommand::Project(ProjectCommand::List) => {
                let window = self.coordinator.window();
                let projects = window
                    .projects
                    .iter()
                    .enumerate()
                    .map(|(i, p)| ProjectInfo {
                        id: p.id,
                        name: p.display_name(i + 1),
                        directory: p.pinned_directory.clone(),
                        selected: window.selected_project == Some(p.id),
                        tab_count: p.tabs.len(),
                    })
                    .collect();
                ok(Out::ProjectList(projects))
            }
            OmaCommand::Project(ProjectCommand::Create { name, directory }) => {
                match self.coordinator.create_project(directory, 80, 24) {
                    Ok((project, tab, session)) => {
                        if let Some(name) = name {
                            let _ = self.coordinator.rename_project(project, name);
                        }
                        let pane = self
                            .coordinator
                            .window()
                            .project(project)
                            .and_then(|p| p.tab(tab))
                            .and_then(|t| t.tree.panes().first().map(|p| p.id));
                        if let Some(pane) = pane {
                            effects.extend([
                                CommandEffect::SessionStarted(session),
                                CommandEffect::WorkspaceChanged,
                            ]);
                            ok(Out::ProjectCreated {
                                project,
                                tab,
                                pane,
                                session,
                            })
                        } else {
                            err(
                                ErrorCode::RuntimeFailure,
                                "created project has no initial pane",
                            )
                        }
                    }
                    Err(error) => coordinator_error(error),
                }
            }
            OmaCommand::Project(ProjectCommand::Delete { project }) => {
                match self.coordinator.close_project(project) {
                    Ok(closed) => {
                        close_effects(effects, closed);
                        effects.push(CommandEffect::WorkspaceChanged);
                        ok(Out::Unit)
                    }
                    Err(error) => coordinator_error(error),
                }
            }
            OmaCommand::Project(ProjectCommand::Select { project }) => {
                match self.coordinator.select_project(project) {
                    Ok(()) => changed(effects, Out::Unit),
                    Err(error) => coordinator_error(error),
                }
            }
            OmaCommand::Project(ProjectCommand::Rename { project, name }) => {
                match self.coordinator.rename_project(project, name) {
                    Ok(()) => changed(effects, Out::Unit),
                    Err(e) => coordinator_error(e),
                }
            }
            OmaCommand::Tab(TabCommand::List { project }) => {
                let Some(p) = self.coordinator.window().project(project) else {
                    return err(ErrorCode::ProjectNotFound, "project does not exist");
                };
                ok(Out::TabList(
                    p.tabs
                        .iter()
                        .enumerate()
                        .map(|(i, t)| TabInfo {
                            id: t.id,
                            project,
                            name: t.display_name(i + 1),
                            selected: p.selected_tab == Some(t.id),
                            focused_pane: Some(t.focused_pane),
                            pane_count: t.tree.panes().len(),
                        })
                        .collect(),
                ))
            }
            OmaCommand::Tab(TabCommand::Create { project, name }) => {
                match self.coordinator.create_tab(project, 80, 24) {
                    Ok((tab, session)) => {
                        if let Some(name) = name {
                            let _ = self.coordinator.rename_tab(tab, name);
                        }
                        let pane = self
                            .coordinator
                            .window()
                            .project(project)
                            .and_then(|p| p.tab(tab))
                            .map(|t| t.focused_pane);
                        if let Some(pane) = pane {
                            effects.extend([
                                CommandEffect::SessionStarted(session),
                                CommandEffect::WorkspaceChanged,
                            ]);
                            ok(Out::TabCreated { tab, pane, session })
                        } else {
                            err(ErrorCode::RuntimeFailure, "created tab has no pane")
                        }
                    }
                    Err(error) => coordinator_error(error),
                }
            }
            OmaCommand::Tab(TabCommand::Close { tab }) => {
                let Some(project) = self
                    .coordinator
                    .window()
                    .projects
                    .iter()
                    .find(|p| p.tab(tab).is_some())
                    .map(|p| p.id)
                else {
                    return err(ErrorCode::TabNotFound, "tab does not exist");
                };
                match self.coordinator.close_tab(project, tab) {
                    Ok(closed) => {
                        close_effects(effects, closed);
                        effects.push(CommandEffect::WorkspaceChanged);
                        ok(Out::Unit)
                    }
                    Err(e) => coordinator_error(e),
                }
            }
            OmaCommand::Tab(TabCommand::Select { tab }) => {
                let Some(project) = self
                    .coordinator
                    .window()
                    .projects
                    .iter()
                    .find(|p| p.tab(tab).is_some())
                    .map(|p| p.id)
                else {
                    return err(ErrorCode::TabNotFound, "tab does not exist");
                };
                match self.coordinator.select_tab(project, tab) {
                    Ok(()) => changed(effects, Out::Unit),
                    Err(e) => coordinator_error(e),
                }
            }
            OmaCommand::Tab(TabCommand::Rename { tab, name }) => {
                match self.coordinator.rename_tab(tab, name) {
                    Ok(()) => changed(effects, Out::Unit),
                    Err(e) => coordinator_error(e),
                }
            }
            OmaCommand::Pane(PaneCommand::List { tab }) => {
                let mut panes = Vec::new();
                for project in &self.coordinator.window().projects {
                    for t in &project.tabs {
                        if tab.is_some_and(|id| id != t.id) {
                            continue;
                        }
                        let active = self.coordinator.selected_project_id() == Some(project.id)
                            && self.coordinator.selected_tab_id() == Some(t.id);
                        for rect in t.tree.pane_rects() {
                            let session = t.tree.find(rect.pane).and_then(|p| match p.content {
                                omaterm_core::PaneContent::Terminal(id) => Some(id),
                                _ => None,
                            });
                            panes.push(PaneInfo {
                                id: rect.pane,
                                project: project.id,
                                tab: t.id,
                                session,
                                focused: active && t.focused_pane == rect.pane,
                                x: rect.rect.x,
                                y: rect.rect.y,
                                width: rect.rect.width,
                                height: rect.rect.height,
                            });
                        }
                    }
                }
                if let Some(id) = tab
                    && !self
                        .coordinator
                        .window()
                        .projects
                        .iter()
                        .any(|p| p.tab(id).is_some())
                {
                    return err(ErrorCode::TabNotFound, "tab does not exist");
                }
                ok(Out::PaneList(panes))
            }
            OmaCommand::Pane(PaneCommand::Split { target, direction }) => {
                match self.coordinator.split_pane(target, direction, 80, 24) {
                    Ok((pane, session)) => {
                        effects.extend([
                            CommandEffect::SessionStarted(session),
                            CommandEffect::WorkspaceChanged,
                        ]);
                        ok(Out::PaneSplit { pane, session })
                    }
                    Err(e) => coordinator_error(e),
                }
            }
            OmaCommand::Pane(PaneCommand::Close { pane }) => {
                match self.coordinator.close_pane_by_id(pane) {
                    Ok(closed) => {
                        effects.push(CommandEffect::SessionClosed(closed));
                        effects.push(CommandEffect::WorkspaceChanged);
                        ok(Out::Unit)
                    }
                    Err(e) => coordinator_error(e),
                }
            }
            OmaCommand::Pane(PaneCommand::Focus { pane }) => {
                match self.coordinator.focus_pane_anywhere(pane) {
                    Ok(()) => changed(effects, Out::Unit),
                    Err(e) => coordinator_error(e),
                }
            }
            OmaCommand::Pane(PaneCommand::FocusDirection { direction }) => {
                match self.coordinator.focus_neighbor(direction) {
                    Some(_) => changed(effects, Out::Unit),
                    None => ok(Out::Unit),
                }
            }
            OmaCommand::Pane(PaneCommand::Resize { split, fraction }) => {
                match self.coordinator.resize_split(split, fraction) {
                    Ok(()) => changed(effects, Out::Unit),
                    Err(e) => coordinator_error(e),
                }
            }
            OmaCommand::Pane(PaneCommand::ResizeFocused { amount }) => {
                match self.coordinator.resize_focused(amount) {
                    Ok(_) => changed(effects, Out::Unit),
                    Err(e) => coordinator_error(e),
                }
            }
            OmaCommand::Pane(PaneCommand::Equalize { tab }) => {
                match self.coordinator.equalize_tab(tab) {
                    Ok(()) => changed(effects, Out::Unit),
                    Err(e) => coordinator_error(e),
                }
            }
            OmaCommand::Pane(PaneCommand::EqualizeSelected) => {
                self.coordinator.equalize();
                changed(effects, Out::Unit)
            }
            OmaCommand::Terminal(TerminalCommand::Create { project, directory }) => {
                match if let Some(directory) = directory {
                    self.coordinator
                        .create_tab_in_directory(project, directory, 80, 24)
                } else {
                    self.coordinator.create_tab(project, 80, 24)
                } {
                    Ok((tab, session)) => {
                        let pane = self
                            .coordinator
                            .window()
                            .project(project)
                            .and_then(|p| p.tab(tab))
                            .map(|t| t.focused_pane);
                        if let Some(pane) = pane {
                            effects.extend([
                                CommandEffect::SessionStarted(session),
                                CommandEffect::WorkspaceChanged,
                            ]);
                            ok(Out::TerminalCreated { tab, pane, session })
                        } else {
                            err(ErrorCode::RuntimeFailure, "created terminal has no pane")
                        }
                    }
                    Err(e) => coordinator_error(e),
                }
            }
            OmaCommand::Terminal(TerminalCommand::RestorePane {
                project,
                tab,
                pane,
                directory,
            }) => match self
                .coordinator
                .launch_restored_pane(project, tab, pane, directory, 80, 24)
            {
                Ok(session) => {
                    effects.extend([
                        CommandEffect::SessionStarted(session),
                        CommandEffect::WorkspaceChanged,
                    ]);
                    ok(Out::Unit)
                }
                Err(e) => coordinator_error(e),
            },
            OmaCommand::Terminal(TerminalCommand::SendBytes { session, data }) => {
                let Some(handle) = self.coordinator.registry().get(session) else {
                    return err(ErrorCode::SessionExited, "terminal session is unavailable");
                };
                match handle.lock() {
                    Ok(mut s) if s.exited().is_none() => match s.write_input(&data) {
                        Ok(()) => ok(Out::Unit),
                        Err(e) => err(ErrorCode::RuntimeFailure, e.to_string()),
                    },
                    _ => err(ErrorCode::SessionExited, "terminal session has exited"),
                }
            }
            OmaCommand::Terminal(TerminalCommand::ReadVisible {
                session,
                max_lines,
                max_columns,
            }) => {
                let Some(handle) = self.coordinator.registry().get(session) else {
                    return err(ErrorCode::SessionExited, "terminal session is unavailable");
                };
                match handle.lock() {
                    Ok(s) if s.exited().is_none() => {
                        let viewport = s.viewport();
                        let truncated = viewport.lines as usize > max_lines
                            || viewport.cols as usize > max_columns;
                        let text = s.read_visible_text(max_lines, max_columns);
                        ok(Out::TerminalOutput {
                            text,
                            truncated,
                            lines: (viewport.lines as usize).min(max_lines),
                            columns: (viewport.cols as usize).min(max_columns),
                        })
                    }
                    _ => err(ErrorCode::SessionExited, "terminal session has exited"),
                }
            }
            OmaCommand::Terminal(TerminalCommand::List) => {
                let mut items = Vec::new();
                for p in &self.coordinator.window().projects {
                    for t in &p.tabs {
                        for pane in t.tree.panes() {
                            if let omaterm_core::PaneContent::Terminal(id) = pane.content
                                && let Some(h) = self.coordinator.registry().get(id)
                                && let Ok(s) = h.lock()
                            {
                                let v = s.viewport();
                                items.push(TerminalInfo {
                                    id,
                                    project: p.id,
                                    tab: t.id,
                                    pane: pane.id,
                                    cwd: s.cwd().path.clone(),
                                    title: s.title().map(str::to_owned),
                                    exited: s.exited().is_some(),
                                    columns: v.cols as usize,
                                    lines: v.lines as usize,
                                });
                            }
                        }
                    }
                }
                ok(Out::TerminalList(items))
            }
            OmaCommand::Terminal(TerminalCommand::RunCommand { .. }) => err(
                ErrorCode::UnsupportedOperation,
                "terminal.run requires supported-shell lifecycle integration, which is not available yet",
            ),
            OmaCommand::Terminal(TerminalCommand::Clear { .. }) => err(
                ErrorCode::UnsupportedOperation,
                "terminal.clear is not implemented",
            ),
        }
    }
}

fn ok(output: CommandOutput) -> CommandResult {
    CommandResult::Ok(output)
}
fn err(code: ErrorCode, message: impl Into<String>) -> CommandResult {
    CommandResult::Err(CommandError::new(code, message))
}
fn changed(effects: &mut Vec<CommandEffect>, output: CommandOutput) -> CommandResult {
    effects.extend([CommandEffect::WorkspaceChanged]);
    ok(output)
}
fn close_effects(effects: &mut Vec<CommandEffect>, closed: ClosedSessions) {
    effects.extend(closed.0.into_iter().map(CommandEffect::SessionClosed));
}
fn coordinator_error(error: CoordinatorError) -> CommandResult {
    match &error {
        CoordinatorError::Core(omaterm_core::CoreError::ProjectNotFound(_)) => {
            err(ErrorCode::ProjectNotFound, error.to_string())
        }
        CoordinatorError::Core(omaterm_core::CoreError::TabNotFound(_)) => {
            err(ErrorCode::TabNotFound, error.to_string())
        }
        CoordinatorError::Core(omaterm_core::CoreError::PaneNotFound(_)) => {
            err(ErrorCode::PaneNotFound, error.to_string())
        }
        CoordinatorError::Core(omaterm_core::CoreError::SplitNotFound(_)) => {
            err(ErrorCode::SplitNotFound, error.to_string())
        }
        CoordinatorError::NoFocusedPane => err(ErrorCode::NoFocusedPane, error.to_string()),
        _ => err(ErrorCode::RuntimeFailure, error.to_string()),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use omaterm_core::{
        PaneCommand, ProjectCommand, SessionId, SplitDirection, SplitId, TerminalCommand,
    };

    fn router() -> CommandRouter {
        CommandRouter::new(WorkspaceCoordinator::new(std::env::temp_dir()))
    }

    #[test]
    fn queries_return_owned_empty_snapshot() {
        let mut router = router();
        let result = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::List),
        );
        assert_eq!(
            result.result,
            CommandResult::Ok(CommandOutput::ProjectList(vec![]))
        );
        assert!(result.effects.is_empty());
    }

    #[test]
    fn validation_failure_has_stable_code_and_no_effects() {
        let mut router = router();
        let result = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Pane(PaneCommand::Resize {
                split: SplitId::new(),
                fraction: f32::NAN,
            }),
        );
        let CommandResult::Err(error) = result.result else {
            panic!("expected invalid request");
        };
        assert_eq!(error.code, ErrorCode::InvalidRequest);
        assert!(result.effects.is_empty());
        assert_eq!(error.code.as_str(), "invalid_request");
    }

    #[test]
    fn command_targets_report_stale_ids_and_unsupported_run_explicitly() {
        let mut router = router();
        let stale = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Pane(PaneCommand::Split {
                target: omaterm_core::PaneId::new(),
                direction: SplitDirection::Right,
            }),
        );
        assert!(matches!(
            stale.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::PaneNotFound,
                ..
            })
        ));
        assert!(stale.effects.is_empty());
        let run = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Terminal(TerminalCommand::RunCommand {
                session: SessionId::new(),
                argv: vec!["true".into()],
            }),
        );
        assert!(matches!(
            run.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::UnsupportedOperation,
                ..
            })
        ));
    }

    #[test]
    fn project_creation_split_and_close_share_dispatch_effect_path() {
        let mut router = router();
        let created = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Create {
                name: Some("router-test".into()),
                directory: Some(std::env::temp_dir()),
            }),
        );
        let CommandResult::Ok(CommandOutput::ProjectCreated { project, pane, .. }) = created.result
        else {
            panic!("project command should create the initial terminal");
        };
        assert!(
            created
                .effects
                .iter()
                .any(|e| matches!(e, CommandEffect::SessionStarted(_)))
        );
        assert!(
            created
                .effects
                .iter()
                .any(|e| matches!(e, CommandEffect::PersistenceDirty))
        );

        let split = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Pane(PaneCommand::Split {
                target: pane,
                direction: SplitDirection::Right,
            }),
        );
        assert!(matches!(
            split.result,
            CommandResult::Ok(CommandOutput::PaneSplit { .. })
        ));

        let closed = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Delete { project }),
        );
        assert!(matches!(
            closed.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        for effect in closed.effects {
            if let CommandEffect::SessionClosed(closed) = effect
                && let Some(handle) = closed.handle
            {
                let _ = handle.lock().map(|mut session| session.shutdown());
            }
        }
    }
}
