use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::mpsc::{self, Receiver, SyncSender, TryRecvError, TrySendError};
use std::thread::{self, JoinHandle};

use omaterm_core::SessionId;

use crate::{SessionError, TerminalConfig, TerminalSession};

/// Result of a background session spawn. The application owner must validate
/// the operation and target again before inserting this session in the registry.
pub struct SpawnCompletion {
    pub operation_id: u64,
    pub session_id: SessionId,
    pub result: Result<TerminalSession, SessionError>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum SpawnQueueError {
    #[error("terminal launch queue is full")]
    Full,
    #[error("terminal launch queue is closed")]
    Closed,
}

struct SpawnRequest {
    operation_id: u64,
    session_id: SessionId,
    config: TerminalConfig,
    child_env: std::collections::HashMap<String, String>,
}

/// A single bounded PTY-launch worker. It never accesses workspace state or a
/// registry; completed sessions are handed back to the submitting owner.
///
/// The request queue is bounded and submission never waits. The completion
/// channel is bounded as well, so an owner that stops collecting results will
/// eventually apply backpressure to submissions rather than grow memory
/// without limit. Dropping the queue disconnects both sides; a session whose
/// completion is abandoned is dropped on the worker and its PTY cleanup path
/// terminates the child.
pub struct SessionSpawnQueue {
    requests: SyncSender<SpawnRequest>,
    completions: Receiver<SpawnCompletion>,
    stopping: Arc<AtomicBool>,
    worker: Option<JoinHandle<()>>,
}

impl SessionSpawnQueue {
    pub fn new(capacity: usize) -> Self {
        Self::with_spawner(capacity, |session_id, config, env| {
            TerminalSession::new_with_id_and_env(
                session_id,
                config.working_directory,
                config.shell.as_deref(),
                config.cols,
                config.rows,
                env,
            )
        })
    }

    fn with_spawner(
        capacity: usize,
        spawn: impl Fn(
            SessionId,
            TerminalConfig,
            std::collections::HashMap<String, String>,
        ) -> Result<TerminalSession, SessionError>
        + Send
        + 'static,
    ) -> Self {
        assert!(capacity > 0, "spawn queue capacity must be non-zero");
        let (request_tx, request_rx) = mpsc::sync_channel::<SpawnRequest>(capacity);
        // One in-flight spawn plus every request accepted into the queue may
        // complete without the owner polling, hence capacity + 1 slots.
        let (completion_tx, completions) = mpsc::sync_channel(capacity + 1);
        let stopping = Arc::new(AtomicBool::new(false));
        let worker_stopping = stopping.clone();
        let worker = thread::Builder::new()
            .name("omaterm-session-spawn".into())
            .spawn(move || spawn_loop(request_rx, completion_tx, worker_stopping, spawn))
            .expect("failed to start terminal spawn worker");
        Self {
            requests: request_tx,
            completions,
            stopping,
            worker: Some(worker),
        }
    }

    /// Queue a launch without blocking. IDs are allocated by the caller so
    /// completions can be correlated and stale results rejected by the owner.
    pub fn try_spawn(
        &self,
        operation_id: u64,
        session_id: SessionId,
        config: TerminalConfig,
    ) -> Result<(), SpawnQueueError> {
        self.try_spawn_with_env(operation_id, session_id, config, Default::default())
    }

    pub fn try_spawn_with_env(
        &self,
        operation_id: u64,
        session_id: SessionId,
        config: TerminalConfig,
        child_env: std::collections::HashMap<String, String>,
    ) -> Result<(), SpawnQueueError> {
        self.requests
            .try_send(SpawnRequest {
                operation_id,
                session_id,
                config,
                child_env,
            })
            .map_err(|error| match error {
                TrySendError::Full(_) => SpawnQueueError::Full,
                TrySendError::Disconnected(_) => SpawnQueueError::Closed,
            })
    }

    /// Non-blocking completion drain for an application event loop.
    pub fn try_recv(&self) -> Result<SpawnCompletion, TryRecvError> {
        self.completions.try_recv()
    }

    /// Stop accepting work, discard queued launches, and join the worker.
    /// Call this from a background thread: a spawn already in progress may
    /// take time to return from the operating system.
    pub fn shutdown(mut self) {
        self.stop_and_join();
    }

    fn stop_and_join(&mut self) {
        self.stopping.store(true, Ordering::Release);
        // Closing completions first prevents a blocked sender from holding up
        // shutdown if the owner is no longer collecting results.
        let (_, replacement_completions) = mpsc::sync_channel(1);
        let original_completions =
            std::mem::replace(&mut self.completions, replacement_completions);
        drop(original_completions);
        // Disconnect the request receiver by dropping the final sender.
        let (replacement, receiver) = mpsc::sync_channel(1);
        drop(receiver);
        let original = std::mem::replace(&mut self.requests, replacement);
        drop(original);
        if let Some(worker) = self.worker.take() {
            let _ = worker.join();
        }
    }
}

impl Drop for SessionSpawnQueue {
    fn drop(&mut self) {
        self.stopping.store(true, Ordering::Release);
        let (_, replacement_completions) = mpsc::sync_channel(1);
        let original_completions =
            std::mem::replace(&mut self.completions, replacement_completions);
        drop(original_completions);
        // Do not join here: the queue is owned by the desktop/UI object and
        // joining could block its event thread. Disconnect and detach instead.
        let (replacement, receiver) = mpsc::sync_channel(1);
        drop(receiver);
        let original = std::mem::replace(&mut self.requests, replacement);
        drop(original);
        self.worker.take();
    }
}

fn spawn_loop(
    requests: mpsc::Receiver<SpawnRequest>,
    completions: SyncSender<SpawnCompletion>,
    stopping: Arc<AtomicBool>,
    spawn: impl Fn(
        SessionId,
        TerminalConfig,
        std::collections::HashMap<String, String>,
    ) -> Result<TerminalSession, SessionError>,
) {
    while let Ok(request) = requests.recv() {
        if stopping.load(Ordering::Acquire) {
            break;
        }
        let result = spawn(request.session_id, request.config, request.child_env);
        if stopping.load(Ordering::Acquire) {
            drop(result);
            break;
        }
        let completion = SpawnCompletion {
            operation_id: request.operation_id,
            session_id: request.session_id,
            result,
        };
        if completions.send(completion).is_err() {
            break;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicBool, Ordering};
    use std::sync::{Arc, Barrier};
    use std::time::Duration;

    fn config(shell: &str) -> TerminalConfig {
        TerminalConfig {
            working_directory: std::env::temp_dir(),
            shell: Some(shell.into()),
            cols: 80,
            rows: 24,
        }
    }

    #[test]
    fn worker_returns_preallocated_identity_and_spawn_failure() {
        let queue = SessionSpawnQueue::new(1);
        let expected = SessionId::new();
        queue
            .try_spawn(9, expected, config("/bin/sh"))
            .expect("enqueue shell");
        let completion = queue.completions.recv().expect("spawn completion");
        assert_eq!(completion.operation_id, 9);
        assert_eq!(completion.session_id, expected);
        let session = completion.result.expect("spawn shell");
        assert_eq!(session.id(), expected);
        drop(session);

        queue
            .try_spawn(10, SessionId::new(), config("/definitely/missing/shell"))
            .expect("enqueue failing shell");
        let completion = queue.completions.recv().expect("failure completion");
        assert!(completion.result.is_err());
        queue.shutdown();
    }

    #[test]
    fn submission_is_nonblocking_and_queue_capacity_is_enforced() {
        let entered = Arc::new(Barrier::new(2));
        let release = Arc::new(Barrier::new(2));
        let first_spawn = Arc::new(AtomicBool::new(true));
        let worker_entered = entered.clone();
        let worker_release = release.clone();
        let worker_first_spawn = first_spawn.clone();
        let queue = SessionSpawnQueue::with_spawner(1, move |id, config, env| {
            if worker_first_spawn.swap(false, Ordering::AcqRel) {
                worker_entered.wait();
                worker_release.wait();
            }
            TerminalSession::new_with_id_and_env(
                id,
                config.working_directory,
                config.shell.as_deref(),
                config.cols,
                config.rows,
                env,
            )
        });
        queue
            .try_spawn(1, SessionId::new(), config("/bin/sh"))
            .expect("first queued request");
        entered.wait(); // Worker is occupied; the one-slot queue is empty.
        queue
            .try_spawn(2, SessionId::new(), config("/bin/sh"))
            .expect("request fits in bounded queue");
        assert_eq!(
            queue.try_spawn(3, SessionId::new(), config("/bin/sh")),
            Err(SpawnQueueError::Full)
        );
        release.wait();
        let first = queue.completions.recv().unwrap();
        drop(first.result);
        let second = queue.completions.recv().unwrap();
        drop(second.result);
        queue.shutdown();
    }

    #[test]
    fn dropping_queue_does_not_wait_for_a_blocked_spawn() {
        let entered = Arc::new(Barrier::new(2));
        let release = Arc::new(Barrier::new(2));
        let worker_entered = entered.clone();
        let worker_release = release.clone();
        let queue = SessionSpawnQueue::with_spawner(1, move |id, config, env| {
            worker_entered.wait();
            worker_release.wait();
            TerminalSession::new_with_id_and_env(
                id,
                config.working_directory,
                config.shell.as_deref(),
                config.cols,
                config.rows,
                env,
            )
        });
        queue
            .try_spawn(1, SessionId::new(), config("/bin/sh"))
            .unwrap();
        entered.wait();
        let start = std::time::Instant::now();
        drop(queue);
        assert!(start.elapsed() < Duration::from_millis(100));
        release.wait();
    }
}
