use std::collections::{HashMap, HashSet};
use std::path::Path;
use std::path::PathBuf;

use crate::credentials::Credentials;
use crate::editor::{
    DocumentStore, EditorIoCompletion, EditorIoError, EditorIoJob, EditorIoKind, EditorIoQueue,
    EditorIoRequest, EditorIoSubmitError, EditorIoSuccess, EditorOperationId,
};
use crate::history::HistoryManager;
use omaterm_core::{
    CommandContext, CommandError, CommandOutput, CommandResult, DiffCommand, EditorCommand,
    ErrorCode, FileCommand, FileListInfo, GitCommand, GitStatusInfo, HistoryCommand,
    HistoryStatusInfo, JournalEntryInfo, MAX_PROCESS_ENTRIES, OmaCommand, PaneCommand, PaneContent,
    PaneId, PaneInfo, ProcessCommand, ProcessEntryInfo, ProcessListInfo, ProjectCommand, ProjectId,
    ProjectInfo, ProjectRootInfo, SessionId, SplitDirection, TabCommand, TabId, TabInfo,
    TerminalCommand, TerminalInfo,
};
use omaterm_protocol::CapabilityToken;
use omaterm_terminal::history::RecordedEvent;
use omaterm_terminal::workspace::ClosedSessions;
use omaterm_terminal::{
    ClosedPane, CoordinatorError, LinuxProcessInspector, ProcessInspector, ProjectSessionCommit,
    SessionSpawnQueue, SpawnCompletion, SplitSessionCommit, TerminalConfig, TerminalSession,
    WorkspaceCoordinator,
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

/// Owner-side guard for one editor I/O request. Queue operation IDs remain
/// internal so editor completions share the router receipt namespace without
/// colliding with terminal launches.
struct PendingEditor {
    context: CommandContext,
    project: ProjectId,
    root_path: PathBuf,
    root_identity: omaterm_context::RootIdentity,
    path: PathBuf,
    document: Option<omaterm_core::DocumentId>,
    generation: u64,
    kind: EditorIoKind,
    cancelled: bool,
}

pub enum CommandEffect {
    SessionStarted(omaterm_core::SessionId),
    SessionClosed(ClosedPane),
    PersistenceDirty,
    WorkspaceChanged,
    GitChanged(ProjectId),
    ProjectDirectoryChanged(ProjectId),
    FileOpened(ProjectId),
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
    editor_io: Option<EditorIoQueue>,
    pending_editors: HashMap<u64, PendingEditor>,
    editor_receipts: HashMap<EditorOperationId, u64>,
    next_operation: u64,
    credentials: Option<Credentials>,
    socket_path: Option<PathBuf>,
    history: Option<HistoryManager>,
    /// Native editor buffers (M19 Phase C). Owned here so every entry
    /// point — UI now, IPC/CLI when a wire contract lands — shares one
    /// lifecycle. Dirty text is never persisted; only explicit saves
    /// touch the filesystem.
    documents: DocumentStore,
    /// Verified scrollback staged per pane for restore-before-spawn. Loaded
    /// by the desktop at restore time, consumed when the restored session
    /// commits — before any reader pumps fresh shell output.
    pending_history: HashMap<PaneId, Vec<RecordedEvent>>,
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
        let history_store = omaterm_state::default_history_dir().map(|dir| {
            omaterm_state::HistoryStore::new(dir, omaterm_state::HistoryLimits::default())
        });
        let provider: Box<dyn omaterm_state::KeyProvider> =
            Box::new(omaterm_state::OsKeyProvider::omaterm_default());
        let mut history = HistoryManager::new(history_store, provider);
        history.load_config();
        Self {
            coordinator,
            spawns: Some(SessionSpawnQueue::new(MAX_PENDING_LAUNCHES)),
            pending: HashMap::new(),
            editor_io: Some(EditorIoQueue::new()),
            pending_editors: HashMap::new(),
            editor_receipts: HashMap::new(),
            next_operation: 0,
            credentials: None,
            socket_path: None,
            history: Some(history),
            documents: DocumentStore::default(),
            pending_history: HashMap::new(),
        }
    }

    /// Owner-side buffer access for the Phase D editing surface (tests
    /// exercise it meanwhile).
    #[allow(dead_code)]
    pub fn documents(&self) -> &DocumentStore {
        &self.documents
    }

    #[allow(dead_code)]
    pub fn documents_mut(&mut self) -> &mut DocumentStore {
        &mut self.documents
    }

    /// Replace the history manager (tests inject an in-memory key provider).
    #[cfg(test)]
    pub fn with_history_manager(mut self, manager: HistoryManager) -> Self {
        self.history = Some(manager);
        self
    }

    pub fn history_manager(&self) -> Option<&HistoryManager> {
        self.history.as_ref()
    }

    pub fn history_manager_mut(&mut self) -> Option<&mut HistoryManager> {
        self.history.as_mut()
    }

    /// Move the history manager out for shutdown-flush ownership. Dispatch
    /// must already be stopped (the IPC server closes first).
    pub fn take_history_manager(&mut self) -> Option<HistoryManager> {
        self.history.take()
    }

    /// Stage verified scrollback for a restoring pane. Consumed exactly once
    /// when the restored session commits.
    pub fn stage_restore_history(&mut self, pane: PaneId, events: Vec<RecordedEvent>) {
        self.pending_history.insert(pane, events);
    }

    fn take_restore_history(&mut self, pane: PaneId) -> Option<Vec<RecordedEvent>> {
        self.pending_history.remove(&pane)
    }

    /// Drop staged restore history for a closed pane (e.g. closed before
    /// its restore commit landed). Called on every pane-close path.
    pub fn discard_restore_history(&mut self, pane: PaneId) {
        self.pending_history.remove(&pane);
    }

    fn history_manager_or_unavailable(&mut self) -> Result<&mut HistoryManager, CommandError> {
        self.history.as_mut().ok_or_else(|| {
            CommandError::new(ErrorCode::HistoryUnavailable, "history is shutting down")
        })
    }

    fn find_pane(&self, pane: PaneId) -> Option<(ProjectId, TabId)> {
        for project in &self.coordinator.window().projects {
            for tab in &project.tabs {
                if tab.tree.find(pane).is_some() {
                    return Some((project.id, tab.id));
                }
            }
        }
        None
    }

    /// Filesystem root for file operations: project existence plus scope
    /// check, then M12 resolution. `Ok(None)` is the explicit empty state
    /// (non-repo project without a usable pin) — listing/searching returns
    /// an empty envelope, only `open` reports `no_project_root`.
    fn file_root(
        &mut self,
        context: CommandContext,
        project: ProjectId,
    ) -> Result<Option<PathBuf>, CommandError> {
        let Some(target) = self.coordinator.window().project(project) else {
            return Err(CommandError::new(
                ErrorCode::ProjectNotFound,
                "project does not exist",
            ));
        };
        if matches!(context, CommandContext::Project(scope) if scope != project) {
            return Err(CommandError::new(
                ErrorCode::CrossProjectDenied,
                "outside project scope",
            ));
        }
        let pinned = target.pinned_directory.clone();
        let active_cwd = self.active_shell_cwd(project);
        Ok(omaterm_context::resolve_root(pinned.as_deref(), active_cwd.as_deref()).root)
    }

    /// Synchronous editor lifecycle operations. Filesystem work is prepared
    /// and enqueued by `prepare_editor`; close remains owner-local.
    fn editor_dispatch(
        &mut self,
        _context: CommandContext,
        command: EditorCommand,
    ) -> CommandResult {
        match command {
            EditorCommand::Close { document } => {
                if self.documents.is_dirty(document) == Some(true) {
                    return err(
                        ErrorCode::DocumentConflict,
                        "unsaved changes; save or explicitly discard before closing",
                    );
                }
                if self.documents.remove(document) {
                    ok(CommandOutput::Unit)
                } else {
                    err(ErrorCode::DocumentNotOpen, "document is not open")
                }
            }
            EditorCommand::Open { .. }
            | EditorCommand::Save { .. }
            | EditorCommand::Revert { .. } => err(
                ErrorCode::RuntimeFailure,
                "editor filesystem work must use the async path",
            ),
        }
    }

    /// Owned snapshot of a live buffer for save/revert paths: project,
    /// supplied relative path, open-time root identity, on-disk revision,
    /// dirty flag, and buffer text.
    fn editor_buffer(
        &self,
        document: omaterm_core::DocumentId,
    ) -> Option<(
        ProjectId,
        PathBuf,
        omaterm_context::RootIdentity,
        omaterm_context::FileRevision,
        bool,
        String,
    )> {
        let store = &self.documents;
        Some((
            store.project_of(document)?,
            store.relative_path(document)?,
            store.open_root_identity(document)?,
            store.revision(document)?,
            store.is_dirty(document)?,
            store.buffer_text(document)?,
        ))
    }

    /// Capture owner-verified editor state, then hand only immutable data to
    /// the app-local worker. The returned receipt uses `next_operation`, the
    /// same router-wide sequence as terminal launches, while the queue keeps
    /// its own private operation ID for cancellation and completion routing.
    fn prepare_editor(
        &mut self,
        context: CommandContext,
        command: EditorCommand,
    ) -> DispatchOutcome {
        let error = |result| DispatchOutcome {
            result,
            effects: vec![],
        };
        let (project, root, document, generation, job) = match command {
            EditorCommand::Open { project, path } => {
                let root = match self.file_root(context, project) {
                    Ok(Some(root)) => root,
                    Ok(None) => {
                        return error(err(
                            ErrorCode::NoProjectRoot,
                            "project has no filesystem root",
                        ));
                    }
                    Err(issue) => return error(CommandResult::Err(issue)),
                };
                let root = match omaterm_context::EditorRoot::open(&root) {
                    Ok(root) => root,
                    Err(issue) => return error(editor_error(issue, ErrorCode::FileNotFound)),
                };
                (project, root, None, 0, EditorIoJob::Open { path })
            }
            EditorCommand::Save { document } => {
                let (project, path, open_root_identity, revision, _, text) = match self
                    .editor_buffer(document)
                {
                    Some(buffer) => buffer,
                    None => return error(err(ErrorCode::DocumentNotOpen, "document is not open")),
                };
                let generation = self
                    .documents
                    .generation(document)
                    .expect("live editor buffer");
                let root = match self.file_root(context, project) {
                    Ok(Some(root)) => root,
                    Ok(None) => {
                        return error(err(
                            ErrorCode::NoProjectRoot,
                            "project has no filesystem root",
                        ));
                    }
                    Err(issue) => return error(CommandResult::Err(issue)),
                };
                let root = match omaterm_context::EditorRoot::open(&root) {
                    Ok(root) => root,
                    Err(issue) => return error(editor_error(issue, ErrorCode::DocumentConflict)),
                };
                if root.identity() != open_root_identity {
                    return error(err(
                        ErrorCode::DocumentConflict,
                        "project root changed since open; reopen the document",
                    ));
                }
                (
                    project,
                    root,
                    Some(document),
                    generation,
                    EditorIoJob::Save {
                        document,
                        path,
                        text,
                        expected: Some(revision),
                    },
                )
            }
            EditorCommand::Revert { document } => {
                let (project, path, open_root_identity, _, _, _) = match self
                    .editor_buffer(document)
                {
                    Some(buffer) => buffer,
                    None => return error(err(ErrorCode::DocumentNotOpen, "document is not open")),
                };
                let generation = self
                    .documents
                    .generation(document)
                    .expect("live editor buffer");
                let root = match self.file_root(context, project) {
                    Ok(Some(root)) => root,
                    Ok(None) => {
                        return error(err(
                            ErrorCode::NoProjectRoot,
                            "project has no filesystem root",
                        ));
                    }
                    Err(issue) => return error(CommandResult::Err(issue)),
                };
                let root = match omaterm_context::EditorRoot::open(&root) {
                    Ok(root) => root,
                    Err(issue) => return error(editor_error(issue, ErrorCode::DocumentConflict)),
                };
                if root.identity() != open_root_identity {
                    return error(err(
                        ErrorCode::DocumentConflict,
                        "project root changed since open; reopen the document",
                    ));
                }
                (
                    project,
                    root,
                    Some(document),
                    generation,
                    EditorIoJob::Revert { document, path },
                )
            }
            EditorCommand::Close { .. } => unreachable!("close is owner-local"),
        };
        let kind = match &job {
            EditorIoJob::Open { .. } => EditorIoKind::Open,
            EditorIoJob::Save { .. } => EditorIoKind::Save,
            EditorIoJob::Revert { .. } => EditorIoKind::Revert,
        };
        let path = match &job {
            EditorIoJob::Open { path }
            | EditorIoJob::Save { path, .. }
            | EditorIoJob::Revert { path, .. } => path.clone(),
        };
        let request = EditorIoRequest {
            project,
            root_path: root.canonical_path().to_path_buf(),
            root_identity: root.identity(),
            generation,
            job,
        };
        let Some(queue) = &self.editor_io else {
            return error(err(ErrorCode::RuntimeFailure, "editor I/O queue is closed"));
        };
        let queue_operation = match queue.submit(request) {
            Ok(operation) => operation,
            Err(EditorIoSubmitError::QueueFull) => {
                return error(err(ErrorCode::RuntimeFailure, "editor I/O queue is full"));
            }
            Err(EditorIoSubmitError::Shutdown) => {
                return error(err(ErrorCode::RuntimeFailure, "editor I/O queue is closed"));
            }
            Err(EditorIoSubmitError::OperationIdExhausted) => {
                return error(err(
                    ErrorCode::RuntimeFailure,
                    "editor I/O operation IDs exhausted",
                ));
            }
        };
        self.next_operation += 1;
        let operation_id = self.next_operation;
        self.pending_editors.insert(
            operation_id,
            PendingEditor {
                context,
                project,
                root_path: root.canonical_path().to_path_buf(),
                root_identity: root.identity(),
                path,
                document,
                generation,
                kind,
                cancelled: false,
            },
        );
        self.editor_receipts.insert(queue_operation, operation_id);
        DispatchOutcome {
            result: ok(CommandOutput::Pending { operation_id }),
            effects: vec![],
        }
    }

    /// Shared stage/unstage/discard path (M14): project existence plus
    /// scope check through `file_root`, then one bounded git subprocess
    /// batch. No root (non-repo without a pin) is `not_a_repo` for
    /// mutations — unlike status, which renders the empty envelope. No
    /// persistence effects: git state lives outside the snapshot.
    fn git_mutation(
        &mut self,
        context: CommandContext,
        project: ProjectId,
        paths: &[PathBuf],
        mutation: GitMutation,
    ) -> CommandResult {
        let root = match self.file_root(context, project) {
            Ok(Some(root)) => root,
            Ok(None) => {
                return err(
                    ErrorCode::NotARepo,
                    "project is not inside a git repository",
                );
            }
            Err(error) => return CommandResult::Err(error),
        };
        let result = match mutation {
            GitMutation::Stage => omaterm_context::git_stage(&root, paths),
            GitMutation::Unstage => omaterm_context::git_unstage(&root, paths),
            GitMutation::Discard => omaterm_context::git_discard(&root, paths),
        };
        match result {
            Ok(()) => {
                tracing::debug!(
                    target: "omaterm::git",
                    project_id = %project.0,
                    operation = mutation.name(),
                    paths = paths.len(),
                    "git mutation applied",
                );
                ok(CommandOutput::Unit)
            }
            Err(error) => git_error(error),
        }
    }

    fn process_list(&self, context: CommandContext, project: ProjectId) -> CommandResult {
        let Some(owner) = self.coordinator.projects().iter().find(|p| p.id == project) else {
            return err(ErrorCode::ProjectNotFound, "project does not exist");
        };
        if matches!(context, CommandContext::Project(scope) if scope != project) {
            return err(ErrorCode::CrossProjectDenied, "outside project scope");
        }
        let inspector = LinuxProcessInspector;
        let mut entries = Vec::new();
        let mut seen = HashSet::new();
        let mut truncated = false;
        'tabs: for tab in &owner.tabs {
            for pane in tab.tree.panes() {
                let PaneContent::Terminal(session_id) = &pane.content else {
                    continue;
                };
                let Some(handle) = self.coordinator.registry().get(*session_id) else {
                    continue;
                };
                let Ok(session) = handle.lock() else {
                    continue;
                };
                let root_pid = session.child_pid();
                drop(session);
                if inspector.process_info(root_pid).is_none() {
                    continue;
                }
                let descendants = inspector.descendants(root_pid);
                let ports = inspector.listening_ports(root_pid);
                for process in descendants {
                    if !seen.insert(process.pid) {
                        continue;
                    }
                    if entries.len() == MAX_PROCESS_ENTRIES {
                        truncated = true;
                        break 'tabs;
                    }
                    entries.push(ProcessEntryInfo {
                        pid: process.pid,
                        ppid: process.ppid,
                        name: process.name,
                        pane: pane.id,
                        session: *session_id,
                        ports: ports
                            .iter()
                            .filter(|port| port.pid == process.pid)
                            .map(|port| port.port)
                            .collect(),
                        cpu_percent: None,
                        memory_bytes: None,
                    });
                }
            }
        }
        entries.sort_by_key(|entry| entry.pid);
        ok(CommandOutput::ProcessList(ProcessListInfo {
            entries,
            truncated,
        }))
    }

    /// Shared diff query path (M15): project existence plus scope check
    /// through `file_root`, then one bounded `git diff` subprocess. No
    /// root or a non-repo root renders the empty envelope (M14 product
    /// contract); no persistence effects — diffs are read-only views of
    /// repository state. Only counts reach the log: diff bodies never do
    /// (blueprint §45).
    fn diff_query(
        &mut self,
        context: CommandContext,
        project: ProjectId,
        path: Option<&std::path::Path>,
        staged: bool,
        context_lines: u8,
        files_only: bool,
    ) -> CommandResult {
        let root = match self.file_root(context, project) {
            Ok(Some(root)) => root,
            Ok(None) => {
                return ok(CommandOutput::Diff(omaterm_core::DiffInfo::empty(staged)));
            }
            Err(error) => return CommandResult::Err(error),
        };
        let request = omaterm_context::DiffRequest {
            staged,
            path: path.map(std::path::PathBuf::from),
            context_lines,
            files_only,
        };
        match omaterm_context::git_diff(&root, &request) {
            Ok(info) => {
                tracing::debug!(
                    target: "omaterm::diff",
                    project_id = %project.0,
                    staged = info.staged,
                    files = info.files.len(),
                    truncated = info.truncated,
                    "diff served",
                );
                ok(CommandOutput::Diff(info))
            }
            Err(omaterm_context::GitError::NotARepo) => {
                ok(CommandOutput::Diff(omaterm_core::DiffInfo::empty(staged)))
            }
            Err(error) => git_error(error),
        }
    }

    /// Shell CWD of the project's active tab (focused pane), refreshed from
    /// procfs like persistence does. `None` when the tab has no live shell —
    /// root resolution then reports the explicit empty state.
    fn active_shell_cwd(&mut self, project: ProjectId) -> Option<PathBuf> {
        let tab = self.coordinator.window().project(project)?.selected_tab()?;
        let PaneContent::Terminal(session) = tab.tree.find(tab.focused_pane)?.content else {
            return None;
        };
        let handle = self.coordinator.registry().get(session)?;
        let mut live = handle.lock().ok()?;
        live.refresh_cwd_from_procfs();
        Some(live.cwd().path.clone())
    }

    /// Apply the manager's desired recording flags to one live session.
    fn sync_session_history_flags(&self, pane: PaneId) {
        let (enabled, paused) = match &self.history {
            Some(manager) => (
                manager.config().enabled,
                manager.config().is_paused(&opaque_pane(pane)),
            ),
            None => return,
        };
        if let Some(session) = self.coordinator.session_id_for_pane(pane)
            && let Some(handle) = self.coordinator.registry().get(session)
            && let Ok(mut terminal) = handle.lock()
        {
            terminal.set_history_enabled(enabled);
            terminal.set_history_paused(paused);
        }
    }

    /// Reconcile every live session with history configuration. The
    /// periodic desktop flush repeats this, so a missed update heals.
    fn sync_all_session_history_flags(&self) {
        let panes: Vec<PaneId> = self
            .coordinator
            .window()
            .projects
            .iter()
            .flat_map(|project| project.tabs.iter())
            .flat_map(|tab| tab.tree.panes())
            .map(|pane| pane.id)
            .collect();
        for pane in panes {
            self.sync_session_history_flags(pane);
        }
    }

    /// Replay staged scrollback into a freshly committed restored session
    /// and seed its recorder, before any reader pumps shell output: this
    /// runs inside `finish_launch`, ahead of the `SessionStarted` effect
    /// that starts runtime. Fresh shell, fresh PIDs — only pixels return.
    ///
    /// The trailing partial line (an idle shell's rendered prompt) is
    /// stripped before replay and seeding alike, so the fresh shell's own
    /// prompt does not stack under a stale duplicate on every restart.
    fn replay_restore_history(&mut self, pane: PaneId) {
        let Some(events) = self.take_restore_history(pane) else {
            return;
        };
        if events.is_empty() {
            return;
        }
        let events = omaterm_terminal::history::strip_trailing_partial_line(&events);
        let paused = self
            .history
            .as_ref()
            .is_some_and(|manager| manager.config().is_paused(&opaque_pane(pane)));
        let replayed = (|| {
            let session = self.coordinator.session_id_for_pane(pane)?;
            let handle = self.coordinator.registry().get(session)?;
            let mut terminal = handle.lock().ok()?;
            omaterm_terminal::history::replay_into(terminal.engine_mut(), &events);
            terminal.start_history_with_seed(&events);
            terminal.set_history_paused(paused);
            Some(())
        })()
        .is_some();
        if !replayed {
            // Never silently drop verified history: re-stage so a later
            // commit or retry of the same pane attempts replay again.
            self.pending_history.insert(pane, events);
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

    /// Pinned directory for git root resolution (M14 desktop refresh
    /// clones this on the UI thread; all git subprocesses run on a
    /// background worker).
    pub fn pinned_for(&self, project: ProjectId) -> Option<PathBuf> {
        self.coordinator
            .window()
            .project(project)
            .and_then(|target| target.pinned_directory.clone())
    }

    /// Active shell CWD for the same path (procfs refresh, no subprocess).
    pub fn shell_cwd_for(&mut self, project: ProjectId) -> Option<PathBuf> {
        self.active_shell_cwd(project)
    }

    /// Cached shell CWD for background work setup. Unlike `shell_cwd_for`,
    /// this reads the already tracked session value and never probes `/proc`
    /// on the caller thread.
    pub fn cached_shell_cwd_for(&self, project: ProjectId) -> Option<PathBuf> {
        let tab = self.coordinator.window().project(project)?.selected_tab()?;
        let PaneContent::Terminal(session) = &tab.tree.find(tab.focused_pane)?.content else {
            return None;
        };
        let handle = self.coordinator.registry().get(*session)?;
        handle.lock().ok().map(|live| live.cwd().path.clone())
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
                ProjectCommand::Delete { project }
                | ProjectCommand::Rename { project, .. }
                | ProjectCommand::SetDirectory { project, .. }
                | ProjectCommand::Root { project },
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
            OmaCommand::History(
                HistoryCommand::EnablePersistence
                | HistoryCommand::DisablePersistence
                | HistoryCommand::Status
                | HistoryCommand::ClearWorkspace,
            ) => {
                return Err(CommandError::new(
                    ErrorCode::PermissionDenied,
                    "global history operation requires local authority",
                ));
            }
            OmaCommand::History(HistoryCommand::ClearProject { project }) => Some(*project),
            OmaCommand::History(
                HistoryCommand::PausePane { pane }
                | HistoryCommand::ResumePane { pane }
                | HistoryCommand::ListJournal { pane, .. }
                | HistoryCommand::ClearPane { pane },
            ) => owner_pane(*pane),
            OmaCommand::File(FileCommand::List { project, .. })
            | OmaCommand::File(FileCommand::Search { project, .. })
            | OmaCommand::File(FileCommand::Open { project, .. }) => Some(*project),
            OmaCommand::Editor(EditorCommand::Open { project, .. }) => Some(*project),
            OmaCommand::Editor(
                EditorCommand::Close { document }
                | EditorCommand::Save { document }
                | EditorCommand::Revert { document },
            ) => self.documents.project_of(*document),
            OmaCommand::Git(GitCommand::Status { project })
            | OmaCommand::Git(GitCommand::Stage { project, .. })
            | OmaCommand::Git(GitCommand::StageHunk { project, .. })
            | OmaCommand::Git(GitCommand::Unstage { project, .. })
            | OmaCommand::Git(GitCommand::Discard { project, .. })
            | OmaCommand::Git(GitCommand::Commit { project, .. }) => Some(*project),
            OmaCommand::Diff(DiffCommand::Show { project, .. })
            | OmaCommand::Diff(DiffCommand::ListFiles { project, .. }) => Some(*project),
            OmaCommand::Process(ProcessCommand::List { project }) => Some(*project),
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
        if matches!(
            command,
            OmaCommand::Editor(
                EditorCommand::Open { .. }
                    | EditorCommand::Save { .. }
                    | EditorCommand::Revert { .. }
            )
        ) {
            let OmaCommand::Editor(command) = command else {
                unreachable!("editor command was matched above");
            };
            return self.prepare_editor(context, command);
        }
        let mut effects = Vec::new();
        let file_opened = match &command {
            OmaCommand::File(FileCommand::Open { project, .. }) => Some(*project),
            _ => None,
        };
        let git_changed = match &command {
            OmaCommand::Git(
                GitCommand::Stage { project, .. }
                | GitCommand::StageHunk { project, .. }
                | GitCommand::Unstage { project, .. }
                | GitCommand::Discard { project, .. }
                | GitCommand::Commit { project, .. },
            ) => Some(*project),
            _ => None,
        };
        let result = self.dispatch_valid(context, command, &mut effects);
        if matches!(result, CommandResult::Ok(_))
            && let Some(project) = file_opened
        {
            effects.push(CommandEffect::FileOpened(project));
        }
        if matches!(result, CommandResult::Ok(_))
            && let Some(project) = git_changed
        {
            effects.push(CommandEffect::GitChanged(project));
        }
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
            for (id, finished) in self.poll_editor_operations() {
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

    pub fn has_pending_editor_operations(&self) -> bool {
        !self.pending_editors.is_empty()
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

    /// Called only by the application owner. Completed worker operations are
    /// committed here after every captured identity still matches live state.
    pub fn poll_editor_operations(&mut self) -> Vec<(u64, DispatchOutcome)> {
        let mut outcomes = Vec::new();
        while let Some(completion) = self
            .editor_io
            .as_ref()
            .and_then(EditorIoQueue::take_completion)
        {
            let Some(operation_id) = self.editor_receipts.remove(&completion.operation) else {
                continue;
            };
            outcomes.push((
                operation_id,
                self.finish_editor_operation(operation_id, completion),
            ));
        }
        outcomes
    }

    /// Reject a completion even when cancellation races a completed worker
    /// syscall. The worker is asked to stop too, but only the owner decides
    /// whether its result may touch the document store.
    pub fn cancel_editor_operation(&mut self, operation_id: u64) -> bool {
        let Some(pending) = self.pending_editors.get_mut(&operation_id) else {
            return false;
        };
        pending.cancelled = true;
        if let Some(queue) = &self.editor_io
            && let Some((&queue_operation, _)) = self
                .editor_receipts
                .iter()
                .find(|(_, receipt)| **receipt == operation_id)
        {
            let _ = queue.cancel(queue_operation);
        }
        true
    }

    /// Stop accepting editor I/O and reject all outstanding completions. Call
    /// `shutdown_editor_operations` during final application shutdown to join
    /// the worker rather than detaching it.
    pub fn cancel_editor_operations(&mut self) {
        for pending in self.pending_editors.values_mut() {
            pending.cancelled = true;
        }
        if let Some(queue) = &self.editor_io {
            queue.shutdown();
        }
    }

    #[allow(dead_code)]
    pub fn shutdown_editor_operations(&mut self) -> std::thread::Result<()> {
        self.cancel_editor_operations();
        let result = match self.editor_io.as_mut() {
            Some(queue) => queue.shutdown_and_join(),
            None => Ok(()),
        };
        self.editor_io = None;
        self.pending_editors.clear();
        self.editor_receipts.clear();
        result
    }

    fn finish_editor_operation(
        &mut self,
        operation_id: u64,
        completion: EditorIoCompletion,
    ) -> DispatchOutcome {
        let Some(pending) = self.pending_editors.remove(&operation_id) else {
            return DispatchOutcome {
                result: err(ErrorCode::RuntimeFailure, "editor operation was cancelled"),
                effects: vec![],
            };
        };
        let rejected = |result| DispatchOutcome {
            result,
            effects: vec![],
        };
        if pending.cancelled {
            return rejected(err(
                ErrorCode::RuntimeFailure,
                "editor operation was cancelled",
            ));
        }
        if completion.project != pending.project
            || completion.root_path != pending.root_path
            || completion.root_identity != pending.root_identity
            || completion.document != pending.document
            || completion.generation != pending.generation
            || completion.kind != pending.kind
        {
            return rejected(err(
                ErrorCode::RuntimeFailure,
                "editor operation identity mismatch",
            ));
        }
        if let CommandContext::Project(scope) = pending.context
            && (scope != pending.project || self.coordinator.window().project(scope).is_none())
        {
            return rejected(err(
                ErrorCode::CrossProjectDenied,
                "project scope has changed",
            ));
        }
        if self.coordinator.window().project(pending.project).is_none() {
            return rejected(err(ErrorCode::ProjectNotFound, "project no longer exists"));
        }
        let root = match self.file_root(pending.context, pending.project) {
            Ok(Some(root)) => root,
            Ok(None) => {
                return rejected(err(
                    ErrorCode::DocumentConflict,
                    "project root changed while editor I/O was pending",
                ));
            }
            Err(issue) => return rejected(CommandResult::Err(issue)),
        };
        let root = match omaterm_context::EditorRoot::open(&root) {
            Ok(root) => root,
            Err(issue) => return rejected(editor_error(issue, ErrorCode::DocumentConflict)),
        };
        if root.canonical_path() != pending.root_path || root.identity() != pending.root_identity {
            return rejected(err(
                ErrorCode::DocumentConflict,
                "project root changed while editor I/O was pending",
            ));
        }
        if let Some(document) = pending.document
            && (self.documents.project_of(document) != Some(pending.project)
                || self.documents.open_root_identity(document) != Some(pending.root_identity)
                || self.documents.relative_path(document).as_deref()
                    != Some(pending.path.as_path())
                || self.documents.generation(document) != Some(pending.generation))
        {
            return rejected(err(
                ErrorCode::DocumentConflict,
                "document changed while editor I/O was pending",
            ));
        }
        match (pending.kind, completion.result) {
            (EditorIoKind::Open, Ok(EditorIoSuccess::Opened(file))) => {
                let document = self.documents.open(
                    pending.project,
                    pending.path,
                    pending.root_path,
                    pending.root_identity,
                    file,
                );
                let Some(info) = self.documents.document_info(document) else {
                    return rejected(err(
                        ErrorCode::RuntimeFailure,
                        "document vanished after open",
                    ));
                };
                tracing::debug!(target: "omaterm::editor", project_id = %pending.project.0, bytes = info.bytes, lines = info.lines, "editor document opened");
                rejected(ok(CommandOutput::EditorOpened(info)))
            }
            (EditorIoKind::Save, Ok(EditorIoSuccess::Saved(saved))) => {
                let document = pending.document.expect("save has document");
                if !saved.is_durable() {
                    tracing::warn!(target: "omaterm::editor", project_id = %pending.project.0, "editor save committed without directory-sync confirmation");
                }
                self.documents.mark_saved(document, saved.revision());
                let Some(info) = self.documents.document_info(document) else {
                    return rejected(err(ErrorCode::DocumentNotOpen, "document is not open"));
                };
                tracing::debug!(target: "omaterm::editor", project_id = %pending.project.0, bytes = info.bytes, "editor document saved");
                rejected(ok(CommandOutput::EditorSaved(info)))
            }
            (EditorIoKind::Revert, Ok(EditorIoSuccess::Reverted(file))) => {
                let document = pending.document.expect("revert has document");
                self.documents.adopt_disk_text(document, file);
                let Some(info) = self.documents.document_info(document) else {
                    return rejected(err(ErrorCode::DocumentNotOpen, "document is not open"));
                };
                rejected(ok(CommandOutput::EditorOpened(info)))
            }
            (_, Ok(_)) => rejected(err(
                ErrorCode::RuntimeFailure,
                "editor operation kind mismatch",
            )),
            (_, Err(EditorIoError::Cancelled)) => rejected(err(
                ErrorCode::RuntimeFailure,
                "editor operation was cancelled",
            )),
            (_, Err(EditorIoError::RootChanged)) => rejected(err(
                ErrorCode::DocumentConflict,
                "project root changed while editor I/O was pending",
            )),
            (EditorIoKind::Save, Err(EditorIoError::Context(issue))) => {
                rejected(editor_error(issue, ErrorCode::DocumentConflict))
            }
            (_, Err(EditorIoError::Context(issue))) => {
                rejected(editor_error(issue, ErrorCode::FileNotFound))
            }
        }
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
            PendingTarget::Restore { project, tab, pane } => {
                let output = self
                    .coordinator
                    .commit_restored_session(project, tab, pane, session)
                    .map(|_| CommandOutput::Unit);
                if output.is_ok() {
                    self.replay_restore_history(pane);
                }
                output
            }
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
                if self
                    .documents
                    .project_documents(project)
                    .iter()
                    .any(|document| self.documents.is_dirty(*document) == Some(true))
                {
                    return err(
                        ErrorCode::DocumentConflict,
                        "project has unsaved documents; save or close them first",
                    );
                }
                match self.coordinator.close_project(project) {
                    Ok(closed) => {
                        close_effects(effects, closed);
                        // Owned editor buffers retire with the project;
                        // dirty text is discarded, never written.
                        self.documents.close_project(project);
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
            OmaCommand::Project(ProjectCommand::SetDirectory { project, directory }) => {
                match self.coordinator.set_project_directory(project, directory) {
                    Ok(()) => {
                        effects.push(CommandEffect::ProjectDirectoryChanged(project));
                        changed(effects, Out::Unit)
                    }
                    Err(e) => coordinator_error(e),
                }
            }
            OmaCommand::Project(ProjectCommand::Root { project }) => {
                let Some(target) = self.coordinator.window().project(project) else {
                    return err(ErrorCode::ProjectNotFound, "project does not exist");
                };
                if matches!(context, CommandContext::Project(scope) if scope != project) {
                    return err(ErrorCode::CrossProjectDenied, "outside project scope");
                }
                let pinned = target.pinned_directory.clone();
                let active_cwd = self.active_shell_cwd(project);
                let resolved =
                    omaterm_context::resolve_root(pinned.as_deref(), active_cwd.as_deref());
                // Redaction contract: IDs, source, and counts only — never
                // raw paths or terminal output (blueprint §45).
                tracing::debug!(
                    target: "omaterm::files",
                    project_id = %project.0,
                    source = resolved.source.as_str(),
                    "project root resolved",
                );
                ok(Out::ProjectRoot(ProjectRootInfo {
                    root: resolved.root,
                    source: resolved.source,
                }))
            }
            OmaCommand::Editor(command) => self.editor_dispatch(context, command),
            OmaCommand::File(FileCommand::List {
                project,
                dir,
                limit,
            }) => {
                let root = match self.file_root(context, project) {
                    Ok(Some(root)) => root,
                    Ok(None) => {
                        return ok(Out::FileList(FileListInfo {
                            entries: Vec::new(),
                            truncated: false,
                        }));
                    }
                    Err(error) => return CommandResult::Err(error),
                };
                let config = omaterm_state::AppConfig::load().unwrap_or_default();
                let capped = limit
                    .unwrap_or(config.resolved_max_results() as usize)
                    .clamp(1, omaterm_core::validation::MAX_FILE_ENTRIES);
                match omaterm_context::list_dir(&root, dir.as_deref(), capped, config.show_hidden())
                {
                    Ok(list) => {
                        tracing::debug!(
                            target: "omaterm::files",
                            project_id = %project.0,
                            entries = list.entries.len(),
                            truncated = list.truncated,
                            "file list served",
                        );
                        ok(Out::FileList(list))
                    }
                    Err(omaterm_context::ContextError::PathOutsideRoot) => {
                        err(ErrorCode::PathOutsideRoot, "path escapes the project root")
                    }
                    Err(omaterm_context::ContextError::Io(error))
                        if error.kind() == std::io::ErrorKind::NotFound =>
                    {
                        err(ErrorCode::FileNotFound, "directory does not exist")
                    }
                    Err(error) => err(ErrorCode::RuntimeFailure, error.to_string()),
                }
            }
            OmaCommand::File(FileCommand::Search {
                project,
                query,
                limit,
            }) => {
                let root = match self.file_root(context, project) {
                    Ok(Some(root)) => root,
                    Ok(None) => {
                        return ok(Out::FileList(FileListInfo {
                            entries: Vec::new(),
                            truncated: false,
                        }));
                    }
                    Err(error) => return CommandResult::Err(error),
                };
                let config = omaterm_state::AppConfig::load().unwrap_or_default();
                let capped = limit
                    .unwrap_or(config.resolved_max_results() as usize)
                    .clamp(1, omaterm_core::validation::MAX_FILE_ENTRIES);
                match omaterm_context::search_files(&root, &query, capped, config.show_hidden()) {
                    Ok(list) => {
                        tracing::debug!(
                            target: "omaterm::search",
                            project_id = %project.0,
                            entries = list.entries.len(),
                            truncated = list.truncated,
                            "file search served",
                        );
                        ok(Out::FileList(list))
                    }
                    Err(omaterm_context::ContextError::PathOutsideRoot) => {
                        err(ErrorCode::PathOutsideRoot, "path escapes the project root")
                    }
                    Err(error) => err(ErrorCode::RuntimeFailure, error.to_string()),
                }
            }
            OmaCommand::File(FileCommand::Open { project, path }) => {
                let root = match self.file_root(context, project) {
                    Ok(Some(root)) => root,
                    Ok(None) => {
                        return err(ErrorCode::NoProjectRoot, "project has no filesystem root");
                    }
                    Err(error) => return CommandResult::Err(error),
                };
                let absolute = match omaterm_context::canonicalize_under_root(&root, &path) {
                    Ok(resolved) => resolved,
                    Err(omaterm_context::ContextError::PathOutsideRoot) => {
                        return err(ErrorCode::PathOutsideRoot, "path escapes the project root");
                    }
                    Err(omaterm_context::ContextError::Io(error))
                        if error.kind() == std::io::ErrorKind::NotFound =>
                    {
                        return err(ErrorCode::FileNotFound, "file does not exist");
                    }
                    Err(error) => return err(ErrorCode::RuntimeFailure, error.to_string()),
                };
                let Some(owner) = self.coordinator.window().project(project) else {
                    return err(ErrorCode::ProjectNotFound, "project does not exist");
                };
                let Some(tab) = owner.selected_tab() else {
                    return err(ErrorCode::NoFocusedPane, "project has no open tab");
                };
                let PaneContent::Terminal(session) = tab
                    .tree
                    .find(tab.focused_pane)
                    .map(|pane| pane.content.clone())
                    .unwrap_or(PaneContent::Empty)
                else {
                    return err(
                        ErrorCode::TerminalRequired,
                        "focused pane has no live terminal",
                    );
                };
                let editor = std::env::var("EDITOR").unwrap_or_default();
                let editor = editor.trim();
                if editor.is_empty() {
                    return err(
                        ErrorCode::EditorNotConfigured,
                        "EDITOR is not set; cannot open files in a terminal editor",
                    );
                }
                let mut argv: Vec<String> = editor.split_whitespace().map(str::to_owned).collect();
                argv.push(absolute.to_string_lossy().into_owned());
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
            OmaCommand::Git(GitCommand::Status { project }) => {
                let root = match self.file_root(context, project) {
                    Ok(Some(root)) => root,
                    Ok(None) => {
                        return ok(Out::GitStatus(GitStatusInfo::empty()));
                    }
                    Err(error) => return CommandResult::Err(error),
                };
                let config = omaterm_state::AppConfig::load().unwrap_or_default();
                let capped = config.resolved_max_results() as usize;
                let capped = capped.clamp(1, omaterm_core::validation::MAX_FILE_ENTRIES);
                match omaterm_context::git_status(&root, capped) {
                    Ok(status) => {
                        // Redaction contract: IDs and counts only — never
                        // branch names, paths, or contents (blueprint §45).
                        tracing::debug!(
                            target: "omaterm::git",
                            project_id = %project.0,
                            staged = status.staged.len(),
                            unstaged = status.unstaged.len(),
                            untracked = status.untracked.len(),
                            truncated = status.truncated,
                            "git status served",
                        );
                        ok(Out::GitStatus(status))
                    }
                    // A pinned non-repo directory is the explicit empty
                    // state, never an error (M14 product contract).
                    Err(omaterm_context::GitError::NotARepo) => {
                        ok(Out::GitStatus(GitStatusInfo::empty()))
                    }
                    Err(error) => git_error(error),
                }
            }
            OmaCommand::Git(GitCommand::Stage { project, paths }) => {
                self.git_mutation(context, project, &paths, GitMutation::Stage)
            }
            OmaCommand::Git(GitCommand::StageHunk {
                project,
                path,
                hunk_id,
            }) => {
                let root = match self.file_root(context, project) {
                    Ok(Some(root)) => root,
                    Ok(None) => {
                        return err(
                            ErrorCode::NotARepo,
                            "project is not inside a git repository",
                        );
                    }
                    Err(error) => return CommandResult::Err(error),
                };
                match omaterm_context::git_stage_hunk(&root, &path, hunk_id) {
                    Ok(()) => {
                        tracing::debug!(target: "omaterm::git", project_id = %project.0, operation = "stage_hunk", "git hunk staged");
                        ok(CommandOutput::Unit)
                    }
                    Err(error) => git_error(error),
                }
            }
            OmaCommand::Git(GitCommand::Unstage { project, paths }) => {
                self.git_mutation(context, project, &paths, GitMutation::Unstage)
            }
            OmaCommand::Git(GitCommand::Discard { project, paths }) => {
                self.git_mutation(context, project, &paths, GitMutation::Discard)
            }
            OmaCommand::Git(GitCommand::Commit { project, message }) => {
                let root = match self.file_root(context, project) {
                    Ok(Some(root)) => root,
                    Ok(None) => {
                        return err(
                            ErrorCode::NotARepo,
                            "project is not inside a git repository",
                        );
                    }
                    Err(error) => return CommandResult::Err(error),
                };
                match omaterm_context::git_commit(&root, &message) {
                    Ok(oid) => {
                        tracing::debug!(
                            target: "omaterm::git",
                            project_id = %project.0,
                            "git commit created",
                        );
                        ok(CommandOutput::GitCommitted { oid })
                    }
                    Err(error) => git_error(error),
                }
            }
            OmaCommand::Diff(DiffCommand::Show {
                project,
                path,
                staged,
                context_lines,
            }) => self.diff_query(
                context,
                project,
                path.as_deref(),
                staged,
                context_lines,
                false,
            ),
            OmaCommand::Diff(DiffCommand::ListFiles { project, staged }) => {
                self.diff_query(context, project, None, staged, 0, true)
            }
            OmaCommand::Process(ProcessCommand::List { project }) => {
                self.process_list(context, project)
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
                                splits: t.tree.split_path(rect.pane).unwrap_or_default(),
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
            OmaCommand::History(HistoryCommand::EnablePersistence) => {
                let manager = match self.history_manager_or_unavailable() {
                    Ok(manager) => manager,
                    Err(error) => return CommandResult::Err(error),
                };
                manager.set_enabled(true);
                // Warm journal buffers from existing archives so the first
                // flush merges instead of discarding the persisted prefix.
                let panes: Vec<String> = self
                    .coordinator
                    .window()
                    .projects
                    .iter()
                    .flat_map(|project| project.tabs.iter())
                    .flat_map(|tab| tab.tree.panes())
                    .map(|pane| opaque_pane(pane.id))
                    .collect();
                if let Some(manager) = self.history.as_mut() {
                    manager.warm_journals(&panes);
                }
                self.sync_all_session_history_flags();
                ok(Out::Unit)
            }
            OmaCommand::History(HistoryCommand::DisablePersistence) => {
                let removed = match self.history_manager_or_unavailable() {
                    Ok(manager) => manager,
                    Err(error) => return CommandResult::Err(error),
                }
                .delete_all_data();
                let removed = match removed {
                    Ok(removed) => removed,
                    Err(error) => return err(ErrorCode::HistoryUnavailable, error),
                };
                if let Some(manager) = self.history.as_mut() {
                    manager.set_enabled(false);
                }
                self.sync_all_session_history_flags();
                ok(Out::HistoryCleared {
                    removed_files: removed,
                })
            }
            OmaCommand::History(HistoryCommand::PausePane { pane }) => {
                if self.find_pane(pane).is_none() {
                    return err(ErrorCode::PaneNotFound, "pane does not exist");
                }
                let manager = match self.history_manager_or_unavailable() {
                    Ok(manager) => manager,
                    Err(error) => return CommandResult::Err(error),
                };
                manager.set_paused(&opaque_pane(pane), true);
                self.sync_session_history_flags(pane);
                ok(Out::Unit)
            }
            OmaCommand::History(HistoryCommand::ResumePane { pane }) => {
                if self.find_pane(pane).is_none() {
                    return err(ErrorCode::PaneNotFound, "pane does not exist");
                }
                let manager = match self.history_manager_or_unavailable() {
                    Ok(manager) => manager,
                    Err(error) => return CommandResult::Err(error),
                };
                manager.set_paused(&opaque_pane(pane), false);
                self.sync_session_history_flags(pane);
                ok(Out::Unit)
            }
            OmaCommand::History(HistoryCommand::ListJournal { pane, limit }) => {
                if self.find_pane(pane).is_none() {
                    return err(ErrorCode::PaneNotFound, "pane does not exist");
                }
                let manager = match self.history_manager_or_unavailable() {
                    Ok(manager) => manager,
                    Err(error) => return CommandResult::Err(error),
                };
                if !manager.config().enabled {
                    return err(
                        ErrorCode::HistoryDisabled,
                        "history persistence is not enabled",
                    );
                }
                match manager.list_journal(&opaque_pane(pane), limit) {
                    Ok(entries) => ok(Out::JournalEntries(
                        entries
                            .into_iter()
                            .map(|entry| JournalEntryInfo {
                                pane: entry.pane,
                                project: entry.project,
                                tab: entry.tab,
                                command: entry.command,
                                shell_dialect: entry.shell_dialect,
                                working_directory: entry.working_directory,
                                started_unix_secs: entry.started_unix_secs,
                                finished_unix_secs: entry.finished_unix_secs,
                                exit_status: entry.exit_status,
                            })
                            .collect(),
                    )),
                    Err(error) => err(ErrorCode::HistoryUnavailable, error),
                }
            }
            OmaCommand::History(HistoryCommand::ClearPane { pane }) => {
                if self.find_pane(pane).is_none() {
                    return err(ErrorCode::PaneNotFound, "pane does not exist");
                }
                let manager = match self.history_manager_or_unavailable() {
                    Ok(manager) => manager,
                    Err(error) => return CommandResult::Err(error),
                };
                match manager.clear_pane(&opaque_pane(pane)) {
                    Ok(removed) => ok(Out::HistoryCleared {
                        removed_files: removed,
                    }),
                    Err(error) => err(ErrorCode::HistoryUnavailable, error),
                }
            }
            OmaCommand::History(HistoryCommand::ClearProject { project }) => {
                let Some(owner) = self.coordinator.window().project(project) else {
                    return err(ErrorCode::ProjectNotFound, "project does not exist");
                };
                let panes: HashSet<PaneId> = owner
                    .tabs
                    .iter()
                    .flat_map(|tab| tab.tree.panes())
                    .map(|pane| pane.id)
                    .collect();
                let opaque: HashSet<String> = panes.iter().map(|pane| opaque_pane(*pane)).collect();
                let manager = match self.history_manager_or_unavailable() {
                    Ok(manager) => manager,
                    Err(error) => return CommandResult::Err(error),
                };
                match manager.clear_panes(&opaque) {
                    Ok(removed) => ok(Out::HistoryCleared {
                        removed_files: removed,
                    }),
                    Err(error) => err(ErrorCode::HistoryUnavailable, error),
                }
            }
            OmaCommand::History(HistoryCommand::ClearWorkspace) => {
                let manager = match self.history_manager_or_unavailable() {
                    Ok(manager) => manager,
                    Err(error) => return CommandResult::Err(error),
                };
                match manager.clear_all() {
                    Ok(removed) => ok(Out::HistoryCleared {
                        removed_files: removed,
                    }),
                    Err(error) => err(ErrorCode::HistoryUnavailable, error),
                }
            }
            OmaCommand::History(HistoryCommand::Status) => {
                let manager = match self.history_manager_or_unavailable() {
                    Ok(manager) => manager,
                    Err(error) => return CommandResult::Err(error),
                };
                let status = manager.status();
                ok(Out::HistoryStatus(HistoryStatusInfo {
                    enabled: status.enabled,
                    key_available: status.key_available,
                    warning: status.warning,
                    archive_files: status.archive_files,
                    archive_bytes: status.archive_bytes,
                    paused_panes: status.paused_panes,
                }))
            }
        }
    }
}

fn opaque_pane(pane: PaneId) -> String {
    HistoryManager::opaque_name(pane.0.as_bytes())
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

/// Map a context git failure to its stable wire code (M14, blueprint
/// §44). `GitFailed` carries bounded git stderr as the message; agents
/// key off the code, never the text.
fn git_error(error: omaterm_context::GitError) -> CommandResult {
    use omaterm_context::GitError as Context;
    match &error {
        Context::NotARepo => err(ErrorCode::NotARepo, error.to_string()),
        Context::GitFailed(_) => err(ErrorCode::GitFailed, error.to_string()),
        Context::GitUnavailable(_) => err(ErrorCode::GitUnavailable, error.to_string()),
        Context::PathOutsideRoot => {
            err(ErrorCode::PathOutsideRoot, "path escapes the project root")
        }
        Context::Timeout => err(ErrorCode::Timeout, error.to_string()),
        Context::Cancelled => err(ErrorCode::Timeout, error.to_string()),
        Context::Io(_) => err(ErrorCode::RuntimeFailure, error.to_string()),
    }
}

/// Map bounded editor I/O failures to stable codes. `missing` selects the
/// caller-appropriate code for a vanished file: `FileNotFound` on open/
/// revert (nothing to load), `DocumentConflict` on save (the open handle
/// is stale — the file was deleted or moved under it).
fn editor_error(error: omaterm_context::EditorError, missing: ErrorCode) -> CommandResult {
    use omaterm_context::EditorError as Context;
    match &error {
        Context::PathOutsideRoot => {
            err(ErrorCode::PathOutsideRoot, "path escapes the project root")
        }
        Context::NotFound => err(missing, error.to_string()),
        Context::NotRegularFile => err(ErrorCode::InvalidRequest, error.to_string()),
        Context::TooLarge => err(ErrorCode::DocumentTooLarge, error.to_string()),
        Context::NotTextFile => err(ErrorCode::NotTextFile, error.to_string()),
        Context::Conflict => err(ErrorCode::DocumentConflict, error.to_string()),
        Context::SecureResolutionUnavailable => err(ErrorCode::RuntimeFailure, error.to_string()),
        Context::Io(_) => err(ErrorCode::RuntimeFailure, error.to_string()),
    }
}

/// Which git mutation a `git_mutation` call performs. Kept separate from
/// `GitCommand` so the shared resolve/map/log path stays in one place.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum GitMutation {
    Stage,
    Unstage,
    Discard,
}

impl GitMutation {
    const fn name(self) -> &'static str {
        match self {
            Self::Stage => "stage",
            Self::Unstage => "unstage",
            Self::Discard => "discard",
        }
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
    fn process_list_is_project_scoped_bounded_and_effect_free() {
        let mut router = router();
        let receipt = router.dispatch_async(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Create {
                name: Some("process-list-test".into()),
                directory: Some(std::env::temp_dir()),
            }),
        );
        let CommandResult::Ok(CommandOutput::Pending { operation_id }) = receipt.result else {
            panic!("expected pending project creation");
        };
        let start = std::time::Instant::now();
        let project = loop {
            if let Some((id, outcome)) = router.poll_launches().into_iter().next()
                && id == operation_id
            {
                let CommandResult::Ok(CommandOutput::ProjectCreated { project, .. }) =
                    outcome.result
                else {
                    panic!("project should commit: {:?}", outcome.result);
                };
                break project;
            }
            assert!(start.elapsed() < std::time::Duration::from_secs(5));
            std::thread::sleep(std::time::Duration::from_millis(2));
        };
        let result = router.dispatch_async(
            CommandContext::LocalUser,
            OmaCommand::Process(ProcessCommand::List { project }),
        );
        assert!(result.effects.is_empty());
        let CommandResult::Ok(CommandOutput::ProcessList(list)) = result.result else {
            panic!("expected process list");
        };
        assert!(list.entries.len() <= MAX_PROCESS_ENTRIES);
        let known_sessions: HashSet<_> = router
            .projects()
            .iter()
            .find(|owner| owner.id == project)
            .into_iter()
            .flat_map(|owner| owner.tabs.iter())
            .flat_map(|tab| tab.tree.panes())
            .filter_map(|pane| match &pane.content {
                PaneContent::Terminal(session) => Some(*session),
                PaneContent::Empty => None,
            })
            .collect();
        assert!(
            list.entries
                .iter()
                .all(|entry| known_sessions.contains(&entry.session))
        );
        let denied = router.dispatch_async(
            CommandContext::Project(ProjectId::new()),
            OmaCommand::Process(ProcessCommand::List { project }),
        );
        assert!(matches!(denied.result, CommandResult::Err(_)));
        assert!(denied.effects.is_empty());
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
        use std::time::Duration;
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
        // The spawn receipt precedes shell exec on some runs. Wait for the
        // child-specific environment, then assert the complete contract.
        let deadline = std::time::Instant::now() + Duration::from_secs(2);
        let environ = loop {
            let environ = std::fs::read(format!("/proc/{pid}/environ")).unwrap();
            if environ
                .split(|byte| *byte == 0)
                .any(|entry| entry.starts_with(b"OMATERM_TOKEN="))
            {
                break environ;
            }
            assert!(
                std::time::Instant::now() < deadline,
                "child never installed its credential environment"
            );
            std::thread::sleep(Duration::from_millis(5));
        };
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
    fn project_set_directory_updates_base_for_future_tabs() {
        let mut router = router();
        let mk_project = |router: &mut CommandRouter| {
            let created = router.dispatch(
                CommandContext::LocalUser,
                OmaCommand::Project(ProjectCommand::Create {
                    name: None,
                    directory: Some(std::env::temp_dir()),
                }),
            );
            let CommandResult::Ok(CommandOutput::ProjectCreated { project, tab, .. }) =
                created.result
            else {
                panic!("project creation");
            };
            (project, tab)
        };
        let (project, tab) = mk_project(&mut router);
        let (other, _) = mk_project(&mut router);
        // Success emits ordered effects and updates the pinned base.
        let target = std::env::temp_dir();
        let updated = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::SetDirectory {
                project,
                directory: target.clone(),
            }),
        );
        assert!(matches!(
            updated.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        assert!(matches!(
            updated.effects.as_slice(),
            [
                CommandEffect::ProjectDirectoryChanged(owner),
                CommandEffect::WorkspaceChanged,
                CommandEffect::PersistenceDirty,
            ] if *owner == project
        ));
        assert_eq!(
            router.window().project(project).unwrap().pinned_directory,
            Some(target)
        );
        // Existing tabs still resolve after the base change.
        assert!(router.window().project(project).unwrap().tab(tab).is_some());
        // Stale project is rejected without effects.
        let stale = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::SetDirectory {
                project: omaterm_core::ProjectId::new(),
                directory: std::env::temp_dir(),
            }),
        );
        assert!(matches!(
            stale.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::ProjectNotFound,
                ..
            })
        ));
        assert!(stale.effects.is_empty());
        // Missing directory fails validation with no effects.
        let missing = router.dispatch_async(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::SetDirectory {
                project,
                directory: std::env::temp_dir().join("omaterm-no-such-dir"),
            }),
        );
        assert!(matches!(
            missing.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::InvalidRequest,
                ..
            })
        ));
        assert!(missing.effects.is_empty());
        // A scoped credential cannot retarget a foreign project.
        let foreign = router.dispatch(
            CommandContext::Project(project),
            OmaCommand::Project(ProjectCommand::SetDirectory {
                project: other,
                directory: std::env::temp_dir(),
            }),
        );
        assert!(matches!(
            foreign.result,
            CommandResult::Err(ref error) if error.code == ErrorCode::CrossProjectDenied
        ));
        // Tidy up the spawned shells; the binary exit would reap regardless.
        for id in [project, other] {
            let _ = router.dispatch(
                CommandContext::LocalUser,
                OmaCommand::Project(ProjectCommand::Delete { project: id }),
            );
        }
    }

    #[test]
    fn project_root_reports_pinned_git_empty_and_scope_without_effects() {
        use omaterm_core::{ProjectRootInfo, RootSource};

        let mut router = router();
        let pin =
            std::env::temp_dir().join(format!("omaterm-m12-router-pin-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&pin);
        std::fs::create_dir_all(&pin).unwrap();
        let created = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Create {
                name: None,
                directory: Some(pin.clone()),
            }),
        );
        let CommandResult::Ok(CommandOutput::ProjectCreated { project, .. }) = created.result
        else {
            panic!("project creation");
        };
        // Pin wins regardless of the live shell CWD; a query emits no effects.
        let rooted = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Root { project }),
        );
        assert_eq!(
            rooted.result,
            CommandResult::Ok(CommandOutput::ProjectRoot(ProjectRootInfo {
                root: Some(pin.clone()),
                source: RootSource::Pinned,
            }))
        );
        assert!(rooted.effects.is_empty());
        // A scoped credential may query its own project through the same arm.
        let scoped = router.dispatch(
            CommandContext::Project(project),
            OmaCommand::Project(ProjectCommand::Root { project }),
        );
        assert!(matches!(
            scoped.result,
            CommandResult::Ok(CommandOutput::ProjectRoot(_))
        ));
        assert!(scoped.effects.is_empty());
        // A deleted pin is the explicit empty state, never a silent re-root.
        let _ = std::fs::remove_dir_all(&pin);
        let gone = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Root { project }),
        );
        assert_eq!(
            gone.result,
            CommandResult::Ok(CommandOutput::ProjectRoot(ProjectRootInfo {
                root: None,
                source: RootSource::Absent,
            }))
        );
        assert!(gone.effects.is_empty());
        // Stale IDs and foreign scopes fail without effects.
        let stale = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Root {
                project: omaterm_core::ProjectId::new(),
            }),
        );
        assert!(matches!(
            stale.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::ProjectNotFound,
                ..
            })
        ));
        assert!(stale.effects.is_empty());
        let foreign = router.dispatch(
            CommandContext::Project(project),
            OmaCommand::Project(ProjectCommand::Root {
                project: omaterm_core::ProjectId::new(),
            }),
        );
        assert!(matches!(
            foreign.result,
            CommandResult::Err(ref error) if error.code == ErrorCode::CrossProjectDenied
        ));
        assert!(foreign.effects.is_empty());
        let _ = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Delete { project }),
        );
    }

    #[test]
    fn file_list_and_search_serve_bounded_results_without_effects() {
        use omaterm_core::{FileCommand, FileListInfo};

        let root = std::env::temp_dir().join(format!("omaterm-m13-router-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir_all(root.join("src")).unwrap();
        std::fs::write(root.join("src").join("main.rs"), b"fn main() {}").unwrap();
        std::fs::write(root.join("README.md"), b"hi").unwrap();

        let mut router = router();
        let created = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Create {
                name: None,
                directory: Some(root.clone()),
            }),
        );
        let CommandResult::Ok(CommandOutput::ProjectCreated { project, .. }) = created.result
        else {
            panic!("project creation");
        };

        let mut list = || {
            router.dispatch(
                CommandContext::LocalUser,
                OmaCommand::File(FileCommand::List {
                    project,
                    dir: None,
                    limit: None,
                }),
            )
        };
        let listed = list();
        let CommandResult::Ok(CommandOutput::FileList(FileListInfo { entries, truncated })) =
            listed.result
        else {
            panic!("file list: {:?}", list().result);
        };
        assert!(!truncated);
        assert!(entries.iter().any(|entry| entry.path.as_os_str() == "src"));
        assert!(listed.effects.is_empty());

        // Subdirectory scoping plus an accurate truncation flag.
        let nested = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::File(FileCommand::List {
                project,
                dir: Some(std::path::PathBuf::from("src")),
                limit: None,
            }),
        );
        let CommandResult::Ok(CommandOutput::FileList(FileListInfo { entries, truncated })) =
            nested.result
        else {
            panic!("nested list");
        };
        assert_eq!(entries.len(), 1);
        assert_eq!(entries[0].path, std::path::PathBuf::from("src/main.rs"));
        assert!(!truncated);
        let bounded = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::File(FileCommand::List {
                project,
                dir: None,
                limit: Some(1),
            }),
        );
        let CommandResult::Ok(CommandOutput::FileList(FileListInfo { entries, truncated })) =
            bounded.result
        else {
            panic!("bounded list");
        };
        assert_eq!(entries.len(), 1);
        assert!(truncated);

        // Escape attempts fail with the stable boundary code (an existing
        // absolute path outside the root canonicalizes and is rejected).
        let evil = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::File(FileCommand::List {
                project,
                dir: Some(std::env::temp_dir()),
                limit: None,
            }),
        );
        assert!(matches!(
            evil.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::PathOutsideRoot,
                ..
            })
        ));
        assert!(evil.effects.is_empty());

        // Search finds the file, never directories, with no effects.
        let found = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::File(FileCommand::Search {
                project,
                query: "main".into(),
                limit: None,
            }),
        );
        let CommandResult::Ok(CommandOutput::FileList(FileListInfo { entries, .. })) = found.result
        else {
            panic!("file search");
        };
        assert_eq!(entries.len(), 1);
        assert_eq!(entries[0].path, std::path::PathBuf::from("src/main.rs"));
        assert!(found.effects.is_empty());

        // Scoped credentials may query their own project but not a foreign one.
        let scoped = router.dispatch(
            CommandContext::Project(project),
            OmaCommand::File(FileCommand::List {
                project,
                dir: None,
                limit: None,
            }),
        );
        assert!(matches!(
            scoped.result,
            CommandResult::Ok(CommandOutput::FileList(_))
        ));
        let foreign = router.dispatch(
            CommandContext::Project(project),
            OmaCommand::File(FileCommand::List {
                project: omaterm_core::ProjectId::new(),
                dir: None,
                limit: None,
            }),
        );
        assert!(matches!(
            foreign.result,
            CommandResult::Err(ref error) if error.code == ErrorCode::CrossProjectDenied
        ));

        let _ = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Delete { project }),
        );
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn file_open_reports_no_root_missing_editor_and_submits_to_ready_bash() {
        use omaterm_core::FileCommand;

        // Serial-gate note: this test mutates SHELL/EDITOR and pumps a live
        // Bash session; the workspace gate runs `--test-threads=1`.
        let prior_shell = std::env::var("SHELL").ok();
        let prior_editor = std::env::var("EDITOR").ok();
        unsafe {
            std::env::set_var("SHELL", "/bin/bash");
            std::env::remove_var("EDITOR");
        }

        let root = std::env::temp_dir().join(format!("omaterm-m13-open-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir_all(&root).unwrap();
        std::fs::write(root.join("notes.txt"), b"hi").unwrap();

        let mut router = router();
        let created = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Create {
                name: None,
                directory: Some(root.clone()),
            }),
        );
        let CommandResult::Ok(CommandOutput::ProjectCreated { project, pane, .. }) = created.result
        else {
            panic!("project creation");
        };

        // Missing $EDITOR fails before any shell interaction.
        let unconfigured = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::File(FileCommand::Open {
                project,
                path: std::path::PathBuf::from("notes.txt"),
            }),
        );
        assert!(matches!(
            unconfigured.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::EditorNotConfigured,
                ..
            })
        ));

        // Missing files and escapes fail with stable codes.
        unsafe {
            std::env::set_var("EDITOR", "true");
        }
        let missing = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::File(FileCommand::Open {
                project,
                path: std::path::PathBuf::from("no-such-file.txt"),
            }),
        );
        assert!(matches!(
            missing.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::FileNotFound,
                ..
            })
        ));
        let evil = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::File(FileCommand::Open {
                project,
                path: std::path::PathBuf::from("../outside.txt"),
            }),
        );
        assert!(matches!(
            evil.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::FileNotFound | ErrorCode::PathOutsideRoot,
                ..
            })
        ));

        // Wait for the Bash prompt hook, then submit `$EDITOR <path>`.
        let session = router
            .coordinator
            .session_id_for_pane(pane)
            .expect("pane session");
        let start = std::time::Instant::now();
        while start.elapsed() < std::time::Duration::from_secs(10) {
            let handle = router.coordinator.registry().get(session).unwrap();
            let mut terminal = handle.lock().unwrap();
            let _ = terminal.pump();
            if terminal.prompt_ready() {
                break;
            }
            std::thread::sleep(std::time::Duration::from_millis(20));
        }
        assert!(
            router
                .coordinator
                .registry()
                .get(session)
                .unwrap()
                .lock()
                .unwrap()
                .prompt_ready(),
            "Bash prompt hook should establish readiness"
        );
        let opened = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::File(FileCommand::Open {
                project,
                path: std::path::PathBuf::from("notes.txt"),
            }),
        );
        assert_eq!(
            opened.result,
            CommandResult::Ok(CommandOutput::RunSubmitted)
        );
        assert!(
            matches!(opened.effects.as_slice(), [CommandEffect::FileOpened(owner)] if *owner == project)
        );

        let _ = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Delete { project }),
        );
        let _ = std::fs::remove_dir_all(&root);
        unsafe {
            match prior_shell {
                Some(value) => std::env::set_var("SHELL", value),
                None => std::env::remove_var("SHELL"),
            }
            match prior_editor {
                Some(value) => std::env::set_var("EDITOR", value),
                None => std::env::remove_var("EDITOR"),
            }
        }
    }

    #[test]
    fn editor_document_lifecycle_open_save_revert_close() {
        use omaterm_core::{DocumentId, EditorCommand, EditorDocumentInfo};

        let root = std::env::temp_dir().join(format!("omaterm-m19-docs-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir_all(&root).unwrap();
        std::fs::write(root.join("notes.md"), "# v1\n").unwrap();

        let mut router = router();
        let created = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Create {
                name: None,
                directory: Some(root.clone()),
            }),
        );
        let CommandResult::Ok(CommandOutput::ProjectCreated { project, .. }) = created.result
        else {
            panic!("project creation");
        };
        let open = |router: &mut CommandRouter| {
            router.dispatch(
                CommandContext::LocalUser,
                OmaCommand::Editor(EditorCommand::Open {
                    project,
                    path: std::path::PathBuf::from("notes.md"),
                }),
            )
        };

        // Open loads bounded text with metadata and no effects.
        let opened = open(&mut router);
        let CommandResult::Ok(CommandOutput::EditorOpened(EditorDocumentInfo {
            document,
            bytes,
            lines,
            ..
        })) = opened.result
        else {
            panic!("editor open: {:?}", opened.result);
        };
        assert_eq!((bytes, lines), ("# v1\n".len(), 2));
        assert!(opened.effects.is_empty());
        assert_eq!(
            router.documents().language(document),
            Some(omaterm_context::EditorLanguage::Markdown)
        );

        // Reopen is idempotent: the live buffer, not a fork.
        let reopened = open(&mut router);
        let CommandResult::Ok(CommandOutput::EditorOpened(info)) = reopened.result else {
            panic!("editor reopen");
        };
        assert_eq!(info.document, document);

        // Clean save is a no-op write with saved metadata.
        let saved = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Editor(EditorCommand::Save { document }),
        );
        assert!(matches!(
            saved.result,
            CommandResult::Ok(CommandOutput::EditorSaved(_))
        ));

        // Buffer edits mark dirty; save persists bytes and clears dirty.
        router
            .documents_mut()
            .apply_edit(document, 4, 0, " wonderful")
            .unwrap();
        assert_eq!(router.documents().is_dirty(document), Some(true));
        for command in [
            OmaCommand::Editor(EditorCommand::Close { document }),
            OmaCommand::Project(ProjectCommand::Delete { project }),
        ] {
            let rejected = router.dispatch(CommandContext::LocalUser, command);
            assert!(matches!(
                rejected.result,
                CommandResult::Err(CommandError {
                    code: ErrorCode::DocumentConflict,
                    ..
                })
            ));
            assert!(rejected.effects.is_empty());
            assert!(router.documents().text(document).is_some());
        }
        let saved = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Editor(EditorCommand::Save { document }),
        );
        assert!(matches!(
            saved.result,
            CommandResult::Ok(CommandOutput::EditorSaved(_))
        ));
        assert_eq!(
            std::fs::read_to_string(root.join("notes.md")).unwrap(),
            "# v1 wonderful\n"
        );
        assert_eq!(router.documents().is_dirty(document), Some(false));

        // External changes conflict instead of clobbering.
        std::fs::write(root.join("notes.md"), "# external\n").unwrap();
        router
            .documents_mut()
            .apply_edit(document, 0, 0, "> ")
            .unwrap();
        let conflicted = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Editor(EditorCommand::Save { document }),
        );
        assert!(matches!(
            conflicted.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::DocumentConflict,
                ..
            })
        ));
        assert_eq!(
            std::fs::read_to_string(root.join("notes.md")).unwrap(),
            "# external\n"
        );

        // Revert reloads disk text and clears dirty + undo.
        let reverted = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Editor(EditorCommand::Revert { document }),
        );
        assert!(matches!(
            reverted.result,
            CommandResult::Ok(CommandOutput::EditorOpened(_))
        ));
        assert_eq!(router.documents().text(document), Some("# external\n"));
        assert_eq!(router.documents().is_dirty(document), Some(false));

        // Unknown documents and foreign scopes fail without effects.
        let stale = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Editor(EditorCommand::Save {
                document: DocumentId::new(),
            }),
        );
        assert!(matches!(
            stale.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::DocumentNotOpen,
                ..
            })
        ));
        assert!(stale.effects.is_empty());
        let created_b = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Create {
                name: None,
                directory: Some(root.clone()),
            }),
        );
        let CommandResult::Ok(CommandOutput::ProjectCreated {
            project: project_b, ..
        }) = created_b.result
        else {
            panic!("second project creation");
        };
        let foreign = router.dispatch(
            CommandContext::Project(project_b),
            OmaCommand::Editor(EditorCommand::Open {
                project,
                path: std::path::PathBuf::from("notes.md"),
            }),
        );
        assert!(matches!(
            foreign.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::CrossProjectDenied,
                ..
            })
        ));
        let _ = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Delete { project: project_b }),
        );

        // Close drops the buffer; project delete retires the rest.
        let closed = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Editor(EditorCommand::Close { document }),
        );
        assert_eq!(closed.result, CommandResult::Ok(CommandOutput::Unit));
        let reopen = open(&mut router);
        let CommandResult::Ok(CommandOutput::EditorOpened(reopened)) = reopen.result else {
            panic!("editor reopen after close");
        };
        assert_ne!(reopened.document, document);
        let deleted = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Delete { project }),
        );
        assert!(matches!(deleted.result, CommandResult::Ok(_)));
        assert!(router.documents().project_of(reopened.document).is_none());
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn editor_io_receipts_complete_or_reject_without_terminal_side_effects() {
        use omaterm_core::EditorCommand;

        fn wait_for_editor(router: &mut CommandRouter, operation_id: u64) -> DispatchOutcome {
            let deadline = std::time::Instant::now() + std::time::Duration::from_secs(5);
            loop {
                for (id, outcome) in router.poll_editor_operations() {
                    if id == operation_id {
                        return outcome;
                    }
                }
                assert!(std::time::Instant::now() < deadline, "editor I/O timed out");
                std::thread::sleep(std::time::Duration::from_millis(2));
            }
        }

        let root = std::env::temp_dir().join(format!("omaterm-editor-io-{}", std::process::id()));
        let replacement = root.with_extension("replacement");
        let _ = std::fs::remove_dir_all(&root);
        let _ = std::fs::remove_dir_all(&replacement);
        std::fs::create_dir_all(&root).unwrap();
        std::fs::create_dir_all(&replacement).unwrap();
        std::fs::write(root.join("notes.txt"), b"disk\n").unwrap();
        std::fs::write(replacement.join("notes.txt"), b"replacement\n").unwrap();

        let mut router = router();
        let created = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Create {
                name: None,
                directory: Some(root.clone()),
            }),
        );
        let CommandResult::Ok(CommandOutput::ProjectCreated { project, .. }) = created.result
        else {
            panic!("project creation");
        };
        let terminal_count = router.registry().list().len();

        // Editor work receives a router receipt but does not enter the
        // terminal launch queue or create a terminal session.
        let receipt = router.dispatch_async(
            CommandContext::LocalUser,
            OmaCommand::Editor(EditorCommand::Open {
                project,
                path: PathBuf::from("notes.txt"),
            }),
        );
        let CommandResult::Ok(CommandOutput::Pending { operation_id }) = receipt.result else {
            panic!("editor open receipt");
        };
        assert!(receipt.effects.is_empty());
        assert!(!router.has_pending_launches());
        assert!(router.has_pending_editor_operations());
        assert_eq!(router.registry().list().len(), terminal_count);
        let completed = wait_for_editor(&mut router, operation_id);
        let CommandResult::Ok(CommandOutput::EditorOpened(info)) = completed.result else {
            panic!("editor open completion");
        };
        assert!(completed.effects.is_empty());
        let document = info.document;
        assert_eq!(router.registry().list().len(), terminal_count);

        // A root retarget after enqueue rejects the result and does not open
        // another buffer or emit an effect.
        let stale_root = router.dispatch_async(
            CommandContext::LocalUser,
            OmaCommand::Editor(EditorCommand::Open {
                project,
                path: PathBuf::from("notes.txt"),
            }),
        );
        let CommandResult::Ok(CommandOutput::Pending { operation_id }) = stale_root.result else {
            panic!("stale-root receipt");
        };
        let moved = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::SetDirectory {
                project,
                directory: replacement.clone(),
            }),
        );
        assert!(matches!(
            moved.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        let stale_root = wait_for_editor(&mut router, operation_id);
        assert!(matches!(stale_root.result, CommandResult::Err(_)));
        assert!(stale_root.effects.is_empty());
        assert_eq!(
            router.documents().project_documents(project),
            vec![document]
        );

        // Restore the original root. A newer in-memory edit invalidates the
        // queued revert, so its disk read cannot overwrite the buffer.
        let _ = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::SetDirectory {
                project,
                directory: root.clone(),
            }),
        );
        let stale_document = router.dispatch_async(
            CommandContext::LocalUser,
            OmaCommand::Editor(EditorCommand::Revert { document }),
        );
        let CommandResult::Ok(CommandOutput::Pending { operation_id }) = stale_document.result
        else {
            panic!("stale-document receipt");
        };
        let closed = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Editor(EditorCommand::Close { document }),
        );
        assert!(matches!(
            closed.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        let stale_document = wait_for_editor(&mut router, operation_id);
        assert!(matches!(stale_document.result, CommandResult::Err(_)));
        assert!(stale_document.effects.is_empty());
        assert!(router.documents().document_info(document).is_none());

        let reopened = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Editor(EditorCommand::Open {
                project,
                path: PathBuf::from("notes.txt"),
            }),
        );
        let CommandResult::Ok(CommandOutput::EditorOpened(info)) = reopened.result else {
            panic!("reopen after stale document");
        };
        let document = info.document;
        std::fs::write(root.join("notes.txt"), b"external\n").unwrap();
        let stale_generation = router.dispatch_async(
            CommandContext::LocalUser,
            OmaCommand::Editor(EditorCommand::Revert { document }),
        );
        let CommandResult::Ok(CommandOutput::Pending { operation_id }) = stale_generation.result
        else {
            panic!("stale-generation receipt");
        };
        router
            .documents_mut()
            .apply_edit(document, 0, 0, "local ")
            .unwrap();
        let stale_generation = wait_for_editor(&mut router, operation_id);
        assert!(matches!(stale_generation.result, CommandResult::Err(_)));
        assert!(stale_generation.effects.is_empty());
        assert_eq!(router.documents().text(document), Some("local disk\n"));

        // Cancellation remains an owner decision even if the worker was
        // already about to publish its completion.
        let cancelled = router.dispatch_async(
            CommandContext::LocalUser,
            OmaCommand::Editor(EditorCommand::Open {
                project,
                path: PathBuf::from("notes.txt"),
            }),
        );
        let CommandResult::Ok(CommandOutput::Pending { operation_id }) = cancelled.result else {
            panic!("cancellation receipt");
        };
        assert!(router.cancel_editor_operation(operation_id));
        let cancelled = wait_for_editor(&mut router, operation_id);
        assert!(matches!(cancelled.result, CommandResult::Err(_)));
        assert!(cancelled.effects.is_empty());
        assert_eq!(router.registry().list().len(), terminal_count);
        assert!(!router.has_pending_launches());

        router.documents_mut().discard_changes(document);
        let _ = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Delete { project }),
        );
        let _ = std::fs::remove_dir_all(&root);
        let _ = std::fs::remove_dir_all(&replacement);
    }

    #[test]
    fn git_status_stage_unstage_and_discard_flow() {
        use omaterm_core::{GitCommand, GitStatusInfo};

        fn git(repo: &std::path::Path, args: &[&str]) {
            let status = std::process::Command::new("git")
                .args(args)
                .current_dir(repo)
                .env("GIT_TERMINAL_PROMPT", "0")
                .env("GIT_CONFIG_NOSYSTEM", "1")
                .stdin(std::process::Stdio::null())
                .stdout(std::process::Stdio::null())
                .stderr(std::process::Stdio::null())
                .status()
                .expect("git must spawn");
            assert!(status.success(), "git {args:?}");
        }

        let root = std::env::temp_dir().join(format!("omaterm-m14-router-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir_all(&root).unwrap();
        git(&root, &["init"]);
        git(&root, &["config", "user.email", "m14@test"]);
        git(&root, &["config", "user.name", "m14"]);
        git(&root, &["config", "commit.gpgsign", "false"]);
        std::fs::write(root.join("a.txt"), b"v1\n").unwrap();
        git(&root, &["add", "-A"]);
        git(&root, &["commit", "-qm", "init"]);

        let mut router = router();
        let created = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Create {
                name: None,
                directory: Some(root.clone()),
            }),
        );
        let CommandResult::Ok(CommandOutput::ProjectCreated { project, .. }) = created.result
        else {
            panic!("project creation");
        };

        // Dirty worktree: status groups it unstaged with no effects.
        std::fs::write(root.join("a.txt"), b"v2\n").unwrap();
        let status = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Git(GitCommand::Status { project }),
        );
        let CommandResult::Ok(CommandOutput::GitStatus(GitStatusInfo {
            staged, unstaged, ..
        })) = status.result
        else {
            panic!("git status");
        };
        assert!(staged.is_empty());
        assert_eq!(unstaged.len(), 1);
        assert_eq!(unstaged[0].path, std::path::PathBuf::from("a.txt"));
        assert!(status.effects.is_empty());

        // Stage → staged; unstage → unstaged again. Mutations emit no
        // persistence effects (git state lives outside the snapshot).
        let paths = vec![std::path::PathBuf::from("a.txt")];
        let staged_outcome = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Git(GitCommand::Stage {
                project,
                paths: paths.clone(),
            }),
        );
        assert_eq!(
            staged_outcome.result,
            CommandResult::Ok(CommandOutput::Unit)
        );
        assert!(
            matches!(staged_outcome.effects.as_slice(), [CommandEffect::GitChanged(owner)] if *owner == project)
        );
        let status = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Git(GitCommand::Status { project }),
        );
        let CommandResult::Ok(CommandOutput::GitStatus(info)) = status.result else {
            panic!("staged status");
        };
        assert_eq!(info.staged.len(), 1);
        assert!(info.unstaged.is_empty());

        let unstage = router.dispatch(
            CommandContext::Project(project),
            OmaCommand::Git(GitCommand::Unstage {
                project,
                paths: paths.clone(),
            }),
        );
        assert_eq!(unstage.result, CommandResult::Ok(CommandOutput::Unit));
        assert!(
            matches!(unstage.effects.as_slice(), [CommandEffect::GitChanged(owner)] if *owner == project)
        );

        // Untracked discard deletes from disk; missing paths are success.
        std::fs::write(root.join("scratch.txt"), b"drop\n").unwrap();
        let discard = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Git(GitCommand::Discard {
                project,
                paths: vec![std::path::PathBuf::from("scratch.txt")],
            }),
        );
        assert_eq!(discard.result, CommandResult::Ok(CommandOutput::Unit));
        assert!(
            matches!(discard.effects.as_slice(), [CommandEffect::GitChanged(owner)] if *owner == project)
        );
        assert!(!root.join("scratch.txt").exists());

        // Traversal is rejected before git runs; empty paths fail
        // validation; stale and foreign projects fail with stable codes.
        let evil = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Git(GitCommand::Discard {
                project,
                paths: vec![std::path::PathBuf::from("../outside.txt")],
            }),
        );
        assert!(matches!(
            evil.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::PathOutsideRoot,
                ..
            })
        ));
        assert!(evil.effects.is_empty());
        let empty = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Git(GitCommand::Stage {
                project,
                paths: vec![],
            }),
        );
        assert!(matches!(
            empty.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::InvalidRequest,
                ..
            })
        ));
        assert!(empty.effects.is_empty());
        let stale = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Git(GitCommand::Status {
                project: omaterm_core::ProjectId::new(),
            }),
        );
        assert!(matches!(
            stale.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::ProjectNotFound,
                ..
            })
        ));
        let foreign = router.dispatch(
            CommandContext::Project(project),
            OmaCommand::Git(GitCommand::Status {
                project: omaterm_core::ProjectId::new(),
            }),
        );
        assert!(matches!(
            foreign.result,
            CommandResult::Err(ref error) if error.code == ErrorCode::CrossProjectDenied
        ));

        let _ = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Delete { project }),
        );
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn git_commit_creates_a_head_commit_from_staged_changes() {
        use omaterm_core::GitCommand;

        fn git(repo: &std::path::Path, args: &[&str]) -> String {
            let output = std::process::Command::new("git")
                .args(args)
                .current_dir(repo)
                .env("GIT_TERMINAL_PROMPT", "0")
                .env("GIT_CONFIG_NOSYSTEM", "1")
                .stdin(std::process::Stdio::null())
                .output()
                .expect("git must spawn");
            assert!(output.status.success(), "git {args:?}");
            String::from_utf8_lossy(&output.stdout).trim().to_owned()
        }

        let root = std::env::temp_dir().join(format!("omaterm-m14-commit-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir_all(&root).unwrap();
        git(&root, &["init"]);
        git(&root, &["config", "user.email", "m14@test"]);
        git(&root, &["config", "user.name", "m14"]);
        git(&root, &["config", "commit.gpgsign", "false"]);
        std::fs::write(root.join("a.txt"), b"v1\n").unwrap();
        git(&root, &["add", "-A"]);
        git(&root, &["commit", "-qm", "init"]);
        std::fs::write(root.join("b.txt"), b"new\n").unwrap();

        let mut router = router();
        let created = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Create {
                name: None,
                directory: Some(root.clone()),
            }),
        );
        let CommandResult::Ok(CommandOutput::ProjectCreated { project, .. }) = created.result
        else {
            panic!("project creation");
        };

        // Empty messages fail validation with no effects; committing with
        // nothing staged fails loudly (nothing to commit) instead of an
        // empty commit.
        let empty = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Git(GitCommand::Commit {
                project,
                message: String::new(),
            }),
        );
        assert!(matches!(
            empty.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::InvalidRequest,
                ..
            })
        ));
        assert!(empty.effects.is_empty());
        let unstaged = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Git(GitCommand::Commit {
                project,
                message: "nothing staged".into(),
            }),
        );
        assert!(matches!(
            unstaged.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::GitFailed,
                ..
            })
        ));

        // Stage, then commit: the oid returns and the tree goes clean.
        let staged = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Git(GitCommand::Stage {
                project,
                paths: vec![std::path::PathBuf::from("b.txt")],
            }),
        );
        assert_eq!(staged.result, CommandResult::Ok(CommandOutput::Unit));
        let committed = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Git(GitCommand::Commit {
                project,
                message: "add b".into(),
            }),
        );
        let CommandResult::Ok(CommandOutput::GitCommitted { oid }) = committed.result else {
            panic!("git commit");
        };
        assert!(!oid.is_empty());
        assert!(
            matches!(committed.effects.as_slice(), [CommandEffect::GitChanged(owner)] if *owner == project)
        );
        assert_eq!(git(&root, &["log", "-1", "--format=%s"]), "add b");
        assert_eq!(git(&root, &["rev-parse", "--short", "HEAD"]), oid);
        let status = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Git(GitCommand::Status { project }),
        );
        let CommandResult::Ok(CommandOutput::GitStatus(info)) = status.result else {
            panic!("post-commit status");
        };
        assert!(info.is_empty());

        // Foreign scope is denied.
        let foreign = router.dispatch(
            CommandContext::Project(project),
            OmaCommand::Git(GitCommand::Commit {
                project: omaterm_core::ProjectId::new(),
                message: "x".into(),
            }),
        );
        assert!(matches!(
            foreign.result,
            CommandResult::Err(ref error) if error.code == ErrorCode::CrossProjectDenied
        ));

        let _ = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Delete { project }),
        );
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn diff_show_and_list_files_serve_bounded_envelopes() {
        use omaterm_core::{DiffCommand, DiffFileStatus};

        fn git(repo: &std::path::Path, args: &[&str]) {
            let status = std::process::Command::new("git")
                .args(args)
                .current_dir(repo)
                .env("GIT_TERMINAL_PROMPT", "0")
                .env("GIT_CONFIG_NOSYSTEM", "1")
                .stdin(std::process::Stdio::null())
                .stdout(std::process::Stdio::null())
                .stderr(std::process::Stdio::null())
                .status()
                .expect("git must spawn");
            assert!(status.success(), "git {args:?}");
        }

        let root = std::env::temp_dir().join(format!("omaterm-m15-router-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir_all(&root).unwrap();
        git(&root, &["init"]);
        git(&root, &["config", "user.email", "m15@test"]);
        git(&root, &["config", "user.name", "m15"]);
        git(&root, &["config", "commit.gpgsign", "false"]);
        std::fs::write(root.join("a.txt"), b"one\ntwo\n").unwrap();
        git(&root, &["add", "-A"]);
        git(&root, &["commit", "-qm", "init"]);
        std::fs::write(root.join("a.txt"), b"one\nTWO\n").unwrap();

        let mut router = router();
        let created = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Create {
                name: None,
                directory: Some(root.clone()),
            }),
        );
        let CommandResult::Ok(CommandOutput::ProjectCreated { project, .. }) = created.result
        else {
            panic!("project creation");
        };

        // Unstaged show carries hunk bodies with no effects; the staged
        // side is empty until something is staged.
        let show = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Diff(DiffCommand::Show {
                project,
                path: None,
                staged: false,
                context_lines: 3,
            }),
        );
        let CommandResult::Ok(CommandOutput::Diff(info)) = show.result else {
            panic!("diff show");
        };
        assert!(!info.staged && !info.truncated);
        assert_eq!(info.files.len(), 1);
        assert_eq!(info.files[0].path, std::path::PathBuf::from("a.txt"));
        assert_eq!(info.files[0].status, DiffFileStatus::Modified);
        assert_eq!(info.files[0].hunks.len(), 1);
        assert!(show.effects.is_empty());

        let staged_empty = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Diff(DiffCommand::Show {
                project,
                path: None,
                staged: true,
                context_lines: 3,
            }),
        );
        let CommandResult::Ok(CommandOutput::Diff(staged_info)) = staged_empty.result else {
            panic!("staged diff show");
        };
        assert!(staged_info.staged && staged_info.files.is_empty());

        // Header-only surface: same file, hunk count without bodies.
        let headers = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Diff(DiffCommand::ListFiles {
                project,
                staged: false,
            }),
        );
        let CommandResult::Ok(CommandOutput::Diff(header_info)) = headers.result else {
            panic!("diff list-files");
        };
        assert_eq!(header_info.files.len(), 1);
        assert!(header_info.files[0].hunks.is_empty());
        assert_eq!(header_info.files[0].hunk_count, 1);
        assert!(headers.effects.is_empty());

        // Path filter, over-context validation, traversal, stale, and
        // foreign scope follow the git matrix exactly.
        let filtered = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Diff(DiffCommand::Show {
                project,
                path: Some(std::path::PathBuf::from("a.txt")),
                staged: false,
                context_lines: 0,
            }),
        );
        let CommandResult::Ok(CommandOutput::Diff(filtered_info)) = filtered.result else {
            panic!("filtered diff show");
        };
        assert_eq!(filtered_info.files.len(), 1);
        let over_context = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Diff(DiffCommand::Show {
                project,
                path: None,
                staged: false,
                context_lines: 11,
            }),
        );
        assert!(matches!(
            over_context.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::InvalidRequest,
                ..
            })
        ));
        assert!(over_context.effects.is_empty());
        let evil = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Diff(DiffCommand::Show {
                project,
                path: Some(std::path::PathBuf::from("../outside.txt")),
                staged: false,
                context_lines: 3,
            }),
        );
        assert!(matches!(
            evil.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::PathOutsideRoot,
                ..
            })
        ));
        let stale = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Diff(DiffCommand::ListFiles {
                project: omaterm_core::ProjectId::new(),
                staged: false,
            }),
        );
        assert!(matches!(
            stale.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::ProjectNotFound,
                ..
            })
        ));
        let foreign = router.dispatch(
            CommandContext::Project(project),
            OmaCommand::Diff(DiffCommand::ListFiles {
                project: omaterm_core::ProjectId::new(),
                staged: false,
            }),
        );
        assert!(matches!(
            foreign.result,
            CommandResult::Err(ref error) if error.code == ErrorCode::CrossProjectDenied
        ));

        let _ = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Delete { project }),
        );
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn diff_on_non_repo_root_is_an_empty_envelope() {
        use omaterm_core::DiffCommand;

        let root = std::env::temp_dir().join(format!("omaterm-m15-norepo-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir_all(&root).unwrap();

        let mut router = router();
        let created = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Create {
                name: None,
                directory: Some(root.clone()),
            }),
        );
        let CommandResult::Ok(CommandOutput::ProjectCreated { project, .. }) = created.result
        else {
            panic!("project creation");
        };
        for command in [
            OmaCommand::Diff(DiffCommand::Show {
                project,
                path: None,
                staged: false,
                context_lines: 3,
            }),
            OmaCommand::Diff(DiffCommand::ListFiles {
                project,
                staged: true,
            }),
        ] {
            let outcome = router.dispatch(CommandContext::LocalUser, command);
            let CommandResult::Ok(CommandOutput::Diff(info)) = outcome.result else {
                panic!("non-repo diff must be an empty envelope");
            };
            assert!(info.files.is_empty() && !info.truncated);
            assert!(outcome.effects.is_empty());
        }
        let _ = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Delete { project }),
        );
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn git_status_on_non_repo_root_is_an_empty_envelope() {
        use omaterm_core::{GitCommand, GitStatusInfo};

        let root = std::env::temp_dir().join(format!("omaterm-m14-norepo-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir_all(&root).unwrap();

        let mut router = router();
        let created = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Create {
                name: None,
                directory: Some(root.clone()),
            }),
        );
        let CommandResult::Ok(CommandOutput::ProjectCreated { project, .. }) = created.result
        else {
            panic!("project creation");
        };
        // Status on a pinned non-repo directory is the explicit empty
        // state, never an error; mutations report `not_a_repo`.
        let status = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Git(GitCommand::Status { project }),
        );
        assert_eq!(
            status.result,
            CommandResult::Ok(CommandOutput::GitStatus(GitStatusInfo::empty()))
        );
        let mutation = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Git(GitCommand::Stage {
                project,
                paths: vec![std::path::PathBuf::from("a.txt")],
            }),
        );
        assert!(matches!(
            mutation.result,
            CommandResult::Err(CommandError {
                code: ErrorCode::NotARepo,
                ..
            })
        ));

        let _ = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Delete { project }),
        );
        let _ = std::fs::remove_dir_all(&root);
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
        // 11F: the split is discoverable from `pane list` on both panes,
        // innermost-first for resize targeting.
        let listed = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Pane(PaneCommand::List { tab: Some(tab) }),
        );
        let CommandResult::Ok(CommandOutput::PaneList(ref items)) = listed.result else {
            panic!("pane list should succeed after split");
        };
        assert_eq!(items.len(), 2);
        for item in items {
            let innermost = item.splits.last().expect("split pane carries its split");
            assert_eq!(innermost.id, split_id);
            assert_eq!(innermost.axis, omaterm_core::SplitAxis::Horizontal);
            assert_eq!(innermost.fraction, 0.5);
        }
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
        let after_resize = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Pane(PaneCommand::List { tab: Some(tab) }),
        );
        let CommandResult::Ok(CommandOutput::PaneList(ref items)) = after_resize.result else {
            panic!("pane list should succeed after resize");
        };
        for item in items {
            assert_eq!(item.splits.last().map(|s| s.fraction), Some(0.6));
        }

        let equalized = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Pane(PaneCommand::Equalize { tab }),
        );
        assert!(matches!(
            equalized.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        // 11F: resize/equalize fractions are observable through the same list.
        let relisted = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Pane(PaneCommand::List { tab: Some(tab) }),
        );
        let CommandResult::Ok(CommandOutput::PaneList(ref items)) = relisted.result else {
            panic!("pane list should succeed after equalize");
        };
        for item in items {
            assert_eq!(item.splits.last().map(|s| s.fraction), Some(0.5));
        }
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
                scrollback_lines: None,
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
                scrollback_lines: None,
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
    fn history_router() -> (CommandRouter, std::path::PathBuf) {
        let dir = std::env::temp_dir().join(format!(
            "omaterm-router-hist-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .map(|elapsed| elapsed.subsec_nanos())
                .unwrap_or(0)
        ));
        std::fs::create_dir_all(&dir).expect("history test dir");
        let store = omaterm_state::HistoryStore::new(
            dir.join("history"),
            omaterm_state::HistoryLimits::default(),
        );
        let manager = crate::history::HistoryManager::new(
            Some(store),
            Box::new(omaterm_state::InMemoryKeyProvider::new()),
        );
        let router = CommandRouter::new(WorkspaceCoordinator::new(std::env::temp_dir()))
            .with_history_manager(manager);
        (router, dir)
    }

    fn create_history_project(router: &mut CommandRouter) -> (ProjectId, TabId, PaneId) {
        let created = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Project(ProjectCommand::Create {
                name: None,
                directory: Some(std::env::temp_dir()),
            }),
        );
        let CommandResult::Ok(CommandOutput::ProjectCreated {
            project, tab, pane, ..
        }) = created.result
        else {
            panic!("project creation failed: {:?}", created.result);
        };
        (project, tab, pane)
    }

    fn session_history_flags(router: &CommandRouter, pane: PaneId) -> (bool, bool) {
        let session = router
            .coordinator
            .session_id_for_pane(pane)
            .expect("pane has a session");
        let handle = router
            .coordinator
            .registry()
            .get(session)
            .expect("session live");
        let terminal = handle.lock().expect("session lock");
        (terminal.history_enabled(), terminal.history_paused())
    }

    #[test]
    fn history_enable_status_and_disable_cycle() {
        let (mut router, dir) = history_router();
        let status = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::History(HistoryCommand::Status),
        );
        let CommandResult::Ok(CommandOutput::HistoryStatus(info)) = status.result else {
            panic!("status must work while disabled: {:?}", status.result);
        };
        assert!(!info.enabled);
        assert!(!info.key_available);

        let missing = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::History(HistoryCommand::ListJournal {
                pane: PaneId::new(),
                limit: 10,
            }),
        );
        assert!(matches!(
            missing.result,
            CommandResult::Err(ref error) if error.code == ErrorCode::PaneNotFound
        ));

        let enabled = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::History(HistoryCommand::EnablePersistence),
        );
        assert!(matches!(
            enabled.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));

        let status = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::History(HistoryCommand::Status),
        );
        let CommandResult::Ok(CommandOutput::HistoryStatus(info)) = status.result else {
            panic!("status after enable: {:?}", status.result);
        };
        assert!(info.enabled);

        let disabled = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::History(HistoryCommand::DisablePersistence),
        );
        assert!(matches!(
            disabled.result,
            CommandResult::Ok(CommandOutput::HistoryCleared { .. })
        ));
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn history_list_pause_resume_and_clear_need_known_pane_and_opt_in() {
        let (mut router, dir) = history_router();
        let (project, _tab, pane) = create_history_project(&mut router);

        let unknown = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::History(HistoryCommand::PausePane {
                pane: PaneId::new(),
            }),
        );
        assert!(matches!(
            unknown.result,
            CommandResult::Err(ref error) if error.code == ErrorCode::PaneNotFound
        ));

        let gated = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::History(HistoryCommand::ListJournal { pane, limit: 10 }),
        );
        assert!(matches!(
            gated.result,
            CommandResult::Err(ref error) if error.code == ErrorCode::HistoryDisabled
        ));

        router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::History(HistoryCommand::EnablePersistence),
        );
        assert_eq!(session_history_flags(&router, pane), (true, false));

        let paused = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::History(HistoryCommand::PausePane { pane }),
        );
        assert!(matches!(
            paused.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        assert_eq!(session_history_flags(&router, pane), (true, true));

        let listed = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::History(HistoryCommand::ListJournal { pane, limit: 10 }),
        );
        assert!(matches!(
            listed.result,
            CommandResult::Ok(CommandOutput::JournalEntries(ref entries)) if entries.is_empty()
        ));

        let resumed = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::History(HistoryCommand::ResumePane { pane }),
        );
        assert!(matches!(
            resumed.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        assert_eq!(session_history_flags(&router, pane), (true, false));

        let cleared = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::History(HistoryCommand::ClearPane { pane }),
        );
        assert!(matches!(
            cleared.result,
            CommandResult::Ok(CommandOutput::HistoryCleared { .. })
        ));

        let missing_project = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::History(HistoryCommand::ClearProject {
                project: ProjectId::new(),
            }),
        );
        assert!(matches!(
            missing_project.result,
            CommandResult::Err(ref error) if error.code == ErrorCode::ProjectNotFound
        ));

        let cleared_project = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::History(HistoryCommand::ClearProject { project }),
        );
        assert!(matches!(
            cleared_project.result,
            CommandResult::Ok(CommandOutput::HistoryCleared { .. })
        ));

        let cleared_all = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::History(HistoryCommand::ClearWorkspace),
        );
        assert!(matches!(
            cleared_all.result,
            CommandResult::Ok(CommandOutput::HistoryCleared { .. })
        ));
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn history_project_scope_denies_global_and_foreign_targets() {
        let (mut router, dir) = history_router();
        let (project_a, _, pane_a) = create_history_project(&mut router);
        let (project_b, _, pane_b) = create_history_project(&mut router);
        let scope_a = CommandContext::Project(project_a);

        for command in [
            OmaCommand::History(HistoryCommand::EnablePersistence),
            OmaCommand::History(HistoryCommand::DisablePersistence),
            OmaCommand::History(HistoryCommand::Status),
            OmaCommand::History(HistoryCommand::ClearWorkspace),
        ] {
            let outcome = router.dispatch(scope_a, command);
            assert!(
                matches!(
                    outcome.result,
                    CommandResult::Err(ref error) if error.code == ErrorCode::PermissionDenied
                ),
                "global history op denied to project scope"
            );
        }

        let own = router.dispatch(
            scope_a,
            OmaCommand::History(HistoryCommand::PausePane { pane: pane_a }),
        );
        assert!(matches!(own.result, CommandResult::Ok(CommandOutput::Unit)));

        let foreign = router.dispatch(
            scope_a,
            OmaCommand::History(HistoryCommand::PausePane { pane: pane_b }),
        );
        assert!(matches!(
            foreign.result,
            CommandResult::Err(ref error) if error.code == ErrorCode::CrossProjectDenied
        ));

        let foreign_project = router.dispatch(
            scope_a,
            OmaCommand::History(HistoryCommand::ClearProject { project: project_b }),
        );
        assert!(matches!(
            foreign_project.result,
            CommandResult::Err(ref error) if error.code == ErrorCode::CrossProjectDenied
        ));
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn history_limit_validation_rejects_out_of_range() {
        let (mut router, dir) = history_router();
        let (_, _, pane) = create_history_project(&mut router);
        for limit in [0, 1001] {
            let outcome = router.dispatch(
                CommandContext::LocalUser,
                OmaCommand::History(HistoryCommand::ListJournal { pane, limit }),
            );
            assert!(
                matches!(
                    outcome.result,
                    CommandResult::Err(ref error) if error.code == ErrorCode::InvalidRequest
                ),
                "limit {limit} rejected"
            );
        }
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn restore_replays_staged_history_before_fresh_output() {
        let (mut router, dir) = history_router();
        // A restored window carries one Empty pane (M6 layout/CWD contract).
        let pane = Pane::empty();
        let pane_id = pane.id;
        let tree = PaneTree::new(pane);
        let tab = Tab::new(tree, pane_id).expect("restored tab");
        let tab_id = tab.id;
        let mut project = Project::new(None, Some(std::env::temp_dir()));
        let project_id = project.id;
        project.add_tab(tab).expect("restored project tab");
        let mut window = WorkspaceWindow::new();
        window.add_project(project).expect("restored window");
        router
            .coordinator
            .restore_window(window)
            .expect("install restored window");

        router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::History(HistoryCommand::EnablePersistence),
        );
        router.stage_restore_history(
            pane_id,
            vec![RecordedEvent::Output(b"pre-restart line\r\n".to_vec())],
        );

        let outcome = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Terminal(TerminalCommand::RestorePane {
                project: project_id,
                tab: tab_id,
                pane: pane_id,
                directory: std::env::temp_dir(),
            }),
        );
        assert!(matches!(
            outcome.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));

        // Restored pixels are visible immediately, before any shell output.
        let session = router
            .coordinator
            .session_id_for_pane(pane_id)
            .expect("restored session");
        let handle = router
            .coordinator
            .registry()
            .get(session)
            .expect("live session");
        let text = handle
            .lock()
            .expect("session lock")
            .read_visible_text(24, 80);
        assert!(
            text.contains("pre-restart line"),
            "replayed scrollback visible"
        );
        assert!(
            !handle
                .lock()
                .expect("session lock")
                .history_snapshot()
                .is_empty(),
            "recorder seeded so the next flush merges instead of discarding"
        );

        // The shell is fresh: it reaches readiness with a live child.
        let start = std::time::Instant::now();
        loop {
            let ready = handle
                .lock()
                .map(|mut terminal| {
                    let _ = terminal.pump();
                    terminal.prompt_ready()
                })
                .unwrap_or(false);
            if ready {
                break;
            }
            assert!(
                start.elapsed() < std::time::Duration::from_secs(10),
                "fresh shell reaches readiness"
            );
            std::thread::sleep(std::time::Duration::from_millis(20));
        }
        assert!(
            handle.lock().expect("session lock").exited().is_none(),
            "restored pane runs a fresh live shell, not a resumed process"
        );
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn restore_seeds_recorder_without_the_trailing_idle_prompt() {
        let (mut router, dir) = history_router();
        let pane = Pane::empty();
        let pane_id = pane.id;
        let tree = PaneTree::new(pane);
        let tab = Tab::new(tree, pane_id).expect("restored tab");
        let tab_id = tab.id;
        let mut project = Project::new(None, Some(std::env::temp_dir()));
        let project_id = project.id;
        project.add_tab(tab).expect("restored project tab");
        let mut window = WorkspaceWindow::new();
        window.add_project(project).expect("restored window");
        router
            .coordinator
            .restore_window(window)
            .expect("install restored window");
        router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::History(HistoryCommand::EnablePersistence),
        );
        router.stage_restore_history(
            pane_id,
            vec![
                RecordedEvent::Output(b"echo hi\r\n".to_vec()),
                RecordedEvent::Output(b"hi\r\n".to_vec()),
                RecordedEvent::Output("❯ ".as_bytes().to_vec()),
            ],
        );
        let outcome = router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::Terminal(TerminalCommand::RestorePane {
                project: project_id,
                tab: tab_id,
                pane: pane_id,
                directory: std::env::temp_dir(),
            }),
        );
        assert!(matches!(
            outcome.result,
            CommandResult::Ok(CommandOutput::Unit)
        ));
        let session = router
            .coordinator
            .session_id_for_pane(pane_id)
            .expect("restored session");
        let snapshot = router
            .coordinator
            .registry()
            .get(session)
            .expect("live session")
            .lock()
            .expect("session lock")
            .history_snapshot();
        let combined: Vec<u8> = snapshot
            .iter()
            .filter_map(|event| match event {
                RecordedEvent::Output(bytes) => Some(bytes.clone()),
                RecordedEvent::Resize { .. } => None,
            })
            .flatten()
            .collect();
        let text = String::from_utf8_lossy(&combined);
        assert!(text.contains("hi\r\n"), "complete lines seeded: {text:?}");
        assert!(
            !text.contains('❯'),
            "stale idle prompt not seeded: {text:?}"
        );
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn restore_replays_history_for_panes_in_inactive_projects() {
        let (mut router, dir) = history_router();
        // Two same-directory projects (`omaterm` / `omaterm 1` report):
        // project A stays selected, project B is never activated, yet both
        // panes must replay their staged scrollback.
        let mut window = WorkspaceWindow::new();
        let mut panes = Vec::new();
        for marker in ["project-a-marker", "project-b-marker"] {
            let pane = Pane::empty();
            let pane_id = pane.id;
            let tree = PaneTree::new(pane);
            let tab = Tab::new(tree, pane_id).expect("restored tab");
            let tab_id = tab.id;
            let mut project = Project::new(None, Some(std::env::temp_dir()));
            let project_id = project.id;
            project.add_tab(tab).expect("restored project tab");
            panes.push((project_id, tab_id, pane_id, marker));
            window.add_project(project).expect("restored window");
        }
        router
            .coordinator
            .restore_window(window)
            .expect("install restored window");
        router
            .coordinator
            .select_project(panes[0].0)
            .expect("select project A");
        router.dispatch(
            CommandContext::LocalUser,
            OmaCommand::History(HistoryCommand::EnablePersistence),
        );
        for (_, _, pane_id, marker) in &panes {
            router.stage_restore_history(
                *pane_id,
                vec![RecordedEvent::Output(format!("{marker}\r\n").into_bytes())],
            );
        }
        for (project_id, tab_id, pane_id, marker) in &panes {
            let outcome = router.dispatch(
                CommandContext::LocalUser,
                OmaCommand::Terminal(TerminalCommand::RestorePane {
                    project: *project_id,
                    tab: *tab_id,
                    pane: *pane_id,
                    directory: std::env::temp_dir(),
                }),
            );
            assert!(
                matches!(outcome.result, CommandResult::Ok(CommandOutput::Unit)),
                "restore commits for {marker}"
            );
        }
        assert!(
            router.pending_history.is_empty(),
            "staged history consumed, not re-staged or dropped"
        );
        for (_, _, pane_id, marker) in &panes {
            let session = router
                .coordinator
                .session_id_for_pane(*pane_id)
                .expect("restored session resolves without activation");
            let handle = router
                .coordinator
                .registry()
                .get(session)
                .expect("live session");
            let text = handle
                .lock()
                .expect("session lock")
                .read_visible_text(24, 80);
            assert!(
                text.contains(marker),
                "inactive project pane replays scrollback"
            );
            assert!(
                !handle
                    .lock()
                    .expect("session lock")
                    .history_snapshot()
                    .is_empty(),
                "inactive project recorder seeded"
            );
        }
        let _ = std::fs::remove_dir_all(&dir);
    }
}
