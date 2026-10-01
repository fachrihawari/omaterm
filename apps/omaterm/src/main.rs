use std::cell::Cell;
use std::collections::HashMap;
use std::rc::Rc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use gpui::{
    App, Application, AsyncApp, Bounds, ClipboardItem, Context, Div, ExternalPaths, FocusHandle,
    Font, FontFallbacks, HighlightStyle, Hsla, KeyDownEvent, ModifiersChangedEvent, MouseButton,
    MouseDownEvent, MouseMoveEvent, MouseUpEvent, PathPromptOptions, Pixels, ScrollDelta,
    ScrollWheelEvent, SharedString, StyledText, TextRun, Timer, WeakEntity, Window, WindowBounds,
    WindowOptions, canvas, div, font, hsla, prelude::*, px, relative, rgb, rgba, size,
};
use omaterm_core::{
    CommandContext, CommandOutput, CommandResult, FileCommand, FileEntry, GitCommand, OmaCommand,
    Pane, PaneCommand, PaneContent, PaneId, PaneNode, ProjectCommand, ProjectId, SessionId,
    SplitAxis, SplitDirection, TabCommand, TerminalCommand,
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
mod files;
mod git_panel;
mod history;
mod ipc_bridge;
mod router;

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
    /// M13 file panel: right-sidebar tree rows for the selected project.
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
    /// Wheel scroll offset into the tree rows (row-granular, clamped every
    /// render). Reset on project switch.
    files_scroll_rows: usize,
    /// Horizontal tree offset in pixels (Shift+wheel or bar drag, clamped
    /// to `MAX_SCROLL_COLS_PX`). Zero renders the classic ellipsis path.
    files_scroll_cols: f32,
    /// Thumb drag in flight: (last pointer position, sub-row accumulator).
    /// Cleared on release, pane clicks, and tab switches (no stuck drags).
    files_vdrag: Option<(f32, f32)>,
    files_hdrag: Option<(f32, f32)>,
    files_last_resolve: Instant,
    files_poller_active: bool,
    /// `Ctrl+P` fuzzy finder overlay state.
    ctrlp_open: bool,
    ctrlp_query: String,
    ctrlp_results: Vec<FileEntry>,
    ctrlp_selected: usize,
    ctrlp_truncated: bool,
    ctrlp_generation: u64,
    ctrlp_rx: Option<std::sync::mpsc::Receiver<(u64, Vec<FileEntry>, bool)>>,
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
    /// Right-sidebar tab: the Files tree or the Git status panel (M14
    /// follow-up; git no longer stacks under files). View-local, never
    /// persisted.
    sidebar_tab: SidebarTab,
    /// M15 unified-diff panel: last-good diffs per project+side, explicit
    /// empty/error states, file/hunk selection, staged toggle (GPUI-free).
    diff_panel: diff_panel::DiffPanel,
    /// Background diff-refresh completions `(generation, project, staged,
    /// outcome)`. Root resolution and `git diff` both run on the worker
    /// (never the UI thread); stale generations drop on project switch.
    diff_tx: std::sync::mpsc::Sender<(u64, ProjectId, bool, diff_panel::DiffRefresh)>,
    diff_rx: std::sync::mpsc::Receiver<(u64, ProjectId, bool, diff_panel::DiffRefresh)>,
    diff_generation: u64,
    /// Project+side with a refresh in flight, if any (one diff call at a
    /// time; the rest wait for the next poller tick).
    diff_in_flight: Option<(ProjectId, bool)>,
    /// Last landed refresh per project+side (interval source).
    diff_refreshed_at: HashMap<(ProjectId, bool), Instant>,
    /// Set by manual refresh, staged-toggle, git mutations, and
    /// post-`terminal.run` submissions: the next tick refreshes immediately.
    diff_dirty_hint: bool,
    /// Last project the diff poller served (switch detection).
    diff_last_project: Option<ProjectId>,
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

/// Right-sidebar tab (M14 follow-up): the file tree or the Source
/// Control panel. Defaults to Files every launch; intentionally not
/// persisted (view chrome, not workspace state — no schema churn).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
enum SidebarTab {
    #[default]
    Files,
    Git,
}

/// Arm window for two-step history controls.
const HISTORY_ARM_WINDOW: Duration = Duration::from_secs(8);

/// Arm window for two-step risky-paste confirmation.
const PASTE_ARM_WINDOW: Duration = Duration::from_secs(8);

struct IpcWork {
    request: IpcRequest,
    reply: std::sync::mpsc::SyncSender<IpcResponse>,
    deadline: Instant,
    cancelled: Arc<AtomicBool>,
}

enum PendingUiLaunch {
    Project(Option<std::path::PathBuf>),
    Tab(omaterm_core::ProjectId),
    Restore {
        project: omaterm_core::ProjectId,
        tab: omaterm_core::TabId,
        pane: PaneId,
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
        // Background diff channel (M15): same contract for `git diff`.
        let (diff_tx, diff_rx) = std::sync::mpsc::channel();
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
            ipc_server: None,
            ipc_receiver: None,
            ipc_pending: HashMap::new(),
            shutting_down: false,
            history_arm: None,
            paste_arm: None,
            input_notice: None,
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
            files_scroll_rows: 0,
            files_scroll_cols: 0.0,
            files_vdrag: None,
            files_hdrag: None,
            files_last_resolve: Instant::now()
                .checked_sub(Duration::from_secs(60))
                .unwrap_or_else(Instant::now),
            files_poller_active: false,
            ctrlp_open: false,
            ctrlp_query: String::new(),
            ctrlp_results: Vec::new(),
            ctrlp_selected: 0,
            ctrlp_truncated: false,
            ctrlp_generation: 0,
            ctrlp_rx: None,
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
            sidebar_tab: SidebarTab::Files,
            diff_panel: diff_panel::DiffPanel::default(),
            diff_tx,
            diff_rx,
            diff_generation: 0,
            diff_in_flight: None,
            diff_refreshed_at: HashMap::new(),
            diff_dirty_hint: true,
            diff_last_project: None,
        };
        view.start_ipc(cx);
        view.restore_or_initialize(cx);
        view.warm_history_journals();
        view.start_history_timer(cx);
        view.start_files_poller(cx);
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
        WorkspaceSnapshot::capture_with_expanded(
            self.coordinator.window(),
            &self.current_cwds(),
            &self.files_panel.expanded_snapshot(),
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
        self.shutting_down = true;
        if let Some(receiver) = self.ipc_receiver.take() {
            receiver.close();
        }
        for (id, work) in self.ipc_pending.drain() {
            self.coordinator.cancel_launch(id);
            let _ = work.reply.send(IpcResponse::failure(
                work.request.request_id,
                "timeout",
                "desktop is shutting down",
            ));
        }
        let ipc_server = self.ipc_server.take();
        self.coordinator.cancel_launches();
        self.pending_ui_launches.clear();
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
            }
        }
        for (operation_id, outcome) in self.coordinator.poll_launches() {
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
            match (&outcome.result, ui) {
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
        }
        let pending = self.coordinator.has_pending_launches();
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

    // ---- M13 file panel (right-sidebar tree + Ctrl+P finder) ----
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
        self.git_tick(cx);
        self.diff_tick(cx);
        // Latest `Ctrl+P` result wins; stale generations are dropped.
        if let Some(rx) = self.ctrlp_rx.as_ref() {
            let mut latest = None;
            while let Ok(result) = rx.try_recv() {
                latest = Some(result);
            }
            if let Some((generation, entries, truncated)) = latest
                && generation == self.ctrlp_generation
                && self.ctrlp_open
            {
                self.ctrlp_results = entries;
                self.ctrlp_selected = 0;
                self.ctrlp_truncated = truncated;
                cx.notify();
            }
        }
        let project = self.coordinator.selected_project_id();
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
            // New generation retires in-flight fetches; other projects'
            // caches drop so memory stays bounded by one project.
            self.files_generation = self.files_generation.wrapping_add(1);
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
            if changed {
                self.files_generation = self.files_generation.wrapping_add(1);
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
    fn git_tick(&mut self, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
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
            return;
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
            return;
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
            return;
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
    }

    /// M15 diff refresh on the same 250ms poller: drains landed workers
    /// and spawns at most one fetch per tick for the visible side
    /// (unstaged/staged toggle). Stale generations drop on project
    /// switch; other projects' caches clear to bound memory.
    fn diff_tick(&mut self, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
        }
        let mut landed = false;
        while let Ok((generation, project, staged, refresh)) = self.diff_rx.try_recv() {
            if generation != self.diff_generation {
                continue;
            }
            // Landed work ran off this thread by construction; pin it.
            debug_assert_ne!(refresh.worker, std::thread::current().id());
            if self.diff_in_flight == Some((project, staged)) {
                self.diff_in_flight = None;
            }
            self.diff_panel.apply_refresh(project, staged, refresh);
            self.diff_refreshed_at
                .insert((project, staged), Instant::now());
            landed = true;
        }
        if landed {
            cx.notify();
        }
        let Some(project) = self.coordinator.selected_project_id() else {
            return;
        };
        if self.diff_last_project != Some(project) {
            self.diff_last_project = Some(project);
            self.diff_generation = self.diff_generation.wrapping_add(1);
            self.diff_in_flight = None;
            for other in self
                .coordinator
                .projects()
                .iter()
                .map(|candidate| candidate.id)
                .collect::<Vec<_>>()
            {
                if other != project {
                    self.diff_panel.clear_project(other);
                    self.diff_refreshed_at.remove(&(other, false));
                    self.diff_refreshed_at.remove(&(other, true));
                }
            }
        }
        if self.diff_in_flight.is_some() {
            return;
        }
        let staged = self.diff_panel.show_staged(project);
        let config = omaterm_state::AppConfig::load().unwrap_or_default();
        let interval = Duration::from_secs(config.resolved_git_refresh_secs().clamp(1, 300));
        let known = self.diff_panel.diff_for(project, staged).is_some()
            || self.diff_panel.empty_for(project, staged).is_some();
        if !git_panel::should_refresh(
            known,
            self.diff_dirty_hint,
            self.diff_refreshed_at.get(&(project, staged)).copied(),
            interval,
            Instant::now(),
        ) {
            return;
        }
        let pinned = self.coordinator.pinned_for(project);
        let active_cwd = self.coordinator.shell_cwd_for(project);
        let tx = self.diff_tx.clone();
        diff_panel::spawn_diff_thread(
            std::thread::current().id(),
            diff_panel::DiffSpawn {
                project,
                staged,
                generation: self.diff_generation,
                pinned,
                active_cwd,
                context_lines: 3,
                tx,
            },
        );
        self.diff_in_flight = Some((project, staged));
        self.diff_dirty_hint = false;
    }

    /// Stage one file from a diff hunk button through the dispatcher
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

    /// Copy a diff file's absolute path, Bourne shell-escaped (§47
    /// policy reuse), to the clipboard.
    fn diff_copy_path(
        &mut self,
        project: ProjectId,
        relative: &std::path::Path,
        cx: &mut Context<Self>,
    ) {
        if self.shutting_down {
            return;
        }
        self.copy_path(project, relative, cx);
    }

    /// Jump to a diff file through `FileCommand::Open` (terminal-routed,
    /// v0.3-editor-compatible). Failures surface as notices, never silent.
    fn diff_open_file(
        &mut self,
        project: ProjectId,
        relative: std::path::PathBuf,
        cx: &mut Context<Self>,
    ) {
        if self.shutting_down {
            return;
        }
        match self.dispatch_command(
            OmaCommand::File(FileCommand::Open {
                project,
                path: relative,
            }),
            cx,
        ) {
            Ok(_) => {}
            Err(error) => {
                self.input_notice = Some(format!("Open: {error}"));
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
        match self.dispatch_command(OmaCommand::Git(GitCommand::Stage { project, paths }), cx) {
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
        match self.dispatch_command(OmaCommand::Git(GitCommand::Unstage { project, paths }), cx) {
            Ok(_) => {
                self.git_dirty_hint = true;
                self.diff_dirty_hint = true;
                cx.notify();
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
            }
            Err(error) => {
                self.input_notice = Some(format!("Discard: {error}"));
                cx.notify();
            }
        }
    }

    /// Select a changed path and show its M15 diff in the Git panel. The
    /// clicked row's group selects the staged or unstaged diff side.
    fn git_select_path(&mut self, project: ProjectId, path: std::path::PathBuf, staged: bool) {
        self.git_panel.select(project, path.clone());
        self.diff_panel.select_file(project, path);
        self.diff_panel.set_show_staged(project, staged);
        self.diff_dirty_hint = true;
        tracing::debug!(
            target: "omaterm::git",
            project_id = %project.0,
            "git change selected (diff shown in Git panel)",
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
                self.input_notice = Some(format!("Committed {oid}."));
                cx.notify();
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

    fn toggle_ctrlp(&mut self, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
        }
        self.ctrlp_open = !self.ctrlp_open;
        if self.ctrlp_open {
            self.ctrlp_query.clear();
            self.ctrlp_results.clear();
            self.ctrlp_selected = 0;
            self.ctrlp_truncated = false;
            self.ctrlp_caret_on = true;
            self.ensure_ctrlp_blink(cx);
            self.ctrlp_search(cx);
        } else {
            self.ctrlp_rx = None;
        }
        cx.notify();
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
    fn ctrlp_search(&mut self, cx: &mut Context<Self>) {
        let Some(project) = self.coordinator.selected_project_id() else {
            self.ctrlp_results.clear();
            return;
        };
        let query = self.ctrlp_query.clone();
        if query.is_empty() {
            self.ctrlp_results.clear();
            self.ctrlp_selected = 0;
            self.ctrlp_truncated = false;
            return;
        }
        let root = self.files_project_root(project, cx);
        let config = omaterm_state::AppConfig::load().unwrap_or_default();
        let show_hidden = config.show_hidden();
        self.ctrlp_generation = self.ctrlp_generation.wrapping_add(1);
        let generation = self.ctrlp_generation;
        let (tx, rx) = std::sync::mpsc::channel();
        self.ctrlp_rx = Some(rx);
        std::thread::spawn(move || {
            let (entries, truncated) = match root {
                Some(root) => omaterm_context::search_files(&root, &query, 100, show_hidden)
                    .map(|list| (list.entries, list.truncated))
                    .unwrap_or_default(),
                None => (Vec::new(), false),
            };
            let _ = tx.send((generation, entries, truncated));
        });
    }

    fn ctrlp_confirm(&mut self, cx: &mut Context<Self>) {
        let Some(entry) = self.ctrlp_results.get(self.ctrlp_selected).cloned() else {
            return;
        };
        let Some(project) = self.coordinator.selected_project_id() else {
            return;
        };
        self.files_panel.select(project, entry.path.clone());
        self.ctrlp_open = false;
        self.ctrlp_rx = None;
        self.open_file_path(project, entry.path, cx);
        cx.notify();
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
    fn on_ctrlp_key(&mut self, event: &KeyDownEvent, cx: &mut Context<Self>) {
        // Any keystroke restores caret visibility (standard blink-phase
        // reset) and keeps the blink task alive while open.
        self.ctrlp_caret_on = true;
        self.ensure_ctrlp_blink(cx);
        let key_name = event.keystroke.key.to_lowercase().replace('_', "");
        // Keyboard-only copy/reveal of the highlighted result (mirrors the
        // tree's Ctrl+Shift+Y/U); the finder stays open for further picks.
        if event.keystroke.modifiers.control
            && !event.keystroke.modifiers.shift
            && !event.keystroke.modifiers.alt
            && let Some(entry) = self.ctrlp_results.get(self.ctrlp_selected).cloned()
            && let Some(project) = self.coordinator.selected_project_id()
        {
            if key_name == "y" {
                self.files_panel.select(project, entry.path.clone());
                self.copy_path(project, &entry.path, cx);
                return;
            }
            if key_name == "u" {
                self.files_panel.select(project, entry.path.clone());
                self.reveal_path(project, &entry.path, cx);
                return;
            }
        }
        match key_name.as_str() {
            "escape" => {
                self.ctrlp_open = false;
                self.ctrlp_rx = None;
                cx.notify();
            }
            "enter" | "return" | "kpenter" => self.ctrlp_confirm(cx),
            "backspace" => {
                self.ctrlp_query.pop();
                self.ctrlp_search(cx);
                cx.notify();
            }
            "up" => {
                if self.ctrlp_selected > 0 {
                    self.ctrlp_selected -= 1;
                    cx.notify();
                }
            }
            "down" => {
                if self.ctrlp_selected + 1 < self.ctrlp_results.len() {
                    self.ctrlp_selected += 1;
                    cx.notify();
                }
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
                if let Some(ch) = ch
                    && !ch.is_control()
                    && self.ctrlp_query.len() < 256
                {
                    self.ctrlp_query.push(ch);
                    self.ctrlp_search(cx);
                    cx.notify();
                }
            }
        }
    }

    fn on_key_down(&mut self, event: &KeyDownEvent, _: &mut Window, cx: &mut Context<Self>) {
        if self.shutting_down {
            return;
        }
        let key_name = event.keystroke.key.to_lowercase().replace('_', "");

        if event.keystroke.modifiers.control && event.keystroke.modifiers.shift && key_name == "p" {
            self.create_project(cx);
            return;
        }
        // M13 `Ctrl+P` file finder (plain Ctrl+P is free: Ctrl+Shift+P
        // creates projects). While open, the overlay owns the keyboard.
        if self.ctrlp_open {
            return self.on_ctrlp_key(event, cx);
        }
        // M14 commit input: while focused (Git tab), plain keys type the
        // message; Ctrl/Alt combinations fall through to global shortcuts
        // so they keep working while typing.
        if self.git_panel.commit_focused()
            && self.sidebar_tab == SidebarTab::Git
            && !event.keystroke.modifiers.control
            && !event.keystroke.modifiers.alt
        {
            return self.on_commit_key(event, cx);
        }
        if event.keystroke.modifiers.control
            && !event.keystroke.modifiers.shift
            && !event.keystroke.modifiers.alt
            && key_name == "p"
        {
            self.toggle_ctrlp(cx);
            return;
        }
        // M15 hunk navigation: Alt+N next / Alt+P previous within the
        // selected diff in Git. Plain Alt+letter is otherwise free
        // (Alt only pairs with PageUp/PageDown for tab/project jumps).
        if event.keystroke.modifiers.alt
            && !event.keystroke.modifiers.control
            && !event.keystroke.modifiers.shift
            && self.sidebar_tab == SidebarTab::Git
            && let Some(project) = self.coordinator.selected_project_id()
        {
            if key_name == "n" {
                self.diff_panel.next_hunk(project);
                cx.notify();
                return;
            }
            if key_name == "p" {
                self.diff_panel.prev_hunk(project);
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
            self.paste(cx);
            return;
        }

        // Explicit clipboard copy of the drag selection: Ctrl+Shift+C.
        if event.keystroke.modifiers.control && event.keystroke.modifiers.shift && key_name == "c" {
            self.copy_selection(cx);
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
                    SplitAxis::Horizontal => {
                        div().w(relative(fraction)).h_full().flex().child(first)
                    }
                    SplitAxis::Vertical => div().h(relative(fraction)).w_full().flex().child(first),
                };
                let second = match axis {
                    SplitAxis::Horizontal => div()
                        .w(relative(1.0 - fraction))
                        .h_full()
                        .flex()
                        .child(second),
                    SplitAxis::Vertical => div()
                        .h(relative(1.0 - fraction))
                        .w_full()
                        .flex()
                        .child(second),
                };
                let container = div().flex().flex_1().size_full();
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

        let border = if focused { 0xA1A1AA } else { 0x27272A };
        div()
            .flex()
            .flex_1()
            .flex_col()
            .size_full()
            .bg(rgb(0x18181B))
            .border_1()
            .border_color(rgb(border))
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
            )
            .child(div().flex_1().size_full().child(canvas(
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
            )))
            .into_any_element()
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
        // The pane viewport starts after the fixed sidebars and tab strip.
        let window_width: f32 = (viewport.width - px(180.0) - px(files::RIGHT_SIDEBAR_WIDTH_PX))
            .max(px(1.0))
            .into();
        let window_height: f32 = (viewport.height - px(36.0)).max(px(1.0)).into();
        for pane_rect in self.coordinator.tree().pane_rects() {
            let Some(session_id) = self.coordinator.session_id_for_pane(pane_rect.pane) else {
                continue;
            };
            let cols =
                ((window_width * pane_rect.rect.width / cell_width).floor() as u16).clamp(2, 500);
            let rows = ((window_height * pane_rect.rect.height / line_height).floor() as u16)
                .clamp(1, 500);
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
                    .on_mouse_move(cx.listener(|view, event: &MouseMoveEvent, _, cx| {
                        let Some((last_y, mut acc)) = view.files_vdrag else {
                            return;
                        };
                        if view.shutting_down {
                            view.files_vdrag = None;
                            return;
                        }
                        let row_height =
                            f32::from(view.fonts(&*cx).line_height).max(1.0) + Self::FILES_ROW_VPAD;
                        let y = f32::from(event.position.y);
                        acc += (y - last_y) / row_height;
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

    /// Horizontal tree scrollbar: position nub over an estimated-width
    /// track (content width is unmeasured; the estimate is documented at
    /// the constant). Shift+wheel, native x deltas, or press-and-slide.
    fn render_tree_hscrollbar(&mut self, cx: &mut Context<Self>) -> Div {
        /// Track width estimate: sidebar minus padding, row gutters, and
        /// the vertical rail. A few px off either way is invisible on an
        /// 8px chrome element.
        const TRACK_W: f32 = files::RIGHT_SIDEBAR_WIDTH_PX - 16.0 - 12.0 - 12.0;
        const THUMB_W: f32 = 48.0;
        let pos = (self.files_scroll_cols / files::MAX_SCROLL_COLS_PX).clamp(0.0, 1.0);
        let left_px = pos * (TRACK_W - THUMB_W);
        div()
            .h(px(10.0))
            .flex()
            .flex_row()
            .items_center()
            .px_2()
            .child(
                div()
                    .w(px(TRACK_W))
                    .h(px(6.0))
                    .rounded_full()
                    .bg(rgb(0x1F1F23))
                    .flex()
                    .flex_row()
                    .items_center()
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(|view, event: &MouseDownEvent, _, cx| {
                            if view.shutting_down {
                                return;
                            }
                            cx.stop_propagation();
                            view.files_hdrag = Some((f32::from(event.position.x), 0.0));
                            cx.notify();
                        }),
                    )
                    .on_mouse_move(cx.listener(|view, event: &MouseMoveEvent, _, cx| {
                        let Some((last_x, mut acc)) = view.files_hdrag else {
                            return;
                        };
                        if view.shutting_down {
                            view.files_hdrag = None;
                            return;
                        }
                        let x = f32::from(event.position.x);
                        acc += x - last_x;
                        let step = acc.trunc();
                        acc -= step;
                        view.files_scroll_cols =
                            (view.files_scroll_cols + step).clamp(0.0, files::MAX_SCROLL_COLS_PX);
                        view.files_hdrag = Some((x, acc));
                        cx.notify();
                    }))
                    .on_mouse_up(
                        MouseButton::Left,
                        cx.listener(|view, _, _, cx| {
                            view.files_hdrag = None;
                            cx.notify();
                        }),
                    )
                    .child(div().w(px(left_px)))
                    .child(
                        div()
                            .w(px(THUMB_W))
                            .h_full()
                            .rounded_full()
                            .bg(rgb(0x52525B)),
                    )
                    .child(div().flex_1()),
            )
    }

    /// Right-sidebar file tree for the selected project. Pure render from
    /// the panel row cache (no filesystem or dispatcher work per frame);
    /// clicks select/toggle through the dispatcher-owned refresh.
    /// Vertical padding both sides of one tree row (`py_1` at the 16px
    /// tailwind base). Added to the font line height for scroll math; a
    /// small mismatch only costs a partially-cut last row, never input.
    const FILES_ROW_VPAD: f32 = 8.0;

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
        // Header, footers, and hints stay fixed; only rows move.
        let row_height = f32::from(self.fonts(&*cx).line_height).max(1.0) + Self::FILES_ROW_VPAD;
        let visible = ((viewport_height / row_height) as usize).clamp(1, files::MAX_RENDER_ROWS);
        let all_rows = self.files_panel.rows_for(project).unwrap_or_default();
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
            bar = bar.child(
                div()
                    .px_2()
                    .py_1()
                    .text_color(rgb(0x71717A))
                    .child("Empty directory"),
            );
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
        let h_offset = self.files_scroll_cols;
        let rows_shown = rows.len();
        let rows_total = all_rows.len();
        for row in rows {
            let is_selected = selected.as_ref() == Some(&row.path);
            let is_dir = row.kind == omaterm_core::FileKind::Directory;
            let name = row
                .path
                .file_name()
                .map(|name| name.to_string_lossy().into_owned())
                .unwrap_or_else(|| row.path.to_string_lossy().into_owned());
            // Directories carry no chevron: the folder glyph alone shows
            // state (closed = collapsed, open = expanded, dimmed + `…` =
            // still loading), so a second leading marker is redundant.
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
            // Horizontal scroll: at rest the classic ellipsis path renders
            // (byte-identical to before); once shifted, the full relative
            // path lays out nowrap inside an overflow-hidden viewport and
            // slides under a negative margin.
            let full_path = row.path.to_string_lossy().into_owned();
            let label_view = if h_offset <= 0.0 {
                div().flex_1().min_w(px(0.0)).truncate().child(label)
            } else {
                div().flex_1().min_w(px(0.0)).overflow_hidden().child(
                    div()
                        .flex()
                        .flex_row()
                        .items_center()
                        .flex_shrink_0()
                        .whitespace_nowrap()
                        .ml(px(-h_offset))
                        .child(full_path),
                )
            };
            rows_col = rows_col.child(
                div()
                    .flex()
                    .flex_row()
                    .items_center()
                    .gap_2()
                    .px_2()
                    .py_1()
                    .pl(px(8.0 + row.depth as f32 * 16.0))
                    .rounded_sm()
                    .bg(rgb(if is_selected { 0x27272A } else { 0x111113 }))
                    .text_color(rgb(if is_selected {
                        0xFAFAFA
                    } else if dimmed {
                        0x52525B
                    } else {
                        0xA1A1AA
                    }))
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(move |view, _, window, cx| {
                            if view.shutting_down {
                                return;
                            }
                            window.focus(&view.focus_handle);
                            view.files_vdrag = None;
                            view.files_hdrag = None;
                            if is_dir {
                                view.toggle_file_row(project, path.clone(), true, cx);
                            } else {
                                view.files_panel.select(project, path.clone());
                                view.open_file_path(project, path.clone(), cx);
                                view.refresh_files(cx);
                            }
                        }),
                    )
                    .child(
                        div()
                            .w(px(18.0))
                            .flex()
                            .flex_shrink_0()
                            .items_center()
                            .justify_center()
                            .text_color(rgb(icon_color))
                            .child(icon.glyph.to_string()),
                    )
                    // Long names ellipsize inside the fixed sidebar instead
                    // of stretching the row and breaking column alignment.
                    .child(label_view),
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
        // Mouse-friendly actions for the selected file row.
        if let Some(path) = selected
            && let Some(row) = self
                .files_panel
                .rows_for(project)
                .unwrap_or_default()
                .iter()
                .find(|row| row.path == path)
            && row.kind != omaterm_core::FileKind::Directory
        {
            let open_path = path.clone();
            let bar_with_actions = bar.child(
                div()
                    .flex()
                    .flex_row()
                    .gap_2()
                    .px_2()
                    .py_1()
                    .text_color(rgb(0x71717A))
                    .child(
                        div()
                            .px_1()
                            .on_mouse_down(
                                MouseButton::Left,
                                cx.listener(move |view, _, window, cx| {
                                    if view.shutting_down {
                                        return;
                                    }
                                    cx.stop_propagation();
                                    window.focus(&view.focus_handle);
                                    view.open_file_path(project, open_path.clone(), cx);
                                }),
                            )
                            .child("open"),
                    )
                    .child(
                        div()
                            .px_1()
                            .on_mouse_down(
                                MouseButton::Left,
                                cx.listener(|view, _, window, cx| {
                                    if view.shutting_down {
                                        return;
                                    }
                                    cx.stop_propagation();
                                    window.focus(&view.focus_handle);
                                    view.copy_selected_path(cx);
                                }),
                            )
                            .child("copy"),
                    )
                    .child(
                        div()
                            .px_1()
                            .on_mouse_down(
                                MouseButton::Left,
                                cx.listener(|view, _, window, cx| {
                                    if view.shutting_down {
                                        return;
                                    }
                                    cx.stop_propagation();
                                    window.focus(&view.focus_handle);
                                    view.reveal_selected_in_terminal(cx);
                                }),
                            )
                            .child("reveal"),
                    ),
            );
            bar = bar_with_actions.child(
                div()
                    .px_2()
                    .text_color(rgb(0x52525B))
                    .child("Ctrl+P find · ^⇧Y copy · ^⇧U reveal"),
            );
        } else {
            bar = bar.child(
                div()
                    .px_2()
                    .text_color(rgb(0x52525B))
                    .child("Ctrl+P find · ^⇧Y copy · ^⇧U reveal"),
            );
        }
        bar = bar.child(self.render_tree_hscrollbar(cx));
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
        // Discard arm banner (two-step confirm).
        if let Some(text) = self.git_panel.armed_text(project) {
            bar = bar.child(
                div()
                    .px_2()
                    .py_1()
                    .bg(rgb(0x3F321D))
                    .text_color(rgb(0xFDE68A))
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
        // Branch header with ahead/behind badge.
        let mut badge = String::new();
        if status.ahead > 0 {
            badge.push_str(&format!(" ↑{}", status.ahead));
        }
        if status.behind > 0 {
            badge.push_str(&format!(" ↓{}", status.behind));
        }
        let branch_line = match (&status.branch, &status.upstream) {
            (Some(branch), Some(upstream)) => format!("{branch}{badge} ({upstream})"),
            (Some(branch), None) => format!("{branch}{badge}"),
            (None, _) => format!("detached{badge}"),
        };
        // Branch header with the refresh action icon at the far end.
        bar = bar.child(
            div()
                .flex()
                .flex_row()
                .items_center()
                .justify_between()
                .px_2()
                .py_1()
                .text_color(rgb(0xFAFAFA))
                .child(div().child(branch_line))
                .child(
                    div()
                        .px_1()
                        .text_color(rgb(0x71717A))
                        .on_mouse_down(
                            MouseButton::Left,
                            cx.listener(move |view, _, window, cx| {
                                if view.shutting_down {
                                    return;
                                }
                                cx.stop_propagation();
                                window.focus(&view.focus_handle);
                                view.git_dirty_hint = true;
                                cx.notify();
                            }),
                        )
                        .child(git_panel::REFRESH_ICON.to_string()),
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
        // Commit row: single-line message input plus a check button. The
        // button enables only with staged changes; authorship comes from
        // the repo config (no author UI).
        let can_commit = !status.staged.is_empty();
        let input_focused = self.git_panel.commit_focused();
        let draft = self.git_panel.commit_draft(project).to_owned();
        let caret_h = px(f32::from(self.fonts(&*cx).line_height).max(1.0));
        let input_content = if draft.is_empty() && !input_focused {
            div().text_color(rgb(0x52525B)).child("Commit message…")
        } else {
            div()
                .flex()
                .flex_row()
                .items_center()
                .child(div().child(draft))
                .child(div().w(px(2.0)).h(caret_h).bg(rgb(if input_focused {
                    0xFAFAFA
                } else {
                    0x52525B
                })))
        };
        bar = bar.child(
            div()
                .flex()
                .flex_row()
                .items_center()
                .gap_2()
                .px_2()
                .py_1()
                .child(
                    div()
                        .flex_1()
                        .min_w(px(0.0))
                        .px_2()
                        .py_1()
                        .rounded_sm()
                        .border_1()
                        .border_color(rgb(if input_focused { 0x4C9AFF } else { 0x27272A }))
                        .bg(rgb(0x18181B))
                        .text_color(rgb(0xE4E4E7))
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
                        .child(
                            div()
                                .flex_1()
                                .min_w(px(0.0))
                                .overflow_hidden()
                                .child(input_content),
                        ),
                )
                .child(
                    div()
                        .px_1()
                        .text_color(rgb(if can_commit { 0x89E051 } else { 0x52525B }))
                        .on_mouse_down(
                            MouseButton::Left,
                            cx.listener(move |view, _, window, cx| {
                                if view.shutting_down {
                                    return;
                                }
                                cx.stop_propagation();
                                window.focus(&view.focus_handle);
                                view.git_commit_submit(project, cx);
                            }),
                        )
                        .child(git_panel::COMMIT_ICON.to_string()),
                ),
        );
        let selected = self
            .git_panel
            .selected_path(project)
            .map(|path| path.to_path_buf());
        // Flatten groups in order with a render cap; the footer names it.
        let mut rows: Vec<(git_panel::GitGroup, &omaterm_core::GitEntry)> = Vec::new();
        for entry in &status.staged {
            rows.push((git_panel::GitGroup::Staged, entry));
        }
        for entry in &status.unstaged {
            rows.push((git_panel::GitGroup::Unstaged, entry));
        }
        for entry in &status.untracked {
            rows.push((git_panel::GitGroup::Untracked, entry));
        }
        let total = rows.len();
        let capped = total > git_panel::MAX_GIT_RENDER_ROWS;
        let mut last_group: Option<git_panel::GitGroup> = None;
        for (group, entry) in rows.into_iter().take(git_panel::MAX_GIT_RENDER_ROWS) {
            if last_group != Some(group) {
                last_group = Some(group);
                let (title, count) = match group {
                    git_panel::GitGroup::Staged => ("Staged", status.staged.len()),
                    git_panel::GitGroup::Unstaged => ("Unstaged", status.unstaged.len()),
                    git_panel::GitGroup::Untracked => ("Untracked", status.untracked.len()),
                };
                bar = bar.child(
                    div()
                        .px_2()
                        .py_1()
                        .text_color(rgb(0xA1A1AA))
                        .child(format!("{title} ({count})")),
                );
            }
            let is_selected = selected.as_ref() == Some(&entry.path);
            let path = entry.path.clone();
            let label = match &entry.renamed_from {
                Some(from) => format!(
                    "{} → {}",
                    from.to_string_lossy(),
                    entry.path.to_string_lossy()
                ),
                None => entry.path.to_string_lossy().into_owned(),
            };
            let mut row = div()
                .flex()
                .flex_row()
                .items_center()
                .gap_2()
                .px_2()
                .py_1()
                .rounded_sm()
                .bg(rgb(if is_selected { 0x27272A } else { 0x111113 }))
                .text_color(rgb(if is_selected { 0xFAFAFA } else { 0xA1A1AA }))
                .on_mouse_down(
                    MouseButton::Left,
                    cx.listener(move |view, _, window, cx| {
                        if view.shutting_down {
                            return;
                        }
                        window.focus(&view.focus_handle);
                        view.git_select_path(
                            project,
                            path.clone(),
                            group == git_panel::GitGroup::Staged,
                        );
                        cx.notify();
                    }),
                )
                .child(div().flex_1().min_w(px(0.0)).truncate().child(label));
            // Per-group action icons through the dispatcher (VSCode-style:
            // plus/minus/trash with decorator hues). The path clones
            // before the listener so the handler owns 'static data.
            let action = |kind: git_panel::GitAction, path: std::path::PathBuf| {
                let glyph = kind.icon();
                div()
                    .px_1()
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(move |view, _, window, cx| {
                            if view.shutting_down {
                                return;
                            }
                            cx.stop_propagation();
                            window.focus(&view.focus_handle);
                            match kind {
                                git_panel::GitAction::Stage => {
                                    view.git_stage_paths(project, vec![path.clone()], cx)
                                }
                                git_panel::GitAction::Unstage => {
                                    view.git_unstage_paths(project, vec![path.clone()], cx)
                                }
                                git_panel::GitAction::Discard => {
                                    view.git_discard_path(project, path.clone(), cx)
                                }
                            }
                        }),
                    )
                    .child(glyph.to_string())
            };
            use git_panel::GitAction as Act;
            match group {
                git_panel::GitGroup::Staged => {
                    row = row
                        .child(action(Act::Unstage, entry.path.clone()))
                        .child(action(Act::Discard, entry.path.clone()));
                }
                git_panel::GitGroup::Unstaged | git_panel::GitGroup::Untracked => {
                    row = row
                        .child(action(Act::Stage, entry.path.clone()))
                        .child(action(Act::Discard, entry.path.clone()));
                }
            }
            bar = bar.child(row);
        }
        if capped || status.truncated {
            bar = bar.child(div().px_2().py_1().text_color(rgb(0x71717A)).child(
                if status.truncated {
                    format!("(truncated: showing first {total} bounded entries)")
                } else {
                    format!(
                        "(showing first {} of {total})",
                        git_panel::MAX_GIT_RENDER_ROWS
                    )
                },
            ));
        }
        // Legend: the icons are conventional, the line guarantees nobody
        // has to guess. Rendered from the same glyph constants as the
        // actions so they can never drift apart.
        bar = bar.child(div().px_2().py_1().text_color(rgb(0x52525B)).child(format!(
            "{} stage · {} unstage · {} discard",
            git_panel::STAGE_ICON,
            git_panel::UNSTAGE_ICON,
            git_panel::DISCARD_ICON,
        )));
        self.render_selected_diff(bar, project, cx)
    }

    /// Show the selected change's unified diff beneath Git's change list.
    /// The selected path comes from the clicked M14 Git row; no second file
    /// list or separate Diff tab exists. Refreshes land via `diff_tick`.
    /// Stage uses the shared GitCommand path.
    fn render_selected_diff(
        &mut self,
        bar: Div,
        project: ProjectId,
        cx: &mut Context<Self>,
    ) -> Div {
        let staged = self.diff_panel.show_staged(project);
        let mut bar = bar;
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
        // Keep the refresh affordance in Git, adjacent to the selected
        // change. The diff command runs in the background.
        let refresh = div()
            .px_1()
            .text_color(rgb(0x71717A))
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(move |view, _, window, cx| {
                    if view.shutting_down {
                        return;
                    }
                    cx.stop_propagation();
                    window.focus(&view.focus_handle);
                    view.diff_dirty_hint = true;
                    cx.notify();
                }),
            )
            .child(git_panel::REFRESH_ICON.to_string());
        let open_path = path.clone();
        let open = div()
            .px_1()
            .text_color(rgb(0x71717A))
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(move |view, _, window, cx| {
                    if view.shutting_down {
                        return;
                    }
                    cx.stop_propagation();
                    window.focus(&view.focus_handle);
                    view.diff_open_file(project, open_path.clone(), cx);
                }),
            )
            .child("open");
        let copy_path = path.clone();
        let copy = div()
            .px_1()
            .text_color(rgb(0x71717A))
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(move |view, _, window, cx| {
                    if view.shutting_down {
                        return;
                    }
                    cx.stop_propagation();
                    window.focus(&view.focus_handle);
                    view.diff_copy_path(project, &copy_path, cx);
                }),
            )
            .child("copy");
        bar = bar.child(
            div()
                .flex()
                .flex_row()
                .items_center()
                .justify_between()
                .px_2()
                .py_1()
                .text_color(rgb(0xA1A1AA))
                .child(format!("Diff · {}", path.to_string_lossy()))
                .child(
                    div()
                        .flex()
                        .flex_row()
                        .items_center()
                        .gap_1()
                        .child(open)
                        .child(copy)
                        .child(refresh),
                ),
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
                diff_panel::DiffEmpty::Unavailable(detail) => {
                    format!("Git unavailable: {}", short(detail))
                }
                diff_panel::DiffEmpty::Failed(detail) => {
                    format!("Git error: {}", short(detail))
                }
            };
            let amber = !matches!(
                empty,
                diff_panel::DiffEmpty::NoRoot | diff_panel::DiffEmpty::NotRepo
            );
            return bar.child(
                div()
                    .px_2()
                    .py_1()
                    .text_color(rgb(if amber { 0xFDE68A } else { 0x71717A }))
                    .child(message),
            );
        }
        let Some(info) = self.diff_panel.diff_for(project, staged).cloned() else {
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
            let count = file.hunks.len().min(diff_panel::MAX_DIFF_RENDER_HUNKS);
            let cursor = self
                .diff_panel
                .selected_hunk(project)
                .min(count.saturating_sub(1));
            bar = bar.child(div().px_2().py_1().text_color(rgb(0xA1A1AA)).child(format!(
                "{} ({}/{})",
                file.path.to_string_lossy(),
                if count == 0 { 0 } else { cursor + 1 },
                file.hunk_count,
            )));
            let nav = |label: &'static str, next: bool| {
                div()
                    .px_2()
                    .py_1()
                    .text_color(rgb(0x71717A))
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(move |view, _, window, cx| {
                            if view.shutting_down {
                                return;
                            }
                            cx.stop_propagation();
                            window.focus(&view.focus_handle);
                            if next {
                                view.diff_panel.next_hunk(project);
                            } else {
                                view.diff_panel.prev_hunk(project);
                            }
                            cx.notify();
                        }),
                    )
                    .child(label)
            };
            bar = bar.child(
                div()
                    .flex()
                    .flex_row()
                    .items_center()
                    .gap_1()
                    .child(nav("‹ prev", false))
                    .child(nav("next ›", true)),
            );
            for (index, hunk) in file
                .hunks
                .iter()
                .take(diff_panel::MAX_DIFF_RENDER_HUNKS)
                .enumerate()
            {
                let current = index == cursor;
                let heading = format!(
                    "@@ -{},{} +{},{} @@{}",
                    hunk.old_start,
                    hunk.old_lines,
                    hunk.new_start,
                    hunk.new_lines,
                    if current { " ◀" } else { "" },
                );
                let mut hunk_header = div()
                    .flex()
                    .flex_row()
                    .items_center()
                    .justify_between()
                    .px_2()
                    .py_1()
                    .text_color(rgb(0x71717A))
                    .child(heading);
                if !staged {
                    let stage_path = path.clone();
                    hunk_header = hunk_header.child(
                        div()
                            .px_1()
                            .on_mouse_down(
                                MouseButton::Left,
                                cx.listener(move |view, _, window, cx| {
                                    if view.shutting_down {
                                        return;
                                    }
                                    cx.stop_propagation();
                                    window.focus(&view.focus_handle);
                                    view.diff_stage_file(project, stage_path.clone(), cx);
                                }),
                            )
                            .child(git_panel::STAGE_ICON.to_string()),
                    );
                }
                bar = bar.child(hunk_header);
                for line in hunk.lines.iter().take(diff_panel::MAX_DIFF_RENDER_LINES) {
                    let (marker, color) = match line.kind {
                        omaterm_core::DiffLineKind::Addition => ("+", 0x89E051),
                        omaterm_core::DiffLineKind::Deletion => ("-", 0xE06C75),
                        omaterm_core::DiffLineKind::Context => (" ", 0xA1A1AA),
                    };
                    bar = bar.child(
                        div()
                            .px_2()
                            .text_color(rgb(color))
                            .child(format!("{marker}{}", line.text)),
                    );
                }
                if hunk.truncated || hunk.lines.len() > diff_panel::MAX_DIFF_RENDER_LINES {
                    bar = bar.child(
                        div()
                            .px_2()
                            .py_1()
                            .text_color(rgb(0x71717A))
                            .child("… (hunk truncated)"),
                    );
                }
            }
            if file.truncated || file.hunks.len() > diff_panel::MAX_DIFF_RENDER_HUNKS {
                bar = bar.child(
                    div()
                        .px_2()
                        .py_1()
                        .text_color(rgb(0x71717A))
                        .child("… (file truncated)"),
                );
            }
        }
        bar = bar.child(
            div()
                .px_2()
                .py_1()
                .text_color(rgb(0x52525B))
                .child(if staged {
                    "Open/copy path · Alt+N/P hunk navigation"
                } else {
                    "Stage hunk · open/copy path · Alt+N/P hunk navigation"
                }),
        );
        bar
    }

    /// Right-sidebar tab bar: `| Files | Git |` (M14 follow-up). Diff
    /// details are shown inline in Git when a changed path is selected.
    fn render_sidebar_tabs(&mut self, bar: Div, cx: &mut Context<Self>) -> Div {
        let active = self.sidebar_tab;
        let mut row = div().flex().flex_row().items_center().gap_1().px_2().py_1();
        for (tab, label) in [(SidebarTab::Files, "Files"), (SidebarTab::Git, "Git")] {
            let selected = tab == active;
            row = row.child(
                div()
                    .px_2()
                    .py_1()
                    .rounded_sm()
                    .bg(rgb(if selected { 0x27272A } else { 0x111113 }))
                    .text_color(rgb(if selected { 0xFAFAFA } else { 0x71717A }))
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(move |view, _, window, cx| {
                            if view.shutting_down {
                                return;
                            }
                            cx.stop_propagation();
                            window.focus(&view.focus_handle);
                            view.sidebar_tab = tab;
                            view.git_panel.set_commit_focused(false);
                            view.files_vdrag = None;
                            view.files_hdrag = None;
                            cx.notify();
                        }),
                    )
                    .child(label),
            );
        }
        bar.child(row)
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
                        .child(files::SEARCH_ICON.to_string()),
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
                        .child(files::SEARCH_ICON.to_string()),
                )
                .child({
                    let caret_h = px(f32::from(self.fonts(&*cx).line_height));
                    let caret_bg = if self.ctrlp_caret_on {
                        rgb(0x71717A)
                    } else {
                        rgba(0x00000000)
                    };
                    div()
                        .flex()
                        .flex_row()
                        .items_center()
                        .child(div().child(self.ctrlp_query.clone()))
                        .child(div().w(px(2.0)).h(caret_h).bg(caret_bg))
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
            overlay = overlay.child(div().px_3().py_2().text_color(rgb(0x71717A)).child(
                if self.ctrlp_query.is_empty() {
                    "Type to narrow the file list."
                } else {
                    "No matches."
                },
            ));
            overlay = overlay.child(Self::ctrlp_hint_row());
            return self.ctrlp_frame(overlay, box_x, box_w);
        }
        // Match indices come from the same skim scorer as the ranking, so
        // highlights always agree with result order. Recomputed per frame
        // over at most 100 short strings — microseconds, no caching needed.
        let query = self.ctrlp_query.clone();
        for (index, entry) in self.ctrlp_results.iter().take(100).cloned().enumerate() {
            let selected = index == self.ctrlp_selected;
            let icon = files::icon_for(&entry.path, entry.kind, false);
            let icon_color = icon
                .color
                .unwrap_or(if selected { 0xFA_FA_FA } else { 0xA1_A1_AA });
            let full = entry.path.to_string_lossy().into_owned();
            let (name, parent) = match full.rfind('/') {
                Some(at) => (full[at + 1..].to_owned(), full[..at].to_owned()),
                None => (full.clone(), String::new()),
            };
            // Full-path char indices mapped onto each segment, then to
            // byte ranges via highlight_ranges for StyledText.
            let matched: Vec<usize> = omaterm_context::fuzzy_match_indices(&full, &query)
                .map(|(_, indices)| indices)
                .unwrap_or_default();
            let parent_chars = parent.chars().count();
            let name_offset = if parent.is_empty() {
                0
            } else {
                parent_chars + 1
            };
            let name_hits: Vec<usize> = matched
                .iter()
                .filter_map(|index| index.checked_sub(name_offset))
                .collect();
            let parent_hits: Vec<usize> = matched
                .iter()
                .copied()
                .filter(|index| *index < parent_chars)
                .collect();
            // One StyledText per row half: a single wrapping context, so
            // long names clip instead of breaking rows apart. Ranges are
            // byte spans from char indices (see highlight_ranges).
            let accent = HighlightStyle {
                // files::MATCH_ACCENT as HSL.
                color: Some(hsla(0.594, 1.0, 0.649, 1.0)),
                ..Default::default()
            };
            // truncate() cascades nowrap + ellipsis into the StyledText;
            // overflow_hidden alone would still let it wrap.
            let name_row = div().flex_1().min_w(px(0.0)).truncate().child(
                StyledText::new(name.clone()).with_highlights(
                    files::highlight_ranges(&name, &name_hits)
                        .into_iter()
                        .map(|range| (range, accent)),
                ),
            );
            // Capped well below the filename's share: the name is the
            // primary identifier, the parent is context.
            let mut parent_row = div()
                .truncate()
                .min_w(px(0.0))
                .text_color(rgb(0x71717A))
                .max_w(px(140.0));
            parent_row = parent_row.child(
                StyledText::new(parent.clone()).with_highlights(
                    files::highlight_ranges(&parent, &parent_hits)
                        .into_iter()
                        .map(|range| (range, accent)),
                ),
            );
            let mut row = div()
                .flex()
                .flex_row()
                .items_center()
                .px_2()
                .py_1()
                .rounded_sm()
                .bg(rgb(if selected { 0x27272A } else { 0x18181B }))
                .text_color(rgb(if selected { 0xFAFAFA } else { 0xA1A1AA }));
            if selected {
                row = row.border_l_2().border_color(rgb(files::MATCH_ACCENT));
            }
            overlay = overlay.child(
                row.on_mouse_down(
                    MouseButton::Left,
                    cx.listener(move |view, _, window, cx| {
                        if view.shutting_down {
                            return;
                        }
                        window.focus(&view.focus_handle);
                        view.ctrlp_selected = index;
                        view.ctrlp_confirm(cx);
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
                                .w(px(18.0))
                                .flex()
                                .flex_shrink_0()
                                .items_center()
                                .justify_center()
                                .text_color(rgb(icon_color))
                                .child(icon.glyph.to_string()),
                        )
                        .child(name_row)
                        .child(parent_row),
                ),
            );
        }
        if self.ctrlp_truncated {
            overlay = overlay.child(
                div()
                    .px_3()
                    .py_1()
                    .text_color(rgb(0x71717A))
                    .child("(truncated: showing first 100)"),
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
            .child("up/down navigate · enter open · esc dismiss")
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
                    ("No projects yet.", "New project (Ctrl+Shift+P)")
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
                                    if has_project {
                                        view.create_tab(cx);
                                    } else {
                                        view.create_project(cx);
                                    }
                                }),
                            )
                            .child(action),
                    )
                    .into_any_element()
            });
        let selected_project = self.coordinator.selected_project_id();
        let mut sidebar = div()
            .w(px(180.0))
            .h_full()
            .flex()
            .flex_col()
            .gap_1()
            .p_2()
            .bg(rgb(0x111113));
        sidebar = sidebar.child(
            div()
                .px_2()
                .py_1()
                .text_color(rgb(0xA1A1AA))
                .child("PROJECTS"),
        );
        let mut project_label_counts = HashMap::<String, usize>::new();
        for (index, project) in self.coordinator.projects().iter().enumerate() {
            let id = project.id;
            let base_name = project.display_name(index + 1);
            let occurrence = project_label_counts.entry(base_name.clone()).or_default();
            *occurrence += 1;
            let name = if *occurrence == 1 {
                base_name
            } else {
                format!("{base_name} {}", *occurrence)
            };
            let active = selected_project == Some(id);
            let close_id = id;
            let pick_id = id;
            let label = if self.show_project_hints && index < 9 {
                format!("{} · {name}", index + 1)
            } else {
                name
            };
            let label = div().flex_1().child(label);
            let row = div()
                .flex()
                .flex_row()
                .items_center()
                .justify_between()
                .px_2()
                .py_1()
                .rounded_sm()
                .bg(rgb(if active { 0x27272A } else { 0x111113 }))
                .text_color(rgb(if active { 0xFAFAFA } else { 0xA1A1AA }))
                .on_mouse_down(
                    MouseButton::Left,
                    cx.listener(move |view, _, window, cx| {
                        if view.shutting_down {
                            return;
                        }
                        window.focus(&view.focus_handle);
                        let _ = view.dispatch_command(
                            OmaCommand::Project(ProjectCommand::Select { project: id }),
                            cx,
                        );
                    }),
                )
                .child(label);
            // Keep row controls inside the selected row and out of
            // the way for inactive projects.
            let row = if active {
                row.child(
                    div()
                        .px_1()
                        .text_color(rgb(0x71717A))
                        .on_mouse_down(
                            MouseButton::Left,
                            cx.listener(move |view, _, window, cx| {
                                if view.shutting_down {
                                    return;
                                }
                                cx.stop_propagation();
                                window.focus(&view.focus_handle);
                                view.pick_project_directory(pick_id, cx);
                            }),
                        )
                        .child("change"),
                )
                .child(
                    div()
                        .px_1()
                        .text_color(rgb(0xA1A1AA))
                        .on_mouse_down(
                            MouseButton::Left,
                            cx.listener(move |view, _, window, cx| {
                                if view.shutting_down {
                                    return;
                                }
                                cx.stop_propagation();
                                window.focus(&view.focus_handle);
                                view.close_project(close_id, cx);
                            }),
                        )
                        .child("×"),
                )
            } else {
                row
            };
            sidebar = sidebar.child(row);
        }
        sidebar = sidebar.child(
            div()
                .px_2()
                .py_1()
                .text_color(rgb(0xA1A1AA))
                .on_mouse_down(
                    MouseButton::Left,
                    cx.listener(|view, _, window, cx| {
                        if view.shutting_down {
                            return;
                        }
                        window.focus(&view.focus_handle);
                        view.create_project(cx);
                    }),
                )
                .child("+ New project"),
        );

        let mut tabs_bar = div()
            .h(px(36.0))
            .flex()
            .flex_row()
            .items_center()
            .gap_1()
            .px_2()
            .bg(rgb(0x111113));
        if let Some(project) = self.coordinator.active_project() {
            for (index, tab) in project.tabs.iter().enumerate() {
                let project_id = project.id;
                let tab_id = tab.id;
                let label = tab.display_name(index + 1);
                let active = project.selected_tab == Some(tab_id);
                let label = div().flex_1().child(label);
                let chip = div()
                    .flex()
                    .flex_row()
                    .items_center()
                    .gap_1()
                    .px_3()
                    .py_1()
                    .rounded_sm()
                    .bg(rgb(if active { 0x27272A } else { 0x111113 }))
                    .text_color(rgb(if active { 0xFAFAFA } else { 0xA1A1AA }))
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(move |view, _, window, cx| {
                            if view.shutting_down {
                                return;
                            }
                            window.focus(&view.focus_handle);
                            let _ = view.dispatch_command(
                                OmaCommand::Tab(TabCommand::Select { tab: tab_id }),
                                cx,
                            );
                        }),
                    )
                    .child(label);
                // The selected tab owns its close control; the add button
                // remains the final item in the strip.
                let chip = if active {
                    chip.child(
                        div()
                            .px_1()
                            .text_color(rgb(0xA1A1AA))
                            .on_mouse_down(
                                MouseButton::Left,
                                cx.listener(move |view, _, window, cx| {
                                    if view.shutting_down {
                                        return;
                                    }
                                    cx.stop_propagation();
                                    window.focus(&view.focus_handle);
                                    view.close_tab(project_id, tab_id, cx);
                                }),
                            )
                            .child("×"),
                    )
                } else {
                    chip
                };
                tabs_bar = tabs_bar.child(chip);
            }
            tabs_bar = tabs_bar.child(
                div()
                    .px_2()
                    .py_1()
                    .text_color(rgb(0xA1A1AA))
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(|view, _, window, cx| {
                            window.focus(&view.focus_handle);
                            view.create_tab(cx);
                        }),
                    )
                    .child("+"),
            );
        } else {
            tabs_bar = tabs_bar.child(
                div()
                    .px_3()
                    .py_1()
                    .text_color(rgb(0xA1A1AA))
                    .child("No project selected"),
            );
        }
        // History state chip: same semantic opt-in toggle as Ctrl+Shift+O.
        if let Some(status) = self.history_status_text() {
            let attention = status != "history: off" && status != "history: on";
            tabs_bar = tabs_bar.child(
                div()
                    .px_3()
                    .py_1()
                    .rounded_sm()
                    .bg(rgb(if attention { 0x3F321D } else { 0x111113 }))
                    .text_color(rgb(if attention { 0xFDE68A } else { 0xA1A1AA }))
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(move |view, _, window, cx| {
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
        let mut files_bar = div()
            .w(px(files::RIGHT_SIDEBAR_WIDTH_PX))
            .h_full()
            .flex()
            .flex_col()
            .gap_1()
            .p_2()
            .bg(rgb(0x111113))
            .on_scroll_wheel(cx.listener(|view, event: &ScrollWheelEvent, _, cx| {
                // Row-step scrolling with the terminal's delta
                // convention: positive deltas move toward the top
                // (earlier rows). Render clamps the offset, so no max
                // math is needed here.
                let row_height = {
                    let line_height: f32 = view.fonts(&*cx).line_height.into();
                    if line_height <= 0.0 {
                        return;
                    }
                    line_height + WorkspaceView::FILES_ROW_VPAD
                };
                let dy_lines: f32 = match event.delta {
                    ScrollDelta::Pixels(point) => f32::from(point.y) / row_height,
                    ScrollDelta::Lines(point) => point.y,
                };
                let mut steps = dy_lines.round() as i32;
                if steps == 0 && dy_lines != 0.0 {
                    steps = dy_lines.signum() as i32;
                }
                // Horizontal: native x deltas, plus Shift+wheel (vertical
                // deltas with Shift held) for ordinary mice. Checked before
                // the vertical early-return so pure-horizontal events are
                // never swallowed.
                let dx_px: f32 = match event.delta {
                    ScrollDelta::Pixels(point) => f32::from(point.x),
                    ScrollDelta::Lines(point) => point.x * row_height,
                };
                let dy_px: f32 = match event.delta {
                    ScrollDelta::Pixels(point) => f32::from(point.y),
                    ScrollDelta::Lines(point) => point.y * row_height,
                };
                let mut sideways = dx_px;
                if event.modifiers.shift {
                    sideways += dy_px;
                }
                if steps == 0 && sideways == 0.0 {
                    return;
                }
                view.files_scroll_rows = (view.files_scroll_rows as i32 - steps).max(0) as usize;
                if sideways != 0.0 {
                    view.files_scroll_cols =
                        (view.files_scroll_cols + sideways).clamp(0.0, files::MAX_SCROLL_COLS_PX);
                }
                cx.notify();
            }));
        files_bar = self.render_sidebar_tabs(files_bar, cx);
        match self.sidebar_tab {
            SidebarTab::Files => {
                files_bar =
                    self.render_files_tree(files_bar, f32::from(window.viewport_size().height), cx);
                if let Some(message) = self.files_warning.clone() {
                    files_bar = files_bar
                        .child(div().px_2().py_1().text_color(rgb(0xFDE68A)).child(message));
                }
            }
            SidebarTab::Git => {
                files_bar = self.render_git_panel(files_bar, cx);
            }
        }

        let mut pane_area = div().flex().flex_1().flex_col().size_full().relative();
        if let Some((arm, at)) = self.history_arm
            && at.elapsed() < HISTORY_ARM_WINDOW
        {
            pane_area = pane_area.child(
                div()
                    .px_3()
                    .py_1()
                    .bg(rgb(0x3F321D))
                    .text_color(rgb(0xFDE68A))
                    .child(Self::history_arm_text(&arm)),
            );
        }
        if let Some(message) = self.persistence_warning.clone() {
            pane_area = pane_area.child(
                div()
                    .px_3()
                    .py_1()
                    .bg(rgb(0x3F321D))
                    .text_color(rgb(0xFDE68A))
                    .child(message),
            );
        }
        if let Some(message) = self.config_warning.clone() {
            pane_area = pane_area.child(
                div()
                    .px_3()
                    .py_1()
                    .bg(rgb(0x3F321D))
                    .text_color(rgb(0xFDE68A))
                    .child(message),
            );
        }
        if let Some(message) = self.input_notice.clone() {
            pane_area = pane_area.child(
                div()
                    .px_3()
                    .py_1()
                    .bg(rgb(0x3F321D))
                    .text_color(rgb(0xFDE68A))
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
                    .bg(rgb(0x3F321D))
                    .text_color(rgb(0xFDE68A))
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
                    .bg(rgb(0x3F1D1D))
                    .text_color(rgb(0xFCA5A5))
                    .child(message)
                    .child(
                        div()
                            .px_2()
                            .py_1()
                            .text_color(rgb(0xFECACA))
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
        pane_area = pane_area.child(div().flex().flex_1().size_full().child(content));
        // Finder paints last so the floating layer sits above the terminal.
        // Box geometry is plain arithmetic from the fixed sidebar widths —
        // no reliance on align/max interplay.
        if self.ctrlp_open {
            let viewport_w: f32 = window.viewport_size().width.into();
            let pane_w = (viewport_w - 180.0 - files::RIGHT_SIDEBAR_WIDTH_PX).max(1.0);
            let box_w = (pane_w - 32.0).clamp(200.0, 600.0);
            let box_x = ((pane_w - box_w) / 2.0).max(0.0);
            pane_area = pane_area.child(self.render_ctrlp(box_x, box_w, cx));
        }
        let main = div()
            .flex()
            .flex_1()
            .flex_col()
            .size_full()
            .child(tabs_bar)
            .child(pane_area);
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
            .bg(rgb(0x18181B))
            .child(sidebar)
            .child(main)
            .child(files_bar)
    }
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
    Application::new().run(|cx: &mut App| {
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
    use super::{SidebarTab, project_jump_index, select_mono_family};

    #[test]
    fn sidebar_tab_defaults_to_files() {
        assert_eq!(SidebarTab::default(), SidebarTab::Files);
        assert_ne!(SidebarTab::Files, SidebarTab::Git);
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
}
