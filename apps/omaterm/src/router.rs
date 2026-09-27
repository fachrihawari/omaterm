use std::collections::HashMap;
use std::path::Path;
use std::path::PathBuf;

use crate::credentials::Credentials;
use omaterm_core::{
    CommandContext, CommandError, CommandOutput, CommandResult, ErrorCode, OmaCommand, PaneCommand,
    PaneContent, PaneId, PaneInfo, ProjectCommand, ProjectId, ProjectInfo, SessionId,
    SplitDirection, TabCommand, TabId, TabInfo, TerminalCommand, TerminalInfo,
};
use omaterm_protocol::CapabilityToken;
use omaterm_terminal::workspace::ClosedSessions;
use omaterm_terminal::{
    ClosedPane, CoordinatorError, ProjectSessionCommit, SessionSpawnQueue, SpawnCompletion,
    SplitSessionCommit, TerminalConfig, TerminalSession, WorkspaceCoordinator,
};

const MAX_PENDING_LAUNCHES: usize = 8;

enum PendingTarget {
    Project {
        previous: Option<ProjectId>,
        project: ProjectId,
        tab: TabId,
        pane: PaneId,
        name: Option<String>,
        directory: PathBuf,
    },
    Tab {
        previous: Option<ProjectId>,
        project: ProjectId,
        tab: TabId,
        pane: PaneId,
        name: Option<String>,
        terminal_create: bool,
    },
    Split {
        previous: Option<ProjectId>,
        selected_tab: Option<TabId>,
        project: ProjectId,
        tab: TabId,
        focus: PaneId,
        source: SessionId,
        target: PaneId,
        direction: SplitDirection,
        pane: PaneId,
    },
    Restore {
        project: ProjectId,
        tab: TabId,
        pane: PaneId,
    },
}

struct PendingLaunch {
    session: SessionId,
    target: PendingTarget,
    context: CommandContext,
    credential: Option<(String, ProjectId)>,
}

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
    spawns: Option<SessionSpawnQueue>,
    pending: HashMap<u64, PendingLaunch>,
    next_operation: u64,
    credentials: Option<Credentials>,
    socket_path: Option<PathBuf>,
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
        Self {
            coordinator,
            spawns: Some(SessionSpawnQueue::new(MAX_PENDING_LAUNCHES)),
            pending: HashMap::new(),
            next_operation: 0,
            credentials: None,
            socket_path: None,
        }
    }

    pub fn enable_credentials(&mut self, socket: &Path) -> std::io::Result<()> {
        self.credentials = Some(Credentials::create(socket)?);
        self.socket_path = Some(socket.to_path_buf());
        Ok(())
    }

    pub fn authenticate(&self, token: Option<&CapabilityToken>) -> Option<CommandContext> {
        self.credentials.as_ref()?.authenticate(token)
    }

    pub fn revoke_session(&mut self, session: SessionId) {
        if let Some(credentials) = &mut self.credentials {
            credentials.revoke(session);
        }
    }

    fn authorize(&self, context: CommandContext, command: &OmaCommand) -> Result<(), CommandError> {
        let CommandContext::Project(scope) = context else {
            return Ok(());
        };
        if self.coordinator.window().project(scope).is_none() {
            return Err(CommandError::new(
                ErrorCode::PermissionDenied,
                "project credential is no longer valid",
            ));
        }
        let owner_tab = |tab| {
            self.coordinator
                .window()
                .projects
                .iter()
                .find(|p| p.tab(tab).is_some())
                .map(|p| p.id)
        };
        let owner_pane = |pane| {
            self.coordinator
                .window()
                .projects
                .iter()
                .find(|p| p.tabs.iter().any(|t| t.tree.find(pane).is_some()))
                .map(|p| p.id)
        };
        let owner_split = |split| {
            self.coordinator
                .window()
                .projects
                .iter()
                .find(|p| {
                    p.tabs
                        .iter()
                        .any(|t| t.tree.split_fraction(split).is_some())
                })
                .map(|p| p.id)
        };
        let owner_session = |session| {
            self.coordinator
                .window()
                .projects
                .iter()
                .find(|p| {
                    p.tabs.iter().any(|t| {
                        t.tree
                            .panes()
                            .iter()
                            .any(|pane| pane.content == PaneContent::Terminal(session))
                    })
                })
                .map(|p| p.id)
        };
        let owner = match command {
            OmaCommand::Project(ProjectCommand::Create { .. } | ProjectCommand::Select { .. }) => {
                return Err(CommandError::new(
                    ErrorCode::PermissionDenied,
                    "global project operation requires local authority",
                ));
            }
            OmaCommand::Project(
                ProjectCommand::Delete { project } | ProjectCommand::Rename { project, .. },
            )
            | OmaCommand::Tab(TabCommand::Create { project, .. } | TabCommand::List { project })
            | OmaCommand::Terminal(TerminalCommand::Create { project, .. }) => Some(*project),
            OmaCommand::Tab(
                TabCommand::Close { tab }
                | TabCommand::Select { tab }
                | TabCommand::Rename { tab, .. },
            )
            | OmaCommand::Pane(PaneCommand::Equalize { tab }) => owner_tab(*tab),
            OmaCommand::Pane(PaneCommand::Close { pane } | PaneCommand::Focus { pane }) => {
                owner_pane(*pane)
            }
            OmaCommand::Pane(PaneCommand::Split { target: pane, .. }) => owner_pane(*pane),
            OmaCommand::Pane(PaneCommand::Resize { split, .. }) => owner_split(*split),
            OmaCommand::Pane(PaneCommand::List { tab: Some(tab) }) => owner_tab(*tab),
            OmaCommand::Terminal(
                TerminalCommand::SendBytes { session, .. }
                | TerminalCommand::RunCommand { session, .. }
                | TerminalCommand::ReadVisible { session, .. }
                | TerminalCommand::Clear { session },
            ) => owner_session(*session),
            OmaCommand::Terminal(TerminalCommand::RestorePane { project, .. }) => Some(*project),
            OmaCommand::Pane(
                PaneCommand::FocusDirection { .. }
                | PaneCommand::ResizeFocused { .. }
                | PaneCommand::EqualizeSelected,
            ) => self.coordinator.selected_project_id(),
            _ => None,
        };
        if owner.is_some_and(|project| project != scope) {
            Err(CommandError::new(
                ErrorCode::CrossProjectDenied,
                "target belongs to another project",
            ))
        } else {
            Ok(())
        }
    }

    pub fn dispatch_async(
        &mut self,
        context: CommandContext,
        command: OmaCommand,
    ) -> DispatchOutcome {
        if let Err(error) = omaterm_core::validation::validate(&command) {
            return DispatchOutcome {
                result: CommandResult::Err(error),
                effects: vec![],
            };
        }
        if let Err(error) = self.authorize(context, &command) {
            return DispatchOutcome {
                result: CommandResult::Err(error),
                effects: vec![],
            };
        }
        if matches!(
            command,
            OmaCommand::Project(ProjectCommand::Create { .. })
                | OmaCommand::Tab(TabCommand::Create { .. })
                | OmaCommand::Pane(PaneCommand::Split { .. })
                | OmaCommand::Terminal(
                    TerminalCommand::Create { .. } | TerminalCommand::RestorePane { .. }
                )
        ) {
            return self.prepare_launch(context, command);
        }
        let mut effects = Vec::new();
        let result = self.dispatch_valid(context, command, &mut effects);
        if matches!(result, CommandResult::Ok(_))
            && effects
                .iter()
                .any(|effect| matches!(effect, CommandEffect::WorkspaceChanged))
        {
            effects.push(CommandEffect::PersistenceDirty);
        }
        DispatchOutcome { result, effects }
    }

    #[cfg(test)]
    fn dispatch(&mut self, context: CommandContext, command: OmaCommand) -> DispatchOutcome {
        let outcome = self.dispatch_async(context, command);
        let CommandResult::Ok(CommandOutput::Pending { operation_id }) = outcome.result else {
            return outcome;
        };
        let start = std::time::Instant::now();
        loop {
            for (id, finished) in self.poll_launches() {
                if id == operation_id {
                    return finished;
                }
            }
            assert!(
                start.elapsed() < std::time::Duration::from_secs(5),
                "launch timed out"
            );
            std::thread::sleep(std::time::Duration::from_millis(2));
        }
    }

    pub fn has_pending_launches(&self) -> bool {
        !self.pending.is_empty()
    }

    /// Called only by the application owner. No worker may mutate the tree.
    pub fn poll_launches(&mut self) -> Vec<(u64, DispatchOutcome)> {
        let mut outcomes = Vec::new();
        while let Some(completion) = self.spawns.as_ref().and_then(|queue| queue.try_recv().ok()) {
            let operation = completion.operation_id;
            outcomes.push((operation, self.finish_launch(completion)));
        }
        outcomes
    }

    pub fn cancel_launches(&mut self) {
        self.pending.clear();
        if let Some(queue) = self.spawns.take() {
            std::thread::spawn(move || queue.shutdown());
        }
    }

    pub fn cancel_launch(&mut self, operation_id: u64) {
        self.pending.remove(&operation_id);
    }

    fn prepare_launch(&mut self, context: CommandContext, command: OmaCommand) -> DispatchOutcome {
        let error = |code, message| DispatchOutcome {
            result: err(code, message),
            effects: vec![],
        };
        if self.pending.len() >= MAX_PENDING_LAUNCHES {
            return error(ErrorCode::RuntimeFailure, "terminal launch queue is full");
        }
        let mut config = TerminalConfig::new(self.coordinator.working_directory().clone());
        let target = match command {
            OmaCommand::Project(ProjectCommand::Create { name, directory }) => {
                let directory = directory
                    .filter(|path| path.is_dir())
                    .unwrap_or_else(|| self.coordinator.home_directory().clone());
                config.working_directory = directory.clone();
                PendingTarget::Project {
                    previous: self.coordinator.selected_project_id(),
                    project: ProjectId::new(),
                    tab: TabId::new(),
                    pane: PaneId::new(),
                    name,
                    directory,
                }
            }
            OmaCommand::Tab(TabCommand::Create { project, name }) => {
                let Some(owner) = self.coordinator.window().project(project) else {
                    return error(ErrorCode::ProjectNotFound, "project does not exist");
                };
                config.working_directory = owner
                    .pinned_directory
                    .clone()
                    .filter(|path| path.is_dir())
                    .unwrap_or_else(|| self.coordinator.home_directory().clone());
                PendingTarget::Tab {
                    previous: self.coordinator.selected_project_id(),
                    project,
                    tab: TabId::new(),
                    pane: PaneId::new(),
                    name,
                    terminal_create: false,
                }
            }
            OmaCommand::Terminal(TerminalCommand::Create { project, directory }) => {
                if self.coordinator.window().project(project).is_none() {
                    return error(ErrorCode::ProjectNotFound, "project does not exist");
                }
                config.working_directory = directory.unwrap_or_else(|| {
                    self.coordinator
                        .window()
                        .project(project)
                        .and_then(|owner| owner.pinned_directory.clone())
                        .filter(|path| path.is_dir())
                        .unwrap_or_else(|| self.coordinator.home_directory().clone())
                });
                PendingTarget::Tab {
                    previous: self.coordinator.selected_project_id(),
                    project,
                    tab: TabId::new(),
                    pane: PaneId::new(),
                    name: None,
                    terminal_create: true,
                }
            }
            OmaCommand::Pane(PaneCommand::Split { target, direction }) => {
                let Some((project, tab, focus, source)) = self
                    .coordinator
                    .window()
                    .projects
                    .iter()
                    .find_map(|project| {
                        project.tabs.iter().find_map(|tab| {
                            tab.tree.find(target).map(|pane| {
                                (project.id, tab.id, tab.focused_pane, pane.content.clone())
                            })
                        })
                    })
                else {
                    return error(ErrorCode::PaneNotFound, "pane does not exist");
                };
                let PaneContent::Terminal(source) = source else {
                    return error(ErrorCode::TerminalRequired, "pane has no terminal");
                };
                config.working_directory = self
                    .coordinator
                    .registry()
                    .get(source)
                    .and_then(|handle| handle.lock().ok().map(|session| session.cwd().path.clone()))
                    .filter(|path| path.is_dir())
                    .unwrap_or_else(|| self.coordinator.home_directory().clone());
                PendingTarget::Split {
                    previous: self.coordinator.selected_project_id(),
                    selected_tab: self
                        .coordinator
                        .window()
                        .project(project)
                        .and_then(|p| p.selected_tab),
                    project,
                    tab,
                    focus,
                    source,
                    target,
                    direction,
                    pane: PaneId::new(),
                }
            }
            OmaCommand::Terminal(TerminalCommand::RestorePane {
                project,
                tab,
                pane,
                directory,
            }) => {
                let Some(owner) = self.coordinator.window().project(project) else {
                    return error(ErrorCode::ProjectNotFound, "project does not exist");
                };
                let Some(tab_state) = owner.tab(tab) else {
                    return error(ErrorCode::TabNotFound, "tab does not exist");
                };
                match tab_state.tree.find(pane) {
                    Some(pane) if matches!(pane.content, PaneContent::Empty) => {}
                    Some(_) => {
                        return error(ErrorCode::RuntimeFailure, "pane already has a session");
                    }
                    None => return error(ErrorCode::PaneNotFound, "pane does not exist"),
                }
                if self.pending.values().any(|pending| matches!(pending.target, PendingTarget::Restore { pane: current, .. } if current == pane)) { return error(ErrorCode::RuntimeFailure, "pane launch is already pending"); }
                config.working_directory = if directory.is_dir() {
                    directory
                } else if self.coordinator.home_directory_available() {
                    self.coordinator.home_directory().clone()
                } else {
                    return error(ErrorCode::RuntimeFailure, "home directory is unavailable");
                };
                PendingTarget::Restore { project, tab, pane }
            }
            _ => unreachable!("only create commands reach prepare_launch"),
        };
        let Some(queue) = &self.spawns else {
            return error(ErrorCode::RuntimeFailure, "terminal launch queue is closed");
        };
        self.next_operation += 1;
        let operation_id = self.next_operation;
        let session = SessionId::new();
        let (project, tab, pane) = match &target {
            PendingTarget::Project {
                project, tab, pane, ..
            }
            | PendingTarget::Tab {
                project, tab, pane, ..
            }
            | PendingTarget::Restore { project, tab, pane }
            | PendingTarget::Split {
                project, tab, pane, ..
            } => (*project, *tab, *pane),
        };
        let credential = self
            .credentials
            .as_ref()
            .map(|credentials| (credentials.new_session_token(), project));
        let mut env = HashMap::new();
        if let (Some(socket), Some((token, _))) = (&self.socket_path, &credential) {
            env.insert(
                "OMATERM_SOCKET".into(),
                socket.to_string_lossy().into_owned(),
            );
            env.insert("OMATERM_TOKEN".into(), token.clone());
            env.insert("OMATERM_PROJECT_ID".into(), project.0.to_string());
            env.insert("OMATERM_TAB_ID".into(), tab.0.to_string());
            env.insert("OMATERM_PANE_ID".into(), pane.0.to_string());
            env.insert("OMATERM_SESSION_ID".into(), session.0.to_string());
        }
        if let Err(issue) = queue.try_spawn_with_env(operation_id, session, config, env) {
            return error(ErrorCode::RuntimeFailure, &issue.to_string());
        }
        self.pending.insert(
            operation_id,
            PendingLaunch {
                session,
                target,
                context,
                credential,
            },
        );
        DispatchOutcome {
            result: ok(CommandOutput::Pending { operation_id }),
            effects: vec![],
        }
    }

    fn finish_launch(&mut self, completion: SpawnCompletion) -> DispatchOutcome {
        let Some(pending) = self.pending.remove(&completion.operation_id) else {
            if let Ok(session) = completion.result {
                dispose_session(session);
            }
            return DispatchOutcome {
                result: err(ErrorCode::RuntimeFailure, "launch was cancelled"),
                effects: vec![],
            };
        };
        if pending.session != completion.session_id {
            if let Ok(session) = completion.result {
                dispose_session(session);
            }
            return DispatchOutcome {
                result: err(ErrorCode::RuntimeFailure, "launch identity mismatch"),
                effects: vec![],
            };
        }
        let session = match completion.result {
            Ok(session) => session,
            Err(issue) => {
                return DispatchOutcome {
                    result: err(ErrorCode::RuntimeFailure, issue.to_string()),
                    effects: vec![],
                };
            }
        };
        if let CommandContext::Project(scope) = pending.context {
            let project = match &pending.target {
                PendingTarget::Project { .. } => None,
                PendingTarget::Tab { project, .. }
                | PendingTarget::Split { project, .. }
                | PendingTarget::Restore { project, .. } => Some(*project),
            };
            if project != Some(scope) || self.coordinator.window().project(scope).is_none() {
                dispose_session(session);
                return DispatchOutcome {
                    result: err(ErrorCode::CrossProjectDenied, "project scope has changed"),
                    effects: vec![],
                };
            }
        }
        let id = session.id();
        let result = match pending.target {
            PendingTarget::Project {
                previous,
                project,
                tab,
                pane,
                name,
                directory,
            } => self
                .coordinator
                .commit_project_session(ProjectSessionCommit {
                    expected_selected_project: previous,
                    project_id: project,
                    tab_id: tab,
                    pane_id: pane,
                    name,
                    directory,
                    session,
                })
                .map(|_| CommandOutput::ProjectCreated {
                    project,
                    tab,
                    pane,
                    session: id,
                }),
            PendingTarget::Tab {
                previous,
                project,
                tab,
                pane,
                name,
                terminal_create,
            } => self
                .coordinator
                .commit_tab_session(previous, project, tab, pane, name, session)
                .map(|_| {
                    if terminal_create {
                        CommandOutput::TerminalCreated {
                            tab,
                            pane,
                            session: id,
                        }
                    } else {
                        CommandOutput::TabCreated {
                            tab,
                            pane,
                            session: id,
                        }
                    }
                }),
            PendingTarget::Split {
                previous,
                selected_tab,
                project,
                tab,
                focus,
                source,
                target,
                direction,
                pane,
            } => self
                .coordinator
                .commit_split_session(SplitSessionCommit {
                    expected_selected_project: previous,
                    expected_selected_tab: selected_tab,
                    expected_project: project,
                    expected_tab: tab,
                    expected_focus: focus,
                    target,
                    expected_source: source,
                    direction,
                    new_pane: pane,
                    session,
                })
                .map(|_| CommandOutput::PaneSplit { pane, session: id }),
            PendingTarget::Restore { project, tab, pane } => self
                .coordinator
                .commit_restored_session(project, tab, pane, session)
                .map(|_| CommandOutput::Unit),
        };
        match result {
            Ok(output) => {
                if let (Some(credentials), Some((token, project))) =
                    (&mut self.credentials, pending.credential)
                {
                    credentials.publish(token, id, project);
                }
                DispatchOutcome {
                    result: ok(output),
                    effects: vec![
                        CommandEffect::SessionStarted(id),
                        CommandEffect::WorkspaceChanged,
                        CommandEffect::PersistenceDirty,
                    ],
                }
            }
            Err(issue) => DispatchOutcome {
                result: coordinator_error(issue),
                effects: vec![],
            },
        }
    }

    fn dispatch_valid(
        &mut self,
        context: CommandContext,
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
                    .filter(|p| !matches!(context, CommandContext::Project(scope) if p.id != scope))
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
            OmaCommand::Project(ProjectCommand::Create { .. }) => {
                // Creation is asynchronous: dispatch_async routes this to
                // prepare_launch. Reaching here means the single-path rule
                // was bypassed.
                err(
                    ErrorCode::RuntimeFailure,
                    "project creation must go through the async launch path",
                )
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
                if matches!(context, CommandContext::Project(scope) if scope != project) {
                    return err(ErrorCode::CrossProjectDenied, "outside project scope");
                }
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
            OmaCommand::Tab(TabCommand::Create { .. }) => err(
                ErrorCode::RuntimeFailure,
                "tab creation must go through the async launch path",
            ),
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
                    if matches!(context, CommandContext::Project(scope) if scope != project.id) {
                        continue;
                    }
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
            OmaCommand::Pane(PaneCommand::Split { .. }) => err(
                ErrorCode::RuntimeFailure,
                "pane split must go through the async launch path",
            ),
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
            OmaCommand::Terminal(TerminalCommand::Create { .. }) => err(
                ErrorCode::RuntimeFailure,
                "terminal creation must go through the async launch path",
            ),
            OmaCommand::Terminal(TerminalCommand::RestorePane { .. }) => err(
                ErrorCode::RuntimeFailure,
                "restored pane launch must go through the async launch path",
            ),
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
                    if matches!(context, CommandContext::Project(scope) if scope != p.id) {
                        continue;
                    }
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
            OmaCommand::Terminal(TerminalCommand::RunCommand { session, argv }) => {
                let Some(handle) = self.coordinator.registry().get(session) else {
                    return err(ErrorCode::SessionExited, "terminal session is unavailable");
                };
                match handle.lock() {
                    Ok(mut terminal) if terminal.exited().is_none() => {
                        match terminal.run_argv(&argv) {
                            Ok(()) => ok(Out::RunSubmitted),
                            Err(omaterm_terminal::RunCommandError::UnsupportedShell) => err(
                                ErrorCode::UnsupportedOperation,
                                "terminal.run is supported only by OmaTerm-integrated Bash sessions",
                            ),
                            Err(omaterm_terminal::RunCommandError::ShellBusy) => err(
                                ErrorCode::ShellBusy,
                                "shell is not at a confirmed ready prompt",
                            ),
                            Err(omaterm_terminal::RunCommandError::EmptyArgv) => err(
                                ErrorCode::InvalidRequest,
                                "argv must contain at least one argument",
                            ),
                            Err(omaterm_terminal::RunCommandError::Io(error)) => {
                                err(ErrorCode::RuntimeFailure, error.to_string())
                            }
                        }
                    }
                    _ => err(ErrorCode::SessionExited, "terminal session has exited"),
                }
            }
            OmaCommand::Terminal(TerminalCommand::Clear { .. }) => err(
                ErrorCode::UnsupportedOperation,
                "terminal.clear is not implemented",
            ),
        }
    }
}

fn dispose_session(mut session: TerminalSession) {
    std::thread::spawn(move || {
        let _ = session.shutdown();
    });
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
        Pane, PaneCommand, PaneTree, Project, ProjectCommand, SessionId, SplitDirection, SplitId,
        Tab, TerminalCommand, WorkspaceWindow,
    };
    use omaterm_terminal::TerminalConfig;

    fn router() -> CommandRouter {
        CommandRouter::new(WorkspaceCoordinator::new(std::env::temp_dir()))
    }

    #[test]
    fn cancelled_and_duplicate_completions_publish_nothing() {
        let mut router = router();
        let receipt = router.dispatch_async(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Create {
                name: None,
                directory: Some(std::env::temp_dir()),
            }),
        );
        let CommandResult::Ok(CommandOutput::Pending { operation_id }) = receipt.result else {
            panic!("expected pending receipt");
        };
        assert!(receipt.effects.is_empty());
        router.cancel_launch(operation_id);
        // The worker still completes; the owner must reject it without effects.
        let start = std::time::Instant::now();
        loop {
            if let Some((id, outcome)) = router.poll_launches().into_iter().next() {
                assert_eq!(id, operation_id);
                assert!(matches!(outcome.result, CommandResult::Err(_)));
                assert!(outcome.effects.is_empty());
                break;
            }
            assert!(start.elapsed() < std::time::Duration::from_secs(5));
            std::thread::sleep(std::time::Duration::from_millis(2));
        }
        assert!(router.registry().is_empty());
        assert!(router.window().projects.is_empty());
        // A late duplicate for the same operation is also rejected cleanly.
        let duplicate = omaterm_terminal::SpawnCompletion {
            operation_id,
            session_id: SessionId::new(),
            result: Err(omaterm_terminal::SessionError::InvalidSize { cols: 80, rows: 24 }),
        };
        let outcome = router.finish_launch(duplicate);
        assert!(matches!(outcome.result, CommandResult::Err(_)));
        assert!(outcome.effects.is_empty());
        assert!(router.registry().is_empty());
    }

    #[test]
    fn async_split_waits_for_owner_commit_and_rejects_a_closed_target() {
        let mut router = router();
        let created = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Create {
                name: None,
                directory: Some(std::env::temp_dir()),
            }),
        );
        let CommandResult::Ok(CommandOutput::ProjectCreated { project, pane, .. }) = created.result
        else {
            panic!("project creation");
        };
        let receipt = router.dispatch_async(
            CommandContext::LocalUser,
            OmaCommand::Pane(PaneCommand::Split {
                target: pane,
                direction: SplitDirection::Right,
            }),
        );
        let CommandResult::Ok(CommandOutput::Pending { operation_id }) = receipt.result else {
            panic!("expected pending receipt");
        };
        assert!(receipt.effects.is_empty());
        assert_eq!(
            router.window().project(project).unwrap().tabs[0]
                .tree
                .panes()
                .len(),
            1
        );
        let closed = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Delete { project }),
        );
        for effect in closed.effects {
            if let CommandEffect::SessionClosed(pane) = effect
                && let Some(handle) = pane.handle
            {
                let _ = handle.lock().map(|mut session| session.shutdown());
            }
        }
        let start = std::time::Instant::now();
        loop {
            if let Some((id, outcome)) = router.poll_launches().into_iter().next() {
                assert_eq!(id, operation_id);
                assert!(matches!(outcome.result, CommandResult::Err(_)));
                assert!(outcome.effects.is_empty());
                assert!(router.registry().is_empty());
                break;
            }
            assert!(start.elapsed() < std::time::Duration::from_secs(5));
            std::thread::sleep(std::time::Duration::from_millis(2));
        }
    }

    #[test]
    fn project_context_filters_lists_and_denies_foreign_targets_before_effects() {
        let mut router = router();
        let create = |router: &mut CommandRouter| {
            router.dispatch(
                CommandContext::LocalUser,
                OmaCommand::Project(ProjectCommand::Create {
                    name: None,
                    directory: Some(std::env::temp_dir()),
                }),
            )
        };
        let CommandResult::Ok(CommandOutput::ProjectCreated {
            project: a,
            pane: a_pane,
            ..
        }) = create(&mut router).result
        else {
            panic!("project A");
        };
        let CommandResult::Ok(CommandOutput::ProjectCreated {
            project: b,
            pane: b_pane,
            session: b_session,
            ..
        }) = create(&mut router).result
        else {
            panic!("project B");
        };
        let scope = CommandContext::Project(a);
        let projects = router.dispatch_async(scope, OmaCommand::Project(ProjectCommand::List));
        assert!(
            matches!(projects.result, CommandResult::Ok(CommandOutput::ProjectList(ref items)) if items.len() == 1 && items[0].id == a)
        );
        let panes = router.dispatch_async(scope, OmaCommand::Pane(PaneCommand::List { tab: None }));
        assert!(
            matches!(panes.result, CommandResult::Ok(CommandOutput::PaneList(ref items)) if items.len() == 1 && items[0].id == a_pane)
        );
        let terminals = router.dispatch_async(scope, OmaCommand::Terminal(TerminalCommand::List));
        assert!(
            matches!(terminals.result, CommandResult::Ok(CommandOutput::TerminalList(ref items)) if items.len() == 1 && items[0].project == a)
        );
        for command in [
            OmaCommand::Pane(PaneCommand::Split {
                target: b_pane,
                direction: SplitDirection::Right,
            }),
            OmaCommand::Terminal(TerminalCommand::SendBytes {
                session: b_session,
                data: b"secret".to_vec(),
            }),
            OmaCommand::Project(ProjectCommand::Select { project: b }),
        ] {
            let denied = router.dispatch_async(scope, command);
            assert!(matches!(denied.result, CommandResult::Err(_)));
            assert!(denied.effects.is_empty());
        }
        for project in [a, b] {
            let close = router.dispatch(
                CommandContext::LocalUser,
                OmaCommand::Project(ProjectCommand::Delete { project }),
            );
            for effect in close.effects {
                if let CommandEffect::SessionClosed(pane) = effect
                    && let Some(handle) = pane.handle
                {
                    let _ = handle.lock().map(|mut session| session.shutdown());
                }
            }
        }
    }

    #[test]
    fn credentialed_launch_injects_child_env_and_revokes_on_owner_close() {
        let root = std::env::temp_dir().join(format!("omaterm-router-cred-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir(&root).unwrap();
        let socket = root.join("omaterm.sock");
        let mut router = router();
        router.enable_credentials(&socket).unwrap();
        let local = std::fs::read_to_string(socket.with_extension("credential")).unwrap();
        let created = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Create {
                name: None,
                directory: Some(std::env::temp_dir()),
            }),
        );
        let CommandResult::Ok(CommandOutput::ProjectCreated {
            project,
            tab,
            pane,
            session,
        }) = created.result
        else {
            panic!("credentialed project creation");
        };
        let pid = router
            .registry()
            .get(session)
            .unwrap()
            .lock()
            .unwrap()
            .child_pid();
        let environ = std::fs::read(format!("/proc/{pid}/environ")).unwrap();
        let vars: std::collections::HashMap<_, _> = environ
            .split(|b| *b == 0)
            .filter_map(|entry| {
                let (k, v) = entry.split_at(entry.iter().position(|b| *b == b'=')?);
                Some((
                    String::from_utf8_lossy(k).into_owned(),
                    String::from_utf8_lossy(&v[1..]).into_owned(),
                ))
            })
            .collect();
        assert_eq!(
            vars.get("OMATERM_SOCKET").map(String::as_str),
            Some(socket.to_string_lossy().as_ref())
        );
        let token = vars.get("OMATERM_TOKEN").cloned().unwrap();
        assert_ne!(
            token, local,
            "child must not receive the local-user credential"
        );
        assert_eq!(
            vars.get("OMATERM_PROJECT_ID").map(String::as_str),
            Some(project.0.to_string().as_str())
        );
        assert_eq!(
            vars.get("OMATERM_TAB_ID").map(String::as_str),
            Some(tab.0.to_string().as_str())
        );
        assert_eq!(
            vars.get("OMATERM_PANE_ID").map(String::as_str),
            Some(pane.0.to_string().as_str())
        );
        assert_eq!(
            vars.get("OMATERM_SESSION_ID").map(String::as_str),
            Some(session.0.to_string().as_str())
        );
        let scoped = omaterm_protocol::CapabilityToken::from_secret(token).unwrap();
        assert_eq!(
            router.authenticate(Some(&scoped)),
            Some(CommandContext::Project(project))
        );
        // Owner close revokes the session credential.
        let closed = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Delete { project }),
        );
        for effect in closed.effects {
            if let CommandEffect::SessionClosed(pane) = effect {
                if let Some(id) = pane.session_id {
                    router.revoke_session(id);
                }
                if let Some(handle) = pane.handle {
                    let _ = handle.lock().map(|mut session| session.shutdown());
                }
            }
        }
        assert_eq!(router.authenticate(Some(&scoped)), None);
        assert_eq!(router.authenticate(None), None);
        drop(router);
        assert!(!socket.with_extension("credential").exists());
        let _ = std::fs::remove_dir_all(root);
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
    fn command_targets_report_stale_ids_and_missing_run_session() {
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
                code: ErrorCode::SessionExited,
                ..
            })
        ));
    }

    #[test]
    fn command_variant_matrix_covers_remaining_selectors_and_failures() {
        let mut router = router();
        let created = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Create {
                name: None,
                directory: Some(std::env::temp_dir()),
            }),
        );
        let CommandResult::Ok(CommandOutput::ProjectCreated {
            project,
            tab,
            pane,
            session,
        }) = created.result
        else {
            panic!("project creation");
        };
        // Rename success + stale rename.
        let renamed = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Rename {
                project,
                name: "renamed".into(),
            }),
        );
        assert!(matches!(
            renamed.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        assert!(matches!(
            renamed.effects.as_slice(),
            [
                CommandEffect::WorkspaceChanged,
                CommandEffect::PersistenceDirty,
            ]
        ));
        let stale_rename = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Rename {
                project: omaterm_core::ProjectId::new(),
                name: "x".into(),
            }),
        );
        assert!(matches!(
            stale_rename.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::ProjectNotFound,
                ..
            })
        ));
        assert!(stale_rename.effects.is_empty());
        // Rename validation failure (blank name) has no effects.
        let bad_rename = router.dispatch_async(
            CommandContext::LocalUser,
            OmaCommand::Tab(TabCommand::Rename {
                tab,
                name: "  ".into(),
            }),
        );
        assert!(matches!(
            bad_rename.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::InvalidRequest,
                ..
            })
        ));
        assert!(bad_rename.effects.is_empty());
        // Tab rename success.
        let tab_renamed = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Tab(TabCommand::Rename {
                tab,
                name: "tests".into(),
            }),
        );
        assert!(matches!(
            tab_renamed.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        // Stale tab select / close / focus targets.
        for outcome in [
            router.dispatch_async(
                CommandContext::LocalUser,
                OmaCommand::Tab(TabCommand::Select {
                    tab: omaterm_core::TabId::new(),
                }),
            ),
            router.dispatch_async(
                CommandContext::LocalUser,
                OmaCommand::Tab(TabCommand::Close {
                    tab: omaterm_core::TabId::new(),
                }),
            ),
            router.dispatch_async(
                CommandContext::LocalUser,
                OmaCommand::Pane(PaneCommand::Focus {
                    pane: omaterm_core::PaneId::new(),
                }),
            ),
            router.dispatch_async(
                CommandContext::LocalUser,
                OmaCommand::Pane(PaneCommand::Close {
                    pane: omaterm_core::PaneId::new(),
                }),
            ),
            router.dispatch_async(
                CommandContext::LocalUser,
                OmaCommand::Pane(PaneCommand::Resize {
                    split: omaterm_core::SplitId::new(),
                    fraction: 0.5,
                }),
            ),
            router.dispatch_async(
                CommandContext::LocalUser,
                OmaCommand::Pane(PaneCommand::Equalize {
                    tab: omaterm_core::TabId::new(),
                }),
            ),
            router.dispatch_async(
                CommandContext::LocalUser,
                OmaCommand::Project(ProjectCommand::Select {
                    project: omaterm_core::ProjectId::new(),
                }),
            ),
            router.dispatch_async(
                CommandContext::LocalUser,
                OmaCommand::Project(ProjectCommand::Delete {
                    project: omaterm_core::ProjectId::new(),
                }),
            ),
        ] {
            assert!(matches!(outcome.result, CommandResult::Err(_)));
            assert!(outcome.effects.is_empty());
        }
        // FocusDirection with a single pane: Ok(Unit), no state change.
        let dir = router.dispatch_async(
            CommandContext::LocalUser,
            OmaCommand::Pane(PaneCommand::FocusDirection {
                direction: SplitDirection::Right,
            }),
        );
        assert!(matches!(dir.result, CommandResult::Ok(CommandOutput::Unit)));
        assert!(dir.effects.is_empty());
        // EqualizeSelected and ResizeFocused bounds.
        let eq = router.dispatch_async(
            CommandContext::LocalUser,
            OmaCommand::Pane(PaneCommand::EqualizeSelected),
        );
        assert!(matches!(eq.result, CommandResult::Ok(CommandOutput::Unit)));
        let bad_amount = router.dispatch_async(
            CommandContext::LocalUser,
            OmaCommand::Pane(PaneCommand::ResizeFocused { amount: 5.0 }),
        );
        assert!(matches!(
            bad_amount.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::InvalidRequest,
                ..
            })
        ));
        assert!(bad_amount.effects.is_empty());
        // Terminal validation failures carry no effects.
        let over_read = router.dispatch_async(
            CommandContext::LocalUser,
            OmaCommand::Terminal(TerminalCommand::ReadVisible {
                session,
                max_lines: 5000,
                max_columns: 80,
            }),
        );
        assert!(matches!(
            over_read.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::InvalidRequest,
                ..
            })
        ));
        assert!(over_read.effects.is_empty());
        let over_send = router.dispatch_async(
            CommandContext::LocalUser,
            OmaCommand::Terminal(TerminalCommand::SendBytes {
                session,
                data: vec![b'x'; 9000],
            }),
        );
        assert!(matches!(
            over_send.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::InvalidRequest,
                ..
            })
        ));
        assert!(over_send.effects.is_empty());
        let empty_run = router.dispatch_async(
            CommandContext::LocalUser,
            OmaCommand::Terminal(TerminalCommand::RunCommand {
                session,
                argv: vec![],
            }),
        );
        assert!(matches!(
            empty_run.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::InvalidRequest,
                ..
            })
        ));
        assert!(empty_run.effects.is_empty());
        // Defensive single-path guard: synchronous creation arms are unreachable.
        let guarded = router.dispatch_valid(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Create {
                name: None,
                directory: None,
            }),
            &mut Vec::new(),
        );
        assert!(matches!(
            guarded,
            CommandResult::Err(CommandError {
                code: ErrorCode::RuntimeFailure,
                ..
            })
        ));
        // Cleanup.
        let closed = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Delete { project }),
        );
        for effect in closed.effects {
            if let CommandEffect::SessionClosed(pane) = effect
                && let Some(handle) = pane.handle
            {
                let _ = handle.lock().map(|mut session| session.shutdown());
            }
        }
        let _ = (tab, pane);
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

    #[test]
    fn project_tab_pane_terminal_queries_and_effect_order_are_consistent() {
        let mut router = router();
        let created = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Create {
                name: Some("dispatch-contract".into()),
                directory: Some(std::env::temp_dir()),
            }),
        );
        let CommandResult::Ok(CommandOutput::ProjectCreated {
            project,
            tab: initial_tab,
            pane: _initial_pane,
            session: initial_session,
        }) = created.result
        else {
            panic!("project creation should return its owned IDs");
        };
        assert!(matches!(created.effects.as_slice(), [
            CommandEffect::SessionStarted(id),
            CommandEffect::WorkspaceChanged,
            CommandEffect::PersistenceDirty,
        ] if *id == initial_session));

        let projects = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::List),
        );
        assert!(matches!(
            projects.result,
            CommandResult::Ok(CommandOutput::ProjectList(ref items))
                if items.len() == 1 && items[0].id == project && items[0].selected
        ));
        assert!(projects.effects.is_empty());

        let renamed = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Rename {
                project,
                name: "renamed-project".into(),
            }),
        );
        assert!(matches!(
            renamed.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        assert!(matches!(
            renamed.effects.as_slice(),
            [
                CommandEffect::WorkspaceChanged,
                CommandEffect::PersistenceDirty,
            ]
        ));

        let new_tab = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Tab(TabCommand::Create {
                project,
                name: Some("second-tab".into()),
            }),
        );
        let CommandResult::Ok(CommandOutput::TabCreated { tab, pane, session }) = new_tab.result
        else {
            panic!("tab creation should return its owned IDs");
        };
        assert_ne!(tab, initial_tab);
        assert!(matches!(new_tab.effects.as_slice(), [
            CommandEffect::SessionStarted(id),
            CommandEffect::WorkspaceChanged,
            CommandEffect::PersistenceDirty,
        ] if *id == session));

        let tabs = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Tab(TabCommand::List { project }),
        );
        assert!(matches!(
            tabs.result,
            CommandResult::Ok(CommandOutput::TabList(ref items))
                if items.len() == 2
                    && items.iter().any(|item| item.id == initial_tab)
                    && items.iter().any(|item| item.id == tab && item.selected)
        ));
        assert!(tabs.effects.is_empty());

        let rename_tab = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Tab(TabCommand::Rename {
                tab,
                name: "renamed-tab".into(),
            }),
        );
        assert!(matches!(
            rename_tab.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        assert!(matches!(
            rename_tab.effects.as_slice(),
            [
                CommandEffect::WorkspaceChanged,
                CommandEffect::PersistenceDirty,
            ]
        ));

        let panes = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Pane(PaneCommand::List { tab: Some(tab) }),
        );
        assert!(matches!(
            panes.result,
            CommandResult::Ok(CommandOutput::PaneList(ref items))
                if items.len() == 1 && items[0].id == pane && items[0].session == Some(session)
        ));

        let split = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Pane(PaneCommand::Split {
                target: pane,
                direction: omaterm_core::SplitDirection::Right,
            }),
        );
        let CommandResult::Ok(CommandOutput::PaneSplit {
            pane: split_pane,
            session: split_session,
        }) = split.result
        else {
            panic!("split should return the new pane and session");
        };
        assert!(matches!(split.effects.as_slice(), [
            CommandEffect::SessionStarted(id),
            CommandEffect::WorkspaceChanged,
            CommandEffect::PersistenceDirty,
        ] if *id == split_session));

        let split_id = router
            .tree()
            .ancestors(split_pane)
            .and_then(|ids| ids.first().copied())
            .expect("new split has an ancestor split ID");
        let resized = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Pane(PaneCommand::Resize {
                split: split_id,
                fraction: 0.6,
            }),
        );
        assert!(matches!(
            resized.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        assert!(matches!(
            resized.effects.as_slice(),
            [
                CommandEffect::WorkspaceChanged,
                CommandEffect::PersistenceDirty,
            ]
        ));

        let equalized = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Pane(PaneCommand::Equalize { tab }),
        );
        assert!(matches!(
            equalized.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        assert!(matches!(
            equalized.effects.as_slice(),
            [
                CommandEffect::WorkspaceChanged,
                CommandEffect::PersistenceDirty,
            ]
        ));

        let terminal_list = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Terminal(TerminalCommand::List),
        );
        assert!(matches!(
            terminal_list.result,
            CommandResult::Ok(CommandOutput::TerminalList(ref items))
                if items.len() == 3
                    && items.iter().any(|item| item.id == initial_session)
                    && items.iter().any(|item| item.id == session)
                    && items.iter().any(|item| item.id == split_session)
        ));

        let terminal_output = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Terminal(TerminalCommand::ReadVisible {
                session,
                max_lines: 24,
                max_columns: 80,
            }),
        );
        assert!(matches!(
            terminal_output.result,
            CommandResult::Ok(CommandOutput::TerminalOutput {
                lines: 24,
                columns: 80,
                ..
            })
        ));

        let closed = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Pane(PaneCommand::Close { pane: split_pane }),
        );
        assert!(matches!(
            closed.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        assert!(matches!(
            closed.effects.as_slice(),
            [
                CommandEffect::SessionClosed(_),
                CommandEffect::WorkspaceChanged,
                CommandEffect::PersistenceDirty,
            ]
        ));
        for effect in closed.effects {
            if let CommandEffect::SessionClosed(closed) = effect
                && let Some(handle) = closed.handle
            {
                assert!(handle.lock().unwrap().shutdown());
            }
        }

        let deleted = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Delete { project }),
        );
        assert!(matches!(
            deleted.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        assert_eq!(
            deleted
                .effects
                .iter()
                .filter(|effect| matches!(effect, CommandEffect::SessionClosed(_)))
                .count(),
            2
        );
        for effect in deleted.effects {
            if let CommandEffect::SessionClosed(closed) = effect
                && let Some(handle) = closed.handle
            {
                assert!(handle.lock().unwrap().shutdown());
            }
        }
        assert!(matches!(
            router
                .dispatch(
                    CommandContext::LocalUser,
                    OmaCommand::Project(ProjectCommand::Select { project }),
                )
                .result,
            CommandResult::Err(CommandError {
                code: ErrorCode::ProjectNotFound,
                ..
            })
        ));
    }

    #[test]
    fn terminal_byte_send_does_not_mark_workspace_persistence_dirty() {
        let mut router = router();
        let created = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Create {
                name: None,
                directory: Some(std::env::temp_dir()),
            }),
        );
        let CommandResult::Ok(CommandOutput::ProjectCreated {
            project,
            pane,
            session,
            ..
        }) = created.result
        else {
            panic!("project creation should provide the target terminal");
        };
        let sent = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Terminal(TerminalCommand::SendBytes {
                session,
                data: b"echo router-input\n".to_vec(),
            }),
        );
        assert!(matches!(
            sent.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        assert!(
            sent.effects.is_empty(),
            "terminal bytes are not workspace mutations"
        );

        let stale = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Terminal(TerminalCommand::SendBytes {
                session: SessionId::new(),
                data: vec![],
            }),
        );
        assert!(matches!(
            stale.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::SessionExited,
                ..
            })
        ));
        assert!(stale.effects.is_empty());

        let closed = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Pane(PaneCommand::Close { pane }),
        );
        assert!(matches!(
            closed.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        for effect in closed.effects {
            if let CommandEffect::SessionClosed(closed) = effect
                && let Some(handle) = closed.handle
            {
                assert!(handle.lock().unwrap().shutdown());
            }
        }
        let _ = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Delete { project }),
        );
    }

    #[test]
    fn terminal_run_requires_bash_prompt_and_only_acknowledges_submission() {
        let mut router = router();
        let session = router
            .registry_mut()
            .create(TerminalConfig {
                working_directory: std::env::temp_dir(),
                shell: Some("/bin/bash".into()),
                cols: 80,
                rows: 24,
            })
            .expect("spawn integrated bash");
        let command = || {
            OmaCommand::Terminal(TerminalCommand::RunCommand {
                session,
                argv: vec!["printf".into(), "M7_SUBMITTED".into()],
            })
        };

        let early = router.dispatch(CommandContext::LocalUser, command());
        assert!(matches!(
            early.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::ShellBusy,
                ..
            })
        ));
        assert!(early.effects.is_empty());

        let start = std::time::Instant::now();
        while start.elapsed() < std::time::Duration::from_secs(5) {
            let handle = router.registry().get(session).unwrap();
            let mut terminal = handle.lock().unwrap();
            let _ = terminal.pump();
            if terminal.prompt_ready() {
                break;
            }
            std::thread::sleep(std::time::Duration::from_millis(20));
        }
        assert!(
            router
                .registry()
                .get(session)
                .unwrap()
                .lock()
                .unwrap()
                .prompt_ready(),
            "Bash prompt hook should establish readiness"
        );

        let submitted = router.dispatch(CommandContext::LocalUser, command());
        assert_eq!(
            submitted.result,
            CommandResult::Ok(CommandOutput::RunSubmitted)
        );
        assert!(submitted.effects.is_empty());
        assert!(router.registry_mut().close(session).unwrap());
    }

    #[test]
    fn terminal_run_returns_unsupported_for_a_valid_non_bash_session() {
        let mut router = router();
        let session = router
            .registry_mut()
            .create(TerminalConfig {
                working_directory: std::env::temp_dir(),
                shell: Some("/bin/sh".into()),
                cols: 80,
                rows: 24,
            })
            .expect("spawn sh");
        let outcome = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Terminal(TerminalCommand::RunCommand {
                session,
                argv: vec!["true".into()],
            }),
        );
        assert!(matches!(
            outcome.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::UnsupportedOperation,
                ..
            })
        ));
        assert!(outcome.effects.is_empty());
        assert!(router.registry_mut().close(session).unwrap());
    }

    #[test]
    fn selection_focus_and_tab_close_commands_preserve_effect_contracts() {
        let mut router = router();
        let create_a = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Create {
                name: Some("project-a".into()),
                directory: Some(std::env::temp_dir()),
            }),
        );
        let CommandResult::Ok(CommandOutput::ProjectCreated {
            project: project_a,
            tab: tab_a,
            pane: pane_a,
            ..
        }) = create_a.result
        else {
            panic!("project A creation should succeed");
        };
        let create_b = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Create {
                name: Some("project-b".into()),
                directory: Some(std::env::temp_dir()),
            }),
        );
        let CommandResult::Ok(CommandOutput::ProjectCreated {
            project: project_b, ..
        }) = create_b.result
        else {
            panic!("project B creation should succeed");
        };

        let select_a = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Select { project: project_a }),
        );
        assert!(matches!(
            select_a.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        assert!(matches!(
            select_a.effects.as_slice(),
            [
                CommandEffect::WorkspaceChanged,
                CommandEffect::PersistenceDirty,
            ]
        ));

        let select_tab = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Tab(TabCommand::Select { tab: tab_a }),
        );
        assert!(matches!(
            select_tab.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        assert!(matches!(
            select_tab.effects.as_slice(),
            [
                CommandEffect::WorkspaceChanged,
                CommandEffect::PersistenceDirty,
            ]
        ));

        let second_tab = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Tab(TabCommand::Create {
                project: project_a,
                name: None,
            }),
        );
        let CommandResult::Ok(CommandOutput::TabCreated { tab: tab_b, .. }) = second_tab.result
        else {
            panic!("second tab should be created");
        };
        let close_tab = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Tab(TabCommand::Close { tab: tab_b }),
        );
        assert!(matches!(
            close_tab.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        assert!(matches!(
            close_tab.effects.as_slice(),
            [
                CommandEffect::SessionClosed(_),
                CommandEffect::WorkspaceChanged,
                CommandEffect::PersistenceDirty,
            ]
        ));
        for effect in close_tab.effects {
            if let CommandEffect::SessionClosed(closed) = effect
                && let Some(handle) = closed.handle
            {
                assert!(handle.lock().unwrap().shutdown());
            }
        }

        let split = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Pane(PaneCommand::Split {
                target: pane_a,
                direction: SplitDirection::Right,
            }),
        );
        let CommandResult::Ok(CommandOutput::PaneSplit { pane: pane_b, .. }) = split.result else {
            panic!("pane split should succeed");
        };
        let focus = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Pane(PaneCommand::Focus { pane: pane_a }),
        );
        assert!(matches!(
            focus.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        assert!(matches!(
            focus.effects.as_slice(),
            [
                CommandEffect::WorkspaceChanged,
                CommandEffect::PersistenceDirty,
            ]
        ));
        let focus_direction = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Pane(PaneCommand::FocusDirection {
                direction: SplitDirection::Right,
            }),
        );
        assert!(matches!(
            focus_direction.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        assert!(matches!(
            focus_direction.effects.as_slice(),
            [
                CommandEffect::WorkspaceChanged,
                CommandEffect::PersistenceDirty,
            ]
        ));
        let resize = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Pane(PaneCommand::ResizeFocused { amount: 0.05 }),
        );
        assert!(matches!(
            resize.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        assert!(matches!(
            resize.effects.as_slice(),
            [
                CommandEffect::WorkspaceChanged,
                CommandEffect::PersistenceDirty,
            ]
        ));
        let equalize = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Pane(PaneCommand::EqualizeSelected),
        );
        assert!(matches!(
            equalize.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        assert!(matches!(
            equalize.effects.as_slice(),
            [
                CommandEffect::WorkspaceChanged,
                CommandEffect::PersistenceDirty,
            ]
        ));

        let close_pane = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Pane(PaneCommand::Close { pane: pane_b }),
        );
        assert!(matches!(
            close_pane.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        for effect in close_pane.effects {
            if let CommandEffect::SessionClosed(closed) = effect
                && let Some(handle) = closed.handle
            {
                assert!(handle.lock().unwrap().shutdown());
            }
        }

        let created_terminal = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Terminal(TerminalCommand::Create {
                project: project_a,
                directory: Some(std::env::temp_dir()),
            }),
        );
        assert!(matches!(
            created_terminal.result,
            CommandResult::Ok(CommandOutput::TerminalCreated { .. })
        ));
        assert!(matches!(
            created_terminal.effects.as_slice(),
            [
                CommandEffect::SessionStarted(_),
                CommandEffect::WorkspaceChanged,
                CommandEffect::PersistenceDirty,
            ]
        ));

        let select_b = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Select { project: project_b }),
        );
        assert!(matches!(
            select_b.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        let close_b = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Delete { project: project_b }),
        );
        for effect in close_b.effects {
            if let CommandEffect::SessionClosed(closed) = effect
                && let Some(handle) = closed.handle
            {
                assert!(handle.lock().unwrap().shutdown());
            }
        }
        let close_a = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Delete { project: project_a }),
        );
        for effect in close_a.effects {
            if let CommandEffect::SessionClosed(closed) = effect
                && let Some(handle) = closed.handle
            {
                assert!(handle.lock().unwrap().shutdown());
            }
        }
    }

    #[test]
    fn restored_pane_and_explicit_unsupported_clear_have_stable_results() {
        let directory = std::env::temp_dir();
        let pane = Pane::empty();
        let pane_id = pane.id;
        let tab = Tab::new(PaneTree::new(pane), pane_id).unwrap();
        let tab_id = tab.id;
        let mut project_model = Project::new(Some("restored-test".into()), Some(directory.clone()));
        project_model.add_tab(tab).unwrap();
        let project_id = project_model.id;
        let mut window = WorkspaceWindow::new();
        window.add_project(project_model).unwrap();

        let mut router = CommandRouter::new(WorkspaceCoordinator::new(directory.clone()));
        router.restore_window(window).unwrap();
        let restored = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Terminal(TerminalCommand::RestorePane {
                project: project_id,
                tab: tab_id,
                pane: pane_id,
                directory: directory.clone(),
            }),
        );
        assert!(matches!(
            restored.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        let [
            CommandEffect::SessionStarted(session),
            CommandEffect::WorkspaceChanged,
            CommandEffect::PersistenceDirty,
        ] = restored.effects.as_slice()
        else {
            panic!("restore binds the session before publishing ordered effects");
        };

        let clear = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Terminal(TerminalCommand::Clear { session: *session }),
        );
        assert!(matches!(
            clear.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::UnsupportedOperation,
                ..
            })
        ));
        assert!(clear.effects.is_empty());

        let closed = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Delete {
                project: project_id,
            }),
        );
        assert!(matches!(
            closed.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        for effect in closed.effects {
            if let CommandEffect::SessionClosed(closed) = effect
                && let Some(handle) = closed.handle
            {
                assert!(handle.lock().unwrap().shutdown());
            }
        }
    }
}
