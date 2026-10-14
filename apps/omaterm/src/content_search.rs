//! Global content search ("search in files") — model and background worker.
//!
//! The backend is `omaterm_context::search_in_files`. This module owns the
//! desktop-side request/result envelope, the single background worker, and the
//! generation bookkeeping that makes stale keystrokes harmless. It has no
//! `gpui` imports, so the worker logic is unit-testable.
//!
//! One worker thread handles one active query and one replaceable pending
//! query: typing fast cancels the in-flight scan and supersedes the queued one
//! instead of spawning a thread per keystroke.
//!
//! The preview pane is **not** served from here. It opens the matched file as
//! a real document and renders it with the editor in read-only mode
//! (`EditorViewMode::ReadOnly`), so preview and editor share one renderer.

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Condvar, Mutex};

use omaterm_context::{ContentSearchResults, SearchOptions, search_in_files};

/// One search request. `generation` tags the response so the UI can drop a
/// result whose query has already been replaced.
pub struct ContentSearchRequest {
    pub generation: u64,
    pub root: std::path::PathBuf,
    pub query: String,
    pub options: SearchOptions,
    pub cancelled: Arc<AtomicBool>,
}

/// One search response.
pub struct ContentSearchResult {
    pub generation: u64,
    pub outcome: Result<Option<ContentSearchResults>, String>,
}

#[derive(Default)]
struct WorkerState {
    pending: Option<ContentSearchRequest>,
    active_cancel: Option<Arc<AtomicBool>>,
    shutdown: bool,
}

/// Single-thread background worker for content searches.
pub struct ContentSearchWorker {
    state: Arc<(Mutex<WorkerState>, Condvar)>,
    result: Arc<Mutex<Option<ContentSearchResult>>>,
}

impl ContentSearchWorker {
    pub fn new() -> Self {
        let state = Arc::new((Mutex::new(WorkerState::default()), Condvar::new()));
        let result = Arc::new(Mutex::new(None));
        let worker_state = Arc::clone(&state);
        let worker_result = Arc::clone(&result);
        std::thread::spawn(move || {
            loop {
                let request = {
                    let (lock, ready) = &*worker_state;
                    let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
                    while state.pending.is_none() && !state.shutdown {
                        state = ready
                            .wait(state)
                            .unwrap_or_else(|poisoned| poisoned.into_inner());
                    }
                    if state.shutdown {
                        return;
                    }
                    let request = state.pending.take().expect("request exists");
                    state.active_cancel = Some(Arc::clone(&request.cancelled));
                    request
                };
                let cancellation = Arc::clone(&request.cancelled);
                let generation = request.generation;
                let outcome = search_in_files(
                    &request.root,
                    &request.query,
                    request.options,
                    Some(&request.cancelled),
                )
                .map_err(|error| error.to_string());
                Self::clear_active(&worker_state, &cancellation);
                if let Ok(mut slot) = worker_result.lock() {
                    *slot = Some(ContentSearchResult {
                        generation,
                        outcome,
                    });
                }
            }
        });
        Self { state, result }
    }

    fn clear_active(state: &Arc<(Mutex<WorkerState>, Condvar)>, cancellation: &Arc<AtomicBool>) {
        let (lock, _) = &**state;
        let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
        if state
            .active_cancel
            .as_ref()
            .is_some_and(|active| Arc::ptr_eq(active, cancellation))
        {
            state.active_cancel = None;
        }
    }

    /// Queue a request, cancelling whatever was running or queued.
    pub fn submit(&self, mut request: ContentSearchRequest) {
        let (lock, ready) = &*self.state;
        let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
        if state.shutdown {
            return;
        }
        if let Some(active) = &state.active_cancel {
            active.store(true, Ordering::Release);
        }
        if let Some(pending) = state.pending.take() {
            pending.cancelled.store(true, Ordering::Release);
        }
        request.cancelled = Arc::new(AtomicBool::new(false));
        state.pending = Some(request);
        if let Ok(mut slot) = self.result.lock() {
            *slot = None;
        }
        ready.notify_one();
    }

    pub fn take_result(&self) -> Option<ContentSearchResult> {
        self.result.lock().ok().and_then(|mut slot| slot.take())
    }

    pub fn cancel_current(&self) {
        let (lock, _) = &*self.state;
        let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
        if let Some(active) = &state.active_cancel {
            active.store(true, Ordering::Release);
        }
        if let Some(pending) = state.pending.take() {
            pending.cancelled.store(true, Ordering::Release);
        }
        if let Ok(mut result) = self.result.lock() {
            *result = None;
        }
    }

    pub fn shutdown(&self) {
        let (lock, ready) = &*self.state;
        let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
        state.shutdown = true;
        if let Some(active) = &state.active_cancel {
            active.store(true, Ordering::Release);
        }
        if let Some(pending) = state.pending.take() {
            pending.cancelled.store(true, Ordering::Release);
        }
        ready.notify_one();
    }
}

impl Default for ContentSearchWorker {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn fixture(name: &str) -> std::path::PathBuf {
        let root = std::env::temp_dir().join(format!("omaterm-csw-{name}-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir_all(&root).unwrap();
        std::fs::write(root.join("a.txt"), "alpha\nbeta\n").unwrap();
        root
    }

    fn wait_for_result(worker: &ContentSearchWorker, generation: u64) -> ContentSearchResult {
        for _ in 0..2000 {
            if let Some(result) = worker.take_result()
                && result.generation == generation
            {
                return result;
            }
            std::thread::sleep(std::time::Duration::from_millis(2));
        }
        panic!("no result for generation {generation}");
    }

    #[test]
    fn worker_returns_matches_for_the_latest_generation() {
        let worker = ContentSearchWorker::new();
        let root = fixture("basic");
        worker.submit(ContentSearchRequest {
            generation: 1,
            root: root.clone(),
            query: "beta".into(),
            options: SearchOptions::default(),
            cancelled: Arc::new(AtomicBool::new(false)),
        });
        let result = wait_for_result(&worker, 1);
        let results = result.outcome.unwrap().unwrap();
        assert_eq!(results.matches.len(), 1);
        std::fs::remove_dir_all(&root).ok();
    }

    #[test]
    fn a_newer_submission_supersedes_the_older_query() {
        let worker = ContentSearchWorker::new();
        let root = fixture("supersede");
        worker.submit(ContentSearchRequest {
            generation: 1,
            root: root.clone(),
            query: "alpha".into(),
            options: SearchOptions::default(),
            cancelled: Arc::new(AtomicBool::new(false)),
        });
        worker.submit(ContentSearchRequest {
            generation: 2,
            root: root.clone(),
            query: "beta".into(),
            options: SearchOptions::default(),
            cancelled: Arc::new(AtomicBool::new(false)),
        });
        let result = wait_for_result(&worker, 2);
        let results = result.outcome.unwrap().unwrap();
        assert_eq!(results.matches.len(), 1);
        assert_eq!(results.matches[0].line, 2);
        std::fs::remove_dir_all(&root).ok();
    }

    #[test]
    fn invalid_pattern_surfaces_as_an_error_string() {
        let worker = ContentSearchWorker::new();
        let root = fixture("invalid");
        worker.submit(ContentSearchRequest {
            generation: 1,
            root: root.clone(),
            query: "(".into(),
            options: SearchOptions {
                regex: true,
                ..Default::default()
            },
            cancelled: Arc::new(AtomicBool::new(false)),
        });
        let result = wait_for_result(&worker, 1);
        assert!(result.outcome.is_err());
        std::fs::remove_dir_all(&root).ok();
    }
}
