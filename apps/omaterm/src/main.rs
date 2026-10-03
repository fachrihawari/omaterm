use std::cell::Cell;
use std::collections::HashMap;
use std::path::Path;
use std::rc::Rc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Condvar, Mutex};
use std::time::{Duration, Instant};

use gpui::{
    App, Application, AsyncApp, Bounds, ClipboardItem, Context, Div, ExternalPaths, FocusHandle,
    Font, FontFallbacks, HighlightStyle, Hsla, KeyDownEvent, ModifiersChangedEvent, MouseButton,
    MouseDownEvent, MouseMoveEvent, MouseUpEvent, PathPromptOptions, Pixels, ScrollDelta,
    ScrollHandle, ScrollStrategy, ScrollWheelEvent, SharedString, StyledText, TextRun, Timer,
    UniformListScrollHandle, WeakEntity, Window, WindowBounds, WindowOptions, canvas, div, font,
    hsla, prelude::*, px, relative, rgb, rgba, size, uniform_list,
};
use omaterm_core::{
    CommandContext, CommandOutput, CommandResult, DiffCommand, DocumentId, EditorCommand,
    FileCommand, FileEntry, GitCommand, OmaCommand, Pane, PaneCommand, PaneContent, PaneId,
    PaneNode, ProjectCommand, ProjectId, SessionId, SplitAxis, SplitDirection, TabCommand,
    TerminalCommand,
};
use omaterm_ipc::{IpcServer, RequestHandler};
use omaterm_protocol::{IpcRequest, IpcResponse};
use omaterm_state::{
    CwdProvenance as StoredCwdProvenance, LoadOutcome, PersistedCwd, SnapshotDestination,
    SnapshotStore, SnapshotWriter, WorkspaceSnapshot,
};
use omaterm_terminal::{
    CellPoint, CellWidth, Key, KeyEvent, KeyModifiers, ScrollCommand, SelectionRange, TermColor,
    TerminalSession, TerminalViewport, WorkspaceCoordinator, encode_key, extract_text,
    format_dropped_paths, needs_paste_confirm, poll_fd_readable, prepare_paste,
};
mod credentials;
mod diff_panel;
mod editor;
mod files;
mod git_panel;
mod history;
mod ipc_bridge;
mod palette;
mod router;
mod ui;
mod workbench;

/// M4 workspace: a recursive pane tree whose leaves reference
/// registry-owned `TerminalSession`s by `SessionId`.
///
/// Sessions are durable: focus changes, resizes, and sibling split/close
/// never recreate them. Each session has a background reader thread pumping
/// PTY output into engine state and forwarding immutable snapshots over a
/// bounded channel; the main thread applies snapshots event-driven and
/// repaints. Painting never holds a session lock.
struct WorkspaceView {
    focus_handle: FocusHandle,
    coordinator: router::CommandRouter,
    snapshots: HashMap<SessionId, TerminalViewport>,
    receivers: HashMap<SessionId, async_channel::Receiver<TerminalViewport>>,
    selections: HashMap<SessionId, SelectionRange>,
    selecting: Option<PaneId>,
    scroll_indicator_until: HashMap<SessionId, Instant>,
    grid_origins: HashMap<PaneId, Rc<Cell<gpui::Point<Pixels>>>>,
    /// Last grid applied to each session. Compared in `render` so the PTY
    /// follows pane geometry without locking sessions on every frame.
    grid_sizes: HashMap<SessionId, (u16, u16)>,
    fonts: Option<ResolvedFonts>,
    font_size: f32,
    /// Configured `terminal.font-family` override. Applied when the family is
    /// installed; otherwise the built-in preference stack wins and a warning
    /// names the missing family once.
    font_family: Option<String>,
    spawn_failure: Option<(String, SpawnRetry)>,
    persistence_store: Option<SnapshotStore>,
    persistence_writer: Option<SnapshotWriter>,
    persistence_destination: SnapshotDestination,
    persistence_warning: Option<String>,
    /// General `config.toml` problem (malformed file or invalid value).
    /// Defaults apply; the message names the offending key.
    config_warning: Option<String>,
    save_revision: u64,
    observed_cwds: HashMap<PaneId, PersistedCwd>,
    restored_failures: HashMap<PaneId, (omaterm_core::ProjectId, omaterm_core::TabId, String)>,
    pending_ui_launches: HashMap<u64, PendingUiLaunch>,
    launch_poller_active: bool,
    /// True while Control or Shift is held: the sidebar then shows the
    /// `Ctrl+Shift+1..9` jump index next to each project.
    show_project_hints: bool,
    /// Project whose context menu is open. The menu is transient view chrome;
    /// keeping it outside the card preserves the reference card height.
    project_context_menu: Option<ProjectId>,
    ipc_server: Option<IpcServer>,
    ipc_receiver: Option<async_channel::Receiver<IpcWork>>,
    ipc_pending: HashMap<u64, IpcWork>,
    shutting_down: bool,
    history_arm: Option<(HistoryArm, Instant)>,
    /// Armed risky paste: (session, bytes, armed-at). The first Ctrl+Shift+V
    /// of a multiline/control-character paste arms with a banner; the second
    /// identical paste within the window sends. Anything else disarms.
    paste_arm: Option<(SessionId, Vec<u8>, Instant)>,
    /// Transient input notice (paste confirmation prompt, drop errors).
    input_notice: Option<String>,
    /// Bottom-center toast (message, hide-after deadline). Pointer-
    /// transparent; action confirmations only, never errors or prompts.
    toast: Option<(String, Instant)>,
    /// M13 file panel: contextual-sidebar tree rows for the selected project.
    files_panel: files::FilePanel,
    /// Watcher-limit banner (inotify exhaustion keeps the last good tree).
    files_warning: Option<String>,
    /// Active recursive watcher plus the (project, root) it watches.
    files_watcher: Option<omaterm_context::FileWatcher>,
    files_watched: Option<(ProjectId, std::path::PathBuf)>,
    /// Background watcher-arm completions `(generation, project, root,
    /// result)`; arming walks the whole tree, so it never runs on the UI
    /// thread. Stale generations drop on project/root transitions.
    files_arm_tx: std::sync::mpsc::Sender<(
        u64,
        ProjectId,
        std::path::PathBuf,
        Result<omaterm_context::FileWatcher, omaterm_context::WatchError>,
    )>,
    files_arm_rx: std::sync::mpsc::Receiver<(
        u64,
        ProjectId,
        std::path::PathBuf,
        Result<omaterm_context::FileWatcher, omaterm_context::WatchError>,
    )>,
    /// Watcher arm currently in flight, if any (dedupes re-arms).
    files_arming: Option<(ProjectId, std::path::PathBuf)>,
    /// Last event-batch refresh; batches coalesce to ~1Hz max so a busy
    /// filesystem can never keep the UI thread hot.
    files_last_event_refresh: Instant,
    /// Watcher hit flag + last-event time (set on the notify thread,
    /// consumed debounced by the files poller on the UI thread).
    files_event: Arc<AtomicBool>,
    files_event_at: Arc<Mutex<Instant>>,
    /// Absolute event paths batch-drained by the poller; each maps to one
    /// invalidated tree directory (targeted refetch, never a blind walk).
    files_event_paths: Arc<Mutex<Vec<std::path::PathBuf>>>,
    /// Background directory-fetch completions `(generation, project, dir,
    /// entries)`; stale generations drop on project/root transitions.
    files_fetch_tx: std::sync::mpsc::Sender<(u64, ProjectId, std::path::PathBuf, Vec<FileEntry>)>,
    files_fetch_rx: std::sync::mpsc::Receiver<(u64, ProjectId, std::path::PathBuf, Vec<FileEntry>)>,
    files_generation: u64,
    /// Per-project top-level listing caps (`Show more` paging; ephemeral,
    /// never persisted). Nested dirs always use the config default.
    files_root_caps: HashMap<ProjectId, usize>,
    /// Inspector search-box query: filters cached tree rows by file-name
    /// substring (view-local, never persisted). Empty matches everything.
    files_search: String,
    /// Whether the inspector search box owns the keyboard. Clicking it
    /// focuses; Esc/Enter, tab switches, and terminal clicks release it.
    files_search_focused: bool,
    /// Wheel scroll offset into the tree rows (row-granular, clamped every
    /// render). Reset on project switch.
    files_scroll_rows: usize,
    /// Fractional wheel/trackpad remainder in row units. Retains small native
    /// deltas until a complete row can be shown instead of forcing a jump.
    files_scroll_remainder: f32,
    /// Thumb drag in flight: (last pointer position, sub-row accumulator).
    /// Cleared on release, pane clicks, and tab switches (no stuck drags).
    files_vdrag: Option<(f32, f32)>,
    files_last_resolve: Instant,
    files_poller_active: bool,
    /// `Ctrl+P` fuzzy finder overlay state.
    ctrlp_open: bool,
    ctrlp_query: String,
    ctrlp_caret_byte: usize,
    ctrlp_results: Vec<palette::PaletteCandidate>,
    ctrlp_static_results: Vec<palette::PaletteCandidate>,
    ctrlp_selected: usize,
    ctrlp_truncated: bool,
    ctrlp_source_error: Option<String>,
    ctrlp_generation: u64,
    palette_search_worker: PaletteSearchWorker,
    files_show_hidden: bool,
    ctrlp_project: Option<ProjectId>,
    ctrlp_origin: Option<PaletteOrigin>,
    ctrlp_search_root: Option<std::path::PathBuf>,
    palette_file_index: Arc<PaletteFileIndexCache>,
    palette_scroll_handle: UniformListScrollHandle,
    /// Successful palette actions, newest first; session-local and bounded.
    palette_mru: Vec<String>,
    /// Recent file targets are retained only in memory, scoped to the active
    /// project/root and revalidated again on execution.
    palette_recent_files: Vec<palette::PaletteCandidate>,
    /// Candidate keys whose async create/split launch must commit before
    /// the action is eligible for MRU promotion.
    pending_palette_mru: HashMap<u64, String>,
    pending_palette_origins: HashMap<u64, PaletteOrigin>,
    process_list: Option<(ProjectId, omaterm_core::ProcessListInfo)>,
    ctrlp_caret_on: bool,
    ctrlp_blink_active: bool,
    /// M14 Source Control panel: last-good statuses, explicit empty/error
    /// states, selection, and the discard two-step arm (GPUI-free).
    git_panel: git_panel::GitPanel,
    /// Background status-refresh completions `(generation, project,
    /// outcome)`. Root resolution and `git status` both run on the worker
    /// (never the UI thread); stale generations drop on project switch.
    git_tx: std::sync::mpsc::Sender<(u64, ProjectId, git_panel::GitRefresh)>,
    git_rx: std::sync::mpsc::Receiver<(u64, ProjectId, git_panel::GitRefresh)>,
    git_generation: u64,
    /// Project with a refresh in flight, if any (one status call at a
    /// time; the rest wait for the next poller tick).
    git_in_flight: Option<ProjectId>,
    /// Last landed refresh per project (interval source).
    git_refreshed_at: HashMap<ProjectId, Instant>,
    /// Set by manual refresh, git mutations, and post-`terminal.run`
    /// submissions: the next tick refreshes immediately.
    git_dirty_hint: bool,
    /// Last project the git poller served (switch detection).
    git_last_project: Option<ProjectId>,
    /// UI v5 shell: independent Projects (left) and Inspector (right)
    /// panels. View chrome, never persisted. Widths clamp to the v5 ranges
    /// on every resize; visibility toggles remember the last nonzero width.
    projects_visible: bool,
    projects_width: f32,
    /// Projects-resize drag in flight: last pointer x. Cleared on release.
    projects_resize: Option<f32>,
    inspector_visible: bool,
    inspector_width: f32,
    /// Inspector-resize drag in flight: last pointer x. Cleared on release.
    inspector_resize: Option<f32>,
    /// Inspector tab: Info, Files, or Git. Defaults to Info every launch
    /// (mock default); intentionally not persisted.
    inspector_tab: InspectorTab,
    /// M15 unified-diff panel: last-good diffs per project+side, explicit
    /// empty/error states, file/hunk selection, staged toggle (GPUI-free).
    diff_panel: diff_panel::DiffPanel,
    /// Native virtual-list positions keyed by project/side/path/mode so a
    /// different preview cannot inherit another file's scroll offset.
    diff_scroll_handles:
        HashMap<(ProjectId, bool, std::path::PathBuf, diff_panel::DiffMode), DiffPreviewScroll>,
    /// Single background Git worker with one replaceable pending selected diff.
    diff_worker: diff_panel::DiffWorker,
    diff_generation: u64,
    /// Project+side with a refresh in flight, if any (one diff call at a
    /// time; the rest wait for the next poller tick).
    diff_in_flight: Option<diff_panel::DiffRequestKey>,
    /// Last landed refresh per project+side (interval source).
    diff_refreshed_at: HashMap<(ProjectId, bool), Instant>,
    /// Set by manual refresh, staged-toggle, git mutations, and
    /// post-`terminal.run` submissions: the next tick refreshes immediately.
    diff_dirty_hint: bool,
    /// Last project the diff poller served (switch detection).
    diff_last_project: Option<ProjectId>,
    /// Native editor documents (M19 Phase D): view-local activation per
    /// project. Buffers live in the router-owned `DocumentStore`; these
    /// maps hold caret, scroll, and presentation state only.
    ///
    /// `editor_active` is the *surface*: which document replaces the terminal
    /// area for a project. `editor_selected` is the *selection*: the document
    /// chip highlighted as current. Startup restore sets selection without
    /// activating the surface, so a restored selection never steals the
    /// terminal on launch.
    editor_active: HashMap<ProjectId, DocumentId>,
    editor_selected: HashMap<ProjectId, DocumentId>,
    editor_carets: HashMap<DocumentId, editor::EditorCaret>,
    editor_pending_action: Option<EditorPendingAction>,
    editor_rows_handles: HashMap<DocumentId, UniformListScrollHandle>,
    editor_x_handles: HashMap<DocumentId, ScrollHandle>,
    /// Measured width from the latest accepted highlight result. Token spans
    /// themselves live in the store's immutable render snapshot.
    editor_highlight_widths: HashMap<DocumentId, usize>,
    editor_highlight_worker: editor::HighlightWorker,
    /// Body origin per document for click-to-caret mapping, recorded by a
    /// paint-time canvas like the terminal grid origins. Rebuilt every frame.
    editor_body_origins: HashMap<DocumentId, Rc<Cell<gpui::Point<Pixels>>>>,
    /// Active pointer drag: document plus the press-point anchor offset.
    /// Cleared on release.
    editor_selecting: Option<(DocumentId, usize)>,
    /// Bounded restart restores: descriptors are scheduled one at a time, and
    /// the next is enqueued only after the previous completes. This keeps at
    /// most one restore read in flight without blocking terminal startup.
    document_restore_queue: std::collections::VecDeque<router::DocumentRestoreRequest>,
    /// Router receipt for the restore read currently in flight.
    document_restore_in_flight: Option<u64>,
    /// Monotonic counter bumped by any user focus action after startup. A
    /// pending restore only activates its document when this is unchanged, so
    /// a late restore load cannot steal focus from the user.
    focus_epoch: u64,
}

/// Editor geometry and type in logical pixels (plan §14: 54px gutter,
/// 22px lines, 12.5px monospace).
const EDITOR_GUTTER_W: f32 = 54.0;
const EDITOR_ROW_H: f32 = 22.0;
const EDITOR_FONT_SIZE: f32 = 12.5;

#[derive(Default)]
struct DiffPreviewScroll {
    rows: UniformListScrollHandle,
    old: ScrollHandle,
    new: ScrollHandle,
    inline: ScrollHandle,
    widths: Option<(Arc<[diff_panel::PreviewRow]>, String, f32)>,
}

/// Two-step destructive-or-sensitive history control: the first press arms
/// (with an explicit disclosure banner), the second press within the window
/// executes. Anything else disarms.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum HistoryArm {
    Enable,
    Disable,
    ClearPane(PaneId),
}

/// Right-inspector panel: Info, Files tree, or Source Control. Defaults to
/// Info every launch (mock default); intentionally not persisted (view
/// chrome, not workspace state — no schema churn).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
enum InspectorTab {
    #[default]
    Info,
    Files,
    Git,
}

#[derive(Clone, Copy)]
enum EditorPendingAction {
    Close(ProjectId, DocumentId),
    Revert(DocumentId),
    Shutdown(gpui::AnyWindowHandle),
}

/// Arm window for two-step history controls.
const HISTORY_ARM_WINDOW: Duration = Duration::from_secs(8);

/// Arm window for two-step risky-paste confirmation.
const PASTE_ARM_WINDOW: Duration = Duration::from_secs(8);

/// Toast visibility after the last confirmation (mock: 1400ms).
const TOAST_MS: u64 = 1400;

struct IpcWork {
    request: IpcRequest,
    reply: std::sync::mpsc::SyncSender<IpcResponse>,
    deadline: Instant,
    cancelled: Arc<AtomicBool>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
struct PaletteOrigin {
    project: Option<ProjectId>,
    tab: Option<omaterm_core::TabId>,
    pane: Option<PaneId>,
    session: Option<SessionId>,
}

struct PaletteSearchResult {
    generation: u64,
    entries: Vec<palette::PaletteCandidate>,
    truncated: bool,
    error: Option<String>,
    root: Option<std::path::PathBuf>,
}

struct PaletteSearchRequest {
    generation: u64,
    project: ProjectId,
    query: String,
    show_hidden: bool,
    pinned: Option<std::path::PathBuf>,
    active_cwd: Option<std::path::PathBuf>,
    watched_root: Option<std::path::PathBuf>,
    cancelled: Arc<AtomicBool>,
}

struct CachedPaletteFileIndex {
    project: ProjectId,
    root: std::path::PathBuf,
    show_hidden: bool,
    index: Arc<omaterm_context::FileSearchIndex>,
}

#[derive(Default)]
struct PaletteFileIndexState {
    entry: Option<CachedPaletteFileIndex>,
    building: bool,
    generation: u64,
}

#[derive(Default)]
struct PaletteFileIndexCache {
    state: Mutex<PaletteFileIndexState>,
    ready: Condvar,
}

impl PaletteFileIndexCache {
    fn get_or_build(
        &self,
        project: ProjectId,
        root: &std::path::Path,
        show_hidden: bool,
        cancelled: &AtomicBool,
    ) -> Result<Option<Arc<omaterm_context::FileSearchIndex>>, omaterm_context::ContextError> {
        let canonical = std::fs::canonicalize(root)?;
        loop {
            if cancelled.load(Ordering::Acquire) {
                return Ok(None);
            }
            let mut state = self
                .state
                .lock()
                .unwrap_or_else(|poisoned| poisoned.into_inner());
            if let Some(entry) = &state.entry
                && entry.project == project
                && entry.root == canonical
                && entry.show_hidden == show_hidden
            {
                return Ok(Some(Arc::clone(&entry.index)));
            }
            if !state.building {
                state.building = true;
                let generation = state.generation;
                drop(state);
                let built = omaterm_context::FileSearchIndex::build(
                    &canonical,
                    show_hidden,
                    Some(cancelled),
                );
                let mut state = self
                    .state
                    .lock()
                    .unwrap_or_else(|poisoned| poisoned.into_inner());
                state.building = false;
                match built {
                    Ok(Some(index)) => {
                        let index = Arc::new(index);
                        let still_current = state.generation == generation;
                        if still_current {
                            state.entry = Some(CachedPaletteFileIndex {
                                project,
                                root: index.root.clone(),
                                show_hidden,
                                index: Arc::clone(&index),
                            });
                        }
                        self.ready.notify_all();
                        return Ok(still_current.then_some(index));
                    }
                    Ok(None) => {
                        self.ready.notify_all();
                        return Ok(None);
                    }
                    Err(error) => {
                        self.ready.notify_all();
                        return Err(error);
                    }
                }
            }
            let waited = self
                .ready
                .wait_timeout(state, Duration::from_millis(20))
                .unwrap_or_else(|poisoned| poisoned.into_inner());
            state = waited.0;
            drop(state);
        }
    }

    fn invalidate(&self) {
        let mut state = self
            .state
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner());
        state.generation = state.generation.wrapping_add(1);
        state.entry = None;
        self.ready.notify_all();
    }
}

#[derive(Default)]
struct PaletteSearchWorkerState {
    pending: Option<PaletteSearchRequest>,
    active_cancel: Option<Arc<AtomicBool>>,
    shutdown: bool,
}

struct PaletteSearchWorker {
    state: Arc<(Mutex<PaletteSearchWorkerState>, Condvar)>,
    result: Arc<Mutex<Option<PaletteSearchResult>>>,
}

impl PaletteSearchWorker {
    fn new(cache: Arc<PaletteFileIndexCache>) -> Self {
        let state = Arc::new((
            Mutex::new(PaletteSearchWorkerState::default()),
            Condvar::new(),
        ));
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
                    let request = state.pending.take().expect("palette request exists");
                    state.active_cancel = Some(Arc::clone(&request.cancelled));
                    request
                };
                let cancellation = Arc::clone(&request.cancelled);
                let response = run_palette_search(request, &cache);
                {
                    let (lock, _) = &*worker_state;
                    let mut state = lock.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
                    if state
                        .active_cancel
                        .as_ref()
                        .is_some_and(|active| Arc::ptr_eq(active, &cancellation))
                    {
                        state.active_cancel = None;
                    }
                }
                if let Ok(mut result) = worker_result.lock() {
                    *result = Some(response);
                }
            }
        });
        Self { state, result }
    }

    fn submit(&self, mut request: PaletteSearchRequest) {
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
        if let Ok(mut result) = self.result.lock() {
            *result = None;
        }
        ready.notify_one();
    }

    fn take_result(&self) -> Option<PaletteSearchResult> {
        self.result.lock().ok().and_then(|mut result| result.take())
    }

    fn cancel_current(&self) {
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

    fn shutdown(&self) {
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

fn run_palette_search(
    request: PaletteSearchRequest,
    cache: &PaletteFileIndexCache,
) -> PaletteSearchResult {
    let PaletteSearchRequest {
        generation,
        project,
        query,
        show_hidden,
        pinned,
        active_cwd,
        watched_root,
        cancelled,
    } = request;
    let root = watched_root
        .or_else(|| omaterm_context::resolve_root(pinned.as_deref(), active_cwd.as_deref()).root);
    let (entries, truncated, error, root) = match root {
        Some(root) => match cache.get_or_build(project, &root, show_hidden, &cancelled) {
            Ok(Some(index)) => match index.search(&query, 100, Some(&cancelled)) {
                Some(list) => {
                    let entries = list
                        .entries
                        .into_iter()
                        .map(|entry| {
                            palette::file_candidate(
                                project,
                                &index.root,
                                entry.path.clone(),
                                entry.path.to_string_lossy().into_owned(),
                            )
                        })
                        .collect();
                    (entries, list.truncated, None, Some(index.root.clone()))
                }
                None => (Vec::new(), false, None, Some(index.root.clone())),
            },
            Ok(None) => (Vec::new(), false, None, Some(root)),
            Err(error) => (Vec::new(), false, Some(error.to_string()), Some(root)),
        },
        None => (
            Vec::new(),
            false,
            Some("Project has no file root.".into()),
            None,
        ),
    };
    PaletteSearchResult {
        generation,
        entries,
        truncated,
        error,
        root,
    }
}

enum PendingUiLaunch {
    Project(Option<std::path::PathBuf>),
    Tab(omaterm_core::ProjectId),
    Restore {
        project: omaterm_core::ProjectId,
        tab: omaterm_core::TabId,
        pane: PaneId,
    },
    EditorOpen(omaterm_core::ProjectId),
    EditorSave,
    EditorRevert(DocumentId),
    /// Restart restore completion: only activates the saved active document
    /// after a successful load, and never steals focus from a user action
    /// taken after startup.
    EditorRestore {
        project: omaterm_core::ProjectId,
        document: DocumentId,
    },
    Other,
}

#[derive(Clone)]
enum SpawnRetry {
    Project(Option<std::path::PathBuf>),
    Tab(omaterm_core::ProjectId),
}

/// How long the scroll thumb lingers after the last scroll input.
const SCROLL_INDICATOR_FADE_MS: u64 = 800;

impl WorkspaceView {
    /// Load general `config.toml` settings with explicit failure reporting.
    /// Returns the effective config plus an optional banner warning: malformed
    /// files and invalid values fall back to defaults (never silent), and a
    /// recorded `automation.enabled=false` is surfaced because IPC stays
    /// enabled in v0.1 by design.
    fn startup_config() -> (omaterm_state::AppConfig, Option<String>) {
        match omaterm_state::AppConfig::load() {
            Ok(config) => {
                let warning = (!config.automation_enabled()).then(|| {
                    "Config: automation.enabled=false is recorded; IPC stays enabled in v0.1".into()
                });
                (config, warning)
            }
            Err(error) => (
                omaterm_state::AppConfig::default(),
                Some(format!("Config: {error}; using defaults")),
            ),
        }
    }

    fn new(window: &mut Window, cx: &mut Context<Self>) -> Self {
        let focus_handle = cx.focus_handle();
        window.focus(&focus_handle);
        let working_directory = std::env::current_dir().unwrap_or_else(|_| std::env::temp_dir());
        let store = omaterm_state::default_snapshot_path().map(SnapshotStore::new);
        let persistence_available = store.is_some();
        let persistence_writer = store
            .as_ref()
            .map(|store| SnapshotWriter::new(store.clone()));
        // General config (11D): malformed files and invalid values warn and
        // fall back to defaults; unknown sections are never touched.
        let (app_config, config_warning) = Self::startup_config();
        let mut coordinator = WorkspaceCoordinator::new(working_directory);
        coordinator.set_scrollback_lines(app_config.resolved_scrollback_lines());
        // Background directory-fetch channel for lazy tree loading;
        // completions are generation-guarded in the files poller.
        let (files_fetch_tx, files_fetch_rx) = std::sync::mpsc::channel();
        // Background watcher-arm channel; arming walks the whole tree, so
        // it never runs on the UI thread.
        let (files_arm_tx, files_arm_rx) = std::sync::mpsc::channel();
        // Background git-status channel (M14): root resolution and the
        // status subprocess both run on the worker, never the UI thread.
        let (git_tx, git_rx) = std::sync::mpsc::channel();
        let palette_file_index = Arc::new(PaletteFileIndexCache::default());
        let palette_search_worker = PaletteSearchWorker::new(Arc::clone(&palette_file_index));
        let mut view = Self {
            focus_handle,
            coordinator: router::CommandRouter::new(coordinator),
            snapshots: HashMap::new(),
            receivers: HashMap::new(),
            selections: HashMap::new(),
            selecting: None,
            scroll_indicator_until: HashMap::new(),
            grid_origins: HashMap::new(),
            grid_sizes: HashMap::new(),
            fonts: None,
            font_size: app_config.resolved_font_size(),
            font_family: app_config.terminal.font_family.clone(),
            spawn_failure: None,
            persistence_store: store,
            persistence_writer,
            persistence_destination: SnapshotDestination::Primary,
            persistence_warning: (!persistence_available).then(|| {
                "Persistent workspace state unavailable: no usable state directory".into()
            }),
            config_warning,
            save_revision: 0,
            observed_cwds: HashMap::new(),
            restored_failures: HashMap::new(),
            pending_ui_launches: HashMap::new(),
            launch_poller_active: false,
            show_project_hints: false,
            project_context_menu: None,
            ipc_server: None,
            ipc_receiver: None,
            ipc_pending: HashMap::new(),
            shutting_down: false,
            history_arm: None,
            paste_arm: None,
            input_notice: None,
            toast: None,
            files_panel: files::FilePanel::default(),
            files_warning: None,
            files_watcher: None,
            files_watched: None,
            files_event: Arc::new(AtomicBool::new(false)),
            files_event_at: Arc::new(Mutex::new(Instant::now())),
            files_event_paths: Arc::new(Mutex::new(Vec::new())),
            files_fetch_tx,
            files_fetch_rx,
            files_generation: 0,
            files_arm_tx,
            files_arm_rx,
            files_arming: None,
            files_last_event_refresh: Instant::now()
                .checked_sub(Duration::from_secs(60))
                .unwrap_or_else(Instant::now),
            files_root_caps: HashMap::new(),
            files_search: String::new(),
            files_search_focused: false,
            files_scroll_rows: 0,
            files_scroll_remainder: 0.0,
            files_vdrag: None,
            files_last_resolve: Instant::now()
                .checked_sub(Duration::from_secs(60))
                .unwrap_or_else(Instant::now),
            files_poller_active: false,
            ctrlp_open: false,
            ctrlp_query: String::new(),
            ctrlp_caret_byte: 0,
            ctrlp_results: Vec::new(),
            ctrlp_static_results: Vec::new(),
            ctrlp_selected: 0,
            ctrlp_truncated: false,
            ctrlp_source_error: None,
            ctrlp_generation: 0,
            palette_search_worker,
            files_show_hidden: app_config.show_hidden(),
            ctrlp_project: None,
            ctrlp_origin: None,
            ctrlp_search_root: None,
            palette_file_index,
            palette_scroll_handle: UniformListScrollHandle::new(),
            palette_mru: Vec::new(),
            palette_recent_files: Vec::new(),
            pending_palette_mru: HashMap::new(),
            pending_palette_origins: HashMap::new(),
            process_list: None,
            ctrlp_caret_on: true,
            ctrlp_blink_active: false,
            git_panel: git_panel::GitPanel::default(),
            git_tx,
            git_rx,
            git_generation: 0,
            git_in_flight: None,
            git_refreshed_at: HashMap::new(),
            git_dirty_hint: true,
            git_last_project: None,
            projects_visible: true,
            projects_width: crate::ui::geometry::PROJECTS_DEFAULT,
            projects_resize: None,
            inspector_visible: true,
            inspector_width: crate::ui::geometry::INSPECTOR_DEFAULT,
            inspector_resize: None,
            inspector_tab: InspectorTab::Info,
            diff_panel: diff_panel::DiffPanel::default(),
            diff_scroll_handles: HashMap::new(),
            diff_worker: diff_panel::DiffWorker::new(),
            diff_generation: 0,
            diff_in_flight: None,
            diff_refreshed_at: HashMap::new(),
            diff_dirty_hint: true,
            diff_last_project: None,
            editor_active: HashMap::new(),
            editor_selected: HashMap::new(),
            editor_carets: HashMap::new(),
            editor_pending_action: None,
            editor_rows_handles: HashMap::new(),
            editor_x_handles: HashMap::new(),
            editor_highlight_widths: HashMap::new(),
            editor_highlight_worker: editor::HighlightWorker::new(),
            editor_body_origins: HashMap::new(),
            editor_selecting: None,
            document_restore_queue: std::collections::VecDeque::new(),
            document_restore_in_flight: None,
            focus_epoch: 0,
        };
        view.start_ipc(cx);
        view.restore_or_initialize(cx);
        view.warm_history_journals();
        view.start_history_timer(cx);
        view.start_files_poller(cx);
        let (highlight_tx, highlight_rx) = async_channel::bounded(1);
        view.editor_highlight_worker = editor::HighlightWorker::with_notifier(move || {
            let _ = highlight_tx.try_send(());
        });
        cx.spawn(async move |weak: WeakEntity<Self>, cx: &mut AsyncApp| {
            while highlight_rx.recv().await.is_ok() {
                if weak
                    .update(cx, |view, cx| view.editor_drain_highlights(cx))
                    .is_err()
                {
                    break;
                }
            }
        })
        .detach();
        view
    }

    fn start_ipc(&mut self, cx: &mut Context<Self>) {
        let path = match IpcServer::default_socket_path() {
            Ok(path) => path,
            Err(error) => {
                self.persistence_warning = Some(format!("IPC unavailable: {error}"));
                return;
            }
        };
        let (sender, receiver) = async_channel::bounded::<IpcWork>(32);
        let handler: RequestHandler = Arc::new(move |request, deadline| {
            let request_id = request.request_id.clone();
            let (reply, response) = std::sync::mpsc::sync_channel(1);
            let cancelled = Arc::new(AtomicBool::new(false));
            let work = IpcWork {
                request,
                reply,
                deadline,
                cancelled: cancelled.clone(),
            };
            if sender.try_send(work).is_err() {
                return IpcResponse::failure(
                    request_id,
                    "timeout",
                    "IPC owner queue is full or closed",
                );
            }
            match response.recv_timeout(deadline.saturating_duration_since(Instant::now())) {
                Ok(result) => result,
                Err(_) => {
                    cancelled.store(true, Ordering::Release);
                    IpcResponse::failure(request_id, "timeout", "request outcome may be unknown")
                }
            }
        });
        let server = match IpcServer::bind(&path, handler) {
            Ok(server) => server,
            Err(error) => {
                self.persistence_warning = Some(format!("IPC unavailable: {error}"));
                return;
            }
        };
        if let Err(error) = self.coordinator.enable_credentials(&path) {
            self.persistence_warning = Some(format!("IPC credentials unavailable: {error}"));
            std::thread::spawn(move || {
                let mut server = server;
                let _ = server.shutdown();
            });
            return;
        }
        self.ipc_server = Some(server);
        self.ipc_receiver = Some(receiver.clone());
        cx.spawn(async move |weak: WeakEntity<Self>, cx: &mut AsyncApp| {
            while let Ok(work) = receiver.recv().await {
                if weak
                    .update(cx, |view, cx| view.handle_ipc_work(work, cx))
                    .is_err()
                {
                    break;
                }
            }
        })
        .detach();
    }

    fn handle_ipc_work(&mut self, work: IpcWork, cx: &mut Context<Self>) {
        if self.shutting_down
            || work.cancelled.load(Ordering::Acquire)
            || Instant::now() >= work.deadline
        {
            return;
        }
        let Some(context) = self.coordinator.authenticate(work.request.token.as_ref()) else {
            let _ = work.reply.send(IpcResponse::failure(
                work.request.request_id.clone(),
                "permission_denied",
                "missing or invalid credential",
            ));
            return;
        };
        let command = match ipc_bridge::map_request(&work.request, context, &self.coordinator) {
            Ok(command) => command,
            Err(error) => {
                let _ = work.reply.send(*error);
                return;
            }
        };
        let outcome = self.coordinator.dispatch_async(context, command);
        if let CommandResult::Ok(CommandOutput::Pending { operation_id }) = &outcome.result {
            self.ipc_pending.insert(*operation_id, work);
            self.ensure_launch_poller(cx);
        } else {
            self.apply_command_effects(outcome.effects, cx);
            let _ = work.reply.send(ipc_bridge::response(
                work.request.request_id.clone(),
                outcome.result,
            ));
        }
    }

    fn restore_or_initialize(&mut self, cx: &mut Context<Self>) {
        let outcome = self.persistence_store.as_ref().map(SnapshotStore::load);
        match outcome {
            Some(Ok(LoadOutcome::Valid(restored))) => self.restore_snapshot(restored, cx),
            Some(Ok(LoadOutcome::Missing)) | None => self.initialize_default(cx),
            Some(Ok(LoadOutcome::RecoveryRequired(error))) => {
                self.restore_recovery(error.to_string(), cx)
            }
            Some(Ok(LoadOutcome::ValidRecovery(restored, destination))) => {
                self.persistence_destination = destination;
                self.restore_snapshot(restored, cx);
            }
            Some(Ok(LoadOutcome::RecoveryUnavailable(error, destination))) => {
                self.persistence_destination = destination;
                self.persistence_warning = Some(format!("Recovery snapshot unavailable: {error}"));
                self.initialize_default(cx);
            }
            Some(Err(error)) => self.restore_recovery(error.to_string(), cx),
        }
    }

    fn restore_recovery(&mut self, primary_error: String, cx: &mut Context<Self>) {
        self.persistence_destination = SnapshotDestination::Recovery;
        self.persistence_warning = Some(format!(
            "Primary workspace snapshot needs recovery ({primary_error}); changes are saved separately."
        ));
        let recovery = self
            .persistence_store
            .as_ref()
            .map(SnapshotStore::load_recovery);
        match recovery {
            Some(Ok(LoadOutcome::Valid(restored))) => {
                self.persistence_destination = SnapshotDestination::Recovery;
                self.restore_snapshot(restored, cx);
            }
            Some(Ok(LoadOutcome::ValidRecovery(restored, destination))) => {
                self.persistence_destination = destination;
                self.restore_snapshot(restored, cx);
            }
            Some(Ok(LoadOutcome::Missing)) | None => {
                self.persistence_destination = SnapshotDestination::Recovery;
                self.initialize_default(cx);
            }
            Some(Ok(LoadOutcome::RecoveryRequired(error))) => {
                self.persistence_destination = self
                    .persistence_store
                    .as_ref()
                    .map(|store| SnapshotDestination::RecoveryFile(store.new_recovery_path()))
                    .unwrap_or(SnapshotDestination::Recovery);
                self.persistence_warning = Some(format!(
                    "Primary snapshot needs recovery ({primary_error}); recovery snapshot is also invalid ({error}). Changes remain in the recovery file."
                ));
                self.initialize_default(cx);
            }
            Some(Ok(LoadOutcome::RecoveryUnavailable(error, destination))) => {
                self.persistence_destination = destination;
                self.persistence_warning = Some(format!(
                    "Primary snapshot needs recovery ({primary_error}); no valid recovery snapshot exists ({error})."
                ));
                self.initialize_default(cx);
            }
            Some(Err(error)) => {
                self.persistence_destination = self
                    .persistence_store
                    .as_ref()
                    .map(|store| SnapshotDestination::RecoveryFile(store.new_recovery_path()))
                    .unwrap_or(SnapshotDestination::Recovery);
                self.persistence_warning = Some(format!(
                    "Primary snapshot needs recovery ({primary_error}); recovery snapshot could not be read ({error}). Changes remain in the recovery file."
                ));
                self.initialize_default(cx);
            }
        }
    }

    fn restore_snapshot(
        &mut self,
        restored: omaterm_state::ValidatedSnapshot,
        cx: &mut Context<Self>,
    ) {
        let pane_cwds: HashMap<PaneId, PersistedCwd> = restored.pane_cwds.into_iter().collect();
        self.observed_cwds = pane_cwds.clone();
        self.files_panel.restore(restored.expanded_dirs);
        // Force the watcher to re-arm on the restored selection.
        self.files_watcher = None;
        self.files_watched = None;
        if let Err(error) = self.coordinator.restore_window(restored.window) {
            self.persistence_destination = self
                .persistence_store
                .as_ref()
                .map(|store| SnapshotDestination::RecoveryFile(store.new_recovery_path()))
                .unwrap_or(SnapshotDestination::Recovery);
            self.persistence_warning = Some(format!(
                "Validated workspace could not be installed: {error}"
            ));
            self.initialize_default(cx);
            return;
        }
        let panes = self
            .coordinator
            .projects()
            .iter()
            .flat_map(|project| {
                let project_id = project.id;
                project.tabs.iter().flat_map(move |tab| {
                    tab.tree
                        .panes()
                        .into_iter()
                        .map(move |pane| (project_id, tab.id, pane.id))
                })
            })
            .collect::<Vec<_>>();
        for (project, tab, pane) in panes {
            let cwd = pane_cwds
                .get(&pane)
                .map(|cwd| cwd.path.clone())
                .unwrap_or_default();
            // Stage verified scrollback before the fresh shell spawns: the
            // router replays it into the committed engine ahead of any
            // reader output. Any outcome other than Ready starts the pane
            // empty (with a warning when history was expected).
            if let Some(manager) = self.coordinator.history_manager_mut() {
                let opaque = history::HistoryManager::opaque_name(pane.0.as_bytes());
                match manager.restore_scrollback(&opaque) {
                    history::RestoreOutcome::Ready(events) => {
                        self.coordinator.stage_restore_history(pane, events);
                    }
                    history::RestoreOutcome::Corrupt(warning)
                    | history::RestoreOutcome::Unavailable(warning) => {
                        self.persistence_warning = Some(warning);
                    }
                    history::RestoreOutcome::Disabled | history::RestoreOutcome::Missing => {}
                }
            }
            match self.dispatch_command(
                OmaCommand::Terminal(TerminalCommand::RestorePane {
                    project,
                    tab,
                    pane,
                    directory: cwd,
                }),
                cx,
            ) {
                Ok(_) => {}
                Err(error) => {
                    tracing::warn!(target: "omaterm::terminal", "restored pane {pane:?} shell failed: {error}");
                    self.persistence_warning =
                        Some(format!("Some restored terminals could not start: {error}"));
                    self.restored_failures
                        .insert(pane, (project, tab, error.to_string()));
                }
            }
        }
        self.observed_cwds = self.current_cwds();
        // Terminal workspace is installed first; document restores are then
        // scheduled one at a time so startup interaction is never blocked.
        self.schedule_restored_documents(restored.document_registries, cx);
    }

    /// Queue bounded document restores from validated schema-3 registries.
    /// Selection is recorded immediately (chips highlight) but the editor
    /// surface is only activated after a successful load, and only when the
    /// user has not taken a focus action after startup.
    fn schedule_restored_documents(
        &mut self,
        registries: Vec<(ProjectId, omaterm_state::DocumentRegistry)>,
        cx: &mut Context<Self>,
    ) {
        for (project, registry) in registries {
            if let Some(active) = registry.active_document {
                self.editor_selected.insert(project, active);
            }
            for descriptor in registry.documents {
                self.document_restore_queue.push_back(
                    router::DocumentRestoreRequest::from_descriptor(project, &descriptor),
                );
            }
        }
        self.schedule_next_document_restore(cx);
    }

    /// Start the next queued restore read, enforcing one-in-flight. Does
    /// nothing when a read is already pending or the queue is empty.
    fn schedule_next_document_restore(&mut self, cx: &mut Context<Self>) {
        if self.document_restore_in_flight.is_some() {
            return;
        }
        let Some(request) = self.document_restore_queue.pop_front() else {
            return;
        };
        let project = request.project;
        let document = request.document;
        let outcome = self.coordinator.schedule_document_restore(request);
        self.apply_command_effects(outcome.effects, cx);
        match outcome.result {
            CommandResult::Ok(CommandOutput::Pending { operation_id }) => {
                self.pending_ui_launches.insert(
                    operation_id,
                    PendingUiLaunch::EditorRestore { project, document },
                );
                self.document_restore_in_flight = Some(operation_id);
                self.ensure_launch_poller(cx);
            }
            // An immediately unavailable restore (missing/replaced root) still
            // advances the queue; its Retry/Close surface is rendered from the
            // store's placeholder.
            _ => {
                self.schedule_next_document_restore(cx);
            }
        }
    }

    fn initialize_default(&mut self, cx: &mut Context<Self>) {
        match self.dispatch_command(
            OmaCommand::Project(ProjectCommand::Create {
                name: None,
                // No explicit directory: the router falls back to the home
                // directory so fresh projects always start from $HOME.
                directory: None,
            }),
            cx,
        ) {
            Ok(CommandOutput::ProjectCreated { .. }) => {}
            Ok(_) => {}
            Err(error) => {
                tracing::error!(target: "omaterm::terminal", "failed to spawn initial shell: {error}");
                self.spawn_failure = Some((error.to_string(), SpawnRetry::Project(None)));
            }
        }
    }

    fn snapshot(&self) -> WorkspaceSnapshot {
        let registries = self
            .coordinator
            .export_document_registry(&self.editor_selected);
        WorkspaceSnapshot::capture_with_expanded_and_documents(
            self.coordinator.window(),
            &self.current_cwds(),
            &self.files_panel.expanded_snapshot(),
            &registries,
        )
    }

    fn current_cwds(&self) -> HashMap<PaneId, PersistedCwd> {
        let mut cwds = self.observed_cwds.clone();
        for project in self.coordinator.projects() {
            for tab in &project.tabs {
                for pane in tab.tree.panes() {
                    let PaneContent::Terminal(session_id) = pane.content else {
                        continue;
                    };
                    if let Some(session) = self.coordinator.registry().get(session_id)
                        && let Ok(mut session) = session.lock()
                    {
                        session.refresh_cwd_from_procfs();
                        let cwd = session.cwd();
                        let provenance = if let Some(saved) = cwds.get(&pane.id)
                            && saved.path == cwd.path
                        {
                            saved.provenance
                        } else if cwds
                            .get(&pane.id)
                            .is_some_and(|saved| saved.path != cwd.path && !saved.path.is_dir())
                        {
                            StoredCwdProvenance::FallbackHome
                        } else {
                            match cwd.provenance {
                                omaterm_terminal::CwdProvenance::Launch => {
                                    StoredCwdProvenance::Launch
                                }
                                omaterm_terminal::CwdProvenance::Osc7 => StoredCwdProvenance::Osc7,
                                omaterm_terminal::CwdProvenance::Procfs => {
                                    StoredCwdProvenance::Procfs
                                }
                            }
                        };
                        cwds.insert(
                            pane.id,
                            PersistedCwd {
                                path: cwd.path.clone(),
                                provenance,
                            },
                        );
                    }
                }
            }
        }
        cwds
    }

    fn detect_cwd_changes(&mut self, cx: &mut Context<Self>) {
        let current = self.current_cwds();
        if current != self.observed_cwds {
            self.observed_cwds = current;
            self.mark_persistence_dirty(cx);
        }
    }

    fn mark_persistence_dirty(&mut self, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
        }
        self.save_revision = self.save_revision.wrapping_add(1);
        let revision = self.save_revision;
        cx.spawn(async move |weak: WeakEntity<Self>, cx: &mut AsyncApp| {
            Timer::after(Duration::from_secs(2)).await;
            let _ = weak.update(cx, |view, _| {
                if view.save_revision == revision {
                    view.queue_snapshot();
                }
            });
        })
        .detach();
    }

    fn queue_snapshot(&mut self) {
        let Some(writer) = &self.persistence_writer else {
            return;
        };
        if let Err(error) = writer.submit(
            self.snapshot(),
            self.persistence_destination.clone(),
            self.save_revision,
        ) {
            tracing::error!(target: "omaterm::persistence", "workspace snapshot submission failed: {error}");
        }
        self.flush_history();
    }

    /// Owned history flush targets for every live terminal pane: workspace
    /// identity plus a registry handle per pane.
    fn history_flush_targets(&self) -> Vec<history::FlushTarget> {
        let mut targets = Vec::new();
        for project in self.coordinator.projects() {
            for tab in &project.tabs {
                for pane in tab.tree.panes() {
                    let omaterm_core::PaneContent::Terminal(session_id) = pane.content else {
                        continue;
                    };
                    let Some(handle) = self.coordinator.registry().get(session_id) else {
                        continue;
                    };
                    targets.push(history::FlushTarget {
                        pane_opaque: history::HistoryManager::opaque_name(pane.id.0.as_bytes()),
                        pane_uuid: *pane.id.0.as_bytes(),
                        project: Some(project.id.0.to_string()),
                        tab: Some(tab.id.0.to_string()),
                        pane_id: pane.id.0.to_string(),
                        handle,
                    });
                }
            }
        }
        targets
    }

    /// Drain session recorders and journals into the background history
    /// writer. Cheap when nothing changed; sessions gate capture on the
    /// same configuration this tick applies.
    fn flush_history(&mut self) {
        if !self
            .coordinator
            .history_manager()
            .is_some_and(|manager| manager.config().enabled)
        {
            return;
        }
        let targets = self.history_flush_targets();
        let Some(manager) = self.coordinator.history_manager_mut() else {
            return;
        };
        manager.flush_targets(targets);
        if let Some(warning) = manager.warning() {
            tracing::debug!(target: "omaterm::persistence", "history flush warning: {warning}");
        }
    }

    /// Warm in-memory journals from archives at startup so the first flush
    /// merges new records instead of discarding the persisted prefix.
    fn warm_history_journals(&mut self) {
        let panes: Vec<String> = self
            .coordinator
            .projects()
            .iter()
            .flat_map(|project| project.tabs.iter())
            .flat_map(|tab| tab.tree.panes())
            .map(|pane| history::HistoryManager::opaque_name(pane.id.0.as_bytes()))
            .collect();
        if let Some(manager) = self.coordinator.history_manager_mut() {
            manager.warm_journals(&panes);
        }
    }

    /// Periodic history persistence independent of workspace mutations, so
    /// quiet sessions still checkpoint. Started once with the window.
    fn start_history_timer(&mut self, cx: &mut Context<Self>) {
        cx.spawn(async move |weak: WeakEntity<Self>, cx: &mut AsyncApp| {
            loop {
                Timer::after(Duration::from_secs(30)).await;
                let done = weak.update(cx, |view, _| view.flush_history()).is_err();
                if done {
                    break;
                }
            }
        })
        .detach();
    }

    fn begin_shutdown(&mut self, window: gpui::AnyWindowHandle, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
        }
        if self.coordinator.documents().has_dirty_documents() {
            self.editor_pending_action = Some(EditorPendingAction::Shutdown(window));
            cx.notify();
            return;
        }
        self.shutting_down = true;
        let diff_thread = self.diff_worker.take_shutdown_thread();
        let highlight_thread = self.editor_highlight_worker.take_shutdown_thread();
        self.palette_search_worker.shutdown();
        if let Some(receiver) = self.ipc_receiver.take() {
            receiver.close();
        }
        for (id, work) in self.ipc_pending.drain() {
            self.coordinator.cancel_launch(id);
            self.coordinator.cancel_editor_operation(id);
            let _ = work.reply.send(IpcResponse::failure(
                work.request.request_id,
                "timeout",
                "desktop is shutting down",
            ));
        }
        let ipc_server = self.ipc_server.take();
        self.coordinator.cancel_launches();
        self.coordinator.cancel_editor_operations();
        self.pending_ui_launches.clear();
        self.pending_palette_mru.clear();
        self.pending_palette_origins.clear();
        self.save_revision = self.save_revision.wrapping_add(1);
        let revision = self.save_revision;
        let snapshot = self.snapshot();
        let destination = self.persistence_destination.clone();
        let writer = self.persistence_writer.take();
        let history_targets = self.history_flush_targets();
        let mut history = self.coordinator.take_history_manager();
        let ids = self.coordinator.registry().list();
        let handles = ids
            .into_iter()
            .filter_map(|id| self.coordinator.registry_mut().detach(id))
            .collect::<Vec<_>>();
        let (done_tx, done_rx) = async_channel::bounded::<Result<(), String>>(1);
        std::thread::spawn(move || {
            if let Some(thread) = diff_thread {
                let _ = thread.join();
            }
            if let Some(thread) = highlight_thread {
                let _ = thread.join();
            }
            if let Some(mut server) = ipc_server {
                let _ = server.shutdown();
            }
            // Encrypted history checkpoints before the final snapshot so a
            // restart restores both layout and scrollback. Bounded: slow or
            // locked key storage never blocks shutdown indefinitely.
            if let Some(manager) = history.as_mut()
                && !manager.shutdown_flush_targets(history_targets, Duration::from_secs(10))
            {
                tracing::warn!(target: "omaterm::persistence", "history shutdown flush incomplete; in-memory records discarded");
            }
            let save_result = writer.map_or(Ok(()), |writer| {
                writer.flush(snapshot, destination, revision)
            });
            let workers = handles
                .into_iter()
                .map(|handle| {
                    std::thread::spawn(move || {
                        if let Ok(mut session) = handle.lock() {
                            let _ = session.shutdown();
                        }
                    })
                })
                .collect::<Vec<_>>();
            for worker in workers {
                let _ = worker.join();
            }
            let _ = done_tx.send_blocking(save_result);
        });
        cx.spawn(async move |weak: WeakEntity<Self>, cx: &mut AsyncApp| {
            let result = done_rx
                .recv()
                .await
                .unwrap_or_else(|error| Err(error.to_string()));
            if let Err(error) = result {
                tracing::error!(
                    target: "omaterm::persistence",
                    "final workspace snapshot failed; terminal cleanup completed: {error}"
                );
                let _ = weak.update(cx, |view, cx| {
                    view.persistence_warning =
                        Some(format!("Final workspace save failed: {error}"));
                    cx.notify();
                });
            }
            let _ = window.update(cx, |_, window, _| window.remove_window());
        })
        .detach();
    }

    fn retry_restored_pane(&mut self, pane: PaneId, cx: &mut Context<Self>) {
        let Some((project, tab, _)) = self.restored_failures.get(&pane).cloned() else {
            return;
        };
        let cwd = self
            .observed_cwds
            .get(&pane)
            .map(|cwd| cwd.path.clone())
            .unwrap_or_default();
        match self.dispatch_command(
            OmaCommand::Terminal(TerminalCommand::RestorePane {
                project,
                tab,
                pane,
                directory: cwd,
            }),
            cx,
        ) {
            Ok(CommandOutput::Pending { .. }) => {}
            Ok(_) => {
                self.restored_failures.remove(&pane);
            }
            Err(error) => {
                if let Some((_, _, message)) = self.restored_failures.get_mut(&pane) {
                    *message = error.to_string();
                }
                cx.notify();
            }
        }
    }

    /// Publish a coordinator-owned session's initial snapshot and start its
    /// reader + poller. The session is already registered, so lookup never
    /// observes a half-created terminal.
    fn start_runtime(&mut self, cx: &mut Context<Self>, id: SessionId) {
        let snapshot = self
            .coordinator
            .registry()
            .get(id)
            .and_then(|s| s.lock().ok().map(|s| s.viewport()))
            .expect("coordinator-owned session must be registered");
        self.grid_sizes.insert(id, (snapshot.cols, snapshot.lines));
        self.snapshots.insert(id, snapshot);
        let (tx, rx) = async_channel::bounded::<TerminalViewport>(8);
        self.receivers.insert(id, rx.clone());
        let session = self
            .coordinator
            .registry()
            .get(id)
            .expect("just registered");
        Self::spawn_reader(session, tx);
        Self::spawn_poller(cx, id, rx);
    }

    /// Background thread: drain PTY output, forward snapshots when changed.
    fn spawn_reader(
        session: Arc<Mutex<TerminalSession>>,
        tx: async_channel::Sender<TerminalViewport>,
    ) {
        std::thread::spawn(move || {
            let master_fd = match session.lock() {
                Ok(session) => session.pty_fd(),
                Err(_) => return,
            };
            let mut last: Option<TerminalViewport> = None;
            loop {
                let readable = poll_fd_readable(master_fd, 200).unwrap_or(true);
                let (snapshot, exited) = {
                    let mut session = match session.lock() {
                        Ok(guard) => guard,
                        Err(_) => break,
                    };
                    if readable {
                        match session.pump() {
                            Ok((out, bytes_read)) => {
                                let exited = session.exited().is_some();
                                let viewport = if bytes_read > 0 || exited {
                                    Some(session.viewport())
                                } else {
                                    None
                                };
                                let _ = out;
                                (viewport, exited)
                            }
                            Err(_) => {
                                let exited = session.poll_child();
                                let viewport = if exited {
                                    Some(session.viewport())
                                } else {
                                    None
                                };
                                (viewport, exited)
                            }
                        }
                    } else {
                        if tx.is_closed() {
                            return;
                        }
                        let exited = session.poll_child();
                        let viewport = if exited {
                            Some(session.viewport())
                        } else {
                            None
                        };
                        (viewport, exited)
                    }
                };
                if let Some(viewport) = snapshot {
                    let changed = last.as_ref() != Some(&viewport);
                    if changed {
                        match tx.try_send(viewport.clone()) {
                            Ok(()) => last = Some(viewport),
                            Err(async_channel::TrySendError::Full(_)) => {}
                            Err(async_channel::TrySendError::Closed(_)) => break,
                        }
                    }
                }
                if exited {
                    break;
                }
            }
        });
    }

    /// Main-thread receiver for one session. Event-driven (`recv().await`
    /// parks the task), so idle terminals cost zero main-thread wakeups.
    /// Exits when the session is detached/closed or the view is released.
    fn spawn_poller(
        cx: &mut Context<Self>,
        session_id: SessionId,
        rx: async_channel::Receiver<TerminalViewport>,
    ) {
        cx.spawn(async move |weak: WeakEntity<Self>, cx: &mut AsyncApp| {
            loop {
                let viewport = rx.recv().await;
                let Ok(viewport) = viewport else {
                    let _ = weak.update(cx, |view, cx| {
                        let exited = view
                            .coordinator
                            .registry()
                            .get(session_id)
                            .map(|s| s.lock().map(|s| s.exited().is_some()).unwrap_or(false))
                            .unwrap_or(false);
                        if exited {
                            view.close_session(session_id, cx);
                            cx.notify();
                        }
                    });
                    break;
                };
                let done = weak
                    .update(cx, |view, cx| {
                        if !view.coordinator.registry().contains(session_id) {
                            return true;
                        }
                        view.snapshots.insert(session_id, viewport);
                        view.detect_cwd_changes(cx);
                        if view
                            .coordinator
                            .registry()
                            .get(session_id)
                            .map(|s| s.lock().map(|s| s.exited().is_some()).unwrap_or(false))
                            .unwrap_or(false)
                        {
                            // A shell exit is a pane-close event regardless
                            // of which pane currently has focus. The session
                            // ID is unique, so a delayed reader cannot close
                            // a newly created/reused pane.
                            view.close_session(session_id, cx);
                            return true;
                        }
                        cx.notify();
                        false
                    })
                    .unwrap_or(true);
                if done {
                    break;
                }
            }
        })
        .detach();
    }

    fn focused_session_id(&self) -> Option<SessionId> {
        self.coordinator.focused_session_id()
    }

    fn session_id_for_pane(&self, pane: PaneId) -> Option<SessionId> {
        self.coordinator.session_id_for_pane(pane)
    }

    fn dispatch_command(
        &mut self,
        command: OmaCommand,
        cx: &mut Context<Self>,
    ) -> Result<CommandOutput, omaterm_core::CommandError> {
        let launch = match &command {
            OmaCommand::Project(ProjectCommand::Create { directory, .. }) => {
                PendingUiLaunch::Project(directory.clone())
            }
            OmaCommand::Tab(TabCommand::Create { project, .. })
            | OmaCommand::Terminal(TerminalCommand::Create { project, .. }) => {
                PendingUiLaunch::Tab(*project)
            }
            OmaCommand::Terminal(TerminalCommand::RestorePane {
                project, tab, pane, ..
            }) => PendingUiLaunch::Restore {
                project: *project,
                tab: *tab,
                pane: *pane,
            },
            OmaCommand::Editor(EditorCommand::Open { project, .. }) => {
                PendingUiLaunch::EditorOpen(*project)
            }
            OmaCommand::Editor(EditorCommand::Save { .. }) => PendingUiLaunch::EditorSave,
            OmaCommand::Editor(EditorCommand::Revert { document }) => {
                PendingUiLaunch::EditorRevert(*document)
            }
            _ => PendingUiLaunch::Other,
        };
        let submitted_run = matches!(
            &command,
            OmaCommand::Terminal(TerminalCommand::RunCommand { .. })
        );
        let outcome = self
            .coordinator
            .dispatch_async(CommandContext::LocalUser, command);
        self.apply_command_effects(outcome.effects, cx);
        if let CommandResult::Ok(CommandOutput::Pending { operation_id }) = &outcome.result {
            self.pending_ui_launches.insert(*operation_id, launch);
            self.ensure_launch_poller(cx);
        }
        let output = outcome.result.output();
        // Post-`terminal.run` hint (M14): a submitted command may change
        // the worktree, so the next poller tick refreshes git status. The
        // hint never scrapes terminal text — it only re-runs `git status`.
        if output.is_ok() && submitted_run {
            self.git_dirty_hint = true;
            self.diff_dirty_hint = true;
        }
        output
    }

    fn ensure_launch_poller(&mut self, cx: &mut Context<Self>) {
        if self.launch_poller_active {
            return;
        }
        self.launch_poller_active = true;
        cx.spawn(async move |weak: WeakEntity<Self>, cx: &mut AsyncApp| {
            loop {
                Timer::after(Duration::from_millis(20)).await;
                let continuing = weak
                    .update(cx, |view, cx| view.finish_pending_launches(cx))
                    .unwrap_or(false);
                if !continuing {
                    break;
                }
            }
        })
        .detach();
    }

    fn apply_command_effects(
        &mut self,
        effects: Vec<router::CommandEffect>,
        cx: &mut Context<Self>,
    ) {
        for effect in effects {
            match effect {
                router::CommandEffect::SessionStarted(session) => self.start_runtime(cx, session),
                router::CommandEffect::SessionClosed(closed) => self.finish_close(closed),
                router::CommandEffect::PersistenceDirty => self.mark_persistence_dirty(cx),
                router::CommandEffect::WorkspaceChanged => cx.notify(),
                router::CommandEffect::FileOpened(project) => {
                    self.diff_panel.close_preview(project);
                    cx.notify();
                }
                router::CommandEffect::ProjectDirectoryChanged(project) => {
                    self.files_panel.clear_project(project);
                    self.diff_panel.invalidate_data(project);
                    if self.coordinator.selected_project_id() == Some(project) {
                        self.files_generation = self.files_generation.wrapping_add(1);
                        self.files_watcher = None;
                        self.files_watched = None;
                        self.files_arming = None;
                        self.files_last_resolve = Instant::now()
                            .checked_sub(Duration::from_secs(6))
                            .unwrap_or_else(Instant::now);
                        self.invalidate_palette_file_index();
                        self.diff_scroll_handles.clear();
                        self.apply_command_effects(
                            vec![router::CommandEffect::GitChanged(project)],
                            cx,
                        );
                        if self.ctrlp_open {
                            self.ctrlp_search(cx);
                        }
                    }
                    cx.notify();
                }
                router::CommandEffect::GitChanged(project) => {
                    self.diff_panel.invalidate_data(project);
                    self.diff_refreshed_at.remove(&(project, false));
                    self.diff_refreshed_at.remove(&(project, true));
                    if self.coordinator.selected_project_id() == Some(project) {
                        self.diff_generation = self.diff_generation.wrapping_add(1);
                        self.diff_worker.cancel();
                        self.diff_in_flight = None;
                        self.diff_dirty_hint = true;
                        self.git_generation = self.git_generation.wrapping_add(1);
                        self.git_in_flight = None;
                        self.git_dirty_hint = true;
                    }
                    cx.notify();
                }
            }
        }
    }

    fn finish_pending_launches(&mut self, cx: &mut Context<Self>) -> bool {
        if self.shutting_down {
            self.launch_poller_active = false;
            return false;
        }
        for (id, work) in &self.ipc_pending {
            if work.cancelled.load(Ordering::Acquire)
                || Instant::now() >= work.deadline
                || self
                    .coordinator
                    .authenticate(work.request.token.as_ref())
                    .is_none()
            {
                self.coordinator.cancel_launch(*id);
                self.coordinator.cancel_editor_operation(*id);
            }
        }
        for (operation_id, outcome) in self
            .coordinator
            .poll_launches()
            .into_iter()
            .chain(self.coordinator.poll_editor_operations())
        {
            if let Some(work) = self.ipc_pending.remove(&operation_id) {
                if !work.cancelled.load(Ordering::Acquire) {
                    let result = if self
                        .coordinator
                        .authenticate(work.request.token.as_ref())
                        .is_some()
                    {
                        ipc_bridge::response(work.request.request_id.clone(), outcome.result)
                    } else {
                        IpcResponse::failure(
                            work.request.request_id.clone(),
                            "permission_denied",
                            "credential expired or revoked",
                        )
                    };
                    let _ = work.reply.send(result);
                }
                self.apply_command_effects(outcome.effects, cx);
                continue;
            }
            let ui = self.pending_ui_launches.remove(&operation_id);
            let mut restore_completed = false;
            let palette_key = self.pending_palette_mru.remove(&operation_id);
            if matches!(&outcome.result, CommandResult::Ok(_))
                && let Some(key) = palette_key
            {
                self.palette_mru.retain(|existing| existing != &key);
                self.palette_mru.insert(0, key);
                self.palette_mru.truncate(100);
            }
            match (&outcome.result, ui) {
                (
                    CommandResult::Ok(CommandOutput::EditorOpened(info)),
                    Some(PendingUiLaunch::EditorOpen(project)),
                ) => {
                    self.input_notice = None;
                    self.editor_activate(project, info.document, cx);
                }
                (
                    CommandResult::Ok(CommandOutput::EditorSaved(_)),
                    Some(PendingUiLaunch::EditorSave),
                ) => {
                    self.input_notice = None;
                    self.show_toast("Saved".into(), cx);
                }
                (
                    CommandResult::Ok(CommandOutput::EditorOpened(_)),
                    Some(PendingUiLaunch::EditorRevert(document)),
                ) => {
                    self.input_notice = None;
                    if let Some(caret) = self.editor_carets.get(&document).copied() {
                        self.editor_set_caret(document, caret.cursor, false);
                    }
                    self.editor_after_edit(document, cx);
                    self.show_toast("Reverted to disk".into(), cx);
                }
                (CommandResult::Err(error), Some(PendingUiLaunch::EditorOpen(_))) => {
                    self.input_notice = Some(format!("Open: {error}"));
                    cx.notify();
                }
                (CommandResult::Err(error), Some(PendingUiLaunch::EditorSave)) => {
                    self.input_notice = Some(format!("Save: {error}"));
                    cx.notify();
                }
                (CommandResult::Err(error), Some(PendingUiLaunch::EditorRevert(_))) => {
                    self.input_notice = Some(format!("Revert: {error}"));
                    cx.notify();
                }
                (
                    CommandResult::Ok(CommandOutput::EditorOpened(info)),
                    Some(PendingUiLaunch::EditorRestore { project, document }),
                ) => {
                    restore_completed = true;
                    if self.document_restore_in_flight == Some(operation_id) {
                        self.document_restore_in_flight = None;
                    }
                    // Dedup may have converged on a live alias with a
                    // different id; selection follows the real buffer.
                    self.editor_selected.insert(project, info.document);
                    // Activate the restored surface only when this document is
                    // the saved active selection and the user has not taken a
                    // focus action since startup.
                    let is_saved_active = document == info.document;
                    let startup_launch = self.focus_epoch == 0;
                    if is_saved_active && startup_launch {
                        self.editor_activate(project, info.document, cx);
                    } else {
                        cx.notify();
                    }
                }
                (CommandResult::Err(_), Some(PendingUiLaunch::EditorRestore { .. })) => {
                    restore_completed = true;
                    if self.document_restore_in_flight == Some(operation_id) {
                        self.document_restore_in_flight = None;
                    }
                    cx.notify();
                }
                (CommandResult::Ok(_), Some(PendingUiLaunch::Restore { pane, .. })) => {
                    self.restored_failures.remove(&pane);
                }
                (
                    CommandResult::Err(error),
                    Some(PendingUiLaunch::Restore { project, tab, pane }),
                ) => {
                    self.restored_failures
                        .insert(pane, (project, tab, error.to_string()));
                    cx.notify();
                }
                (CommandResult::Err(error), Some(PendingUiLaunch::Project(directory))) => {
                    self.spawn_failure = Some((error.to_string(), SpawnRetry::Project(directory)));
                    cx.notify();
                }
                (CommandResult::Err(error), Some(PendingUiLaunch::Tab(project))) => {
                    self.spawn_failure = Some((error.to_string(), SpawnRetry::Tab(project)));
                    cx.notify();
                }
                (
                    CommandResult::Ok(_),
                    Some(PendingUiLaunch::Project(_) | PendingUiLaunch::Tab(_)),
                ) => {
                    self.spawn_failure = None;
                }
                (CommandResult::Err(error), _) => {
                    tracing::warn!(target: "omaterm::terminal", "terminal launch failed: {error}");
                }
                _ => {}
            }
            self.apply_command_effects(outcome.effects, cx);
            if restore_completed {
                self.schedule_next_document_restore(cx);
            }
        }
        let pending = self.coordinator.has_pending_launches()
            || self.coordinator.has_pending_editor_operations()
            || self.document_restore_in_flight.is_some()
            || !self.document_restore_queue.is_empty();
        if !pending {
            self.launch_poller_active = false;
        }
        pending
    }

    /// Drop per-session UI state after its pane is gone. The PTY/session
    /// handle itself is shut down by the caller off the UI thread.
    fn forget_session_state(&mut self, session_id: SessionId, pane: PaneId) {
        self.snapshots.remove(&session_id);
        self.grid_sizes.remove(&session_id);
        if let Some(rx) = self.receivers.remove(&session_id) {
            rx.close();
        }
        self.selections.remove(&session_id);
        self.scroll_indicator_until.remove(&session_id);
        self.grid_origins.remove(&pane);
        if self.selecting == Some(pane) {
            self.selecting = None;
        }
    }

    fn shutdown_session_bg(handle: Arc<Mutex<TerminalSession>>) {
        std::thread::spawn(move || {
            let reaped = handle
                .lock()
                .map(|mut session| session.shutdown())
                .unwrap_or(false);
            if !reaped {
                tracing::warn!(target: "omaterm::pty", "terminal child did not reap within the bounded shutdown");
            }
        });
    }

    fn split_focused(&mut self, direction: SplitDirection, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
        }
        if self.coordinator.focused().is_none() {
            // Empty workspace: a split key creates the first terminal.
            self.new_terminal_for_empty(cx);
            return;
        }
        let target = self.coordinator.focused().expect("checked focused pane");
        if let Err(error) = self.dispatch_command(
            OmaCommand::Pane(PaneCommand::Split { target, direction }),
            cx,
        ) {
            tracing::error!(target: "omaterm::pane", "split aborted: {error}");
        }
    }

    /// Ctrl+Shift+O: explicit two-step history opt-in/opt-out. Enabling
    /// discloses that terminal output and commands may contain secrets;
    /// disabling deletes all persisted history data.
    fn history_opt_in_key(&mut self, cx: &mut Context<Self>) {
        let enabled = self
            .coordinator
            .history_manager()
            .is_some_and(|manager| manager.config().enabled);
        self.confirm_or_arm(
            if enabled {
                HistoryArm::Disable
            } else {
                HistoryArm::Enable
            },
            cx,
        );
    }

    /// Ctrl+Shift+G: pause or resume capture for the focused pane.
    /// Immediate (non-destructive); the kept record is preserved.
    fn history_pause_key(&mut self, cx: &mut Context<Self>) {
        let Some(pane) = self.coordinator.focused() else {
            return;
        };
        let paused = self.coordinator.history_manager().is_some_and(|manager| {
            manager
                .config()
                .is_paused(&history::HistoryManager::opaque_name(pane.0.as_bytes()))
        });
        let command = if paused {
            OmaCommand::History(omaterm_core::HistoryCommand::ResumePane { pane })
        } else {
            OmaCommand::History(omaterm_core::HistoryCommand::PausePane { pane })
        };
        if let Err(error) = self.dispatch_command(command, cx) {
            tracing::warn!(target: "omaterm::persistence", "history pause toggle failed: {error}");
        }
    }

    /// Ctrl+Shift+X: two-step clear of the focused pane's persisted history.
    fn history_clear_key(&mut self, cx: &mut Context<Self>) {
        let Some(pane) = self.coordinator.focused() else {
            return;
        };
        self.confirm_or_arm(HistoryArm::ClearPane(pane), cx);
    }

    fn confirm_or_arm(&mut self, arm: HistoryArm, cx: &mut Context<Self>) {
        let confirmed = matches!(&self.history_arm, Some((pending, at))
            if pending == &arm && at.elapsed() < HISTORY_ARM_WINDOW);
        if confirmed {
            self.history_arm = None;
            self.execute_history_arm(arm, cx);
        } else {
            self.history_arm = Some((arm, Instant::now()));
            cx.notify();
        }
    }

    fn execute_history_arm(&mut self, arm: HistoryArm, cx: &mut Context<Self>) {
        use omaterm_core::HistoryCommand;
        let command = match arm {
            HistoryArm::Enable => OmaCommand::History(HistoryCommand::EnablePersistence),
            HistoryArm::Disable => OmaCommand::History(HistoryCommand::DisablePersistence),
            HistoryArm::ClearPane(pane) => OmaCommand::History(HistoryCommand::ClearPane { pane }),
        };
        if let Err(error) = self.dispatch_command(command, cx) {
            self.persistence_warning = Some(format!("History: {error}"));
        }
        cx.notify();
    }

    fn history_arm_text(arm: &HistoryArm) -> &'static str {
        match arm {
            HistoryArm::Enable => {
                "History records terminal output and commands, which may contain secrets. Press Ctrl+Shift+O again to enable encrypted history."
            }
            HistoryArm::Disable => {
                "Press Ctrl+Shift+O again to disable history and delete all persisted archives and journals."
            }
            HistoryArm::ClearPane(_) => {
                "Press Ctrl+Shift+X again to delete this pane's persisted history."
            }
        }
    }

    fn history_status_text(&self) -> Option<String> {
        let manager = self.coordinator.history_manager()?;
        let status = manager.status();
        if !status.enabled {
            return Some("history: off".into());
        }
        if status.warning.is_some() {
            return Some("history: attention".into());
        }
        if status.paused_panes > 0 {
            return Some(format!("history: on ({} paused)", status.paused_panes));
        }
        Some("history: on".into())
    }

    fn close_focused(&mut self, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
        }
        if let Some(pane) = self.coordinator.focused()
            && let Err(error) =
                self.dispatch_command(OmaCommand::Pane(PaneCommand::Close { pane }), cx)
        {
            tracing::warn!(target: "omaterm::pane", "pane close failed: {error}");
        }
    }

    fn close_session(&mut self, session_id: SessionId, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
        }
        let pane = self
            .coordinator
            .window()
            .projects
            .iter()
            .flat_map(|p| p.tabs.iter())
            .flat_map(|t| t.tree.panes())
            .find(|pane| pane.content == PaneContent::Terminal(session_id))
            .map(|pane| pane.id);
        if let Some(pane) = pane
            && let Err(error) =
                self.dispatch_command(OmaCommand::Pane(PaneCommand::Close { pane }), cx)
        {
            tracing::warn!(target: "omaterm::pty", "exited pane cleanup failed: {error}");
        }
    }

    fn finish_close(&mut self, closed: omaterm_terminal::ClosedPane) {
        self.restored_failures.remove(&closed.pane_id);
        self.coordinator.discard_restore_history(closed.pane_id);
        // Per-pane history follows pane lifetime, including panes closed
        // with their tab or project: every close path funnels through here.
        if let Some(manager) = self.coordinator.history_manager_mut() {
            let opaque = history::HistoryManager::opaque_name(closed.pane_id.0.as_bytes());
            if let Err(error) = manager.clear_pane(&opaque) {
                tracing::warn!(target: "omaterm::persistence", "history cleanup for closed pane failed: {error}");
            }
        }
        if let Some(session_id) = closed.session_id {
            self.coordinator.revoke_session(session_id);
            self.forget_session_state(session_id, closed.pane_id);
        }
        if let Some(handle) = closed.handle {
            // For a naturally exited shell, shutdown() observes the recorded
            // exit immediately and does not send a second signal.
            Self::shutdown_session_bg(handle);
        }
    }

    fn focus_neighbor(&mut self, direction: SplitDirection, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
        }
        let _ = self.dispatch_command(
            OmaCommand::Pane(PaneCommand::FocusDirection { direction }),
            cx,
        );
    }

    fn resize_focused(&mut self, amount: f32, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
        }
        if let Err(error) =
            self.dispatch_command(OmaCommand::Pane(PaneCommand::ResizeFocused { amount }), cx)
        {
            tracing::debug!(target: "omaterm::pane", "pane resize ignored: {error}");
        }
    }

    fn equalize(&mut self, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
        }
        let _ = self.dispatch_command(OmaCommand::Pane(PaneCommand::EqualizeSelected), cx);
    }

    fn new_terminal_for_empty(&mut self, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
        }
        if !self.coordinator.is_empty() {
            return;
        }
        let command = if self.coordinator.selected_project_id().is_some() {
            OmaCommand::Terminal(TerminalCommand::Create {
                project: self.coordinator.selected_project_id().unwrap(),
                directory: None,
            })
        } else {
            // Fresh projects start from the home directory (router fallback).
            OmaCommand::Project(ProjectCommand::Create {
                name: None,
                directory: None,
            })
        };
        if let Err(error) = self.dispatch_command(command, cx) {
            tracing::error!(target: "omaterm::terminal", "failed to spawn shell: {error}");
        }
    }

    fn create_project(&mut self, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
        }
        // Fresh projects start from the home directory: pass no directory
        // and let the router fall back to `home_directory()`.
        match self.dispatch_command(
            OmaCommand::Project(ProjectCommand::Create {
                name: None,
                directory: None,
            }),
            cx,
        ) {
            Ok(CommandOutput::ProjectCreated { .. }) => {
                self.spawn_failure = None;
            }
            Ok(_) => {}
            Err(error) => {
                tracing::error!(target: "omaterm::workspace", "failed to create project: {error}");
                self.spawn_failure = Some((error.to_string(), SpawnRetry::Project(None)));
                cx.notify();
            }
        }
    }

    /// Open a directory picker before creating a project. This is distinct
    /// from the keyboard New Project action, which intentionally starts at
    /// the default home directory.
    fn open_project_directory(&mut self, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
        }
        let receiver = cx.prompt_for_paths(PathPromptOptions {
            files: false,
            directories: true,
            multiple: false,
            prompt: Some("Open project directory".into()),
        });
        cx.spawn(async move |weak: WeakEntity<Self>, cx: &mut AsyncApp| {
            let result = receiver.await;
            let _ = weak.update(cx, |view, cx| {
                if view.shutting_down {
                    return;
                }
                match result {
                    Ok(Ok(Some(mut paths))) if !paths.is_empty() => {
                        let directory = paths.remove(0);
                        if let Err(error) = view.dispatch_command(
                            OmaCommand::Project(ProjectCommand::Create {
                                name: None,
                                directory: Some(directory),
                            }),
                            cx,
                        ) {
                            tracing::warn!(target: "omaterm::workspace", "project directory not opened: {error}");
                            view.persistence_warning =
                                Some(format!("Project directory not opened: {error}"));
                            cx.notify();
                        }
                    }
                    // Dialog cancellation is intentionally a no-op.
                    Ok(Ok(_)) => {}
                    Ok(Err(error)) => {
                        tracing::warn!(target: "omaterm::workspace", "folder picker unavailable: {error:#}");
                        view.persistence_warning =
                            Some(format!("Folder picker unavailable: {error:#}"));
                        cx.notify();
                    }
                    Err(_) => {}
                }
            });
        })
        .detach();
    }

    fn create_tab(&mut self, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
        }
        let Some(project) = self.coordinator.selected_project_id() else {
            self.create_project(cx);
            return;
        };
        match self.dispatch_command(
            OmaCommand::Tab(TabCommand::Create {
                project,
                name: None,
            }),
            cx,
        ) {
            Ok(CommandOutput::TabCreated { .. }) => {
                self.spawn_failure = None;
            }
            Ok(_) => {}
            Err(error) => {
                tracing::error!(target: "omaterm::workspace", "failed to create tab: {error}");
                self.spawn_failure = Some((error.to_string(), SpawnRetry::Tab(project)));
                cx.notify();
            }
        }
    }

    fn retry_spawn(&mut self, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
        }
        match self.spawn_failure.clone().map(|(_, retry)| retry) {
            Some(SpawnRetry::Project(directory)) => {
                match self.dispatch_command(
                    OmaCommand::Project(ProjectCommand::Create {
                        name: None,
                        directory: directory.clone(),
                    }),
                    cx,
                ) {
                    Ok(CommandOutput::ProjectCreated { .. }) => {
                        self.spawn_failure = None;
                    }
                    Ok(_) => {}
                    Err(error) => {
                        self.spawn_failure = Some((error.to_string(), SpawnRetry::Project(None)));
                        cx.notify();
                    }
                }
            }
            Some(SpawnRetry::Tab(project)) => self.create_tab_for_project(project, cx),
            None => {}
        }
    }

    fn create_tab_for_project(&mut self, project: omaterm_core::ProjectId, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
        }
        match self.dispatch_command(
            OmaCommand::Tab(TabCommand::Create {
                project,
                name: None,
            }),
            cx,
        ) {
            Ok(CommandOutput::TabCreated { .. }) => {
                self.spawn_failure = None;
            }
            Ok(_) => {}
            Err(error) => {
                self.spawn_failure = Some((error.to_string(), SpawnRetry::Tab(project)));
                cx.notify();
            }
        }
    }

    fn close_tab(
        &mut self,
        project: omaterm_core::ProjectId,
        tab: omaterm_core::TabId,
        cx: &mut Context<Self>,
    ) {
        if self.shutting_down {
            return;
        }
        let _ = project;
        let _ = self.dispatch_command(OmaCommand::Tab(TabCommand::Close { tab }), cx);
    }

    fn close_project(&mut self, project: omaterm_core::ProjectId, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
        }
        let _ = self.dispatch_command(OmaCommand::Project(ProjectCommand::Delete { project }), cx);
    }

    // ---- M13 file panel (contextual-sidebar tree + Ctrl+P finder) ----
    //
    // Every read goes through the same `OmaCommand::File` dispatcher arms
    // as IPC/CLI. The tree refreshes on project switch, expansion toggle,
    // debounced watcher events, and a 5s root re-resolve; only the `Ctrl+P`
    // keystroke search runs on a background thread (with a router-resolved
    // root), cancelled by generation.

    /// Resolve the selected project's filesystem root through the dispatcher.
    /// `None` is the explicit empty state (or a stale project), never an error.
    fn files_project_root(
        &mut self,
        project: ProjectId,
        cx: &mut Context<Self>,
    ) -> Option<std::path::PathBuf> {
        match self.dispatch_command(
            OmaCommand::Project(omaterm_core::ProjectCommand::Root { project }),
            cx,
        ) {
            Ok(CommandOutput::ProjectRoot(info)) => info.root,
            Ok(_) => None,
            Err(error) => {
                tracing::debug!(target: "omaterm::files", project_id = %project.0, "root resolve failed: {error}");
                None
            }
        }
    }

    /// One bounded directory listing through the dispatcher. Errors yield an
    /// empty envelope (missing dirs prune from the expansion set); the
    /// boundary code is preserved in tests, not shown as tree content.
    fn file_list_info(
        &mut self,
        project: ProjectId,
        dir: Option<std::path::PathBuf>,
        limit: Option<usize>,
        cx: &mut Context<Self>,
    ) -> omaterm_core::FileListInfo {
        match self.dispatch_command(
            OmaCommand::File(FileCommand::List {
                project,
                dir,
                limit,
            }),
            cx,
        ) {
            Ok(CommandOutput::FileList(list)) => list,
            Ok(_) => omaterm_core::FileListInfo {
                entries: Vec::new(),
                truncated: false,
            },
            Err(error) => {
                tracing::debug!(target: "omaterm::files", project_id = %project.0, "file list failed: {error}");
                omaterm_core::FileListInfo {
                    entries: Vec::new(),
                    truncated: false,
                }
            }
        }
    }

    fn refresh_process_list(&mut self, project: ProjectId, cx: &mut Context<Self>) {
        match self.dispatch_command(
            OmaCommand::Process(omaterm_core::ProcessCommand::List { project }),
            cx,
        ) {
            Ok(CommandOutput::ProcessList(info)) => self.process_list = Some((project, info)),
            Ok(_) => {
                self.input_notice = Some("Process query returned an unexpected result.".into())
            }
            Err(error) => self.input_notice = Some(format!("Process refresh: {error}")),
        }
        cx.notify();
    }

    /// Effective top-level cap for a project: the `Show more` paging
    /// override or the configured file default.
    fn files_root_cap(&self, project: ProjectId) -> usize {
        self.files_root_caps
            .get(&project)
            .copied()
            .unwrap_or_else(|| {
                omaterm_state::AppConfig::load()
                    .unwrap_or_default()
                    .resolved_max_results() as usize
            })
    }

    /// At most this many directory fetches run concurrently; the rest wait
    /// for the next poller tick (their rows show `loading` meanwhile).
    const MAX_FETCH_IN_FLIGHT: usize = 4;

    /// Rebuild the tree rows for the selected project and (re)arm the
    /// watcher on its root. Only the top level lists synchronously (one
    /// bounded walk so first paint never waits); expanded directories
    /// resolve from cache or fetch in the background. The top level honors
    /// the `Show more` cap; nested dirs use the config default.
    fn refresh_files(&mut self, cx: &mut Context<Self>) {
        let Some(project) = self.coordinator.selected_project_id() else {
            self.files_watcher = None;
            self.files_watched = None;
            return;
        };
        let Some(root) = self.files_project_root(project, cx) else {
            self.files_panel.refresh(project, true);
            self.files_watcher = None;
            self.files_watched = None;
            self.files_warning = None;
            cx.notify();
            return;
        };
        let root_cap = self.files_root_cap(project);
        let top = self.file_list_info(project, None, Some(root_cap), cx);
        self.files_panel
            .insert_listing(project, std::path::PathBuf::new(), top.entries);
        self.files_panel.set_root_truncated(top.truncated);
        self.sync_files_from_cache(project, &root);
        self.ensure_files_watcher(project, &root);
        cx.notify();
    }

    /// Rebuild rows purely from cache, then spawn background fetches for
    /// uncached expansions (bounded concurrency). Never touches the
    /// filesystem itself.
    fn sync_files_from_cache(&mut self, project: ProjectId, root: &std::path::Path) {
        self.files_panel.refresh(project, false);
        let in_flight = self.files_panel.pending_count(project);
        let slots = Self::MAX_FETCH_IN_FLIGHT.saturating_sub(in_flight);
        if slots == 0 {
            return;
        }
        let needed = self.files_panel.needed_dirs(project);
        if needed.is_empty() {
            return;
        }
        let canonical = std::fs::canonicalize(root).unwrap_or_else(|_| root.to_path_buf());
        let config = omaterm_state::AppConfig::load().unwrap_or_default();
        let limit = config.resolved_max_results() as usize;
        let show_hidden = config.show_hidden();
        for dir in needed.into_iter().take(slots) {
            self.files_panel.mark_pending(project, dir.clone());
            let tx = self.files_fetch_tx.clone();
            let generation = self.files_generation;
            let canonical = canonical.clone();
            std::thread::spawn(move || {
                let entries = omaterm_context::list_dir(&canonical, Some(&dir), limit, show_hidden)
                    .map(|list| list.entries)
                    .unwrap_or_default();
                let _ = tx.send((generation, project, dir, entries));
            });
        }
    }

    /// Watch exactly the selected project's root. Arming walks the whole
    /// tree, so it runs on a background thread keyed by generation — the UI
    /// thread never blocks, even on `$HOME`. Dropping the old handle
    /// cancels watching on project switch; limit exhaustion keeps the last
    /// good tree plus a warning banner.
    fn ensure_files_watcher(&mut self, project: ProjectId, root: &std::path::Path) {
        // Store the canonical root so watcher event paths (always
        // canonical) map back to tree-relative directories.
        let canonical = std::fs::canonicalize(root).unwrap_or_else(|_| root.to_path_buf());
        if self
            .files_watched
            .as_ref()
            .is_some_and(|(watched_project, watched_root)| {
                *watched_project == project && *watched_root == canonical
            })
            || self
                .files_arming
                .as_ref()
                .is_some_and(|(arming_project, arming_root)| {
                    *arming_project == project && *arming_root == canonical
                })
        {
            return;
        }
        self.files_watcher = None;
        self.files_watched = None;
        self.files_arming = Some((project, canonical.clone()));
        let hit = self.files_event.clone();
        let at = self.files_event_at.clone();
        let paths = self.files_event_paths.clone();
        let tx = self.files_arm_tx.clone();
        let generation = self.files_generation;
        let root = root.to_path_buf();
        // Skip our own state/config writes when they sit under the root —
        // their saves must never re-arm our own refresh.
        let mut extra_skips = Vec::new();
        if let Some(store) = self.persistence_store.as_ref()
            && let Some(parent) = store.path().parent()
            && let Ok(canonical_parent) = std::fs::canonicalize(parent)
        {
            extra_skips.push(canonical_parent);
        }
        if let Some(config) = omaterm_state::default_config_toml_path()
            && let Some(parent) = config.parent()
            && let Ok(canonical_parent) = std::fs::canonicalize(parent)
        {
            extra_skips.push(canonical_parent);
        }
        let config = omaterm_state::AppConfig::load().unwrap_or_default();
        let show_hidden = config.show_hidden();
        std::thread::spawn(move || {
            let started = Instant::now();
            let result = omaterm_context::FileWatcher::watch(
                &root,
                &extra_skips,
                show_hidden,
                move |event_paths| {
                    hit.store(true, Ordering::Release);
                    if let Ok(mut stamp) = at.lock() {
                        *stamp = Instant::now();
                    }
                    if let Ok(mut pending) = paths.lock() {
                        pending.extend(event_paths);
                        // Bound the backlog: a burst re-lists its dirs once each.
                        if pending.len() > 512 {
                            let excess = pending.len() - 512;
                            pending.drain(..excess);
                        }
                    }
                },
            );
            tracing::debug!(
                target: "omaterm::files",
                project_id = %project.0,
                elapsed_ms = started.elapsed().as_millis() as u64,
                armed = result.is_ok(),
                "watcher arming finished",
            );
            let _ = tx.send((generation, project, canonical, result));
        });
    }

    /// 250ms UI-thread poller: debounced watcher refresh, `Ctrl+P` result
    /// drain, and a 5s root re-resolve (pins and git toplevels move). One
    /// cheap task per window, like the history timer; exits with the view.
    fn start_files_poller(&mut self, cx: &mut Context<Self>) {
        if self.files_poller_active {
            return;
        }
        self.files_poller_active = true;
        cx.spawn(async move |weak: WeakEntity<Self>, cx: &mut AsyncApp| {
            loop {
                Timer::after(Duration::from_millis(250)).await;
                let alive = weak
                    .update(cx, |view, cx| view.files_tick(cx))
                    .unwrap_or(false);
                if !alive {
                    break;
                }
            }
        })
        .detach();
    }

    fn files_tick(&mut self, cx: &mut Context<Self>) -> bool {
        if self.shutting_down {
            return false;
        }
        // M14 status refresh rides the same 250ms poller: drains landed
        // workers and spawns at most one fetch per tick. M15 diff rides
        // along with the same one-fetch-per-tick bound per surface.
        let git_landed = self.git_tick(cx);
        self.diff_tick(cx);
        if self.ctrlp_open
            && (git_landed || self.coordinator.selected_project_id() != self.ctrlp_project)
        {
            self.ctrlp_search(cx);
        }
        // The latest-source worker owns one active request and one replaceable
        // pending request; each completion is tagged by query generation.
        if let Some(result) = self.palette_search_worker.take_result()
            && result.generation == self.ctrlp_generation
            && self.ctrlp_open
        {
            let query = self.ctrlp_query.trim_start_matches('>').trim();
            let (ranked, merged_truncated) = palette::rank_with_truncation(
                self.ctrlp_static_results
                    .iter()
                    .cloned()
                    .chain(result.entries),
                query,
                palette::MAX_PALETTE_RESULTS,
            );
            self.ctrlp_results = ranked;
            self.ctrlp_selected = 0;
            if !self.ctrlp_results.is_empty() {
                self.palette_scroll_handle
                    .scroll_to_item(0, ScrollStrategy::Top);
            }
            self.ctrlp_truncated |= result.truncated || merged_truncated;
            self.ctrlp_source_error = result.error;
            self.ctrlp_search_root = result.root;
            cx.notify();
        }
        let project = self.coordinator.selected_project_id();
        self.ctrlp_project = project;
        let switched = project != self.files_panel.rows_project_for_tick();
        let event_due = self.files_event.load(Ordering::Acquire)
            && self
                .files_event_at
                .lock()
                .is_ok_and(|stamp| stamp.elapsed() >= Duration::from_millis(250));
        if project.is_none() {
            if self.files_watched.is_some() || self.files_panel.rows_project_for_tick().is_some() {
                self.files_watcher = None;
                self.files_watched = None;
            }
            return true;
        }
        let project = project.expect("selected project");
        // Background directory-fetch completions: stale generations (from
        // before a switch/root change) drop; the rest land in cache and
        // rebuild rows with zero filesystem work on this thread. A landed
        // fetch can unblock deeper levels, so fetching continues until
        // nothing is needed (still bounded per tick).
        let mut landed_selected = false;
        while let Ok((generation, fetched_project, dir, entries)) = self.files_fetch_rx.try_recv() {
            if generation != self.files_generation {
                // Retired by a switch/root change: drop the payload and free
                // its in-flight slot so the fetch budget never leaks.
                self.files_panel.unpend(fetched_project, &dir);
                continue;
            }
            self.files_panel
                .insert_listing(fetched_project, dir, entries);
            if Some(fetched_project) == self.coordinator.selected_project_id() {
                landed_selected = true;
            }
        }
        // Background watcher-arm completions: stale generations drop (the
        // tick re-arms for whatever is current); otherwise install the
        // handle or surface the limit banner.
        while let Ok((generation, armed_project, armed_root, result)) = self.files_arm_rx.try_recv()
        {
            self.files_arming = None;
            if generation != self.files_generation {
                continue;
            }
            if Some(armed_project) != self.coordinator.selected_project_id() {
                continue;
            }
            match result {
                Ok(watcher) => {
                    self.files_watcher = Some(watcher);
                    self.files_watched = Some((armed_project, armed_root));
                    self.files_warning = None;
                    cx.notify();
                }
                Err(omaterm_context::WatchError::LimitExhausted(message)) => {
                    tracing::warn!(target: "omaterm::files", "file watcher limit exhausted: {message}");
                    self.files_warning = Some(
                        "Files: system watch limit reached — showing the last good tree.".into(),
                    );
                    cx.notify();
                }
                Err(omaterm_context::WatchError::Unavailable(message)) => {
                    tracing::debug!(target: "omaterm::files", "file watcher unavailable: {message}");
                }
            }
        }
        if landed_selected && let Some(project) = self.coordinator.selected_project_id() {
            self.files_panel.refresh(project, false);
            // Continue fetching independent of watcher health: re-resolve
            // the root (cheap for pinned projects) and sync from cache.
            if !self.files_panel.needed_dirs(project).is_empty()
                && let Some(root) = self.files_project_root(project, cx)
            {
                self.sync_files_from_cache(project, &root);
            }
            cx.notify();
        }
        if switched {
            self.files_event.store(false, Ordering::Release);
            self.files_last_resolve = Instant::now();
            self.files_scroll_rows = 0;
            self.files_scroll_remainder = 0.0;
            // New generation retires in-flight fetches; other projects'
            // caches drop so memory stays bounded by one project.
            self.files_generation = self.files_generation.wrapping_add(1);
            self.invalidate_palette_file_index();
            self.files_panel.unpend_all(project);
            self.files_arming = None;
            for other in self
                .coordinator
                .projects()
                .iter()
                .map(|candidate| candidate.id)
                .collect::<Vec<_>>()
            {
                if other != project {
                    self.files_panel.clear_project(other);
                }
            }
            self.refresh_files(cx);
            if self.ctrlp_open {
                self.ctrlp_search(cx);
            }
            return true;
        }
        // The 5s tick only re-resolves the root (pins and git toplevels
        // move); the tree rebuilds solely when the root actually changed.
        if self.files_last_resolve.elapsed() >= Duration::from_secs(5) {
            self.files_last_resolve = Instant::now();
            let root = self.files_project_root(project, cx);
            let changed = match (&root, &self.files_watched) {
                (Some(root), Some((watched_project, watched_root))) => {
                    let canonical = std::fs::canonicalize(root).unwrap_or_else(|_| root.clone());
                    *watched_project != project || *watched_root != canonical
                }
                (Some(_), None) => true,
                (None, _) => false,
            };
            if self.ctrlp_open && self.files_watched.is_none() && !changed {
                // Without a live watcher, periodically retire the cached
                // filename snapshot so palette results still converge.
                self.invalidate_palette_file_index();
                self.ctrlp_search(cx);
            }
            if changed {
                self.files_generation = self.files_generation.wrapping_add(1);
                self.invalidate_palette_file_index();
                self.files_panel.clear_project(project);
                self.files_arming = None;
                self.refresh_files(cx);
                if self.ctrlp_open {
                    self.ctrlp_search(cx);
                }
                return true;
            }
            // A selected project with no watcher and no arm in flight
            // re-arms here (at most once per 5s), so arming failures retry
            // instead of wedging the tree unwatched. `root` is already
            // resolved above — no extra subprocess.
            if self.files_watched.is_none()
                && self.files_arming.is_none()
                && let Some(root) = root
            {
                self.ensure_files_watcher(project, &root);
            }
        }
        // Debounced watcher events refresh their parent directories. The
        // batch path never re-resolves the root (the 5s tick owns root
        // moves) and coalesces to ~1Hz, so a busy filesystem can never
        // keep the UI thread hot.
        if event_due && self.files_last_event_refresh.elapsed() >= Duration::from_secs(1) {
            self.invalidate_palette_file_index();
            self.files_event.store(false, Ordering::Release);
            self.files_last_event_refresh = Instant::now();
            let paths = self
                .files_event_paths
                .lock()
                .map(|mut pending| std::mem::take(&mut *pending))
                .unwrap_or_default();
            let backlog_shed = paths.len();
            if backlog_shed >= 512 {
                tracing::warn!(
                    target: "omaterm::files",
                    backlog_shed,
                    "watcher event backlog shed; tree converges on next quiet tick",
                );
            }
            self.refresh_files_events(project, paths, cx);
            if self.ctrlp_open {
                self.ctrlp_search(cx);
            }
        }
        true
    }

    /// Targeted refresh for debounced watcher event paths (absolute,
    /// canonical). Each path invalidates its parent tree directory; rows
    /// rebuild purely from cache while background refetches converge.
    /// Deliberately no root re-resolve and no synchronous root re-walk
    /// here (the 5s tick owns root moves) — only the root level itself
    /// re-lists synchronously when directly affected, one bounded walk.
    /// Unknown prefixes fall back to a full rebuild.
    fn refresh_files_events(
        &mut self,
        project: ProjectId,
        paths: Vec<std::path::PathBuf>,
        cx: &mut Context<Self>,
    ) {
        let Some((_, watched_root)) = self.files_watched.clone() else {
            self.refresh_files(cx);
            return;
        };
        let mut dirs: Vec<std::path::PathBuf> = Vec::new();
        for path in &paths {
            let Ok(relative) = path.strip_prefix(&watched_root) else {
                self.refresh_files(cx);
                return;
            };
            let dir = relative
                .parent()
                .filter(|parent| !parent.as_os_str().is_empty())
                .map(|parent| parent.to_path_buf())
                .unwrap_or_else(std::path::PathBuf::new);
            if !dirs.contains(&dir) {
                dirs.push(dir);
            }
        }
        if dirs.is_empty() {
            return;
        }
        for dir in &dirs {
            self.files_panel.invalidate(project, dir);
        }
        // Top up watches for newly created directories (the pruned arming
        // walk cannot see the future); files and known dirs are cheap
        // no-ops inside `watch_single`.
        if let Some(watcher) = self.files_watcher.as_mut() {
            for path in &paths {
                if path.is_dir() {
                    // A failed top-up only delays coverage until the next
                    // batch; the row still refreshes through the listing.
                    let _ = watcher.watch_single(path);
                }
            }
        }
        // Only a directly-affected root re-lists synchronously (one bounded
        // walk); nested levels refetch in the background.
        if dirs.iter().any(|dir| dir.as_os_str().is_empty()) {
            let root_cap = self.files_root_cap(project);
            let top = self.file_list_info(project, None, Some(root_cap), cx);
            self.files_panel
                .insert_listing(project, std::path::PathBuf::new(), top.entries);
            self.files_panel.set_root_truncated(top.truncated);
        }
        self.files_panel.refresh(project, false);
        self.sync_files_from_cache(project, &watched_root);
        cx.notify();
        if self.ctrlp_open {
            self.ctrlp_search(cx);
        }
    }

    /// M14 git poller section: drain landed background refreshes (stale
    /// generations drop), then spawn one status worker when the selected
    /// project is new, dirty-hinted, or past its refresh interval. Only
    /// cheap clones happen on this thread (pinned dir, shell CWD path);
    /// root resolution and `git status` run on the worker.
    fn git_tick(&mut self, cx: &mut Context<Self>) -> bool {
        if self.shutting_down {
            return false;
        }
        let mut landed = false;
        while let Ok((generation, project, refresh)) = self.git_rx.try_recv() {
            if generation != self.git_generation {
                continue;
            }
            // Landed work ran off this thread by construction; pin it.
            debug_assert_ne!(refresh.worker, std::thread::current().id());
            if self.git_in_flight == Some(project) {
                self.git_in_flight = None;
            }
            self.git_panel.apply_refresh(project, refresh);
            self.git_refreshed_at.insert(project, Instant::now());
            landed = true;
        }
        if landed {
            cx.notify();
        }
        let Some(project) = self.coordinator.selected_project_id() else {
            return landed;
        };
        // A switch retires in-flight work (landings drop by generation)
        // and bounds memory to one project (files panel precedent).
        if self.git_last_project != Some(project) {
            self.git_last_project = Some(project);
            self.git_generation = self.git_generation.wrapping_add(1);
            self.git_in_flight = None;
            for other in self
                .coordinator
                .projects()
                .iter()
                .map(|candidate| candidate.id)
                .collect::<Vec<_>>()
            {
                if other != project {
                    self.git_panel.clear_project(other);
                    self.git_refreshed_at.remove(&other);
                }
            }
        }
        if self.git_in_flight.is_some() {
            return landed;
        }
        let config = omaterm_state::AppConfig::load().unwrap_or_default();
        let interval = Duration::from_secs(config.resolved_git_refresh_secs().clamp(1, 300));
        let known = self.git_panel.status_for(project).is_some()
            || self.git_panel.empty_for(project).is_some();
        if !git_panel::should_refresh(
            known,
            self.git_dirty_hint,
            self.git_refreshed_at.get(&project).copied(),
            interval,
            Instant::now(),
        ) {
            return landed;
        }
        let pinned = self.coordinator.pinned_for(project);
        let active_cwd = self.coordinator.shell_cwd_for(project);
        let limit = (config.resolved_max_results() as usize)
            .clamp(1, omaterm_core::validation::MAX_FILE_ENTRIES);
        let tx = self.git_tx.clone();
        git_panel::spawn_status_thread(
            std::thread::current().id(),
            project,
            self.git_generation,
            pinned,
            active_cwd,
            limit,
            tx,
        );
        self.git_in_flight = Some(project);
        self.git_dirty_hint = false;
        landed
    }

    /// M15 diff refresh on the same 250ms poller: drains landed workers
    /// and spawns at most one fetch per tick for the visible side
    /// (unstaged/staged toggle). Stale generations drop on project
    /// switch; other projects' caches clear to bound memory.
    fn diff_tick(&mut self, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
        }
        let current_project = self.coordinator.selected_project_id();
        let current_diff_key = current_project.map(|project| diff_panel::DiffRequestKey {
            generation: self.diff_generation,
            root_generation: self.files_generation,
            project,
            path: self.diff_panel.selected_file(project).cloned(),
            pinned_root: self.coordinator.pinned_for(project),
            active_cwd: self.coordinator.cached_shell_cwd_for(project),
            staged: self.diff_panel.show_staged(project),
            context_lines: 3,
        });
        let mut landed = false;
        while let Some(result) = self.diff_worker.take_result() {
            let key = result.key.clone();
            debug_assert_ne!(result.refresh.worker, std::thread::current().id());
            if !self.diff_panel.apply_current_refresh(
                result,
                self.diff_in_flight.as_ref(),
                current_diff_key.as_ref(),
            ) {
                continue;
            }
            self.diff_in_flight = None;
            self.diff_refreshed_at
                .insert((key.project, key.staged), Instant::now());
            landed = true;
        }
        if landed {
            cx.notify();
        }
        let Some(project) = self.coordinator.selected_project_id() else {
            self.diff_worker.cancel();
            self.diff_panel.retain_project(None);
            self.diff_scroll_handles.clear();
            self.diff_refreshed_at.clear();
            self.diff_in_flight = None;
            self.diff_last_project = None;
            return;
        };
        if self.diff_last_project != Some(project) {
            self.diff_last_project = Some(project);
            self.diff_generation = self.diff_generation.wrapping_add(1);
            self.diff_in_flight = None;
            self.diff_worker.cancel();
            self.diff_panel.retain_project(Some(project));
            self.diff_refreshed_at
                .retain(|(owner, _), _| *owner == project);
            self.diff_scroll_handles
                .retain(|(owner, _, _, _), _| *owner == project);
        }
        let staged = self.diff_panel.show_staged(project);
        let path = self.diff_panel.selected_file(project).cloned();
        let pinned_root = self.coordinator.pinned_for(project);
        let active_cwd = self.coordinator.cached_shell_cwd_for(project);
        let key = diff_panel::DiffRequestKey {
            generation: self.diff_generation,
            root_generation: self.files_generation,
            project,
            path: path.clone(),
            pinned_root,
            active_cwd,
            staged,
            context_lines: 3,
        };
        if self.diff_in_flight.as_ref() == Some(&key) {
            return;
        }
        let supersedes = self.diff_in_flight.is_some();
        let config = omaterm_state::AppConfig::load().unwrap_or_default();
        let interval = Duration::from_secs(config.resolved_git_refresh_secs().clamp(1, 300));
        let known = self.diff_panel.diff_for(project, staged).is_some()
            || self.diff_panel.empty_for(project, staged).is_some();
        if !supersedes
            && !git_panel::should_refresh(
                known,
                self.diff_dirty_hint,
                self.diff_refreshed_at.get(&(project, staged)).copied(),
                interval,
                Instant::now(),
            )
        {
            return;
        }
        self.diff_worker.submit(diff_panel::DiffSpawn {
            key: key.clone(),
            cancelled: Arc::new(AtomicBool::new(false)),
        });
        self.diff_in_flight = Some(key);
        self.diff_dirty_hint = false;
    }

    /// Stage one whole file through the dispatcher
    /// (same path as the Source Control panel and IPC/CLI), then hint
    /// both refreshers. Visible only for unstaged-side files.
    fn diff_stage_file(
        &mut self,
        project: ProjectId,
        path: std::path::PathBuf,
        cx: &mut Context<Self>,
    ) {
        if self.shutting_down {
            return;
        }
        match self.dispatch_command(
            OmaCommand::Git(GitCommand::Stage {
                project,
                paths: vec![path],
            }),
            cx,
        ) {
            Ok(_) => {
                self.git_dirty_hint = true;
                self.diff_dirty_hint = true;
                self.diff_panel.set_show_staged(project, true);
                cx.notify();
            }
            Err(error) => {
                self.input_notice = Some(format!("Stage: {error}"));
                cx.notify();
            }
        }
    }

    fn diff_stage_hunk(
        &mut self,
        project: ProjectId,
        path: std::path::PathBuf,
        hunk_id: u64,
        cx: &mut Context<Self>,
    ) {
        if self.shutting_down {
            return;
        }
        match self.dispatch_command(
            OmaCommand::Git(GitCommand::StageHunk {
                project,
                path,
                hunk_id,
            }),
            cx,
        ) {
            Ok(_) => {}
            Err(error) => self.input_notice = Some(format!("Stage Hunk: {error}")),
        }
        // Invalidate both cached comparisons, including after a stale request.
        self.diff_generation = self.diff_generation.wrapping_add(1);
        self.diff_worker.cancel();
        self.diff_panel.invalidate_data(project);
        self.diff_in_flight = None;
        self.diff_refreshed_at.remove(&(project, false));
        self.diff_refreshed_at.remove(&(project, true));
        self.git_dirty_hint = true;
        self.diff_dirty_hint = true;
        cx.notify();
    }

    fn reveal_current_diff_hunk(&mut self, project: ProjectId) {
        let staged = self.diff_panel.show_staged(project);
        let Some(path) = self.diff_panel.selected_file(project).cloned() else {
            return;
        };
        let mode = self.diff_panel.diff_mode(project);
        let Some(rows) = self.diff_panel.preview_rows_for(project, staged) else {
            return;
        };
        if let Some(row) = diff_panel::hunk_row_index(&rows, self.diff_panel.selected_hunk(project))
        {
            self.diff_scroll_handles
                .entry((project, staged, path, mode))
                .or_default()
                .rows
                .scroll_to_item(row, ScrollStrategy::Center);
        }
    }

    fn switch_diff_mode(&mut self, project: ProjectId, next: diff_panel::DiffMode) {
        let staged = self.diff_panel.show_staged(project);
        let path = self.diff_panel.selected_file(project).cloned();
        let previous_mode = self.diff_panel.diff_mode(project);
        let previous_rows = self.diff_panel.preview_rows_for(project, staged);
        let previous_offset = path
            .as_ref()
            .and_then(|path| {
                self.diff_scroll_handles
                    .get(&(project, staged, path.clone(), previous_mode))
            })
            .map(|scroll| f32::from(scroll.rows.0.borrow().base_handle.offset().y))
            .unwrap_or(0.0);
        self.diff_panel.set_diff_mode(project, next);
        if let (Some(path), Some(previous), Some(rows)) = (
            path,
            previous_rows,
            self.diff_panel.preview_rows_for(project, staged),
        ) {
            let offset = diff_panel::remap_preview_offset(&previous, &rows, previous_offset);
            self.diff_scroll_handles
                .entry((project, staged, path, next))
                .or_default()
                .rows
                .0
                .borrow()
                .base_handle
                .set_offset(gpui::point(px(0.0), px(offset)));
        }
    }

    fn stage_current_diff_hunk(&mut self, project: ProjectId, cx: &mut Context<Self>) {
        let staged = self.diff_panel.show_staged(project);
        let selected = self.diff_panel.selected_file(project).cloned();
        let hunk_index = self.diff_panel.selected_hunk(project);
        let target = selected.and_then(|path| {
            let file = self
                .diff_panel
                .diff_for(project, staged)?
                .files
                .iter()
                .find(|file| file.path == path)?;
            let hunk = file.hunks.get(hunk_index)?;
            diff_panel::can_stage_hunk(file, hunk, staged).then_some((path, hunk.id))
        });
        if let Some((path, id)) = target {
            self.diff_stage_hunk(project, path, id, cx);
        } else {
            self.input_notice = Some("Selected hunk cannot be staged.".into());
            cx.notify();
        }
    }

    fn copy_current_diff_hunk(&mut self, project: ProjectId, cx: &mut Context<Self>) {
        let staged = self.diff_panel.show_staged(project);
        let selected = self.diff_panel.selected_file(project);
        let hunk_index = self.diff_panel.selected_hunk(project);
        let text = selected.and_then(|path| {
            let file = self
                .diff_panel
                .diff_for(project, staged)?
                .files
                .iter()
                .find(|file| &file.path == path)?;
            if file.binary || file.truncated {
                return None;
            }
            let hunk = file.hunks.get(hunk_index)?;
            (!hunk.truncated).then(|| diff_panel::unified_hunk_text(hunk))
        });
        if let Some(text) = text {
            cx.write_to_clipboard(ClipboardItem::new_string(text));
            self.show_toast("Copied hunk".into(), cx);
        } else {
            self.input_notice = Some("Selected hunk cannot be copied completely.".into());
            cx.notify();
        }
    }

    /// Native editor documents (M19 Phase D). Activation is view-local per
    /// project like the diff preview, but buffers persist in the
    /// router-owned store across tab switches and preview changes. Only
    /// lifecycle/save travel through the dispatcher; keystrokes edit the
    /// store directly and never touch terminal sessions.
    /// Active document for a project, dropping handles whose document
    /// closed underneath (project delete retires buffers).
    fn editor_active_doc(&self, project: ProjectId) -> Option<DocumentId> {
        self.editor_active
            .get(&project)
            .copied()
            .filter(|doc| self.coordinator.documents().project_of(*doc) == Some(project))
    }

    fn editor_resolve_pending(&mut self, choice: u8, cx: &mut Context<Self>) {
        let Some(action) = self.editor_pending_action else {
            return;
        };
        if choice == 0 {
            self.editor_pending_action = None;
            cx.notify();
            return;
        }
        let documents = match action {
            EditorPendingAction::Close(_, document) | EditorPendingAction::Revert(document) => {
                vec![document]
            }
            EditorPendingAction::Shutdown(_) => self.coordinator.documents().dirty_documents(),
        };
        for document in documents {
            if choice == 1 {
                if let Err(error) =
                    self.dispatch_command(OmaCommand::Editor(EditorCommand::Save { document }), cx)
                {
                    self.input_notice =
                        Some(format!("Save: {error}. Action cancelled; buffer retained."));
                    cx.notify();
                    return;
                }
            } else {
                self.coordinator.documents_mut().discard_changes(document);
            }
        }
        self.editor_pending_action = None;
        match action {
            EditorPendingAction::Close(project, document) => {
                self.editor_close_document(project, document, cx)
            }
            EditorPendingAction::Revert(document) => {
                self.editor_after_edit(document, cx);
                if let Some(project) = self.coordinator.documents().project_of(document) {
                    self.editor_revert_document(project, document, cx);
                }
            }
            EditorPendingAction::Shutdown(window) => self.begin_shutdown(window, cx),
        }
        cx.notify();
    }

    /// Dispatch `EditorCommand::Open` and activate the document on success.
    /// Phase D trigger: palette file results with Ctrl+Enter/Ctrl+click.
    /// Failures surface as an input notice; no terminal is disturbed.
    fn editor_open_document(
        &mut self,
        project: ProjectId,
        path: std::path::PathBuf,
        cx: &mut Context<Self>,
    ) {
        if self.shutting_down {
            return;
        }
        self.user_focus_action();
        match self.dispatch_command(
            OmaCommand::Editor(EditorCommand::Open { project, path }),
            cx,
        ) {
            Ok(CommandOutput::EditorOpened(info)) => {
                self.input_notice = None;
                self.editor_activate(project, info.document, cx)
            }
            Ok(CommandOutput::Pending { .. }) => {}
            Ok(_) => {
                self.input_notice = Some("Open: unexpected editor result.".into());
                cx.notify();
            }
            Err(error) => {
                self.input_notice = Some(format!("Open: {error}"));
                cx.notify();
            }
        }
    }

    /// Record that the user took an explicit focus/general action. Any pending
    /// startup restore then declines to activate its saved document, so a late
    /// restore load can never steal focus from the user.
    fn user_focus_action(&mut self) {
        self.focus_epoch = self.focus_epoch.saturating_add(1);
    }

    /// Show a document: mark active, ensure caret/scroll state, request
    /// fresh highlights, and reveal the caret line.
    fn editor_activate(
        &mut self,
        project: ProjectId,
        document: DocumentId,
        cx: &mut Context<Self>,
    ) {
        self.editor_active.insert(project, document);
        self.editor_selected.insert(project, document);
        self.files_search_focused = false;
        self.git_panel.set_commit_focused(false);
        self.editor_carets.entry(document).or_default();
        self.editor_submit_highlight(document);
        self.editor_reveal_caret(document);
        cx.notify();
    }

    /// Queue a background tokenize of the live buffer. Generations retire
    /// superseded keystrokes; the worker cancels the active job on submit.
    fn editor_submit_highlight(&mut self, document: DocumentId) {
        let store = self.coordinator.documents();
        let (Some(snapshot), Some(language)) =
            (store.render_snapshot(document), store.language(document))
        else {
            return;
        };
        self.editor_highlight_widths.remove(&document);
        self.editor_highlight_worker
            .submit(editor::HighlightRequest {
                document,
                generation: snapshot.generation(),
                language,
                // The worker must own its request while the UI continues to
                // edit. Rendering itself retains the shared snapshot text.
                text: snapshot.text().to_owned(),
                cancelled: Arc::new(AtomicBool::new(false)),
            });
    }

    /// Apply landed highlights whose generation still matches the buffer.
    /// Called from the editor render path; notifies only on real updates.
    fn editor_drain_highlights(&mut self, cx: &mut Context<Self>) {
        let mut landed = false;
        while let Some(result) = self.editor_highlight_worker.take_result() {
            let document = result.document;
            let max_cols = result.max_cols();
            if self.coordinator.documents_mut().set_highlight(result) {
                self.editor_highlight_widths.insert(document, max_cols);
                landed = true;
            }
        }
        if landed {
            cx.notify();
        }
    }

    /// Forget view-local state for a document (close/project switch).
    /// Buffer lifetime is owned by the store, not this map.
    fn editor_forget_view(&mut self, document: DocumentId) {
        self.editor_carets.remove(&document);
        self.editor_rows_handles.remove(&document);
        self.editor_x_handles.remove(&document);
        self.editor_highlight_widths.remove(&document);
        self.editor_body_origins.remove(&document);
        if self
            .editor_selecting
            .is_some_and(|(selecting, _)| selecting == document)
        {
            self.editor_selecting = None;
        }
    }

    /// Close a document through the dispatcher. Dirty buffers refuse with
    /// an explicit notice — close never discards text implicitly.
    fn editor_close_document(
        &mut self,
        project: ProjectId,
        document: DocumentId,
        cx: &mut Context<Self>,
    ) {
        if self.shutting_down {
            return;
        }
        if self.coordinator.documents().is_dirty(document) == Some(true) {
            self.editor_pending_action = Some(EditorPendingAction::Close(project, document));
            cx.notify();
            return;
        }
        match self.dispatch_command(OmaCommand::Editor(EditorCommand::Close { document }), cx) {
            Ok(_) => {
                self.editor_forget_view(document);
                if self.editor_active.get(&project) == Some(&document) {
                    self.editor_active.remove(&project);
                }
                if self.editor_selected.get(&project) == Some(&document) {
                    self.editor_selected.remove(&project);
                }
                cx.notify();
            }
            Err(error) => {
                self.input_notice = Some(format!("Close: {error}"));
                cx.notify();
            }
        }
    }

    /// Retry a failed restore read. Reuses the persisted descriptor (id,
    /// lossless path bytes, captured root identity) and re-enqueues through the
    /// one-in-flight restore queue; the chip returns to loading.
    fn editor_retry_placeholder(
        &mut self,
        project: ProjectId,
        document: DocumentId,
        cx: &mut Context<Self>,
    ) {
        if self.shutting_down {
            return;
        }
        let Some(entry) = self.coordinator.documents().placeholder(document) else {
            return;
        };
        let request = router::DocumentRestoreRequest {
            project: entry.project(),
            document: entry.id(),
            path_bytes: entry.path_bytes().to_vec(),
            root_device: entry.root_identity().device,
            root_inode: entry.root_identity().inode,
        };
        let _ = project;
        // Resubmitting is idempotent for an existing reservation; the request
        // goes through the same bounded one-in-flight scheduler.
        self.document_restore_queue.push_front(request);
        self.schedule_next_document_restore(cx);
    }

    /// Close a metadata-only placeholder (no live buffer). Used by the
    /// unavailable chip's Close action.
    fn editor_close_placeholder(
        &mut self,
        project: ProjectId,
        document: DocumentId,
        cx: &mut Context<Self>,
    ) {
        if self.shutting_down {
            return;
        }
        if self.coordinator.close_document_placeholder(document) {
            if self.editor_selected.get(&project) == Some(&document) {
                self.editor_selected.remove(&project);
            }
            self.apply_command_effects(vec![router::CommandEffect::PersistenceDirty], cx);
            cx.notify();
        }
    }

    /// Save the active buffer through the dispatcher. Success toasts;
    /// conflicts and I/O failures surface as notices with the buffer kept
    /// dirty and intact.
    fn editor_save_document(
        &mut self,
        _project: ProjectId,
        document: DocumentId,
        cx: &mut Context<Self>,
    ) {
        if self.shutting_down {
            return;
        }
        match self.dispatch_command(OmaCommand::Editor(EditorCommand::Save { document }), cx) {
            Ok(CommandOutput::EditorSaved(_)) => {
                self.input_notice = None;
                self.show_toast("Saved".into(), cx);
            }
            Ok(CommandOutput::Pending { .. }) => {}
            Ok(_) => {
                self.input_notice = Some("Save: unexpected editor result.".into());
                cx.notify();
            }
            Err(error) => {
                self.input_notice = Some(format!("Save: {error}"));
                cx.notify();
            }
        }
    }

    /// Reload the buffer from disk, discarding unsaved changes. The caret
    /// clamps into the fresh text; highlights regenerate from it.
    fn editor_revert_document(
        &mut self,
        _project: ProjectId,
        document: DocumentId,
        cx: &mut Context<Self>,
    ) {
        if self.shutting_down {
            return;
        }
        if self.coordinator.documents().is_dirty(document) == Some(true) {
            self.editor_pending_action = Some(EditorPendingAction::Revert(document));
            cx.notify();
            return;
        }
        match self.dispatch_command(OmaCommand::Editor(EditorCommand::Revert { document }), cx) {
            Ok(CommandOutput::EditorOpened(_)) => {
                self.input_notice = None;
                if let Some(caret) = self.editor_carets.get(&document).copied() {
                    self.editor_set_caret(document, caret.cursor, false);
                }
                self.editor_after_edit(document, cx);
                self.show_toast("Reverted to disk".into(), cx);
            }
            Ok(CommandOutput::Pending { .. }) => {}
            Ok(_) => {
                self.input_notice = Some("Revert: unexpected editor result.".into());
                cx.notify();
            }
            Err(error) => {
                self.input_notice = Some(format!("Revert: {error}"));
                cx.notify();
            }
        }
    }

    /// Scroll the caret line into view (non-strict: no jump when visible).
    fn editor_reveal_caret(&mut self, document: DocumentId) {
        let (Some(snapshot), Some(caret)) = (
            self.coordinator.documents().render_snapshot(document),
            self.editor_carets.get(&document).copied(),
        ) else {
            return;
        };
        let (line, _) = snapshot.offset_to_line_col(caret.cursor);
        if let Some(handle) = self.editor_rows_handles.get(&document) {
            handle.scroll_to_item(line, ScrollStrategy::Center);
        }
    }

    /// Set the caret, extending the selection when requested. Offsets clamp
    /// into the buffer on char boundaries; the caret line is revealed.
    fn editor_set_caret(&mut self, document: DocumentId, offset: usize, extend: bool) {
        let Some(snapshot) = self.coordinator.documents().render_snapshot(document) else {
            return;
        };
        let mut offset = offset.min(snapshot.text().len());
        while offset > 0 && !snapshot.text().is_char_boundary(offset) {
            offset -= 1;
        }
        let caret = self.editor_carets.entry(document).or_default();
        if extend {
            if caret.anchor.is_none() {
                caret.anchor = Some(caret.cursor);
            }
            caret.cursor = offset;
            if caret.anchor == Some(caret.cursor) {
                caret.anchor = None;
            }
        } else {
            caret.collapse_to(offset);
        }
        caret.clamp(snapshot.text());
        self.editor_reveal_caret(document);
    }

    /// Shared post-edit bookkeeping: fresh highlights, caret reveal, repaint.
    fn editor_after_edit(&mut self, document: DocumentId, cx: &mut Context<Self>) {
        if let Some(snapshot) = self.coordinator.documents().render_snapshot(document) {
            self.editor_carets
                .entry(document)
                .or_default()
                .clamp(snapshot.text());
        }
        self.editor_submit_highlight(document);
        self.editor_reveal_caret(document);
        cx.notify();
    }

    /// Insert text at the caret, replacing any selection. Control chars
    /// (except newline/tab) are filtered so pasted terminal output cannot
    /// inject control sequences into the buffer.
    fn editor_insert_text(&mut self, document: DocumentId, text: &str, cx: &mut Context<Self>) {
        let clean: String = text
            .chars()
            .filter(|ch| !ch.is_control() || *ch == '\n' || *ch == '\t')
            .collect();
        if clean.is_empty() {
            return;
        }
        let caret = self
            .editor_carets
            .get(&document)
            .copied()
            .unwrap_or_default();
        let (start, end) = caret
            .selection_range()
            .unwrap_or((caret.cursor, caret.cursor));
        match self
            .coordinator
            .documents_mut()
            .apply_edit(document, start, end - start, &clean)
        {
            Ok(()) => {
                self.editor_carets
                    .entry(document)
                    .or_default()
                    .collapse_to(start + clean.len());
                self.editor_after_edit(document, cx);
            }
            Err(error) => {
                self.input_notice = Some(format!("Edit: {error}"));
                cx.notify();
            }
        }
    }

    /// Delete the selection; returns whether anything was deleted.
    fn editor_delete_selection(&mut self, document: DocumentId, cx: &mut Context<Self>) -> bool {
        let caret = self
            .editor_carets
            .get(&document)
            .copied()
            .unwrap_or_default();
        let Some((start, end)) = caret.selection_range() else {
            return false;
        };
        match self
            .coordinator
            .documents_mut()
            .apply_edit(document, start, end - start, "")
        {
            Ok(()) => {
                self.editor_carets
                    .entry(document)
                    .or_default()
                    .collapse_to(start);
                self.editor_after_edit(document, cx);
                true
            }
            Err(error) => {
                self.input_notice = Some(format!("Edit: {error}"));
                cx.notify();
                false
            }
        }
    }

    /// Map a window point to a buffer offset: body origin plus native
    /// scroll offsets, fixed 22px rows, then shaped-prefix binary search
    /// for the column. Wide/tab text maps through display columns.
    fn editor_offset_at_point(
        &mut self,
        document: DocumentId,
        position: gpui::Point<Pixels>,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> Option<usize> {
        let origin = self.editor_body_origins.get(&document)?.get();
        let scroll_y: f32 = self
            .editor_rows_handles
            .get(&document)
            .map(|handle| handle.0.borrow().base_handle.offset().y.into())
            .unwrap_or(0.0);
        let scroll_x: f32 = self
            .editor_x_handles
            .get(&document)
            .map(|handle| handle.offset().x.into())
            .unwrap_or(0.0);
        let snapshot = self.coordinator.documents().render_snapshot(document)?;
        let rel_y: f32 = (position.y - origin.y).into();
        let line = ((rel_y - scroll_y) / EDITOR_ROW_H).floor() as usize;
        let line = line.min(snapshot.line_count().saturating_sub(1));
        let line_range = snapshot.line_range(line)?;
        let line_start = line_range.start;
        let line_text = snapshot.line(line)?;
        let display = editor::display_line(line_text);
        let rel_x: f32 = (position.x - origin.x).into();
        let target_x = (rel_x - EDITOR_GUTTER_W - scroll_x).max(0.0);
        let mono = mono_family_for_chrome(&*cx, self.font_family.as_deref());
        let shaped = window.text_system().shape_line(
            SharedString::from(display.into_owned()),
            px(EDITOR_FONT_SIZE),
            &[TextRun {
                len: editor::display_line(line_text).len(),
                font: font(mono),
                color: rgb(crate::ui::theme::TEXT).into(),
                background_color: None,
                underline: None,
                strikethrough: None,
            }],
            None,
        );
        let best = shaped.closest_index_for_x(px(target_x));
        let buffer_col = editor::display_byte_to_buffer_col(line_text, best);
        let mut caret = editor::EditorCaret {
            cursor: line_start + buffer_col,
            anchor: None,
        };
        caret.clamp(snapshot.text());
        Some(caret.cursor)
    }

    /// Undo the newest buffer edit and restore its valid caret endpoint.
    fn editor_undo(&mut self, document: DocumentId, cx: &mut Context<Self>) {
        match self.coordinator.documents_mut().undo(document) {
            Ok(applied) => {
                if applied {
                    if let Some(offset) = self.coordinator.documents().history_cursor(document) {
                        self.editor_set_caret(document, offset, false);
                    }
                    self.editor_after_edit(document, cx);
                }
            }
            Err(error) => {
                self.input_notice = Some(format!("Undo: {error}"));
                cx.notify();
            }
        }
    }

    fn editor_redo(&mut self, document: DocumentId, cx: &mut Context<Self>) {
        match self.coordinator.documents_mut().redo(document) {
            Ok(applied) => {
                if applied {
                    if let Some(offset) = self.coordinator.documents().history_cursor(document) {
                        self.editor_set_caret(document, offset, false);
                    }
                    self.editor_after_edit(document, cx);
                }
            }
            Err(error) => {
                self.input_notice = Some(format!("Redo: {error}"));
                cx.notify();
            }
        }
    }

    /// Copy the selection, or the caret line when there is none.
    fn editor_copy(&mut self, document: DocumentId, cx: &mut Context<Self>) {
        let (Some(snapshot), Some(caret)) = (
            self.coordinator.documents().render_snapshot(document),
            self.editor_carets.get(&document).copied(),
        ) else {
            return;
        };
        let payload = match caret.selection_range() {
            Some((start, end)) => snapshot.text().get(start..end).unwrap_or("").to_owned(),
            None => {
                let (line, _) = snapshot.offset_to_line_col(caret.cursor);
                let range = snapshot.line_range(line).unwrap_or(0..0);
                let end = range
                    .end
                    .saturating_add(usize::from(range.end < snapshot.text().len()));
                snapshot
                    .text()
                    .get(range.start..end)
                    .unwrap_or("")
                    .to_owned()
            }
        };
        if !payload.is_empty() {
            cx.write_to_clipboard(ClipboardItem::new_string(payload));
        }
    }

    /// Cut the selection, or the caret line when there is none: copy to
    /// the clipboard first, then delete.
    fn editor_cut(&mut self, document: DocumentId, cx: &mut Context<Self>) {
        let caret = self
            .editor_carets
            .get(&document)
            .copied()
            .unwrap_or_default();
        if caret.selection_range().is_none() {
            let Some(snapshot) = self.coordinator.documents().render_snapshot(document) else {
                return;
            };
            let (line, _) = snapshot.offset_to_line_col(caret.cursor);
            let range = snapshot.line_range(line).unwrap_or(0..0);
            let start = range.start;
            let end = range
                .end
                .saturating_add(usize::from(range.end < snapshot.text().len()));
            if start < end {
                let payload = snapshot.text().get(start..end).unwrap_or("").to_owned();
                if self
                    .coordinator
                    .documents_mut()
                    .apply_edit(document, start, end - start, "")
                    .is_ok()
                {
                    cx.write_to_clipboard(ClipboardItem::new_string(payload));
                    self.editor_carets
                        .entry(document)
                        .or_default()
                        .collapse_to(start);
                    self.editor_after_edit(document, cx);
                }
            }
            return;
        }
        self.editor_copy(document, cx);
        self.editor_delete_selection(document, cx);
    }

    /// Pointer press on a code row: focus, place the caret, begin a drag.
    /// Shift+press extends the selection from the existing anchor.
    fn editor_mouse_down(
        &mut self,
        document: DocumentId,
        event: &MouseDownEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if self.shutting_down {
            return;
        }
        window.focus(&self.focus_handle);
        let Some(offset) = self.editor_offset_at_point(document, event.position, window, cx) else {
            return;
        };
        let caret = self.editor_carets.entry(document).or_default();
        if event.modifiers.shift {
            if caret.anchor.is_none() {
                caret.anchor = Some(caret.cursor);
            }
            caret.cursor = offset;
            if caret.anchor == Some(caret.cursor) {
                caret.anchor = None;
            }
        } else {
            caret.collapse_to(offset);
        }
        self.editor_selecting = Some((document, caret.anchor.unwrap_or(offset)));
        cx.notify();
    }

    /// Pointer drag: extend the selection from the press point.
    fn editor_mouse_move(
        &mut self,
        document: DocumentId,
        event: &MouseMoveEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let Some((selecting, anchor)) = self.editor_selecting else {
            return;
        };
        if selecting != document {
            return;
        }
        let Some(offset) = self.editor_offset_at_point(document, event.position, window, cx) else {
            return;
        };
        let caret = self.editor_carets.entry(document).or_default();
        caret.anchor = Some(anchor);
        caret.cursor = offset;
        if caret.anchor == Some(caret.cursor) {
            caret.anchor = None;
        }
        cx.notify();
    }

    /// Pointer release: end the drag, publishing non-empty selections to
    /// the primary clipboard like terminal drag selection.
    fn editor_mouse_up(
        &mut self,
        document: DocumentId,
        _event: &MouseUpEvent,
        cx: &mut Context<Self>,
    ) {
        let Some((selecting, _)) = self.editor_selecting else {
            return;
        };
        if selecting != document {
            return;
        }
        self.editor_selecting = None;
        let (Some(snapshot), Some(caret)) = (
            self.coordinator.documents().render_snapshot(document),
            self.editor_carets.get(&document).copied(),
        ) else {
            return;
        };
        if let Some((start, end)) = caret.selection_range()
            && let Some(payload) = snapshot.text().get(start..end)
            && !payload.is_empty()
        {
            cx.write_to_primary(ClipboardItem::new_string(payload.to_owned()));
        }
        cx.notify();
    }

    /// Keyboard while a document is active. Global chrome chords (palette,
    /// panel toggles, workspace splits, inspector inputs) are consumed
    /// before this point, so everything arriving here belongs to the
    /// editor — except nothing: unhandled keys are swallowed rather than
    /// forwarded to a terminal that does not own input.
    #[allow(clippy::too_many_lines)]
    fn on_editor_key(&mut self, event: &KeyDownEvent, cx: &mut Context<Self>) {
        let Some(project) = self.coordinator.selected_project_id() else {
            return;
        };
        let Some(document) = self.editor_active_doc(project) else {
            return;
        };
        let modifiers = &event.keystroke.modifiers;
        let key_name = event.keystroke.key.to_lowercase().replace('_', "");

        // Editing chords. Plain Ctrl+C/X/V/A/S/Z and Ctrl+Shift+Z / Ctrl+Y
        // are editor-owned here; the Ctrl+Shift terminal clipboard chords
        // keep their global behavior above.
        if modifiers.control && !modifiers.alt {
            match (modifiers.shift, key_name.as_str()) {
                (false, "s") => {
                    self.editor_save_document(project, document, cx);
                    return;
                }
                (false, "z") => {
                    self.editor_undo(document, cx);
                    return;
                }
                (true, "z") => {
                    self.editor_redo(document, cx);
                    return;
                }
                (false, "y") => {
                    self.editor_redo(document, cx);
                    return;
                }
                (false, "a") => {
                    if let Some(snapshot) = self.coordinator.documents().render_snapshot(document) {
                        let caret = self.editor_carets.entry(document).or_default();
                        caret.anchor = Some(0);
                        caret.cursor = snapshot.text().len();
                        cx.notify();
                    }
                    return;
                }
                (false, "c") => {
                    self.editor_copy(document, cx);
                    return;
                }
                (false, "x") => {
                    self.editor_cut(document, cx);
                    return;
                }
                (false, "v") => {
                    if let Some(paste) = cx
                        .read_from_clipboard()
                        .and_then(|item| item.text().map(|text| text.to_string()))
                    {
                        self.editor_insert_text(document, &paste, cx);
                    }
                    return;
                }
                (false, "home") => {
                    self.editor_set_caret(document, 0, false);
                    cx.notify();
                    return;
                }
                (false, "end") => {
                    if let Some(snapshot) = self.coordinator.documents().render_snapshot(document) {
                        let end = snapshot.text().len();
                        self.editor_set_caret(document, end, false);
                        cx.notify();
                    }
                    return;
                }
                (true, "home") => {
                    self.editor_set_caret(document, 0, true);
                    cx.notify();
                    return;
                }
                (true, "end") => {
                    if let Some(snapshot) = self.coordinator.documents().render_snapshot(document) {
                        let end = snapshot.text().len();
                        self.editor_set_caret(document, end, true);
                        cx.notify();
                    }
                    return;
                }
                _ => return,
            }
        }
        if modifiers.alt {
            return;
        }
        match key_name.as_str() {
            "escape" => {
                self.editor_active.remove(&project);
                cx.notify();
            }
            "enter" | "return" | "kpenter" => self.editor_insert_newline(document, cx),
            "backspace" => self.editor_backspace(document, cx),
            "delete" => self.editor_delete_forward(document, cx),
            "tab" => self.editor_insert_text(document, "    ", cx),
            "left" => self.editor_move_char(document, -1, modifiers.shift, cx),
            "right" => self.editor_move_char(document, 1, modifiers.shift, cx),
            "up" => self.editor_move_line(document, -1, modifiers.shift, cx),
            "down" => self.editor_move_line(document, 1, modifiers.shift, cx),
            "home" => self.editor_move_line_bound(document, true, modifiers.shift, cx),
            "end" => self.editor_move_line_bound(document, false, modifiers.shift, cx),
            "pageup" => self.editor_move_page(document, -1, cx),
            "pagedown" => self.editor_move_page(document, 1, cx),
            _ => {
                if let Some(text) = event.keystroke.key_char.as_ref() {
                    self.editor_insert_text(document, text, cx);
                }
            }
        }
    }

    /// Move one char, extending the selection with Shift.
    fn editor_move_char(
        &mut self,
        document: DocumentId,
        delta: i32,
        extend: bool,
        cx: &mut Context<Self>,
    ) {
        let (Some(snapshot), Some(caret)) = (
            self.coordinator.documents().render_snapshot(document),
            self.editor_carets.get(&document).copied(),
        ) else {
            return;
        };
        // A selection collapses toward the motion before stepping.
        let base = match caret.selection_range() {
            Some((start, end)) if !extend => {
                if delta < 0 {
                    start
                } else {
                    end
                }
            }
            _ => caret.cursor,
        };
        let mut offset = base;
        if caret.selection_range().is_some() && !extend {
            self.editor_set_caret(document, base, false);
            cx.notify();
            return;
        }
        if delta < 0 {
            for _ in delta..0 {
                if offset == 0 {
                    break;
                }
                offset = editor::previous_grapheme(snapshot.text(), offset);
            }
        } else {
            for _ in 0..delta {
                if offset >= snapshot.text().len() {
                    break;
                }
                offset = editor::next_grapheme(snapshot.text(), offset);
            }
        }
        self.editor_set_caret(document, offset, extend);
        cx.notify();
    }

    /// Move vertically, preserving the visual column across tabs/Unicode.
    fn editor_move_line(
        &mut self,
        document: DocumentId,
        delta: i32,
        extend: bool,
        cx: &mut Context<Self>,
    ) {
        let (Some(snapshot), Some(caret)) = (
            self.coordinator.documents().render_snapshot(document),
            self.editor_carets.get(&document).copied(),
        ) else {
            return;
        };
        let (line, col) = snapshot.offset_to_line_col(caret.cursor);
        let next = (line as i32 + delta).clamp(0, snapshot.line_count() as i32 - 1) as usize;
        let line_start = snapshot.line_range(line).map_or(0, |range| range.start);
        let visual_col = editor::line_display_width(&snapshot.text()[line_start..line_start + col]);
        let next_line = snapshot.line(next).unwrap_or("");
        let next_col = editor::display_col_to_buffer_col(next_line, visual_col);
        let offset = snapshot.line_col_to_offset(next, next_col);
        self.editor_set_caret(document, offset, extend);
        cx.notify();
    }

    /// Line start/end, extending with Shift.
    fn editor_move_line_bound(
        &mut self,
        document: DocumentId,
        home: bool,
        extend: bool,
        cx: &mut Context<Self>,
    ) {
        let (Some(snapshot), Some(caret)) = (
            self.coordinator.documents().render_snapshot(document),
            self.editor_carets.get(&document).copied(),
        ) else {
            return;
        };
        let (line, _) = snapshot.offset_to_line_col(caret.cursor);
        let offset = if home {
            snapshot.line_range(line).map_or(0, |range| range.start)
        } else {
            snapshot.line_range(line).map_or(0, |range| range.end)
        };
        self.editor_set_caret(document, offset, extend);
        cx.notify();
    }

    /// Page motion: twenty lines, caret-centered reveal.
    fn editor_move_page(&mut self, document: DocumentId, delta: i32, cx: &mut Context<Self>) {
        self.editor_move_line(document, delta * 20, false, cx);
    }

    /// Enter: replace any selection, then newline plus the current line's
    /// leading whitespace (spaces and tabs preserved verbatim).
    fn editor_insert_newline(&mut self, document: DocumentId, cx: &mut Context<Self>) {
        let (Some(snapshot), Some(caret)) = (
            self.coordinator.documents().render_snapshot(document),
            self.editor_carets.get(&document).copied(),
        ) else {
            return;
        };
        let (start, end) = caret
            .selection_range()
            .unwrap_or((caret.cursor, caret.cursor));
        let (line, _) = snapshot.offset_to_line_col(start);
        let line_start = snapshot.line_range(line).map_or(0, |range| range.start);
        let line_text = snapshot
            .text()
            .get(line_start..start.min(snapshot.text().len()))
            .unwrap_or("");
        let indent: String = line_text
            .chars()
            .take_while(|ch| *ch == ' ' || *ch == '\t')
            .collect();
        let insert = format!("\n{indent}");
        match self
            .coordinator
            .documents_mut()
            .apply_edit(document, start, end - start, &insert)
        {
            Ok(()) => {
                self.editor_carets
                    .entry(document)
                    .or_default()
                    .collapse_to(start + insert.len());
                self.editor_after_edit(document, cx);
            }
            Err(error) => {
                self.input_notice = Some(format!("Edit: {error}"));
                cx.notify();
            }
        }
    }

    /// Backspace: delete the selection, join with the previous line at
    /// column zero, or delete the previous char.
    fn editor_backspace(&mut self, document: DocumentId, cx: &mut Context<Self>) {
        if self.editor_delete_selection(document, cx) {
            return;
        }
        let (Some(snapshot), Some(caret)) = (
            self.coordinator.documents().render_snapshot(document),
            self.editor_carets.get(&document).copied(),
        ) else {
            return;
        };
        if caret.cursor == 0 {
            return;
        }
        let start = editor::previous_grapheme(snapshot.text(), caret.cursor);
        match self
            .coordinator
            .documents_mut()
            .apply_edit(document, start, caret.cursor - start, "")
        {
            Ok(()) => {
                self.editor_carets
                    .entry(document)
                    .or_default()
                    .collapse_to(start);
                self.editor_after_edit(document, cx);
            }
            Err(error) => {
                self.input_notice = Some(format!("Edit: {error}"));
                cx.notify();
            }
        }
    }

    /// Delete forward: selection, line join at end of line, or next char.
    fn editor_delete_forward(&mut self, document: DocumentId, cx: &mut Context<Self>) {
        if self.editor_delete_selection(document, cx) {
            return;
        }
        let (Some(snapshot), Some(caret)) = (
            self.coordinator.documents().render_snapshot(document),
            self.editor_carets.get(&document).copied(),
        ) else {
            return;
        };
        if caret.cursor >= snapshot.text().len() {
            return;
        }
        let end = editor::next_grapheme(snapshot.text(), caret.cursor);
        match self.coordinator.documents_mut().apply_edit(
            document,
            caret.cursor,
            end - caret.cursor,
            "",
        ) {
            Ok(()) => self.editor_after_edit(document, cx),
            Err(error) => {
                self.input_notice = Some(format!("Edit: {error}"));
                cx.notify();
            }
        }
    }

    /// Render one open document: breadcrumb header, virtualized code rows
    /// with gutter + token/selection highlights + caret, and a status
    /// footer. Only visible rows enter the element tree; horizontal reach
    /// comes from the measured max display width.
    fn render_editor(
        &mut self,
        project: ProjectId,
        document: DocumentId,
        main_view_width: f32,
        _window: &mut Window,
        cx: &mut Context<Self>,
    ) -> Div {
        self.editor_drain_highlights(cx);
        let Some(snapshot) = self.coordinator.documents().render_snapshot(document) else {
            return div().child("Document closed");
        };
        let path = self.coordinator.documents().relative_path(document);
        let dirty = self
            .coordinator
            .documents()
            .is_dirty(document)
            .unwrap_or(false);
        let language = self
            .coordinator
            .documents()
            .language(document)
            .unwrap_or(omaterm_context::EditorLanguage::Plain);
        let caret = self
            .editor_carets
            .get(&document)
            .copied()
            .unwrap_or_default();
        let selection = caret.selection_range();
        let mono = mono_family_for_chrome(&*cx, self.font_family.as_deref());
        let cell_width: f32 = self.fonts(&*cx).cell_width.into();
        let max_cols = self
            .editor_highlight_widths
            .get(&document)
            .copied()
            .unwrap_or_default();
        let row_width = main_view_width.max(max_cols as f32 * cell_width + EDITOR_GUTTER_W + 16.0);

        // Header: breadcrumb path, dirty marker, document actions.
        let mut bar = div()
            .flex()
            .flex_col()
            .flex_1()
            .min_w(px(0.0))
            .min_h(px(0.0));
        let filename = path
            .as_ref()
            .and_then(|path| {
                path.file_name()
                    .map(|name| name.to_string_lossy().into_owned())
            })
            .unwrap_or_else(|| "untitled".into());
        let parent = path
            .as_ref()
            .and_then(|path| {
                path.parent()
                    .map(|parent| parent.to_string_lossy().into_owned())
            })
            .unwrap_or_default();
        let mut header = div()
            .flex()
            .flex_row()
            .items_center()
            .gap_2()
            .px_3()
            .h(px(36.0))
            .flex_shrink_0()
            .border_b_1()
            .border_color(rgb(crate::ui::theme::BORDER))
            .bg(rgb(crate::ui::theme::PANEL))
            .text_size(px(10.0))
            .text_color(rgb(crate::ui::theme::MUTED));
        if !parent.is_empty() {
            header = header.child(parent).child("›");
        }
        header = header
            .child(
                div()
                    .text_color(rgb(crate::ui::theme::TEXT2))
                    .child(filename),
            )
            .child(div().flex_1());
        if dirty {
            header = header.child(div().text_color(rgb(crate::ui::theme::YELLOW)).child("M"));
        }
        for (label, action) in [("Save", 0u8), ("Revert", 1u8), ("Close", 2u8)] {
            header = header.child(
                div()
                    .cursor_pointer()
                    .px_2()
                    .py_1()
                    .rounded_sm()
                    .text_color(rgb(crate::ui::theme::TEXT2))
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(move |view, _, window, cx| {
                            cx.stop_propagation();
                            window.focus(&view.focus_handle);
                            match action {
                                0 => view.editor_save_document(project, document, cx),
                                1 => view.editor_revert_document(project, document, cx),
                                _ => view.editor_close_document(project, document, cx),
                            }
                        }),
                    )
                    .child(label),
            );
        }
        bar = bar.child(header);

        // Body: origin recorder behind natively scrolled virtual rows.
        let origin = self
            .editor_body_origins
            .entry(document)
            .or_insert_with(|| {
                Rc::new(Cell::new(gpui::Point {
                    x: px(0.0),
                    y: px(0.0),
                }))
            })
            .clone();
        let rows_handle = self
            .editor_rows_handles
            .entry(document)
            .or_default()
            .clone();
        let x_handle = self.editor_x_handles.entry(document).or_default().clone();
        let snapshot = Arc::new(snapshot);
        let row_mono = mono.clone();
        let (caret_line, caret_col) = snapshot.offset_to_line_col(caret.cursor);
        let rows = uniform_list(
            "editor-lines",
            snapshot.line_count(),
            cx.processor({
                let snapshot = Arc::clone(&snapshot);
                move |_view, range: std::ops::Range<usize>, window, _cx| {
                    range
                        .map(|line| {
                            let line_range = snapshot.line_range(line).unwrap_or(0..0);
                            let line_start = line_range.start;
                            let line_end = line_range.end;
                            let line_text = snapshot.line(line).unwrap_or("");
                            let display = editor::display_line(line_text);
                            // Token + selection highlights merged into
                            // non-overlapping segments (no cascade ambiguity).
                            let mut cuts = vec![0usize, display.len()];
                            let mut token_at: Vec<(usize, usize, editor::TokenKind)> = Vec::new();
                            for span in snapshot.tokens_for_line(line) {
                                let end = span.start.saturating_add(span.len);
                                if end <= line_start || span.start >= line_end {
                                    continue;
                                }
                                let (from, to) = editor::buffer_range_to_display_range(
                                    line_text,
                                    span.start.saturating_sub(line_start),
                                    end.saturating_sub(line_start),
                                );
                                if from < to {
                                    token_at.push((from, to, span.kind));
                                    cuts.push(from);
                                    cuts.push(to);
                                }
                            }
                            // Selection in display coordinates for this row.
                            let sel_display = selection.and_then(|(sel_start, sel_end)| {
                                if sel_end > line_start && sel_start < line_end {
                                    let (from, to) = editor::buffer_range_to_display_range(
                                        line_text,
                                        sel_start.saturating_sub(line_start),
                                        sel_end.saturating_sub(line_start),
                                    );
                                    (from < to).then_some((from, to))
                                } else {
                                    None
                                }
                            });
                            if let Some((from, to)) = sel_display {
                                cuts.push(from);
                                cuts.push(to);
                            }
                            cuts.sort_unstable();
                            cuts.dedup();
                            let mut styles = Vec::new();
                            for window_ in cuts.windows(2) {
                                let (from, to) = (window_[0], window_[1]);
                                if from >= to {
                                    continue;
                                }
                                let in_selection = sel_display.is_some_and(|(sel_from, sel_to)| {
                                    from < sel_to && to > sel_from
                                });
                                let fg = token_at
                                    .iter()
                                    .find_map(|(tok_from, tok_to, kind)| {
                                        (*tok_from <= from && from < *tok_to).then_some(*kind)
                                    })
                                    .map(Self::editor_token_color)
                                    .unwrap_or(crate::ui::theme::TEXT);
                                styles.push((
                                    from..to,
                                    HighlightStyle {
                                        color: Some(rgb(fg).into()),
                                        background_color: if in_selection {
                                            Some(hsla(0.591, 0.92, 0.578, 0.35))
                                        } else {
                                            None
                                        },
                                        ..Default::default()
                                    },
                                ));
                            }
                            let mut row = div()
                                .id(line)
                                .relative()
                                .h(px(EDITOR_ROW_H))
                                .flex_shrink_0()
                                .flex()
                                .flex_row()
                                .items_center()
                                .font_family(row_mono.clone())
                                .text_size(px(EDITOR_FONT_SIZE))
                                .text_color(rgb(crate::ui::theme::TEXT));
                            if line == caret_line {
                                row = row.bg(rgb(0x141A21));
                            }
                            row = row
                                .child(
                                    div()
                                        .w(px(EDITOR_GUTTER_W))
                                        .flex_shrink_0()
                                        .flex()
                                        .flex_row()
                                        .justify_end()
                                        .pr(px(14.0))
                                        .text_size(px(10.0))
                                        .text_color(rgb(if line == caret_line {
                                            0x768193
                                        } else {
                                            0x515D6D
                                        }))
                                        .child(format!("{}", line + 1)),
                                )
                                .child(
                                    div()
                                        .flex_1()
                                        .min_w(px(0.0))
                                        .whitespace_nowrap()
                                        .on_mouse_down(
                                            MouseButton::Left,
                                            _cx.listener(move |view, event, window, cx| {
                                                view.editor_mouse_down(document, event, window, cx);
                                            }),
                                        )
                                        .child(
                                            StyledText::new(display.clone().into_owned())
                                                .with_highlights(styles),
                                        ),
                                );
                            if line == caret_line && selection.is_none() {
                                let display_col =
                                    editor::buffer_col_to_display_col(line_text, caret_col);
                                let prefix = display.get(..display_col.min(display.len()));
                                let mut prefix_len = prefix.map(str::len).unwrap_or(0);
                                while prefix_len > 0 && !display.is_char_boundary(prefix_len) {
                                    prefix_len -= 1;
                                }
                                let caret_x = Self::editor_shape_width(
                                    &display[..prefix_len],
                                    &row_mono,
                                    window,
                                );
                                row = row.child(
                                    div()
                                        .absolute()
                                        .left(px(EDITOR_GUTTER_W + caret_x))
                                        .top(px(3.0))
                                        .w(px(1.0))
                                        .h(px(16.0))
                                        .bg(rgb(crate::ui::theme::TEXT)),
                                );
                            }
                            row.w(px(row_width))
                        })
                        .collect::<Vec<_>>()
                }
            }),
        )
        .track_scroll(rows_handle)
        .w(px(row_width))
        .h_full()
        .flex_shrink_0()
        .map(|mut list| {
            list.style().restrict_scroll_to_axis = Some(true);
            list
        });
        let body = div()
            .relative()
            .flex()
            .flex_1()
            .min_w(px(0.0))
            .min_h(px(0.0))
            .child(
                canvas(
                    move |bounds, _, _| bounds,
                    move |_, bounds_prepaint: Bounds<Pixels>, _, _| {
                        origin.set(bounds_prepaint.origin);
                    },
                )
                .absolute()
                .top(px(0.0))
                .left(px(0.0))
                .right(px(0.0))
                .bottom(px(0.0)),
            )
            .child(
                div()
                    .id("editor-horizontal")
                    .flex()
                    .flex_1()
                    .min_w(px(0.0))
                    .min_h(px(0.0))
                    .overflow_x_scroll()
                    .track_scroll(&x_handle)
                    .map(|mut horizontal| {
                        horizontal.style().restrict_scroll_to_axis = Some(true);
                        horizontal
                    })
                    .on_mouse_move(cx.listener(move |view, event, window, cx| {
                        view.editor_mouse_move(document, event, window, cx);
                    }))
                    .on_mouse_up(
                        MouseButton::Left,
                        cx.listener(move |view, event, _, cx| {
                            view.editor_mouse_up(document, event, cx);
                        }),
                    )
                    .child(rows),
            );
        bar = bar.child(body);

        // Footer: cursor (1-based display column), indentation,
        // encoding, language.
        let caret_display_col = {
            let line_text = snapshot.line(caret_line).unwrap_or("");
            let mut cols = 0;
            let mut bytes = 0;
            for ch in line_text.chars() {
                if bytes >= caret_col {
                    break;
                }
                bytes += ch.len_utf8();
                cols += if ch == '\t' {
                    4 - (cols % 4)
                } else {
                    editor::char_display_width(ch)
                };
            }
            cols
        };
        let footer = div()
            .flex()
            .flex_row()
            .items_center()
            .gap_4()
            .px_3()
            .h(px(28.0))
            .flex_shrink_0()
            .border_t_1()
            .border_color(rgb(crate::ui::theme::BORDER))
            .bg(rgb(0x0F1216))
            .text_size(px(10.0))
            .text_color(rgb(crate::ui::theme::MUTED))
            .child(format!(
                "Ln {}, Col {}",
                caret_line + 1,
                caret_display_col + 1
            ))
            .child("Spaces: 4")
            .child("UTF-8")
            .child(div().flex_1())
            .child(language.as_str());
        if dirty {
            bar = bar.child(
                footer.child(
                    div()
                        .text_color(rgb(crate::ui::theme::YELLOW))
                        .child("unsaved changes"),
                ),
            );
        } else {
            bar = bar.child(footer);
        }
        bar.font_family(mono)
            .bg(rgb(crate::ui::theme::EDITOR_BG))
            .text_color(rgb(crate::ui::theme::TEXT))
    }

    /// Token foregrounds: plan palette hues; comments use a muted green
    /// first-delivery approximation until a full token palette lands.
    fn editor_token_color(kind: editor::TokenKind) -> u32 {
        match kind {
            editor::TokenKind::Comment => 0x6A9955,
            editor::TokenKind::String => crate::ui::theme::ORANGE,
            editor::TokenKind::Number => crate::ui::theme::CYAN,
            editor::TokenKind::Keyword => crate::ui::theme::PURPLE,
        }
    }

    /// Shaped pixel width of a display-expanded prefix in the editor face.
    fn editor_shape_width(prefix: &str, mono: &str, window: &Window) -> f32 {
        window
            .text_system()
            .shape_line(
                SharedString::from(prefix.to_owned()),
                px(EDITOR_FONT_SIZE),
                &[TextRun {
                    len: prefix.len(),
                    font: font(mono.to_owned()),
                    color: rgb(crate::ui::theme::TEXT).into(),
                    background_color: None,
                    underline: None,
                    strikethrough: None,
                }],
                None,
            )
            .width
            .into()
    }

    /// Unstage one file from the staged diff side (whole-file scope,
    /// same path as the Source Control panel).
    fn diff_unstage_file(
        &mut self,
        project: ProjectId,
        path: std::path::PathBuf,
        cx: &mut Context<Self>,
    ) {
        if self.shutting_down {
            return;
        }
        match self.dispatch_command(
            OmaCommand::Git(GitCommand::Unstage {
                project,
                paths: vec![path],
            }),
            cx,
        ) {
            Ok(_) => {
                self.git_dirty_hint = true;
                self.diff_dirty_hint = true;
                self.diff_panel.set_show_staged(project, false);
                cx.notify();
            }
            Err(error) => {
                self.input_notice = Some(format!("Unstage: {error}"));
                cx.notify();
            }
        }
    }

    /// Stage explicit paths through the dispatcher (same path as IPC/CLI),
    /// then hint an immediate status refresh.
    fn git_stage_paths(
        &mut self,
        project: ProjectId,
        paths: Vec<std::path::PathBuf>,
        cx: &mut Context<Self>,
    ) {
        if self.shutting_down {
            return;
        }
        let summary = summarize_paths(&paths);
        match self.dispatch_command(OmaCommand::Git(GitCommand::Stage { project, paths }), cx) {
            Ok(_) => {
                self.git_dirty_hint = true;
                self.diff_dirty_hint = true;
                self.diff_panel.set_show_staged(project, true);
                self.show_toast(format!("Staged {summary}"), cx);
            }
            Err(error) => {
                self.input_notice = Some(format!("Stage: {error}"));
                cx.notify();
            }
        }
    }

    /// Unstage explicit paths (index restored, worktree kept).
    fn git_unstage_paths(
        &mut self,
        project: ProjectId,
        paths: Vec<std::path::PathBuf>,
        cx: &mut Context<Self>,
    ) {
        if self.shutting_down {
            return;
        }
        let summary = summarize_paths(&paths);
        match self.dispatch_command(OmaCommand::Git(GitCommand::Unstage { project, paths }), cx) {
            Ok(_) => {
                self.git_dirty_hint = true;
                self.diff_dirty_hint = true;
                self.show_toast(format!("Unstaged {summary}"), cx);
            }
            Err(error) => {
                self.input_notice = Some(format!("Unstage: {error}"));
                cx.notify();
            }
        }
    }

    /// Discard with the two-step arm: the first press arms (banner), the
    /// second press inside the window dispatches. No code path dispatches
    /// without a live arm, so discard-without-confirm is impossible.
    fn git_discard_path(
        &mut self,
        project: ProjectId,
        path: std::path::PathBuf,
        cx: &mut Context<Self>,
    ) {
        if self.shutting_down {
            return;
        }
        if !self.git_panel.arm_discard(project, &path) {
            cx.notify();
            return;
        }
        let summary = summarize_paths(std::slice::from_ref(&path));
        match self.dispatch_command(
            OmaCommand::Git(GitCommand::Discard {
                project,
                paths: vec![path],
            }),
            cx,
        ) {
            Ok(_) => {
                self.git_dirty_hint = true;
                self.diff_dirty_hint = true;
                // Discard may delete files: rebuild the tree now instead
                // of waiting for the watcher tick.
                self.refresh_files(cx);
                self.show_toast(format!("Discarded {summary}"), cx);
            }
            Err(error) => {
                self.input_notice = Some(format!("Discard: {error}"));
                cx.notify();
            }
        }
    }

    /// Stage every unstaged/untracked path of the working-tree group.
    fn git_stage_all(&mut self, project: ProjectId, cx: &mut Context<Self>) {
        let paths: Vec<std::path::PathBuf> = self
            .git_panel
            .status_for(project)
            .map(|status| {
                status
                    .unstaged
                    .iter()
                    .chain(status.untracked.iter())
                    .map(|entry| entry.path.clone())
                    .collect()
            })
            .unwrap_or_default();
        if !paths.is_empty() {
            self.git_stage_paths(project, paths, cx);
        }
    }

    /// Unstage every staged path.
    fn git_unstage_all(&mut self, project: ProjectId, cx: &mut Context<Self>) {
        let paths: Vec<std::path::PathBuf> = self
            .git_panel
            .status_for(project)
            .map(|status| {
                status
                    .staged
                    .iter()
                    .map(|entry| entry.path.clone())
                    .collect()
            })
            .unwrap_or_default();
        if !paths.is_empty() {
            self.git_unstage_paths(project, paths, cx);
        }
    }

    /// Discard every working-tree path behind the same two-step arm as
    /// single-path discard: first press arms (banner names the count),
    /// second press inside the window dispatches one discard per path.
    fn git_discard_all(&mut self, project: ProjectId, cx: &mut Context<Self>) {
        let paths: Vec<std::path::PathBuf> = self
            .git_panel
            .status_for(project)
            .map(|status| {
                status
                    .unstaged
                    .iter()
                    .chain(status.untracked.iter())
                    .map(|entry| entry.path.clone())
                    .collect()
            })
            .unwrap_or_default();
        if paths.is_empty() {
            return;
        }
        if !self.git_panel.arm_discard_all(project, &paths) {
            cx.notify();
            return;
        }
        let summary = summarize_paths(&paths);
        match self.dispatch_command(OmaCommand::Git(GitCommand::Discard { project, paths }), cx) {
            Ok(_) => {
                self.git_dirty_hint = true;
                self.diff_dirty_hint = true;
                self.refresh_files(cx);
                self.show_toast(format!("Discarded {summary}"), cx);
            }
            Err(error) => {
                self.input_notice = Some(format!("Discard: {error}"));
                cx.notify();
            }
        }
    }

    /// Select a changed path and open its M15 diff in the main-area
    /// preview tab. The clicked row's group selects the staged or
    /// unstaged diff side. The preview is view-local: core tabs stay
    /// terminal-only (M13/M17 scope).
    fn git_select_path(&mut self, project: ProjectId, path: std::path::PathBuf, staged: bool) {
        self.diff_generation = self.diff_generation.wrapping_add(1);
        self.diff_worker.cancel();
        self.diff_in_flight = None;
        self.diff_scroll_handles
            .retain(|(owner, _, selected, _), _| *owner != project || selected == &path);
        self.git_panel.select(project, path.clone());
        self.diff_panel.select_file(project, path);
        self.diff_panel.set_show_staged(project, staged);
        self.diff_panel.open_preview(project);
        self.diff_dirty_hint = true;
        tracing::debug!(
            target: "omaterm::git",
            project_id = %project.0,
            "git change selected (diff preview opened)",
        );
    }

    /// Keyboard for the focused commit input (single-line): Esc releases,
    /// Enter submits, Backspace deletes, printable characters append.
    /// Ctrl/Alt combinations never reach here — the caller falls through
    /// to global shortcuts so they keep working while typing.
    fn on_commit_key(&mut self, event: &KeyDownEvent, cx: &mut Context<Self>) {
        let Some(project) = self.coordinator.selected_project_id() else {
            return;
        };
        let key_name = event.keystroke.key.to_lowercase().replace('_', "");
        match key_name.as_str() {
            "escape" => {
                self.git_panel.set_commit_focused(false);
                cx.notify();
            }
            "enter" | "return" | "kpenter" => self.git_commit_submit(project, cx),
            "backspace" => {
                if self.git_panel.pop_commit_char(project) {
                    cx.notify();
                }
            }
            _ => {
                let char = event
                    .keystroke
                    .key_char
                    .as_ref()
                    .and_then(|text| text.chars().next());
                if let Some(char) = char
                    && self.git_panel.push_commit_char(project, char)
                {
                    cx.notify();
                }
            }
        }
    }

    /// Submit the draft through the dispatcher (same path as IPC/CLI).
    /// Success clears and releases the input; failure puts the message
    /// back so the user fixes and retries instead of retyping.
    fn git_commit_submit(&mut self, project: ProjectId, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
        }
        let staged_empty = self
            .git_panel
            .status_for(project)
            .is_none_or(|status| status.staged.is_empty());
        if staged_empty {
            self.input_notice = Some("Commit: nothing staged to commit.".into());
            cx.notify();
            return;
        }
        let message = self.git_panel.take_commit_draft(project);
        if message.trim().is_empty() {
            self.git_panel.restore_commit_draft(project, message);
            self.input_notice = Some("Commit: type a commit message first.".into());
            cx.notify();
            return;
        }
        match self.dispatch_command(
            OmaCommand::Git(GitCommand::Commit {
                project,
                message: message.clone(),
            }),
            cx,
        ) {
            Ok(CommandOutput::GitCommitted { oid }) => {
                self.git_panel.set_commit_focused(false);
                self.git_dirty_hint = true;
                self.diff_dirty_hint = true;
                self.show_toast(format!("Committed {oid}"), cx);
            }
            Ok(_) => {
                self.git_dirty_hint = true;
                self.diff_dirty_hint = true;
                cx.notify();
            }
            Err(error) => {
                // Put the message back: a hook/GPG rejection is fixable.
                self.git_panel.restore_commit_draft(project, message);
                self.input_notice = Some(format!("Commit: {error}"));
                cx.notify();
            }
        }
    }

    fn toggle_file_row(
        &mut self,
        project: ProjectId,
        path: std::path::PathBuf,
        is_dir: bool,
        cx: &mut Context<Self>,
    ) {
        if self.shutting_down {
            return;
        }
        self.files_panel.toggle(project, &path, is_dir);
        self.refresh_files(cx);
        self.mark_persistence_dirty(cx);
    }

    fn open_file_path(
        &mut self,
        project: ProjectId,
        path: std::path::PathBuf,
        cx: &mut Context<Self>,
    ) {
        if self.shutting_down {
            return;
        }
        if let Err(error) =
            self.dispatch_command(OmaCommand::File(FileCommand::Open { project, path }), cx)
        {
            self.input_notice = Some(format!("Open: {error}"));
            cx.notify();
        }
    }

    /// Copy the selected row's absolute path, Bourne shell-escaped (§47
    /// policy reuse), to the clipboard.
    fn copy_selected_path(&mut self, cx: &mut Context<Self>) {
        let Some(project) = self.coordinator.selected_project_id() else {
            return;
        };
        let Some(relative) = self
            .files_panel
            .selected_path(project)
            .map(|path| path.to_path_buf())
        else {
            self.input_notice = Some("Files: no file selected.".into());
            cx.notify();
            return;
        };
        self.copy_path(project, &relative, cx);
    }

    fn copy_path(
        &mut self,
        project: ProjectId,
        relative: &std::path::Path,
        cx: &mut Context<Self>,
    ) {
        let Some(root) = self.files_project_root(project, cx) else {
            self.input_notice = Some("Files: project has no root.".into());
            cx.notify();
            return;
        };
        let absolute = root.join(relative);
        let escaped = omaterm_terminal::escape_shell_path(&absolute);
        cx.write_to_clipboard(ClipboardItem::new_string(escaped));
        tracing::debug!(target: "omaterm::files", project_id = %project.0, "file path copied");
    }

    /// Type `cd <escaped-dir>` (no newline — the user reviews and submits)
    /// into the focused terminal.
    fn reveal_selected_in_terminal(&mut self, cx: &mut Context<Self>) {
        let Some(project) = self.coordinator.selected_project_id() else {
            return;
        };
        let Some(relative) = self
            .files_panel
            .selected_path(project)
            .map(|path| path.to_path_buf())
        else {
            self.input_notice = Some("Files: no file selected.".into());
            cx.notify();
            return;
        };
        self.reveal_path(project, &relative, cx);
    }

    fn reveal_path(
        &mut self,
        project: ProjectId,
        relative: &std::path::Path,
        cx: &mut Context<Self>,
    ) {
        let Some(root) = self.files_project_root(project, cx) else {
            self.input_notice = Some("Files: project has no root.".into());
            cx.notify();
            return;
        };
        let absolute = root.join(relative);
        let dir = if absolute.is_dir() {
            absolute
        } else {
            absolute
                .parent()
                .map(|parent| parent.to_path_buf())
                .unwrap_or(root)
        };
        let Some(session) = self.focused_session_id() else {
            self.input_notice = Some("Files: no focused terminal.".into());
            cx.notify();
            return;
        };
        let text = format!("cd {}", omaterm_terminal::escape_shell_path(&dir));
        if let Err(error) = self.dispatch_command(
            OmaCommand::Terminal(TerminalCommand::SendBytes {
                session,
                data: text.into_bytes(),
            }),
            cx,
        ) {
            self.input_notice = Some(format!("Reveal: {error}"));
            cx.notify();
        }
    }

    fn open_palette(&mut self, command_mode: bool, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
        }
        self.ctrlp_open = true;
        self.ctrlp_origin = Some(PaletteOrigin {
            project: self.coordinator.selected_project_id(),
            tab: self.coordinator.selected_tab_id(),
            pane: self.coordinator.focused(),
            session: self.coordinator.focused_session_id(),
        });
        self.ctrlp_query = if command_mode {
            ">".into()
        } else {
            String::new()
        };
        self.ctrlp_caret_byte = self.ctrlp_query.len();
        self.ctrlp_results.clear();
        self.ctrlp_static_results.clear();
        self.ctrlp_selected = 0;
        self.palette_scroll_handle
            .scroll_to_item(0, ScrollStrategy::Top);
        self.ctrlp_truncated = false;
        self.ctrlp_source_error = None;
        self.ctrlp_search_root = None;
        self.ctrlp_caret_on = true;
        self.ensure_ctrlp_blink(cx);
        self.ctrlp_search(cx);
        cx.notify();
    }

    fn palette_static_candidates(
        &self,
        command_mode: bool,
    ) -> (Vec<palette::PaletteCandidate>, bool) {
        use palette::{PaletteCandidate as Candidate, PaletteKind as Kind, PaletteTarget};

        let mut candidates = Vec::new();
        let mut candidate_keys = std::collections::HashSet::new();
        let mut source_truncated = false;
        let mut add = |candidate: Candidate| {
            if !candidate_keys.insert(candidate.key.clone()) {
                return;
            }
            if candidates.len() >= palette::MAX_PALETTE_SOURCE_CANDIDATES {
                source_truncated = true;
                return;
            }
            candidates.push(candidate);
        };
        let projects = self.coordinator.projects();
        let selected_project = self.coordinator.selected_project_id();
        let active_pane = self.coordinator.focused();

        if command_mode {
            let mut command = |key: &str, label: &str, aliases: &[&str], value: OmaCommand| {
                add(palette::semantic_candidate(
                    key,
                    label,
                    "Command",
                    aliases.iter().copied(),
                    Kind::Command,
                    value,
                ));
            };
            command(
                "command.project.new",
                "New Project",
                &["project create", "open project"],
                OmaCommand::Project(ProjectCommand::Create {
                    name: None,
                    directory: None,
                }),
            );
            if let Some(project) = selected_project {
                if let Some(tab) = self.coordinator.selected_tab_id() {
                    command(
                        "command.tab.close",
                        "Close Current Tab",
                        &["tab close", "close tab"],
                        OmaCommand::Tab(TabCommand::Close { tab }),
                    );
                }
                command(
                    "command.tab.new",
                    "New Tab",
                    &["tab create"],
                    OmaCommand::Tab(TabCommand::Create {
                        project,
                        name: None,
                    }),
                );
                command(
                    "command.terminal.new",
                    "New Terminal Tab",
                    &["terminal create", "new shell"],
                    OmaCommand::Terminal(TerminalCommand::Create {
                        project,
                        directory: None,
                    }),
                );
                command(
                    "command.git.refresh",
                    "Refresh Git Changes",
                    &["git refresh", "source control refresh"],
                    OmaCommand::Git(GitCommand::Status { project }),
                );
                command(
                    "command.process.refresh",
                    "Refresh Process List",
                    &["process list", "refresh processes"],
                    OmaCommand::Process(omaterm_core::ProcessCommand::List { project }),
                );
                let diff_path = self
                    .diff_panel
                    .selected_file(project)
                    .or_else(|| self.git_panel.selected_path(project))
                    .cloned();
                if let Some(path) = diff_path {
                    command(
                        "command.diff.refresh",
                        "Refresh Selected Diff",
                        &["diff refresh", "refresh diff"],
                        OmaCommand::Diff(DiffCommand::Show {
                            project,
                            path: Some(path),
                            staged: self.diff_panel.show_staged(project),
                            context_lines: 3,
                        }),
                    );
                }
                if let Some(pane) = active_pane {
                    for (label, key, direction) in [
                        ("Split Left", "left", SplitDirection::Left),
                        ("Split Right", "right", SplitDirection::Right),
                        ("Split Up", "up", SplitDirection::Up),
                        ("Split Down", "down", SplitDirection::Down),
                    ] {
                        command(
                            &format!("command.pane.split.{key}"),
                            label,
                            &["split", key],
                            OmaCommand::Pane(PaneCommand::Split {
                                target: pane,
                                direction,
                            }),
                        );
                    }
                    command(
                        "command.pane.close",
                        "Close Focused Pane",
                        &["pane close", "close pane"],
                        OmaCommand::Pane(PaneCommand::Close { pane }),
                    );
                    command(
                        "command.pane.resize.grow",
                        "Grow Focused Pane",
                        &["pane resize", "grow pane"],
                        OmaCommand::Pane(PaneCommand::ResizeFocused { amount: 0.1 }),
                    );
                    command(
                        "command.pane.resize.shrink",
                        "Shrink Focused Pane",
                        &["pane resize", "shrink pane"],
                        OmaCommand::Pane(PaneCommand::ResizeFocused { amount: -0.1 }),
                    );
                }
                for (label, key, direction) in [
                    ("Focus Left", "left", SplitDirection::Left),
                    ("Focus Right", "right", SplitDirection::Right),
                    ("Focus Up", "up", SplitDirection::Up),
                    ("Focus Down", "down", SplitDirection::Down),
                ] {
                    command(
                        &format!("command.pane.focus.{key}"),
                        label,
                        &["focus", key],
                        OmaCommand::Pane(PaneCommand::FocusDirection { direction }),
                    );
                }
                command(
                    "command.pane.equalize",
                    "Equalize Panes",
                    &["pane equalize", "equalize"],
                    OmaCommand::Pane(PaneCommand::EqualizeSelected),
                );
            }
        } else {
            for (project_index, project) in projects.iter().enumerate() {
                let name = project.display_name(project_index + 1);
                let project_short = project.id.0.simple().to_string();
                add(palette::semantic_candidate(
                    format!("project:{}", project.id.0),
                    format!("{name} · {}", &project_short[..8]),
                    format!("Project · {}", project.id.0),
                    [project.id.0.to_string()],
                    Kind::Project,
                    OmaCommand::Project(ProjectCommand::Select {
                        project: project.id,
                    }),
                ));
                for (tab_index, tab) in project.tabs.iter().enumerate() {
                    let tab_name = tab.display_name(tab_index + 1);
                    let tab_short = tab.id.0.simple().to_string();
                    add(palette::semantic_candidate(
                        format!("tab:{}", tab.id.0),
                        format!("{tab_name} · {}", &tab_short[..8]),
                        format!("{name} · Tab · {}", tab.id.0),
                        [tab.id.0.to_string()],
                        Kind::Tab,
                        OmaCommand::Tab(TabCommand::Select { tab: tab.id }),
                    ));
                    for pane in tab.tree.panes() {
                        let pane_short = pane.id.0.simple().to_string();
                        let expected_session = match &pane.content {
                            PaneContent::Terminal(session) => Some(*session),
                            PaneContent::Empty => None,
                        };
                        let pane_title = expected_session.map_or_else(
                            || "Empty pane".to_owned(),
                            |session| format!("Pane · session {}", session.0),
                        );
                        add(Candidate {
                            key: format!("pane:{}", pane.id.0),
                            label: format!("{pane_title} · {}", &pane_short[..8]),
                            detail: format!("{name} · {tab_name} · {}", pane.id.0),
                            aliases: vec![pane.id.0.to_string()],
                            kind: Kind::Pane,
                            target: PaletteTarget::PaneFocus {
                                pane: pane.id,
                                expected_session,
                            },
                            file_root: None,
                            mru_rank: None,
                        });
                        if let PaneContent::Terminal(session) = &pane.content {
                            add(Candidate {
                                key: format!("session:{}", session.0),
                                label: format!("Session · {}", session.0),
                                detail: format!("{name} · {tab_name} · pane {}", pane.id.0),
                                aliases: vec![session.0.to_string()],
                                kind: Kind::Session,
                                target: PaletteTarget::PaneFocus {
                                    pane: pane.id,
                                    expected_session: Some(*session),
                                },
                                file_root: None,
                                mru_rank: None,
                            });
                        }
                    }
                }
            }
            if let Some(project) = selected_project
                && let Some(status) = self.git_panel.status_for(project)
            {
                for (staged, entries) in [
                    (true, status.staged.as_slice()),
                    (false, status.unstaged.as_slice()),
                    (false, status.untracked.as_slice()),
                ] {
                    for entry in entries {
                        let path = entry.path.clone();
                        let path_display = path.to_string_lossy().into_owned();
                        let suffix = if staged { "staged" } else { "working" };
                        let display = format!(
                            "{} · {}",
                            path_display,
                            if staged { "Staged" } else { "Working Tree" }
                        );
                        add(Candidate {
                            key: format!(
                                "git:{}:{suffix}:{}",
                                project.0,
                                palette::path_identity(&path)
                            ),
                            label: display,
                            detail: path_display,
                            aliases: Vec::new(),
                            kind: Kind::GitPath,
                            file_root: None,
                            target: PaletteTarget::GitDiff {
                                project,
                                path,
                                staged,
                            },
                            mru_rank: None,
                        });
                    }
                }
            }
            if let Some(project) = selected_project
                && let Some((watched_project, root)) = &self.files_watched
                && *watched_project == project
                && let Ok(cache) = self.palette_file_index.state.lock()
                && let Some(index) = cache
                    .entry
                    .as_ref()
                    .filter(|entry| entry.project == project && entry.root == *root)
            {
                for recent in &self.palette_recent_files {
                    let Some((owner, path)) = recent.file_target() else {
                        continue;
                    };
                    if owner == project
                        && recent.file_root.as_deref() == Some(root.as_path())
                        && index.index.contains(&path)
                    {
                        add(recent.clone());
                    }
                }
            }
        }
        for candidate in &mut candidates {
            candidate.mru_rank = self
                .palette_mru
                .iter()
                .position(|key| key == &candidate.key);
        }
        (candidates, source_truncated)
    }

    fn palette_query(&self) -> &str {
        self.ctrlp_query.trim_start_matches('>').trim()
    }

    fn invalidate_palette_file_index(&self) {
        self.palette_file_index.invalidate();
    }

    fn palette_insert_text(&mut self, text: &str, cx: &mut Context<Self>) {
        let available = palette::MAX_PALETTE_QUERY_BYTES.saturating_sub(self.ctrlp_query.len());
        let mut inserted = String::new();
        for ch in text.chars() {
            if !ch.is_control() && inserted.len() + ch.len_utf8() <= available {
                inserted.push(ch);
            }
        }
        self.ctrlp_query
            .insert_str(self.ctrlp_caret_byte, &inserted);
        self.ctrlp_caret_byte += inserted.len();
        if !inserted.is_empty() {
            self.ctrlp_search(cx);
            cx.notify();
        }
    }

    /// Drives the finder caret blink (~530ms, VSCode-like rate). One task
    /// per open session: it exits on close/shutdown, and any keystroke
    /// restores visibility (standard blink-phase reset). No timers run
    /// while the finder is closed.
    fn ensure_ctrlp_blink(&mut self, cx: &mut Context<Self>) {
        if self.ctrlp_blink_active {
            return;
        }
        self.ctrlp_blink_active = true;
        cx.spawn(async move |weak: WeakEntity<Self>, cx: &mut AsyncApp| {
            loop {
                Timer::after(Duration::from_millis(530)).await;
                let alive = weak
                    .update(cx, |view, cx| {
                        if view.shutting_down || !view.ctrlp_open {
                            view.ctrlp_blink_active = false;
                            return false;
                        }
                        view.ctrlp_caret_on = !view.ctrlp_caret_on;
                        cx.notify();
                        true
                    })
                    .unwrap_or(false);
                if !alive {
                    break;
                }
            }
        })
        .detach();
    }

    /// Run the fuzzy search on a background thread against a
    /// router-resolved root; results land via the files poller keyed by
    /// generation (stale keystrokes never overwrite newer ones).
    fn ctrlp_search(&mut self, _cx: &mut Context<Self>) {
        let project = self.coordinator.selected_project_id();
        let command_mode = self.ctrlp_query.starts_with('>');
        let query = self.palette_query().to_owned();
        let (static_results, source_truncated) = self.palette_static_candidates(command_mode);
        self.ctrlp_static_results = static_results;
        let (ranked, rank_truncated) = palette::rank_with_truncation(
            self.ctrlp_static_results.clone(),
            &query,
            palette::MAX_PALETTE_RESULTS,
        );
        self.ctrlp_results = ranked;
        self.ctrlp_selected = 0;
        self.palette_scroll_handle
            .scroll_to_item(0, ScrollStrategy::Top);
        self.ctrlp_truncated = source_truncated || rank_truncated;
        self.ctrlp_source_error = None;
        self.ctrlp_search_root = None;
        self.ctrlp_generation = self.ctrlp_generation.wrapping_add(1);
        let generation = self.ctrlp_generation;
        if command_mode || query.is_empty() {
            self.palette_search_worker.cancel_current();
            if !command_mode && let Some(project) = project {
                self.ctrlp_search_root = self
                    .files_watched
                    .as_ref()
                    .filter(|(owner, _)| *owner == project)
                    .map(|(_, root)| root.clone());
            }
            return;
        }
        let Some(project) = project else {
            self.palette_search_worker.cancel_current();
            return;
        };
        let pinned = self
            .coordinator
            .projects()
            .iter()
            .find(|candidate| candidate.id == project)
            .and_then(|candidate| candidate.pinned_directory.clone());
        let active_cwd = self
            .coordinator
            .focused_session_id()
            .and_then(|session| self.coordinator.registry().get(session))
            .and_then(|handle| handle.lock().ok().map(|session| session.cwd().path.clone()));
        let watched_root = self
            .files_watched
            .as_ref()
            .filter(|(watched_project, _)| *watched_project == project)
            .map(|(_, root)| root.clone());
        self.palette_search_worker.submit(PaletteSearchRequest {
            generation,
            project,
            query,
            show_hidden: self.files_show_hidden,
            pinned,
            active_cwd,
            watched_root,
            cancelled: Arc::new(AtomicBool::new(false)),
        });
    }

    /// Open the highlighted file result in the native editor (M19 Phase D
    /// interim trigger: Ctrl+Enter / Ctrl+click). Closes the palette and
    /// activates the document; non-file results report a notice instead of
    /// falling back to terminal submission.
    fn ctrlp_open_in_editor(&mut self, cx: &mut Context<Self>) {
        let Some((project, path)) = self
            .ctrlp_results
            .get(self.ctrlp_selected)
            .cloned()
            .and_then(|entry| entry.file_target())
        else {
            self.input_notice = Some("No file result selected.".into());
            cx.notify();
            return;
        };
        self.ctrlp_open = false;
        self.palette_search_worker.cancel_current();
        self.editor_open_document(project, path, cx);
    }

    fn ctrlp_confirm(&mut self, cx: &mut Context<Self>) {
        let Some(entry) = self.ctrlp_results.get(self.ctrlp_selected).cloned() else {
            return;
        };
        self.ctrlp_open = false;
        self.palette_search_worker.cancel_current();
        let outcome = match entry.target.clone() {
            palette::PaletteTarget::Semantic(command) => {
                if let Some((project, path)) = entry.file_target() {
                    let current_origin = PaletteOrigin {
                        project: self.coordinator.selected_project_id(),
                        tab: self.coordinator.selected_tab_id(),
                        pane: self.coordinator.focused(),
                        session: self.coordinator.focused_session_id(),
                    };
                    let current_root = self.files_project_root(project, cx);
                    if self.coordinator.selected_project_id() != Some(project)
                        || self.ctrlp_origin != Some(current_origin)
                        || self.ctrlp_search_root != current_root
                    {
                        self.input_notice = Some("Palette target changed; search again.".into());
                        self.restore_palette_origin(cx);
                        cx.notify();
                        return;
                    }
                    self.files_panel.select(project, path.clone());
                    Some(self.dispatch_command(
                        OmaCommand::File(FileCommand::Open { project, path }),
                        cx,
                    ))
                } else {
                    let requires_origin = matches!(
                        &command,
                        OmaCommand::Pane(
                            PaneCommand::FocusDirection { .. }
                                | PaneCommand::ResizeFocused { .. }
                                | PaneCommand::EqualizeSelected
                        )
                    );
                    let current_origin = PaletteOrigin {
                        project: self.coordinator.selected_project_id(),
                        tab: self.coordinator.selected_tab_id(),
                        pane: self.coordinator.focused(),
                        session: self.coordinator.focused_session_id(),
                    };
                    if requires_origin && self.ctrlp_origin != Some(current_origin) {
                        self.input_notice = Some("Palette origin changed; search again.".into());
                        self.restore_palette_origin(cx);
                        cx.notify();
                        return;
                    }
                    let refresh_git =
                        matches!(&command, OmaCommand::Git(GitCommand::Status { .. }));
                    let refresh_diff = matches!(
                        &command,
                        OmaCommand::Diff(DiffCommand::Show { .. } | DiffCommand::ListFiles { .. })
                    );
                    let process_project = match &command {
                        OmaCommand::Process(omaterm_core::ProcessCommand::List { project }) => {
                            Some(*project)
                        }
                        _ => None,
                    };
                    let outcome = self.dispatch_command(command, cx);
                    if outcome.is_ok() {
                        self.git_dirty_hint |= refresh_git;
                        self.diff_dirty_hint |= refresh_diff;
                    }
                    if let (Some(project), Ok(CommandOutput::ProcessList(info))) =
                        (process_project, &outcome)
                    {
                        self.process_list = Some((project, info.clone()));
                        self.select_inspector_tab(InspectorTab::Info, cx);
                    }
                    Some(outcome)
                }
            }
            palette::PaletteTarget::PaneFocus {
                pane,
                expected_session,
            } => {
                let exists =
                    self.coordinator.projects().iter().any(|project| {
                        project.tabs.iter().any(|tab| tab.tree.find(pane).is_some())
                    });
                if !exists || self.coordinator.session_id_for_pane(pane) != expected_session {
                    self.input_notice =
                        Some("Palette pane/session target changed; search again.".into());
                    self.restore_palette_origin(cx);
                    cx.notify();
                    return;
                }
                Some(self.dispatch_command(OmaCommand::Pane(PaneCommand::Focus { pane }), cx))
            }
            palette::PaletteTarget::GitDiff {
                project,
                path,
                staged,
            } => {
                let still_changed = self.git_panel.status_for(project).is_some_and(|status| {
                    if staged {
                        status.staged.iter().any(|entry| entry.path == path)
                    } else {
                        status
                            .unstaged
                            .iter()
                            .chain(status.untracked.iter())
                            .any(|entry| entry.path == path)
                    }
                });
                if self.coordinator.selected_project_id() != Some(project) || !still_changed {
                    self.input_notice = Some("Palette target changed; search again.".into());
                    self.restore_palette_origin(cx);
                    cx.notify();
                    return;
                }
                let outcome = self.dispatch_command(
                    OmaCommand::Diff(DiffCommand::Show {
                        project,
                        path: Some(path.clone()),
                        staged,
                        context_lines: 3,
                    }),
                    cx,
                );
                if let Err(error) = &outcome {
                    self.input_notice = Some(format!("Diff: {error}"));
                    self.restore_palette_origin(cx);
                    cx.notify();
                    return;
                }
                self.git_select_path(project, path, staged);
                Some(outcome)
            }
        };
        if let Some(outcome) = outcome {
            match outcome {
                Ok(omaterm_core::CommandOutput::Pending { operation_id }) => {
                    self.pending_palette_mru.insert(operation_id, entry.key);
                    if let Some(origin) = self.ctrlp_origin {
                        self.pending_palette_origins.insert(operation_id, origin);
                    }
                }
                Ok(_) => {
                    self.palette_mru.retain(|key| key != &entry.key);
                    self.palette_mru.insert(0, entry.key.clone());
                    self.palette_mru.truncate(100);
                    if entry.kind == palette::PaletteKind::File {
                        self.palette_recent_files
                            .retain(|recent| recent.key != entry.key);
                        self.palette_recent_files.insert(0, entry.clone());
                        self.palette_recent_files.truncate(100);
                    }
                }
                Err(error) => self.input_notice = Some(format!("Palette: {error}")),
            }
            if matches!(
                &entry.target,
                palette::PaletteTarget::Semantic(
                    OmaCommand::File(FileCommand::Open { .. })
                        | OmaCommand::Git(GitCommand::Status { .. })
                        | OmaCommand::Diff(
                            DiffCommand::Show { .. } | DiffCommand::ListFiles { .. }
                        )
                        | OmaCommand::Process(omaterm_core::ProcessCommand::List { .. })
                )
            ) {
                self.restore_palette_origin(cx);
            }
        }
        cx.notify();
    }

    fn restore_palette_origin(&mut self, cx: &mut Context<Self>) {
        if let Some(origin) = self.ctrlp_origin {
            self.restore_palette_origin_to(origin, cx);
        }
    }

    fn restore_palette_origin_to(&mut self, origin: PaletteOrigin, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
        }
        let current = PaletteOrigin {
            project: self.coordinator.selected_project_id(),
            tab: self.coordinator.selected_tab_id(),
            pane: self.coordinator.focused(),
            session: self.coordinator.focused_session_id(),
        };
        if current == origin {
            return;
        }
        let result = if let Some(pane) = origin.pane {
            let location = self.coordinator.projects().iter().find_map(|project| {
                project
                    .tabs
                    .iter()
                    .find(|tab| tab.tree.find(pane).is_some())
                    .map(|_| project.id)
            });
            if location.is_none()
                || (origin.session.is_some()
                    && self.coordinator.session_id_for_pane(pane) != origin.session)
            {
                self.input_notice =
                    Some("Palette origin no longer exists; focus was not restored.".into());
                return;
            }
            self.dispatch_command(OmaCommand::Pane(PaneCommand::Focus { pane }), cx)
        } else if let Some(tab) = origin.tab {
            let exists = self
                .coordinator
                .projects()
                .iter()
                .any(|project| project.tab(tab).is_some());
            if !exists {
                self.input_notice =
                    Some("Palette origin no longer exists; focus was not restored.".into());
                return;
            }
            self.dispatch_command(OmaCommand::Tab(TabCommand::Select { tab }), cx)
        } else if let Some(project) = origin.project {
            if !self
                .coordinator
                .projects()
                .iter()
                .any(|candidate| candidate.id == project)
            {
                self.input_notice =
                    Some("Palette origin no longer exists; focus was not restored.".into());
                return;
            }
            self.dispatch_command(OmaCommand::Project(ProjectCommand::Select { project }), cx)
        } else {
            return;
        };
        if let Err(error) = result {
            self.input_notice = Some(format!("Palette focus restore: {error}"));
        }
    }

    fn palette_select(&mut self, selected: usize) {
        self.ctrlp_selected = selected.min(self.ctrlp_results.len().saturating_sub(1));
        if !self.ctrlp_results.is_empty() {
            self.palette_scroll_handle
                .scroll_to_item(self.ctrlp_selected, ScrollStrategy::Center);
        }
    }

    /// Open the native folder picker to change a project's base directory.
    /// Only future tabs, splits, and default launches use the new directory;
    /// live sessions keep their CWD. Cancellation changes nothing; a missing
    /// portal surfaces a dismissible warning instead of failing silently.
    fn pick_project_directory(&mut self, project: omaterm_core::ProjectId, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
        }
        let receiver = cx.prompt_for_paths(PathPromptOptions {
            files: false,
            directories: true,
            multiple: false,
            prompt: Some("Set project directory".into()),
        });
        cx.spawn(async move |weak: WeakEntity<Self>, cx: &mut AsyncApp| {
            let result = receiver.await;
            let _ = weak.update(cx, |view, cx| {
                if view.shutting_down {
                    return;
                }
                match result {
                    Ok(Ok(Some(mut paths))) if !paths.is_empty() => {
                        let directory = paths.remove(0);
                        if let Err(error) = view.dispatch_command(
                            OmaCommand::Project(ProjectCommand::SetDirectory {
                                project,
                                directory,
                            }),
                            cx,
                        ) {
                            tracing::warn!(target: "omaterm::workspace", "project directory not updated: {error}");
                            view.persistence_warning =
                                Some(format!("Project directory not updated: {error}"));
                            cx.notify();
                        }
                    }
                    // Dialog cancelled: no state change.
                    Ok(Ok(_)) => {}
                    // Portal missing or dialog failure: warn, keep everything.
                    Ok(Err(error)) => {
                        tracing::warn!(target: "omaterm::workspace", "folder picker unavailable: {error:#}");
                        view.persistence_warning =
                            Some(format!("Folder picker unavailable: {error:#}"));
                        cx.notify();
                    }
                    Err(_) => {}
                }
            });
        })
        .detach();
    }

    /// Resolve (once per font size) and cache the terminal font set.
    fn fonts(&mut self, cx: &App) -> ResolvedFonts {
        let font_size = px(self.font_size);
        if self
            .fonts
            .as_ref()
            .is_none_or(|cached| cached.font_size != font_size)
        {
            self.fonts = Some(resolve_terminal_fonts(
                cx,
                font_size,
                self.font_family.clone(),
            ));
        }
        self.fonts.clone().expect("fonts just resolved")
    }

    /// Keyboard handling while the `Ctrl+P` finder is open: the overlay
    /// owns every keystroke (typing filters, Up/Down navigate, Enter opens,
    /// Esc dismisses back to the terminal). Nothing reaches the shell.
    /// Inspector search-box key handling: Esc clears and releases, Enter
    /// opens the first file match (dirs toggle), Backspace edits, printable
    /// characters append (bounded). The filter applies to cached rows only.
    fn on_files_search_key(&mut self, event: &KeyDownEvent, cx: &mut Context<Self>) {
        let key_name = event.keystroke.key.to_lowercase().replace('_', "");
        match key_name.as_str() {
            "escape" => {
                self.files_search.clear();
                self.files_search_focused = false;
                self.files_scroll_rows = 0;
                self.files_scroll_remainder = 0.0;
                cx.notify();
            }
            "enter" | "return" | "kpenter" => {
                let project = self.coordinator.selected_project_id();
                let query = self.files_search.clone();
                // M19 Phase E: Ctrl+Enter opens the first file match in
                // the native editor; plain Enter keeps terminal routing.
                let to_editor = event.keystroke.modifiers.control
                    && !event.keystroke.modifiers.shift
                    && !event.keystroke.modifiers.alt;
                if let Some(project) = project
                    && let Some(row) = self
                        .files_panel
                        .rows_for(project)
                        .unwrap_or_default()
                        .iter()
                        .find(|row| files::row_matches_query(&row.path, &query))
                        .cloned()
                {
                    let is_dir = row.kind == omaterm_core::FileKind::Directory;
                    self.files_search_focused = false;
                    if is_dir {
                        self.toggle_file_row(project, row.path, true, cx);
                    } else if to_editor {
                        self.files_panel.select(project, row.path.clone());
                        self.editor_open_document(project, row.path, cx);
                    } else {
                        self.files_panel.select(project, row.path.clone());
                        self.open_file_path(project, row.path, cx);
                        self.refresh_files(cx);
                    }
                } else {
                    self.files_search_focused = false;
                }
                cx.notify();
            }
            "backspace" => {
                self.files_search.pop();
                self.files_scroll_rows = 0;
                self.files_scroll_remainder = 0.0;
                cx.notify();
            }
            _ => {
                let ch = event
                    .keystroke
                    .key_char
                    .as_ref()
                    .and_then(|s| s.chars().next());
                if let Some(ch) = ch
                    && !ch.is_control()
                    && self.files_search.len() < 256
                {
                    self.files_search.push(ch);
                    self.files_scroll_rows = 0;
                    self.files_scroll_remainder = 0.0;
                    cx.notify();
                }
            }
        }
    }

    fn on_ctrlp_key(&mut self, event: &KeyDownEvent, cx: &mut Context<Self>) {
        // Any keystroke restores caret visibility (standard blink-phase
        // reset) and keeps the blink task alive while open.
        self.ctrlp_caret_on = true;
        self.ensure_ctrlp_blink(cx);
        let key_name = event.keystroke.key.to_lowercase().replace('_', "");
        if event.keystroke.modifiers.control && !event.keystroke.modifiers.alt && key_name == "p" {
            self.ctrlp_query = if event.keystroke.modifiers.shift {
                ">".into()
            } else {
                String::new()
            };
            self.ctrlp_caret_byte = self.ctrlp_query.len();
            self.ctrlp_search(cx);
            cx.notify();
            return;
        }
        if event.keystroke.modifiers.control
            && !event.keystroke.modifiers.shift
            && !event.keystroke.modifiers.alt
            && key_name == "v"
        {
            if let Some(text) = cx
                .read_from_clipboard()
                .and_then(|item| item.text().map(|text| text.to_string()))
            {
                self.palette_insert_text(&text, cx);
            }
            return;
        }
        // Keyboard-only copy/reveal of the highlighted result (mirrors the
        // tree's Ctrl+Shift+Y/U); the finder stays open for further picks.
        if event.keystroke.modifiers.control
            && !event.keystroke.modifiers.shift
            && !event.keystroke.modifiers.alt
            && let Some(entry) = self.ctrlp_results.get(self.ctrlp_selected).cloned()
            && let Some((project, path)) = entry.file_target()
            && self.coordinator.selected_project_id() == Some(project)
        {
            if key_name == "y" {
                self.files_panel.select(project, path.clone());
                self.copy_path(project, &path, cx);
                return;
            }
            if key_name == "u" {
                self.files_panel.select(project, path.clone());
                self.reveal_path(project, &path, cx);
                return;
            }
        }
        match key_name.as_str() {
            "escape" => {
                self.ctrlp_open = false;
                self.palette_search_worker.cancel_current();
                self.restore_palette_origin(cx);
                cx.notify();
            }
            // M19 Phase D interim: Ctrl+Enter opens the highlighted
            // file result in the native editor instead of submitting it
            // to a terminal. Plain Enter keeps terminal-routed behavior.
            "enter" | "return" | "kpenter" => {
                if event.keystroke.modifiers.control
                    && !event.keystroke.modifiers.shift
                    && !event.keystroke.modifiers.alt
                {
                    self.ctrlp_open_in_editor(cx);
                } else {
                    self.ctrlp_confirm(cx);
                }
            }
            "backspace" => {
                if self.ctrlp_caret_byte > 0 {
                    let previous = self.ctrlp_query[..self.ctrlp_caret_byte]
                        .char_indices()
                        .next_back()
                        .map(|(index, _)| index)
                        .unwrap_or(0);
                    self.ctrlp_query.drain(previous..self.ctrlp_caret_byte);
                    self.ctrlp_caret_byte = previous;
                }
                self.ctrlp_search(cx);
                cx.notify();
            }
            "delete" => {
                if let Some((_, ch)) = self.ctrlp_query[self.ctrlp_caret_byte..]
                    .char_indices()
                    .next()
                {
                    let end = self.ctrlp_caret_byte + ch.len_utf8();
                    self.ctrlp_query.drain(self.ctrlp_caret_byte..end);
                    self.ctrlp_search(cx);
                    cx.notify();
                }
            }
            "left" => {
                self.ctrlp_caret_byte = self.ctrlp_query[..self.ctrlp_caret_byte]
                    .char_indices()
                    .next_back()
                    .map(|(index, _)| index)
                    .unwrap_or(0);
                cx.notify();
            }
            "right" => {
                self.ctrlp_caret_byte += self.ctrlp_query[self.ctrlp_caret_byte..]
                    .chars()
                    .next()
                    .map_or(0, char::len_utf8);
                cx.notify();
            }
            "home" => {
                self.ctrlp_caret_byte = 0;
                cx.notify();
            }
            "end" => {
                self.ctrlp_caret_byte = self.ctrlp_query.len();
                cx.notify();
            }
            "up" => {
                if self.ctrlp_selected > 0 {
                    self.palette_select(self.ctrlp_selected - 1);
                    cx.notify();
                }
            }
            "down" => {
                if self.ctrlp_selected + 1 < self.ctrlp_results.len() {
                    self.palette_select(self.ctrlp_selected + 1);
                    cx.notify();
                }
            }
            "pageup" => {
                self.palette_select(self.ctrlp_selected.saturating_sub(8));
                cx.notify();
            }
            "pagedown" => {
                self.palette_select(self.ctrlp_selected.saturating_add(8));
                cx.notify();
            }
            _ => {
                if event.keystroke.modifiers.control || event.keystroke.modifiers.alt {
                    return;
                }
                let ch = event
                    .keystroke
                    .key_char
                    .as_ref()
                    .and_then(|s| s.chars().next());
                if let Some(ch) = ch {
                    self.palette_insert_text(&ch.to_string(), cx);
                }
            }
        }
    }

    fn on_key_down(&mut self, event: &KeyDownEvent, _: &mut Window, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
        }
        let key_name = event.keystroke.key.to_lowercase().replace('_', "");

        if self.editor_pending_action.is_some() {
            match key_name.as_str() {
                "escape" => self.editor_resolve_pending(0, cx),
                "s" => self.editor_resolve_pending(1, cx),
                "d" => self.editor_resolve_pending(2, cx),
                _ => {}
            }
            return;
        }
        // The active overlay owns keyboard input before any global command,
        // picker, focused inspector field, or terminal forwarding.
        if self.ctrlp_open {
            return self.on_ctrlp_key(event, cx);
        }

        if event.keystroke.modifiers.control
            && !event.keystroke.modifiers.shift
            && !event.keystroke.modifiers.alt
            && key_name == "o"
        {
            self.open_project_directory(cx);
            return;
        }
        if event.keystroke.modifiers.control
            && event.keystroke.modifiers.alt
            && !event.keystroke.modifiers.shift
            && key_name == "n"
        {
            self.create_project(cx);
            return;
        }
        if event.keystroke.modifiers.control && event.keystroke.modifiers.shift && key_name == "p" {
            self.open_palette(true, cx);
            return;
        }
        // M14 commit input: while focused (Git tab), plain keys type the
        // message; Ctrl/Alt combinations fall through to global shortcuts
        // so they keep working while typing.
        if self.git_panel.commit_focused()
            && self.inspector_tab == InspectorTab::Git
            && !event.keystroke.modifiers.control
            && !event.keystroke.modifiers.alt
        {
            return self.on_commit_key(event, cx);
        }
        // Inspector search input: while focused (Files tab), plain keys
        // edit the filter; Ctrl/Alt combinations fall through. Esc clears
        // and releases, Enter opens the first match.
        if self.files_search_focused
            && self.inspector_tab == InspectorTab::Files
            && self.inspector_visible
            && (!event.keystroke.modifiers.control
                || matches!(key_name.as_str(), "enter" | "return" | "kpenter"))
            && !event.keystroke.modifiers.alt
        {
            return self.on_files_search_key(event, cx);
        }
        if event.keystroke.modifiers.control
            && !event.keystroke.modifiers.shift
            && !event.keystroke.modifiers.alt
            && key_name == "p"
        {
            self.open_palette(false, cx);
            return;
        }
        // UI v5 shell: Ctrl+B toggles Projects, Ctrl+Shift+B toggles the
        // Inspector, Ctrl+Shift+E/G/I select its Files/Git/Info tabs.
        // Ctrl+Shift+P/T/Q/V stay reserved (project/tab/close/paste).
        if event.keystroke.modifiers.control
            && !event.keystroke.modifiers.alt
            && (key_name == "b" || key_name == "e" || key_name == "g" || key_name == "i")
        {
            if key_name == "b" && !event.keystroke.modifiers.shift {
                self.projects_visible = !self.projects_visible;
                self.projects_resize = None;
                cx.notify();
                return;
            }
            if key_name == "b" && event.keystroke.modifiers.shift {
                self.inspector_visible = !self.inspector_visible;
                self.inspector_resize = None;
                cx.notify();
                return;
            }
            if event.keystroke.modifiers.shift
                && (key_name == "e" || key_name == "g" || key_name == "i")
            {
                self.inspector_tab = if key_name == "e" {
                    InspectorTab::Files
                } else if key_name == "g" {
                    InspectorTab::Git
                } else {
                    InspectorTab::Info
                };
                self.inspector_visible = true;
                self.git_panel.set_commit_focused(false);
                self.files_search_focused = false;
                self.files_vdrag = None;
                cx.notify();
                return;
            }
        }
        if event.keystroke.modifiers.control
            && event.keystroke.modifiers.shift
            && !event.keystroke.modifiers.alt
            && key_name == "s"
            && let Some(project) = self.coordinator.selected_project_id()
            && self.diff_panel.preview_open(project)
        {
            self.stage_current_diff_hunk(project, cx);
            return;
        }
        // M15 hunk navigation: Alt+N next / Alt+P previous within the
        // main-area diff preview tab. Plain Alt+letter is otherwise free
        // (Alt only pairs with PageUp/PageDown for tab/project jumps).
        if event.keystroke.modifiers.alt
            && !event.keystroke.modifiers.control
            && !event.keystroke.modifiers.shift
            && let Some(project) = self.coordinator.selected_project_id()
            && self.diff_panel.preview_open(project)
        {
            if key_name == "n" {
                self.diff_panel.next_hunk(project);
                self.reveal_current_diff_hunk(project);
                cx.notify();
                return;
            }
            if key_name == "p" {
                self.diff_panel.prev_hunk(project);
                self.reveal_current_diff_hunk(project);
                cx.notify();
                return;
            }
        }
        // Git row keyboard navigation: Alt+Up/Down moves the Source
        // Control selection, Alt+Enter opens the selected row's diff.
        // Guarded to the visible Git tab with the commit box unfocused so
        // terminal input (including plain arrows) never leaks.
        if event.keystroke.modifiers.alt
            && !event.keystroke.modifiers.control
            && !event.keystroke.modifiers.shift
            && self.inspector_visible
            && self.inspector_tab == InspectorTab::Git
            && !self.git_panel.commit_focused()
            && let Some(project) = self.coordinator.selected_project_id()
        {
            if key_name == "up" {
                self.git_panel.move_selection(project, -1);
                cx.notify();
                return;
            }
            if key_name == "down" {
                self.git_panel.move_selection(project, 1);
                cx.notify();
                return;
            }
            if (key_name == "enter" || key_name == "return" || key_name == "kpenter")
                && let Some(row) = self
                    .git_panel
                    .selected_path(project)
                    .cloned()
                    .and_then(|path| {
                        self.git_panel
                            .rows_for(project)
                            .into_iter()
                            .find(|row| row.path == path)
                    })
            {
                let staged = row.group == git_panel::GitGroup::Staged;
                self.git_select_path(project, row.path, staged);
                cx.notify();
                return;
            }
        }
        if event.keystroke.modifiers.control && event.keystroke.modifiers.shift && key_name == "t" {
            self.create_tab(cx);
            return;
        }
        if event.keystroke.modifiers.control && event.keystroke.modifiers.shift && key_name == "q" {
            if let (Some(project), Some(tab)) = (
                self.coordinator.selected_project_id(),
                self.coordinator.selected_tab_id(),
            ) {
                self.close_tab(project, tab, cx);
            }
            return;
        }
        // Direct project jump: Ctrl+Shift+1..9 selects the n-th project in
        // sidebar order. Shift applies to the character before GPUI reports
        // it (US layout: `!` for `1`, `@` for `2`, …), so both forms map.
        if event.keystroke.modifiers.control
            && event.keystroke.modifiers.shift
            && !event.keystroke.modifiers.alt
            && let Some(index) = project_jump_index(&key_name)
        {
            let projects = self.coordinator.projects();
            if index < projects.len() {
                let id = projects[index].id;
                let _ = self.dispatch_command(
                    OmaCommand::Project(ProjectCommand::Select { project: id }),
                    cx,
                );
            }
            return;
        }
        if (event.keystroke.modifiers.control || event.keystroke.modifiers.alt)
            && (key_name == "pageup" || key_name == "pagedown")
        {
            let projects = self.coordinator.projects();
            let is_project = event.keystroke.modifiers.alt;
            if is_project && !projects.is_empty() {
                let current = self.coordinator.selected_project_id();
                let index = projects
                    .iter()
                    .position(|p| Some(p.id) == current)
                    .unwrap_or(0);
                let next = if key_name == "pageup" {
                    index.checked_sub(1).unwrap_or(projects.len() - 1)
                } else {
                    (index + 1) % projects.len()
                };
                let id = projects[next].id;
                let _ = self.dispatch_command(
                    OmaCommand::Project(ProjectCommand::Select { project: id }),
                    cx,
                );
            } else if !is_project
                && let Some(project) = self.coordinator.active_project()
                && !project.tabs.is_empty()
            {
                let index = project
                    .tabs
                    .iter()
                    .position(|tab| Some(tab.id) == project.selected_tab)
                    .unwrap_or(0);
                let next = if key_name == "pageup" {
                    index.checked_sub(1).unwrap_or(project.tabs.len() - 1)
                } else {
                    (index + 1) % project.tabs.len()
                };
                let tab_id = project.tabs[next].id;
                let _ =
                    self.dispatch_command(OmaCommand::Tab(TabCommand::Select { tab: tab_id }), cx);
            }
            return;
        }

        // Clipboard paste: Ctrl+Shift+V.
        if event.keystroke.modifiers.control && event.keystroke.modifiers.shift && key_name == "v" {
            if let Some(document) = self
                .coordinator
                .selected_project_id()
                .and_then(|project| self.editor_active_doc(project))
            {
                if let Some(text) = cx.read_from_clipboard().and_then(|item| item.text()) {
                    self.editor_insert_text(document, &text, cx);
                }
            } else {
                self.paste(cx);
            }
            return;
        }

        // Explicit clipboard copy of the drag selection: Ctrl+Shift+C.
        if event.keystroke.modifiers.control && event.keystroke.modifiers.shift && key_name == "c" {
            if let Some(document) = self
                .coordinator
                .selected_project_id()
                .and_then(|project| self.editor_active_doc(project))
            {
                self.editor_copy(document, cx);
            } else if let Some(project) = self.coordinator.selected_project_id()
                && self.diff_panel.preview_open(project)
            {
                self.copy_current_diff_hunk(project, cx);
            } else {
                self.copy_selection(cx);
            }
            return;
        }

        // Workspace commands (M2 bindings, preserved for M4). These take
        // precedence over terminal input so layout never depends on the
        // foreground program.
        if event.keystroke.modifiers.control {
            let key = event.keystroke.key.as_str();
            if key == "{" || key == "[" {
                self.resize_focused(-0.05, cx);
                return;
            }
            if key == "}" || key == "]" {
                self.resize_focused(0.05, cx);
                return;
            }
        }
        if event.keystroke.modifiers.control
            && event.keystroke.modifiers.shift
            && !event.keystroke.modifiers.alt
        {
            match key_name.as_str() {
                "r" => {
                    self.split_focused(SplitDirection::Right, cx);
                    return;
                }
                "d" => {
                    self.split_focused(SplitDirection::Down, cx);
                    return;
                }
                "w" => {
                    self.close_focused(cx);
                    return;
                }
                "h" => {
                    self.focus_neighbor(SplitDirection::Left, cx);
                    return;
                }
                "j" => {
                    self.focus_neighbor(SplitDirection::Down, cx);
                    return;
                }
                "k" => {
                    self.focus_neighbor(SplitDirection::Up, cx);
                    return;
                }
                "l" => {
                    self.focus_neighbor(SplitDirection::Right, cx);
                    return;
                }
                "e" => {
                    self.equalize(cx);
                    return;
                }
                "t" => {
                    self.new_terminal_for_empty(cx);
                    return;
                }
                // History controls (M10). Shortcuts dispatch the same
                // semantic commands as IPC and CLI; destructive or
                // secret-disclosing actions use the two-step arm pattern.
                "o" => {
                    self.history_opt_in_key(cx);
                    return;
                }
                "g" => {
                    self.history_pause_key(cx);
                    return;
                }
                "x" => {
                    self.history_clear_key(cx);
                    return;
                }
                // M13 file actions on the tree's selected row. Same
                // semantic commands as IPC/CLI; failures surface as a
                // transient input notice.
                "y" => {
                    self.copy_selected_path(cx);
                    return;
                }
                "u" => {
                    self.reveal_selected_in_terminal(cx);
                    return;
                }
                _ => {}
            }
        }

        // M19 editor: an active document owns keystrokes after global
        // chrome shortcuts and inspector inputs, before terminal
        // forwarding. Workspace splits and palette toggles keep their
        // global behavior; clipboard chords target the visible surface.
        // Everything else
        // belongs to the document. Unhandled keys are swallowed rather than
        // forwarded to a terminal that does not own input.
        if self
            .coordinator
            .selected_project_id()
            .is_some_and(|project| self.editor_active_doc(project).is_some())
        {
            return self.on_editor_key(event, cx);
        }

        let Some(session_id) = self.focused_session_id() else {
            // Empty workspace: Enter/T also offers a fresh terminal.
            if key_name == "enter" {
                self.new_terminal_for_empty(cx);
            }
            return;
        };
        let Some(handle) = self.coordinator.registry().get(session_id) else {
            return;
        };

        // Scrollback when not in the alternate screen: Shift+PageUp/PageDown.
        if event.keystroke.modifiers.shift
            && (key_name == "pageup" || key_name == "pagedown")
            && !event.keystroke.modifiers.control
            && !event.keystroke.modifiers.alt
        {
            let in_alt_screen = handle
                .lock()
                .map(|s| s.viewport().is_alt_screen)
                .unwrap_or(false);
            if !in_alt_screen {
                let command = if key_name == "pageup" {
                    ScrollCommand::PageUp
                } else {
                    ScrollCommand::PageDown
                };
                if let Ok(mut session) = handle.lock() {
                    session.scroll(command);
                    self.snapshots.insert(session_id, session.viewport());
                }
                self.flash_scroll_indicator(session_id, cx);
                return;
            }
        }

        let (app_cursor, app_keypad) = handle
            .lock()
            .map(|s| (s.app_cursor(), s.app_keypad()))
            .unwrap_or((false, false));
        let Some(key_event) = translate_key(event, app_cursor, app_keypad) else {
            return;
        };
        let bytes = encode_key(&key_event);
        if bytes.is_empty() {
            return;
        }
        let _ = self.dispatch_command(
            OmaCommand::Terminal(TerminalCommand::SendBytes {
                session: session_id,
                data: bytes,
            }),
            cx,
        );
    }

    fn paste(&mut self, cx: &mut Context<Self>) {
        let text = cx
            .read_from_clipboard()
            .and_then(|item| item.text().map(|s| s.to_string()));
        let Some(text) = text else { return };
        let Some(session_id) = self.focused_session_id() else {
            return;
        };
        let Some(handle) = self.coordinator.registry().get(session_id) else {
            return;
        };
        let bytes = {
            let bracketed = handle.lock().map(|s| s.bracketed_paste()).unwrap_or(false);
            prepare_paste(&text, bracketed)
        };
        // Risky pastes (multiline, control characters) need an explicit
        // second identical paste within the arm window. Direct pastes send
        // at once, preserving the basic copy/paste workflow.
        if needs_paste_confirm(&text) && !self.confirm_paste(session_id, &bytes) {
            self.paste_arm = Some((session_id, bytes, Instant::now()));
            self.input_notice = Some(
                "Paste: multiline or control-character content — press Ctrl+Shift+V again within 8s to send.".into(),
            );
            tracing::warn!(target: "omaterm::terminal", "risky paste awaiting confirmation");
            cx.notify();
            return;
        }
        self.paste_arm = None;
        self.input_notice = None;
        let _ = self.dispatch_command(
            OmaCommand::Terminal(TerminalCommand::SendBytes {
                session: session_id,
                data: bytes,
            }),
            cx,
        );
    }

    /// Second identical paste within the window confirms; anything else arms.
    fn confirm_paste(&mut self, session: SessionId, bytes: &[u8]) -> bool {
        match &self.paste_arm {
            Some((id, armed, at))
                if *id == session
                    && armed.as_slice() == bytes
                    && at.elapsed() < PASTE_ARM_WINDOW =>
            {
                self.paste_arm = None;
                true
            }
            _ => false,
        }
    }

    /// File drop onto the workspace: safely escaped absolute paths go to the
    /// focused pane's shell, space-separated, without submitting. Drops never
    /// target a pane by screen coordinates; per-pane targeting is future work.
    fn on_file_drop(&mut self, paths: &ExternalPaths, cx: &mut Context<Self>) {
        let dropped = paths.paths();
        if dropped.is_empty() {
            return;
        }
        let Some(session_id) = self.focused_session_id() else {
            self.input_notice = Some("Drop: no focused terminal to receive paths.".into());
            cx.notify();
            return;
        };
        let data = format_dropped_paths(dropped);
        tracing::info!(target: "omaterm::terminal", count = dropped.len(), "file drop inserted as escaped paths");
        self.input_notice = None;
        let _ = self.dispatch_command(
            OmaCommand::Terminal(TerminalCommand::SendBytes {
                session: session_id,
                data,
            }),
            cx,
        );
    }

    /// Map a window-relative pointer position onto a grid cell for one pane.
    /// Returns `None` outside the painted grid (no clamping).
    fn pos_to_cell(
        &mut self,
        pane: PaneId,
        position: gpui::Point<Pixels>,
        cx: &App,
    ) -> Option<CellPoint> {
        let origin = self.grid_origins.get(&pane)?.get();
        let fonts = self.fonts(cx);
        let cell_width: f32 = fonts.cell_width.into();
        let line_height: f32 = fonts.line_height.into();
        if cell_width <= 0.0 || line_height <= 0.0 {
            return None;
        }
        let session_id = self.session_id_for_pane(pane)?;
        let snapshot = self.snapshots.get(&session_id)?;
        let cols = snapshot.cols as usize;
        let lines = snapshot.lines as usize;
        if cols == 0 || lines == 0 {
            return None;
        }
        let rel_x = f32::from(position.x) - f32::from(origin.x);
        let rel_y = f32::from(position.y) - f32::from(origin.y);
        let col = (rel_x / cell_width).floor() as isize;
        let row = (rel_y / line_height).floor() as isize;
        if row < 0 || col < 0 || row >= lines as isize || col >= cols as isize {
            return None;
        }
        Some(CellPoint::new(row as usize, col as usize))
    }

    fn on_mouse_down(
        &mut self,
        pane: PaneId,
        event: &MouseDownEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if self.shutting_down {
            return;
        }
        if self.coordinator.tree().find(pane).is_none() {
            return;
        }
        // Click focuses first; selection starts only inside the grid.
        if self.coordinator.focused() != Some(pane) {
            let _ = self.dispatch_command(OmaCommand::Pane(PaneCommand::Focus { pane }), cx);
        }
        // Typing belongs to the terminal again once its pane is clicked.
        self.git_panel.set_commit_focused(false);
        self.files_search_focused = false;
        window.focus(&self.focus_handle);
        let Some(cell) = self.pos_to_cell(pane, event.position, cx) else {
            return;
        };
        let Some(session_id) = self.session_id_for_pane(pane) else {
            return;
        };
        self.selecting = Some(pane);
        self.selections
            .insert(session_id, SelectionRange::new(cell, cell));
        cx.notify();
    }

    fn on_mouse_move(
        &mut self,
        pane: PaneId,
        event: &MouseMoveEvent,
        _window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if self.shutting_down {
            return;
        }
        if self.coordinator.tree().find(pane).is_none() {
            return;
        }
        // Hover is the pane focus model: keyboard input follows the pointer
        // without requiring a click. During a drag, the selection continues
        // to belong to the pane where the drag began.
        if self.coordinator.focused() != Some(pane) {
            let _ = self.dispatch_command(OmaCommand::Pane(PaneCommand::Focus { pane }), cx);
        }
        if self.selecting != Some(pane) {
            return;
        }
        let Some(cell) = self.pos_to_cell(pane, event.position, cx) else {
            return;
        };
        let Some(session_id) = self.session_id_for_pane(pane) else {
            return;
        };
        if let Some(selection) = self.selections.get_mut(&session_id) {
            selection.active = cell;
            cx.notify();
        }
    }

    fn on_mouse_up(
        &mut self,
        pane: PaneId,
        event: &MouseUpEvent,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if self.selecting != Some(pane) {
            return;
        }
        self.selecting = None;
        let Some(session_id) = self.session_id_for_pane(pane) else {
            return;
        };
        if let Some(cell) = self.pos_to_cell(pane, event.position, cx)
            && let Some(selection) = self.selections.get_mut(&session_id)
        {
            selection.active = cell;
        }
        match self.selections.get(&session_id).copied() {
            Some(range) if range.is_empty() => {
                self.selections.remove(&session_id);
            }
            Some(range) => {
                if let Some(snapshot) = self.snapshots.get(&session_id) {
                    let text = extract_text(snapshot, range);
                    if !text.is_empty() {
                        cx.write_to_primary(ClipboardItem::new_string(text));
                    }
                }
            }
            None => {}
        }
        cx.notify();
    }

    /// Explicit clipboard copy of the focused pane's selection (Ctrl+Shift+C).
    fn copy_selection(&mut self, cx: &mut Context<Self>) {
        let Some(session_id) = self.focused_session_id() else {
            return;
        };
        let Some(range) = self.selections.get(&session_id).copied() else {
            return;
        };
        if range.is_empty() {
            return;
        };
        let Some(snapshot) = self.snapshots.get(&session_id) else {
            return;
        };
        let text = extract_text(snapshot, range);
        if text.is_empty() {
            return;
        }
        cx.write_to_clipboard(ClipboardItem::new_string(text));
    }

    /// Flash the scroll thumb for [`SCROLL_INDICATOR_FADE_MS`], then hide it.
    /// Show a transient bottom-center confirmation. A new message
    /// replaces the current one and restarts the hide timer; stale timer
    /// tasks exit quietly. Pointer-transparent: never blocks pane clicks.
    fn show_toast(&mut self, message: String, cx: &mut Context<Self>) {
        self.toast = Some((message, Instant::now() + Duration::from_millis(TOAST_MS)));
        cx.notify();
        cx.spawn(async move |weak: WeakEntity<Self>, cx: &mut AsyncApp| {
            Timer::after(Duration::from_millis(TOAST_MS + 50)).await;
            let _ = weak.update(cx, |view, cx| {
                if view
                    .toast
                    .as_ref()
                    .is_some_and(|(_, deadline)| Instant::now() >= *deadline)
                {
                    view.toast = None;
                    cx.notify();
                }
            });
        })
        .detach();
    }

    fn flash_scroll_indicator(&mut self, session_id: SessionId, cx: &mut Context<Self>) {
        self.scroll_indicator_until.insert(
            session_id,
            Instant::now() + Duration::from_millis(SCROLL_INDICATOR_FADE_MS),
        );
        cx.notify();
        cx.spawn(async move |weak: WeakEntity<Self>, cx: &mut AsyncApp| {
            Timer::after(Duration::from_millis(SCROLL_INDICATOR_FADE_MS + 50)).await;
            let _ = weak.update(cx, |view, cx| {
                if view
                    .scroll_indicator_until
                    .get(&session_id)
                    .is_some_and(|deadline| Instant::now() >= *deadline)
                {
                    view.scroll_indicator_until.remove(&session_id);
                    cx.notify();
                }
            });
        })
        .detach();
    }

    fn on_scroll_wheel(
        &mut self,
        session_id: SessionId,
        event: &ScrollWheelEvent,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let Some(handle) = self.coordinator.registry().get(session_id) else {
            return;
        };
        let dy_lines: f32 = match event.delta {
            ScrollDelta::Pixels(point) => {
                let line_height: f32 = self.fonts(&*cx).line_height.into();
                if line_height <= 0.0 {
                    return;
                }
                f32::from(point.y) / line_height
            }
            ScrollDelta::Lines(point) => point.y,
        };
        let mut steps = dy_lines.round() as i32;
        if steps == 0 && dy_lines != 0.0 {
            steps = dy_lines.signum() as i32;
        }
        if steps == 0 {
            return;
        }

        let Ok(session) = handle.lock() else {
            return;
        };
        if session.viewport().is_alt_screen {
            let app_cursor = session.app_cursor();
            drop(session);
            let key = if steps > 0 { Key::Up } else { Key::Down };
            let repeats = steps.unsigned_abs().min(3) as usize;
            for _ in 0..repeats {
                let bytes = encode_key(&KeyEvent {
                    key: key.clone(),
                    modifiers: KeyModifiers::default(),
                    app_cursor,
                    app_keypad: false,
                });
                if let Ok(mut session) = handle.lock() {
                    let _ = session.write_input(&bytes);
                }
            }
            return;
        }
        drop(session);
        if let Ok(mut session) = handle.lock() {
            session.scroll(ScrollCommand::Lines(steps));
            self.snapshots.insert(session_id, session.viewport());
        }
        self.flash_scroll_indicator(session_id, cx);
    }

    fn render_node(
        &mut self,
        node: &PaneNode,
        _window: &mut Window,
        cx: &mut Context<Self>,
    ) -> gpui::AnyElement {
        match node.clone() {
            PaneNode::Pane(pane) => self.render_leaf(pane, cx),
            PaneNode::Split {
                axis,
                fraction,
                first,
                second,
                ..
            } => {
                let first = self.render_node(&first, _window, cx);
                let second = self.render_node(&second, _window, cx);
                let first = match axis {
                    SplitAxis::Horizontal => div()
                        .w(relative(fraction))
                        .h_full()
                        .min_w(px(0.0))
                        .min_h(px(0.0))
                        .overflow_hidden()
                        .flex()
                        .child(first),
                    SplitAxis::Vertical => div()
                        .h(relative(fraction))
                        .w_full()
                        .min_w(px(0.0))
                        .min_h(px(0.0))
                        .overflow_hidden()
                        .flex()
                        .child(first),
                };
                let second = match axis {
                    SplitAxis::Horizontal => div()
                        .w(relative(1.0 - fraction))
                        .h_full()
                        .min_w(px(0.0))
                        .min_h(px(0.0))
                        .overflow_hidden()
                        .flex()
                        .child(second),
                    SplitAxis::Vertical => div()
                        .h(relative(1.0 - fraction))
                        .w_full()
                        .min_w(px(0.0))
                        .min_h(px(0.0))
                        .overflow_hidden()
                        .flex()
                        .child(second),
                };
                let container = div()
                    .flex()
                    .flex_1()
                    .size_full()
                    .min_w(px(0.0))
                    .min_h(px(0.0))
                    .overflow_hidden();
                match axis {
                    SplitAxis::Horizontal => container.flex_row(),
                    SplitAxis::Vertical => container.flex_col(),
                }
                .child(first)
                .child(second)
                .into_any_element()
            }
        }
    }

    fn render_leaf(&mut self, pane: Pane, cx: &mut Context<Self>) -> gpui::AnyElement {
        let pane_id = pane.id;
        let fonts = self.fonts(cx);
        let origin = self
            .grid_origins
            .entry(pane_id)
            .or_insert_with(|| {
                Rc::new(Cell::new(gpui::Point {
                    x: px(0.0),
                    y: px(0.0),
                }))
            })
            .clone();

        let session_id = match pane.content {
            PaneContent::Terminal(id) => id,
            PaneContent::Empty => {
                if let Some((_, _, message)) = self.restored_failures.get(&pane_id).cloned() {
                    return div()
                        .flex()
                        .flex_1()
                        .flex_col()
                        .items_center()
                        .justify_center()
                        .gap_2()
                        .bg(rgb(0x18181B))
                        .text_color(rgb(0xFCA5A5))
                        .child("Restored shell could not start")
                        .child(message)
                        .child(
                            div()
                                .px_3()
                                .py_2()
                                .border_1()
                                .border_color(rgb(0x52525B))
                                .on_mouse_down(
                                    MouseButton::Left,
                                    cx.listener(move |view, _, window, cx| {
                                        window.focus(&view.focus_handle);
                                        view.retry_restored_pane(pane_id, cx);
                                    }),
                                )
                                .child("Retry"),
                        )
                        .into_any_element();
                }
                return div()
                    .flex()
                    .flex_1()
                    .m_1()
                    .items_center()
                    .justify_center()
                    .bg(rgb(0x18181B))
                    .text_color(rgb(0xA1A1AA))
                    .child("Empty pane")
                    .into_any_element();
            }
        };

        if !self.coordinator.registry().contains(session_id) {
            return div()
                .flex()
                .flex_1()
                .m_1()
                .items_center()
                .justify_center()
                .bg(rgb(0x18181B))
                .text_color(rgb(0xA1A1AA))
                .child("Terminal closed.")
                .into_any_element();
        }

        let Some(snapshot) = self.snapshots.get(&session_id).cloned() else {
            return div()
                .flex()
                .flex_1()
                .m_1()
                .items_center()
                .justify_center()
                .bg(rgb(0x18181B))
                .text_color(rgb(0xA1A1AA))
                .child("Starting shell…")
                .into_any_element();
        };

        let focused = self.coordinator.focused() == Some(pane_id);
        let cursor_color: Hsla = rgb(0xE4E4E7).into();
        let show_scrollbar = self
            .scroll_indicator_until
            .get(&session_id)
            .is_some_and(|deadline| Instant::now() < *deadline);
        let selection = self.selections.get(&session_id).copied().and_then(|range| {
            if range.is_empty() {
                None
            } else {
                Some(range.normalized())
            }
        });

        // UI v5 pane chrome: 32px header (status dot, OSC title, pid),
        // hover-equivalent toolbar on the focused pane (hover-to-focus
        // keeps this equivalent to the reference in practice), 28px footer
        // (shell, cwd, grid dims). Every string here is live session data.
        let (title, pid) = self
            .coordinator
            .registry()
            .get(session_id)
            .and_then(|handle| {
                handle.lock().ok().map(|session| {
                    (
                        session.title().unwrap_or("shell").to_string(),
                        session.child_pid(),
                    )
                })
            })
            .unwrap_or_else(|| ("shell".to_string(), 0));
        let attention = self.restored_failures.contains_key(&pane_id);
        let dot = if attention {
            crate::ui::theme::YELLOW
        } else {
            crate::ui::theme::GREEN
        };
        let cwd = self
            .observed_cwds
            .get(&pane_id)
            .map(|cwd| short_home_path(&cwd.path))
            .unwrap_or_default();
        let dims = self
            .grid_sizes
            .get(&session_id)
            .map(|(cols, rows)| format!("{cols}×{rows}"))
            .unwrap_or_default();
        let shell = shell_name();
        let header = div()
            .h(px(31.0))
            .flex()
            .flex_row()
            .items_center()
            .px_3()
            .gap_2()
            .flex_shrink_0()
            .border_b_1()
            .border_color(rgb(crate::ui::theme::BORDER))
            .bg(rgba(crate::ui::theme::with_alpha(
                crate::ui::theme::BG2,
                crate::ui::theme::PANE_HEADER_BG_OPACITY,
            )))
            .text_color(rgb(crate::ui::theme::TEXT2))
            .child(div().w(px(6.0)).h(px(6.0)).rounded_full().bg(rgb(dot)))
            .child(div().flex_1().truncate().child(title))
            .child(
                div()
                    .text_color(rgb(crate::ui::theme::MUTED))
                    .child(format!("{shell} · pid {pid}")),
            );
        let footer = div()
            .h(px(27.0))
            .flex()
            .flex_row()
            .items_center()
            .px_3()
            .gap_3()
            .flex_shrink_0()
            .border_t_1()
            .border_color(rgb(crate::ui::theme::BORDER))
            .text_color(rgb(crate::ui::theme::MUTED))
            .child(shell)
            .child(div().flex_1().truncate().child(cwd))
            .child(dims);
        let mut leaf = div()
            .flex()
            .flex_1()
            .flex_col()
            .size_full()
            .min_w(px(0.0))
            .min_h(px(0.0))
            .overflow_hidden()
            .relative()
            .bg(rgb(crate::ui::theme::BG2))
            .border_1()
            .border_color(rgb(if focused {
                crate::ui::theme::BLUE
            } else {
                crate::ui::theme::BORDER
            }))
            .child(header);
        leaf = leaf
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(move |view, event: &MouseDownEvent, window, cx| {
                    view.on_mouse_down(pane_id, event, window, cx);
                }),
            )
            .on_mouse_move(
                cx.listener(move |view, event: &MouseMoveEvent, window, cx| {
                    view.on_mouse_move(pane_id, event, window, cx);
                }),
            )
            .on_mouse_up(
                MouseButton::Left,
                cx.listener(move |view, event: &MouseUpEvent, window, cx| {
                    view.on_mouse_up(pane_id, event, window, cx);
                }),
            )
            .on_scroll_wheel(
                cx.listener(move |view, event: &ScrollWheelEvent, window, cx| {
                    view.on_scroll_wheel(session_id, event, window, cx);
                }),
            );
        // Toolbar floats over the canvas (never consumes grid space) and
        // shows on the focused pane. Restart/overflow arrive with their own
        // command/menu slices; only implemented actions render.
        if focused {
            let split_id = pane_id;
            let close_id = pane_id;
            leaf = leaf.child(
                div()
                    .absolute()
                    .top(px(39.0))
                    .right(px(8.0))
                    .flex()
                    .flex_row()
                    .items_center()
                    .gap(px(2.0))
                    .px_1()
                    .py(px(2.0))
                    .rounded_md()
                    .border_1()
                    .border_color(rgb(crate::ui::theme::BORDER2))
                    .bg(rgba(crate::ui::theme::with_alpha(
                        crate::ui::theme::ACTIVE_TAB_BG,
                        crate::ui::theme::PANE_TOOLBAR_BG_OPACITY,
                    )))
                    .text_color(rgb(crate::ui::theme::MUTED))
                    .child(
                        div()
                            .p(px(6.0))
                            .rounded_sm()
                            .on_mouse_down(
                                MouseButton::Left,
                                cx.listener(move |view, _, window, cx| {
                                    if view.shutting_down {
                                        return;
                                    }
                                    cx.stop_propagation();
                                    window.focus(&view.focus_handle);
                                    let _ = view.dispatch_command(
                                        OmaCommand::Pane(PaneCommand::Split {
                                            target: split_id,
                                            direction: SplitDirection::Right,
                                        }),
                                        cx,
                                    );
                                }),
                            )
                            .child(crate::ui::primitives::cmd_icon(
                                crate::ui::assets::COLUMNS,
                                14.0,
                                crate::ui::theme::MUTED,
                            )),
                    )
                    .child(
                        div()
                            .p(px(6.0))
                            .rounded_sm()
                            .on_mouse_down(
                                MouseButton::Left,
                                cx.listener(move |view, _, window, cx| {
                                    if view.shutting_down {
                                        return;
                                    }
                                    cx.stop_propagation();
                                    window.focus(&view.focus_handle);
                                    let _ = view.dispatch_command(
                                        OmaCommand::Pane(PaneCommand::Split {
                                            target: split_id,
                                            direction: SplitDirection::Down,
                                        }),
                                        cx,
                                    );
                                }),
                            )
                            .child(crate::ui::primitives::cmd_icon(
                                crate::ui::assets::ROWS,
                                14.0,
                                crate::ui::theme::MUTED,
                            )),
                    )
                    .child(
                        div()
                            .p(px(6.0))
                            .rounded_sm()
                            .on_mouse_down(
                                MouseButton::Left,
                                cx.listener(move |view, _, window, cx| {
                                    if view.shutting_down {
                                        return;
                                    }
                                    cx.stop_propagation();
                                    window.focus(&view.focus_handle);
                                    let _ = view.dispatch_command(
                                        OmaCommand::Pane(PaneCommand::Close { pane: close_id }),
                                        cx,
                                    );
                                }),
                            )
                            .child(crate::ui::primitives::cmd_icon(
                                crate::ui::assets::TRASH,
                                14.0,
                                crate::ui::theme::MUTED,
                            )),
                    ),
            );
        }
        leaf = leaf.child(
            div()
                .flex()
                .flex_1()
                .size_full()
                .min_w(px(0.0))
                .min_h(px(0.0))
                .child(canvas(
                    move |bounds, _, _| bounds,
                    move |bounds: Bounds<Pixels>,
                          bounds_prepaint: Bounds<Pixels>,
                          window: &mut Window,
                          cx: &mut App| {
                        // Grid origin for mouse-to-cell mapping. PTY sizing is
                        // handled in `render` via window geometry x pane
                        // fractions (deterministic); paint never resizes, so a
                        // transient canvas offer can never collapse a live grid.
                        origin.set(bounds_prepaint.origin);
                        paint_terminal(
                            bounds,
                            bounds_prepaint,
                            &PaintArgs {
                                snapshot: &snapshot,
                                fonts: &fonts,
                                cursor_color,
                                show_scrollbar,
                                selection,
                            },
                            window,
                            cx,
                        );
                    },
                )),
        );
        leaf.child(footer).into_any_element()
    }

    /// Match every live PTY grid to its pane's share of the window.
    ///
    /// Geometry comes from the window size times the core normalized pane
    /// rects (the same fractions the layout renders), never from transient
    /// canvas offers — so an unsettled layout pass can never collapse a
    /// live grid. Sessions resize in place: shell, scrollback, PID, and CWD
    /// survive. Offers below 2x2 are layout noise and are skipped (M2
    /// fractions guarantee larger panes at sane window sizes).
    fn resize_panes_to_window(&mut self, window: &Window, cx: &mut App) {
        let fonts = self.fonts(cx);
        let cell_width: f32 = fonts.cell_width.into();
        let line_height: f32 = fonts.line_height.into();
        if cell_width <= 0.0 || line_height <= 0.0 {
            return;
        }
        let viewport = window.viewport_size();
        // UI v5 chrome: the header row and global status vertically; the
        // Projects and Inspector panels (plus their resizers) horizontally.
        // One shared helper drives both this and the render composition so
        // sizing and painting can never disagree.
        let viewport_w: f32 = viewport.width.into();
        let viewport_h: f32 = viewport.height.into();
        let shell = crate::ui::geometry::shell_rects(
            viewport_w,
            viewport_h,
            self.projects_visible,
            self.projects_width,
            self.inspector_visible,
            self.inspector_width,
        );
        let window_width: f32 = px(shell.main_view.2).max(px(1.0)).into();
        let window_height: f32 = px(shell.main_view.3).max(px(1.0)).into();
        for pane_rect in self.coordinator.tree().pane_rects() {
            let Some(session_id) = self.coordinator.session_id_for_pane(pane_rect.pane) else {
                continue;
            };
            let cols =
                ((window_width * pane_rect.rect.width / cell_width).floor() as u16).clamp(2, 500);
            // Every leaf carries the v5 header (31px + 1px border) and
            // footer (27px + 1px border): the grid gets the canvas remainder
            // so rows are never hidden behind the chrome.
            let rows = ((window_height * pane_rect.rect.height - LEAF_CHROME_H) / line_height)
                .floor() as u16;
            let rows = rows.clamp(1, 500);
            if cols < 2 || rows < 2 {
                continue;
            }
            if self.grid_sizes.get(&session_id) == Some(&(cols, rows)) {
                continue;
            }
            if let Some(handle) = self.coordinator.registry().get(session_id)
                && let Ok(mut session) = handle.lock()
            {
                session.resize(cols, rows);
                // The engine grid changes synchronously, while PTY output
                // may be quiet. Publish the new viewport now so rendering
                // never draws an old-sized grid after a pane resize.
                self.snapshots.insert(session_id, session.viewport());
                self.grid_sizes.insert(session_id, (cols, rows));
            }
        }
    }
}

impl WorkspaceView {
    /// Vertical tree scrollbar: a thin rail beside the rows with a
    /// proportional thumb. Wheel scrolls, press-and-slide on the rail
    /// drags (deltas only — no window geometry needed), releases end the
    /// drag; stuck drags clear on the next pane click or row action.
    /// Hidden entirely when everything fits (nothing to scroll).
    fn render_tree_vscrollbar(
        &mut self,
        total: usize,
        visible: usize,
        rows_shown: usize,
        row_height: f32,
        cx: &mut Context<Self>,
    ) -> Div {
        if total <= visible {
            return div();
        }
        let (top_frac, height_frac) = files::scroll_thumb(total, visible, self.files_scroll_rows);
        let track_h = (rows_shown as f32 * row_height).max(1.0);
        let thumb_h = (height_frac * track_h)
            .max(files::MIN_THUMB_PX)
            .min(track_h);
        let top_px = top_frac * (track_h - thumb_h);
        let max_start = total.saturating_sub(visible) as f32;
        let travel = (track_h - thumb_h).max(1.0);
        div()
            .w(px(files::SCROLLBAR_WIDTH_PX))
            .h(px(track_h))
            .flex()
            .flex_col()
            .items_center()
            .child(
                div()
                    .w(px(6.0))
                    .h(px(track_h))
                    .rounded_full()
                    .bg(rgb(0x1F1F23))
                    .flex()
                    .flex_col()
                    .items_center()
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(|view, event: &MouseDownEvent, _, cx| {
                            if view.shutting_down {
                                return;
                            }
                            cx.stop_propagation();
                            view.files_vdrag = Some((f32::from(event.position.y), 0.0));
                            cx.notify();
                        }),
                    )
                    .on_mouse_move(cx.listener(move |view, event: &MouseMoveEvent, _, cx| {
                        let Some((last_y, mut acc)) = view.files_vdrag else {
                            return;
                        };
                        if view.shutting_down {
                            view.files_vdrag = None;
                            return;
                        }
                        let y = f32::from(event.position.y);
                        acc += (y - last_y) / travel * max_start;
                        let step = acc.trunc() as i32;
                        acc -= step as f32;
                        view.files_scroll_rows =
                            (view.files_scroll_rows as i32 + step).max(0) as usize;
                        view.files_vdrag = Some((y, acc));
                        cx.notify();
                    }))
                    .on_mouse_up(
                        MouseButton::Left,
                        cx.listener(|view, _, _, cx| {
                            view.files_vdrag = None;
                            cx.notify();
                        }),
                    )
                    .child(div().h(px(top_px)))
                    .child(
                        div()
                            .w_full()
                            .h(px(thumb_h))
                            .rounded_full()
                            .bg(rgb(0x52525B)),
                    )
                    .child(div().flex_1()),
            )
    }

    /// Inspector file tree for the selected project. Pure render from
    /// the panel row cache (no filesystem or dispatcher work per frame);
    /// clicks select/toggle through the dispatcher-owned refresh. Rows are
    /// the exact 28px height (`files::TREE_ROW_H`); scroll math uses the
    /// same constant so wheel, drag, and render always agree.
    fn render_files_tree(&mut self, bar: Div, viewport_height: f32, cx: &mut Context<Self>) -> Div {
        let Some(project) = self.coordinator.selected_project_id() else {
            return bar.child(
                div()
                    .px_2()
                    .py_1()
                    .text_color(rgb(0x71717A))
                    .child("No project"),
            );
        };
        let mut bar = bar;
        if self.files_panel.is_empty_root() {
            return bar.child(
                div()
                    .px_2()
                    .py_1()
                    .text_color(rgb(0x71717A))
                    .child("No files (no project root)"),
            );
        }
        let selected = self
            .files_panel
            .selected_path(project)
            .map(|path| path.to_path_buf());
        // Row-granular wheel scroll: visible window over the cached rows.
        // Header, footers, and hints stay fixed; only rows move. Rows are
        // the exact 28px inspector height; the search box filters cached
        // rows by file-name substring (display only, cache intact).
        let row_height = files::TREE_ROW_H;
        let visible = ((viewport_height / row_height) as usize).clamp(1, files::MAX_RENDER_ROWS);
        let query = self.files_search.clone();
        let all_rows: Vec<files::FileRow> = self
            .files_panel
            .rows_for(project)
            .unwrap_or_default()
            .iter()
            .filter(|row| files::row_matches_query(&row.path, &query))
            .cloned()
            .collect();
        let max_start = all_rows
            .len()
            .saturating_sub(visible.min(all_rows.len().max(1)));
        self.files_scroll_rows = self.files_scroll_rows.min(max_start);
        let rows: Vec<files::FileRow> = all_rows
            .iter()
            .skip(self.files_scroll_rows)
            .take(visible)
            .cloned()
            .collect();
        if rows.is_empty() {
            bar = bar.child(div().px_2().py_1().text_color(rgb(0x71717A)).child(
                if query.is_empty() {
                    "Empty directory"
                } else {
                    "No matches."
                },
            ));
        }
        // Paged top-level cap for crowded roots (`$HOME`): rendered above
        // the rows (not as a footer) so it stays reachable — the sidebar
        // has no scroll, and a full page of rows pushes any footer out of
        // view. Each press re-lists just the root wider (100 → 500 → 5000);
        // hides once the cap reaches the entry ceiling.
        let root_cap = self.files_root_cap(project);
        if self.files_panel.is_root_truncated() && root_cap < 5_000 {
            bar = bar.child(
                div()
                    .px_2()
                    .py_1()
                    .text_color(rgb(0xA1A1AA))
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(move |view, _, window, cx| {
                            if view.shutting_down {
                                return;
                            }
                            window.focus(&view.focus_handle);
                            let next = if view.files_root_cap(project) >= 500 {
                                5_000
                            } else {
                                500
                            };
                            view.files_root_caps.insert(project, next);
                            view.refresh_files(cx);
                        }),
                    )
                    .child(format!("Show more ({root_cap}+)")),
            );
        }
        // Rows render into their own column so the vertical scrollbar
        // rail can sit beside them (footers stay full-width below).
        let mut rows_col = div().flex().flex_col().flex_1().min_w(px(0.0));
        let rows_shown = rows.len();
        let rows_total = all_rows.len();
        // Git decorations for tree rows (mock M/U marks): untracked → U,
        // staged/unstaged → M. Computed once per frame over bounded rows.
        let git_mark = |path: &std::path::Path| -> Option<(char, u32)> {
            let status = self.git_panel.status_for(project)?;
            if status.untracked.iter().any(|entry| entry.path == path) {
                return Some(('U', crate::ui::theme::GREEN));
            }
            if status.staged.iter().any(|entry| entry.path == path)
                || status.unstaged.iter().any(|entry| entry.path == path)
            {
                return Some(('M', crate::ui::theme::YELLOW));
            }
            None
        };
        for row in rows {
            let is_selected = selected.as_ref() == Some(&row.path);
            let is_dir = row.kind == omaterm_core::FileKind::Directory;
            let name = row
                .path
                .file_name()
                .map(|name| name.to_string_lossy().into_owned())
                .unwrap_or_else(|| row.path.to_string_lossy().into_owned());
            let icon = files::icon_for(&row.path, row.kind, row.expanded);
            let icon_color =
                icon.color
                    .unwrap_or(if is_selected { 0xFA_FA_FA } else { 0xA1_A1_AA });
            let label = if is_dir && row.loading {
                format!("{name} …")
            } else {
                name
            };
            let path = row.path.clone();
            let dimmed = row.loading && !is_selected;
            // Keep the file identity stable. The prior pseudo-horizontal
            // scroll swapped this basename for a full path and left icons
            // behind; a true whole-row horizontal viewport comes in U4.
            let label_view = div().flex_1().min_w(px(0.0)).truncate().child(label);
            // Leading marker: explicit chevron for directories (mock),
            // 14px spacer for files. Badge: TS/{ } text marks, else the
            // file-type glyph.
            let marker: Div = if is_dir {
                div()
                    .w(px(14.0))
                    .flex_shrink_0()
                    .child(crate::ui::assets::icon(
                        if row.expanded {
                            crate::ui::assets::CHEVRON_DOWN
                        } else {
                            crate::ui::assets::CHEVRON_RIGHT
                        },
                        14.0,
                        crate::ui::theme::MUTED,
                    ))
            } else {
                div().w(px(14.0)).flex_shrink_0()
            };
            // Directories use Lucide folder glyphs (mock); other types
            // keep Nerd file marks plus TS/{ } badges until the full
            // Lucide file set lands (recorded P6 follow-up).
            let badge: Div = match files::file_badge(&row.path) {
                Some((text, color)) => div()
                    .w(px(18.0))
                    .flex_shrink_0()
                    .font_weight(crate::ui::metrics::BADGE_600)
                    .text_color(rgb(color))
                    .child(text),
                None if is_dir => div()
                    .w(px(18.0))
                    .flex_shrink_0()
                    .child(crate::ui::assets::icon(
                        if row.expanded {
                            crate::ui::assets::FOLDER_OPEN
                        } else {
                            crate::ui::assets::FOLDER
                        },
                        16.0,
                        crate::ui::theme::YELLOW,
                    )),
                None => div()
                    .w(px(18.0))
                    .flex()
                    .flex_shrink_0()
                    .items_center()
                    .justify_center()
                    .text_color(rgb(icon_color))
                    .child(icon.glyph.to_string()),
            };
            let mark = git_mark(&row.path);
            rows_col = rows_col.child(
                div()
                    .flex()
                    .flex_row()
                    .items_center()
                    .gap_2()
                    .px_2()
                    .h(px(files::TREE_ROW_H))
                    .pl(px(8.0 + row.depth as f32 * 16.0))
                    .hover(|s| s.bg(gpui::rgb(crate::ui::theme::ROW_HOVER_BG)))
                    .bg(rgb(if is_selected {
                        crate::ui::theme::TREE_SELECTED_BG
                    } else {
                        crate::ui::theme::PANEL
                    }))
                    .text_size(px(11.0))
                    .text_color(rgb(if is_selected {
                        0xFAFAFA
                    } else if dimmed {
                        0x52525B
                    } else {
                        crate::ui::theme::TEXT2
                    }))
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(move |view, event: &MouseDownEvent, window, cx| {
                            if view.shutting_down {
                                return;
                            }
                            window.focus(&view.focus_handle);
                            view.files_search_focused = false;
                            view.files_vdrag = None;
                            if is_dir {
                                view.toggle_file_row(project, path.clone(), true, cx);
                            } else if event.modifiers.control {
                                // M19 Phase E: Ctrl+click opens files in the
                                // native editor; plain click keeps the
                                // terminal-routed open (M13 contract).
                                view.files_panel.select(project, path.clone());
                                view.editor_open_document(project, path.clone(), cx);
                            } else {
                                view.files_panel.select(project, path.clone());
                                view.open_file_path(project, path.clone(), cx);
                                view.refresh_files(cx);
                            }
                        }),
                    )
                    .child(marker)
                    .child(badge)
                    // Long names ellipsize inside the fixed sidebar instead
                    // of stretching the row and breaking column alignment.
                    .child(label_view)
                    .child(
                        div()
                            .w(px(14.0))
                            .flex_shrink_0()
                            .text_color(rgb(mark
                                .map(|(_, color)| color)
                                .unwrap_or(crate::ui::theme::PANEL)))
                            .child(
                                mark.map(|(letter, _)| letter.to_string())
                                    .unwrap_or_default(),
                            ),
                    ),
            );
        }
        // Rows area: windowed rows beside the vertical scrollbar rail.
        // The rail spans exactly the shown rows (one unit each).
        let vbar = self.render_tree_vscrollbar(rows_total, visible, rows_shown, row_height, cx);
        bar = bar.child(div().flex().flex_row().child(rows_col).child(vbar));
        if self.files_panel.is_truncated() {
            bar = bar.child(
                div()
                    .px_2()
                    .py_1()
                    .text_color(rgb(0x71717A))
                    .child("(truncated: bounded tree)"),
            );
        }
        // File actions stay available through their real keyboard bindings.
        // A selected row no longer grows the tree with a duplicate action/help
        // strip; the contextual menu lands with the Files viewport rebuild.
        bar
    }

    /// M14 Source Control section: branch header with ahead/behind,
    /// staged/unstaged/untracked groups with counts, and per-file
    /// stage/unstage/discard actions through the `GitCommand` dispatcher.
    /// Pure render from the panel cache; refreshes land via `git_tick`.
    /// Row selection only highlights + logs (diff-on-select opens M15).
    fn render_git_panel(&mut self, bar: Div, cx: &mut Context<Self>) -> Div {
        let Some(project) = self.coordinator.selected_project_id() else {
            return bar.child(
                div()
                    .px_2()
                    .py_1()
                    .text_color(rgb(0x71717A))
                    .child("No project"),
            );
        };
        let mut bar = bar;
        // Discard arm banners (two-step confirm, single and bulk).
        if let Some(text) = self.git_panel.armed_text(project) {
            bar = bar.child(
                div()
                    .px_2()
                    .py_1()
                    .bg(rgb(workbench::WARN_BG))
                    .text_color(rgb(workbench::WARN_TEXT))
                    .child(text),
            );
        }
        if let Some(text) = self.git_panel.armed_all_text(project) {
            bar = bar.child(
                div()
                    .px_2()
                    .py_1()
                    .bg(rgb(workbench::WARN_BG))
                    .text_color(rgb(workbench::WARN_TEXT))
                    .child(text),
            );
        }
        // Explicit empty/error states (never a spinner forever).
        // Failure details are truncated: the stable code drives agents,
        // never the full stderr text.
        if let Some(empty) = self.git_panel.empty_for(project).cloned() {
            let snippet = |detail: &str| {
                let short: String = detail.chars().take(160).collect();
                if detail.chars().count() > 160 {
                    format!("{short}…")
                } else {
                    short
                }
            };
            let message = match &empty {
                git_panel::GitEmpty::NoRoot => "No project root".to_owned(),
                git_panel::GitEmpty::NotRepo => "Not a git repository".to_owned(),
                git_panel::GitEmpty::Unavailable(detail) => {
                    format!("Git unavailable: {}", snippet(detail))
                }
                git_panel::GitEmpty::Failed(detail) => {
                    format!("Git error: {}", snippet(detail))
                }
            };
            let amber = !matches!(
                empty,
                git_panel::GitEmpty::NoRoot | git_panel::GitEmpty::NotRepo
            );
            return bar.child(
                div()
                    .px_2()
                    .py_1()
                    .text_color(rgb(if amber { 0xFDE68A } else { 0x71717A }))
                    .child(message),
            );
        }
        let Some(status) = self.git_panel.status_for(project).cloned() else {
            return bar.child(
                div()
                    .px_2()
                    .py_1()
                    .text_color(rgb(0x71717A))
                    .child("Loading git status…"),
            );
        };
        // Branch + commit header: 11px branch row (icon, name, sync pill,
        // refresh), draft box (min 56px), full-width Commit button. The
        // draft stays single-line editing (multiline caret/selection is a
        // recorded P4 follow-up); the commit-options dropdown is omitted
        // until its menu has backend actions.
        let branch_name = status
            .branch
            .clone()
            .unwrap_or_else(|| "detached".to_string());
        let sync_pill = format!("↑{} ↓{}", status.ahead, status.behind);
        let can_commit = !status.staged.is_empty();
        let input_focused = self.git_panel.commit_focused();
        let draft = self.git_panel.commit_draft(project).to_owned();
        let input_content = if draft.is_empty() && !input_focused {
            div()
                .text_color(rgb(crate::ui::theme::MUTED))
                .child("Commit message")
        } else {
            div()
                .flex()
                .flex_row()
                .child(div().flex_1().min_w(px(0.0)).child(draft))
                .child(div().w(px(2.0)).h(px(15.0)).bg(rgb(if input_focused {
                    crate::ui::theme::TEXT
                } else {
                    crate::ui::theme::MUTED
                })))
        };
        bar = bar.child(
            div()
                .p_3()
                .flex()
                .flex_col()
                .gap_3()
                .border_b_1()
                .border_color(rgb(crate::ui::theme::BORDER))
                .child(
                    div()
                        .flex()
                        .flex_row()
                        .items_center()
                        .gap_2()
                        .text_size(px(11.0))
                        .text_color(rgb(crate::ui::theme::TEXT))
                        .child(crate::ui::assets::icon(
                            crate::ui::assets::GIT_BRANCH,
                            16.0,
                            crate::ui::theme::PURPLE,
                        ))
                        .child(branch_name)
                        .child(
                            crate::ui::metrics::text_role(
                                crate::ui::primitives::pill()
                                    .px(px(6.0))
                                    .py(px(2.0))
                                    .rounded_sm(),
                                crate::ui::metrics::META_9,
                            )
                            .text_color(rgb(crate::ui::theme::MUTED))
                            .child(sync_pill),
                        )
                        .child(div().flex_1())
                        .child(
                            div()
                                .p(px(6.0))
                                .rounded_sm()
                                .text_color(rgb(crate::ui::theme::MUTED))
                                .on_mouse_down(
                                    MouseButton::Left,
                                    cx.listener(|view, _, window, cx| {
                                        if view.shutting_down {
                                            return;
                                        }
                                        cx.stop_propagation();
                                        window.focus(&view.focus_handle);
                                        view.git_dirty_hint = true;
                                        cx.notify();
                                    }),
                                )
                                .child(crate::ui::primitives::cmd_icon(
                                    crate::ui::assets::REFRESH,
                                    14.0,
                                    crate::ui::theme::MUTED,
                                )),
                        ),
                )
                .child(
                    div()
                        .min_h(px(56.0))
                        .p_2()
                        .rounded_md()
                        .border_1()
                        .border_color(rgb(if input_focused {
                            crate::ui::theme::BLUE2
                        } else {
                            crate::ui::theme::BORDER
                        }))
                        .bg(rgb(crate::ui::theme::PILL_BG))
                        .text_size(px(11.0))
                        .text_color(rgb(crate::ui::theme::TEXT))
                        .on_mouse_down(
                            MouseButton::Left,
                            cx.listener(move |view, _, window, cx| {
                                if view.shutting_down {
                                    return;
                                }
                                cx.stop_propagation();
                                window.focus(&view.focus_handle);
                                view.git_panel.set_commit_focused(true);
                                cx.notify();
                            }),
                        )
                        .child(input_content),
                )
                .child(
                    crate::ui::metrics::text_role(
                        div()
                            .h(px(32.0))
                            .flex()
                            .flex_row()
                            .items_center()
                            .justify_center()
                            .rounded_md()
                            .bg(rgb(if can_commit {
                                crate::ui::theme::BLUE2
                            } else {
                                crate::ui::theme::PANEL2
                            })),
                        crate::ui::metrics::BODY_11_MEDIUM,
                    )
                    .text_color(rgb(if can_commit {
                        0xFFFFFF
                    } else {
                        crate::ui::theme::MUTED
                    }))
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(move |view, _, window, cx| {
                            if view.shutting_down {
                                return;
                            }
                            cx.stop_propagation();
                            window.focus(&view.focus_handle);
                            if can_commit {
                                view.git_commit_submit(project, cx);
                            }
                        }),
                    )
                    .child("Commit"),
                ),
        );
        if status.staged.is_empty() && status.unstaged.is_empty() && status.untracked.is_empty() {
            return bar.child(
                div()
                    .px_2()
                    .py_1()
                    .text_color(rgb(0x71717A))
                    .child("Working tree clean"),
            );
        }
        let selected = self
            .git_panel
            .selected_path(project)
            .map(|path| path.to_path_buf());
        // Two visible groups (mock): Staged Changes, then Changes holding
        // unstaged + untracked. Backend group identity still drives row
        // actions and the diff side; untracked rows open the unstaged side
        // (empty/error states render there, as before).
        let staged_count = status.staged.len();
        let changes_count = status.unstaged.len() + status.untracked.len();
        let staged_entries: Vec<(bool, omaterm_core::GitEntry)> = status
            .staged
            .iter()
            .take(git_panel::MAX_GIT_RENDER_ROWS)
            .map(|entry| (false, entry.clone()))
            .collect();
        let changes_entries: Vec<(bool, omaterm_core::GitEntry)> = status
            .unstaged
            .iter()
            .map(|entry| (false, entry.clone()))
            .chain(status.untracked.iter().map(|entry| (true, entry.clone())))
            .take(git_panel::MAX_GIT_RENDER_ROWS)
            .collect();
        let capped = staged_count + changes_count > staged_entries.len() + changes_entries.len();
        bar = bar.child(self.render_change_group(
            project,
            true,
            staged_count,
            staged_entries,
            selected.clone(),
            cx,
        ));
        bar = bar.child(self.render_change_group(
            project,
            false,
            changes_count,
            changes_entries,
            selected,
            cx,
        ));
        if capped || status.truncated {
            bar = bar.child(
                div()
                    .px_2()
                    .py_1()
                    .text_color(rgb(0x71717A))
                    .child("(truncated: bounded change list)"),
            );
        }
        // Footer: upstream identity left; the short HEAD hash has no
        // semantic query yet, so nothing renders on the right (recorded).
        bar = bar.child(
            div()
                .border_t_1()
                .border_color(rgb(crate::ui::theme::BORDER))
                .p_3()
                .flex()
                .flex_row()
                .items_center()
                .text_size(px(10.0))
                .text_color(rgb(crate::ui::theme::MUTED))
                .child(
                    status
                        .upstream
                        .clone()
                        .unwrap_or_else(|| status.branch.clone().unwrap_or_default()),
                ),
        );
        bar
    }

    /// Per-row Git actions in the reserved 56px slot: staged rows offer
    /// unstage + terminal-routed open; working-tree rows offer stage +
    /// discard (trash glyph for untracked paths, undo glyph otherwise).
    /// Every action dispatches through the shared `GitCommand` path.
    fn git_row_actions(
        &mut self,
        cx: &mut Context<Self>,
        project: ProjectId,
        path: std::path::PathBuf,
        open_path: std::path::PathBuf,
        staged_group: bool,
        untracked: bool,
    ) -> Div {
        let (first, second) = if staged_group {
            (
                (crate::ui::assets::UNSTAGE, GitRowAction::Unstage),
                (crate::ui::assets::EXTERNAL, GitRowAction::Open),
            )
        } else if untracked {
            (
                (crate::ui::assets::STAGE, GitRowAction::Stage),
                (crate::ui::assets::TRASH, GitRowAction::Discard),
            )
        } else {
            (
                (crate::ui::assets::STAGE, GitRowAction::Stage),
                (crate::ui::assets::UNDO, GitRowAction::Discard),
            )
        };
        div()
            .flex()
            .flex_row()
            .items_center()
            .child(self.git_action_button(cx, project, path.clone(), open_path.clone(), first))
            .child(self.git_action_button(cx, project, path, open_path, second))
    }

    /// One inspector Git icon button. The glyph → action pair is chosen by
    /// the caller; dispatch always flows through the shared commands.
    fn git_action_button(
        &mut self,
        cx: &mut Context<Self>,
        project: ProjectId,
        path: std::path::PathBuf,
        open_path: std::path::PathBuf,
        (asset, run): (&'static str, GitRowAction),
    ) -> Div {
        div()
            .p(px(6.0))
            .rounded_sm()
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(move |view, _, window, cx| {
                    if view.shutting_down {
                        return;
                    }
                    cx.stop_propagation();
                    window.focus(&view.focus_handle);
                    match run {
                        GitRowAction::Stage => {
                            view.git_stage_paths(project, vec![path.clone()], cx)
                        }
                        GitRowAction::Unstage => {
                            view.git_unstage_paths(project, vec![path.clone()], cx)
                        }
                        GitRowAction::Discard => view.git_discard_path(project, path.clone(), cx),
                        GitRowAction::Open => view.open_file_path(project, open_path.clone(), cx),
                    }
                }),
            )
            .child(crate::ui::assets::icon(
                asset,
                14.0,
                crate::ui::theme::MUTED,
            ))
    }

    /// One change-group section: 32px collapsible header (chevron, title,
    /// count pill, bulk actions) plus 40px rows. `staged_group` selects the
    /// staged section; `untracked` flags entries inside the working-tree
    /// section. Row actions reserve a fixed slot so hover state never
    /// reflows filenames.
    fn render_change_group(
        &mut self,
        project: ProjectId,
        staged_group: bool,
        count: usize,
        entries: Vec<(bool, omaterm_core::GitEntry)>,
        selected: Option<std::path::PathBuf>,
        cx: &mut Context<Self>,
    ) -> Div {
        let title = if staged_group {
            "Staged Changes"
        } else {
            "Changes"
        };
        let collapsed = self.git_panel.is_collapsed(project, staged_group);
        let mut section = div().flex().flex_col().flex_shrink_0();
        let mut header = div()
            .h(px(32.0))
            .flex()
            .flex_row()
            .items_center()
            .px_2()
            .gap_1()
            .text_size(px(10.0))
            .text_color(rgb(crate::ui::theme::MUTED))
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(move |view, _, window, cx| {
                    if view.shutting_down {
                        return;
                    }
                    cx.stop_propagation();
                    window.focus(&view.focus_handle);
                    view.git_panel.toggle_collapsed(project, staged_group);
                    cx.notify();
                }),
            )
            .child(
                div()
                    .w(px(14.0))
                    .flex_shrink_0()
                    .child(crate::ui::assets::icon(
                        if collapsed {
                            crate::ui::assets::CHEVRON_RIGHT
                        } else {
                            crate::ui::assets::CHEVRON_DOWN
                        },
                        14.0,
                        crate::ui::theme::MUTED,
                    )),
            )
            .child(title.to_uppercase())
            .child(
                div()
                    .ml(px(8.0))
                    .px(px(6.0))
                    .rounded_full()
                    .border_1()
                    .border_color(rgb(crate::ui::theme::PILL_BORDER))
                    .bg(rgb(crate::ui::theme::PILL_BG))
                    .text_size(px(9.0))
                    .child(format!("{count}")),
            )
            .child(div().flex_1());
        // Bulk actions: unstage-all on staged, stage-all + discard-all on
        // working tree. Discard-all keeps the two-step arm.
        if staged_group {
            header = header.child(
                div()
                    .p(px(6.0))
                    .rounded_sm()
                    .text_color(rgb(crate::ui::theme::MUTED))
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(move |view, _, window, cx| {
                            if view.shutting_down {
                                return;
                            }
                            cx.stop_propagation();
                            window.focus(&view.focus_handle);
                            view.git_unstage_all(project, cx);
                        }),
                    )
                    .child(crate::ui::primitives::cmd_icon(
                        crate::ui::assets::UNSTAGE,
                        14.0,
                        crate::ui::theme::MUTED,
                    )),
            );
        } else {
            header = header
                .child(
                    div()
                        .p(px(6.0))
                        .rounded_sm()
                        .text_color(rgb(crate::ui::theme::MUTED))
                        .on_mouse_down(
                            MouseButton::Left,
                            cx.listener(move |view, _, window, cx| {
                                if view.shutting_down {
                                    return;
                                }
                                cx.stop_propagation();
                                window.focus(&view.focus_handle);
                                view.git_stage_all(project, cx);
                            }),
                        )
                        .child(crate::ui::primitives::cmd_icon(
                            crate::ui::assets::STAGE,
                            14.0,
                            crate::ui::theme::MUTED,
                        )),
                )
                .child(
                    div()
                        .p(px(6.0))
                        .rounded_sm()
                        .text_color(rgb(crate::ui::theme::MUTED))
                        .on_mouse_down(
                            MouseButton::Left,
                            cx.listener(move |view, _, window, cx| {
                                if view.shutting_down {
                                    return;
                                }
                                cx.stop_propagation();
                                window.focus(&view.focus_handle);
                                view.git_discard_all(project, cx);
                            }),
                        )
                        .child(crate::ui::primitives::cmd_icon(
                            crate::ui::assets::UNDO,
                            14.0,
                            crate::ui::theme::MUTED,
                        )),
                );
        }
        section = section.child(header);
        if collapsed {
            return section;
        }
        for (untracked, entry) in entries {
            let is_selected = selected.as_ref() == Some(&entry.path);
            let name = entry
                .path
                .file_name()
                .map(|name| name.to_string_lossy().into_owned())
                .unwrap_or_else(|| entry.path.to_string_lossy().into_owned());
            let dir = entry
                .path
                .parent()
                .map(|parent| parent.to_string_lossy().into_owned())
                .unwrap_or_default();
            // Status letter from porcelain codes (mock M/A/U/D column).
            let (mark, mark_color) = if untracked {
                ("U", crate::ui::theme::GREEN)
            } else if entry.x == 'A' {
                ("A", crate::ui::theme::GREEN)
            } else if entry.x == 'D' || entry.y == 'D' {
                ("D", crate::ui::theme::RED)
            } else {
                ("M", crate::ui::theme::YELLOW)
            };
            let path = entry.path.clone();
            let row = div()
                .h(px(40.0))
                .flex()
                .flex_row()
                .items_center()
                .gap_2()
                .px_3()
                .hover(|s| s.bg(gpui::rgb(crate::ui::theme::ROW_HOVER_BG)))
                .text_size(px(11.0))
                .text_color(rgb(if is_selected {
                    0xFAFAFA
                } else {
                    crate::ui::theme::TEXT2
                }))
                .on_mouse_down(
                    MouseButton::Left,
                    cx.listener(move |view, event: &MouseDownEvent, window, cx| {
                        if view.shutting_down {
                            return;
                        }
                        window.focus(&view.focus_handle);
                        view.files_search_focused = false;
                        // M19 Phase E: Ctrl+click opens the path in the
                        // native editor; plain click keeps diff-on-select.
                        if event.modifiers.control {
                            view.editor_open_document(project, path.clone(), cx);
                        } else {
                            view.git_select_path(project, path.clone(), staged_group);
                        }
                        cx.notify();
                    }),
                )
                .child(
                    div()
                        .flex()
                        .flex_1()
                        .flex_row()
                        .items_center()
                        .gap_2()
                        .min_w(px(0.0))
                        .child(match files::file_badge(&entry.path) {
                            Some((text, color)) => div()
                                .w(px(20.0))
                                .h_full()
                                .flex()
                                .items_center()
                                .justify_center()
                                .flex_shrink_0()
                                .text_size(px(10.0))
                                .font_weight(crate::ui::metrics::BADGE_600)
                                .text_color(rgb(color))
                                .child(text),
                            None => div()
                                .w(px(20.0))
                                .h_full()
                                .flex()
                                .items_center()
                                .justify_center()
                                .flex_shrink_0()
                                .child(crate::ui::assets::icon(
                                    crate::ui::assets::FILE_TEXT,
                                    16.0,
                                    crate::ui::theme::MUTED,
                                )),
                        })
                        .child(
                            div()
                                .flex()
                                .flex_1()
                                .flex_col()
                                .justify_center()
                                .min_w(px(0.0))
                                .child(
                                    crate::ui::metrics::text_role(
                                        div().truncate(),
                                        crate::ui::metrics::BODY_11,
                                    )
                                    .child(name),
                                )
                                // Root-level files have no parent label. An
                                // empty text element still reserves line height
                                // and lifts the filename above the icon center.
                                .when(!dir.is_empty(), |column| {
                                    column.child(
                                        crate::ui::metrics::text_role(
                                            div().truncate(),
                                            crate::ui::metrics::META_9,
                                        )
                                        .text_color(rgb(crate::ui::theme::MUTED))
                                        .child(dir),
                                    )
                                }),
                        ),
                )
                .child(
                    div()
                        .flex()
                        .flex_row()
                        .items_center()
                        .gap(px(2.0))
                        .flex_shrink_0()
                        .min_w(px(56.0))
                        .justify_end()
                        .text_color(rgb(crate::ui::theme::MUTED))
                        .child({
                            let actions_path = entry.path.clone();
                            let actions_open = entry.path.clone();
                            self.git_row_actions(
                                cx,
                                project,
                                actions_path,
                                actions_open,
                                staged_group,
                                untracked,
                            )
                        }),
                )
                .child(
                    div()
                        .w(px(14.0))
                        .flex_shrink_0()
                        .text_size(px(10.0))
                        .text_color(rgb(mark_color))
                        .child(mark),
                );
            section = section.child(row);
        }
        section
    }

    /// Main-area diff preview tab (M15): the selected change's unified
    /// diff, opened by clicking a Git row. The tab holds a file diff,
    /// never a terminal — core tabs stay terminal-only (M13/M17 scope).
    /// Refreshes land via `diff_tick`; stage/open use shared semantic
    /// operations. The bounded parsed source is flattened into a cached
    /// virtual row list so only the visible range enters the GPUI tree.
    fn render_diff_preview(
        &mut self,
        project: ProjectId,
        main_view_width: f32,
        window: &Window,
        cx: &mut Context<Self>,
    ) -> Div {
        let staged = self.diff_panel.show_staged(project);
        let mut bar = div()
            .flex()
            .flex_col()
            .flex_1()
            .size_full()
            .min_h(px(0.0))
            .bg(rgb(crate::ui::theme::EDITOR_BG));
        let Some(path) = self
            .git_panel
            .selected_path(project)
            .or_else(|| self.diff_panel.selected_file(project))
            .map(|path| path.to_path_buf())
        else {
            return bar.child(
                div()
                    .px_2()
                    .py_1()
                    .text_color(rgb(0x71717A))
                    .child("Select a Git change to view its diff"),
            );
        };
        // v5 file-action header (40px): filename + staged/working-tree
        // scope left; whole-file Stage/Unstage, Discard (working tree
        // only), and Split/Inline toggle right. Stage buttons act on the
        // whole file through the shared Git path — never per-hunk.
        let file_name = path
            .file_name()
            .map(|name| name.to_string_lossy().into_owned())
            .unwrap_or_else(|| path.to_string_lossy().into_owned());
        let scope_label = if staged {
            "Staged · HEAD ↔ INDEX"
        } else {
            "Working Tree · INDEX ↔ WORKING TREE"
        };
        let mode = self.diff_panel.diff_mode(project);
        let stage_path = path.clone();
        let discard_path = path.clone();
        let mut actions = div()
            .flex()
            .flex_row()
            .items_center()
            .gap(px(6.0))
            .text_size(px(10.0))
            .child(
                div()
                    .px(px(10.0))
                    .py(px(4.0))
                    .rounded_md()
                    .border_1()
                    .border_color(rgb(crate::ui::theme::PILL_BORDER))
                    .bg(rgb(crate::ui::theme::PILL_BG))
                    .text_color(rgb(crate::ui::theme::TEXT2))
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(move |view, _, window, cx| {
                            if view.shutting_down {
                                return;
                            }
                            cx.stop_propagation();
                            window.focus(&view.focus_handle);
                            if staged {
                                view.diff_unstage_file(project, stage_path.clone(), cx);
                            } else {
                                view.diff_stage_file(project, stage_path.clone(), cx);
                            }
                        }),
                    )
                    .child(if staged { "Unstage File" } else { "Stage File" }),
            );
        if !staged {
            actions = actions.child(
                div()
                    .px(px(10.0))
                    .py(px(4.0))
                    .rounded_md()
                    .text_color(rgb(crate::ui::theme::RED))
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(move |view, _, window, cx| {
                            if view.shutting_down {
                                return;
                            }
                            cx.stop_propagation();
                            window.focus(&view.focus_handle);
                            view.git_discard_path(project, discard_path.clone(), cx);
                        }),
                    )
                    .child("Discard"),
            );
        }
        actions = actions
            .child(
                div()
                    .w(px(1.0))
                    .h(px(16.0))
                    .bg(rgb(crate::ui::theme::BORDER)),
            )
            .child({
                let pill = |label: &'static str, selected: bool, next: diff_panel::DiffMode| {
                    div()
                        .px(px(10.0))
                        .py(px(4.0))
                        .rounded_md()
                        .border_1()
                        .border_color(rgb(if selected {
                            crate::ui::theme::PILL_BORDER
                        } else {
                            crate::ui::theme::EDITOR_BG
                        }))
                        .bg(rgb(if selected {
                            crate::ui::theme::PILL_BG
                        } else {
                            crate::ui::theme::EDITOR_BG
                        }))
                        .text_color(rgb(if selected {
                            0xFFFFFF
                        } else {
                            crate::ui::theme::MUTED
                        }))
                        .on_mouse_down(
                            MouseButton::Left,
                            cx.listener(move |view, _, window, cx| {
                                if view.shutting_down {
                                    return;
                                }
                                cx.stop_propagation();
                                window.focus(&view.focus_handle);
                                view.switch_diff_mode(project, next);
                                cx.notify();
                            }),
                        )
                        .child(label)
                };
                div()
                    .flex()
                    .flex_row()
                    .items_center()
                    .child(pill(
                        "Split",
                        mode == diff_panel::DiffMode::Split,
                        diff_panel::DiffMode::Split,
                    ))
                    .child(pill(
                        "Inline",
                        mode == diff_panel::DiffMode::Inline,
                        diff_panel::DiffMode::Inline,
                    ))
            });
        bar = bar.child(
            div()
                .h(px(40.0))
                .flex()
                .flex_row()
                .items_center()
                .flex_shrink_0()
                .gap_2()
                .px_3()
                .border_b_1()
                .border_color(rgb(crate::ui::theme::BORDER))
                .bg(rgb(crate::ui::theme::PANEL))
                .child(crate::ui::assets::icon(
                    crate::ui::assets::GIT_COMPARE,
                    16.0,
                    crate::ui::theme::GREEN,
                ))
                .child(
                    div()
                        .text_size(px(12.0))
                        .text_color(rgb(crate::ui::theme::TEXT))
                        .child(file_name),
                )
                .child(
                    div()
                        .text_size(px(10.0))
                        .text_color(rgb(crate::ui::theme::MUTED))
                        .child(scope_label),
                )
                .child(div().flex_1())
                .child(actions),
        );
        // Explicit empty/error states (never a spinner forever). Details
        // are bounded; diff bodies and stderr never enter logs.
        if let Some(empty) = self.diff_panel.empty_for(project, staged).cloned() {
            let short = |detail: &str| {
                let text: String = detail.chars().take(160).collect();
                if detail.chars().count() > 160 {
                    format!("{text}…")
                } else {
                    text
                }
            };
            let message = match &empty {
                diff_panel::DiffEmpty::NoRoot => "No project root".to_owned(),
                diff_panel::DiffEmpty::NotRepo => "Not a git repository".to_owned(),
                diff_panel::DiffEmpty::Cancelled => "Diff request cancelled".to_owned(),
                diff_panel::DiffEmpty::Unavailable(detail) => {
                    format!("Git unavailable: {}", short(detail))
                }
                diff_panel::DiffEmpty::Failed(detail) => {
                    format!("Git error: {}", short(detail))
                }
            };
            let amber = !matches!(
                empty,
                diff_panel::DiffEmpty::NoRoot
                    | diff_panel::DiffEmpty::NotRepo
                    | diff_panel::DiffEmpty::Cancelled
            );
            return bar.child(
                div()
                    .px_2()
                    .py_1()
                    .text_color(rgb(if amber { 0xFDE68A } else { 0x71717A }))
                    .child(message),
            );
        }
        let Some(info) = self.diff_panel.diff_shared_for(project, staged) else {
            return bar.child(
                div()
                    .px_2()
                    .py_1()
                    .text_color(rgb(0x71717A))
                    .child("Loading diff…"),
            );
        };
        if info.files.is_empty() {
            return bar.child(
                div()
                    .px_2()
                    .py_1()
                    .text_color(rgb(0x71717A))
                    .child("No diff for selected change"),
            );
        }
        let Some(file) = info.files.iter().find(|file| file.path == path) else {
            return bar.child(
                div()
                    .px_2()
                    .py_1()
                    .text_color(rgb(0x71717A))
                    .child("Selected change is no longer in this diff"),
            );
        };
        if file.binary {
            return bar.child(
                div()
                    .px_2()
                    .py_1()
                    .text_color(rgb(0x71717A))
                    .child("binary, not shown"),
            );
        }
        {
            let count = file.hunks.len();
            let cursor = self
                .diff_panel
                .selected_hunk(project)
                .min(count.saturating_sub(1));
            let mono = mono_family_for_chrome(&*cx, self.font_family.as_deref());
            let mode = self.diff_panel.diff_mode(project);
            // Split side headers identify the compared revisions once per
            // file (mock): staged HEAD↔INDEX, working tree INDEX↔WORKTREE.
            if mode == diff_panel::DiffMode::Split {
                let (left_rev, right_rev) = if staged {
                    ("HEAD", "INDEX")
                } else {
                    ("INDEX", "WORKING TREE")
                };
                let side_header = |rev: &str| {
                    div()
                        .flex_1()
                        .min_w(px(0.0))
                        .h(px(28.0))
                        .flex()
                        .flex_row()
                        .items_center()
                        .px_3()
                        .border_b_1()
                        .border_color(rgb(crate::ui::theme::BORDER))
                        .bg(rgb(crate::ui::theme::EDITOR_SIDE_HEADER_BG))
                        .text_size(px(10.0))
                        .text_color(rgb(crate::ui::theme::MUTED))
                        .child(format!(
                            "{rev} · {}",
                            file.path.to_string_lossy().rsplit('/').next().unwrap_or("")
                        ))
                };
                bar = bar.child(
                    div()
                        .flex()
                        .flex_row()
                        .flex_shrink_0()
                        .child(side_header(left_rev))
                        .child(
                            div()
                                .w(px(1.0))
                                .flex_shrink_0()
                                .bg(rgb(crate::ui::theme::BORDER)),
                        )
                        .child(side_header(right_rev)),
                );
            }
            let rows = self
                .diff_panel
                .preview_rows_for(project, staged)
                .unwrap_or_else(|| std::sync::Arc::from([]));
            let row_count = rows.len();
            let stage_path = path.clone();
            let copy_info = Arc::clone(&info);
            let copy_path = path.clone();
            let row_mono = mono.clone();
            let scroll = self
                .diff_scroll_handles
                .entry((project, staged, path.clone(), mode))
                .or_default();
            if !scroll
                .widths
                .as_ref()
                .is_some_and(|(source, family, _)| Arc::ptr_eq(source, &rows) && family == &mono)
            {
                if let Some((previous, _, _)) = &scroll.widths {
                    let base = scroll.rows.0.borrow().base_handle.clone();
                    let offset =
                        diff_panel::remap_preview_offset(previous, &rows, base.offset().y.into());
                    base.set_offset(gpui::point(px(0.0), px(offset)));
                }
                let width = measured_diff_width(&rows, &mono, window);
                scroll.widths = Some((Arc::clone(&rows), mono.clone(), width));
            }
            let code_width = scroll.widths.as_ref().unwrap().2;
            let row_width = match mode {
                diff_panel::DiffMode::Split => main_view_width,
                diff_panel::DiffMode::Inline => main_view_width.max(code_width + 92.0 + 16.0),
            };
            let diff_scroll = scroll.rows.clone();
            let old_scroll = scroll.old.clone();
            let new_scroll = scroll.new.clone();
            let inline_scroll = scroll.inline.clone();
            let rows = uniform_list(
                "diff-preview-rows",
                row_count,
                cx.processor(move |_view, range: std::ops::Range<usize>, _window, _cx| {
                    range
                        .map(|row_index| {
                            let row = rows[row_index].clone();
                            let element = match row {
                                diff_panel::PreviewRow::HunkHeader {
                                    hunk,
                                    header,
                                    ..
                                } => {
                                    let current = hunk == cursor;
                                    div()
                                        .id(row_index)
                                        .h(px(21.0))
                                        .flex_shrink_0()
                                        .px_2()
                                        .flex()
                                        .items_center()
                                        .text_size(px(10.0))
                                        .text_color(rgb(if current {
                                            crate::ui::theme::TEXT2
                                        } else {
                                            crate::ui::theme::MUTED
                                        }))
                                    .child(format!("{header}{}", if current { " ◀" } else { "" }))
                                }
                                diff_panel::PreviewRow::HunkActions {
                                    hunk,
                                    id,
                                    can_stage,
                                    can_open,
                                    can_copy,
                                } => {
                                    let mut actions = div()
                                        .id(row_index)
                                        .h(px(21.0))
                                        .flex_shrink_0()
                                        .px_2()
                                        .flex()
                                        .items_center()
                                        .gap_2()
                                        .text_size(px(10.0))
                                        .text_color(rgb(crate::ui::theme::TEXT2));
                                    if can_copy {
                                        let copy_info = Arc::clone(&copy_info);
                                        let copy_path = copy_path.clone();
                                        actions = actions.child(
                                            div()
                                                .cursor_pointer()
                                                .child("Copy Hunk")
                                                .on_mouse_down(
                                                    MouseButton::Left,
                                                    _cx.listener(move |view, _, window, cx| {
                                                        cx.stop_propagation();
                                                        window.focus(&view.focus_handle);
                                                        let text = copy_info
                                                            .files
                                                            .iter()
                                                            .find(|file| file.path == copy_path)
                                                            .and_then(|file| {
                                                                let hunk = file.hunks.get(hunk)?;
                                                                (!file.binary
                                                                    && !file.truncated
                                                                    && !hunk.truncated)
                                                                    .then(|| diff_panel::unified_hunk_text(hunk))
                                                            });
                                                        if let Some(text) = text {
                                                            cx.write_to_clipboard(
                                                                ClipboardItem::new_string(text),
                                                            );
                                                            view.show_toast("Copied hunk".into(), cx);
                                                        } else {
                                                            view.input_notice = Some("Selected hunk cannot be copied completely.".into());
                                                            cx.notify();
                                                        }
                                                    }),
                                                ),
                                        );
                                    }
                                    if can_stage {
                                        let stage_path = stage_path.clone();
                                        actions = actions.child(
                                            div()
                                                .cursor_pointer()
                                                .child("Stage Hunk")
                                                .on_mouse_down(
                                            MouseButton::Left,
                                            _cx.listener(move |view, _, window, cx| {
                                                cx.stop_propagation();
                                                window.focus(&view.focus_handle);
                                                view.diff_stage_hunk(project, stage_path.clone(), id, cx);
                                            }),
                                                ),
                                        );
                                    }
                                    if can_open {
                                        let open_path = stage_path.clone();
                                        actions = actions.child(
                                            div()
                                                .cursor_pointer()
                                                .text_color(rgb(crate::ui::theme::BLUE))
                                                .child("Open in Terminal")
                                                .on_mouse_down(
                                                    MouseButton::Left,
                                                    _cx.listener(move |view, _, window, cx| {
                                                        cx.stop_propagation();
                                                        window.focus(&view.focus_handle);
                                                        view.open_file_path(project, open_path.clone(), cx);
                                                    }),
                                                ),
                                        );
                                    }
                                    actions
                                }
                                diff_panel::PreviewRow::Split(row) => div()
                                    .id(row_index)
                                    .h(px(21.0))
                                    .flex_shrink_0()
                                    .flex()
                                    .flex_row()
                                    .child(split_cell(row.old.as_ref(), &row_mono, &old_scroll, code_width).id("diff-old-side"))
                                    .child(
                                        div()
                                            .w(px(1.0))
                                            .flex_shrink_0()
                                            .bg(rgb(crate::ui::theme::BORDER)),
                                    )
                                    .child(split_cell(row.new.as_ref(), &row_mono, &new_scroll, code_width).id("diff-new-side")),
                                diff_panel::PreviewRow::Inline(row) => inline_row(&row, &row_mono)
                                    .id(row_index)
                                    .flex_shrink_0(),
                                diff_panel::PreviewRow::NoNewline { old, new } => div()
                                    .id(row_index)
                                    .h(px(21.0))
                                    .flex_shrink_0()
                                    .px_2()
                                    .flex()
                                    .items_center()
                                    .font_family(row_mono.clone())
                                    .text_size(px(10.0))
                                    .text_color(rgb(crate::ui::theme::MUTED))
                                    .child(format!(
                                        "\\ No newline at end of {}{}",
                                        if old { "old" } else { "" },
                                        if old && new {
                                            " and new file"
                                        } else if new {
                                            "new file"
                                        } else {
                                            " file"
                                        }
                                    )),
                                diff_panel::PreviewRow::HunkTruncated => div()
                                    .id(row_index)
                                    .h(px(21.0))
                                    .flex_shrink_0()
                                    .px_2()
                                    .flex()
                                    .items_center()
                                    .text_size(px(10.0))
                                    .text_color(rgb(0x71717A))
                                    .child("… (hunk truncated)"),
                                diff_panel::PreviewRow::FileTruncated => div()
                                    .id(row_index)
                                    .h(px(21.0))
                                    .flex_shrink_0()
                                    .px_2()
                                    .flex()
                                    .items_center()
                                    .text_size(px(10.0))
                                    .text_color(rgb(0x71717A))
                                    .child("… (file truncated)"),
                            };
                            element.w(px(row_width))
                        })
                        .collect::<Vec<_>>()
                }),
            )
            .track_scroll(diff_scroll)
            .w(px(row_width))
            .flex_shrink_0()
            .h_full()
            .map(|mut list| {
                list.style().restrict_scroll_to_axis = Some(true);
                list
            });
            let body = div()
                .id("diff-preview-horizontal")
                .flex()
                .flex_1()
                .min_w(px(0.0))
                .min_h(px(0.0))
                .overflow_x_scroll()
                .track_scroll(&inline_scroll)
                .map(|mut body| {
                    body.style().restrict_scroll_to_axis = Some(true);
                    body
                })
                .child(rows);
            bar = bar.child(body);
        }
        bar
    }

    /// Select an inspector tab, revealing the inspector. Shared by pointer
    /// and `Ctrl+Shift+E/G/I`.
    fn select_inspector_tab(&mut self, tab: InspectorTab, cx: &mut Context<Self>) {
        self.inspector_tab = tab;
        self.inspector_visible = true;
        self.git_panel.set_commit_focused(false);
        self.files_search_focused = false;
        self.files_vdrag = None;
        cx.notify();
    }

    /// Whether a project's dot reads live: green when at least one of its
    /// tab panes still owns a registry session, muted otherwise.
    fn project_is_live(&self, panes: &[Pane]) -> bool {
        panes.iter().any(|pane| match pane.content {
            PaneContent::Terminal(session) => self.coordinator.registry().contains(session),
            PaneContent::Empty => false,
        })
    }

    /// Branch name plus dirty flag for a project, from the last-good Git
    /// status. `None` outside a repo so callers omit git rather than
    /// inventing metadata.
    fn branch_for(&self, project: ProjectId) -> Option<(String, bool)> {
        let status = self.git_panel.status_for(project)?;
        let dirty = !(status.staged.is_empty()
            && status.unstaged.is_empty()
            && status.untracked.is_empty());
        status.branch.clone().map(|branch| (branch, dirty))
    }

    /// Pane count plus attention flag for a tab: attention when any pane
    /// holds a restore failure or the global spawn failure is set.
    fn tab_health(&self, tab: &omaterm_core::Tab) -> (usize, bool) {
        let panes = tab.tree.panes();
        let attention = self.spawn_failure.is_some()
            || panes
                .iter()
                .any(|pane| self.restored_failures.contains_key(&pane.id));
        (panes.len(), attention)
    }

    /// Display path for a project card: pinned directory with `$HOME`
    /// shortened to `~`, else an explicit empty state.
    fn project_path_label(&self, project: &omaterm_core::Project) -> String {
        if let Some(dir) = project.pinned_directory.as_deref() {
            let text = dir.to_string_lossy().into_owned();
            if let Ok(home) = std::env::var("HOME")
                && let Ok(rest) = Path::new(&text).strip_prefix(Path::new(&home))
            {
                return if rest.as_os_str().is_empty() {
                    "~".to_string()
                } else {
                    format!("~/{}", rest.display())
                };
            }
            text
        } else {
            "no root".to_string()
        }
    }

    /// UI v5 Projects sidebar: 42px header (hide control, label, open
    /// action), project cards, bottom Open Project button. All actions go
    /// through the dispatcher; collapse only hides the panel.
    fn render_projects_sidebar(&mut self, cx: &mut Context<Self>) -> Div {
        let mut cards: Vec<Div> = Vec::new();
        let selected = self.coordinator.selected_project_id();
        let mut label_counts = HashMap::<String, usize>::new();
        for (index, project) in self.coordinator.projects().iter().enumerate() {
            let id = project.id;
            let base_name = project.display_name(index + 1);
            let occurrence = label_counts.entry(base_name.clone()).or_default();
            *occurrence += 1;
            let name = if *occurrence == 1 {
                base_name
            } else {
                format!("{base_name} {}", *occurrence)
            };
            let live = self.project_is_live(
                &project
                    .tabs
                    .iter()
                    .flat_map(|tab| tab.tree.panes().into_iter().cloned())
                    .collect::<Vec<_>>(),
            );
            let branch = self.branch_for(id).map(
                |(branch, dirty)| {
                    if dirty { format!("{branch}*") } else { branch }
                },
            );
            let path = self.project_path_label(project);
            let active = selected == Some(id);
            let close_id = id;
            let pick_id = id;
            let context_menu_open = self.project_context_menu == Some(id);
            let label = if self.show_project_hints && index < 9 {
                format!("{} · {name}", index + 1)
            } else {
                name
            };
            let dot_color = if live {
                crate::ui::theme::GREEN
            } else {
                crate::ui::theme::MUTED2
            };
            // Measured card: 8px radius, 1px border (transparent when
            // inactive), 10px padding; name 12px (500 active / 400 idle),
            // branch 10px, path 9px; inactive hover #171d25.
            let mut card = div()
                .w_full()
                .relative()
                .rounded(px(8.0))
                .border_1()
                .border_color(if active {
                    rgb(crate::ui::theme::BORDER2)
                } else {
                    gpui::rgba(0x00000000)
                })
                .bg(rgb(if active {
                    crate::ui::theme::SELECTED_PROJECT_BG
                } else {
                    crate::ui::theme::PANEL
                }))
                .px(px(10.0))
                .py(px(10.0))
                .hover(|s| {
                    if active {
                        s
                    } else {
                        s.bg(gpui::rgb(crate::ui::theme::ROW_HOVER_BG))
                    }
                })
                .on_mouse_down(
                    MouseButton::Left,
                    cx.listener(move |view, _, window, cx| {
                        if view.shutting_down {
                            return;
                        }
                        window.focus(&view.focus_handle);
                        view.project_context_menu = None;
                        let _ = view.dispatch_command(
                            OmaCommand::Project(ProjectCommand::Select { project: id }),
                            cx,
                        );
                    }),
                )
                .on_mouse_down(
                    MouseButton::Right,
                    cx.listener(move |view, _, window, cx| {
                        if view.shutting_down {
                            return;
                        }
                        cx.stop_propagation();
                        window.focus(&view.focus_handle);
                        view.project_context_menu = Some(id);
                        cx.notify();
                    }),
                )
                .child(
                    div()
                        .flex()
                        .flex_row()
                        .items_center()
                        .gap_2()
                        .child(
                            div()
                                .w(px(8.0))
                                .h(px(8.0))
                                .rounded_full()
                                .bg(rgb(dot_color)),
                        )
                        .child(
                            crate::ui::metrics::text_role(
                                div().flex_1().truncate(),
                                if active {
                                    crate::ui::metrics::NAME_12_MEDIUM
                                } else {
                                    crate::ui::metrics::NAME_12
                                },
                            )
                            .text_color(rgb(crate::ui::theme::TEXT))
                            .child(label),
                        )
                        .child(
                            crate::ui::metrics::text_role(div(), crate::ui::metrics::META_10)
                                .text_color(rgb(crate::ui::theme::MUTED))
                                .child(branch.unwrap_or_default()),
                        ),
                )
                .child(
                    crate::ui::metrics::text_role(
                        div().pl(px(16.0)).pt(px(4.0)),
                        crate::ui::metrics::META_9,
                    )
                    .text_color(rgb(crate::ui::theme::MUTED))
                    .truncate()
                    .child(path),
                );
            // Actions live in a transient context menu so selection never
            // changes the measured 57.5px card geometry.
            if context_menu_open {
                card = card.child(
                    div()
                        .absolute()
                        .top(px(60.0))
                        .right(px(0.0))
                        .w(px(132.0))
                        .p(px(4.0))
                        .rounded(px(6.0))
                        .border_1()
                        .border_color(rgb(crate::ui::theme::BORDER2))
                        .bg(rgb(crate::ui::theme::PANEL2))
                        .text_color(rgb(crate::ui::theme::TEXT2))
                        .child(
                            div()
                                .h(px(28.0))
                                .flex()
                                .items_center()
                                .px(px(8.0))
                                .rounded(px(4.0))
                                .hover(|s| s.bg(rgb(crate::ui::theme::ROW_HOVER_BG)))
                                .on_mouse_down(
                                    MouseButton::Left,
                                    cx.listener(move |view, _, window, cx| {
                                        if view.shutting_down {
                                            return;
                                        }
                                        cx.stop_propagation();
                                        window.focus(&view.focus_handle);
                                        view.project_context_menu = None;
                                        view.pick_project_directory(pick_id, cx);
                                    }),
                                )
                                .child("Change directory"),
                        )
                        .child(
                            div()
                                .h(px(28.0))
                                .flex()
                                .items_center()
                                .px(px(8.0))
                                .rounded(px(4.0))
                                .hover(|s| s.bg(rgb(crate::ui::theme::ROW_HOVER_BG)))
                                .on_mouse_down(
                                    MouseButton::Left,
                                    cx.listener(move |view, _, window, cx| {
                                        if view.shutting_down {
                                            return;
                                        }
                                        cx.stop_propagation();
                                        window.focus(&view.focus_handle);
                                        view.project_context_menu = None;
                                        view.close_project(close_id, cx);
                                    }),
                                )
                                .child("Close project"),
                        ),
                );
            }
            cards.push(card);
        }
        div()
            .w(px(crate::ui::geometry::clamp_projects_width(
                self.projects_width,
            )))
            .h_full()
            .flex()
            .flex_col()
            .flex_shrink_0()
            .bg(rgb(crate::ui::theme::PANEL))
            .border_r_1()
            .border_color(rgb(crate::ui::theme::BORDER))
            .child(
                div()
                    .h(px(crate::ui::geometry::HEADER_H))
                    .flex()
                    .flex_row()
                    .items_center()
                    .px_2()
                    .gap_2()
                    .border_b_1()
                    .border_color(rgb(crate::ui::theme::BORDER))
                    .child(
                        div()
                            .p(px(6.0))
                            .rounded(px(7.0))
                            .border_1()
                            .border_color(rgba(0x00000000))
                            .text_color(rgb(crate::ui::theme::MUTED))
                            .hover(|s| {
                                s.bg(rgb(crate::ui::theme::CMD_HOVER_BG))
                                    .border_color(rgb(crate::ui::theme::CMD_HOVER_BORDER))
                                    .text_color(rgb(0xFFFFFF))
                            })
                            .on_mouse_down(
                                MouseButton::Left,
                                cx.listener(|view, _, window, cx| {
                                    if view.shutting_down {
                                        return;
                                    }
                                    cx.stop_propagation();
                                    window.focus(&view.focus_handle);
                                    view.projects_visible = false;
                                    view.projects_resize = None;
                                    cx.notify();
                                }),
                            )
                            .child(crate::ui::primitives::cmd_icon(
                                crate::ui::assets::PANEL_LEFT_CLOSE,
                                16.0,
                                crate::ui::theme::MUTED,
                            )),
                    )
                    .child(
                        div()
                            .text_color(rgb(crate::ui::theme::MUTED))
                            .child("PROJECTS"),
                    )
                    .child(
                        div().flex_1().flex().flex_row().justify_end().child(
                            div()
                                .p(px(6.0))
                                .rounded_sm()
                                .text_color(rgb(crate::ui::theme::MUTED))
                                .on_mouse_down(
                                    MouseButton::Left,
                                    cx.listener(|view, _, window, cx| {
                                        if view.shutting_down {
                                            return;
                                        }
                                        cx.stop_propagation();
                                        window.focus(&view.focus_handle);
                                        view.open_project_directory(cx);
                                    }),
                                )
                                .child(crate::ui::primitives::cmd_icon(
                                    crate::ui::assets::FOLDER_PLUS,
                                    14.0,
                                    crate::ui::theme::MUTED,
                                )),
                        ),
                    ),
            )
            .child(
                div()
                    .id("projects-list")
                    .flex()
                    .flex_1()
                    .flex_col()
                    .min_h(px(0.0))
                    .overflow_y_scroll()
                    .p_2()
                    .gap_1()
                    .children(cards),
            )
            .child(
                div()
                    .p_2()
                    .border_t_1()
                    .border_color(rgb(crate::ui::theme::BORDER))
                    .child(
                        div()
                            .h(px(32.0))
                            .flex()
                            .flex_row()
                            .items_center()
                            .gap_2()
                            .px(px(8.0))
                            .rounded(px(6.0))
                            .border_1()
                            .border_color(rgb(crate::ui::theme::BORDER2))
                            .text_color(rgb(crate::ui::theme::TEXT2))
                            .on_mouse_down(
                                MouseButton::Left,
                                cx.listener(|view, _, window, cx| {
                                    if view.shutting_down {
                                        return;
                                    }
                                    cx.stop_propagation();
                                    window.focus(&view.focus_handle);
                                    view.open_project_directory(cx);
                                }),
                            )
                            .child(crate::ui::primitives::cmd_icon(
                                crate::ui::assets::FOLDER_PLUS,
                                14.0,
                                crate::ui::theme::TEXT2,
                            ))
                            .child(
                                crate::ui::metrics::text_role(
                                    div().flex_1().min_w(px(0.0)).truncate(),
                                    crate::ui::metrics::BODY_11,
                                )
                                .child("Open Project"),
                            )
                            .child(
                                crate::ui::metrics::text_role(
                                    crate::ui::primitives::kbd().flex_shrink_0(),
                                    crate::ui::metrics::META_9,
                                )
                                .text_color(rgb(crate::ui::theme::MUTED))
                                .child("Ctrl+O"),
                            ),
                    ),
            )
    }

    /// 4px Projects resizer: transparent at rest, highlighted while the
    /// pointer drags. Pointer capture ends on release anywhere.
    fn render_projects_resizer(&mut self, cx: &mut Context<Self>) -> Div {
        let dragging = self.projects_resize.is_some();
        div()
            .w(px(crate::ui::geometry::RESIZER))
            .h_full()
            .flex_shrink_0()
            .bg(rgb(if dragging {
                crate::ui::theme::BLUE2
            } else {
                crate::ui::theme::BG
            }))
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(|view, event: &MouseDownEvent, _, cx| {
                    if view.shutting_down {
                        return;
                    }
                    cx.stop_propagation();
                    view.projects_resize = Some(f32::from(event.position.x));
                    cx.notify();
                }),
            )
            .on_mouse_move(cx.listener(|view, event: &MouseMoveEvent, _, cx| {
                let Some(last_x) = view.projects_resize else {
                    return;
                };
                if view.shutting_down {
                    view.projects_resize = None;
                    return;
                }
                let x = f32::from(event.position.x);
                view.projects_width =
                    crate::ui::geometry::clamp_projects_width(view.projects_width + (x - last_x));
                view.projects_resize = Some(x);
                cx.notify();
            }))
            .on_mouse_up(
                MouseButton::Left,
                cx.listener(|view, _, _, cx| {
                    view.projects_resize = None;
                    cx.notify();
                }),
            )
    }

    /// 4px Inspector resizer: mirrored drag math (the panel grows leftward).
    fn render_inspector_resizer(&mut self, cx: &mut Context<Self>) -> Div {
        let dragging = self.inspector_resize.is_some();
        div()
            .w(px(crate::ui::geometry::RESIZER))
            .h_full()
            .flex_shrink_0()
            .bg(rgb(if dragging {
                crate::ui::theme::BLUE2
            } else {
                crate::ui::theme::BG
            }))
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(|view, event: &MouseDownEvent, _, cx| {
                    if view.shutting_down {
                        return;
                    }
                    cx.stop_propagation();
                    view.inspector_resize = Some(f32::from(event.position.x));
                    cx.notify();
                }),
            )
            .on_mouse_move(cx.listener(|view, event: &MouseMoveEvent, _, cx| {
                let Some(last_x) = view.inspector_resize else {
                    return;
                };
                if view.shutting_down {
                    view.inspector_resize = None;
                    return;
                }
                let x = f32::from(event.position.x);
                view.inspector_width =
                    crate::ui::geometry::clamp_inspector_width(view.inspector_width + (last_x - x));
                view.inspector_resize = Some(x);
                cx.notify();
            }))
            .on_mouse_up(
                MouseButton::Left,
                cx.listener(|view, _, _, cx| {
                    view.inspector_resize = None;
                    cx.notify();
                }),
            )
    }

    /// UI v5 header row (42px): reveal-projects control when hidden, the
    /// terminal tab strip, new-tab control, then search and inspector
    /// toggle pinned right. Exactly one visible surface is active.
    fn render_header(&mut self, cx: &mut Context<Self>) -> Div {
        let mut row = div()
            .h(px(crate::ui::geometry::HEADER_H))
            .flex()
            .flex_row()
            .items_center()
            .bg(rgb(crate::ui::theme::HEADER_BG))
            .border_b_1()
            .border_color(rgb(crate::ui::theme::BORDER));
        if !self.projects_visible {
            row = row.child(
                div()
                    .ml(px(8.0))
                    .mr(px(4.0))
                    .p(px(6.0))
                    .rounded_sm()
                    .text_color(rgb(crate::ui::theme::MUTED))
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(|view, _, window, cx| {
                            if view.shutting_down {
                                return;
                            }
                            cx.stop_propagation();
                            window.focus(&view.focus_handle);
                            view.projects_visible = true;
                            cx.notify();
                        }),
                    )
                    .child(crate::ui::primitives::cmd_icon(
                        crate::ui::assets::PANEL_LEFT,
                        16.0,
                        crate::ui::theme::MUTED,
                    )),
            );
        }
        let mut tabs = div()
            .flex()
            .flex_1()
            .flex_row()
            .items_end()
            .min_w(px(0.0))
            .h_full()
            .px_1()
            .gap_1()
            .overflow_hidden();
        if let Some(project) = self.coordinator.active_project() {
            for (index, tab) in project.tabs.iter().enumerate() {
                let project_id = project.id;
                let tab_id = tab.id;
                let active = project.selected_tab == Some(tab_id);
                let (pane_count, attention) = self.tab_health(tab);
                let dot = if attention {
                    crate::ui::theme::YELLOW
                } else {
                    crate::ui::theme::GREEN
                };
                let mut chip = crate::ui::metrics::text_role(
                    div()
                        .flex()
                        .flex_row()
                        .flex_shrink_0()
                        .items_center()
                        .gap_2()
                        .px_3()
                        .h(px(36.0))
                        .rounded_t_md(),
                    crate::ui::metrics::TAB_12,
                )
                .bg(rgb(if active {
                    crate::ui::theme::ACTIVE_TAB_BG
                } else {
                    crate::ui::theme::PANEL
                }))
                .text_color(rgb(if active {
                    0xFFFFFF
                } else {
                    crate::ui::theme::TEXT2
                }))
                .on_mouse_down(
                    MouseButton::Left,
                    cx.listener(move |view, _, window, cx| {
                        if view.shutting_down {
                            return;
                        }
                        window.focus(&view.focus_handle);
                        view.user_focus_action();
                        view.diff_panel.close_preview(project_id);
                        view.editor_active.remove(&project_id);
                        let _ = view.dispatch_command(
                            OmaCommand::Tab(TabCommand::Select { tab: tab_id }),
                            cx,
                        );
                    }),
                )
                .child(div().w(px(8.0)).h(px(8.0)).rounded_full().bg(rgb(dot)))
                .child(tab.display_name(index + 1));
                if active {
                    chip = chip
                        .border_t_2()
                        .border_color(rgb(crate::ui::theme::BLUE))
                        .child(
                            div()
                                .text_color(rgb(crate::ui::theme::MUTED))
                                .child(format!("{pane_count} panes")),
                        );
                }
                let close_project = project_id;
                let close_tab = tab_id;
                chip = chip.child(
                    div()
                        .px_1()
                        .text_color(rgb(crate::ui::theme::MUTED))
                        .on_mouse_down(
                            MouseButton::Left,
                            cx.listener(move |view, _, window, cx| {
                                if view.shutting_down {
                                    return;
                                }
                                cx.stop_propagation();
                                window.focus(&view.focus_handle);
                                view.close_tab(close_project, close_tab, cx);
                            }),
                        )
                        .child(crate::ui::assets::icon(
                            crate::ui::assets::CLOSE,
                            14.0,
                            crate::ui::theme::MUTED,
                        )),
                );
                tabs = tabs.child(chip);
            }
            tabs = tabs.child(
                div()
                    .w(px(32.0))
                    .h(px(32.0))
                    .mb(px(2.0))
                    .flex()
                    .items_center()
                    .justify_center()
                    .rounded_md()
                    .text_color(rgb(crate::ui::theme::MUTED))
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(|view, _, window, cx| {
                            if view.shutting_down {
                                return;
                            }
                            cx.stop_propagation();
                            window.focus(&view.focus_handle);
                            view.user_focus_action();
                            if let Some(project) = view.coordinator.selected_project_id() {
                                view.diff_panel.close_preview(project);
                                view.editor_active.remove(&project);
                            }
                            view.create_tab(cx);
                        }),
                    )
                    .child(crate::ui::assets::icon(
                        crate::ui::assets::PLUS,
                        16.0,
                        crate::ui::theme::MUTED,
                    )),
            );
            // M15 diff preview chip: view-local, visually distinct from
            // terminal tabs. Selecting a terminal tab or closing the chip
            // returns to the terminal surface.
            if self.diff_panel.preview_open(project.id)
                && let Some(path) = self.diff_panel.selected_file(project.id).cloned()
            {
                let name = path
                    .file_name()
                    .map(|name| name.to_string_lossy().into_owned())
                    .unwrap_or_else(|| path.to_string_lossy().into_owned());
                let preview_id = project.id;
                tabs = tabs.child(
                    div()
                        .flex()
                        .flex_row()
                        .items_center()
                        .gap_2()
                        .px_3()
                        .h(px(36.0))
                        .rounded_t_md()
                        .border_t_2()
                        .border_color(rgb(crate::ui::theme::BLUE))
                        .bg(rgb(crate::ui::theme::ACTIVE_TAB_BG))
                        .text_color(rgb(0xFFFFFF))
                        .on_mouse_down(
                            MouseButton::Left,
                            cx.listener(move |view, _, window, cx| {
                                if view.shutting_down {
                                    return;
                                }
                                window.focus(&view.focus_handle);
                                cx.notify();
                            }),
                        )
                        .child(format!("Diff: {name}"))
                        .child(
                            div()
                                .px_1()
                                .text_color(rgb(crate::ui::theme::MUTED))
                                .on_mouse_down(
                                    MouseButton::Left,
                                    cx.listener(move |view, _, window, cx| {
                                        if view.shutting_down {
                                            return;
                                        }
                                        cx.stop_propagation();
                                        window.focus(&view.focus_handle);
                                        view.diff_panel.close_preview(preview_id);
                                        cx.notify();
                                    }),
                                )
                                .child(crate::ui::assets::icon(
                                    crate::ui::assets::CLOSE,
                                    14.0,
                                    crate::ui::theme::MUTED,
                                )),
                        ),
                );
            }
            // M19 editor document chips: view-local activation over
            // router-owned buffers. Click activates; the terminal-tab and
            // new-tab controls return to the terminal surface without
            // closing documents.
            for document in self.coordinator.documents().project_documents(project.id) {
                let path = self.coordinator.documents().relative_path(document);
                let name = path
                    .as_ref()
                    .and_then(|path| {
                        path.file_name()
                            .map(|name| name.to_string_lossy().into_owned())
                    })
                    .unwrap_or_else(|| "untitled".into());
                let dirty = self
                    .coordinator
                    .documents()
                    .is_dirty(document)
                    .unwrap_or(false);
                let active = self.editor_selected.get(&project.id) == Some(&document);
                let open_project = project.id;
                tabs = tabs.child(
                    div()
                        .flex()
                        .flex_row()
                        .items_center()
                        .gap_2()
                        .px_3()
                        .h(px(36.0))
                        .rounded_t_md()
                        .border_t_2()
                        .border_color(rgb(if active {
                            crate::ui::theme::BLUE
                        } else {
                            crate::ui::theme::BORDER
                        }))
                        .bg(rgb(if active {
                            crate::ui::theme::ACTIVE_TAB_BG
                        } else {
                            crate::ui::theme::PANEL
                        }))
                        .text_color(rgb(if active {
                            0xFFFFFF
                        } else {
                            crate::ui::theme::TEXT2
                        }))
                        .on_mouse_down(
                            MouseButton::Left,
                            cx.listener(move |view, _, window, cx| {
                                if view.shutting_down {
                                    return;
                                }
                                window.focus(&view.focus_handle);
                                view.user_focus_action();
                                view.editor_activate(open_project, document, cx);
                            }),
                        )
                        .child(crate::ui::assets::icon(
                            crate::ui::assets::FILE_TEXT,
                            14.0,
                            crate::ui::theme::BLUE,
                        ))
                        .child(name)
                        .child(
                            div()
                                .text_color(rgb(if dirty {
                                    crate::ui::theme::YELLOW
                                } else {
                                    crate::ui::theme::MUTED
                                }))
                                .child(if dirty { "M" } else { "" }),
                        )
                        .child(
                            div()
                                .px_1()
                                .text_color(rgb(crate::ui::theme::MUTED))
                                .on_mouse_down(
                                    MouseButton::Left,
                                    cx.listener(move |view, _, window, cx| {
                                        if view.shutting_down {
                                            return;
                                        }
                                        cx.stop_propagation();
                                        window.focus(&view.focus_handle);
                                        view.editor_close_document(open_project, document, cx);
                                    }),
                                )
                                .child(crate::ui::assets::icon(
                                    crate::ui::assets::CLOSE,
                                    14.0,
                                    crate::ui::theme::MUTED,
                                )),
                        ),
                );
            }
            // Metadata-only restore chips: a bounded read is loading, or it
            // failed and offers an explicit Retry/Close path. No text is ever
            // shown for these entries.
            for placeholder in self
                .coordinator
                .documents()
                .project_placeholders(project.id)
            {
                let entry = self.coordinator.documents().placeholder(placeholder);
                let name = entry
                    .as_ref()
                    .and_then(|entry| {
                        entry
                            .path()
                            .file_name()
                            .map(|name| name.to_string_lossy().into_owned())
                    })
                    .unwrap_or_else(|| "untitled".into());
                let unavailable = entry.as_ref().is_some_and(|entry| entry.is_unavailable());
                let reason = entry.as_ref().and_then(|entry| match entry.state() {
                    editor::PlaceholderState::Unavailable(reason) => Some(reason.clone()),
                    editor::PlaceholderState::Loading => None,
                });
                let open_project = project.id;
                let mut chip = div()
                    .flex()
                    .flex_row()
                    .items_center()
                    .gap_2()
                    .px_3()
                    .h(px(36.0))
                    .rounded_t_md()
                    .border_t_2()
                    .border_color(rgb(if unavailable {
                        crate::workbench::ERROR_TEXT
                    } else {
                        crate::ui::theme::BORDER
                    }))
                    .bg(rgb(crate::ui::theme::PANEL))
                    .text_color(rgb(crate::ui::theme::TEXT2))
                    .child(crate::ui::assets::icon(
                        crate::ui::assets::FILE_TEXT,
                        14.0,
                        crate::ui::theme::MUTED,
                    ))
                    .child(name)
                    .child(
                        div()
                            .text_color(rgb(if unavailable {
                                crate::workbench::ERROR_TEXT
                            } else {
                                crate::ui::theme::MUTED
                            }))
                            .child(if unavailable {
                                "unavailable"
                            } else {
                                "loading"
                            }),
                    );
                if let Some(reason) = reason {
                    chip = chip.child(
                        div()
                            .px_1()
                            .text_color(rgb(crate::ui::theme::MUTED))
                            .on_mouse_down(
                                MouseButton::Left,
                                cx.listener(move |view, _, window, cx| {
                                    if view.shutting_down {
                                        return;
                                    }
                                    cx.stop_propagation();
                                    window.focus(&view.focus_handle);
                                    view.editor_retry_placeholder(open_project, placeholder, cx);
                                }),
                            )
                            .child("Retry"),
                    );
                    let _ = reason;
                }
                chip = chip.child(
                    div()
                        .px_1()
                        .text_color(rgb(crate::ui::theme::MUTED))
                        .on_mouse_down(
                            MouseButton::Left,
                            cx.listener(move |view, _, window, cx| {
                                if view.shutting_down {
                                    return;
                                }
                                cx.stop_propagation();
                                window.focus(&view.focus_handle);
                                view.editor_close_placeholder(open_project, placeholder, cx);
                            }),
                        )
                        .child(crate::ui::assets::icon(
                            crate::ui::assets::CLOSE,
                            14.0,
                            crate::ui::theme::MUTED,
                        )),
                );
                tabs = tabs.child(chip);
            }
        } else {
            tabs = tabs.child(
                div()
                    .px_3()
                    .text_color(rgb(crate::ui::theme::MUTED))
                    .child("No project selected"),
            );
        }
        row = row.child(tabs);
        row.child(
            div()
                .flex()
                .flex_row()
                .items_center()
                .gap_1()
                .px_2()
                .flex_shrink_0()
                .child(
                    div()
                        .p(px(6.0))
                        .rounded_sm()
                        .text_color(rgb(crate::ui::theme::MUTED))
                        .on_mouse_down(
                            MouseButton::Left,
                            cx.listener(|view, _, window, cx| {
                                if view.shutting_down {
                                    return;
                                }
                                cx.stop_propagation();
                                window.focus(&view.focus_handle);
                                view.open_palette(false, cx);
                            }),
                        )
                        .child(crate::ui::primitives::cmd_icon(
                            crate::ui::assets::SEARCH,
                            16.0,
                            crate::ui::theme::MUTED,
                        )),
                )
                .child({
                    let visible = self.inspector_visible;
                    div()
                        .p(px(6.0))
                        .rounded_sm()
                        .border_1()
                        .border_color(rgb(if visible {
                            crate::ui::theme::BORDER2
                        } else {
                            crate::ui::theme::HEADER_BG
                        }))
                        .bg(rgb(if visible {
                            crate::ui::theme::SELECTED_PROJECT_BG
                        } else {
                            crate::ui::theme::HEADER_BG
                        }))
                        .text_color(rgb(if visible {
                            0xFFFFFF
                        } else {
                            crate::ui::theme::MUTED
                        }))
                        .on_mouse_down(
                            MouseButton::Left,
                            cx.listener(|view, _, window, cx| {
                                if view.shutting_down {
                                    return;
                                }
                                cx.stop_propagation();
                                window.focus(&view.focus_handle);
                                view.inspector_visible = !view.inspector_visible;
                                view.inspector_resize = None;
                                cx.notify();
                            }),
                        )
                        .child(crate::ui::primitives::cmd_icon(
                            crate::ui::assets::PANEL_RIGHT,
                            16.0,
                            if visible {
                                0xFFFFFF
                            } else {
                                crate::ui::theme::MUTED
                            },
                        ))
                }),
        )
    }

    /// Right inspector: 40px Info/Files/Git tab row plus the selected body.
    /// Switching tabs never disturbs the center surface or panel geometry.
    fn render_inspector(&mut self, viewport_h: f32, cx: &mut Context<Self>) -> Div {
        let git_count = self
            .coordinator
            .selected_project_id()
            .and_then(|project| self.git_panel.status_for(project))
            .map(|status| status.staged.len() + status.unstaged.len() + status.untracked.len())
            .unwrap_or(0);
        let mut tabs = div()
            .h(px(40.0))
            .flex()
            .flex_row()
            .items_end()
            .px_1()
            .border_b_1()
            .border_color(rgb(crate::ui::theme::BORDER))
            .flex_shrink_0();
        for (tab, label, glyph) in [
            (InspectorTab::Info, "Info", crate::ui::assets::INFO),
            (InspectorTab::Files, "Files", crate::ui::assets::FOLDER),
            (InspectorTab::Git, "Git", crate::ui::assets::GIT_BRANCH),
        ] {
            let active = self.inspector_tab == tab;
            let glyph_color = if active {
                0xFFFFFF
            } else {
                crate::ui::theme::MUTED
            };
            let mut button = crate::ui::metrics::text_role(
                div()
                    .h_full()
                    .flex()
                    .flex_row()
                    .items_center()
                    .gap(px(6.0))
                    .px_3()
                    .relative(),
                crate::ui::metrics::BODY_11,
            )
            .text_color(rgb(if active {
                0xFFFFFF
            } else {
                crate::ui::theme::MUTED
            }))
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(move |view, _, window, cx| {
                    if view.shutting_down {
                        return;
                    }
                    cx.stop_propagation();
                    window.focus(&view.focus_handle);
                    view.select_inspector_tab(tab, cx);
                }),
            )
            .child(crate::ui::assets::icon(glyph, 14.0, glyph_color))
            .child(label);
            if active {
                button = button.child(
                    div()
                        .absolute()
                        .left(px(9.0))
                        .right(px(9.0))
                        .bottom(px(0.0))
                        .h(px(2.0))
                        .rounded_full()
                        .bg(rgb(crate::ui::theme::BLUE)),
                );
            }
            if tab == InspectorTab::Git && git_count > 0 {
                button = button.child(
                    crate::ui::metrics::text_role(
                        div()
                            .ml(px(4.0))
                            .px(px(6.0))
                            .rounded_full()
                            .bg(rgb(crate::ui::theme::GIT_BADGE_BG)),
                        crate::ui::metrics::META_9,
                    )
                    .child(format!("{git_count}")),
                );
            }
            tabs = tabs.child(button);
        }
        let mut panel = div()
            .w(px(crate::ui::geometry::clamp_inspector_width(
                self.inspector_width,
            )))
            .h_full()
            .flex()
            .flex_col()
            .flex_shrink_0()
            .bg(rgb(crate::ui::theme::PANEL))
            .border_l_1()
            .border_color(rgb(crate::ui::theme::BORDER))
            .child(tabs);
        panel = match self.inspector_tab {
            InspectorTab::Info => panel.child(self.render_inspector_info(cx)),
            InspectorTab::Files => panel.child(self.render_inspector_files(viewport_h, cx)),
            InspectorTab::Git => panel
                .child(self.render_git_panel(div().flex().flex_col().flex_1().min_h(px(0.0)), cx)),
        };
        panel
    }

    /// Inspector Files body: search box plus the existing tree, with the
    /// row-step wheel handler attached to the scroll container.
    fn render_inspector_files(&mut self, viewport_h: f32, cx: &mut Context<Self>) -> Div {
        // Real filter input over the cached rows (file-name substring).
        // Clicking focuses the box; typing filters, Enter opens the first
        // match, Esc clears and releases. Ctrl+P stays the fuzzy path.
        let query = self.files_search.clone();
        let search_focused = self.files_search_focused;
        let mut body = div().flex().flex_col().flex_1().min_h(px(0.0)).child(
            div()
                .p_2()
                .border_b_1()
                .border_color(rgb(crate::ui::theme::BORDER))
                .child(
                    div()
                        .h(px(32.0))
                        .flex()
                        .flex_row()
                        .items_center()
                        .px_2()
                        .gap_2()
                        .rounded_md()
                        .border_1()
                        .border_color(rgb(if search_focused {
                            crate::ui::theme::BLUE2
                        } else {
                            crate::ui::theme::BORDER
                        }))
                        .bg(rgb(crate::ui::theme::PILL_BG))
                        .text_size(px(11.0))
                        .on_mouse_down(
                            MouseButton::Left,
                            cx.listener(|view, _, window, cx| {
                                if view.shutting_down {
                                    return;
                                }
                                cx.stop_propagation();
                                window.focus(&view.focus_handle);
                                view.files_search_focused = true;
                                cx.notify();
                            }),
                        )
                        .child(crate::ui::assets::icon(
                            crate::ui::assets::SEARCH,
                            14.0,
                            crate::ui::theme::MUTED,
                        ))
                        .child(
                            div()
                                .flex_1()
                                .min_w(px(0.0))
                                .truncate()
                                .text_color(rgb(if query.is_empty() {
                                    crate::ui::theme::MUTED
                                } else {
                                    crate::ui::theme::TEXT
                                }))
                                .child(if query.is_empty() {
                                    "Search files".to_string()
                                } else {
                                    query
                                }),
                        ),
                ),
        );
        body = self.render_files_tree(body, viewport_h, cx);
        if let Some(message) = self.files_warning.clone() {
            body = body.child(
                div()
                    .px_2()
                    .py_1()
                    .text_color(rgb(workbench::WARN_TEXT))
                    .child(message),
            );
        }
        body.on_scroll_wheel(cx.listener(|view, event: &ScrollWheelEvent, _, cx| {
            let row_height = files::TREE_ROW_H;
            let dy_lines: f32 = match event.delta {
                ScrollDelta::Pixels(point) => f32::from(point.y) / row_height,
                // One wheel detent should cover a useful number of compact
                // 28px tree rows; touchpads retain their pixel precision.
                ScrollDelta::Lines(point) => point.y * 3.0,
            };
            view.files_scroll_remainder -= dy_lines;
            let steps = view.files_scroll_remainder.trunc() as i32;
            view.files_scroll_remainder -= steps as f32;
            if steps == 0 {
                return;
            }
            view.files_scroll_rows = (view.files_scroll_rows as i32 + steps).max(0) as usize;
            cx.notify();
        }))
    }

    /// Inspector Info body: real project card plus explicit M18-pending
    /// states for process/port inspection. No fabricated telemetry.
    fn render_inspector_info(&mut self, cx: &mut Context<Self>) -> Div {
        let mut body = div()
            .flex()
            .flex_col()
            .flex_1()
            .min_h(px(0.0))
            .overflow_hidden()
            .p_3()
            .gap_4();
        let (name, path, branch) =
            if let Some(project) = self.coordinator.active_project() {
                let index = self
                    .coordinator
                    .projects()
                    .iter()
                    .position(|p| p.id == project.id)
                    .unwrap_or(0);
                let branch = self.branch_for(project.id).map(|(branch, dirty)| {
                    if dirty { format!("{branch}*") } else { branch }
                });
                (
                    project.display_name(index + 1),
                    self.project_path_label(project),
                    branch,
                )
            } else {
                ("No project".to_string(), String::new(), None)
            };
        body = body.child(
            div()
                .flex()
                .flex_col()
                .gap_2()
                .child(
                    crate::ui::metrics::text_role(div(), crate::ui::metrics::HEADING_10)
                        .text_color(rgb(crate::ui::theme::MUTED))
                        .child("PROJECT"),
                )
                .child(
                    div()
                        .rounded(px(8.0))
                        .border_1()
                        .border_color(rgb(crate::ui::theme::BORDER))
                        .bg(rgb(crate::ui::theme::INFO_CARD_BG))
                        .p_3()
                        .child(
                            div()
                                .flex()
                                .flex_row()
                                .items_center()
                                .gap_2()
                                .child(
                                    div()
                                        .w(px(28.0))
                                        .h(px(28.0))
                                        .rounded_md()
                                        .border_1()
                                        .border_color(rgb(crate::ui::theme::BORDER2))
                                        .bg(rgb(crate::ui::theme::INFO_ICON_BOX_BG))
                                        .flex()
                                        .items_center()
                                        .justify_center()
                                        .text_color(rgb(crate::ui::theme::BLUE))
                                        .child(crate::ui::assets::icon(
                                            crate::ui::assets::FOLDER_GIT,
                                            16.0,
                                            crate::ui::theme::BLUE,
                                        )),
                                )
                                .child(
                                    div()
                                        .flex()
                                        .flex_1()
                                        .flex_col()
                                        .min_w(px(0.0))
                                        .child(
                                            crate::ui::metrics::text_role(
                                                div().truncate(),
                                                crate::ui::metrics::NAME_12,
                                            )
                                            .text_color(rgb(crate::ui::theme::TEXT))
                                            .child(name),
                                        )
                                        .child(
                                            crate::ui::metrics::text_role(
                                                div().truncate(),
                                                crate::ui::metrics::META_10,
                                            )
                                            .text_color(rgb(crate::ui::theme::MUTED))
                                            .child(path),
                                        ),
                                )
                                .child(if let Some(branch) = branch {
                                    crate::ui::metrics::text_role(
                                        crate::ui::primitives::pill()
                                            .px(px(6.0))
                                            .py(px(2.0))
                                            .rounded_sm(),
                                        crate::ui::metrics::META_9,
                                    )
                                    .text_color(rgb(crate::ui::theme::MUTED))
                                    .child(branch)
                                    .into_any_element()
                                } else {
                                    div().into_any_element()
                                }),
                        ),
                ),
        );
        let focused_pane = self.coordinator.focused();
        let process_entries = self
            .coordinator
            .selected_project_id()
            .and_then(|project| {
                self.process_list
                    .as_ref()
                    .filter(|(owner, _)| *owner == project)
            })
            .map(|(_, info)| {
                info.entries
                    .iter()
                    .filter(|entry| Some(entry.pane) == focused_pane)
                    .cloned()
                    .collect::<Vec<_>>()
            })
            .unwrap_or_default();
        let mut process_rows = div().flex().flex_col().gap_1();
        let mut port_rows = div().flex().flex_col().gap_1();
        for entry in &process_entries {
            process_rows = process_rows.child(
                div()
                    .flex()
                    .flex_row()
                    .items_center()
                    .gap_2()
                    .text_size(px(11.0))
                    .child(
                        div()
                            .flex_1()
                            .min_w(px(0.0))
                            .truncate()
                            .text_color(rgb(crate::ui::theme::TEXT))
                            .child(entry.name.clone()),
                    )
                    .text_color(rgb(crate::ui::theme::MUTED))
                    .child(entry.pid.to_string()),
            );
            for port in &entry.ports {
                port_rows = port_rows.child(
                    div()
                        .flex()
                        .flex_row()
                        .items_center()
                        .gap_2()
                        .text_size(px(11.0))
                        .text_color(rgb(crate::ui::theme::TEXT2))
                        .child(format!("{port} · {}", entry.name)),
                );
            }
        }
        let mut process_section = div().flex().flex_col().gap_2().child(
            div()
                .flex()
                .flex_row()
                .items_center()
                .child(
                    crate::ui::metrics::text_role(div(), crate::ui::metrics::HEADING_10)
                        .text_color(rgb(crate::ui::theme::MUTED))
                        .child(format!("PROCESSES · {}", process_entries.len())),
                )
                .child(div().flex_1())
                .child(
                    div()
                        .cursor_pointer()
                        .text_size(px(10.0))
                        .text_color(rgb(crate::ui::theme::BLUE))
                        .child("Refresh")
                        .on_mouse_down(
                            MouseButton::Left,
                            cx.listener(|view, _, _, cx| {
                                if let Some(project) = view.coordinator.selected_project_id() {
                                    view.refresh_process_list(project, cx);
                                }
                            }),
                        ),
                ),
        );
        process_section = if process_entries.is_empty() {
            process_section.child(
                crate::ui::metrics::text_role(
                    div()
                        .rounded(px(8.0))
                        .border_1()
                        .border_color(rgb(crate::ui::theme::BORDER))
                        .px_3()
                        .py_2(),
                    crate::ui::metrics::BODY_11,
                )
                .text_color(rgb(crate::ui::theme::MUTED))
                .child("No child processes"),
            )
        } else {
            process_section.child(process_rows)
        };
        body = body.child(process_section);
        body = body.child(
            div()
                .flex()
                .flex_col()
                .gap_2()
                .child(
                    crate::ui::metrics::text_role(div(), crate::ui::metrics::HEADING_10)
                        .text_color(rgb(crate::ui::theme::MUTED))
                        .child(format!(
                            "PORTS · {}",
                            process_entries
                                .iter()
                                .map(|entry| entry.ports.len())
                                .sum::<usize>()
                        )),
                )
                .child(
                    if process_entries.iter().any(|entry| !entry.ports.is_empty()) {
                        port_rows.into_any_element()
                    } else {
                        crate::ui::metrics::text_role(
                            div()
                                .rounded(px(8.0))
                                .border_1()
                                .border_color(rgb(crate::ui::theme::BORDER))
                                .px_3()
                                .py_2(),
                            crate::ui::metrics::BODY_11,
                        )
                        .text_color(rgb(crate::ui::theme::MUTED))
                        .child("No listening ports")
                        .into_any_element()
                    },
                ),
        );
        body
    }

    /// UI v5 global status bar (24px): branch button, live process count,
    /// then history state, font size, and encoding at right. Port counts,
    /// CPU/MEM telemetry, and the notices bell arrive with M18/P6 — omitted
    /// until real, never fabricated.
    fn render_status_bar(&mut self, cx: &mut Context<Self>) -> Div {
        let mut left = div().flex().flex_row().items_center().h_full();
        if let Some(project) = self.coordinator.selected_project_id()
            && let Some(status) = self.git_panel.status_for(project)
        {
            let dirty = !(status.staged.is_empty()
                && status.unstaged.is_empty()
                && status.untracked.is_empty());
            if let Some(branch) = workbench::branch_label(status.branch.as_deref(), dirty) {
                left = left.child(
                    div()
                        .px(px(10.0))
                        .h_full()
                        .flex()
                        .flex_row()
                        .items_center()
                        .gap(px(6.0))
                        .child(crate::ui::assets::icon(
                            crate::ui::assets::GIT_BRANCH,
                            12.0,
                            crate::ui::theme::MUTED,
                        ))
                        .child(branch),
                );
            }
        }
        let live = self.coordinator.registry().len();
        left = left.child(
            div()
                .px(px(10.0))
                .h_full()
                .flex()
                .flex_row()
                .items_center()
                .gap(px(6.0))
                .child(
                    div()
                        .w(px(8.0))
                        .h(px(8.0))
                        .rounded_full()
                        .bg(rgb(crate::ui::theme::GREEN)),
                )
                .child(format!(
                    "{live} process{}",
                    if live == 1 { "" } else { "es" }
                )),
        );
        let mut right = div().flex().flex_row().items_center().h_full();
        if let Some(status) = self.history_status_text() {
            right = right.child(
                div()
                    .px(px(10.0))
                    .h_full()
                    .flex()
                    .items_center()
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(|view, _, window, cx| {
                            if view.shutting_down {
                                return;
                            }
                            cx.stop_propagation();
                            window.focus(&view.focus_handle);
                            view.history_opt_in_key(cx);
                        }),
                    )
                    .child(status),
            );
        }
        right = right.child(
            div()
                .px(px(10.0))
                .h_full()
                .flex()
                .items_center()
                .child(format!("{:.0}px", self.font_size)),
        );
        right = right.child(
            div()
                .px(px(10.0))
                .h_full()
                .flex()
                .items_center()
                .child("UTF-8"),
        );
        crate::ui::metrics::text_role(
            div()
                .h(px(crate::ui::geometry::STATUS_H))
                .flex()
                .flex_row()
                .items_center()
                .justify_between()
                .bg(rgb(crate::ui::theme::HEADER_BG))
                .border_t_1()
                .border_color(rgb(crate::ui::theme::BORDER)),
            crate::ui::metrics::META_10,
        )
        .text_color(rgb(crate::ui::theme::MUTED))
        .child(left)
        .child(right)
    }

    /// `Ctrl+P` overlay in VSCode Quick Open style: centered floating box
    /// with an input row (magnifier, query, caret), a divider, filename +
    /// dimmed-parent result rows with accent match highlights, and footer
    /// hints. Enter opens through `FileCommand::Open`; Esc dismisses.
    fn render_ctrlp(&mut self, box_x: f32, box_w: f32, cx: &mut Context<Self>) -> Div {
        // Input row: magnifier + query with a 2px block caret hugging the
        // last character (a text-pipe caret would add glyph side bearings
        // on top of the row gap), or a dimmed placeholder when empty. The
        // caret is exactly one text line tall (never the padded row) and
        // blinks via `ctrlp_caret_on`, holding its 2px slot while hidden
        // so the query text never shifts.
        let input = if self.ctrlp_query.is_empty() {
            div()
                .flex()
                .flex_row()
                .items_center()
                .gap_2()
                .child(
                    div()
                        .text_color(rgb(0x71717A))
                        .child(crate::ui::assets::icon(
                            crate::ui::assets::SEARCH,
                            14.0,
                            0x71717A,
                        )),
                )
                .child(
                    div()
                        .text_color(rgb(0x71717A))
                        .child("Search files by name…"),
                )
        } else {
            div()
                .flex()
                .flex_row()
                .items_center()
                .gap_2()
                .child(
                    div()
                        .text_color(rgb(0x71717A))
                        .child(crate::ui::assets::icon(
                            crate::ui::assets::SEARCH,
                            14.0,
                            0x71717A,
                        )),
                )
                .child({
                    let caret_h = px(f32::from(self.fonts(&*cx).line_height));
                    let before = self.ctrlp_query[..self.ctrlp_caret_byte].to_owned();
                    let after = self.ctrlp_query[self.ctrlp_caret_byte..].to_owned();
                    let caret_bg = if self.ctrlp_caret_on {
                        rgb(0x71717A)
                    } else {
                        rgba(0x00000000)
                    };
                    div()
                        .flex()
                        .flex_row()
                        .items_center()
                        .child(div().child(before))
                        .child(div().w(px(2.0)).h(caret_h).bg(caret_bg))
                        .child(div().child(after))
                })
        };
        let mut overlay = div()
            .flex()
            .flex_col()
            .rounded_md()
            .border_1()
            .border_color(rgb(0x52525B))
            .shadow_lg()
            .bg(rgb(0x18181B))
            .text_color(rgb(0xE4E4E7))
            .child(div().px_3().py_2().child(input))
            .child(div().h(px(1.0)).w_full().bg(rgb(0x2E2E33)));
        if self.ctrlp_results.is_empty() {
            let empty_message = if self.ctrlp_source_error.is_some() {
                "File search unavailable; workspace results may still be shown."
            } else if self.ctrlp_query == ">" {
                "Type a command to search actions."
            } else if self.ctrlp_query.is_empty() {
                "Search commands, projects, tabs, panes, files and Git paths."
            } else {
                "No matches."
            };
            overlay = overlay.child(
                div()
                    .px_3()
                    .py_2()
                    .text_color(rgb(if self.ctrlp_source_error.is_some() {
                        0xFBBF24
                    } else {
                        0x71717A
                    }))
                    .child(empty_message),
            );
            overlay = overlay.child(Self::ctrlp_hint_row());
            return self.ctrlp_frame(overlay, box_x, box_w);
        }
        let query = self.palette_query().to_owned();
        let list_entries = Arc::new(self.ctrlp_results.clone());
        let list_count = list_entries.len().min(palette::MAX_PALETTE_RESULTS);
        let selected = self.ctrlp_selected;
        let result_list = uniform_list(
            "palette-results",
            list_count,
            cx.processor(move |_view, range: std::ops::Range<usize>, _window, _cx| {
                range
                    .map(|index| {
                        let entry = list_entries[index].clone();
                        let selected = index == selected;
                        let accent = HighlightStyle {
                            color: Some(hsla(0.594, 1.0, 0.649, 1.0)),
                            ..Default::default()
                        };
                        let label_hits = omaterm_context::fuzzy_match_indices(&entry.label, &query)
                            .map(|(_, indices)| indices)
                            .unwrap_or_default();
                        let label = StyledText::new(entry.label.clone()).with_highlights(
                            files::highlight_ranges(&entry.label, &label_hits)
                                .into_iter()
                                .map(|range| (range, accent)),
                        );
                        let mut row = div()
                            .id(index)
                            .flex()
                            .flex_row()
                            .items_center()
                            .px_2()
                            .h(px(32.0))
                            .rounded_sm()
                            .bg(rgb(if selected { 0x27272A } else { 0x18181B }))
                            .text_color(rgb(if selected { 0xFAFAFA } else { 0xA1A1AA }));
                        if selected {
                            row = row.border_l_2().border_color(rgb(files::MATCH_ACCENT));
                        }
                        row.on_mouse_down(
                            MouseButton::Left,
                            _cx.listener(move |view, event: &MouseDownEvent, window, cx| {
                                if view.shutting_down {
                                    return;
                                }
                                window.focus(&view.focus_handle);
                                view.palette_select(index);
                                // M19 Phase D interim: Ctrl+click opens file
                                // results in the native editor.
                                if event.modifiers.control {
                                    view.ctrlp_open_in_editor(cx);
                                } else {
                                    view.ctrlp_confirm(cx);
                                }
                            }),
                        )
                        .child(
                            div()
                                .flex()
                                .flex_row()
                                .flex_1()
                                .items_center()
                                .gap_2()
                                .overflow_hidden()
                                .child(
                                    div()
                                        .w(px(68.0))
                                        .flex()
                                        .flex_shrink_0()
                                        .items_center()
                                        .justify_center()
                                        .text_size(px(9.0))
                                        .text_color(rgb(crate::ui::theme::MUTED))
                                        .child(format!("{:?}", entry.kind)),
                                )
                                .child(div().flex_1().min_w(px(0.0)).truncate().child(label)),
                        )
                    })
                    .collect::<Vec<_>>()
            }),
        )
        .track_scroll(self.palette_scroll_handle.clone())
        .h(px(360.0));
        overlay = overlay.child(result_list);
        if self.ctrlp_source_error.is_some() {
            overlay = overlay.child(
                div()
                    .px_3()
                    .py_1()
                    .text_size(px(10.0))
                    .text_color(rgb(0xFBBF24))
                    .child("File search unavailable; other matching sources remain available."),
            );
        }
        if self.ctrlp_truncated {
            overlay = overlay.child(
                div()
                    .px_3()
                    .py_1()
                    .text_color(rgb(0x71717A))
                    .child("(results capped at 100; some source results omitted)"),
            );
        }
        overlay = overlay.child(Self::ctrlp_hint_row());
        self.ctrlp_frame(overlay, box_x, box_w)
    }

    /// Footer hint row living inside the box, so it shares the box's
    /// centering instead of needing its own.
    fn ctrlp_hint_row() -> Div {
        div()
            .px_3()
            .py_1()
            .text_color(rgb(0x71717A))
            .child("up/down navigate · enter run/open · type `>` for commands · esc dismiss")
    }

    /// True floating layer, VSCode Quick Open style: absolutely positioned
    /// over the terminal content (painted last, so always on top — GPUI
    /// 0.2.2 has no z-index) at an explicitly computed offset. The box
    /// position and width are plain arithmetic from the viewport constants
    /// (sidebar widths are fixed), deliberately avoiding any reliance on
    /// max-width/align/spacer interplay. No backdrop dim: shadow and border
    /// carry the elevation and the terminal stays fully visible around it.
    fn ctrlp_frame(&mut self, overlay: Div, box_x: f32, box_w: f32) -> Div {
        div()
            .absolute()
            .left(px(box_x))
            .top(px(72.0))
            .child(overlay.w(px(box_w)))
    }
}

impl Render for WorkspaceView {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        self.resize_panes_to_window(window, cx);
        let viewport = window.viewport_size();
        let main_view_width = crate::ui::geometry::shell_rects(
            viewport.width.into(),
            viewport.height.into(),
            self.projects_visible,
            self.projects_width,
            self.inspector_visible,
            self.inspector_width,
        )
        .main_view
        .2;
        let content = self
            .coordinator
            .tree()
            .root()
            .cloned()
            .map(|root| self.render_node(&root, window, cx))
            .unwrap_or_else(|| {
                if let Some((message, _)) = self.spawn_failure.clone() {
                    return div()
                        .size_full()
                        .flex()
                        .flex_col()
                        .items_center()
                        .justify_center()
                        .gap_3()
                        .bg(rgb(0x18181B))
                        .text_color(rgb(0xE4E4E7))
                        .child("The shell could not be started.")
                        .child(div().text_color(rgb(0xF87171)).child(message))
                        .child(
                            div()
                                .px_3()
                                .py_2()
                                .border_1()
                                .border_color(rgb(0x52525B))
                                .on_mouse_down(
                                    MouseButton::Left,
                                    cx.listener(|view, _, window, cx| {
                                        window.focus(&view.focus_handle);
                                        view.retry_spawn(cx);
                                    }),
                                )
                                .child("Retry"),
                        )
                        .into_any_element();
                }
                let has_project = self.coordinator.selected_project_id().is_some();
                let (prompt, action) = if has_project {
                    ("This project has no tabs.", "New tab (Ctrl+Shift+T)")
                } else {
                    ("No projects yet.", "New project (Ctrl+Alt+N)")
                };
                div()
                    .size_full()
                    .flex()
                    .flex_col()
                    .items_center()
                    .justify_center()
                    .gap_2()
                    .bg(rgb(0x18181B))
                    .text_color(rgb(0xA1A1AA))
                    .child(prompt)
                    .child(
                        div()
                            .px_3()
                            .py_2()
                            .border_1()
                            .border_color(rgb(0x52525B))
                            .text_color(rgb(0xE4E4E7))
                            .on_mouse_down(
                                MouseButton::Left,
                                cx.listener(move |view, _event: &MouseDownEvent, window, cx| {
                                    window.focus(&view.focus_handle);
                                    view.user_focus_action();
                                    if has_project {
                                        view.create_tab(cx);
                                    } else {
                                        view.open_project_directory(cx);
                                    }
                                }),
                            )
                            .child(action),
                    )
                    .into_any_element()
            });
        // UI v5: the project list lives in the persistent left sidebar
        // (`render_projects_sidebar`) and the tab strip in the 42px header
        // (`render_header`); both built below at composition time.
        // UI v5: the tab strip is built by `render_header` at composition
        // time (see below); the legacy workbench strip is retired.
        // UI v5 composition happens below, after `pane_area` is built.
        let mut pane_area = div()
            .flex()
            .flex_1()
            .flex_col()
            .size_full()
            .min_w(px(0.0))
            .min_h(px(0.0))
            .relative();
        if self.editor_pending_action.is_some() {
            let mut prompt = div()
                .flex()
                .items_center()
                .gap_2()
                .px_3()
                .py_2()
                .bg(rgb(workbench::WARN_BG))
                .text_color(rgb(workbench::WARN_TEXT))
                .child("Unsaved changes. Choose explicitly:");
            for (choice, label) in [(0, "Cancel (Esc)"), (1, "Save (S)"), (2, "Discard (D)")] {
                prompt = prompt.child(
                    div()
                        .cursor_pointer()
                        .px_2()
                        .py_1()
                        .border_1()
                        .on_mouse_down(
                            MouseButton::Left,
                            cx.listener(move |view, _, window, cx| {
                                cx.stop_propagation();
                                window.focus(&view.focus_handle);
                                view.editor_resolve_pending(choice, cx);
                            }),
                        )
                        .child(label),
                );
            }
            pane_area = pane_area.child(prompt);
        }
        if let Some((arm, at)) = self.history_arm
            && at.elapsed() < HISTORY_ARM_WINDOW
        {
            pane_area = pane_area.child(
                div()
                    .px_3()
                    .py_1()
                    .bg(rgb(workbench::WARN_BG))
                    .text_color(rgb(workbench::WARN_TEXT))
                    .child(Self::history_arm_text(&arm)),
            );
        }
        if let Some(message) = self.persistence_warning.clone() {
            pane_area = pane_area.child(
                div()
                    .px_3()
                    .py_1()
                    .bg(rgb(workbench::WARN_BG))
                    .text_color(rgb(workbench::WARN_TEXT))
                    .child(message),
            );
        }
        if let Some(message) = self.config_warning.clone() {
            pane_area = pane_area.child(
                div()
                    .px_3()
                    .py_1()
                    .bg(rgb(workbench::WARN_BG))
                    .text_color(rgb(workbench::WARN_TEXT))
                    .child(message),
            );
        }
        if let Some(message) = self.input_notice.clone() {
            pane_area = pane_area.child(
                div()
                    .px_3()
                    .py_1()
                    .bg(rgb(workbench::WARN_BG))
                    .text_color(rgb(workbench::WARN_TEXT))
                    .child(message),
            );
        }
        if let Some(manager) = self.coordinator.history_manager()
            && let Some(message) = manager.warning()
        {
            pane_area = pane_area.child(
                div()
                    .px_3()
                    .py_1()
                    .bg(rgb(workbench::WARN_BG))
                    .text_color(rgb(workbench::WARN_TEXT))
                    .child(format!("History: {message}")),
            );
        }
        if !self.coordinator.tree().is_empty()
            && let Some((message, _)) = self.spawn_failure.clone()
        {
            pane_area = pane_area.child(
                div()
                    .flex()
                    .flex_row()
                    .items_center()
                    .justify_between()
                    .px_3()
                    .py_1()
                    .bg(rgb(workbench::ERROR_BG))
                    .text_color(rgb(workbench::ERROR_TEXT))
                    .child(message)
                    .child(
                        div()
                            .px_2()
                            .py_1()
                            .text_color(rgb(workbench::ERROR_TEXT))
                            .on_mouse_down(
                                MouseButton::Left,
                                cx.listener(|view, _, window, cx| {
                                    window.focus(&view.focus_handle);
                                    view.retry_spawn(cx);
                                }),
                            )
                            .child("Retry"),
                    ),
            );
        }
        // M19 editor surface: an active document replaces the main area
        // like the diff preview, with priority over it. Buffers survive
        // tab switches and preview changes in the router-owned store;
        // only this view-local activation is cleared by terminal tabs.
        let editor_surface = self.coordinator.selected_project_id().and_then(|project| {
            self.editor_active_doc(project)
                .map(|document| (project, document))
        });
        // M15 diff preview tab: when open for the selected project, the
        // main area shows the file diff instead of the terminal pane tree.
        // Core tabs are untouched — selecting a terminal tab closes this.
        let preview_project = self
            .coordinator
            .selected_project_id()
            .filter(|project| self.diff_panel.preview_open(*project));
        if let Some((project, document)) = editor_surface {
            pane_area =
                pane_area.child(self.render_editor(project, document, main_view_width, window, cx));
        } else if let Some(project) = preview_project {
            pane_area =
                pane_area.child(self.render_diff_preview(project, main_view_width, window, cx));
        } else {
            pane_area = pane_area.child(
                div()
                    .flex()
                    .flex_1()
                    .size_full()
                    .min_w(px(0.0))
                    .min_h(px(0.0))
                    .overflow_hidden()
                    .child(content),
            );
        }
        // Toast paints last inside the relative pane area: bottom-center,
        // above the status bar, pointer-transparent (no handlers).
        if let Some((message, deadline)) = self.toast.clone()
            && Instant::now() < deadline
        {
            pane_area = pane_area.child(
                div()
                    .absolute()
                    .bottom(px(14.0))
                    .left(px(0.0))
                    .right(px(0.0))
                    .flex()
                    .flex_row()
                    .justify_center()
                    .child(
                        div()
                            .px(px(12.0))
                            .py(px(8.0))
                            .rounded_md()
                            .border_1()
                            .border_color(rgb(crate::ui::theme::BORDER2))
                            .bg(rgb(crate::ui::theme::PANEL3))
                            .shadow_lg()
                            .text_size(px(11.0))
                            .text_color(rgb(crate::ui::theme::TEXT))
                            .child(message),
                    ),
            );
        }
        // Finder paints last so the floating layer sits above the terminal.
        // Box geometry is plain arithmetic from the v5 main-view rectangle
        // (the same helper that sizes PTY grids) — no reliance on
        // align/max interplay.
        if self.ctrlp_open {
            let viewport_w: f32 = window.viewport_size().width.into();
            let viewport_h: f32 = window.viewport_size().height.into();
            let shell = crate::ui::geometry::shell_rects(
                viewport_w,
                viewport_h,
                self.projects_visible,
                self.projects_width,
                self.inspector_visible,
                self.inspector_width,
            );
            let pane_w = shell.main_view.2.max(1.0);
            let box_w = (pane_w - 32.0).clamp(200.0, 600.0);
            // Relative to the center column origin (the overlay's parent),
            // not the window: the finder floats over the main view only.
            let box_x = ((pane_w - box_w) / 2.0).max(0.0);
            pane_area = pane_area.child(self.render_ctrlp(box_x, box_w, cx));
        }
        // UI v5 frame: Projects | resizer | (header over main+inspector,
        // then main | resizer | inspector), then the global status bar.
        let viewport_h: f32 = window.viewport_size().height.into();
        let header = self.render_header(cx);
        let mut center_row = div()
            .flex()
            .flex_1()
            .flex_row()
            .min_w(px(0.0))
            .min_h(px(0.0))
            .overflow_hidden();
        center_row = center_row.child(
            div()
                .flex()
                .flex_1()
                .flex_col()
                .size_full()
                .min_w(px(0.0))
                .child(header)
                .child(
                    div()
                        .flex()
                        .flex_1()
                        .flex_row()
                        .size_full()
                        .min_w(px(0.0))
                        .min_h(px(0.0))
                        .child(
                            div()
                                .flex()
                                .flex_1()
                                .flex_col()
                                .size_full()
                                .min_w(px(0.0))
                                .child(pane_area),
                        )
                        .child(if self.inspector_visible {
                            self.render_inspector_resizer(cx).into_any_element()
                        } else {
                            div().into_any_element()
                        })
                        .child(if self.inspector_visible {
                            self.render_inspector(viewport_h, cx).into_any_element()
                        } else {
                            div().into_any_element()
                        }),
                ),
        );
        let mut content_row = div()
            .flex()
            .flex_1()
            .flex_row()
            .min_w(px(0.0))
            .min_h(px(0.0));
        if self.projects_visible {
            content_row = content_row.child(self.render_projects_sidebar(cx));
            content_row = content_row.child(self.render_projects_resizer(cx));
        }
        content_row = content_row.child(center_row);
        let status_bar = self.render_status_bar(cx);
        // Reveal the Ctrl+Shift+1..9 jump indexes in the sidebar only while
        // Control or Shift is held.
        let weak = cx.entity().downgrade();
        let drop_weak = weak.clone();
        div()
            .track_focus(&self.focus_handle)
            .on_key_down(cx.listener(Self::on_key_down))
            .on_drop(move |paths: &ExternalPaths, _, cx| {
                let _ = drop_weak.update(cx, |view: &mut WorkspaceView, cx| {
                    view.on_file_drop(paths, cx);
                });
            })
            .on_modifiers_changed(move |event: &ModifiersChangedEvent, _, cx| {
                let show = event.modifiers.control || event.modifiers.shift;
                let _ = weak.update(cx, |view: &mut WorkspaceView, cx| {
                    if view.show_project_hints != show {
                        view.show_project_hints = show;
                        cx.notify();
                    }
                });
            })
            .size_full()
            .flex()
            .flex_col()
            .bg(rgb(crate::ui::theme::BG))
            .text_color(rgb(crate::ui::theme::TEXT))
            .child(content_row)
            .child(status_bar)
    }
}

/// Basename of `$SHELL` (`bash`, `fish`, …) for pane chrome labels.
/// Falls back to `shell` when unset or unparseable — never empty.
fn shell_name() -> String {
    std::env::var("SHELL")
        .ok()
        .and_then(|shell| {
            std::path::Path::new(&shell)
                .file_name()
                .map(|name| name.to_string_lossy().into_owned())
        })
        .filter(|name| !name.is_empty())
        .unwrap_or_else(|| "shell".to_string())
}

/// Monospace family for diff code rows: the configured terminal family
/// when installed, else the built-in preference stack (same resolution
/// as the terminal grid, so code never renders in the UI face).
fn mono_family_for_chrome(cx: &App, configured: Option<&str>) -> String {
    select_mono_family(&cx.text_system().all_font_names(), configured)
}

/// Line-number gutter for diff rows: right-aligned number or an empty
/// spacer that keeps add/delete-only rows aligned with their siblings.
/// Split gutters are 54px; inline gutters are measured 46px.
fn diff_gutter(no: Option<u32>, color: u32) -> Div {
    diff_gutter_w(no, color, 54.0)
}

fn diff_gutter_w(no: Option<u32>, color: u32, width: f32) -> Div {
    div()
        .w(px(width))
        .h_full()
        .flex()
        .flex_row()
        .items_center()
        .justify_end()
        .flex_shrink_0()
        .text_right()
        .text_color(rgb(color))
        .child(
            div()
                .mr(px(14.0))
                .child(no.map(|n| n.to_string()).unwrap_or_default()),
        )
}

/// Background + inset mark for an added/removed diff row. GPUI has no
/// inset-shadow primitive, so the mock's 3px mark renders as a 2px left
/// border (recorded 1px delta); context rows stay transparent. Alpha is
/// significant here: `rgba()` preserves the .12/.8 translucency, while
/// `rgb()` would force opaque and misread the packed bytes.
fn diff_row_decor(kind: omaterm_core::DiffLineKind) -> Div {
    match kind {
        omaterm_core::DiffLineKind::Addition => div()
            .flex()
            .flex_row()
            .items_center()
            .bg(rgba(crate::ui::theme::DIFF_ADD_BG))
            .border_l_2()
            .border_color(rgba(crate::ui::theme::DIFF_ADD_MARK)),
        omaterm_core::DiffLineKind::Deletion => div()
            .flex()
            .flex_row()
            .items_center()
            .bg(rgba(crate::ui::theme::DIFF_DEL_BG))
            .border_l_2()
            .border_color(rgba(crate::ui::theme::DIFF_DEL_MARK)),
        omaterm_core::DiffLineKind::Context => div().flex().flex_row().items_center(),
    }
}

/// One Split cell: gutter + its own code/text, or a blank spacer when the
/// paired edit has no line on this side.
fn split_cell(
    cell: Option<&diff_panel::SplitCell>,
    mono: &str,
    scroll: &ScrollHandle,
    content_width: f32,
) -> Div {
    let Some(cell) = cell else {
        return div().flex_1().min_w(px(0.0)).h(px(21.0));
    };
    let no_color = match cell.kind {
        omaterm_core::DiffLineKind::Addition => crate::ui::theme::LINE_NO_ADD,
        omaterm_core::DiffLineKind::Deletion => crate::ui::theme::LINE_NO_DEL,
        omaterm_core::DiffLineKind::Context => crate::ui::theme::LINE_NO,
    };
    div()
        .flex_1()
        .min_w(px(0.0))
        .flex()
        .flex_row()
        .items_center()
        .h(px(21.0))
        .font_family(mono.to_string())
        .text_size(px(12.0))
        .text_color(rgb(crate::ui::theme::TEXT))
        .child(
            diff_row_decor(cell.kind)
                .flex_1()
                .h_full()
                .min_w(px(0.0))
                .child(diff_gutter(cell.line_no, no_color))
                .child(
                    div()
                        .id("diff-code-x")
                        .flex_1()
                        .min_w(px(0.0))
                        .overflow_x_scroll()
                        .track_scroll(scroll)
                        .map(|mut code| {
                            code.style().restrict_scroll_to_axis = Some(true);
                            code
                        })
                        .whitespace_nowrap()
                        .child(
                            div()
                                .w(px(content_width + 8.0))
                                .flex_shrink_0()
                                .child(cell.text.clone()),
                        ),
                ),
        )
}

/// Measured once per presentation/font identity, using the same font and size
/// as the code cells. Fallback glyphs and combining/wide text go through GPUI's
/// shaper instead of terminal-cell or character-count estimates.
fn measured_diff_width(rows: &[diff_panel::PreviewRow], mono: &str, window: &Window) -> f32 {
    let measure = |text: &str| -> f32 {
        window
            .text_system()
            .shape_line(
                SharedString::from(text.to_owned()),
                px(12.0),
                &[TextRun {
                    len: text.len(),
                    font: font(mono.to_owned()),
                    color: rgb(crate::ui::theme::TEXT).into(),
                    background_color: None,
                    underline: None,
                    strikethrough: None,
                }],
                None,
            )
            .width
            .into()
    };
    rows.iter()
        .map(|row| match row {
            diff_panel::PreviewRow::Inline(line) => measure(&line.text),
            diff_panel::PreviewRow::Split(line) => line
                .old
                .iter()
                .chain(line.new.iter())
                .map(|cell| measure(&cell.text))
                .fold(0.0, f32::max),
            diff_panel::PreviewRow::HunkHeader { header, .. } => measure(header),
            _ => 0.0,
        })
        .fold(0.0, f32::max)
}

/// One Inline row: old + new gutters followed by code.
fn inline_row(row: &diff_panel::AlignedRow, mono: &str) -> Div {
    let no_color = match row.kind {
        omaterm_core::DiffLineKind::Addition => crate::ui::theme::LINE_NO_ADD,
        omaterm_core::DiffLineKind::Deletion => crate::ui::theme::LINE_NO_DEL,
        omaterm_core::DiffLineKind::Context => crate::ui::theme::LINE_NO,
    };
    div()
        .flex()
        .flex_row()
        .items_center()
        .w_full()
        .h(px(21.0))
        .font_family(mono.to_string())
        .text_size(px(12.0))
        .text_color(rgb(crate::ui::theme::TEXT))
        .child(
            diff_row_decor(row.kind)
                .flex_1()
                .h_full()
                .min_w(px(0.0))
                .child(diff_gutter_w(
                    row.old_no,
                    {
                        if row.old_no.is_some() {
                            no_color
                        } else {
                            crate::ui::theme::EDITOR_BG
                        }
                    },
                    46.0,
                ))
                .child(diff_gutter_w(
                    row.new_no,
                    {
                        if row.new_no.is_some() {
                            no_color
                        } else {
                            crate::ui::theme::EDITOR_BG
                        }
                    },
                    46.0,
                ))
                .child(
                    div()
                        .flex_1()
                        .min_w(px(0.0))
                        .overflow_hidden()
                        .whitespace_nowrap()
                        .child(row.text.clone()),
                ),
        )
}

/// Shorten `$HOME`-prefixed paths with `~` for pane chrome labels.
fn short_home_path(path: &std::path::Path) -> String {
    let text = path.to_string_lossy().into_owned();
    if let Ok(home) = std::env::var("HOME")
        && let Some(rest) = text.strip_prefix(&home)
    {
        return format!("~{rest}");
    }
    text
}

/// Vertical chrome inside every terminal leaf: 31px header + 1px border +
/// 27px footer + 1px border. Subtracted from the leaf height before grid
/// sizing so PTY rows match the visible canvas exactly.
const LEAF_CHROME_H: f32 = 60.0;

/// Git row action behind an inspector icon. Kept next to the renderer so
/// the icon → dispatch mapping needs no string matching.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum GitRowAction {
    Stage,
    Unstage,
    Discard,
    Open,
}

/// Toast-sized summary of staged/unstaged paths: the file name for a
/// single path, otherwise the count (`3 files`).
fn summarize_paths(paths: &[std::path::PathBuf]) -> String {
    if paths.len() == 1 {
        return paths[0]
            .file_name()
            .map(|name| name.to_string_lossy().into_owned())
            .unwrap_or_else(|| "file".to_string());
    }
    format!("{} files", paths.len())
}

/// Map a Ctrl+Shift+<n> key name to a 0-based project index.
///
/// Shift applies to the character before GPUI reports it, so on a US layout
/// `Ctrl+Shift+1` arrives as `!`, `Ctrl+Shift+2` as `@`, and so on. Both the
/// shifted symbol and the raw digit map to the same slot.
fn project_jump_index(key_name: &str) -> Option<usize> {
    let slot = match key_name {
        "1" | "!" => 1,
        "2" | "@" => 2,
        "3" | "#" => 3,
        "4" | "$" => 4,
        "5" | "%" => 5,
        "6" | "^" => 6,
        "7" | "&" => 7,
        "8" | "*" => 8,
        "9" | "(" => 9,
        _ => return None,
    };
    Some(slot - 1)
}

/// Translate a GPUI key event into the GPUI-free [`KeyEvent`].
fn translate_key(event: &KeyDownEvent, app_cursor: bool, app_keypad: bool) -> Option<KeyEvent> {
    let modifiers = &event.keystroke.modifiers;
    let mods = KeyModifiers {
        ctrl: modifiers.control,
        alt: modifiers.alt,
        shift: modifiers.shift,
        super_key: modifiers.platform,
    };
    let key = match event.keystroke.key.to_lowercase().replace('_', "").as_str() {
        // "return" is what some Wayland virtual keyboards (e.g. wtype
        // `-k Return`) report for the main Enter key; "kpenter" is the
        // keypad variant. Physical keyboards report "enter".
        "enter" | "return" | "kpenter" => Key::Enter,
        "tab" => Key::Tab,
        "backspace" => Key::Backspace,
        "escape" => Key::Escape,
        "left" => Key::Left,
        "right" => Key::Right,
        "up" => Key::Up,
        "down" => Key::Down,
        "home" => Key::Home,
        "end" => Key::End,
        "pageup" => Key::PageUp,
        "pagedown" => Key::PageDown,
        "insert" => Key::Insert,
        "delete" => Key::Delete,
        name if name.starts_with('f') => Key::F(name[1..].parse().ok()?),
        _ => {
            let ch = event
                .keystroke
                .key_char
                .as_ref()
                .and_then(|s| s.chars().next())
                .or_else(|| event.keystroke.key.chars().next())?;
            Key::Char(ch)
        }
    };
    Some(KeyEvent {
        key,
        modifiers: mods,
        app_cursor,
        app_keypad,
    })
}

/// Preferred monospace families, in order.
const MONO_PREFERENCES: &[&str] = &[
    "JetBrainsMono Nerd Font",
    "JetBrainsMono NF",
    "DejaVu Sans Mono",
    "Liberation Mono",
    "Noto Sans Mono",
    "monospace",
];

/// Glyph fallback chain for symbols the primary font lacks.
fn symbol_fallbacks() -> FontFallbacks {
    FontFallbacks::from_fonts(
        [
            "JetBrainsMono Nerd Font",
            "JetBrainsMono NF",
            "Noto Color Emoji",
            "DejaVu Sans Mono",
        ]
        .iter()
        .map(ToString::to_string)
        .collect(),
    )
}

/// Pick the first preferred family installed on this machine. A configured
/// `terminal.font-family` wins when installed; otherwise the built-in stack
/// applies. Pure over the installed list so the preference order is unit
/// tested without a GPUI text system.
fn select_mono_family(installed: &[String], configured: Option<&str>) -> String {
    if let Some(family) = configured
        && installed.iter().any(|name| name == family)
    {
        return family.to_string();
    }
    for preferred in MONO_PREFERENCES {
        if installed.iter().any(|name| name == preferred) {
            return (*preferred).to_string();
        }
    }
    "monospace".to_string()
}

/// Terminal font set: base + bold/italic variants, ligatures disabled.
fn terminal_fonts(cx: &App, configured: Option<&str>) -> [Font; 4] {
    let installed = cx.text_system().all_font_names();
    if let Some(family) = configured
        && !installed.iter().any(|name| name == family)
    {
        tracing::warn!(target: "omaterm::render", "terminal.font-family '{family}' is not installed; using fallback");
    }
    let family = select_mono_family(&installed, configured);
    let fallbacks = symbol_fallbacks();
    let base = Font {
        family: family.clone().into(),
        features: gpui::FontFeatures::disable_ligatures(),
        fallbacks: Some(fallbacks.clone()),
        ..font(&family)
    };
    let bold = Font {
        family: family.clone().into(),
        features: gpui::FontFeatures::disable_ligatures(),
        fallbacks: Some(fallbacks.clone()),
        ..font(&family).bold()
    };
    let italic = Font {
        family: family.clone().into(),
        features: gpui::FontFeatures::disable_ligatures(),
        fallbacks: Some(fallbacks.clone()),
        ..font(&family).italic()
    };
    let bold_italic = Font {
        family: family.clone().into(),
        features: gpui::FontFeatures::disable_ligatures(),
        fallbacks: Some(fallbacks),
        ..font(&family).bold().italic()
    };
    [base, bold, italic, bold_italic]
}

fn style_index(bold: bool, italic: bool) -> usize {
    match (bold, italic) {
        (false, false) => 0,
        (true, false) => 1,
        (false, true) => 2,
        (true, true) => 3,
    }
}

/// Resolved font IDs plus grid metrics, cached per font size.
#[derive(Debug, Clone)]
struct ResolvedFonts {
    fonts: [Font; 4],
    cell_width: Pixels,
    line_height: Pixels,
    font_size: Pixels,
}

fn resolve_terminal_fonts(
    cx: &App,
    font_size: Pixels,
    configured: Option<String>,
) -> ResolvedFonts {
    let fonts = terminal_fonts(cx, configured.as_deref());
    let base_id = cx.text_system().resolve_font(&fonts[0]);
    for variant in &fonts[1..] {
        cx.text_system().resolve_font(variant);
    }
    let cell_width = cx
        .text_system()
        .advance(base_id, font_size, 'M')
        .map(|size| size.width)
        .unwrap_or(px(8.4));
    #[cfg(debug_assertions)]
    {
        let probes = ['i', 'W', ' ', '0', '-', 'M'];
        for probe in probes {
            if let Ok(advance) = cx.text_system().advance(base_id, font_size, probe) {
                let a: f32 = cell_width.into();
                let b: f32 = advance.width.into();
                if (a - b).abs() > 0.5 {
                    tracing::warn!(target: "omaterm::render", "terminal font is not monospace: 'M'={a}px vs {probe:?}={b}px");
                    break;
                }
            }
        }
    }
    let ascent = cx.text_system().ascent(base_id, font_size);
    let descent = cx.text_system().descent(base_id, font_size);
    let line_height = {
        let a: f32 = ascent.into();
        let d: f32 = descent.into();
        px(a + d.abs())
    };
    tracing::info!(
        target: "omaterm::render",
        family = %cx.text_system().get_font_for_id(base_id).map(|font| font.family.to_string()).unwrap_or_else(|| "<unknown>".to_string()),
        "terminal font resolved",
    );
    ResolvedFonts {
        fonts,
        cell_width,
        line_height,
        font_size,
    }
}

fn fg_color(cell_fg: TermColor, inverse: bool) -> Hsla {
    if inverse {
        return match cell_fg {
            TermColor::Rgb(r, g, b) => rgb(rgb_hex(r, g, b)).into(),
            TermColor::DefaultFg => rgb(0x18181B).into(),
            TermColor::DefaultBg => rgb(0xE4E4E7).into(),
        };
    }
    match cell_fg {
        TermColor::Rgb(r, g, b) => rgb(rgb_hex(r, g, b)).into(),
        TermColor::DefaultFg => rgb(0xE4E4E7).into(),
        TermColor::DefaultBg => rgb(0x18181B).into(),
    }
}

/// Background paint color. `None` means transparent (root background shows).
fn bg_paint(cell_bg: TermColor, inverse: bool) -> Option<Hsla> {
    if inverse {
        return Some(match cell_bg {
            TermColor::Rgb(r, g, b) => rgb(rgb_hex(r, g, b)).into(),
            TermColor::DefaultFg => rgb(0xE4E4E7).into(),
            TermColor::DefaultBg => rgb(0xE4E4E7).into(),
        });
    }
    match cell_bg {
        TermColor::Rgb(r, g, b) => Some(rgb(rgb_hex(r, g, b)).into()),
        TermColor::DefaultFg => Some(rgb(0xE4E4E7).into()),
        TermColor::DefaultBg => None,
    }
}

fn rgb_hex(r: u8, g: u8, b: u8) -> u32 {
    (u32::from(r) << 16) | (u32::from(g) << 8) | u32::from(b)
}

/// Paint parameters bundled so `paint_terminal` stays under clippy's
/// `too_many_arguments` threshold.
struct PaintArgs<'a> {
    snapshot: &'a TerminalViewport,
    fonts: &'a ResolvedFonts,
    cursor_color: Hsla,
    show_scrollbar: bool,
    selection: Option<(CellPoint, CellPoint)>,
}

fn paint_terminal(
    _bounds: Bounds<Pixels>,
    origin_bounds: Bounds<Pixels>,
    args: &PaintArgs,
    window: &mut Window,
    cx: &mut App,
) {
    let snapshot = args.snapshot;
    let fonts = args.fonts;
    let cursor_color = args.cursor_color;
    let show_scrollbar = args.show_scrollbar;
    let selection = args.selection;
    let origin = origin_bounds.origin;
    let cell_width = fonts.cell_width;
    let line_height = fonts.line_height;
    let font_size = fonts.font_size;

    let cursor_cell = if snapshot.cursor.visible
        && snapshot.display_offset == 0
        && snapshot.cursor.shape == omaterm_terminal::CursorShape::Block
    {
        Some((snapshot.cursor.row as usize, snapshot.cursor.col as usize))
    } else {
        None
    };

    for (row_idx, row) in snapshot.rows.iter().enumerate() {
        let y = origin.y + line_height * (row_idx as f32);

        let mut run_start: Option<(usize, Hsla)> = None;
        let flush_bg = |start: usize, end: usize, color: Hsla, window: &mut Window| {
            let x = origin.x + cell_width * (start as f32);
            let w = cell_width * ((end - start) as f32);
            window.paint_quad(gpui::fill(
                Bounds {
                    origin: gpui::Point { x, y },
                    size: gpui::Size {
                        width: w,
                        height: line_height,
                    },
                },
                color,
            ));
        };
        for (col_idx, cell) in row.cells.iter().enumerate() {
            let inverse = cell.flags.contains(omaterm_terminal::CellFlags::INVERSE);
            let bg = bg_paint(cell.bg, inverse);
            match (run_start, bg) {
                (Some((start, color)), Some(next)) if color == next => {
                    run_start = Some((start, color));
                }
                (Some((start, color)), _) => {
                    flush_bg(start, col_idx, color, window);
                    run_start = bg.map(|color| (col_idx, color));
                }
                (None, Some(color)) => run_start = Some((col_idx, color)),
                (None, None) => {}
            }
        }
        if let Some((start, color)) = run_start {
            flush_bg(start, row.cells.len(), color, window);
        }

        if let Some((sel_start, sel_end)) = selection
            && row_idx >= sel_start.row
            && row_idx <= sel_end.row
            && !row.cells.is_empty()
        {
            let last = row.cells.len() - 1;
            let c0 = if row_idx == sel_start.row {
                sel_start.col.min(last)
            } else {
                0
            };
            let c1 = if row_idx == sel_end.row {
                sel_end.col.min(last)
            } else {
                last
            };
            if c1 >= c0 {
                let x0 = origin.x + cell_width * (c0 as f32);
                let x1 = origin.x + cell_width * ((c1 + 1) as f32);
                window.paint_quad(gpui::fill(
                    Bounds {
                        origin: gpui::Point { x: x0, y },
                        size: gpui::Size {
                            width: x1 - x0,
                            height: line_height,
                        },
                    },
                    rgba(0x3B82F64D),
                ));
            }
        }

        let mut text = String::new();
        let mut runs: Vec<TextRun> = Vec::new();
        let mut run_start_len = 0usize;
        let mut current_style: Option<(Hsla, usize, bool, bool)> = None;
        let flush_run = |text: &str,
                         start: usize,
                         style: &Option<(Hsla, usize, bool, bool)>,
                         runs: &mut Vec<TextRun>,
                         fonts: &ResolvedFonts| {
            let Some((color, style_idx, underline, strike)) = style else {
                return;
            };
            let len = text.len() - start;
            if len == 0 {
                return;
            }
            runs.push(TextRun {
                len,
                font: fonts.fonts[*style_idx].clone(),
                color: *color,
                background_color: None,
                underline: underline.then(|| gpui::UnderlineStyle {
                    thickness: px(1.0),
                    color: Some(*color),
                    wavy: false,
                }),
                strikethrough: strike.then(|| gpui::StrikethroughStyle {
                    thickness: px(1.0),
                    color: Some(*color),
                }),
            });
        };

        for (col_idx, cell) in row.cells.iter().enumerate() {
            if cell.width == CellWidth::WideContinuation {
                continue;
            }
            if cursor_cell == Some((row_idx, col_idx)) {
                text.push(' ');
                continue;
            }
            let inverse = cell.flags.contains(omaterm_terminal::CellFlags::INVERSE);
            let hidden = cell.flags.contains(omaterm_terminal::CellFlags::HIDDEN);
            if hidden {
                continue;
            }
            let color = fg_color(cell.fg, inverse);
            let bold = cell.flags.contains(omaterm_terminal::CellFlags::BOLD);
            let italic = cell.flags.contains(omaterm_terminal::CellFlags::ITALIC);
            let style = style_index(bold, italic);
            let underline = cell.flags.contains(omaterm_terminal::CellFlags::UNDERLINE);
            let strike = cell
                .flags
                .contains(omaterm_terminal::CellFlags::STRIKETHROUGH);
            let style_key = (color, style, underline, strike);
            match &current_style {
                Some(current) if *current == style_key => {}
                _ => {
                    flush_run(&text, run_start_len, &current_style, &mut runs, fonts);
                    run_start_len = text.len();
                    current_style = Some(style_key);
                }
            }
            text.push_str(&cell.text);
        }
        flush_run(&text, run_start_len, &current_style, &mut runs, fonts);

        if !text.trim().is_empty() {
            let shaped =
                window
                    .text_system()
                    .shape_line(SharedString::from(text), font_size, &runs, None);
            let _ = shaped.paint(
                origin
                    + gpui::Point {
                        x: px(0.0),
                        y: line_height * (row_idx as f32),
                    },
                line_height,
                window,
                cx,
            );
            let _ = y;
        }
    }

    if snapshot.cursor.visible && snapshot.display_offset == 0 {
        let x = origin.x + cell_width * f32::from(snapshot.cursor.col);
        let y = origin.y + line_height * f32::from(snapshot.cursor.row);
        match snapshot.cursor.shape {
            omaterm_terminal::CursorShape::Block => {
                let row = snapshot.cursor.row as usize;
                let col = snapshot.cursor.col as usize;
                let (cell_text, cell_bg) = snapshot
                    .rows
                    .get(row)
                    .and_then(|row| row.cells.get(col))
                    .map(|cell| {
                        let inverse = cell.flags.contains(omaterm_terminal::CellFlags::INVERSE);
                        (cell.text.clone(), bg_paint(cell.bg, inverse))
                    })
                    .unwrap_or_default();
                window.paint_quad(gpui::fill(
                    Bounds {
                        origin: gpui::Point { x, y },
                        size: gpui::Size {
                            width: cell_width,
                            height: line_height,
                        },
                    },
                    cursor_color,
                ));
                let glyph_color: Hsla = cell_bg.unwrap_or(rgb(0x18181B).into());
                if !cell_text.trim().is_empty() {
                    let run_len = cell_text.len();
                    let shaped = window.text_system().shape_line(
                        SharedString::from(cell_text),
                        font_size,
                        &[TextRun {
                            len: run_len,
                            font: fonts.fonts[0].clone(),
                            color: glyph_color,
                            background_color: None,
                            underline: None,
                            strikethrough: None,
                        }],
                        None,
                    );
                    let _ = shaped.paint(gpui::Point { x, y }, line_height, window, cx);
                }
            }
            omaterm_terminal::CursorShape::Underline => {
                window.paint_quad(gpui::fill(
                    Bounds {
                        origin: gpui::Point {
                            x,
                            y: y + line_height - px(2.0),
                        },
                        size: gpui::Size {
                            width: cell_width,
                            height: px(2.0),
                        },
                    },
                    cursor_color,
                ));
            }
            omaterm_terminal::CursorShape::Bar => {
                window.paint_quad(gpui::fill(
                    Bounds {
                        origin: gpui::Point { x, y },
                        size: gpui::Size {
                            width: px(2.0),
                            height: line_height,
                        },
                    },
                    cursor_color,
                ));
            }
            omaterm_terminal::CursorShape::Hidden => {}
        }
    }

    let history = snapshot.history_size;
    if show_scrollbar && history > 0 {
        let track_x = origin.x + origin_bounds.size.width - px(8.0);
        let track_h: f32 = (snapshot.lines as f32) * f32::from(line_height);
        let total = (history + snapshot.lines as usize) as f32;
        let thumb_h = (track_h * snapshot.lines as f32 / total).max(12.0);
        let travel = (track_h - thumb_h).max(0.0);
        let frac = (snapshot.display_offset as f32 / history as f32).clamp(0.0, 1.0);
        let thumb_y = origin.y + px((1.0 - frac) * travel);
        let thumb_color: Hsla = rgb(0x52525B).into();
        window.paint_quad(gpui::fill(
            Bounds {
                origin: gpui::Point {
                    x: track_x,
                    y: thumb_y,
                },
                size: gpui::Size {
                    width: px(4.0),
                    height: px(thumb_h),
                },
            },
            thumb_color,
        ));
    }
}

fn main() {
    omaterm_logging::init_logging();
    Application::new()
        .with_assets(crate::ui::assets::OmaAssets)
        .run(|cx: &mut App| {
            cx.on_window_closed(|cx| {
                if cx.windows().is_empty() {
                    cx.quit();
                }
            })
            .detach();

            let bounds = Bounds::centered(None, size(px(960.0), px(640.0)), cx);
            cx.open_window(
                WindowOptions {
                    window_bounds: Some(WindowBounds::Windowed(bounds)),
                    ..Default::default()
                },
                |window, cx| {
                    window.set_window_title("OmaTerm");
                    let view = cx.new(|cx| WorkspaceView::new(window, cx));
                    let weak = view.downgrade();
                    let window_handle = window.window_handle();
                    window.on_window_should_close(cx, move |_, cx| {
                        let _ = weak.update(cx, |view, cx| view.begin_shutdown(window_handle, cx));
                        false
                    });
                    view
                },
            )
            .expect("failed to open OmaTerm window");

            cx.activate(true);
        });
}

#[cfg(test)]
mod tests {
    use super::{
        InspectorTab, PaletteFileIndexCache, PaletteSearchRequest, PaletteSearchWorker,
        project_jump_index, select_mono_family,
    };
    use std::sync::Arc;

    #[test]
    fn inspector_tab_defaults_to_info() {
        assert_eq!(InspectorTab::default(), InspectorTab::Info);
        assert_ne!(InspectorTab::Files, InspectorTab::Git);
    }

    #[test]
    fn jump_index_maps_digits_and_shifted_symbols_to_slots() {
        for (key, slot) in [
            ("1", 0),
            ("2", 1),
            ("3", 2),
            ("4", 3),
            ("5", 4),
            ("6", 5),
            ("7", 6),
            ("8", 7),
            ("9", 8),
            ("!", 0),
            ("@", 1),
            ("#", 2),
            ("$", 3),
            ("%", 4),
            ("^", 5),
            ("&", 6),
            ("*", 7),
            ("(", 8),
        ] {
            assert_eq!(project_jump_index(key), Some(slot), "key {key}");
        }
        for key in ["0", ")", "a", "p", "pageup", ""] {
            assert_eq!(project_jump_index(key), None, "key {key}");
        }
    }

    #[test]
    fn configured_font_family_wins_when_installed() {
        let installed = [
            "JetBrainsMono Nerd Font".to_string(),
            "monospace".to_string(),
        ];
        assert_eq!(
            select_mono_family(&installed, Some("monospace")),
            "monospace"
        );
        // Missing configured family falls back to the preference stack.
        assert_eq!(
            select_mono_family(&installed, Some("Absent Family")),
            "JetBrainsMono Nerd Font"
        );
        assert_eq!(
            select_mono_family(&installed, None),
            "JetBrainsMono Nerd Font"
        );
        assert_eq!(select_mono_family(&[], None), "monospace");
    }

    #[test]
    fn palette_file_index_cache_reuses_and_invalidates_root_snapshot() {
        let root = std::env::temp_dir().join(format!("omaterm-m16-index-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir_all(&root).unwrap();
        std::fs::write(root.join("alpha.rs"), b"alpha").unwrap();
        let cache = PaletteFileIndexCache::default();
        let project = omaterm_core::ProjectId::new();
        let cancelled = std::sync::atomic::AtomicBool::new(false);
        let first = cache
            .get_or_build(project, &root, false, &cancelled)
            .unwrap()
            .unwrap();
        assert!(first.contains(std::path::Path::new("alpha.rs")));
        assert!(Arc::ptr_eq(
            &first,
            &cache
                .get_or_build(project, &root, false, &cancelled)
                .unwrap()
                .unwrap()
        ));
        std::fs::write(root.join("beta.rs"), b"beta").unwrap();
        cache.invalidate();
        let second = cache
            .get_or_build(project, &root, false, &cancelled)
            .unwrap()
            .unwrap();
        assert!(!Arc::ptr_eq(&first, &second));
        assert!(second.contains(std::path::Path::new("beta.rs")));
        let _ = std::fs::remove_dir_all(root);
    }

    #[test]
    fn palette_search_worker_keeps_latest_request_and_bounds_pending_work() {
        let root = std::env::temp_dir().join(format!("omaterm-m16-worker-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir_all(&root).unwrap();
        std::fs::write(root.join("alpha.rs"), b"alpha").unwrap();
        std::fs::write(root.join("beta.rs"), b"beta").unwrap();
        let project = omaterm_core::ProjectId::new();
        let worker = PaletteSearchWorker::new(Arc::new(PaletteFileIndexCache::default()));
        let request = |generation, query: &str| PaletteSearchRequest {
            generation,
            project,
            query: query.into(),
            show_hidden: false,
            pinned: Some(root.clone()),
            active_cwd: None,
            watched_root: Some(root.clone()),
            cancelled: Arc::new(std::sync::atomic::AtomicBool::new(false)),
        };
        worker.submit(request(1, "alpha"));
        worker.submit(request(2, "beta"));
        let start = std::time::Instant::now();
        loop {
            if let Some(result) = worker.take_result()
                && result.generation == 2
            {
                assert_eq!(result.entries.len(), 1);
                assert_eq!(result.entries[0].detail, "beta.rs");
                break;
            }
            assert!(start.elapsed() < std::time::Duration::from_secs(3));
            std::thread::sleep(std::time::Duration::from_millis(10));
        }
        worker.shutdown();
        let _ = std::fs::remove_dir_all(root);
    }
}
