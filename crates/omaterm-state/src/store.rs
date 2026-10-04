use std::fs::{self, OpenOptions};
use std::io::{Read, Write};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Condvar, Mutex, mpsc};

use crate::{SnapshotError, SnapshotLimits, WorkspaceSnapshot, migration};

pub fn default_snapshot_path() -> Option<PathBuf> {
    let base = std::env::var_os("XDG_STATE_HOME")
        .map(PathBuf::from)
        .filter(|p| p.is_absolute())
        .or_else(|| {
            std::env::var_os("HOME")
                .map(PathBuf::from)
                .map(|p| p.join(".local/state"))
        })?;
    Some(base.join("omaterm/workspace-v1.json"))
}

#[derive(Debug, Clone)]
pub struct SnapshotStore {
    path: PathBuf,
    limits: SnapshotLimits,
}

struct WriterRequest {
    snapshot: WorkspaceSnapshot,
    destination: SnapshotDestination,
    revision: u64,
    completion: Option<mpsc::Sender<Result<u64, String>>>,
}

#[derive(Default)]
struct WriterState {
    pending: Option<WriterRequest>,
    closed: bool,
    completed_revision: u64,
}

/// A single background writer with one-slot latest-snapshot coalescing.
/// Requests are revision ordered; stale revisions cannot replace newer work.
pub struct SnapshotWriter {
    shared: Arc<(Mutex<WriterState>, Condvar)>,
    clients: Arc<AtomicUsize>,
}

impl Clone for SnapshotWriter {
    fn clone(&self) -> Self {
        self.clients.fetch_add(1, Ordering::Relaxed);
        Self {
            shared: self.shared.clone(),
            clients: self.clients.clone(),
        }
    }
}

impl SnapshotWriter {
    pub fn new(store: SnapshotStore) -> Self {
        let shared = Arc::new((Mutex::new(WriterState::default()), Condvar::new()));
        let worker_shared = shared.clone();
        std::thread::spawn(move || {
            loop {
                let request = {
                    let (lock, wake) = &*worker_shared;
                    let mut state = lock
                        .lock()
                        .unwrap_or_else(std::sync::PoisonError::into_inner);
                    while state.pending.is_none() && !state.closed {
                        state = wake
                            .wait(state)
                            .unwrap_or_else(std::sync::PoisonError::into_inner);
                    }
                    match state.pending.take() {
                        Some(request) => request,
                        None if state.closed => break,
                        None => continue,
                    }
                };
                let revision = request.revision;
                let result = store
                    .save_to(&request.snapshot, request.destination)
                    .map(|()| revision)
                    .map_err(|error| error.to_string());
                if let Ok(mut state) = worker_shared.0.lock()
                    && result.is_ok()
                {
                    state.completed_revision = state.completed_revision.max(revision);
                }
                if let Some(completion) = request.completion {
                    let _ = completion.send(result);
                }
            }
        });
        Self {
            shared,
            clients: Arc::new(AtomicUsize::new(1)),
        }
    }

    pub fn submit(
        &self,
        snapshot: WorkspaceSnapshot,
        destination: SnapshotDestination,
        revision: u64,
    ) -> Result<(), String> {
        self.queue(WriterRequest {
            snapshot,
            destination,
            revision,
            completion: None,
        })
    }

    pub fn flush(
        &self,
        snapshot: WorkspaceSnapshot,
        destination: SnapshotDestination,
        revision: u64,
    ) -> Result<(), String> {
        let (tx, rx) = mpsc::channel();
        self.queue(WriterRequest {
            snapshot,
            destination,
            revision,
            completion: Some(tx),
        })?;
        let completed = rx.recv().map_err(|error| error.to_string())??;
        if completed != revision {
            return Err(format!("saved revision {completed}, expected {revision}"));
        }
        Ok(())
    }

    fn queue(&self, request: WriterRequest) -> Result<(), String> {
        let (lock, wake) = &*self.shared;
        let mut state = lock.lock().map_err(|error| error.to_string())?;
        if state.closed {
            return Err("snapshot writer is closed".into());
        }
        if request.revision < state.completed_revision
            || state
                .pending
                .as_ref()
                .is_some_and(|pending| request.revision < pending.revision)
        {
            if let Some(completion) = request.completion {
                let _ = completion.send(Err("snapshot revision is stale".into()));
            }
            return Ok(());
        }
        if let Some(old) = state.pending.replace(request)
            && let Some(completion) = old.completion
        {
            let _ = completion.send(Err("snapshot revision superseded".into()));
        }
        wake.notify_one();
        Ok(())
    }
}

impl Drop for SnapshotWriter {
    fn drop(&mut self) {
        if self.clients.fetch_sub(1, Ordering::AcqRel) != 1 {
            return;
        }
        let (lock, wake) = &*self.shared;
        if let Ok(mut state) = lock.lock() {
            state.closed = true;
            wake.notify_one();
        }
    }
}

#[derive(Debug)]
pub enum LoadOutcome {
    Missing,
    Valid(crate::ValidatedSnapshot),
    RecoveryRequired(SnapshotError),
    ValidRecovery(crate::ValidatedSnapshot, SnapshotDestination),
    RecoveryUnavailable(SnapshotError, SnapshotDestination),
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum SnapshotDestination {
    Primary,
    Recovery,
    RecoveryFile(PathBuf),
}

#[derive(Debug, thiserror::Error)]
pub enum StoreError {
    #[error("snapshot serialization failed: {0}")]
    Serialize(#[from] serde_json::Error),
    #[error("snapshot I/O failed: {0}")]
    Io(#[from] std::io::Error),
}

impl SnapshotStore {
    pub fn new(path: PathBuf) -> Self {
        Self {
            path,
            limits: SnapshotLimits::default(),
        }
    }
    pub fn with_limits(path: PathBuf, limits: SnapshotLimits) -> Self {
        Self { path, limits }
    }
    pub fn path(&self) -> &Path {
        &self.path
    }
    pub fn recovery_path(&self) -> PathBuf {
        self.path.with_file_name("workspace-recovery-v1.json")
    }
    pub fn new_recovery_path(&self) -> PathBuf {
        self.path
            .with_file_name(format!("workspace-recovery-{}.json", uuid::Uuid::new_v4()))
    }

    pub fn load(&self) -> Result<LoadOutcome, StoreError> {
        self.load_from(&self.path)
    }

    pub fn load_recovery(&self) -> Result<LoadOutcome, StoreError> {
        let parent = self.path.parent().unwrap_or_else(|| Path::new("."));
        let mut candidates = Vec::new();
        match fs::read_dir(parent) {
            Ok(entries) => {
                for entry in entries {
                    let entry = entry?;
                    let name = entry.file_name();
                    let name = name.to_string_lossy();
                    if name.starts_with("workspace-recovery")
                        && name.ends_with(".json")
                        && entry.file_type()?.is_file()
                    {
                        let modified = entry
                            .metadata()
                            .and_then(|metadata| metadata.modified())
                            .ok();
                        candidates.push((modified, entry.path()));
                    }
                }
            }
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
            Err(error) => return Err(error.into()),
        }
        candidates.sort_by_key(|candidate| std::cmp::Reverse(candidate.0));
        candidates.truncate(128);
        let mut last_error = None;
        for (_, path) in &candidates {
            match self.load_from(path)? {
                LoadOutcome::Valid(snapshot) => {
                    return Ok(LoadOutcome::ValidRecovery(
                        snapshot,
                        SnapshotDestination::RecoveryFile(path.clone()),
                    ));
                }
                LoadOutcome::RecoveryRequired(error) => last_error = Some(error),
                LoadOutcome::Missing => {}
                LoadOutcome::ValidRecovery(_, _) | LoadOutcome::RecoveryUnavailable(_, _) => {
                    unreachable!()
                }
            }
        }
        if candidates.is_empty() {
            return Ok(LoadOutcome::Missing);
        }
        Ok(LoadOutcome::RecoveryUnavailable(
            last_error
                .unwrap_or_else(|| SnapshotError::Corrupt("no valid recovery snapshot".into())),
            SnapshotDestination::RecoveryFile(self.new_recovery_path()),
        ))
    }

    fn load_from(&self, path: &Path) -> Result<LoadOutcome, StoreError> {
        match fs::metadata(path) {
            Ok(metadata) if metadata.len() > self.limits.max_file_bytes as u64 => {
                return Ok(LoadOutcome::RecoveryRequired(SnapshotError::TooLarge));
            }
            Ok(_) => {}
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                return Ok(LoadOutcome::Missing);
            }
            Err(error) => return Err(error.into()),
        }
        let file = match fs::File::open(path) {
            Ok(file) => file,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                return Ok(LoadOutcome::Missing);
            }
            Err(error) => return Err(error.into()),
        };
        let mut bytes = Vec::with_capacity(self.limits.max_file_bytes.min(64 * 1024));
        file.take(self.limits.max_file_bytes as u64 + 1)
            .read_to_end(&mut bytes)?;
        if bytes.len() > self.limits.max_file_bytes {
            return Ok(LoadOutcome::RecoveryRequired(SnapshotError::TooLarge));
        }
        match migration::decode(&bytes, self.limits) {
            Ok(snapshot) => Ok(LoadOutcome::Valid(
                snapshot
                    .validate(self.limits)
                    .expect("migration validated snapshot"),
            )),
            Err(error) => Ok(LoadOutcome::RecoveryRequired(error)),
        }
    }

    pub fn save(&self, snapshot: &WorkspaceSnapshot, recovery: bool) -> Result<(), StoreError> {
        self.save_to(
            snapshot,
            if recovery {
                SnapshotDestination::Recovery
            } else {
                SnapshotDestination::Primary
            },
        )
    }

    pub fn save_to(
        &self,
        snapshot: &WorkspaceSnapshot,
        destination: SnapshotDestination,
    ) -> Result<(), StoreError> {
        let target = match destination {
            SnapshotDestination::Primary => self.path.clone(),
            SnapshotDestination::Recovery => self.recovery_path(),
            SnapshotDestination::RecoveryFile(path) => path,
        };
        let bytes = serde_json::to_vec_pretty(snapshot)?;
        if bytes.len() > self.limits.max_file_bytes {
            return Err(std::io::Error::new(
                std::io::ErrorKind::InvalidData,
                "snapshot exceeds maximum file size",
            )
            .into());
        }
        atomic_write(&target, &bytes)
    }
}

fn atomic_write(path: &Path, bytes: &[u8]) -> Result<(), StoreError> {
    atomic_write_with(path, bytes, |_| Ok(()))
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum AtomicStage {
    CreateDirectory,
    OpenTemporary,
    WriteTemporary,
    SyncTemporary,
    Rename,
    SyncDirectory,
}

fn atomic_write_with(
    path: &Path,
    bytes: &[u8],
    mut before: impl FnMut(AtomicStage) -> std::io::Result<()>,
) -> Result<(), StoreError> {
    let parent = path.parent().unwrap_or_else(|| Path::new("."));
    before(AtomicStage::CreateDirectory)?;
    #[cfg(unix)]
    let parent_existed = parent.exists();
    fs::create_dir_all(parent)?;
    #[cfg(unix)]
    if !parent_existed {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(parent, fs::Permissions::from_mode(0o700))?;
    }
    let temp = path.with_file_name(format!(".workspace-{}.tmp", uuid::Uuid::new_v4()));
    let result = (|| {
        let mut options = OpenOptions::new();
        options.write(true).create_new(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600);
        }
        before(AtomicStage::OpenTemporary)?;
        let mut file = options.open(&temp)?;
        before(AtomicStage::WriteTemporary)?;
        file.write_all(bytes)?;
        before(AtomicStage::SyncTemporary)?;
        file.sync_all()?;
        before(AtomicStage::Rename)?;
        fs::rename(&temp, path)?;
        #[cfg(unix)]
        {
            before(AtomicStage::SyncDirectory)?;
            fs::File::open(parent)?.sync_all()?;
        }
        Ok::<(), std::io::Error>(())
    })();
    if result.is_err() {
        let _ = fs::remove_file(&temp);
    }
    result.map_err(StoreError::Io)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{CwdProvenance, PersistedCwd, WorkspaceSnapshot};
    use omaterm_core::{Pane, PaneId, PaneTree, Project, Tab};
    use std::collections::HashMap;

    fn store() -> SnapshotStore {
        let path = std::env::temp_dir().join(format!(
            "omaterm-state-test-{}/workspace-v1.json",
            uuid::Uuid::new_v4()
        ));
        SnapshotStore::new(path)
    }

    fn snapshot_named(name: Option<&str>) -> WorkspaceSnapshot {
        let pane = Pane::empty();
        let id: PaneId = pane.id;
        let tab = Tab::new(PaneTree::new(pane), id).unwrap();
        let mut project = Project::new(name.map(str::to_owned), None);
        project.add_tab(tab).unwrap();
        let mut window = omaterm_core::WorkspaceWindow::new();
        window.add_project(project).unwrap();
        WorkspaceSnapshot::capture(
            &window,
            &HashMap::from([(
                id,
                PersistedCwd {
                    path: PathBuf::from("/tmp"),
                    provenance: CwdProvenance::Launch,
                },
            )]),
        )
    }

    fn snapshot() -> WorkspaceSnapshot {
        snapshot_named(None)
    }

    #[test]
    fn missing_load_then_atomic_save_and_reload() {
        let store = store();
        assert!(matches!(store.load().unwrap(), LoadOutcome::Missing));
        store.save(&snapshot(), false).unwrap();
        let LoadOutcome::Valid(restored) = store.load().unwrap() else {
            panic!("snapshot should load")
        };
        assert_eq!(restored.window.projects.len(), 1);
        let bytes = fs::read(store.path()).unwrap();
        assert!(
            String::from_utf8(bytes)
                .unwrap()
                .contains("\n  \"schema_version\"")
        );
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            assert_eq!(
                fs::metadata(store.path()).unwrap().permissions().mode() & 0o777,
                0o600
            );
            assert_eq!(
                fs::metadata(store.path().parent().unwrap())
                    .unwrap()
                    .permissions()
                    .mode()
                    & 0o777,
                0o700
            );
        }
        let _ = fs::remove_dir_all(store.path().parent().unwrap());
    }

    #[test]
    fn pretty_snapshot_file_byte_cap_accepts_boundary_and_preserves_file_when_exceeded() {
        let mut window = omaterm_core::WorkspaceWindow::new();
        for _ in 0..2 {
            window.add_project(Project::new(None, None)).unwrap();
        }
        let mut snapshot = WorkspaceSnapshot::capture(&window, &HashMap::new());
        for project in &mut snapshot.windows[0].projects {
            project.documents = (0..16)
                .map(|index| crate::DocumentSnapshot {
                    id: uuid::Uuid::new_v4().to_string(),
                    path_bytes: format!("src/{index}.rs").into_bytes(),
                    root_device: 0,
                    root_inode: 11,
                })
                .collect();
            project.active_document = project.documents.last().map(|document| document.id.clone());
        }
        let bytes = serde_json::to_vec_pretty(&snapshot).unwrap();
        let base = store();
        let limits = SnapshotLimits {
            max_file_bytes: bytes.len(),
            ..SnapshotLimits::default()
        };
        let at_cap = SnapshotStore::with_limits(base.path().to_owned(), limits);
        at_cap.save(&snapshot, false).unwrap();
        assert_eq!(fs::read(at_cap.path()).unwrap(), bytes);
        let LoadOutcome::Valid(restored) = at_cap.load().unwrap() else {
            panic!("at-cap multi-project registry should load")
        };
        assert_eq!(restored.document_registries.len(), 2);
        assert!(restored.document_registries.iter().all(|(_, registry)| {
            registry.documents.len() == 16 && registry.active_document.is_some()
        }));

        let over_cap = SnapshotStore::with_limits(
            base.path().to_owned(),
            SnapshotLimits {
                max_file_bytes: bytes.len() - 1,
                ..limits
            },
        );
        assert!(over_cap.save(&snapshot, false).is_err());
        assert_eq!(fs::read(over_cap.path()).unwrap(), bytes);
        assert!(matches!(
            over_cap.load().unwrap(),
            LoadOutcome::RecoveryRequired(SnapshotError::TooLarge)
        ));
        fs::remove_dir_all(base.path().parent().unwrap()).unwrap();
    }

    #[test]
    fn corrupt_primary_is_retained_when_recovery_snapshot_is_saved() {
        let store = store();
        fs::create_dir_all(store.path().parent().unwrap()).unwrap();
        fs::write(store.path(), b"not json").unwrap();
        let original = fs::read(store.path()).unwrap();
        assert!(matches!(
            store.load().unwrap(),
            LoadOutcome::RecoveryRequired(_)
        ));
        store.save(&snapshot(), true).unwrap();
        assert_eq!(fs::read(store.path()).unwrap(), original);
        assert!(store.recovery_path().exists());
        assert!(matches!(
            store.load_recovery().unwrap(),
            LoadOutcome::ValidRecovery(_, SnapshotDestination::RecoveryFile(_))
        ));
        assert_eq!(fs::read(store.path()).unwrap(), original);
        let _ = fs::remove_dir_all(store.path().parent().unwrap());
    }

    #[test]
    fn invalid_or_future_primary_is_retained_when_recovery_snapshot_is_saved() {
        for bytes in [
            br#"{"schema_version":77,"future_shape":true}"#.to_vec(),
            br#"{"schema_version":3,"windows":[{"id":"00000000-0000-0000-0000-000000000001","selected_project":null,"projects":[{"id":"00000000-0000-0000-0000-000000000002","custom_name":null,"pinned_directory":null,"selected_tab":null,"tabs":[],"expanded_dirs":[],"documents":[],"active_document":null,"dirty_text":"must never be persisted"}]}]}"#.to_vec(),
        ] {
            let store = store();
            fs::create_dir_all(store.path().parent().unwrap()).unwrap();
            fs::write(store.path(), &bytes).unwrap();
            assert!(matches!(
                store.load().unwrap(),
                LoadOutcome::RecoveryRequired(SnapshotError::UnsupportedVersion(77))
                    | LoadOutcome::RecoveryRequired(SnapshotError::Corrupt(_))
            ));
            store.save(&snapshot(), true).unwrap();
            assert_eq!(fs::read(store.path()).unwrap(), bytes);
            assert!(matches!(
                store.load_recovery().unwrap(),
                LoadOutcome::ValidRecovery(_, SnapshotDestination::RecoveryFile(_))
            ));
            let _ = fs::remove_dir_all(store.path().parent().unwrap());
        }
    }

    #[test]
    fn invalid_recovery_file_is_preserved_and_new_recovery_uses_an_alternate_path() {
        let store = store();
        fs::create_dir_all(store.path().parent().unwrap()).unwrap();
        fs::write(store.path(), b"bad primary").unwrap();
        fs::write(store.recovery_path(), b"bad recovery").unwrap();
        let bad_primary = fs::read(store.path()).unwrap();
        let bad_recovery = fs::read(store.recovery_path()).unwrap();
        assert!(matches!(
            store.load().unwrap(),
            LoadOutcome::RecoveryRequired(_)
        ));
        let LoadOutcome::RecoveryUnavailable(_, destination) = store.load_recovery().unwrap()
        else {
            panic!("recovery should be unavailable")
        };
        store.save_to(&snapshot(), destination).unwrap();
        assert_eq!(fs::read(store.path()).unwrap(), bad_primary);
        assert_eq!(fs::read(store.recovery_path()).unwrap(), bad_recovery);
        assert!(matches!(
            store.load_recovery().unwrap(),
            LoadOutcome::ValidRecovery(_, SnapshotDestination::RecoveryFile(_))
        ));
        let _ = fs::remove_dir_all(store.path().parent().unwrap());
    }

    #[test]
    fn oversized_primary_is_rejected_with_bounded_read() {
        let store = store();
        fs::create_dir_all(store.path().parent().unwrap()).unwrap();
        fs::write(
            store.path(),
            vec![b'x'; SnapshotLimits::default().max_file_bytes + 8],
        )
        .unwrap();
        assert!(matches!(
            store.load().unwrap(),
            LoadOutcome::RecoveryRequired(SnapshotError::TooLarge)
        ));
        let _ = fs::remove_dir_all(store.path().parent().unwrap());
    }

    #[test]
    fn writer_keeps_latest_revision_and_rejects_stale_work() {
        let store = store();
        let writer = SnapshotWriter::new(store.clone());
        writer
            .submit(
                snapshot_named(Some("older")),
                SnapshotDestination::Primary,
                1,
            )
            .unwrap();
        writer
            .submit(
                snapshot_named(Some("latest")),
                SnapshotDestination::Primary,
                2,
            )
            .unwrap();
        writer
            .flush(
                snapshot_named(Some("latest")),
                SnapshotDestination::Primary,
                2,
            )
            .unwrap();
        writer
            .submit(
                snapshot_named(Some("stale")),
                SnapshotDestination::Primary,
                1,
            )
            .unwrap();
        let LoadOutcome::Valid(restored) = store.load().unwrap() else {
            panic!("latest save missing")
        };
        assert_eq!(
            restored.window.projects[0].custom_name.as_deref(),
            Some("latest")
        );
        drop(writer);
        let _ = fs::remove_dir_all(store.path().parent().unwrap());
    }

    #[test]
    fn injected_atomic_failures_keep_old_snapshot_readable() {
        let store = store();
        store.save(&snapshot_named(Some("old")), false).unwrap();
        let old_bytes = fs::read(store.path()).unwrap();
        let next_bytes = serde_json::to_vec_pretty(&snapshot_named(Some("new"))).unwrap();
        for stage in [
            AtomicStage::CreateDirectory,
            AtomicStage::OpenTemporary,
            AtomicStage::WriteTemporary,
            AtomicStage::SyncTemporary,
            AtomicStage::Rename,
        ] {
            let result = atomic_write_with(store.path(), &next_bytes, |current| {
                if current == stage {
                    Err(std::io::Error::other("injected failure"))
                } else {
                    Ok(())
                }
            });
            assert!(result.is_err(), "{stage:?} should fail");
            assert_eq!(fs::read(store.path()).unwrap(), old_bytes);
            assert!(matches!(store.load().unwrap(), LoadOutcome::Valid(_)));
        }
        #[cfg(unix)]
        {
            let result = atomic_write_with(store.path(), &next_bytes, |current| {
                if current == AtomicStage::SyncDirectory {
                    Err(std::io::Error::other("injected directory sync failure"))
                } else {
                    Ok(())
                }
            });
            assert!(result.is_err());
            assert!(matches!(store.load().unwrap(), LoadOutcome::Valid(_)));
        }
        let _ = fs::remove_dir_all(store.path().parent().unwrap());
    }
}
