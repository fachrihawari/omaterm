use std::path::PathBuf;
use std::sync::{Arc, Mutex};

use omaterm_core::{
    CoreError, Pane, PaneContent, PaneId, PaneTree, Project, ProjectId, SessionId, SplitDirection,
    Tab, TabId, WorkspaceWindow,
};

use crate::registry::{RegistryError, TerminalConfig, TerminalRegistry};
use crate::session::TerminalSession;

static EMPTY_TREE: std::sync::OnceLock<PaneTree> = std::sync::OnceLock::new();

/// Errors from workspace coordination. Stable variants for tests/agents.
#[derive(Debug, thiserror::Error)]
pub enum CoordinatorError {
    #[error("no focused pane")]
    NoFocusedPane,
    #[error("workspace is not empty")]
    WorkspaceNotEmpty,
    #[error("workspace target changed while terminal launch was pending")]
    StaleLaunch,
    #[error("home directory is unavailable for restoring the terminal")]
    HomeUnavailable,
    #[error("session {0:?} is not bound to a pane")]
    SessionPaneNotFound(SessionId),
    #[error(transparent)]
    Registry(#[from] RegistryError),
    #[error(transparent)]
    Core(#[from] CoreError),
}

/// A closed pane with its detached session handle.
///
/// The registry no longer resolves the ID. The caller owns bounded
/// shutdown/reap (off the UI thread); dropping the last `Arc` frees the
/// session and the PTY drop path guarantees SIGHUP.
pub struct ClosedPane {
    pub pane_id: PaneId,
    pub session_id: Option<SessionId>,
    pub handle: Option<Arc<Mutex<TerminalSession>>>,
}

pub struct ClosedSessions(pub Vec<ClosedPane>);

pub struct ProjectSessionCommit {
    pub expected_selected_project: Option<ProjectId>,
    pub project_id: ProjectId,
    pub tab_id: TabId,
    pub pane_id: PaneId,
    pub name: Option<String>,
    pub directory: PathBuf,
    pub session: TerminalSession,
}

pub struct SplitSessionCommit {
    pub expected_selected_project: Option<ProjectId>,
    pub expected_selected_tab: Option<TabId>,
    pub expected_project: ProjectId,
    pub expected_tab: TabId,
    pub expected_focus: PaneId,
    pub target: PaneId,
    pub expected_source: SessionId,
    pub direction: SplitDirection,
    pub new_pane: PaneId,
    pub session: TerminalSession,
}

/// GPUI-free workspace coordination: one recursive [`PaneTree`] whose
/// terminal leaves reference registry-owned sessions by ID.
///
/// The desktop (`apps/omaterm`) owns snapshot/reader UI state and delegates
/// every tree + lifecycle mutation here, so split/close/focus/resize behavior
/// is tested headless and never duplicated in GPUI handlers.
pub struct WorkspaceCoordinator {
    window: WorkspaceWindow,
    registry: TerminalRegistry,
    home_directory: PathBuf,
    home_directory_available: bool,
    launch_directory: PathBuf,
    /// Engine scrollback cap for sessions created after this is set. `None`
    /// keeps the engine default; `Some` comes only from validated config.
    scrollback_lines: Option<usize>,
    /// Program for terminals created after this is set. `None` keeps the
    /// platform default (`$SHELL`, else bash or PowerShell).
    preferred_shell_program: Option<String>,
}

impl WorkspaceCoordinator {
    pub fn new(working_directory: PathBuf) -> Self {
        let home_path = std::env::var_os("HOME")
            .map(PathBuf::from)
            .filter(|path| path.is_dir());
        let home_directory_available = home_path.is_some();
        let home_directory = home_path.unwrap_or_else(std::env::temp_dir);
        let launch_directory = if working_directory.is_dir() {
            working_directory
        } else {
            home_directory.clone()
        };
        Self {
            window: WorkspaceWindow::new(),
            registry: TerminalRegistry::new(),
            home_directory,
            home_directory_available,
            launch_directory,
            scrollback_lines: None,
            preferred_shell_program: None,
        }
    }

    pub fn set_preferred_shell(&mut self, program: Option<String>) {
        self.preferred_shell_program = program.filter(|value| !value.is_empty());
    }

    pub fn preferred_shell_program(&self) -> Option<&str> {
        self.preferred_shell_program.as_deref()
    }

    pub fn window(&self) -> &WorkspaceWindow {
        &self.window
    }
    pub fn home_directory(&self) -> &PathBuf {
        &self.home_directory
    }
    pub fn home_directory_available(&self) -> bool {
        self.home_directory_available
    }
    /// Cap engine scrollback for sessions created from here on. Applies to
    /// new spawns only; live sessions keep their existing engine.
    pub fn set_scrollback_lines(&mut self, lines: Option<usize>) {
        self.scrollback_lines = lines;
    }
    pub fn scrollback_lines(&self) -> Option<usize> {
        self.scrollback_lines
    }
    pub fn rename_project(&mut self, id: ProjectId, name: String) -> Result<(), CoordinatorError> {
        let project = self
            .window
            .project_mut(id)
            .ok_or(CoreError::ProjectNotFound(id))?;
        project.custom_name = Some(name);
        Ok(())
    }
    /// Change a project's base directory. Only future tabs, splits, and
    /// default terminal launches resolve against it; live sessions and
    /// persisted per-pane CWDs are untouched.
    pub fn set_project_directory(
        &mut self,
        id: ProjectId,
        directory: std::path::PathBuf,
    ) -> Result<(), CoordinatorError> {
        let project = self
            .window
            .project_mut(id)
            .ok_or(CoreError::ProjectNotFound(id))?;
        project.pinned_directory = Some(directory);
        Ok(())
    }
    pub fn rename_tab(&mut self, id: TabId, name: String) -> Result<(), CoordinatorError> {
        let tab = self
            .window
            .projects
            .iter_mut()
            .find_map(|project| project.tab_mut(id))
            .ok_or(CoreError::TabNotFound(id))?;
        tab.custom_name = Some(name);
        Ok(())
    }
    pub fn projects(&self) -> &[Project] {
        &self.window.projects
    }
    pub fn selected_project_id(&self) -> Option<ProjectId> {
        self.window.selected_project
    }
    pub fn selected_tab_id(&self) -> Option<TabId> {
        self.active_tab().map(|tab| tab.id)
    }
    pub fn active_project(&self) -> Option<&Project> {
        self.window.selected_project()
    }
    pub fn active_tab(&self) -> Option<&Tab> {
        self.active_project()?.selected_tab()
    }

    pub fn tree(&self) -> &PaneTree {
        self.active_tab()
            .map(|tab| &tab.tree)
            .unwrap_or_else(|| EMPTY_TREE.get_or_init(PaneTree::empty))
    }

    pub fn focused(&self) -> Option<PaneId> {
        self.active_tab().map(|tab| tab.focused_pane)
    }

    pub fn registry(&self) -> &TerminalRegistry {
        &self.registry
    }

    pub fn registry_mut(&mut self) -> &mut TerminalRegistry {
        &mut self.registry
    }

    /// Install a previously validated logical workspace before any restored
    /// terminal sessions are spawned. The persisted tree must not contain live
    /// session references; each terminal pane starts empty and is rebound below.
    pub fn restore_window(&mut self, window: WorkspaceWindow) -> Result<(), CoordinatorError> {
        if !self.registry.is_empty() || !self.window.projects.is_empty() {
            return Err(CoordinatorError::WorkspaceNotEmpty);
        }
        window.validate()?;
        self.window = window;
        Ok(())
    }

    /// Commit a worker-created session as a new project/tab/pane. The caller
    /// supplies identities and the selected-project guard captured at prepare
    /// time. A selection change invalidates the operation instead of letting a
    /// late completion steal focus.
    pub fn commit_project_session(
        &mut self,
        commit: ProjectSessionCommit,
    ) -> Result<SessionId, CoordinatorError> {
        let ProjectSessionCommit {
            expected_selected_project,
            project_id,
            tab_id,
            pane_id,
            name,
            directory,
            session,
        } = commit;
        if self.window.selected_project != expected_selected_project
            || self.window.project(project_id).is_some()
            || self.window.projects.iter().any(|project| {
                project
                    .tabs
                    .iter()
                    .any(|tab| tab.id == tab_id || tab.tree.find(pane_id).is_some())
            })
        {
            return Err(CoordinatorError::StaleLaunch);
        }
        let session_id = session.id();
        self.registry.insert(session)?;
        let pane = Pane {
            id: pane_id,
            content: PaneContent::Terminal(session_id),
        };
        let tab = Tab {
            id: tab_id,
            custom_name: None,
            tree: PaneTree::new(pane),
            focused_pane: pane_id,
        };
        let mut project = Project {
            id: project_id,
            custom_name: name,
            pinned_directory: Some(directory),
            tabs: Vec::new(),
            selected_tab: None,
        };
        let result = project
            .add_tab(tab)
            .and_then(|()| self.window.add_project(project));
        if let Err(error) = result {
            let _ = self.registry.detach(session_id);
            return Err(error.into());
        }
        Ok(session_id)
    }

    /// Commit a worker-created session as a new tab in an existing project.
    pub fn commit_tab_session(
        &mut self,
        expected_selected_project: Option<ProjectId>,
        project_id: ProjectId,
        tab_id: TabId,
        pane_id: PaneId,
        name: Option<String>,
        session: TerminalSession,
    ) -> Result<SessionId, CoordinatorError> {
        if self.window.selected_project != expected_selected_project
            || self.window.project(project_id).is_none()
            || self.window.projects.iter().any(|project| {
                project
                    .tabs
                    .iter()
                    .any(|tab| tab.id == tab_id || tab.tree.find(pane_id).is_some())
            })
        {
            return Err(CoordinatorError::StaleLaunch);
        }
        let session_id = session.id();
        self.registry.insert(session)?;
        let pane = Pane {
            id: pane_id,
            content: PaneContent::Terminal(session_id),
        };
        let tab = Tab {
            id: tab_id,
            custom_name: name,
            tree: PaneTree::new(pane),
            focused_pane: pane_id,
        };
        let project = self
            .window
            .project_mut(project_id)
            .expect("project validated before registry insertion");
        if let Err(error) = project.add_tab(tab) {
            let _ = self.registry.detach(session_id);
            return Err(error.into());
        }
        if let Err(error) = self.window.select_project(project_id) {
            let _ = self.registry.detach(session_id);
            return Err(error.into());
        }
        Ok(session_id)
    }

    /// Commit a worker-created session as the new child of a pane. All
    /// selection/focus/source guards are checked before publishing the session.
    pub fn commit_split_session(
        &mut self,
        commit: SplitSessionCommit,
    ) -> Result<SessionId, CoordinatorError> {
        let SplitSessionCommit {
            expected_selected_project,
            expected_selected_tab,
            expected_project,
            expected_tab,
            expected_focus,
            target,
            expected_source,
            direction,
            new_pane,
            session,
        } = commit;
        let valid = self.window.selected_project == expected_selected_project
            && self
                .window
                .project(expected_project)
                .and_then(|project| project.selected_tab)
                == expected_selected_tab
            && self
                .window
                .project(expected_project)
                .and_then(|project| project.tab(expected_tab))
                .is_some_and(|tab| {
                    tab.focused_pane == expected_focus
                        && matches!(
                            tab.tree.find(target).map(|pane| &pane.content),
                            Some(PaneContent::Terminal(id)) if *id == expected_source
                        )
                        && tab.tree.find(new_pane).is_none()
                });
        if !valid {
            return Err(CoordinatorError::StaleLaunch);
        }
        let session_id = session.id();
        self.registry.insert(session)?;
        let result = match self
            .window
            .project_mut(expected_project)
            .and_then(|project| project.tab_mut(expected_tab))
        {
            Some(tab) => tab.tree.split(
                target,
                direction,
                Pane {
                    id: new_pane,
                    content: PaneContent::Terminal(session_id),
                },
            ),
            None => Err(CoreError::TabNotFound(expected_tab)),
        };
        if let Err(error) = result {
            let _ = self.registry.detach(session_id);
            return Err(error.into());
        }
        if let Some(tab) = self
            .window
            .project_mut(expected_project)
            .and_then(|project| project.tab_mut(expected_tab))
        {
            tab.focused_pane = new_pane;
        }
        self.window
            .project_mut(expected_project)
            .expect("validated project")
            .select_tab(expected_tab)?;
        self.window.select_project(expected_project)?;
        Ok(session_id)
    }

    /// Commit a session into a restored empty pane. Unlike new-tab creation,
    /// restore is allowed to finish while another project/tab is selected.
    pub fn commit_restored_session(
        &mut self,
        project_id: ProjectId,
        tab_id: TabId,
        pane_id: PaneId,
        session: TerminalSession,
    ) -> Result<SessionId, CoordinatorError> {
        let pane = self
            .window
            .project(project_id)
            .and_then(|project| project.tab(tab_id))
            .and_then(|tab| tab.tree.find(pane_id))
            .ok_or(CoreError::PaneNotFound(pane_id))?;
        if !matches!(pane.content, PaneContent::Empty) {
            return Err(CoordinatorError::StaleLaunch);
        }
        let session_id = session.id();
        self.registry.insert(session)?;
        let pane = self
            .window
            .project_mut(project_id)
            .and_then(|project| project.tab_mut(tab_id))
            .and_then(|tab| tab.tree.find_mut(pane_id));
        let Some(pane) = pane else {
            let _ = self.registry.detach(session_id);
            return Err(CoreError::PaneNotFound(pane_id).into());
        };
        pane.content = PaneContent::Terminal(session_id);
        Ok(session_id)
    }

    /// Launch a fresh session into an existing restored pane. The logical
    /// pane identity/layout remains intact if PTY creation fails.
    pub fn launch_restored_pane(
        &mut self,
        project_id: ProjectId,
        tab_id: TabId,
        pane_id: PaneId,
        working_directory: PathBuf,
        cols: u16,
        rows: u16,
    ) -> Result<SessionId, CoordinatorError> {
        let project = self
            .window
            .project(project_id)
            .ok_or(CoreError::ProjectNotFound(project_id))?;
        let tab = project.tab(tab_id).ok_or(CoreError::TabNotFound(tab_id))?;
        let pane = tab
            .tree
            .find(pane_id)
            .ok_or(CoreError::PaneNotFound(pane_id))?;
        if !matches!(pane.content, PaneContent::Empty) {
            return Err(CoordinatorError::WorkspaceNotEmpty);
        }
        let directory = if working_directory.is_dir() {
            working_directory
        } else if self.home_directory_available {
            self.home_directory.clone()
        } else {
            return Err(CoordinatorError::HomeUnavailable);
        };
        let session_id = self.registry.create(TerminalConfig {
            working_directory: directory,
            shell: self.preferred_shell_program.clone(),
            cols,
            rows,
            scrollback_lines: self.scrollback_lines,
        })?;
        let pane = self
            .window
            .project_mut(project_id)
            .and_then(|project| project.tab_mut(tab_id))
            .and_then(|tab| tab.tree.find_mut(pane_id))
            .expect("pane was validated before session creation");
        pane.content = PaneContent::Terminal(session_id);
        Ok(session_id)
    }

    pub fn working_directory(&self) -> &PathBuf {
        self.active_project()
            .and_then(|p| p.pinned_directory.as_ref())
            .unwrap_or(&self.launch_directory)
    }

    /// Resolve a pane to its live session across the whole window. Pane IDs
    /// are unique workspace-wide, so every project and tab is searched —
    /// not just the active tree. History replay, recording-flag sync, and
    /// similar background paths must reach panes in inactive projects and
    /// hidden tabs; restricting this to the visible tree silently dropped
    /// those panes (notably: only the last active pane's history restored).
    pub fn session_id_for_pane(&self, pane: PaneId) -> Option<SessionId> {
        for project in &self.window.projects {
            for tab in &project.tabs {
                if let Some(node) = tab.tree.find(pane) {
                    return match node.content {
                        PaneContent::Terminal(id) => Some(id),
                        PaneContent::Empty => None,
                    };
                }
            }
        }
        None
    }

    pub fn focused_session_id(&self) -> Option<SessionId> {
        self.focused()
            .and_then(|pane| self.session_id_for_pane(pane))
    }

    fn config(&self, cols: u16, rows: u16, shell: Option<String>) -> TerminalConfig {
        TerminalConfig {
            working_directory: self.working_directory().clone(),
            shell: shell.or_else(|| self.preferred_shell_program.clone()),
            cols,
            rows,
            scrollback_lines: self.scrollback_lines,
        }
    }

    /// Create the first terminal in an empty workspace.
    pub fn create_initial(&mut self, cols: u16, rows: u16) -> Result<SessionId, CoordinatorError> {
        if self.active_tab().is_some() {
            return Err(CoordinatorError::WorkspaceNotEmpty);
        }
        if self.window.projects.is_empty() {
            self.window
                .add_project(Project::new(None, Some(self.launch_directory.clone())))?;
        }
        self.create_tab_for_selected(cols, rows, None)
            .map(|(_, session)| session)
    }

    fn create_tab_for_selected(
        &mut self,
        cols: u16,
        rows: u16,
        directory: Option<PathBuf>,
    ) -> Result<(TabId, SessionId), CoordinatorError> {
        let mut config = self.config(cols, rows, None);
        if let Some(directory) = directory {
            config.working_directory = directory;
        }
        let id = self.registry.create(config)?;
        let pane = Pane {
            id: PaneId::new(),
            content: PaneContent::Terminal(id),
        };
        let pane_id = pane.id;
        let tab = Tab::new(PaneTree::new(pane), pane_id)?;
        let tab_id = tab.id;
        if let Some(project) = self.window.selected_project_mut()
            && let Err(error) = project.add_tab(tab)
        {
            if let Some(handle) = self.registry.detach(id) {
                let _ = handle.lock().map(|mut s| s.shutdown());
            }
            return Err(error.into());
        }
        Ok((tab_id, id))
    }

    pub fn create_project(
        &mut self,
        directory: Option<PathBuf>,
        cols: u16,
        rows: u16,
    ) -> Result<(ProjectId, TabId, SessionId), CoordinatorError> {
        let directory = directory
            .filter(|path| path.is_dir())
            .unwrap_or_else(|| self.home_directory.clone());
        let project = Project::new(None, Some(directory));
        let project_id = project.id;
        self.window.add_project(project)?;
        match self.create_tab_for_selected(cols, rows, None) {
            Ok((tab, session)) => Ok((project_id, tab, session)),
            Err(error) => {
                let _ = self.window.remove_project(project_id);
                Err(error)
            }
        }
    }

    pub fn create_tab(
        &mut self,
        project: ProjectId,
        cols: u16,
        rows: u16,
    ) -> Result<(TabId, SessionId), CoordinatorError> {
        let previously_selected = self.window.selected_project;
        self.window.select_project(project)?;
        match self.create_tab_for_selected(cols, rows, None) {
            Ok(created) => Ok(created),
            Err(error) => {
                if let Some(previous) = previously_selected {
                    let _ = self.window.select_project(previous);
                }
                Err(error)
            }
        }
    }

    pub fn create_tab_in_directory(
        &mut self,
        project: ProjectId,
        directory: PathBuf,
        cols: u16,
        rows: u16,
    ) -> Result<(TabId, SessionId), CoordinatorError> {
        let previously_selected = self.window.selected_project;
        self.window.select_project(project)?;
        match self.create_tab_for_selected(cols, rows, Some(directory)) {
            Ok(created) => Ok(created),
            Err(error) => {
                if let Some(previous) = previously_selected {
                    let _ = self.window.select_project(previous);
                }
                Err(error)
            }
        }
    }

    pub fn select_project(&mut self, project: ProjectId) -> Result<(), CoordinatorError> {
        self.window.select_project(project).map_err(Into::into)
    }
    pub fn select_tab(&mut self, project: ProjectId, tab: TabId) -> Result<(), CoordinatorError> {
        self.window
            .project_mut(project)
            .ok_or(CoreError::ProjectNotFound(project))?
            .select_tab(tab)?;
        self.window.select_project(project)?;
        Ok(())
    }

    pub fn close_tab(
        &mut self,
        project_id: ProjectId,
        tab_id: TabId,
    ) -> Result<ClosedSessions, CoordinatorError> {
        let tab = self
            .window
            .project_mut(project_id)
            .ok_or(CoreError::ProjectNotFound(project_id))?
            .remove_tab(tab_id)?;
        let mut closed = Vec::new();
        for pane in tab.tree.panes() {
            if let PaneContent::Terminal(session_id) = pane.content {
                let handle = self.registry.detach(session_id);
                closed.push(ClosedPane {
                    pane_id: pane.id,
                    session_id: Some(session_id),
                    handle,
                });
            }
        }
        Ok(ClosedSessions(closed))
    }

    pub fn close_project(
        &mut self,
        project_id: ProjectId,
    ) -> Result<ClosedSessions, CoordinatorError> {
        let project = self.window.remove_project(project_id)?;
        let mut closed = Vec::new();
        for tab in project.tabs {
            for pane in tab.tree.panes() {
                if let PaneContent::Terminal(session_id) = pane.content {
                    let handle = self.registry.detach(session_id);
                    closed.push(ClosedPane {
                        pane_id: pane.id,
                        session_id: Some(session_id),
                        handle,
                    });
                }
            }
        }
        Ok(ClosedSessions(closed))
    }

    /// Split the focused pane, creating the new session first.
    ///
    /// On insertion failure the provisional session is detached for the
    /// caller to shut down, and the tree/focus are untouched.
    pub fn split_focused(
        &mut self,
        direction: SplitDirection,
        cols: u16,
        rows: u16,
    ) -> Result<(PaneId, SessionId), CoordinatorError> {
        self.split_focused_with_shell(direction, cols, rows, None)
    }

    /// Split with an explicit shell (tests inject failures via bad shell or
    /// invalid grid size).
    pub fn split_focused_with_shell(
        &mut self,
        direction: SplitDirection,
        cols: u16,
        rows: u16,
        shell: Option<String>,
    ) -> Result<(PaneId, SessionId), CoordinatorError> {
        let focused = self.focused().ok_or(CoordinatorError::NoFocusedPane)?;
        if self.tree().find(focused).is_none() {
            return Err(CoordinatorError::Core(CoreError::PaneNotFound(focused)));
        }
        let directory = self
            .session_id_for_pane(focused)
            .and_then(|id| self.registry.get(id))
            .and_then(|session| {
                session
                    .lock()
                    .ok()
                    .map(|session| session.cwd().path.clone())
            })
            .filter(|path| path.is_dir())
            .or_else(|| {
                self.active_project()
                    .and_then(|project| project.pinned_directory.clone())
                    .filter(|path| path.is_dir())
            })
            .unwrap_or_else(|| self.home_directory.clone());
        let mut config = self.config(cols, rows, shell);
        config.working_directory = directory;
        let session_id = self
            .registry
            .create(config)
            .map_err(CoordinatorError::Registry)?;
        let pane = Pane {
            id: PaneId::new(),
            content: PaneContent::Terminal(session_id),
        };
        let pane_id = pane.id;
        let result = self
            .window
            .selected_project_mut()
            .and_then(Project::selected_tab_mut)
            .ok_or(CoreError::NoSelectedTab)?
            .tree
            .split(focused, direction, pane);
        if let Err(e) = result {
            // Roll back: leave tree/focus intact, detach the provisional
            // session so no orphan child or dangling pane ID remains.
            let _ = self.registry.detach(session_id);
            return Err(CoordinatorError::Core(e));
        }
        if let Some(tab) = self
            .window
            .selected_project_mut()
            .and_then(Project::selected_tab_mut)
        {
            tab.focused_pane = pane_id;
        }
        Ok((pane_id, session_id))
    }

    /// Close the focused pane, detach its session, and move focus.
    ///
    /// The successor is geometry-aware: a live neighbor if one exists, else
    /// the first remaining pane, else `None` for an empty workspace.
    pub fn close_focused(&mut self) -> Result<ClosedPane, CoordinatorError> {
        let focused = self.focused().ok_or(CoordinatorError::NoFocusedPane)?;
        self.close_pane(focused)
    }

    /// Close the pane bound to `session_id`. Used for lifecycle events such
    /// as a shell exiting while its pane is not focused.
    pub fn close_session(&mut self, session_id: SessionId) -> Result<ClosedPane, CoordinatorError> {
        let target = self
            .window
            .projects
            .iter()
            .flat_map(|p| p.tabs.iter().map(move |t| (p.id, t.id, t)))
            .find_map(|(project, tab, t)| {
                t.tree
                    .panes()
                    .into_iter()
                    .find(|pane| pane.content == PaneContent::Terminal(session_id))
                    .map(|pane| (project, tab, pane.id))
            })
            .ok_or(CoordinatorError::SessionPaneNotFound(session_id))?;
        self.close_pane_in_tab(target.0, target.1, target.2)
    }

    fn close_pane(&mut self, pane_id: PaneId) -> Result<ClosedPane, CoordinatorError> {
        let project_id = self
            .window
            .selected_project
            .ok_or(CoreError::NoSelectedProject)?;
        let tab_id = self
            .window
            .project(project_id)
            .and_then(|p| p.selected_tab)
            .ok_or(CoreError::NoSelectedTab)?;
        self.close_pane_in_tab(project_id, tab_id, pane_id)
    }

    fn close_pane_in_tab(
        &mut self,
        project_id: ProjectId,
        tab_id: TabId,
        pane_id: PaneId,
    ) -> Result<ClosedPane, CoordinatorError> {
        let tab = self
            .window
            .project(project_id)
            .and_then(|p| p.tab(tab_id))
            .ok_or(CoreError::TabNotFound(tab_id))?;
        if tab.tree.find(pane_id).is_none() {
            return Err(CoreError::PaneNotFound(pane_id).into());
        }
        let focused = tab.focused_pane == pane_id;
        let session_id = match tab.tree.find(pane_id).map(|p| &p.content) {
            Some(PaneContent::Terminal(id)) => Some(*id),
            _ => None,
        };
        let neighbor = if focused {
            [
                SplitDirection::Left,
                SplitDirection::Right,
                SplitDirection::Up,
                SplitDirection::Down,
            ]
            .into_iter()
            .filter_map(|dir| tab.tree.neighbor(pane_id, dir))
            .next()
        } else {
            None
        };
        let tab = self
            .window
            .project_mut(project_id)
            .and_then(|p| p.tab_mut(tab_id))
            .ok_or(CoreError::TabNotFound(tab_id))?;
        tab.tree.remove(pane_id).map_err(CoordinatorError::Core)?;
        if tab.tree.is_empty() {
            self.window
                .project_mut(project_id)
                .ok_or(CoreError::ProjectNotFound(project_id))?
                .remove_tab(tab_id)?;
        } else if focused {
            tab.focused_pane = neighbor
                .filter(|id| tab.tree.find(*id).is_some())
                .or_else(|| tab.tree.panes().first().map(|pane| pane.id))
                .ok_or(CoreError::InvalidFocusedPane)?;
        }
        let handle = session_id.and_then(|id| self.registry.detach(id));
        Ok(ClosedPane {
            pane_id,
            session_id,
            handle,
        })
    }

    pub fn focus_neighbor(&mut self, direction: SplitDirection) -> Option<PaneId> {
        let next = self
            .focused()
            .and_then(|focused| self.tree().neighbor(focused, direction))?;
        self.window
            .selected_project_mut()?
            .selected_tab_mut()?
            .focused_pane = next;
        Some(next)
    }

    pub fn focus_pane(&mut self, pane: PaneId) -> Result<(), CoordinatorError> {
        if self.tree().find(pane).is_none() {
            return Err(CoordinatorError::Core(CoreError::PaneNotFound(pane)));
        }
        self.window
            .selected_project_mut()
            .and_then(Project::selected_tab_mut)
            .ok_or(CoreError::NoSelectedTab)?
            .focused_pane = pane;
        Ok(())
    }

    /// Focus a pane anywhere in the workspace, selecting its owner first.
    pub fn focus_pane_anywhere(&mut self, pane: PaneId) -> Result<(), CoordinatorError> {
        let owner = self
            .window
            .projects
            .iter()
            .find_map(|project| {
                project
                    .tabs
                    .iter()
                    .find_map(|tab| tab.tree.find(pane).map(|_| (project.id, tab.id)))
            })
            .ok_or(CoreError::PaneNotFound(pane))?;
        self.select_tab(owner.0, owner.1)?;
        self.focus_pane(pane)
    }

    /// Split the requested pane and focus the newly-created pane.
    pub fn split_pane(
        &mut self,
        target: PaneId,
        direction: SplitDirection,
        cols: u16,
        rows: u16,
    ) -> Result<(PaneId, SessionId), CoordinatorError> {
        let selected_project = self.window.selected_project;
        let selections: Vec<_> = self
            .window
            .projects
            .iter()
            .map(|p| (p.id, p.selected_tab))
            .collect();
        let focus: Vec<_> = self
            .window
            .projects
            .iter()
            .flat_map(|p| p.tabs.iter().map(|t| (p.id, t.id, t.focused_pane)))
            .collect();
        self.focus_pane_anywhere(target)?;
        match self.split_focused(direction, cols, rows) {
            Ok(created) => Ok(created),
            Err(error) => {
                self.window.selected_project = selected_project;
                for (project_id, selected_tab) in selections {
                    if let Some(project) = self.window.project_mut(project_id) {
                        project.selected_tab = selected_tab;
                    }
                }
                for (project_id, tab_id, focused_pane) in focus {
                    if let Some(tab) = self
                        .window
                        .project_mut(project_id)
                        .and_then(|p| p.tab_mut(tab_id))
                    {
                        tab.focused_pane = focused_pane;
                    }
                }
                Err(error)
            }
        }
    }

    /// Close a pane by stable ID, including a pane in a hidden project/tab.
    pub fn close_pane_by_id(&mut self, pane: PaneId) -> Result<ClosedPane, CoordinatorError> {
        let owner = self
            .window
            .projects
            .iter()
            .find_map(|project| {
                project
                    .tabs
                    .iter()
                    .find_map(|tab| tab.tree.find(pane).map(|_| (project.id, tab.id)))
            })
            .ok_or(CoreError::PaneNotFound(pane))?;
        self.close_pane_in_tab(owner.0, owner.1, pane)
    }

    pub fn resize_split(
        &mut self,
        split: omaterm_core::SplitId,
        fraction: f32,
    ) -> Result<(), CoordinatorError> {
        let tab_id = self
            .window
            .projects
            .iter()
            .find_map(|project| {
                project
                    .tabs
                    .iter()
                    .find(|tab| tab.tree.split_fraction(split).is_some())
                    .map(|tab| tab.id)
            })
            .ok_or(CoreError::SplitNotFound(split))?;
        let tab = self
            .window
            .projects
            .iter_mut()
            .find_map(|project| project.tab_mut(tab_id))
            .ok_or(CoreError::TabNotFound(tab_id))?;
        tab.tree.resize(split, fraction)?;
        Ok(())
    }

    pub fn equalize_tab(&mut self, tab_id: TabId) -> Result<(), CoordinatorError> {
        let tab = self
            .window
            .projects
            .iter_mut()
            .find_map(|project| project.tab_mut(tab_id))
            .ok_or(CoreError::TabNotFound(tab_id))?;
        tab.tree.equalize();
        Ok(())
    }

    pub fn resize_focused(&mut self, amount: f32) -> Result<f32, CoordinatorError> {
        let focused = self.focused().ok_or(CoordinatorError::NoFocusedPane)?;
        let ancestors = self
            .tree()
            .ancestors(focused)
            .ok_or(CoordinatorError::Core(CoreError::PaneNotFound(focused)))?;
        let split = ancestors
            .last()
            .copied()
            .ok_or(CoordinatorError::NoFocusedPane)?;
        let fraction = self
            .tree()
            .split_fraction(split)
            .ok_or(CoordinatorError::Core(CoreError::SplitNotFound(split)))?;
        self.window
            .selected_project_mut()
            .and_then(Project::selected_tab_mut)
            .ok_or(CoreError::NoSelectedTab)?
            .tree
            .resize(split, fraction + amount)
            .map_err(CoordinatorError::Core)?;
        Ok(self.tree().split_fraction(split).unwrap_or(fraction))
    }

    pub fn equalize(&mut self) {
        if let Some(tab) = self
            .window
            .selected_project_mut()
            .and_then(Project::selected_tab_mut)
        {
            tab.tree.equalize();
        }
    }

    pub fn is_empty(&self) -> bool {
        self.tree().is_empty()
    }

    pub fn pane_count(&self) -> usize {
        self.tree().panes().len()
    }

    pub fn session_count(&self) -> usize {
        self.registry.len()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn coordinator() -> WorkspaceCoordinator {
        WorkspaceCoordinator::new(std::env::temp_dir())
    }

    fn sh() -> Option<String> {
        Some("/bin/sh".to_string())
    }

    fn worker_session(id: SessionId) -> TerminalSession {
        TerminalSession::new_with_id(id, std::env::temp_dir(), Some("/bin/sh"), 80, 24)
            .expect("spawn worker session")
    }

    fn cleanup_project(ws: &mut WorkspaceCoordinator, project: ProjectId) {
        if let Ok(closed) = ws.close_project(project) {
            for pane in closed.0 {
                if let Some(handle) = pane.handle {
                    let _ = handle.lock().map(|mut session| session.shutdown());
                }
            }
        }
    }

    #[test]
    fn worker_session_commit_creates_project_and_tab_atomically() {
        let mut ws = coordinator();
        let project = ProjectId::new();
        let tab = TabId::new();
        let pane = PaneId::new();
        let session_id = SessionId::new();
        assert_eq!(
            ws.commit_project_session(ProjectSessionCommit {
                expected_selected_project: None,
                project_id: project,
                tab_id: tab,
                pane_id: pane,
                name: Some("worker-created".into()),
                directory: std::env::temp_dir(),
                session: worker_session(session_id),
            },)
                .unwrap(),
            session_id
        );
        assert_eq!(ws.selected_project_id(), Some(project));
        assert_eq!(ws.selected_tab_id(), Some(tab));
        assert_eq!(ws.session_id_for_pane(pane), Some(session_id));
        assert_eq!(ws.session_count(), 1);
        cleanup_project(&mut ws, project);
    }

    #[test]
    fn set_project_directory_updates_pinned_base_and_rejects_stale_ids() {
        let mut ws = coordinator();
        let project = ProjectId::new();
        let tab = TabId::new();
        let pane = PaneId::new();
        let session_id = SessionId::new();
        ws.commit_project_session(ProjectSessionCommit {
            expected_selected_project: None,
            project_id: project,
            tab_id: tab,
            pane_id: pane,
            name: None,
            directory: std::env::temp_dir(),
            session: worker_session(session_id),
        })
        .unwrap();
        let target = std::env::temp_dir().join("omaterm-set-dir-test");
        std::fs::create_dir_all(&target).unwrap();
        ws.set_project_directory(project, target.clone()).unwrap();
        assert_eq!(
            ws.window.project(project).unwrap().pinned_directory,
            Some(target.clone())
        );
        // The live session binding is untouched by the base change.
        assert_eq!(ws.session_id_for_pane(pane), Some(session_id));
        assert!(matches!(
            ws.set_project_directory(ProjectId::new(), target.clone()),
            Err(CoordinatorError::Core(CoreError::ProjectNotFound(_)))
        ));
        std::fs::remove_dir_all(&target).ok();
        cleanup_project(&mut ws, project);
    }

    #[test]
    fn worker_tab_commit_adds_new_session_to_requested_project() {
        let mut ws = coordinator();
        ws.create_initial(80, 24).unwrap();
        let project = ws.selected_project_id().unwrap();
        let tab = TabId::new();
        let pane = PaneId::new();
        let session = SessionId::new();
        assert_eq!(
            ws.commit_tab_session(
                Some(project),
                project,
                tab,
                pane,
                Some("worker-tab".into()),
                worker_session(session),
            )
            .unwrap(),
            session
        );
        assert_eq!(ws.selected_tab_id(), Some(tab));
        assert_eq!(ws.session_id_for_pane(pane), Some(session));
        assert_eq!(ws.session_count(), 2);
        cleanup_project(&mut ws, project);
    }

    #[test]
    fn restored_worker_commit_can_finish_after_selection_changes() {
        let directory = std::env::temp_dir();
        let restored_pane = Pane::empty();
        let pane_id = restored_pane.id;
        let restored_tab = Tab {
            id: TabId::new(),
            custom_name: None,
            tree: PaneTree::new(restored_pane),
            focused_pane: pane_id,
        };
        let tab_id = restored_tab.id;
        let mut restored_project = Project {
            id: ProjectId::new(),
            custom_name: Some("restored".into()),
            pinned_directory: Some(directory.clone()),
            tabs: Vec::new(),
            selected_tab: None,
        };
        restored_project.add_tab(restored_tab).unwrap();
        let project_id = restored_project.id;
        let mut other_project = Project::new(Some("other".into()), Some(directory.clone()));
        let other_pane = Pane::empty();
        let other_pane_id = other_pane.id;
        other_project
            .add_tab(Tab::new(PaneTree::new(other_pane), other_pane_id).unwrap())
            .unwrap();
        let other_project_id = other_project.id;
        let mut window = WorkspaceWindow::new();
        window.add_project(restored_project).unwrap();
        window.add_project(other_project).unwrap();
        window.select_project(other_project_id).unwrap();
        let mut ws = coordinator();
        ws.restore_window(window).unwrap();

        let session_id = SessionId::new();
        assert_eq!(
            ws.commit_restored_session(project_id, tab_id, pane_id, worker_session(session_id),)
                .unwrap(),
            session_id
        );
        assert_eq!(ws.selected_project_id(), Some(other_project_id));
        assert_eq!(
            ws.window()
                .project(project_id)
                .unwrap()
                .tab(tab_id)
                .unwrap()
                .tree
                .find(pane_id)
                .unwrap()
                .content,
            PaneContent::Terminal(session_id)
        );
        cleanup_project(&mut ws, project_id);
        cleanup_project(&mut ws, other_project_id);
    }

    #[test]
    fn stale_project_commit_does_not_publish_provisional_session() {
        let mut ws = coordinator();
        let existing_session = ws.create_initial(80, 24).unwrap();
        let existing_project = ws.selected_project_id().unwrap();
        let pending_session = SessionId::new();
        let result = ws.commit_project_session(ProjectSessionCommit {
            expected_selected_project: None, // guard changed during spawn
            project_id: ProjectId::new(),
            tab_id: TabId::new(),
            pane_id: PaneId::new(),
            name: None,
            directory: std::env::temp_dir(),
            session: worker_session(pending_session),
        });
        assert!(matches!(result, Err(CoordinatorError::StaleLaunch)));
        assert_eq!(ws.session_count(), 1);
        assert!(!ws.registry().contains(pending_session));
        assert!(ws.registry().contains(existing_session));
        cleanup_project(&mut ws, existing_project);
    }

    #[test]
    fn worker_split_commit_revalidates_focus_before_registry_insertion() {
        let mut ws = coordinator();
        let source = ws.create_initial(80, 24).unwrap();
        let project = ws.selected_project_id().unwrap();
        let tab = ws.selected_tab_id().unwrap();
        let focus = ws.focused().unwrap();
        let provisional = SessionId::new();
        let result = ws.commit_split_session(SplitSessionCommit {
            expected_selected_project: Some(project),
            expected_selected_tab: Some(tab),
            expected_project: project,
            expected_tab: tab,
            expected_focus: PaneId::new(),
            target: focus,
            expected_source: source,
            direction: SplitDirection::Right,
            new_pane: PaneId::new(),
            session: worker_session(provisional),
        });
        assert!(matches!(result, Err(CoordinatorError::StaleLaunch)));
        assert_eq!(ws.pane_count(), 1);
        assert_eq!(ws.session_count(), 1);
        assert!(!ws.registry().contains(provisional));
        cleanup_project(&mut ws, project);
    }

    #[test]
    fn initial_split_close_lifecycle() {
        let mut ws = coordinator();
        let first = ws.create_initial(80, 24).expect("initial");
        assert_eq!(ws.pane_count(), 1);
        assert_eq!(ws.session_count(), 1);
        assert_eq!(ws.focused_session_id(), Some(first));

        let (pane_id, second) = ws
            .split_focused_with_shell(SplitDirection::Right, 80, 24, sh())
            .expect("split");
        assert_ne!(first, second);
        assert_eq!(ws.pane_count(), 2);
        assert_eq!(ws.session_count(), 2);
        assert_eq!(ws.focused(), Some(pane_id));

        // Focus back to the original pane without touching sessions.
        ws.focus_neighbor(SplitDirection::Left);
        assert_eq!(ws.focused_session_id(), Some(first));
        assert_eq!(ws.session_count(), 2);

        // Close the original: only its session detaches.
        ws.focus_pane(ws.tree().panes()[0].id).unwrap();
        let original_id = ws.focused_session_id().unwrap();
        let closed = ws.close_focused().expect("close");
        assert_eq!(closed.session_id, Some(original_id));
        assert_eq!(ws.pane_count(), 1);
        assert_eq!(ws.session_count(), 1);
        assert!(closed.handle.is_some());
        // Reap the detached child; no orphan may remain.
        let reaped = closed
            .handle
            .unwrap()
            .lock()
            .map(|mut s| s.shutdown())
            .unwrap_or(false);
        assert!(reaped);
        // Survivor keeps its identity.
        assert_ne!(ws.focused_session_id(), Some(original_id));
    }

    #[test]
    fn four_panes_have_independent_sessions() {
        let mut ws = coordinator();
        ws.create_initial(80, 24).expect("initial");
        ws.split_focused_with_shell(SplitDirection::Right, 80, 24, sh())
            .expect("split 2");
        let panes: Vec<PaneId> = ws.tree().panes().iter().map(|p| p.id).collect();
        ws.focus_pane(panes[0]).unwrap();
        ws.split_focused_with_shell(SplitDirection::Down, 80, 24, sh())
            .expect("split 3");
        let panes: Vec<PaneId> = ws.tree().panes().iter().map(|p| p.id).collect();
        ws.focus_pane(panes[1]).unwrap();
        ws.split_focused_with_shell(SplitDirection::Down, 80, 24, sh())
            .expect("split 4");
        assert_eq!(ws.pane_count(), 4);
        assert_eq!(ws.session_count(), 4);
        let mut ids: Vec<SessionId> = ws
            .tree()
            .panes()
            .iter()
            .filter_map(|p| match p.content {
                PaneContent::Terminal(id) => Some(id),
                PaneContent::Empty => None,
            })
            .collect();
        ids.sort_by_key(|id| id.0);
        ids.dedup_by_key(|id| id.0);
        assert_eq!(ids.len(), 4, "every pane must own a distinct session");
    }

    #[test]
    fn spawn_failure_leaves_tree_and_focus_intact() {
        let mut ws = coordinator();
        let first = ws.create_initial(80, 24).expect("initial");
        let before_tree = ws.tree().clone();
        let before_focus = ws.focused();
        let result = ws.split_focused_with_shell(SplitDirection::Right, 1, 24, sh());
        assert!(matches!(result, Err(CoordinatorError::Registry(_))));
        assert_eq!(ws.tree(), &before_tree);
        assert_eq!(ws.focused(), before_focus);
        assert_eq!(ws.focused_session_id(), Some(first));
        assert_eq!(ws.session_count(), 1);
    }

    #[test]
    fn close_last_pane_empties_workspace() {
        let mut ws = coordinator();
        ws.create_initial(80, 24).expect("initial");
        let closed = ws.close_focused().expect("close last");
        assert!(ws.is_empty());
        assert_eq!(ws.focused(), None);
        assert_eq!(ws.session_count(), 0);
        assert!(closed.handle.is_some());
    }

    #[test]
    fn closing_a_nonfocused_session_preserves_focus_and_siblings() {
        let mut ws = coordinator();
        let first = ws.create_initial(80, 24).expect("initial");
        let (second_pane, second) = ws
            .split_focused_with_shell(SplitDirection::Right, 80, 24, sh())
            .expect("split");
        let first_pane = ws
            .tree()
            .panes()
            .into_iter()
            .find(|pane| pane.content == PaneContent::Terminal(first))
            .unwrap()
            .id;

        // Second remains focused when the first session exits on its own.
        let closed = ws.close_session(first).expect("close exited session");
        assert_eq!(closed.pane_id, first_pane);
        assert_eq!(closed.session_id, Some(first));
        assert_eq!(ws.focused(), Some(second_pane));
        assert_eq!(ws.focused_session_id(), Some(second));
        assert!(!ws.registry().contains(first));
        assert!(ws.registry().contains(second));
        assert_eq!(ws.pane_count(), 1);
        assert_eq!(ws.session_count(), 1);
        assert!(closed.handle.is_some());

        let reaped = closed
            .handle
            .unwrap()
            .lock()
            .map(|mut session| session.shutdown())
            .unwrap_or(false);
        assert!(reaped);
    }

    #[test]
    fn resize_and_equalize_preserve_sessions() {
        let mut ws = coordinator();
        let first = ws.create_initial(80, 24).expect("initial");
        let (_, second) = ws
            .split_focused_with_shell(SplitDirection::Right, 80, 24, sh())
            .expect("split");
        let before: Vec<SessionId> = ws
            .tree()
            .panes()
            .iter()
            .filter_map(|p| match p.content {
                PaneContent::Terminal(id) => Some(id),
                PaneContent::Empty => None,
            })
            .collect();
        ws.resize_focused(0.05).expect("resize");
        ws.equalize();
        let after: Vec<SessionId> = ws
            .tree()
            .panes()
            .iter()
            .filter_map(|p| match p.content {
                PaneContent::Terminal(id) => Some(id),
                PaneContent::Empty => None,
            })
            .collect();
        assert_eq!(before, after);
        assert!(after.contains(&first) && after.contains(&second));
    }

    #[test]
    fn projects_and_tabs_keep_independent_sessions_and_close_isolated() {
        let mut ws = coordinator();
        let first = ws.create_initial(80, 24).expect("default");
        let project_a = ws.selected_project_id().unwrap();
        let (tab_b, second) = ws.create_tab(project_a, 80, 24).expect("second tab");
        let second_pid = ws
            .registry()
            .get(second)
            .unwrap()
            .lock()
            .unwrap()
            .child_pid();
        ws.select_tab(project_a, tab_b).unwrap();
        let (project_b, tab_c, third) = ws
            .create_project(Some(std::env::temp_dir()), 80, 24)
            .unwrap();
        let third_pid = ws
            .registry()
            .get(third)
            .unwrap()
            .lock()
            .unwrap()
            .child_pid();
        ws.select_project(project_a).unwrap();
        ws.select_tab(project_a, tab_b).unwrap();
        assert_eq!(ws.focused_session_id(), Some(second));
        assert_eq!(
            ws.registry()
                .get(second)
                .unwrap()
                .lock()
                .unwrap()
                .child_pid(),
            second_pid
        );
        assert_eq!(
            ws.registry()
                .get(third)
                .unwrap()
                .lock()
                .unwrap()
                .child_pid(),
            third_pid
        );
        let closed = ws.close_tab(project_b, tab_c).unwrap();
        assert_eq!(closed.0.len(), 1);
        assert!(!ws.registry().contains(third));
        assert!(ws.registry().contains(first));
        assert!(ws.registry().contains(second));
        for pane in closed.0 {
            if let Some(handle) = pane.handle {
                assert!(handle.lock().unwrap().shutdown());
            }
        }
    }

    #[test]
    fn repeated_tab_close_is_rejected_without_detaching_the_project_sibling() {
        let mut ws = coordinator();
        let first = ws.create_initial(80, 24).expect("initial");
        let project = ws.selected_project_id().unwrap();
        let (tab, second) = ws.create_tab(project, 80, 24).expect("second tab");

        let closed = ws.close_tab(project, tab).expect("first close");
        assert_eq!(closed.0.len(), 1);
        assert!(matches!(
            ws.close_tab(project, tab),
            Err(CoordinatorError::Core(CoreError::TabNotFound(_)))
        ));
        assert!(ws.registry().contains(first));
        assert!(!ws.registry().contains(second));
        assert_eq!(ws.window().project(project).unwrap().tabs.len(), 1);

        for pane in closed.0 {
            if let Some(handle) = pane.handle {
                assert!(handle.lock().unwrap().shutdown());
            }
        }
        ws.close_project(project)
            .unwrap()
            .0
            .into_iter()
            .filter_map(|pane| pane.handle)
            .for_each(|handle| {
                assert!(handle.lock().unwrap().shutdown());
            });
    }

    #[test]
    fn project_close_returns_all_sessions_when_one_cleanup_handle_is_poisoned() {
        let mut ws = coordinator();
        let first = ws.create_initial(80, 24).expect("initial");
        let project = ws.selected_project_id().unwrap();
        let (_, second) = ws.create_tab(project, 80, 24).expect("second tab");
        let poisoned = ws.registry().get(second).unwrap();
        let poisoned_pid = poisoned.lock().unwrap().child_pid();
        let healthy_pid = ws
            .registry()
            .get(first)
            .unwrap()
            .lock()
            .unwrap()
            .child_pid();

        let poisoner = poisoned.clone();
        let _ = std::thread::spawn(move || {
            let _guard = poisoner.lock().unwrap();
            panic!("inject one failed shutdown lock");
        })
        .join();
        drop(poisoned);

        let closed = ws.close_project(project).expect("close project");
        assert_eq!(closed.0.len(), 2);
        assert!(!ws.registry().contains(first));
        assert!(!ws.registry().contains(second));
        assert!(closed.0.iter().any(|pane| pane.session_id == Some(first)));
        assert!(closed.0.iter().any(|pane| pane.session_id == Some(second)));

        for pane in closed.0 {
            let Some(handle) = pane.handle else { continue };
            if pane.session_id == Some(first) {
                assert!(handle.lock().unwrap().shutdown());
            } else {
                assert!(handle.lock().is_err(), "poisoned cleanup path is injected");
                drop(handle);
            }
        }

        for pid in [healthy_pid, poisoned_pid] {
            let deadline = std::time::Instant::now() + std::time::Duration::from_secs(2);
            while std::path::Path::new(&format!("/proc/{pid}")).exists()
                && std::time::Instant::now() < deadline
            {
                std::thread::sleep(std::time::Duration::from_millis(10));
            }
            assert!(
                !std::path::Path::new(&format!("/proc/{pid}")).exists(),
                "child {pid} must be reaped even if its mutex is poisoned"
            );
        }
    }

    #[test]
    fn restored_layout_rebinds_existing_pane_to_a_fresh_session() {
        let mut ws = coordinator();
        let pane = Pane::empty();
        let pane_id = pane.id;
        let tab = Tab::new(PaneTree::new(pane), pane_id).unwrap();
        let tab_id = tab.id;
        let mut project = Project::new(Some("restored".into()), Some(std::env::temp_dir()));
        project.add_tab(tab).unwrap();
        let project_id = project.id;
        let mut window = WorkspaceWindow::new();
        window.add_project(project).unwrap();

        ws.restore_window(window).unwrap();
        let session = ws
            .launch_restored_pane(project_id, tab_id, pane_id, std::env::temp_dir(), 80, 24)
            .unwrap();
        assert_eq!(ws.session_id_for_pane(pane_id), Some(session));
        assert_eq!(ws.focused(), Some(pane_id));
        assert_eq!(ws.session_count(), 1);
        let handle = ws.registry().get(session).unwrap();
        assert_eq!(handle.lock().unwrap().cwd().path, std::env::temp_dir());
        ws.registry_mut()
            .detach(session)
            .unwrap()
            .lock()
            .unwrap()
            .shutdown();
    }

    #[test]
    fn restored_missing_directory_falls_back_to_home() {
        let pane = Pane::empty();
        let pane_id = pane.id;
        let tab = Tab::new(PaneTree::new(pane), pane_id).unwrap();
        let tab_id = tab.id;
        let mut project = Project::new(None, None);
        project.add_tab(tab).unwrap();
        let project_id = project.id;
        let mut window = WorkspaceWindow::new();
        window.add_project(project).unwrap();
        let mut ws = coordinator();
        ws.restore_window(window).unwrap();
        let missing = std::env::temp_dir().join(format!("missing-{}", std::process::id()));
        let id = ws
            .launch_restored_pane(project_id, tab_id, pane_id, missing, 80, 24)
            .unwrap();
        let expected_home = std::env::var_os("HOME")
            .map(PathBuf::from)
            .filter(|path| path.is_dir())
            .unwrap_or_else(std::env::temp_dir);
        assert_eq!(
            ws.registry().get(id).unwrap().lock().unwrap().cwd().path,
            expected_home
        );
        ws.registry_mut()
            .detach(id)
            .unwrap()
            .lock()
            .unwrap()
            .shutdown();
    }

    #[test]
    fn session_id_for_pane_searches_inactive_projects_and_hidden_tabs() {
        let mut ws = coordinator();
        // Project A with two tabs; project B with one tab. Panes are bound
        // by spawning real (idle) shells; cleanup reaps them at the end.
        let pane_a1 = PaneId::new();
        let session_a1 = SessionId::new();
        let project_a = ProjectId::new();
        let tab_a1 = TabId::new();
        ws.commit_project_session(ProjectSessionCommit {
            expected_selected_project: None,
            project_id: project_a,
            tab_id: tab_a1,
            pane_id: pane_a1,
            name: None,
            directory: std::env::temp_dir(),
            session: worker_session(session_a1),
        })
        .unwrap();
        let pane_b1 = PaneId::new();
        let session_b1 = SessionId::new();
        let project_b = ProjectId::new();
        let tab_b1 = TabId::new();
        ws.commit_project_session(ProjectSessionCommit {
            expected_selected_project: Some(project_a),
            project_id: project_b,
            tab_id: tab_b1,
            pane_id: pane_b1,
            name: None,
            directory: std::env::temp_dir(),
            session: worker_session(session_b1),
        })
        .unwrap();
        let pane_a2 = PaneId::new();
        let session_a2 = SessionId::new();
        let tab_a2 = TabId::new();
        ws.commit_tab_session(
            Some(project_b),
            project_a,
            tab_a2,
            pane_a2,
            None,
            worker_session(session_a2),
        )
        .unwrap();
        // Select project A / tab A1: project B and tab A2 are both hidden.
        ws.select_tab(project_a, tab_a1).unwrap();
        assert_eq!(ws.selected_project_id(), Some(project_a));
        assert_eq!(ws.session_id_for_pane(pane_a1), Some(session_a1));
        assert_eq!(
            ws.session_id_for_pane(pane_b1),
            Some(session_b1),
            "inactive project panes must resolve"
        );
        assert_eq!(
            ws.session_id_for_pane(pane_a2),
            Some(session_a2),
            "hidden tab panes must resolve"
        );
        assert_eq!(ws.session_id_for_pane(PaneId::new()), None);
        cleanup_project(&mut ws, project_a);
        cleanup_project(&mut ws, project_b);
    }
}
