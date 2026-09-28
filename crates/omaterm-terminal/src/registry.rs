use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};

use omaterm_core::SessionId;

use crate::session::{SessionError, TerminalSession};

/// Configuration for creating one terminal session.
#[derive(Debug, Clone)]
pub struct TerminalConfig {
    pub working_directory: PathBuf,
    pub shell: Option<String>,
    pub cols: u16,
    pub rows: u16,
    /// Engine scrollback cap. `None` keeps the engine default (10 000 lines);
    /// `Some` comes only from validated `terminal.scrollback-lines` config.
    pub scrollback_lines: Option<usize>,
}

impl TerminalConfig {
    pub fn new(working_directory: PathBuf) -> Self {
        Self {
            working_directory,
            shell: None,
            cols: 80,
            rows: 24,
            scrollback_lines: None,
        }
    }
}

/// Stable registry errors (agents match on codes, not message text).
#[derive(Debug, thiserror::Error)]
pub enum RegistryError {
    #[error(transparent)]
    Spawn(#[from] SessionError),
    #[error("unknown session")]
    UnknownSession(SessionId),
    #[error("session ID is already registered")]
    DuplicateSession(SessionId),
}

/// Owns live terminal sessions by [`SessionId`].
///
/// Views and pane leaves hold only IDs. The registry owns the
/// `Arc<Mutex<TerminalSession>>` handles so background PTY readers and GPUI
/// input handlers can share one durable session while the pane tree only
/// references its ID. The crate stays GPUI-free (`std::sync` only).
pub struct TerminalRegistry {
    sessions: HashMap<SessionId, Arc<Mutex<TerminalSession>>>,
}

impl Default for TerminalRegistry {
    fn default() -> Self {
        Self::new()
    }
}

impl TerminalRegistry {
    pub fn new() -> Self {
        Self {
            sessions: HashMap::new(),
        }
    }

    /// Spawn a session and register it. On spawn failure nothing is inserted.
    pub fn create(&mut self, config: TerminalConfig) -> Result<SessionId, RegistryError> {
        let session = TerminalSession::new_with_id_and_env(
            SessionId::new(),
            config.working_directory,
            config.shell.as_deref(),
            config.cols,
            config.rows,
            Default::default(),
            config.scrollback_lines,
        )?;
        self.insert(session)
    }

    /// Register a session spawned by an application-owned launch worker.
    ///
    /// The owner calls this only after revalidating the logical pane target,
    /// keeping uncommitted PTYs out of registry lookup.
    pub fn insert(&mut self, session: TerminalSession) -> Result<SessionId, RegistryError> {
        let id = session.id();
        if self.sessions.contains_key(&id) {
            return Err(RegistryError::DuplicateSession(id));
        }
        self.sessions.insert(id, Arc::new(Mutex::new(session)));
        Ok(id)
    }

    /// Cloned handle to a live session, if registered.
    pub fn get(&self, id: SessionId) -> Option<Arc<Mutex<TerminalSession>>> {
        self.sessions.get(&id).cloned()
    }

    pub fn contains(&self, id: SessionId) -> bool {
        self.sessions.contains_key(&id)
    }

    pub fn len(&self) -> usize {
        self.sessions.len()
    }

    pub fn is_empty(&self) -> bool {
        self.sessions.is_empty()
    }

    pub fn list(&self) -> Vec<SessionId> {
        let mut ids: Vec<_> = self.sessions.keys().copied().collect();
        ids.sort_by_key(|id| id.0);
        ids
    }

    /// Remove a session from future lookup and return its handle.
    ///
    /// The caller owns shutdown/reap (off the UI thread). Dropping the last
    /// `Arc` frees engine memory; the `PtyProcess` drop path guarantees
    /// SIGHUP even if explicit shutdown is skipped.
    pub fn detach(&mut self, id: SessionId) -> Option<Arc<Mutex<TerminalSession>>> {
        self.sessions.remove(&id)
    }

    /// Remove a session and run bounded shutdown synchronously.
    ///
    /// Returns `true` when the child is confirmed gone. Bounded (2s cap
    /// inside `TerminalSession::shutdown`); callers on the UI thread must
    /// use [`Self::detach`] + a background thread instead.
    pub fn close(&mut self, id: SessionId) -> Result<bool, RegistryError> {
        let handle = self
            .sessions
            .remove(&id)
            .ok_or(RegistryError::UnknownSession(id))?;
        let mut session = match handle.lock() {
            Ok(guard) => guard,
            Err(_) => return Ok(false),
        };
        Ok(session.shutdown())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn config() -> TerminalConfig {
        TerminalConfig {
            working_directory: std::env::temp_dir(),
            shell: Some("/bin/sh".to_string()),
            cols: 80,
            rows: 24,
            scrollback_lines: None,
        }
    }

    #[test]
    fn create_registers_session_with_matching_id() {
        let mut registry = TerminalRegistry::new();
        let id = registry.create(config()).expect("spawn sh");
        assert!(registry.contains(id));
        let handle = registry.get(id).expect("registered");
        assert_eq!(handle.lock().unwrap().id(), id);
        assert_eq!(registry.len(), 1);
    }

    #[test]
    fn multiple_sessions_have_distinct_ids_and_pids() {
        let mut registry = TerminalRegistry::new();
        let first = registry.create(config()).expect("spawn sh");
        let second = registry.create(config()).expect("spawn sh");
        assert_ne!(first, second);
        let first_pid = registry.get(first).unwrap().lock().unwrap().child_pid();
        let second_pid = registry.get(second).unwrap().lock().unwrap().child_pid();
        assert_ne!(first_pid, second_pid);
        assert_eq!(registry.len(), 2);
    }

    #[test]
    fn invalid_size_fails_without_registering() {
        let mut registry = TerminalRegistry::new();
        let bad = TerminalConfig {
            working_directory: std::env::temp_dir(),
            shell: Some("/bin/sh".to_string()),
            cols: 1,
            rows: 24,
            scrollback_lines: None,
        };
        let result = registry.create(bad);
        assert!(matches!(
            result,
            Err(RegistryError::Spawn(SessionError::InvalidSize { .. }))
        ));
        assert!(registry.is_empty());
    }

    #[test]
    fn inserts_a_worker_spawned_session_with_its_preallocated_id() {
        let mut registry = TerminalRegistry::new();
        let expected = SessionId::new();
        let session =
            TerminalSession::new_with_id(expected, std::env::temp_dir(), Some("/bin/sh"), 80, 24)
                .expect("spawn sh");
        assert_eq!(registry.insert(session).expect("register"), expected);
        assert!(matches!(
            registry.insert(
                TerminalSession::new_with_id(
                    expected,
                    std::env::temp_dir(),
                    Some("/bin/sh"),
                    80,
                    24,
                )
                .expect("spawn second sh")
            ),
            Err(RegistryError::DuplicateSession(id)) if id == expected
        ));
        assert!(registry.close(expected).expect("close sh"));
    }

    #[test]
    fn close_removes_only_target_and_reaps() {
        let mut registry = TerminalRegistry::new();
        let first = registry.create(config()).expect("spawn sh");
        let second = registry.create(config()).expect("spawn sh");
        let reaped = registry.close(first).expect("close first");
        assert!(reaped, "sh should reap within the bounded shutdown");
        assert!(!registry.contains(first));
        assert!(registry.get(first).is_none());
        assert!(registry.contains(second));
        assert_eq!(registry.len(), 1);
    }

    #[test]
    fn close_unknown_session_returns_stable_error() {
        let mut registry = TerminalRegistry::new();
        let missing = SessionId::new();
        assert!(matches!(
            registry.close(missing),
            Err(RegistryError::UnknownSession(_))
        ));
    }

    #[test]
    fn detach_returns_handle_for_off_thread_shutdown() {
        let mut registry = TerminalRegistry::new();
        let id = registry.create(config()).expect("spawn sh");
        let handle = registry.detach(id).expect("detach");
        assert!(!registry.contains(id));
        assert!(registry.is_empty());
        assert_eq!(handle.lock().unwrap().id(), id);
        let reaped = handle.lock().unwrap().shutdown();
        assert!(reaped);
    }

    #[test]
    fn exited_session_stays_registered_until_close() {
        let mut registry = TerminalRegistry::new();
        let id = registry.create(config()).expect("spawn sh");
        {
            let handle = registry.get(id).unwrap();
            let mut session = handle.lock().unwrap();
            session.write_input(b"exit\n").expect("write exit");
            session.poll_readable(2000).expect("poll");
            let _ = session.pump();
            let mut exited = session.exited().is_some();
            for _ in 0..50 {
                if exited {
                    break;
                }
                std::thread::sleep(std::time::Duration::from_millis(50));
                exited = session.poll_child();
            }
            assert!(exited, "sh should have exited after `exit`");
        }
        // Still registered and retrievable until explicit close.
        assert!(registry.contains(id));
        assert!(registry.get(id).is_some());
        assert!(registry.close(id).is_ok());
        assert!(registry.is_empty());
    }

    #[test]
    fn repeated_create_close_leaves_no_entries() {
        let mut registry = TerminalRegistry::new();
        for _ in 0..25 {
            let id = registry.create(config()).expect("spawn sh");
            registry.close(id).expect("close");
        }
        assert!(registry.is_empty());
        assert!(registry.list().is_empty());
    }
}
