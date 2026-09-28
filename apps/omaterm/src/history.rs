//! M10 history manager (desktop owner, GPUI-free).
//!
//! Owns history configuration, the encrypted store, the OS key provider,
//! per-pane journal buffers, and a single background worker thread that
//! performs all compression, encryption, and disk I/O. The PTY hot path
//! never blocks on persistence: flush ticks drain bounded snapshots into
//! a bounded job queue, and the worker reports acknowledgements back.
//!
//! Failure policy (no plaintext fallback, ever): when the keyring is
//! unavailable, locked, denied, or lost — or when archives fail to save —
//! terminals keep running in memory, prior archives are left untouched,
//! and a recoverable warning is surfaced for the UI and `history status`.

use std::collections::{HashMap, HashSet};
use std::path::Path;
use std::sync::{Arc, Mutex, mpsc};
use std::time::{Duration, Instant};

use omaterm_state::{
    HistoryConfig, HistoryLoad, HistoryStatus, HistoryStore, JournalBuffer, JournalEntry,
    KeyProvider, KeyProviderError, history_status, load_history_config_migrated, opaque_pane_name,
    save_history_config_toml,
};
use omaterm_terminal::{TerminalSession, history::RecordedEvent};
use zeroize::Zeroize;

/// How long a failed key attempt suppresses keyring retries. The terminal
/// keeps working in memory meanwhile; unlocking the keyring takes effect
/// on the next attempt without a restart.
const KEY_RETRY_INTERVAL: Duration = Duration::from_secs(30);

/// Per-pane context for one flush tick. The desktop resolves workspace
/// identity (project/tab/pane); the manager owns everything else.
pub struct FlushSession<'a> {
    pub pane_opaque: String,
    pub pane_uuid: [u8; 16],
    pub project: Option<String>,
    pub tab: Option<String>,
    pub pane_id: String,
    pub session: &'a mut TerminalSession,
}

/// Owned flush target: workspace identity plus a registry handle. Locking
/// happens inside the manager so both the UI tick and the shutdown thread
/// share one path (poisoned sessions are skipped for that tick).
pub struct FlushTarget {
    pub pane_opaque: String,
    pub pane_uuid: [u8; 16],
    pub project: Option<String>,
    pub tab: Option<String>,
    pub pane_id: String,
    pub handle: Arc<Mutex<TerminalSession>>,
}

/// What the background worker persists for one pane revision.
struct HistoryJob {
    pane: String,
    pane_uuid: [u8; 16],
    revision: u64,
    scrollback: Vec<omaterm_state::HistoryEvent>,
    journal: Vec<JournalEntry>,
    journal_seq: u64,
    recorder_version: u64,
    key: [u8; 32],
}

/// Worker acknowledgement consumed on flush ticks and shutdown.
struct HistoryAck {
    pane: String,
    journal_seq: u64,
    recorder_version: u64,
    ok: bool,
    error: Option<String>,
}

fn save_job(store: &HistoryStore, job: &HistoryJob) -> HistoryAck {
    let mut ok = true;
    let mut error = None;
    if !job.scrollback.is_empty()
        && let Err(save) = store.save_scrollback(
            &job.key,
            &job.pane,
            &job.pane_uuid,
            job.revision,
            &job.scrollback,
        )
    {
        ok = false;
        error = Some(format!("scrollback save failed: {save}"));
    }
    if !job.journal.is_empty()
        && let Err(save) = store.save_journal(
            &job.key,
            &job.pane,
            &job.pane_uuid,
            job.revision,
            &job.journal,
        )
    {
        ok = false;
        let detail = format!("journal save failed: {save}");
        error = Some(match error {
            Some(previous) => format!("{previous}; {detail}"),
            None => detail,
        });
    }
    HistoryAck {
        pane: job.pane.clone(),
        journal_seq: job.journal_seq,
        recorder_version: job.recorder_version,
        ok,
        error,
    }
}

fn worker_main(
    store: HistoryStore,
    jobs: mpsc::Receiver<HistoryJob>,
    acks: mpsc::SyncSender<HistoryAck>,
) {
    while let Ok(mut job) = jobs.recv() {
        let ack = save_job(&store, &job);
        job.key.zeroize();
        if acks.send(ack).is_err() {
            break;
        }
    }
}

/// Outcome of a restore-time scrollback load. Every variant except `Ready`
/// still starts the pane with a fresh shell; warnings are surfaced, never
/// fatal.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum RestoreOutcome {
    Disabled,
    Unavailable(String),
    Missing,
    Ready(Vec<RecordedEvent>),
    Corrupt(String),
}

pub struct HistoryManager {
    store: Option<HistoryStore>,
    provider: Box<dyn KeyProvider>,
    config: HistoryConfig,
    key: Option<[u8; 32]>,
    key_error: Option<(KeyProviderError, Instant)>,
    revisions: HashMap<String, u64>,
    flushed_versions: HashMap<String, u64>,
    flushed_seqs: HashMap<String, u64>,
    inflight: HashMap<String, (u64, u64)>,
    seqs: HashMap<String, u64>,
    journals: HashMap<String, JournalBuffer>,
    warning: Option<String>,
    jobs: Option<mpsc::SyncSender<HistoryJob>>,
    acks: mpsc::Receiver<HistoryAck>,
    worker: Option<std::thread::JoinHandle<()>>,
}

impl HistoryManager {
    pub fn new(store: Option<HistoryStore>, provider: Box<dyn KeyProvider>) -> Self {
        let (jobs_tx, jobs_rx) = mpsc::sync_channel::<HistoryJob>(16);
        let (acks_tx, acks_rx) = mpsc::sync_channel::<HistoryAck>(256);
        // The worker needs a store even when persistence is unavailable; it
        // simply never receives jobs in that case.
        let worker_store = store.clone().unwrap_or_else(|| {
            HistoryStore::new(
                std::env::temp_dir().join("omaterm-history-unused"),
                Default::default(),
            )
        });
        let worker = std::thread::Builder::new()
            .name("omaterm-history-writer".into())
            .spawn(move || worker_main(worker_store, jobs_rx, acks_tx));
        Self {
            store,
            provider,
            config: HistoryConfig::default(),
            key: None,
            key_error: None,
            revisions: HashMap::new(),
            flushed_versions: HashMap::new(),
            flushed_seqs: HashMap::new(),
            inflight: HashMap::new(),
            seqs: HashMap::new(),
            journals: HashMap::new(),
            warning: None,
            jobs: Some(jobs_tx),
            acks: acks_rx,
            worker: worker.ok(),
        }
    }

    #[must_use]
    pub fn config(&self) -> &HistoryConfig {
        &self.config
    }

    #[must_use]
    pub fn warning(&self) -> Option<&str> {
        self.warning.as_deref()
    }

    /// Load configuration through the canonical path with legacy migration.
    /// Missing sources yield defaults; a malformed `config.toml` keeps
    /// defaults and records a warning instead of blocking startup.
    pub fn load_config(&mut self) {
        let toml = omaterm_state::default_config_toml_path();
        let json = omaterm_state::default_history_config_path();
        match (toml, json) {
            (Some(toml), Some(json)) => self.load_config_from(&toml, &json),
            _ => {
                self.warning =
                    Some("history configuration unavailable: no usable config directory".into());
            }
        }
    }

    pub fn load_config_from(&mut self, toml: &Path, json: &Path) {
        match load_history_config_migrated(toml, json) {
            Ok(config) => self.config = config,
            Err(error) => {
                self.warning = Some(format!(
                    "history configuration unreadable ({error}); using defaults"
                ));
            }
        }
    }

    fn save_config(&mut self) {
        let Some(path) = omaterm_state::default_config_toml_path() else {
            self.warning =
                Some("cannot persist history configuration: no usable config directory".into());
            return;
        };
        if let Err(error) = save_history_config_toml(&path, &self.config) {
            self.warning = Some(format!("cannot persist history configuration: {error}"));
        }
    }

    pub fn set_enabled(&mut self, enabled: bool) {
        self.config.enabled = enabled;
        if !enabled {
            self.journals.clear();
            self.seqs.clear();
        }
        self.save_config();
    }

    pub fn set_paused(&mut self, pane_opaque: &str, paused: bool) {
        self.config.set_paused(pane_opaque, paused);
        self.save_config();
    }

    fn ensure_key(&mut self) -> Result<[u8; 32], KeyProviderError> {
        if let Some(key) = self.key {
            return Ok(key);
        }
        if let Some((error, at)) = &self.key_error
            && at.elapsed() < KEY_RETRY_INTERVAL
        {
            return Err(error.clone());
        }
        match self.provider.get_or_create_master_key() {
            Ok(key) => {
                self.key = Some(key);
                self.key_error = None;
                Ok(key)
            }
            Err(error) => {
                self.key_error = Some((error.clone(), Instant::now()));
                Err(error)
            }
        }
    }

    fn map_event(event: &RecordedEvent) -> omaterm_state::HistoryEvent {
        match event {
            RecordedEvent::Output(bytes) => omaterm_state::HistoryEvent::Output(bytes.clone()),
            RecordedEvent::Resize { cols, rows } => omaterm_state::HistoryEvent::Resize {
                cols: *cols,
                rows: *rows,
            },
        }
    }

    /// Drain one session into the journal buffers and, when dirty, queue a
    /// background save. Returns true when a job was queued.
    pub fn flush_one(&mut self, ctx: FlushSession<'_>) -> bool {
        let Some(store) = self.store.clone() else {
            return false;
        };
        if !self.config.enabled || !ctx.session.history_enabled() {
            // Never accumulate records for sessions outside opt-in.
            let _ = ctx.session.drain_lifecycle_records();
            return false;
        }
        if ctx.session.history_paused() || self.config.is_paused(&ctx.pane_opaque) {
            // Paused output is discarded; resume starts fresh.
            let _ = ctx.session.drain_lifecycle_records();
            return false;
        }
        let limits = store.limits();
        for record in ctx.session.drain_lifecycle_records() {
            let buffer = self.journals.entry(ctx.pane_opaque.clone()).or_default();
            buffer.push(
                JournalEntry {
                    pane: ctx.pane_id.clone(),
                    project: ctx.project.clone(),
                    tab: ctx.tab.clone(),
                    command: record.command,
                    shell_dialect: record.shell_dialect,
                    working_directory: record.working_directory.to_string_lossy().into_owned(),
                    started_unix_secs: record.started_unix_secs,
                    finished_unix_secs: record.finished_unix_secs,
                    exit_status: record.exit_status,
                },
                limits,
            );
            *self.seqs.entry(ctx.pane_opaque.clone()).or_default() += 1;
        }
        let version = ctx.session.history_version();
        let snapshot = ctx.session.history_snapshot();
        let seq = self.seqs.get(&ctx.pane_opaque).copied().unwrap_or(0);
        let journal: Vec<JournalEntry> = self
            .journals
            .get(&ctx.pane_opaque)
            .map(|buffer| buffer.entries().to_vec())
            .unwrap_or_default();
        let scrollback_dirty = !snapshot.is_empty()
            && self.flushed_versions.get(&ctx.pane_opaque) != Some(&version)
            && self
                .inflight
                .get(&ctx.pane_opaque)
                .is_none_or(|(v, _)| *v != version);
        let journal_dirty = !journal.is_empty()
            && self.flushed_seqs.get(&ctx.pane_opaque) != Some(&seq)
            && self
                .inflight
                .get(&ctx.pane_opaque)
                .is_none_or(|(_, s)| *s != seq);
        if !scrollback_dirty && !journal_dirty {
            return false;
        }
        let key = match self.ensure_key() {
            Ok(key) => key,
            Err(error) => {
                self.warning = Some(format!(
                    "history unavailable ({error}); terminals keep running in memory"
                ));
                return false;
            }
        };
        let revision = self.revisions.get(&ctx.pane_opaque).copied().unwrap_or(0) + 1;
        let job = HistoryJob {
            pane: ctx.pane_opaque.clone(),
            pane_uuid: ctx.pane_uuid,
            revision,
            scrollback: snapshot.iter().map(Self::map_event).collect(),
            journal,
            journal_seq: seq,
            recorder_version: version,
            key,
        };
        match self.jobs.as_ref() {
            Some(jobs) => match jobs.try_send(job) {
                Ok(()) => {
                    self.revisions.insert(ctx.pane_opaque.clone(), revision);
                    self.inflight
                        .insert(ctx.pane_opaque.clone(), (version, seq));
                    true
                }
                Err(mpsc::TrySendError::Full(_)) => false,
                Err(mpsc::TrySendError::Disconnected(_)) => {
                    self.warning = Some(
                        "history writer is unavailable; terminals keep running in memory".into(),
                    );
                    false
                }
            },
            None => false,
        }
    }

    /// Apply worker acknowledgements. Bounded per call so ticks stay cheap.
    pub fn poll_results(&mut self) {
        for _ in 0..256 {
            match self.acks.try_recv() {
                Ok(ack) => self.apply_ack(ack),
                Err(mpsc::TryRecvError::Empty) => break,
                Err(mpsc::TryRecvError::Disconnected) => {
                    self.warning =
                        Some("history writer stopped; terminals keep running in memory".into());
                    break;
                }
            }
        }
    }

    fn apply_ack(&mut self, ack: HistoryAck) {
        let current = self.inflight.get(&ack.pane).copied();
        if current.is_some_and(|(v, s)| v == ack.recorder_version && s == ack.journal_seq) {
            self.inflight.remove(&ack.pane);
        }
        if ack.ok {
            self.flushed_versions
                .insert(ack.pane.clone(), ack.recorder_version);
            self.flushed_seqs.insert(ack.pane.clone(), ack.journal_seq);
        } else if let Some(error) = ack.error {
            self.warning = Some(format!("history save failed ({error}); will retry"));
        }
    }

    /// Flush owned registry handles: sync session flags from configuration,
    /// drain recorders and journals, and queue dirty panes. Returns the
    /// number of jobs queued.
    pub fn flush_targets(&mut self, targets: Vec<FlushTarget>) -> usize {
        let mut queued = 0;
        for target in targets {
            let Ok(mut session) = target.handle.lock() else {
                continue;
            };
            session.set_history_enabled(self.config.enabled);
            session.set_history_paused(self.config.is_paused(&target.pane_opaque));
            let ctx = FlushSession {
                pane_opaque: target.pane_opaque,
                pane_uuid: target.pane_uuid,
                project: target.project,
                tab: target.tab,
                pane_id: target.pane_id,
                session: &mut session,
            };
            if self.flush_one(ctx) {
                queued += 1;
            }
        }
        self.poll_results();
        queued
    }

    /// Flush owned handles and wait for the worker up to `timeout`.
    /// Returns true when all queued saves were acknowledged.
    pub fn shutdown_flush_targets(&mut self, targets: Vec<FlushTarget>, timeout: Duration) -> bool {
        self.flush_targets(targets);
        let deadline = Instant::now() + timeout;
        loop {
            self.poll_results();
            if self.inflight.is_empty() {
                return true;
            }
            if Instant::now() >= deadline {
                return false;
            }
            std::thread::sleep(Duration::from_millis(20));
        }
    }

    /// Load verified scrollback for restore-before-spawn. Warms the
    /// revision counter so the next save supersedes the restored archive.
    pub fn restore_scrollback(&mut self, pane_opaque: &str) -> RestoreOutcome {
        if !self.config.enabled {
            return RestoreOutcome::Disabled;
        }
        let Some(store) = self.store.clone() else {
            return RestoreOutcome::Unavailable("no usable state directory".into());
        };
        let key = match self.ensure_key() {
            Ok(key) => key,
            Err(error) => return RestoreOutcome::Unavailable(error.to_string()),
        };
        match store.load_scrollback(&key, pane_opaque) {
            Ok(HistoryLoad::Missing) => RestoreOutcome::Missing,
            Ok(HistoryLoad::Scrollback(events, revision)) => {
                self.revisions
                    .insert(pane_opaque.to_string(), revision.max(1));
                RestoreOutcome::Ready(
                    events
                        .into_iter()
                        .map(|event| match event {
                            omaterm_state::HistoryEvent::Output(bytes) => {
                                RecordedEvent::Output(bytes)
                            }
                            omaterm_state::HistoryEvent::Resize { cols, rows } => {
                                RecordedEvent::Resize { cols, rows }
                            }
                        })
                        .collect(),
                )
            }
            Ok(HistoryLoad::Corrupt { error, .. }) => RestoreOutcome::Corrupt(format!(
                "saved history failed verification ({error}); starting fresh"
            )),
            Ok(HistoryLoad::Journal(_, _)) => RestoreOutcome::Corrupt(
                "unexpected journal in scrollback slot; starting fresh".into(),
            ),
            Err(error) => {
                RestoreOutcome::Corrupt(format!("history load failed ({error}); starting fresh"))
            }
        }
    }

    /// Warm one pane's in-memory journal from its newest archive. Required
    /// at startup and on opt-in: without it, the first post-restart flush
    /// would persist only new entries and discard the archived prefix.
    /// Idempotent per pane; failures warn without blocking the terminal.
    pub fn warm_journal(&mut self, pane_opaque: &str) {
        if self.journals.contains_key(pane_opaque) || !self.config.enabled {
            return;
        }
        let Some(store) = self.store.clone() else {
            return;
        };
        let key = match self.ensure_key() {
            Ok(key) => key,
            Err(error) => {
                self.warning = Some(format!(
                    "history unavailable ({error}); terminals keep running in memory"
                ));
                return;
            }
        };
        match store.load_journal(&key, pane_opaque) {
            Ok(HistoryLoad::Journal(entries, _)) => {
                let limits = store.limits();
                let mut seq = 0u64;
                let buffer = self.journals.entry(pane_opaque.to_string()).or_default();
                for entry in entries {
                    buffer.push(entry, limits);
                    seq += 1;
                }
                // The warmed prefix is already persisted; only newer
                // appends are dirty.
                self.seqs.insert(pane_opaque.to_string(), seq);
                self.flushed_seqs.insert(pane_opaque.to_string(), seq);
            }
            Ok(HistoryLoad::Missing) => {}
            Ok(HistoryLoad::Corrupt { error, .. }) => {
                self.warning = Some(format!(
                    "saved journal failed verification ({error}); starting fresh"
                ));
            }
            Ok(HistoryLoad::Scrollback(_, _)) => {
                self.warning = Some("unexpected scrollback in journal slot".into());
            }
            Err(error) => {
                self.warning = Some(format!("journal warm failed ({error})"));
            }
        }
    }

    pub fn warm_journals(&mut self, panes: &[String]) {
        for pane in panes {
            self.warm_journal(pane);
        }
    }

    /// Bounded journal listing: in-memory buffers first, newest archive as
    /// fallback (post-restart before any drain).
    pub fn list_journal(
        &mut self,
        pane_opaque: &str,
        limit: usize,
    ) -> Result<Vec<JournalEntry>, String> {
        let limit = limit.clamp(1, 1000);
        if let Some(buffer) = self.journals.get(pane_opaque)
            && !buffer.is_empty()
        {
            return Ok(buffer.list(limit).to_vec());
        }
        let Some(store) = self.store.clone() else {
            return Err("history is unavailable: no usable state directory".into());
        };
        let key = self.ensure_key().map_err(|error| error.to_string())?;
        match store.load_journal(&key, pane_opaque) {
            Ok(HistoryLoad::Journal(entries, _)) => Ok(entries
                .into_iter()
                .rev()
                .take(limit)
                .collect::<Vec<_>>()
                .into_iter()
                .rev()
                .collect()),
            Ok(HistoryLoad::Missing) => Ok(Vec::new()),
            Ok(HistoryLoad::Corrupt { error, .. }) => {
                Err(format!("journal failed verification: {error}"))
            }
            Ok(HistoryLoad::Scrollback(_, _)) => {
                Err("unexpected scrollback in journal slot".into())
            }
            Err(error) => Err(error.to_string()),
        }
    }

    /// User-visible status: safe metadata only, never keys or plaintext.
    pub fn status(&self) -> HistoryStatus {
        match &self.store {
            Some(store) => history_status(
                &self.config,
                store,
                self.key.is_some(),
                self.warning.clone(),
            ),
            None => HistoryStatus {
                enabled: false,
                key_available: false,
                warning: Some("history unavailable: no usable state directory".into()),
                archive_files: 0,
                archive_bytes: 0,
                paused_panes: self.config.paused_panes.len(),
            },
        }
    }

    fn forget_pane(&mut self, pane_opaque: &str) {
        self.journals.remove(pane_opaque);
        self.revisions.remove(pane_opaque);
        self.flushed_versions.remove(pane_opaque);
        self.flushed_seqs.remove(pane_opaque);
        self.inflight.remove(pane_opaque);
        self.seqs.remove(pane_opaque);
    }

    pub fn clear_pane(&mut self, pane_opaque: &str) -> Result<usize, String> {
        let removed = match &self.store {
            Some(store) => store
                .clear_pane(pane_opaque)
                .map_err(|error| error.to_string())?,
            None => 0,
        };
        self.forget_pane(pane_opaque);
        Ok(removed)
    }

    pub fn clear_panes(&mut self, panes: &HashSet<String>) -> Result<usize, String> {
        let removed = match &self.store {
            Some(store) => store
                .clear_panes(panes)
                .map_err(|error| error.to_string())?,
            None => 0,
        };
        for pane in panes {
            self.forget_pane(pane);
        }
        Ok(removed)
    }

    /// Delete every archive and journal without rotating the key. Used by
    /// disable (recording stops; data goes); clear-all rotates instead.
    pub fn delete_all_data(&mut self) -> Result<usize, String> {
        let removed = match &self.store {
            Some(store) => store.clear_all().map_err(|error| error.to_string())?,
            None => 0,
        };
        self.journals.clear();
        self.revisions.clear();
        self.flushed_versions.clear();
        self.flushed_seqs.clear();
        self.inflight.clear();
        self.seqs.clear();
        Ok(removed)
    }

    /// Delete every archive and journal, then rotate the master key so
    /// undeleted filesystem blocks cannot be decrypted with the prior key.
    pub fn clear_all(&mut self) -> Result<usize, String> {
        let removed = match &self.store {
            Some(store) => store.clear_all().map_err(|error| error.to_string())?,
            None => 0,
        };
        self.journals.clear();
        self.revisions.clear();
        self.flushed_versions.clear();
        self.flushed_seqs.clear();
        self.inflight.clear();
        self.seqs.clear();
        match self.provider.rotate_master_key() {
            Ok(key) => {
                self.key = Some(key);
                self.key_error = None;
            }
            Err(error) => {
                self.key = None;
                self.key_error = Some((error.clone(), Instant::now()));
                self.warning = Some(format!("history cleared but key rotation failed ({error})"));
            }
        }
        Ok(removed)
    }

    /// Opaque archive name for a workspace pane id.
    #[must_use]
    pub fn opaque_name(pane_uuid: &[u8; 16]) -> String {
        opaque_pane_name(pane_uuid)
    }
}

impl Drop for HistoryManager {
    fn drop(&mut self) {
        // Dropping the last job sender disconnects the worker after it
        // finishes queued saves; the join is immediate because the worker
        // only blocks on receive.
        drop(self.jobs.take());
        if let Some(worker) = self.worker.take() {
            let _ = worker.join();
        }
        if let Some(key) = self.key.as_mut() {
            key.zeroize();
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use omaterm_state::{HistoryLimits, InMemoryKeyProvider, KeyProviderError};

    fn test_manager() -> (HistoryManager, tempfile_dir::TempDir) {
        let dir = tempfile_dir::TempDir::new();
        let store = HistoryStore::new(
            dir.path().join("history"),
            HistoryLimits {
                max_workspace_bytes: 16 * 1024 * 1024,
                ..HistoryLimits::default()
            },
        );
        let provider: Box<dyn KeyProvider> = Box::new(InMemoryKeyProvider::new());
        (HistoryManager::new(Some(store), provider), dir)
    }

    fn test_session() -> TerminalSession {
        let mut session = TerminalSession::new(std::env::temp_dir(), Some("/bin/bash"), 80, 24)
            .expect("spawn bash");
        session.set_history_enabled(true);
        session
    }

    mod tempfile_dir {
        use std::path::{Path, PathBuf};
        static COUNTER: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);
        pub struct TempDir(PathBuf);
        impl TempDir {
            pub fn new() -> Self {
                let unique = COUNTER.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
                let path = std::env::temp_dir().join(format!(
                    "omaterm-history-test-{}-{unique}",
                    std::process::id()
                ));
                std::fs::create_dir_all(&path).expect("temp dir");
                Self(path)
            }
            pub fn path(&self) -> &Path {
                &self.0
            }
        }
        impl Drop for TempDir {
            fn drop(&mut self) {
                let _ = std::fs::remove_dir_all(&self.0);
            }
        }
    }

    #[test]
    fn disabled_manager_saves_nothing() {
        let (mut manager, _dir) = test_manager();
        let mut session = test_session();
        session.advance_output(b"hello\r\n");
        let ctx = FlushSession {
            pane_opaque: "a".repeat(32),
            pane_uuid: [1; 16],
            project: None,
            tab: None,
            pane_id: "pane".into(),
            session: &mut session,
        };
        assert!(!manager.flush_one(ctx));
        assert_eq!(manager.status().archive_files, 0);
    }

    #[test]
    fn flush_persists_scrollback_and_journal_for_restore() {
        let (mut manager, _dir) = test_manager();
        manager.set_enabled(true);
        let pane = "b".repeat(32);
        let mut session = test_session();
        session.advance_output(b"hello history\r\n");
        // Synthesize one authenticated-style record through the real path:
        // (lifecycle emission itself is covered by terminal tests).
        {
            let ctx = FlushSession {
                pane_opaque: pane.clone(),
                pane_uuid: [2; 16],
                project: Some("proj".into()),
                tab: Some("tab".into()),
                pane_id: "pane".into(),
                session: &mut session,
            };
            assert!(manager.flush_one(ctx));
        }
        assert!(manager.shutdown_flush_targets(Vec::new(), Duration::from_secs(10)));
        let status = manager.status();
        assert_eq!(status.archive_files, 1, "one scrollback archive");
        assert!(status.archive_bytes > 0);

        // A fresh manager against the same store restores the bytes.
        let key = manager.key.expect("key");
        let store_path = _dir.path().join("history");
        drop(manager);
        let mut manager2 = HistoryManager::new(
            Some(HistoryStore::new(
                store_path,
                HistoryLimits {
                    max_workspace_bytes: 16 * 1024 * 1024,
                    ..HistoryLimits::default()
                },
            )),
            Box::new(InMemoryKeyProvider::new()),
        );
        manager2.key = Some(key);
        manager2.config.enabled = true;
        match manager2.restore_scrollback(&pane) {
            RestoreOutcome::Ready(events) => {
                let text: Vec<u8> = events
                    .iter()
                    .filter_map(|event| match event {
                        RecordedEvent::Output(bytes) => Some(bytes.clone()),
                        RecordedEvent::Resize { .. } => None,
                    })
                    .flatten()
                    .collect();
                assert!(String::from_utf8_lossy(&text).contains("hello history"));
            }
            other => panic!("expected restored events, got {other:?}"),
        }
    }

    #[test]
    fn key_failure_saves_nothing_and_warns_without_blocking() {
        let (mut manager, _dir) = test_manager();
        manager.set_enabled(true);
        // Replace the provider with one that refuses key access.
        manager.provider = Box::new(FailingProvider);
        let pane = "c".repeat(32);
        let mut session = test_session();
        session.advance_output(b"output\r\n");
        let ctx = FlushSession {
            pane_opaque: pane.clone(),
            pane_uuid: [3; 16],
            project: None,
            tab: None,
            pane_id: "pane".into(),
            session: &mut session,
        };
        assert!(!manager.flush_one(ctx));
        assert!(manager.warning().is_some(), "recoverable warning surfaced");
        assert_eq!(manager.status().archive_files, 0, "no plaintext fallback");
        // The terminal itself is unaffected and keeps its record in memory.
        assert!(!session.history_snapshot().is_empty());
    }

    struct FailingProvider;
    impl KeyProvider for FailingProvider {
        fn get_or_create_master_key(&mut self) -> Result<[u8; 32], KeyProviderError> {
            Err(KeyProviderError::Locked("test keyring is locked".into()))
        }
        fn master_key(&self) -> Option<[u8; 32]> {
            None
        }
        fn rotate_master_key(&mut self) -> Result<[u8; 32], KeyProviderError> {
            Err(KeyProviderError::Locked("test keyring is locked".into()))
        }
        fn remove_master_key(&mut self) -> Result<(), KeyProviderError> {
            Err(KeyProviderError::Locked("test keyring is locked".into()))
        }
    }

    #[test]
    fn rapid_flushes_supersede_and_newest_wins() {
        let (mut manager, _dir) = test_manager();
        manager.set_enabled(true);
        let pane = "f".repeat(32);
        let uuid = [6u8; 16];
        let mut session = test_session();
        let send = |manager: &mut HistoryManager, session: &mut TerminalSession, line: &str| {
            session.advance_output(format!("{line}\r\n").as_bytes());
            let ctx = FlushSession {
                pane_opaque: pane.clone(),
                pane_uuid: uuid,
                project: None,
                tab: None,
                pane_id: "pane".into(),
                session,
            };
            assert!(manager.flush_one(ctx));
        };
        send(&mut manager, &mut session, "first");
        send(&mut manager, &mut session, "second");
        send(&mut manager, &mut session, "third");
        assert!(manager.shutdown_flush_targets(Vec::new(), Duration::from_secs(10)));
        // One live revision plus no stale predecessors.
        let inventory = manager
            .store
            .as_ref()
            .expect("store")
            .inventory()
            .expect("inventory");
        assert_eq!(
            inventory.len(),
            1,
            "superseded revisions removed: {inventory:?}"
        );
        match manager.restore_scrollback(&pane) {
            RestoreOutcome::Ready(events) => {
                let text: Vec<u8> = events
                    .iter()
                    .filter_map(|event| match event {
                        RecordedEvent::Output(bytes) => Some(bytes.clone()),
                        RecordedEvent::Resize { .. } => None,
                    })
                    .flatten()
                    .collect();
                let text = String::from_utf8_lossy(&text);
                assert!(text.contains("third"), "newest retained: {text:?}");
            }
            other => panic!("expected restored events, got {other:?}"),
        }
    }

    fn journal_entry(command: &str, started: u64) -> JournalEntry {
        JournalEntry {
            pane: "pane".into(),
            project: None,
            tab: None,
            command: command.into(),
            shell_dialect: "bash".into(),
            working_directory: "/tmp".into(),
            started_unix_secs: started,
            finished_unix_secs: Some(started + 1),
            exit_status: Some(0),
        }
    }

    #[test]
    fn warm_journal_preserves_archived_prefix_across_flush() {
        let (mut manager, _dir) = test_manager();
        manager.set_enabled(true);
        let pane = "7".repeat(32);
        let uuid = [7u8; 16];
        // Seed an archive prefix directly, as a previous run would have.
        let key = manager.ensure_key().expect("key");
        let store = manager.store.clone().expect("store");
        store
            .save_journal(
                &key,
                &pane,
                &uuid,
                1,
                &[journal_entry("old-1", 10), journal_entry("old-2", 11)],
            )
            .expect("seed archive");
        manager.warm_journal(&pane);
        manager.warm_journal(&pane);
        assert_eq!(manager.list_journal(&pane, 100).expect("list").len(), 2);

        // A new record arrives through a live shell; flush until it lands.
        let mut session = test_session();
        session.write_input(b"true\n").expect("write");
        let start = Instant::now();
        while start.elapsed() < Duration::from_secs(10) {
            let _ = session.pump();
            let ctx = FlushSession {
                pane_opaque: pane.clone(),
                pane_uuid: uuid,
                project: None,
                tab: None,
                pane_id: "pane".into(),
                session: &mut session,
            };
            manager.flush_one(ctx);
            if manager.list_journal(&pane, 100).expect("list").len() >= 3 {
                break;
            }
            std::thread::sleep(Duration::from_millis(20));
        }
        assert!(manager.shutdown_flush_targets(Vec::new(), Duration::from_secs(10)));
        let listed = manager.list_journal(&pane, 100).expect("merged list");
        assert_eq!(listed.len(), 3, "prefix plus new record: {listed:?}");
        assert_eq!(listed[0].command, "old-1");
        assert_eq!(listed[1].command, "old-2");
        assert_eq!(listed[2].command, "true");
    }

    #[test]
    fn clear_scopes_remove_data_and_clear_all_rotates_key() {
        let (mut manager, _dir) = test_manager();
        manager.set_enabled(true);
        let mut session = test_session();
        session.advance_output(b"data\r\n");
        for (pane, uuid) in [("d".repeat(32), [4u8; 16]), ("e".repeat(32), [5u8; 16])] {
            let ctx = FlushSession {
                pane_opaque: pane,
                pane_uuid: uuid,
                project: None,
                tab: None,
                pane_id: "pane".into(),
                session: &mut session,
            };
            assert!(manager.flush_one(ctx));
        }
        assert!(manager.shutdown_flush_targets(Vec::new(), Duration::from_secs(10)));
        assert_eq!(manager.status().archive_files, 2);
        let before = manager.key;
        manager.clear_pane(&"d".repeat(32)).expect("clear pane");
        assert_eq!(manager.status().archive_files, 1);
        manager.clear_all().expect("clear all");
        assert_eq!(manager.status().archive_files, 0);
        assert_ne!(manager.key, before, "clear-all rotates the key");
    }
}
