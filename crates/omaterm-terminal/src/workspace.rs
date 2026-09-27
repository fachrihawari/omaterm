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
    launch_directory: PathBuf,
}

impl WorkspaceCoordinator {
    pub fn new(working_directory: PathBuf) -> Self {
        let home_directory = std::env::var_os("HOME")
            .map(PathBuf::from)
            .filter(|path| path.is_dir())
            .unwrap_or_else(std::env::temp_dir);
        let launch_directory = if working_directory.is_dir() {
            working_directory
        } else {
            home_directory.clone()
        };
        Self {
            window: WorkspaceWindow::new(),
            registry: TerminalRegistry::new(),
            home_directory,
            launch_directory,
        }
    }

    pub fn window(&self) -> &WorkspaceWindow {
        &self.window
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

    pub fn working_directory(&self) -> &PathBuf {
        self.active_project()
            .and_then(|p| p.pinned_directory.as_ref())
            .unwrap_or(&self.launch_directory)
    }

    pub fn session_id_for_pane(&self, pane: PaneId) -> Option<SessionId> {
        match self.tree().find(pane)?.content {
            PaneContent::Terminal(id) => Some(id),
            PaneContent::Empty => None,
        }
    }

    pub fn focused_session_id(&self) -> Option<SessionId> {
        self.focused()
            .and_then(|pane| self.session_id_for_pane(pane))
    }

    fn config(&self, cols: u16, rows: u16, shell: Option<String>) -> TerminalConfig {
        TerminalConfig {
            working_directory: self.working_directory().clone(),
            shell,
            cols,
            rows,
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
        self.create_tab_for_selected(cols, rows)
            .map(|(_, session)| session)
    }

    fn create_tab_for_selected(
        &mut self,
        cols: u16,
        rows: u16,
    ) -> Result<(TabId, SessionId), CoordinatorError> {
        let id = self.registry.create(self.config(cols, rows, None))?;
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
        match self.create_tab_for_selected(cols, rows) {
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
        match self.create_tab_for_selected(cols, rows) {
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
}
