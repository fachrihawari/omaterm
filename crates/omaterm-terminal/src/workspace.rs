use std::path::PathBuf;
use std::sync::{Arc, Mutex};

use omaterm_core::{CoreError, Pane, PaneContent, PaneId, PaneTree, SessionId, SplitDirection};

use crate::registry::{RegistryError, TerminalConfig, TerminalRegistry};
use crate::session::TerminalSession;

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

/// GPUI-free workspace coordination: one recursive [`PaneTree`] whose
/// terminal leaves reference registry-owned sessions by ID.
///
/// The desktop (`apps/omaterm`) owns snapshot/reader UI state and delegates
/// every tree + lifecycle mutation here, so split/close/focus/resize behavior
/// is tested headless and never duplicated in GPUI handlers.
pub struct WorkspaceCoordinator {
    tree: PaneTree,
    focused: Option<PaneId>,
    registry: TerminalRegistry,
    working_directory: PathBuf,
}

impl WorkspaceCoordinator {
    pub fn new(working_directory: PathBuf) -> Self {
        Self {
            tree: PaneTree::empty(),
            focused: None,
            registry: TerminalRegistry::new(),
            working_directory,
        }
    }

    pub fn tree(&self) -> &PaneTree {
        &self.tree
    }

    pub fn focused(&self) -> Option<PaneId> {
        self.focused
    }

    pub fn registry(&self) -> &TerminalRegistry {
        &self.registry
    }

    pub fn registry_mut(&mut self) -> &mut TerminalRegistry {
        &mut self.registry
    }

    pub fn working_directory(&self) -> &PathBuf {
        &self.working_directory
    }

    pub fn session_id_for_pane(&self, pane: PaneId) -> Option<SessionId> {
        match self.tree.find(pane)?.content {
            PaneContent::Terminal(id) => Some(id),
            PaneContent::Empty => None,
        }
    }

    pub fn focused_session_id(&self) -> Option<SessionId> {
        self.focused.and_then(|pane| self.session_id_for_pane(pane))
    }

    fn config(&self, cols: u16, rows: u16, shell: Option<String>) -> TerminalConfig {
        TerminalConfig {
            working_directory: self.working_directory.clone(),
            shell,
            cols,
            rows,
        }
    }

    /// Create the first terminal in an empty workspace.
    pub fn create_initial(&mut self, cols: u16, rows: u16) -> Result<SessionId, CoordinatorError> {
        if !self.tree.is_empty() {
            return Err(CoordinatorError::WorkspaceNotEmpty);
        }
        let id = self.registry.create(self.config(cols, rows, None))?;
        let pane = Pane {
            id: PaneId::new(),
            content: PaneContent::Terminal(id),
        };
        self.focused = Some(pane.id);
        self.tree = PaneTree::new(pane);
        Ok(id)
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
        let focused = self.focused.ok_or(CoordinatorError::NoFocusedPane)?;
        if self.tree.find(focused).is_none() {
            return Err(CoordinatorError::Core(CoreError::PaneNotFound(focused)));
        }
        let session_id = self
            .registry
            .create(self.config(cols, rows, shell))
            .map_err(CoordinatorError::Registry)?;
        let pane = Pane {
            id: PaneId::new(),
            content: PaneContent::Terminal(session_id),
        };
        let pane_id = pane.id;
        if let Err(e) = self.tree.split(focused, direction, pane) {
            // Roll back: leave tree/focus intact, detach the provisional
            // session so no orphan child or dangling pane ID remains.
            let _ = self.registry.detach(session_id);
            return Err(CoordinatorError::Core(e));
        }
        self.focused = Some(pane_id);
        Ok((pane_id, session_id))
    }

    /// Close the focused pane, detach its session, and move focus.
    ///
    /// The successor is geometry-aware: a live neighbor if one exists, else
    /// the first remaining pane, else `None` for an empty workspace.
    pub fn close_focused(&mut self) -> Result<ClosedPane, CoordinatorError> {
        let focused = self.focused.ok_or(CoordinatorError::NoFocusedPane)?;
        self.close_pane(focused)
    }

    /// Close the pane bound to `session_id`. Used for lifecycle events such
    /// as a shell exiting while its pane is not focused.
    pub fn close_session(&mut self, session_id: SessionId) -> Result<ClosedPane, CoordinatorError> {
        let pane_id = self
            .tree
            .panes()
            .into_iter()
            .find(|pane| pane.content == PaneContent::Terminal(session_id))
            .map(|pane| pane.id)
            .ok_or(CoordinatorError::SessionPaneNotFound(session_id))?;
        self.close_pane(pane_id)
    }

    fn close_pane(&mut self, pane_id: PaneId) -> Result<ClosedPane, CoordinatorError> {
        let focused = self.focused == Some(pane_id);
        let session_id = self.session_id_for_pane(pane_id);
        let neighbor = if focused {
            [
                SplitDirection::Left,
                SplitDirection::Right,
                SplitDirection::Up,
                SplitDirection::Down,
            ]
            .into_iter()
            .filter_map(|dir| self.tree.neighbor(pane_id, dir))
            .next()
        } else {
            None
        };
        self.tree.remove(pane_id).map_err(CoordinatorError::Core)?;
        if focused {
            self.focused = neighbor
                .filter(|id| self.tree.find(*id).is_some())
                .or_else(|| self.tree.panes().first().map(|pane| pane.id));
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
            .focused
            .and_then(|focused| self.tree.neighbor(focused, direction))?;
        self.focused = Some(next);
        Some(next)
    }

    pub fn focus_pane(&mut self, pane: PaneId) -> Result<(), CoordinatorError> {
        if self.tree.find(pane).is_none() {
            return Err(CoordinatorError::Core(CoreError::PaneNotFound(pane)));
        }
        self.focused = Some(pane);
        Ok(())
    }

    pub fn resize_focused(&mut self, amount: f32) -> Result<f32, CoordinatorError> {
        let focused = self.focused.ok_or(CoordinatorError::NoFocusedPane)?;
        let ancestors = self
            .tree
            .ancestors(focused)
            .ok_or(CoordinatorError::Core(CoreError::PaneNotFound(focused)))?;
        let split = ancestors
            .last()
            .copied()
            .ok_or(CoordinatorError::NoFocusedPane)?;
        let fraction = self
            .tree
            .split_fraction(split)
            .ok_or(CoordinatorError::Core(CoreError::SplitNotFound(split)))?;
        self.tree
            .resize(split, fraction + amount)
            .map_err(CoordinatorError::Core)?;
        Ok(self.tree.split_fraction(split).unwrap_or(fraction))
    }

    pub fn equalize(&mut self) {
        self.tree.equalize();
    }

    pub fn is_empty(&self) -> bool {
        self.tree.is_empty()
    }

    pub fn pane_count(&self) -> usize {
        self.tree.panes().len()
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
        ws.focus_pane(ws.tree.panes()[0].id).unwrap();
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
        let panes: Vec<PaneId> = ws.tree.panes().iter().map(|p| p.id).collect();
        ws.focus_pane(panes[0]).unwrap();
        ws.split_focused_with_shell(SplitDirection::Down, 80, 24, sh())
            .expect("split 3");
        let panes: Vec<PaneId> = ws.tree.panes().iter().map(|p| p.id).collect();
        ws.focus_pane(panes[1]).unwrap();
        ws.split_focused_with_shell(SplitDirection::Down, 80, 24, sh())
            .expect("split 4");
        assert_eq!(ws.pane_count(), 4);
        assert_eq!(ws.session_count(), 4);
        let mut ids: Vec<SessionId> = ws
            .tree
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
}
