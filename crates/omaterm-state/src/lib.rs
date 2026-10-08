pub mod config;
pub mod history;
mod migration;
mod paths;
mod snapshot;
mod store;

pub use config::{
    AppConfig, AppearanceSettings, AutomationSettings, DEFAULT_FONT_SIZE, KNOWN_THEMES,
    MAX_SCROLLBACK_LINES, TerminalSettings, load_app_config_toml,
};

pub use history::{
    ConfigError, HistoryConfig, HistoryError, HistoryEvent, HistoryLimits, HistoryLoad,
    HistoryStatus, HistoryStore, InMemoryKeyProvider, JournalBuffer, JournalEntry, KeyProvider,
    KeyProviderError, OsKeyProvider, StoreError as HistoryStoreError, decrypt_archive,
    decrypt_journal, default_config_toml_path, default_history_config_path, default_history_dir,
    encrypt_archive, encrypt_journal, estimate_output_lines, history_status, load_history_config,
    load_history_config_migrated, load_history_config_toml, opaque_pane_name, save_history_config,
    save_history_config_toml, stale_archives, trim_events_to_limits,
};
pub use snapshot::{
    CwdProvenance, DocumentDescriptor, DocumentRegistry, DocumentSnapshot, PaneNodeSnapshot,
    PersistedCwd, ProjectSnapshot, SnapshotError, SnapshotLimits, SplitAxisSnapshot, TabSnapshot,
    ValidatedSnapshot, WindowSnapshot, WorkspaceSnapshot,
};
pub use store::{
    LoadOutcome, SnapshotDestination, SnapshotStore, SnapshotWriter, StoreError,
    default_snapshot_path,
};
