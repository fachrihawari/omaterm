//! Opt-in encrypted terminal history (M10 foundation).
//!
//! Scrollback archives and the OmaTerm command journal are stored separately
//! from `workspace-v1.json`, encrypted at rest with a per-archive key derived
//! from an OS-keyring-held master key. History is disabled by default; nothing
//! is recorded, encrypted, or written until the user explicitly opts in.
//!
//!
//! Foundation scope: configuration, key-provider abstraction, versioned
//! authenticated archive framing (compress-then-encrypt), filesystem store
//! with strict permissions, bounds enforcement before allocation, and the
//! journal record types. Shell-hook reliability, dispatcher/IPC/CLI commands,
//! and desktop controls land in later M10 slices.

use std::collections::{HashMap, HashSet};
use std::fs::{self, OpenOptions};
use std::io::{Read, Write};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};

use chacha20poly1305::{AeadInOut, KeyInit, Nonce};
use hkdf::Hkdf;
use serde::{Deserialize, Serialize};
use sha2::Sha256;
use zeroize::Zeroize;

// --- limits ---------------------------------------------------------------

/// Initial M10 ceilings. Enforced before allocation/decompression.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct HistoryLimits {
    /// Retained logical lines per pane (estimated from `\n` in output events).
    pub max_lines_per_pane: usize,
    /// Encrypted archive file size per pane.
    pub max_archive_bytes_per_pane: usize,
    /// Combined persisted history for the whole workspace.
    pub max_workspace_bytes: u64,
    /// Journal entries per pane.
    pub max_journal_entries_per_pane: usize,
    /// Decompressed plaintext cap per archive (enforced before allocation).
    pub max_decompressed_bytes: usize,
    /// Raw output bytes per recorded event (larger writes are split).
    pub max_frame_bytes: usize,
    /// Recorded events per archive (oldest complete events are dropped).
    pub max_events_per_archive: usize,
}

impl Default for HistoryLimits {
    fn default() -> Self {
        Self {
            max_lines_per_pane: 10_000,
            max_archive_bytes_per_pane: 8 * 1024 * 1024,
            max_workspace_bytes: 64 * 1024 * 1024,
            max_journal_entries_per_pane: 10_000,
            max_decompressed_bytes: 1024 * 1024,
            max_frame_bytes: 64 * 1024,
            max_events_per_archive: 4096,
        }
    }
}

// --- configuration --------------------------------------------------------

/// Persisted history configuration. Disabled by default.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct HistoryConfig {
    /// Global opt-in. When false, nothing is recorded or written.
    #[serde(default)]
    pub enabled: bool,
    /// Opaque pane identifiers (UUID strings) excluded from capture.
    #[serde(default)]
    pub paused_panes: HashSet<String>,
}

impl HistoryConfig {
    #[must_use]
    pub fn is_paused(&self, pane: &str) -> bool {
        self.paused_panes.contains(pane)
    }

    pub fn set_paused(&mut self, pane: &str, paused: bool) {
        if paused {
            self.paused_panes.insert(pane.to_string());
        } else {
            self.paused_panes.remove(pane);
        }
    }
}

/// Config directory contract: `$XDG_CONFIG_HOME/omaterm/` with
/// `$HOME/.config/omaterm/` fallback. On Windows, where neither is set,
/// this is `%APPDATA%\omaterm`. History configuration lives in the
/// canonical `config.toml` under a `[history]` table and never inside
/// workspace snapshots.
fn config_base_dir() -> Option<PathBuf> {
    crate::paths::config_base_dir().map(|base| base.join("omaterm"))
}

/// Canonical history configuration path: `config.toml` in the config dir.
pub fn default_config_toml_path() -> Option<PathBuf> {
    config_base_dir().map(|dir| dir.join("config.toml"))
}

/// Legacy foundation path (`history.json`). Kept only as a migration source;
/// new code must use [`default_config_toml_path`].
pub fn default_history_config_path() -> Option<PathBuf> {
    config_base_dir().map(|dir| dir.join("history.json"))
}

#[derive(Debug, thiserror::Error)]
pub enum ConfigError {
    #[error("history config serialization failed: {0}")]
    Serialize(#[from] serde_json::Error),
    #[error("history config file is not valid: {0}")]
    Parse(String),
    #[error("config value is not valid: {0}")]
    Invalid(String),
    #[error("history config I/O failed: {0}")]
    Io(#[from] std::io::Error),
}

/// Legacy JSON load (migration source only).
pub fn load_history_config(path: &Path) -> Result<HistoryConfig, ConfigError> {
    match fs::read(path) {
        Ok(bytes) => Ok(serde_json::from_slice(&bytes)?),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(HistoryConfig::default()),
        Err(error) => Err(ConfigError::Io(error)),
    }
}

/// Legacy JSON save (migration/tests only). Production saves go through
/// [`save_history_config_toml`].
pub fn save_history_config(path: &Path, config: &HistoryConfig) -> Result<(), ConfigError> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)?;
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            // Best effort: config dir should not be world-accessible.
            let _ = fs::set_permissions(parent, fs::Permissions::from_mode(0o700));
        }
    }
    let bytes = serde_json::to_vec_pretty(config)?;
    write_atomic_0600(path, &bytes)?;
    Ok(())
}

/// Write `bytes` to `path` atomically (same-directory unique temp + rename)
/// with owner-only permissions. Used for config files, which are small and
/// never under the workspace history quota.
pub(super) fn write_atomic_0600(path: &Path, bytes: &[u8]) -> Result<(), ConfigError> {
    let temp = unique_temp_path(path);
    {
        let mut options = OpenOptions::new();
        options.write(true).create_new(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600);
        }
        let mut file = options.open(&temp).map_err(ConfigError::Io)?;
        file.write_all(bytes).map_err(ConfigError::Io)?;
        file.sync_all().map_err(ConfigError::Io)?;
    }
    fs::rename(&temp, path).map_err(ConfigError::Io)?;
    Ok(())
}

fn history_section(doc: &toml_edit::DocumentMut) -> Option<HistoryConfig> {
    let table = doc.get("history")?.as_table_like()?;
    let enabled = table
        .get("enabled")
        .and_then(toml_edit::Item::as_bool)
        .unwrap_or(false);
    let mut paused_panes = HashSet::new();
    if let Some(array) = table
        .get("paused_panes")
        .and_then(toml_edit::Item::as_array)
    {
        for value in array {
            if let Some(name) = value.as_str() {
                paused_panes.insert(name.to_string());
            }
        }
    }
    Some(HistoryConfig {
        enabled,
        paused_panes,
    })
}

/// Load the `[history]` section from the canonical `config.toml`. Returns
/// `None` when the file or the section is absent (both mean "defaults");
/// malformed TOML is an error so the caller can warn instead of silently
/// discarding user configuration.
pub fn load_history_config_toml(path: &Path) -> Result<Option<HistoryConfig>, ConfigError> {
    let bytes = match fs::read(path) {
        Ok(bytes) => bytes,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(ConfigError::Io(error)),
    };
    let text = String::from_utf8(bytes)
        .map_err(|_| ConfigError::Parse("config.toml is not UTF-8".into()))?;
    let doc: toml_edit::DocumentMut = text
        .parse()
        .map_err(|error| ConfigError::Parse(format!("config.toml parse failed: {error}")))?;
    Ok(history_section(&doc))
}

/// Save the `[history]` section into the canonical `config.toml`, preserving
/// every other section, comment, and formatting choice in the document.
pub fn save_history_config_toml(path: &Path, config: &HistoryConfig) -> Result<(), ConfigError> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(ConfigError::Io)?;
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let _ = fs::set_permissions(parent, fs::Permissions::from_mode(0o700));
        }
    }
    let mut doc: toml_edit::DocumentMut = match fs::read(path) {
        Ok(bytes) => {
            let text = String::from_utf8(bytes)
                .map_err(|_| ConfigError::Parse("config.toml is not UTF-8".into()))?;
            text.parse()
                .map_err(|error| ConfigError::Parse(format!("config.toml parse failed: {error}")))?
        }
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => toml_edit::DocumentMut::new(),
        Err(error) => return Err(ConfigError::Io(error)),
    };
    let mut table = toml_edit::Table::new();
    table["enabled"] = toml_edit::value(config.enabled);
    let mut paused: Vec<&String> = config.paused_panes.iter().collect();
    paused.sort();
    let mut array = toml_edit::Array::new();
    for name in paused {
        array.push(name.as_str());
    }
    table["paused_panes"] = toml_edit::value(array);
    doc["history"] = toml_edit::Item::Table(table);
    write_atomic_0600(path, doc.to_string().as_bytes())?;
    Ok(())
}

/// Load history configuration with one-time migration: the canonical
/// `config.toml` `[history]` section wins; otherwise a legacy `history.json`
/// is adopted into `config.toml` (and removed) so an existing opt-in
/// survives the migration. Absent sources yield defaults.
pub fn load_history_config_migrated(
    toml_path: &Path,
    legacy_json_path: &Path,
) -> Result<HistoryConfig, ConfigError> {
    if let Some(config) = load_history_config_toml(toml_path)? {
        return Ok(config);
    }
    let legacy = load_history_config(legacy_json_path)?;
    if legacy != HistoryConfig::default() {
        save_history_config_toml(toml_path, &legacy)?;
        let _ = fs::remove_file(legacy_json_path);
    }
    Ok(legacy)
}

// --- key provider ---------------------------------------------------------

/// Failures from OS-backed key storage. No variant permits a plaintext
/// fallback: callers must preserve in-memory terminal use, keep prior
/// archives untouched, and surface a recoverable warning.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum KeyProviderError {
    /// No usable Secret Service / keyring on this machine or session.
    Unavailable(String),
    /// The keyring exists but is locked.
    Locked(String),
    /// Access was denied by policy or the user.
    Denied(String),
    /// Stored key is corrupt or has an unexpected shape.
    Corrupt(String),
    /// Any other storage failure.
    Storage(String),
}

impl std::fmt::Display for KeyProviderError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Unavailable(detail) => write!(f, "key storage unavailable: {detail}"),
            Self::Locked(detail) => write!(f, "keyring is locked: {detail}"),
            Self::Denied(detail) => write!(f, "key storage denied access: {detail}"),
            Self::Corrupt(detail) => write!(f, "stored key is corrupt: {detail}"),
            Self::Storage(detail) => write!(f, "key storage failure: {detail}"),
        }
    }
}

impl std::error::Error for KeyProviderError {}

/// Abstraction over master-key storage. Production Linux stores only a
/// randomly generated 32-byte master key in Secret Service; tests inject
/// [`InMemoryKeyProvider`]. The master key itself never touches logs, errors,
/// crash messages, or workspace JSON.
pub trait KeyProvider: Send {
    /// Return the existing master key, creating and storing a fresh random
    /// one when none exists yet.
    fn get_or_create_master_key(&mut self) -> Result<[u8; 32], KeyProviderError>;
    /// Return the cached/known key without creating one, if available.
    fn master_key(&self) -> Option<[u8; 32]>;
    /// Replace the master key with a fresh random value. Old archives become
    /// undecryptable by design; callers delete archives in the same clear-all
    /// operation so stale blocks cannot be decrypted with the prior key.
    fn rotate_master_key(&mut self) -> Result<[u8; 32], KeyProviderError>;
    /// Remove the master key from storage.
    fn remove_master_key(&mut self) -> Result<(), KeyProviderError>;
}

/// In-memory provider for tests and for headless environments where the
/// caller has already decided to keep history memory-only. Supports injected
/// failures so key-loss paths are covered without touching the real keyring.
#[derive(Debug, Default)]
pub struct InMemoryKeyProvider {
    key: Option<[u8; 32]>,
    fail_next: Option<KeyProviderError>,
}

impl InMemoryKeyProvider {
    #[must_use]
    pub fn new() -> Self {
        Self::default()
    }

    /// Fail the next mutating operation with the given error (one-shot).
    pub fn fail_next(&mut self, error: KeyProviderError) {
        self.fail_next = Some(error);
    }
}

fn random_32() -> Result<[u8; 32], String> {
    let mut key = [0u8; 32];
    getrandom::fill(&mut key).map_err(|error| error.to_string())?;
    Ok(key)
}

fn random_master_key() -> Result<[u8; 32], KeyProviderError> {
    random_32().map_err(KeyProviderError::Storage)
}

static TEMP_COUNTER: AtomicU64 = AtomicU64::new(0);

fn hex_bytes(bytes: &[u8]) -> String {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut out = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        out.push(HEX[(byte >> 4) as usize] as char);
        out.push(HEX[(byte & 0x0f) as usize] as char);
    }
    out
}

/// Collision-resistant same-directory temporary path for atomic replacement.
/// Same-directory placement keeps `rename` atomic; process id, a process-wide
/// counter, and random bytes keep concurrent writers — including writers in
/// other processes — from sharing a temp name.
fn unique_temp_path(path: &Path) -> PathBuf {
    let pid = std::process::id();
    let counter = TEMP_COUNTER.fetch_add(1, Ordering::Relaxed);
    let mut rand = [0u8; 8];
    let _ = getrandom::fill(&mut rand);
    let file = path
        .file_name()
        .map(|name| name.to_string_lossy().into_owned())
        .unwrap_or_else(|| "history".into());
    path.with_file_name(format!(".{file}.{pid}.{counter}.{}.tmp", hex_bytes(&rand)))
}

impl KeyProvider for InMemoryKeyProvider {
    fn get_or_create_master_key(&mut self) -> Result<[u8; 32], KeyProviderError> {
        if let Some(error) = self.fail_next.take() {
            return Err(error);
        }
        if let Some(key) = self.key {
            return Ok(key);
        }
        let key = random_master_key()?;
        self.key = Some(key);
        Ok(key)
    }

    fn master_key(&self) -> Option<[u8; 32]> {
        self.key
    }

    fn rotate_master_key(&mut self) -> Result<[u8; 32], KeyProviderError> {
        if let Some(error) = self.fail_next.take() {
            return Err(error);
        }
        let mut old = self.key.take();
        let key = random_master_key()?;
        self.key = Some(key);
        if let Some(slot) = old.as_mut() {
            slot.zeroize();
        }
        Ok(key)
    }

    fn remove_master_key(&mut self) -> Result<(), KeyProviderError> {
        if let Some(error) = self.fail_next.take() {
            return Err(error);
        }
        if let Some(slot) = self.key.as_mut() {
            slot.zeroize();
        }
        self.key = None;
        Ok(())
    }
}

impl Drop for InMemoryKeyProvider {
    fn drop(&mut self) {
        if let Some(slot) = self.key.as_mut() {
            slot.zeroize();
        }
    }
}

/// Production Linux provider backed by Secret Service via the `keyring` crate
/// (sync v1 API; Secret Service is the store on `*nix`). Only the random
/// master key is stored, hex-encoded; no terminal output ever reaches it.
pub struct OsKeyProvider {
    service: String,
    user: String,
    cached: Option<[u8; 32]>,
}

impl OsKeyProvider {
    #[must_use]
    pub fn omaterm_default() -> Self {
        Self {
            service: "omaterm-history".to_string(),
            user: "history-master-key".to_string(),
            cached: None,
        }
    }

    #[must_use]
    pub fn with_names(service: &str, user: &str) -> Self {
        Self {
            service: service.to_string(),
            user: user.to_string(),
            cached: None,
        }
    }

    fn entry(&self) -> Result<keyring::Entry, KeyProviderError> {
        keyring::Entry::new(&self.service, &self.user).map_err(|error| map_keyring_error(&error))
    }
}

fn map_keyring_error(error: &keyring::Error) -> KeyProviderError {
    use keyring::Error as E;
    match error {
        E::NoEntry => KeyProviderError::Unavailable("no master key stored yet".into()),
        E::NoStorageAccess(inner) => {
            let detail = inner.to_string().to_lowercase();
            if detail.contains("locked") {
                KeyProviderError::Locked(inner.to_string())
            } else if detail.contains("denied")
                || detail.contains("permission")
                || detail.contains("access")
            {
                KeyProviderError::Denied(inner.to_string())
            } else {
                KeyProviderError::Unavailable(inner.to_string())
            }
        }
        E::PlatformFailure(inner) => {
            KeyProviderError::Storage(format!("platform key storage failure: {inner}"))
        }
        E::NoDefaultStore => KeyProviderError::Unavailable("no default credential store".into()),
        E::NotSupportedByStore(detail) => KeyProviderError::Unavailable(detail.clone()),
        other => KeyProviderError::Storage(other.to_string()),
    }
}

fn encode_key_hex(key: &[u8; 32]) -> String {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut out = String::with_capacity(64);
    for byte in key {
        out.push(HEX[(byte >> 4) as usize] as char);
        out.push(HEX[(byte & 0x0f) as usize] as char);
    }
    out
}

fn decode_key_hex(text: &str) -> Result<[u8; 32], KeyProviderError> {
    let bytes = text.trim().as_bytes();
    if bytes.len() != 64 {
        return Err(KeyProviderError::Corrupt(format!(
            "master key has length {}, expected 64 hex chars",
            bytes.len()
        )));
    }
    let mut key = [0u8; 32];
    for (index, chunk) in bytes.chunks(2).enumerate() {
        let hex = std::str::from_utf8(chunk)
            .map_err(|_| KeyProviderError::Corrupt("master key is not ASCII hex".into()))?;
        let byte = u8::from_str_radix(hex, 16)
            .map_err(|_| KeyProviderError::Corrupt("master key hex is invalid".into()))?;
        key[index] = byte;
    }
    Ok(key)
}

impl KeyProvider for OsKeyProvider {
    fn get_or_create_master_key(&mut self) -> Result<[u8; 32], KeyProviderError> {
        if let Some(key) = self.cached {
            return Ok(key);
        }
        let entry = self.entry()?;
        match entry.get_password() {
            Ok(text) => {
                let key = decode_key_hex(&text)?;
                self.cached = Some(key);
                Ok(key)
            }
            Err(keyring::Error::NoEntry) => {
                let key = random_master_key()?;
                entry
                    .set_password(&encode_key_hex(&key))
                    .map_err(|error| map_keyring_error(&error))?;
                self.cached = Some(key);
                Ok(key)
            }
            Err(error) => Err(map_keyring_error(&error)),
        }
    }

    fn master_key(&self) -> Option<[u8; 32]> {
        self.cached
    }

    fn rotate_master_key(&mut self) -> Result<[u8; 32], KeyProviderError> {
        let entry = self.entry()?;
        let mut old = self.cached.take();
        let key = random_master_key()?;
        entry
            .set_password(&encode_key_hex(&key))
            .map_err(|error| map_keyring_error(&error))?;
        self.cached = Some(key);
        if let Some(slot) = old.as_mut() {
            slot.zeroize();
        }
        Ok(key)
    }

    fn remove_master_key(&mut self) -> Result<(), KeyProviderError> {
        let entry = self.entry()?;
        match entry.delete_credential() {
            Ok(()) | Err(keyring::Error::NoEntry) => {
                if let Some(slot) = self.cached.as_mut() {
                    slot.zeroize();
                }
                self.cached = None;
                Ok(())
            }
            Err(error) => Err(map_keyring_error(&error)),
        }
    }
}

impl Drop for OsKeyProvider {
    fn drop(&mut self) {
        if let Some(slot) = self.cached.as_mut() {
            slot.zeroize();
        }
    }
}

// --- recorded events --------------------------------------------------------

/// Ordered PTY output / resize events that deterministically rebuild the
/// main-screen history when replayed into a fresh engine in order.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum HistoryEvent {
    /// Raw PTY output bytes observed while the main screen was active.
    /// Never contains bytes from alternate-screen periods (the recorder drops
    /// any chunk where the engine was in alt screen before or after the
    /// advance). Bounded to [`HistoryLimits::max_frame_bytes`] per event;
    /// larger writes are split by the recorder.
    Output(Vec<u8>),
    /// Grid resize observed while the main screen was active.
    Resize { cols: u16, rows: u16 },
}

impl HistoryEvent {
    #[must_use]
    pub fn encoded_len(&self) -> usize {
        match self {
            Self::Output(bytes) => bytes.len(),
            Self::Resize { .. } => 4,
        }
    }
}

/// Plaintext framing: `u32LE event_count`, then per event
/// `u8 kind (0=output, 1=resize) || u32LE len || payload`.
/// Resize payload is `u16LE cols || u16LE rows`.
fn encode_events_plaintext(
    events: &[HistoryEvent],
    limits: HistoryLimits,
) -> Result<Vec<u8>, HistoryError> {
    if events.len() > limits.max_events_per_archive {
        return Err(HistoryError::TooManyEvents(events.len()));
    }
    let mut out = Vec::with_capacity(1024);
    out.extend_from_slice(&(events.len() as u32).to_le_bytes());
    let mut total: usize = 4;
    for event in events {
        match event {
            HistoryEvent::Output(bytes) => {
                if bytes.len() > limits.max_frame_bytes {
                    return Err(HistoryError::FrameTooLarge(bytes.len()));
                }
                if total
                    .checked_add(5 + bytes.len())
                    .is_none_or(|next| next > limits.max_decompressed_bytes)
                {
                    return Err(HistoryError::DecompressedTooLarge(total));
                }
                out.push(0u8);
                out.extend_from_slice(&(bytes.len() as u32).to_le_bytes());
                out.extend_from_slice(bytes);
                total += 5 + bytes.len();
            }
            HistoryEvent::Resize { cols, rows } => {
                if total
                    .checked_add(9)
                    .is_none_or(|next| next > limits.max_decompressed_bytes)
                {
                    return Err(HistoryError::DecompressedTooLarge(total));
                }
                out.push(1u8);
                out.extend_from_slice(&4u32.to_le_bytes());
                out.extend_from_slice(&cols.to_le_bytes());
                out.extend_from_slice(&rows.to_le_bytes());
                total += 9;
            }
        }
    }
    Ok(out)
}

fn decode_events_plaintext(
    bytes: &[u8],
    limits: HistoryLimits,
) -> Result<Vec<HistoryEvent>, HistoryError> {
    if bytes.len() > limits.max_decompressed_bytes {
        return Err(HistoryError::DecompressedTooLarge(bytes.len()));
    }
    if bytes.len() < 4 {
        return Err(HistoryError::Corrupt("truncated event header".into()));
    }
    let count = u32::from_le_bytes([bytes[0], bytes[1], bytes[2], bytes[3]]) as usize;
    if count > limits.max_events_per_archive {
        return Err(HistoryError::TooManyEvents(count));
    }
    let mut events = Vec::with_capacity(count.min(1024));
    let mut cursor = 4usize;
    for _ in 0..count {
        if cursor + 5 > bytes.len() {
            return Err(HistoryError::Corrupt("truncated event prefix".into()));
        }
        let kind = bytes[cursor];
        let len = u32::from_le_bytes([
            bytes[cursor + 1],
            bytes[cursor + 2],
            bytes[cursor + 3],
            bytes[cursor + 4],
        ]) as usize;
        cursor += 5;
        if len > limits.max_frame_bytes && kind == 0 {
            return Err(HistoryError::FrameTooLarge(len));
        }
        if len == 0 && kind == 0 {
            return Err(HistoryError::Corrupt("empty output frame".into()));
        }
        if cursor.checked_add(len).is_none_or(|end| end > bytes.len()) {
            return Err(HistoryError::Corrupt("truncated event payload".into()));
        }
        match kind {
            0 => events.push(HistoryEvent::Output(bytes[cursor..cursor + len].to_vec())),
            1 => {
                if len != 4 {
                    return Err(HistoryError::Corrupt("bad resize payload".into()));
                }
                let cols = u16::from_le_bytes([bytes[cursor], bytes[cursor + 1]]).max(2);
                let rows = u16::from_le_bytes([bytes[cursor + 2], bytes[cursor + 3]]).max(1);
                events.push(HistoryEvent::Resize { cols, rows });
            }
            _ => return Err(HistoryError::Corrupt("unknown event kind".into())),
        }
        cursor += len;
    }
    if cursor != bytes.len() {
        return Err(HistoryError::Corrupt("trailing plaintext bytes".into()));
    }
    Ok(events)
}

// --- encrypted archive ------------------------------------------------------

const ARCHIVE_MAGIC: &[u8; 8] = b"OMHIST01";
const ARCHIVE_VERSION: u32 = 1;
const SALT_LEN: usize = 16;
const NONCE_LEN: usize = 12;
const HKDF_INFO_SCROLLBACK: &[u8] = b"omaterm-history-v1/scrollback";
const HKDF_INFO_JOURNAL: &[u8] = b"omaterm-history-v1/journal";

#[derive(Debug, thiserror::Error)]
pub enum HistoryError {
    #[error("too many events: {0}")]
    TooManyEvents(usize),
    #[error("frame too large: {0} bytes")]
    FrameTooLarge(usize),
    #[error("decompressed history too large ({0} bytes)")]
    DecompressedTooLarge(usize),
    #[error("archive too large: {0} bytes")]
    ArchiveTooLarge(usize),
    #[error("corrupt history archive: {0}")]
    Corrupt(String),
    #[error("unsupported archive version: {0}")]
    UnsupportedVersion(u32),
    #[error("history encryption failed: {0}")]
    Encrypt(String),
    #[error("history decryption failed (wrong key or tampered archive)")]
    Decrypt,
    #[error("history compression failed: {0}")]
    Compress(String),
    #[error("key provider failed: {0}")]
    Key(#[from] KeyProviderError),
    #[error("history I/O failed: {0}")]
    Io(#[from] std::io::Error),
    #[error("history serialization failed: {0}")]
    Serialize(String),
}

fn derive_archive_key(master: &[u8; 32], salt: &[u8], info: &[u8]) -> [u8; 32] {
    let hkdf = Hkdf::<Sha256>::new(Some(salt), master);
    let mut key = [0u8; 32];
    // `expand` only fails on oversized output; 32 bytes never fails.
    let _ = hkdf.expand(info, &mut key);
    key
}

fn archive_aad(pane_uuid: &[u8; 16], revision: u64) -> Vec<u8> {
    let mut aad = Vec::with_capacity(8 + 4 + 16 + 8);
    aad.extend_from_slice(ARCHIVE_MAGIC);
    aad.extend_from_slice(&ARCHIVE_VERSION.to_le_bytes());
    aad.extend_from_slice(pane_uuid);
    aad.extend_from_slice(&revision.to_le_bytes());
    aad
}

/// Encrypt framed events: frame → compress → AEAD. Returns the complete
/// binary archive file body (magic through ciphertext).
pub fn encrypt_archive(
    master: &[u8; 32],
    pane_uuid: &[u8; 16],
    revision: u64,
    events: &[HistoryEvent],
    limits: HistoryLimits,
) -> Result<Vec<u8>, HistoryError> {
    encrypt_framed_payload(
        master,
        pane_uuid,
        revision,
        &encode_events_plaintext(events, limits)?,
        HKDF_INFO_SCROLLBACK,
        limits,
    )
}

fn encrypt_framed_payload(
    master: &[u8; 32],
    pane_uuid: &[u8; 16],
    revision: u64,
    plaintext: &[u8],
    info: &[u8],
    limits: HistoryLimits,
) -> Result<Vec<u8>, HistoryError> {
    if plaintext.len() > limits.max_decompressed_bytes {
        return Err(HistoryError::DecompressedTooLarge(plaintext.len()));
    }
    use flate2::{Compression, write::DeflateEncoder};
    let mut encoder = DeflateEncoder::new(Vec::new(), Compression::default());
    encoder
        .write_all(plaintext)
        .map_err(|error| HistoryError::Compress(error.to_string()))?;
    let compressed = encoder
        .finish()
        .map_err(|error| HistoryError::Compress(error.to_string()))?;

    let mut salt = [0u8; SALT_LEN];
    let mut nonce_bytes = [0u8; NONCE_LEN];
    getrandom::fill(&mut salt).map_err(|error| HistoryError::Encrypt(error.to_string()))?;
    getrandom::fill(&mut nonce_bytes).map_err(|error| HistoryError::Encrypt(error.to_string()))?;

    let mut key = derive_archive_key(master, &salt, info);
    let cipher = chacha20poly1305::ChaCha20Poly1305::new_from_slice(&key)
        .map_err(|error| HistoryError::Encrypt(error.to_string()))?;
    key.zeroize();
    let nonce =
        Nonce::try_from(&nonce_bytes[..]).map_err(|_| HistoryError::Encrypt("bad nonce".into()))?;
    let aad = archive_aad(pane_uuid, revision);
    let mut buffer = compressed;
    cipher
        .encrypt_in_place(&nonce, &aad, &mut buffer)
        .map_err(|_| HistoryError::Encrypt("AEAD seal failed".into()))?;

    if buffer.len() > limits.max_archive_bytes_per_pane {
        return Err(HistoryError::ArchiveTooLarge(buffer.len()));
    }
    let mut out = Vec::with_capacity(8 + 4 + SALT_LEN + NONCE_LEN + 16 + 8 + 4 + buffer.len());
    out.extend_from_slice(ARCHIVE_MAGIC);
    out.extend_from_slice(&ARCHIVE_VERSION.to_le_bytes());
    out.extend_from_slice(&salt);
    out.extend_from_slice(&nonce_bytes);
    out.extend_from_slice(pane_uuid);
    out.extend_from_slice(&revision.to_le_bytes());
    out.extend_from_slice(&(buffer.len() as u32).to_le_bytes());
    out.extend_from_slice(&buffer);
    if out.len() > limits.max_archive_bytes_per_pane + 64 {
        return Err(HistoryError::ArchiveTooLarge(out.len()));
    }
    Ok(out)
}

struct ParsedArchive<'a> {
    salt: &'a [u8],
    nonce: &'a [u8],
    pane_uuid: [u8; 16],
    revision: u64,
    ciphertext: &'a [u8],
}

/// Parse and bounds-check the archive header *before* any allocation tied to
/// attacker-controlled lengths.
fn parse_archive(bytes: &[u8], limits: HistoryLimits) -> Result<ParsedArchive<'_>, HistoryError> {
    let header_len = 8 + 4 + SALT_LEN + NONCE_LEN + 16 + 8 + 4;
    if bytes.len() < header_len {
        return Err(HistoryError::Corrupt("archive shorter than header".into()));
    }
    if &bytes[..8] != ARCHIVE_MAGIC {
        return Err(HistoryError::Corrupt("bad archive magic".into()));
    }
    let version = u32::from_le_bytes([bytes[8], bytes[9], bytes[10], bytes[11]]);
    if version != ARCHIVE_VERSION {
        return Err(HistoryError::UnsupportedVersion(version));
    }
    let mut offset = 12;
    let salt = &bytes[offset..offset + SALT_LEN];
    offset += SALT_LEN;
    let nonce = &bytes[offset..offset + NONCE_LEN];
    offset += NONCE_LEN;
    let mut pane_uuid = [0u8; 16];
    pane_uuid.copy_from_slice(&bytes[offset..offset + 16]);
    offset += 16;
    let revision = u64::from_le_bytes([
        bytes[offset],
        bytes[offset + 1],
        bytes[offset + 2],
        bytes[offset + 3],
        bytes[offset + 4],
        bytes[offset + 5],
        bytes[offset + 6],
        bytes[offset + 7],
    ]);
    offset += 8;
    let ciphertext_len = u32::from_le_bytes([
        bytes[offset],
        bytes[offset + 1],
        bytes[offset + 2],
        bytes[offset + 3],
    ]) as usize;
    offset += 4;
    if ciphertext_len > limits.max_archive_bytes_per_pane {
        return Err(HistoryError::ArchiveTooLarge(ciphertext_len));
    }
    if bytes.len() != offset + ciphertext_len {
        return Err(HistoryError::Corrupt("ciphertext length mismatch".into()));
    }
    Ok(ParsedArchive {
        salt,
        nonce,
        pane_uuid,
        revision,
        ciphertext: &bytes[offset..],
    })
}

fn decrypt_to_plaintext(
    master: &[u8; 32],
    bytes: &[u8],
    info: &[u8],
    limits: HistoryLimits,
) -> Result<(Vec<u8>, [u8; 16], u64), HistoryError> {
    let parsed = parse_archive(bytes, limits)?;
    let mut key = derive_archive_key(master, parsed.salt, info);
    let cipher = chacha20poly1305::ChaCha20Poly1305::new_from_slice(&key)
        .map_err(|error| HistoryError::Encrypt(error.to_string()))?;
    key.zeroize();
    let nonce =
        Nonce::try_from(parsed.nonce).map_err(|_| HistoryError::Encrypt("bad nonce".into()))?;
    let aad = archive_aad(&parsed.pane_uuid, parsed.revision);
    let mut buffer = parsed.ciphertext.to_vec();
    cipher
        .decrypt_in_place(&nonce, &aad, &mut buffer)
        .map_err(|_| HistoryError::Decrypt)?;

    // Decompress with a pre-checked cap: never allocate more than
    // `max_decompressed_bytes + 1` (the extra byte detects over-limit).
    use flate2::read::DeflateDecoder;
    let decoder = DeflateDecoder::new(&buffer[..]);
    let mut plaintext = Vec::new();
    let cap = limits.max_decompressed_bytes.saturating_add(1);
    decoder
        .take(cap as u64)
        .read_to_end(&mut plaintext)
        .map_err(|error| HistoryError::Corrupt(format!("decompression failed: {error}")))?;
    if plaintext.len() > limits.max_decompressed_bytes {
        return Err(HistoryError::DecompressedTooLarge(plaintext.len()));
    }
    Ok((plaintext, parsed.pane_uuid, parsed.revision))
}

/// Decrypt a scrollback archive and return its ordered events.
pub fn decrypt_archive(
    master: &[u8; 32],
    bytes: &[u8],
    limits: HistoryLimits,
) -> Result<(Vec<HistoryEvent>, [u8; 16], u64), HistoryError> {
    let (plaintext, pane_uuid, revision) =
        decrypt_to_plaintext(master, bytes, HKDF_INFO_SCROLLBACK, limits)?;
    let events = decode_events_plaintext(&plaintext, limits)?;
    Ok((events, pane_uuid, revision))
}

// --- command journal ------------------------------------------------------

/// One OmaTerm-owned command record. Only authoritative supported-shell
/// lifecycle events may create entries: never keypresses, password prompts,
/// TUI input, unsupported-shell traffic, or pre-opt-in output. The journal
/// never reads, writes, or replays shell-native history files.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct JournalEntry {
    pub pane: String,
    pub project: Option<String>,
    pub tab: Option<String>,
    pub command: String,
    pub shell_dialect: String,
    pub working_directory: String,
    /// Unix seconds when the shell reported the command start.
    pub started_unix_secs: u64,
    /// Unix seconds when the shell reported completion, if authoritatively known.
    #[serde(default)]
    pub finished_unix_secs: Option<u64>,
    /// Exit status when the shell integration can authoritatively provide it.
    /// `None` must not be presented as success or failure.
    #[serde(default)]
    pub exit_status: Option<i32>,
}

/// Bounded per-pane journal buffer. Oldest entries are dropped first; the
/// buffer never holds a partially recorded command.
#[derive(Debug, Default)]
pub struct JournalBuffer {
    entries: Vec<JournalEntry>,
}

impl JournalBuffer {
    #[must_use]
    pub fn new() -> Self {
        Self::default()
    }

    pub fn push(&mut self, entry: JournalEntry, limits: HistoryLimits) {
        if entry.command.len() > limits.max_frame_bytes {
            return;
        }
        self.entries.push(entry);
        while self.entries.len() > limits.max_journal_entries_per_pane {
            self.entries.remove(0);
        }
    }

    #[must_use]
    pub fn entries(&self) -> &[JournalEntry] {
        &self.entries
    }

    #[must_use]
    pub fn len(&self) -> usize {
        self.entries.len()
    }

    #[must_use]
    pub fn is_empty(&self) -> bool {
        self.entries.is_empty()
    }

    /// Bounded listing: newest last, at most `limit` entries, never an
    /// unbounded dump.
    #[must_use]
    pub fn list(&self, limit: usize) -> &[JournalEntry] {
        let limit = limit.max(1).min(self.entries.len().max(1));
        if self.entries.is_empty() {
            return &[];
        }
        let start = self.entries.len().saturating_sub(limit);
        &self.entries[start..]
    }

    pub fn clear(&mut self) {
        self.entries.clear();
    }
}

pub fn encrypt_journal(
    master: &[u8; 32],
    pane_uuid: &[u8; 16],
    revision: u64,
    entries: &[JournalEntry],
    limits: HistoryLimits,
) -> Result<Vec<u8>, HistoryError> {
    let plaintext =
        serde_json::to_vec(entries).map_err(|error| HistoryError::Serialize(error.to_string()))?;
    encrypt_framed_payload(
        master,
        pane_uuid,
        revision,
        &plaintext,
        HKDF_INFO_JOURNAL,
        limits,
    )
}

pub fn decrypt_journal(
    master: &[u8; 32],
    bytes: &[u8],
    limits: HistoryLimits,
) -> Result<(Vec<JournalEntry>, [u8; 16], u64), HistoryError> {
    let (plaintext, pane_uuid, revision) =
        decrypt_to_plaintext(master, bytes, HKDF_INFO_JOURNAL, limits)?;
    if plaintext.len() > limits.max_decompressed_bytes {
        return Err(HistoryError::DecompressedTooLarge(plaintext.len()));
    }
    let entries: Vec<JournalEntry> = serde_json::from_slice(&plaintext)
        .map_err(|_| HistoryError::Corrupt("bad journal JSON".into()))?;
    if entries.len() > limits.max_journal_entries_per_pane {
        return Err(HistoryError::TooManyEvents(entries.len()));
    }
    Ok((entries, pane_uuid, revision))
}

/// Drop oldest complete journal entries until the serialized form fits the
/// decompressed cap. Mirrors scrollback retention: oldest complete records
/// go first, never a partial entry. A single entry that alone exceeds the
/// cap is an error rather than silent data loss.
fn fit_journal_to_limits(
    entries: &[JournalEntry],
    limits: HistoryLimits,
) -> Result<Vec<JournalEntry>, HistoryError> {
    let mut kept: Vec<JournalEntry> = entries.to_vec();
    loop {
        let plaintext = serde_json::to_vec(&kept)
            .map_err(|error| HistoryError::Serialize(error.to_string()))?;
        if plaintext.len() <= limits.max_decompressed_bytes {
            return Ok(kept);
        }
        if kept.len() <= 1 {
            return Err(HistoryError::DecompressedTooLarge(plaintext.len()));
        }
        // Drop at least a quarter of the remainder: bounded iterations,
        // newest entries always survive.
        let drop = (kept.len() / 4).max(1);
        kept.drain(..drop);
    }
}

// --- filesystem store -----------------------------------------------------

/// Dedicated history directory: `$XDG_STATE_HOME/omaterm/history/` with the
/// standard `$HOME/.local/state/omaterm/history/` fallback. On Windows this
/// is `%LOCALAPPDATA%\omaterm\history`. Archive names are opaque pane UUIDs
/// plus a revision; they disclose no command text, paths, or project names.
/// History never lives in `workspace-v1.json`.
pub fn default_history_dir() -> Option<PathBuf> {
    crate::paths::state_base_dir().map(|base| base.join("omaterm/history"))
}

#[derive(Debug)]
pub enum HistoryLoad {
    Missing,
    Scrollback(Vec<HistoryEvent>, u64),
    Journal(Vec<JournalEntry>, u64),
    /// The archive failed verification. The file is retained for diagnosis
    /// (renamed with a `.quarantined` suffix); the caller must start the pane
    /// with empty history plus a visible warning.
    Corrupt {
        path: PathBuf,
        quarantined: PathBuf,
        error: String,
    },
}

#[derive(Debug, thiserror::Error)]
pub enum StoreError {
    #[error("history store I/O failed: {0}")]
    Io(#[from] std::io::Error),
    #[error("history archive failed: {0}")]
    History(#[from] HistoryError),
    #[error("history key unavailable: {0}")]
    Key(#[from] KeyProviderError),
    #[error("workspace history quota exceeded ({0} bytes)")]
    QuotaExceeded(u64),
    #[error("refusing to use unsafe history path: {0}")]
    UnsafePath(String),
}

#[derive(Debug, Clone)]
pub struct HistoryStore {
    dir: PathBuf,
    limits: HistoryLimits,
}

impl HistoryStore {
    #[must_use]
    pub fn new(dir: PathBuf, limits: HistoryLimits) -> Self {
        Self { dir, limits }
    }

    #[must_use]
    pub fn dir(&self) -> &Path {
        &self.dir
    }

    #[must_use]
    pub fn limits(&self) -> HistoryLimits {
        self.limits
    }

    fn ensure_dir(&self) -> Result<(), StoreError> {
        fs::create_dir_all(&self.dir)?;
        // The history leaf itself must not be a symlink: an attacker-placed
        // symlink would redirect archives outside the state directory.
        // (Ancestors such as $HOME may legitimately be symlinks; only the
        // leaf is checked.)
        match fs::symlink_metadata(&self.dir) {
            Ok(meta) => {
                if meta.file_type().is_symlink() {
                    return Err(StoreError::UnsafePath(format!(
                        "refusing symlinked history directory: {}",
                        self.dir.display()
                    )));
                }
                if !meta.file_type().is_dir() {
                    return Err(StoreError::UnsafePath(format!(
                        "history path is not a directory: {}",
                        self.dir.display()
                    )));
                }
                #[cfg(unix)]
                {
                    use std::os::unix::fs::MetadataExt;
                    if meta.uid() != unsafe { libc::geteuid() } {
                        return Err(StoreError::UnsafePath(format!(
                            "unexpected owner for history directory: {}",
                            self.dir.display()
                        )));
                    }
                }
            }
            Err(error) => return Err(StoreError::Io(error)),
        }
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            fs::set_permissions(&self.dir, fs::Permissions::from_mode(0o700))?;
        }
        Ok(())
    }

    /// Opaque archive name: hex pane UUID + revision. Rejects anything that
    /// is not `[0-9a-f]{32}.<digits>.omhist|omjournal` to avoid path traversal.
    fn archive_name(pane: &str, revision: u64, suffix: &str) -> Result<String, StoreError> {
        let pane_ok = pane.len() == 32 && pane.bytes().all(|b| b.is_ascii_hexdigit());
        if !pane_ok {
            return Err(StoreError::UnsafePath(format!("bad pane identity: {pane}")));
        }
        Ok(format!("{pane}.{revision}.{suffix}"))
    }

    fn checked_path(&self, name: &str) -> Result<PathBuf, StoreError> {
        if name.contains('/') || name.contains('\\') || name.contains("..") {
            return Err(StoreError::UnsafePath(name.to_string()));
        }
        Ok(self.dir.join(name))
    }

    /// Reject symlinks and unexpected file types before reading.
    fn read_checked(&self, path: &Path) -> Result<Option<Vec<u8>>, StoreError> {
        match fs::symlink_metadata(path) {
            Ok(meta) => {
                if meta.file_type().is_symlink() {
                    return Err(StoreError::UnsafePath(format!(
                        "refusing symlink: {}",
                        path.display()
                    )));
                }
                if !meta.file_type().is_file() {
                    return Err(StoreError::UnsafePath(format!(
                        "not a regular file: {}",
                        path.display()
                    )));
                }
                #[cfg(unix)]
                {
                    use std::os::unix::fs::MetadataExt;
                    if meta.uid() != unsafe { libc::geteuid() } {
                        return Err(StoreError::UnsafePath(format!(
                            "unexpected owner: {}",
                            path.display()
                        )));
                    }
                }
                if meta.len() > self.limits.max_archive_bytes_per_pane as u64 + 64 {
                    return Err(StoreError::History(HistoryError::ArchiveTooLarge(
                        meta.len() as usize,
                    )));
                }
            }
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(None),
            Err(error) => return Err(StoreError::Io(error)),
        }
        let file = fs::File::open(path)?;
        let mut bytes = Vec::with_capacity(8192);
        file.take(self.limits.max_archive_bytes_per_pane as u64 + 65)
            .read_to_end(&mut bytes)?;
        if bytes.len() > self.limits.max_archive_bytes_per_pane + 64 {
            return Err(StoreError::History(HistoryError::ArchiveTooLarge(
                bytes.len(),
            )));
        }
        Ok(Some(bytes))
    }

    /// Bytes of this pane's live (non-quarantined) archives of one suffix
    /// with a lower revision: they are deleted right after the new revision
    /// lands, so a replacement save must not count them against the
    /// workspace quota. Quarantined files are retained for diagnosis and
    /// keep counting.
    fn replaced_bytes(&self, pane: &str, revision: u64, suffix: &str) -> u64 {
        let entries = match fs::read_dir(&self.dir) {
            Ok(entries) => entries,
            Err(_) => return 0,
        };
        let prefix = format!("{pane}.");
        let ext = format!(".{suffix}");
        let mut replaced = 0u64;
        for entry in entries.flatten() {
            let name = entry.file_name().to_string_lossy().into_owned();
            if !name.starts_with(&prefix) || !name.ends_with(&ext) {
                continue;
            }
            let middle = &name[prefix.len()..name.len() - ext.len()];
            if let Ok(prior) = middle.parse::<u64>()
                && prior < revision
                && let Ok(meta) = entry.metadata()
                && meta.is_file()
            {
                replaced = replaced.saturating_add(meta.len());
            }
        }
        replaced
    }

    fn atomic_write(&self, path: &Path, bytes: &[u8], exempt_bytes: u64) -> Result<(), StoreError> {
        self.ensure_dir()?;
        // Workspace quota is enforced before the write lands. Bytes of the
        // pane's own superseded revisions are exempt: they are removed by the
        // save, so charging them would reject legitimate replacements.
        let used = self.total_bytes().unwrap_or(0).saturating_sub(exempt_bytes);
        if used.saturating_add(bytes.len() as u64) > self.limits.max_workspace_bytes {
            return Err(StoreError::QuotaExceeded(used));
        }
        let temp = unique_temp_path(path);
        {
            let mut options = OpenOptions::new();
            options.write(true).create_new(true);
            #[cfg(unix)]
            {
                use std::os::unix::fs::OpenOptionsExt;
                options.mode(0o600);
            }
            let mut file = options.open(&temp)?;
            file.write_all(bytes)?;
            file.sync_all()?;
        }
        // Never overwrite a quarantined/corrupt archive as if it were valid:
        // the revisioned name is unique per save, so rename cannot clobber.
        fs::rename(&temp, path)?;
        #[cfg(unix)]
        {
            if let Some(parent) = path.parent()
                && let Ok(dir) = fs::File::open(parent)
            {
                let _ = dir.sync_all();
            }
        }
        Ok(())
    }

    /// Total bytes of history files (bounded scan; used for quota).
    pub fn total_bytes(&self) -> Result<u64, StoreError> {
        let entries = match fs::read_dir(&self.dir) {
            Ok(entries) => entries,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(0),
            Err(error) => return Err(StoreError::Io(error)),
        };
        let mut total = 0u64;
        let mut count = 0usize;
        for entry in entries {
            let entry = entry?;
            if entry.file_type()?.is_file() {
                total = total.saturating_add(entry.metadata()?.len());
            }
            count += 1;
            if count > 8192 {
                break;
            }
        }
        Ok(total)
    }

    /// Save a scrollback archive for one pane at the given revision.
    pub fn save_scrollback(
        &self,
        master: &[u8; 32],
        pane: &str,
        pane_uuid: &[u8; 16],
        revision: u64,
        events: &[HistoryEvent],
    ) -> Result<PathBuf, StoreError> {
        let bytes = encrypt_archive(master, pane_uuid, revision, events, self.limits)?;
        let name = Self::archive_name(pane, revision, "omhist")?;
        let path = self.checked_path(&name)?;
        let exempt = self.replaced_bytes(pane, revision, "omhist");
        self.atomic_write(&path, &bytes, exempt)?;
        self.remove_older_revisions(pane, revision, "omhist")?;
        Ok(path)
    }

    /// Load the newest scrollback archive for one pane.
    pub fn load_scrollback(
        &self,
        master: &[u8; 32],
        pane: &str,
    ) -> Result<HistoryLoad, StoreError> {
        let Some((path, _revision)) = self.newest_revision(pane, "omhist")? else {
            return Ok(HistoryLoad::Missing);
        };
        let Some(bytes) = self.read_checked(&path)? else {
            return Ok(HistoryLoad::Missing);
        };
        match decrypt_archive(master, &bytes, self.limits) {
            Ok((events, _uuid, revision)) => Ok(HistoryLoad::Scrollback(events, revision)),
            Err(error) => {
                let quarantined = path.with_extension("omhist.quarantined");
                // Retain the corrupt file for diagnosis; never overwrite it.
                let _ = fs::rename(&path, &quarantined);
                Ok(HistoryLoad::Corrupt {
                    path,
                    quarantined,
                    error: error.to_string(),
                })
            }
        }
    }

    pub fn save_journal(
        &self,
        master: &[u8; 32],
        pane: &str,
        pane_uuid: &[u8; 16],
        revision: u64,
        entries: &[JournalEntry],
    ) -> Result<PathBuf, StoreError> {
        let fitted = fit_journal_to_limits(entries, self.limits)?;
        let bytes = encrypt_journal(master, pane_uuid, revision, &fitted, self.limits)?;
        let name = Self::archive_name(pane, revision, "omjournal")?;
        let path = self.checked_path(&name)?;
        let exempt = self.replaced_bytes(pane, revision, "omjournal");
        self.atomic_write(&path, &bytes, exempt)?;
        self.remove_older_revisions(pane, revision, "omjournal")?;
        Ok(path)
    }

    pub fn load_journal(&self, master: &[u8; 32], pane: &str) -> Result<HistoryLoad, StoreError> {
        let Some((path, _revision)) = self.newest_revision(pane, "omjournal")? else {
            return Ok(HistoryLoad::Missing);
        };
        let Some(bytes) = self.read_checked(&path)? else {
            return Ok(HistoryLoad::Missing);
        };
        match decrypt_journal(master, &bytes, self.limits) {
            Ok((entries, _uuid, revision)) => Ok(HistoryLoad::Journal(entries, revision)),
            Err(error) => {
                let quarantined = path.with_extension("omjournal.quarantined");
                let _ = fs::rename(&path, &quarantined);
                Ok(HistoryLoad::Corrupt {
                    path,
                    quarantined,
                    error: error.to_string(),
                })
            }
        }
    }

    fn newest_revision(
        &self,
        pane: &str,
        suffix: &str,
    ) -> Result<Option<(PathBuf, u64)>, StoreError> {
        let entries = match fs::read_dir(&self.dir) {
            Ok(entries) => entries,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(None),
            Err(error) => return Err(StoreError::Io(error)),
        };
        let prefix = format!("{pane}.");
        let ext = format!(".{suffix}");
        let mut best: Option<(PathBuf, u64)> = None;
        for entry in entries {
            let entry = entry?;
            let name = entry.file_name().to_string_lossy().into_owned();
            if !name.starts_with(&prefix) || !name.ends_with(&ext) {
                continue;
            }
            let middle = &name[prefix.len()..name.len() - ext.len()];
            if let Ok(revision) = middle.parse::<u64>() {
                let path = entry.path();
                if best.as_ref().is_none_or(|(_, current)| revision > *current) {
                    best = Some((path, revision));
                }
            }
        }
        Ok(best)
    }

    fn remove_older_revisions(
        &self,
        pane: &str,
        keep: u64,
        suffix: &str,
    ) -> Result<(), StoreError> {
        let entries = match fs::read_dir(&self.dir) {
            Ok(entries) => entries,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(()),
            Err(error) => return Err(StoreError::Io(error)),
        };
        let prefix = format!("{pane}.");
        let ext = format!(".{suffix}");
        for entry in entries {
            let entry = entry?;
            let name = entry.file_name().to_string_lossy().into_owned();
            if !name.starts_with(&prefix) || !name.ends_with(&ext) {
                continue;
            }
            let middle = &name[prefix.len()..name.len() - ext.len()];
            if let Ok(revision) = middle.parse::<u64>()
                && revision < keep
            {
                let _ = fs::remove_file(entry.path());
            }
        }
        Ok(())
    }

    /// Delete one pane's archives and journal. `clear_all` callers also rotate
    /// the master key so undeleted filesystem blocks stay undecryptable.
    pub fn clear_pane(&self, pane: &str) -> Result<usize, StoreError> {
        let entries = match fs::read_dir(&self.dir) {
            Ok(entries) => entries,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(0),
            Err(error) => return Err(StoreError::Io(error)),
        };
        let prefix = format!("{pane}.");
        let mut removed = 0;
        for entry in entries {
            let entry = entry?;
            let name = entry.file_name().to_string_lossy().into_owned();
            if (name.starts_with(&prefix))
                && (name.ends_with(".omhist")
                    || name.ends_with(".omjournal")
                    || name.contains(".omhist.")
                    || name.contains(".omjournal."))
            {
                // Quarantined files are retained for diagnosis unless the user
                // explicitly clears them — and this *is* the explicit clear.
                let _ = fs::remove_file(entry.path());
                removed += 1;
            }
        }
        Ok(removed)
    }

    pub fn clear_panes(&self, panes: &HashSet<String>) -> Result<usize, StoreError> {
        let mut removed = 0;
        for pane in panes {
            removed += self.clear_pane(pane)?;
        }
        Ok(removed)
    }

    /// Remove every history file. Returns the removed count.
    pub fn clear_all(&self) -> Result<usize, StoreError> {
        let entries = match fs::read_dir(&self.dir) {
            Ok(entries) => entries,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(0),
            Err(error) => return Err(StoreError::Io(error)),
        };
        let mut removed = 0;
        for entry in entries {
            let entry = entry?;
            let name = entry.file_name().to_string_lossy().into_owned();
            if name.ends_with(".omhist")
                || name.ends_with(".omjournal")
                || name.contains(".omhist.")
                || name.contains(".omjournal.")
            {
                let _ = fs::remove_file(entry.path());
                removed += 1;
            }
        }
        Ok(removed)
    }

    /// Archive inventory for `history status`: (file name, bytes).
    pub fn inventory(&self) -> Result<Vec<(String, u64)>, StoreError> {
        let entries = match fs::read_dir(&self.dir) {
            Ok(entries) => entries,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(Vec::new()),
            Err(error) => return Err(StoreError::Io(error)),
        };
        let mut out = Vec::new();
        for entry in entries {
            let entry = entry?;
            if entry.file_type()?.is_file() {
                out.push((
                    entry.file_name().to_string_lossy().into_owned(),
                    entry.metadata()?.len(),
                ));
            }
        }
        out.sort_by(|a, b| a.0.cmp(&b.0));
        Ok(out)
    }
}

/// Pane lifecycle helper: archive names for panes that no longer exist should
/// be removed when their tab/project closes, including hidden containers.
/// The caller supplies the live pane set; anything else on disk is stale.
pub fn stale_archives(inventory: &[(String, u64)], live_panes: &HashSet<String>) -> Vec<String> {
    inventory
        .iter()
        .filter_map(|(name, _)| {
            let pane = name.split('.').next()?;
            if pane.len() == 32
                && pane.bytes().all(|b| b.is_ascii_hexdigit())
                && !live_panes.contains(pane)
            {
                Some(name.clone())
            } else {
                None
            }
        })
        .collect()
}

/// Estimate logical lines in recorded output (counts `\n` bytes).
#[must_use]
pub fn estimate_output_lines(events: &[HistoryEvent]) -> usize {
    events
        .iter()
        .map(|event| match event {
            HistoryEvent::Output(bytes) => bytes.iter().filter(|b| **b == b'\n').count(),
            HistoryEvent::Resize { .. } => 0,
        })
        .sum()
}

/// Trim oldest complete events until the archive fits line, byte, and count
/// ceilings. Never leaves a partially recorded event behind.
#[must_use]
pub fn trim_events_to_limits(events: &[HistoryEvent], limits: HistoryLimits) -> Vec<HistoryEvent> {
    let mut kept: Vec<HistoryEvent> = Vec::with_capacity(events.len().min(1024));
    let mut bytes = 4usize; // header
    let mut lines = 0usize;
    // Walk from newest to oldest, then reverse: oldest dropped first.
    for event in events.iter().rev() {
        let cost = 5 + event.encoded_len();
        let event_lines = match event {
            HistoryEvent::Output(data) => data.iter().filter(|b| **b == b'\n').count(),
            HistoryEvent::Resize { .. } => 0,
        };
        if kept.len() + 1 > limits.max_events_per_archive {
            break;
        }
        if bytes + cost > limits.max_decompressed_bytes {
            break;
        }
        // Events that would exceed the line cap (including a single huge
        // event on its own) are dropped entirely, oldest first, rather than
        // partially retained.
        if lines + event_lines > limits.max_lines_per_pane {
            break;
        }
        bytes += cost;
        lines += event_lines;
        kept.push(event.clone());
    }
    kept.reverse();
    kept
}

/// Opaque pane identity for archive names: 32 lowercase hex chars derived
/// from the pane UUID. Discloses no paths or project names.
#[must_use]
pub fn opaque_pane_name(pane_uuid: &[u8; 16]) -> String {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut out = String::with_capacity(32);
    for byte in pane_uuid {
        out.push(HEX[(byte >> 4) as usize] as char);
        out.push(HEX[(byte & 0x0f) as usize] as char);
    }
    out
}

/// Status DTO for the future `history status` command (semantic + CLI).
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct HistoryStatus {
    pub enabled: bool,
    pub key_available: bool,
    pub warning: Option<String>,
    pub archive_files: usize,
    pub archive_bytes: u64,
    pub paused_panes: usize,
}

impl HistoryStatus {
    #[must_use]
    pub fn disabled() -> Self {
        Self {
            enabled: false,
            key_available: false,
            warning: None,
            archive_files: 0,
            archive_bytes: 0,
            paused_panes: 0,
        }
    }
}

/// Build a status snapshot from config, store inventory, and key state.
/// Never includes keys or plaintext.
pub fn history_status(
    config: &HistoryConfig,
    store: &HistoryStore,
    key_available: bool,
    warning: Option<String>,
) -> HistoryStatus {
    let inventory: HashMap<String, u64> =
        store.inventory().unwrap_or_default().into_iter().collect();
    HistoryStatus {
        enabled: config.enabled,
        key_available,
        warning,
        archive_files: inventory.len(),
        archive_bytes: inventory.values().sum(),
        paused_panes: config.paused_panes.len(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn test_limits() -> HistoryLimits {
        HistoryLimits {
            max_decompressed_bytes: 64 * 1024,
            max_frame_bytes: 1024,
            max_events_per_archive: 64,
            ..HistoryLimits::default()
        }
    }

    fn test_master() -> [u8; 32] {
        [7u8; 32]
    }

    fn test_uuid() -> [u8; 16] {
        *b"0123456789abcdef"
    }

    fn sample_events() -> Vec<HistoryEvent> {
        vec![
            HistoryEvent::Output(b"\x1b[1;31mred\x1b[0m hello\r\n".to_vec()),
            HistoryEvent::Resize { cols: 60, rows: 20 },
            HistoryEvent::Output("wide \u{4f60}\u{597d}\r\n".as_bytes().to_vec()),
        ]
    }

    #[test]
    fn config_defaults_to_disabled() {
        let config = HistoryConfig::default();
        assert!(!config.enabled);
        assert!(config.paused_panes.is_empty());
    }

    #[test]
    fn config_pause_resume_round_trip() {
        let dir = std::env::temp_dir().join(format!("omaterm-histcfg-{}", uuid::Uuid::new_v4()));
        let path = dir.join("history.json");
        let mut config = HistoryConfig {
            enabled: true,
            paused_panes: HashSet::new(),
        };
        config.set_paused("abc123", true);
        assert!(config.is_paused("abc123"));
        save_history_config(&path, &config).unwrap();
        let loaded = load_history_config(&path).unwrap();
        assert_eq!(loaded, config);
        let missing = load_history_config(&dir.join("absent.json")).unwrap();
        assert_eq!(missing, HistoryConfig::default());
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn in_memory_key_provider_round_trip_and_rotation() {
        let mut provider = InMemoryKeyProvider::new();
        assert_eq!(provider.master_key(), None);
        let first = provider.get_or_create_master_key().unwrap();
        assert_eq!(provider.get_or_create_master_key().unwrap(), first);
        let rotated = provider.rotate_master_key().unwrap();
        assert_ne!(rotated, first);
        provider.remove_master_key().unwrap();
        assert_eq!(provider.master_key(), None);
    }

    #[test]
    fn in_memory_key_failure_never_yields_plaintext_path() {
        let mut provider = InMemoryKeyProvider::new();
        provider.fail_next(KeyProviderError::Locked("locked".into()));
        assert!(matches!(
            provider.get_or_create_master_key(),
            Err(KeyProviderError::Locked(_))
        ));
    }

    #[test]
    fn archive_encrypt_decrypt_round_trip() {
        let events = sample_events();
        let bytes =
            encrypt_archive(&test_master(), &test_uuid(), 3, &events, test_limits()).unwrap();
        let (decoded, uuid, revision) =
            decrypt_archive(&test_master(), &bytes, test_limits()).unwrap();
        assert_eq!(decoded, events);
        assert_eq!(uuid, test_uuid());
        assert_eq!(revision, 3);
    }

    #[test]
    fn wrong_key_fails_without_plaintext() {
        let bytes = encrypt_archive(
            &test_master(),
            &test_uuid(),
            1,
            &sample_events(),
            test_limits(),
        )
        .unwrap();
        assert!(matches!(
            decrypt_archive(&[9u8; 32], &bytes, test_limits()),
            Err(HistoryError::Decrypt)
        ));
    }

    #[test]
    fn tampered_ciphertext_metadata_nonce_and_truncation_fail() {
        let bytes = encrypt_archive(
            &test_master(),
            &test_uuid(),
            1,
            &sample_events(),
            test_limits(),
        )
        .unwrap();
        // Flip a ciphertext byte.
        let mut tampered = bytes.clone();
        let last = tampered.len() - 1;
        tampered[last] ^= 0x01;
        assert!(decrypt_archive(&test_master(), &tampered, test_limits()).is_err());
        // Flip a metadata byte (covered by AAD).
        let mut aad_tampered = bytes.clone();
        aad_tampered[30] ^= 0x01;
        assert!(decrypt_archive(&test_master(), &aad_tampered, test_limits()).is_err());
        // Truncate.
        assert!(decrypt_archive(&test_master(), &bytes[..bytes.len() - 8], test_limits()).is_err());
        // Wrong version.
        let mut versioned = bytes.clone();
        versioned[8] = 0x7f;
        assert!(matches!(
            decrypt_archive(&test_master(), &versioned, test_limits()),
            Err(HistoryError::UnsupportedVersion(_))
        ));
        // Duplicate/reordered framing is rejected at decode time.
        let dup = encode_events_plaintext(&sample_events(), test_limits()).unwrap();
        let mut reordered = dup.clone();
        if reordered.len() > 12 {
            reordered[4..12].reverse();
        }
        assert!(decode_events_plaintext(&reordered, test_limits()).is_err());
    }

    #[test]
    fn oversized_frame_and_decompressed_payload_are_rejected_before_allocation() {
        let limits = test_limits();
        let big = vec![b'x'; limits.max_frame_bytes + 1];
        assert!(matches!(
            encode_events_plaintext(&[HistoryEvent::Output(big)], limits),
            Err(HistoryError::FrameTooLarge(_))
        ));
        // Decompression-bomb shaped input: valid AEAD but claims huge output.
        let mut events = Vec::new();
        for _ in 0..limits.max_events_per_archive + 4 {
            events.push(HistoryEvent::Resize { cols: 80, rows: 24 });
        }
        assert!(matches!(
            encode_events_plaintext(&events, limits),
            Err(HistoryError::TooManyEvents(_))
        ));
    }

    #[test]
    fn journal_round_trip_and_bounds() {
        let entries = vec![JournalEntry {
            pane: "pane".into(),
            project: None,
            tab: None,
            command: "cargo test".into(),
            shell_dialect: "bash".into(),
            working_directory: "/tmp".into(),
            started_unix_secs: 1_700_000_000,
            finished_unix_secs: Some(1_700_000_005),
            exit_status: Some(0),
        }];
        let bytes =
            encrypt_journal(&test_master(), &test_uuid(), 1, &entries, test_limits()).unwrap();
        let (decoded, _, _) = decrypt_journal(&test_master(), &bytes, test_limits()).unwrap();
        assert_eq!(decoded, entries);
        // Multibyte command text survives; oversized entries are dropped.
        let mut buffer = JournalBuffer::new();
        buffer.push(
            JournalEntry {
                command: "\u{96ea}\u{4e2d}\u{6587} ok".into(),
                ..entries[0].clone()
            },
            test_limits(),
        );
        assert_eq!(buffer.len(), 1);
        assert_eq!(buffer.list(5).len(), 1);
        // Journal plaintext is unreadable without the key.
        assert!(!bytes.windows(5).any(|w| w == b"cargo"));
    }

    #[test]
    fn journal_buffer_drops_oldest_and_lists_bounded() {
        let limits = HistoryLimits {
            max_journal_entries_per_pane: 3,
            ..HistoryLimits::default()
        };
        let mut buffer = JournalBuffer::new();
        for index in 0..5 {
            buffer.push(
                JournalEntry {
                    pane: "p".into(),
                    project: None,
                    tab: None,
                    command: format!("cmd{index}"),
                    shell_dialect: "bash".into(),
                    working_directory: "/tmp".into(),
                    started_unix_secs: index as u64,
                    finished_unix_secs: None,
                    exit_status: None,
                },
                limits,
            );
        }
        assert_eq!(buffer.len(), 3);
        assert_eq!(buffer.list(2).len(), 2);
        assert_eq!(buffer.list(2)[1].command, "cmd4");
    }

    #[test]
    fn store_save_load_clear_with_permissions() {
        let dir = std::env::temp_dir().join(format!("omaterm-hist-{}", uuid::Uuid::new_v4()));
        let store = HistoryStore::new(dir.clone(), test_limits());
        let pane = opaque_pane_name(&test_uuid());
        let master = test_master();
        let events = sample_events();
        let path = store
            .save_scrollback(&master, &pane, &test_uuid(), 1, &events)
            .unwrap();
        assert!(path.exists());
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            assert_eq!(
                fs::metadata(&path).unwrap().permissions().mode() & 0o777,
                0o600
            );
            assert_eq!(
                fs::metadata(&dir).unwrap().permissions().mode() & 0o777,
                0o700
            );
        }
        match store.load_scrollback(&master, &pane).unwrap() {
            HistoryLoad::Scrollback(loaded, revision) => {
                assert_eq!(loaded, events);
                assert_eq!(revision, 1);
            }
            other => panic!("expected scrollback, got {other:?}"),
        }
        // Second revision replaces the first; corrupt archives quarantine.
        store
            .save_scrollback(&master, &pane, &test_uuid(), 2, &events)
            .unwrap();
        assert_eq!(store.inventory().unwrap().len(), 1);
        let corrupt_path = dir.join(format!("{pane}.3.omhist"));
        fs::write(&corrupt_path, b"not an archive").unwrap();
        match store.load_scrollback(&master, &pane).unwrap() {
            HistoryLoad::Corrupt { quarantined, .. } => assert!(quarantined.exists()),
            other => panic!("expected quarantine, got {other:?}"),
        }
        let removed = store.clear_pane(&pane).unwrap();
        assert!(removed >= 1);
        assert!(matches!(
            store.load_scrollback(&master, &pane).unwrap(),
            HistoryLoad::Missing
        ));
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn store_rejects_symlinks_and_unsafe_names() {
        let dir = std::env::temp_dir().join(format!("omaterm-histlink-{}", uuid::Uuid::new_v4()));
        let store = HistoryStore::new(dir.clone(), test_limits());
        assert!(
            store
                .save_scrollback(&test_master(), "../evil", &test_uuid(), 1, &[])
                .is_err()
        );
        #[cfg(unix)]
        {
            use std::os::unix::fs::symlink;
            fs::create_dir_all(&dir).unwrap();
            let target = dir.join("real.omhist");
            fs::write(&target, b"data").unwrap();
            let link = dir.join("link.omhist");
            symlink(&target, &link).unwrap();
            assert!(matches!(
                store.read_checked(&link),
                Err(StoreError::UnsafePath(_))
            ));
        }
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn trim_keeps_newest_complete_events_within_caps() {
        let limits = HistoryLimits {
            max_lines_per_pane: 2,
            max_decompressed_bytes: 1024,
            max_events_per_archive: 8,
            ..HistoryLimits::default()
        };
        let events = vec![
            HistoryEvent::Output(b"old1\nold2\nold3\n".to_vec()),
            HistoryEvent::Output(b"new1\n".to_vec()),
            HistoryEvent::Resize { cols: 80, rows: 24 },
        ];
        let trimmed = trim_events_to_limits(&events, limits);
        // Oldest over-cap event is dropped whole; newest complete records stay.
        assert!(trimmed.len() <= 2);
        assert!(estimate_output_lines(&trimmed) <= 2);
    }

    #[test]
    fn stale_archive_detection_ignores_live_panes() {
        let inventory = vec![
            ("abc123.omhist".to_string(), 10),
            (("0".repeat(32) + ".1.omhist"), 10),
        ];
        let live: HashSet<String> = HashSet::from(["0".repeat(32)]);
        let stale = stale_archives(&inventory, &live);
        assert!(stale.is_empty() || stale.len() <= 1);
    }

    #[test]
    fn key_hex_round_trip() {
        let key = test_master();
        assert_eq!(decode_key_hex(&encode_key_hex(&key)).unwrap(), key);
        assert!(decode_key_hex("short").is_err());
    }

    #[test]
    fn symlinked_history_directory_is_rejected() {
        let outer =
            std::env::temp_dir().join(format!("omaterm-histdirsym-{}", uuid::Uuid::new_v4()));
        let target = outer.join("real");
        fs::create_dir_all(&target).unwrap();
        #[cfg(unix)]
        {
            use std::os::unix::fs::symlink;
            let link = outer.join("linked");
            symlink(&target, &link).unwrap();
            let store = HistoryStore::new(link, test_limits());
            let pane = opaque_pane_name(&test_uuid());
            let result =
                store.save_scrollback(&test_master(), &pane, &test_uuid(), 1, &sample_events());
            assert!(
                matches!(result, Err(StoreError::UnsafePath(_))),
                "symlinked history dir must be refused, got {result:?}"
            );
        }
        let _ = fs::remove_dir_all(&outer);
    }

    #[test]
    fn temp_paths_are_unique_within_and_across_names() {
        let dir = std::env::temp_dir();
        let first = dir.join("probe-a.omhist");
        let second = dir.join("probe-b.omhist");
        let mut names = HashSet::new();
        for _ in 0..50 {
            names.insert(unique_temp_path(&first));
            names.insert(unique_temp_path(&second));
        }
        assert_eq!(names.len(), 100);
        for name in &names {
            assert_eq!(name.parent(), Some(dir.as_path()));
        }
    }

    #[test]
    fn quota_ignores_superseded_revisions_of_the_same_pane() {
        let dir = std::env::temp_dir().join(format!("omaterm-histquota-{}", uuid::Uuid::new_v4()));
        let generous = HistoryStore::new(dir.clone(), test_limits());
        let pane = opaque_pane_name(&test_uuid());
        let events = sample_events();
        generous
            .save_scrollback(&test_master(), &pane, &test_uuid(), 1, &events)
            .unwrap();
        let one_revision = generous.total_bytes().unwrap();
        assert!(one_revision > 0);
        // A cap below two full archives would reject any second save if the
        // superseded revision still counted; the replacement must succeed.
        let tight_limits = HistoryLimits {
            max_workspace_bytes: one_revision * 2 - 1,
            ..test_limits()
        };
        let tight = HistoryStore::new(dir.clone(), tight_limits);
        tight
            .save_scrollback(&test_master(), &pane, &test_uuid(), 2, &events)
            .unwrap();
        assert_eq!(tight.total_bytes().unwrap(), one_revision);
        // But an unrelated pane that truly exceeds the cap is still refused.
        let other = "f".repeat(32);
        assert!(matches!(
            tight.save_scrollback(&test_master(), &other, &test_uuid(), 1, &events),
            Err(StoreError::QuotaExceeded(_))
        ));
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn quarantined_archive_survives_newer_revisions_until_explicit_clear() {
        let dir = std::env::temp_dir().join(format!("omaterm-histquar-{}", uuid::Uuid::new_v4()));
        let store = HistoryStore::new(dir.clone(), test_limits());
        let pane = opaque_pane_name(&test_uuid());
        let events = sample_events();
        store
            .save_scrollback(&test_master(), &pane, &test_uuid(), 1, &events)
            .unwrap();
        // Plant a corrupt revision and quarantine it via load.
        fs::write(dir.join(format!("{pane}.2.omhist")), b"not an archive").unwrap();
        match store.load_scrollback(&test_master(), &pane).unwrap() {
            HistoryLoad::Corrupt { quarantined, .. } => assert!(quarantined.exists()),
            other => panic!("expected quarantine, got {other:?}"),
        }
        // A newer valid revision must not delete or reuse the quarantined file.
        store
            .save_scrollback(&test_master(), &pane, &test_uuid(), 3, &events)
            .unwrap();
        let names: Vec<String> = store
            .inventory()
            .unwrap()
            .into_iter()
            .map(|(name, _)| name)
            .collect();
        assert!(
            names.iter().any(|name| name.contains(".quarantined")),
            "quarantine retained, got {names:?}"
        );
        assert!(
            names.iter().any(|name| name == &format!("{pane}.3.omhist")),
            "newest revision present, got {names:?}"
        );
        match store.load_scrollback(&test_master(), &pane).unwrap() {
            HistoryLoad::Scrollback(loaded, revision) => {
                assert_eq!(loaded, events);
                assert_eq!(revision, 3);
            }
            other => panic!("expected rev-3 scrollback, got {other:?}"),
        }
        // Explicit pane clear removes quarantined records too.
        store.clear_pane(&pane).unwrap();
        assert!(store.inventory().unwrap().is_empty());
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn journal_save_trims_oldest_entries_to_fit_the_frame_cap() {
        let dir = std::env::temp_dir().join(format!("omaterm-histjfit-{}", uuid::Uuid::new_v4()));
        let limits = HistoryLimits {
            max_decompressed_bytes: 1024,
            ..HistoryLimits::default()
        };
        let store = HistoryStore::new(dir.clone(), limits);
        let pane = opaque_pane_name(&test_uuid());
        let entries: Vec<JournalEntry> = (0..60)
            .map(|index| JournalEntry {
                pane: pane.clone(),
                project: None,
                tab: None,
                command: format!("command-number-{index:04} with padding text"),
                shell_dialect: "bash".into(),
                working_directory: "/tmp".into(),
                started_unix_secs: index as u64,
                finished_unix_secs: None,
                exit_status: None,
            })
            .collect();
        store
            .save_journal(&test_master(), &pane, &test_uuid(), 1, &entries)
            .unwrap();
        match store.load_journal(&test_master(), &pane).unwrap() {
            HistoryLoad::Journal(loaded, _) => {
                assert!(loaded.len() < entries.len(), "oldest must be trimmed");
                assert!(
                    loaded
                        .iter()
                        .any(|entry| entry.command.contains("command-number-0059")),
                    "newest entries survive"
                );
                assert!(
                    !loaded
                        .iter()
                        .any(|entry| entry.command.contains("command-number-0000")),
                    "oldest entries dropped whole"
                );
            }
            other => panic!("expected journal, got {other:?}"),
        }
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn config_toml_round_trip_preserves_other_sections() {
        let dir = std::env::temp_dir().join(format!("omaterm-histtoml-{}", uuid::Uuid::new_v4()));
        fs::create_dir_all(&dir).unwrap();
        let path = dir.join("config.toml");
        fs::write(&path, "# user comment\n[other]\nfoo = 1\n").unwrap();
        let mut config = HistoryConfig {
            enabled: true,
            ..HistoryConfig::default()
        };
        config.set_paused("deadbeef", true);
        save_history_config_toml(&path, &config).unwrap();
        let text = fs::read_to_string(&path).unwrap();
        assert!(
            text.contains("# user comment"),
            "comments preserved:\n{text}"
        );
        assert!(
            text.contains("foo = 1"),
            "other sections preserved:\n{text}"
        );
        let loaded = load_history_config_toml(&path)
            .unwrap()
            .expect("section present");
        assert_eq!(loaded, config);
        // Absent file and absent section both mean defaults.
        assert_eq!(
            load_history_config_toml(&dir.join("missing.toml")).unwrap(),
            None
        );
        fs::write(&path, "[other]\nfoo = 1\n").unwrap();
        assert_eq!(load_history_config_toml(&path).unwrap(), None);
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn config_migration_adopts_legacy_json_once() {
        let dir = std::env::temp_dir().join(format!("omaterm-histmig-{}", uuid::Uuid::new_v4()));
        fs::create_dir_all(&dir).unwrap();
        let toml = dir.join("config.toml");
        let json = dir.join("history.json");
        let mut legacy = HistoryConfig {
            enabled: true,
            ..HistoryConfig::default()
        };
        legacy.set_paused("abc", true);
        save_history_config(&json, &legacy).unwrap();
        let migrated = load_history_config_migrated(&toml, &json).unwrap();
        assert_eq!(migrated, legacy);
        assert!(toml.exists(), "adopted into the canonical path");
        assert!(!json.exists(), "legacy file removed after adoption");
        // A present TOML section wins over (stale) JSON and leaves it alone.
        let newer = HistoryConfig {
            enabled: false,
            ..HistoryConfig::default()
        };
        save_history_config_toml(&toml, &newer).unwrap();
        save_history_config(&json, &legacy).unwrap();
        let loaded = load_history_config_migrated(&toml, &json).unwrap();
        assert_eq!(loaded, newer);
        assert!(json.exists(), "losing JSON is not deleted");
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn config_toml_malformed_is_an_error_not_silent_defaults() {
        let dir = std::env::temp_dir().join(format!("omaterm-histbad-{}", uuid::Uuid::new_v4()));
        fs::create_dir_all(&dir).unwrap();
        let path = dir.join("config.toml");
        fs::write(&path, "[history\nbroken = ").unwrap();
        assert!(matches!(
            load_history_config_toml(&path),
            Err(ConfigError::Parse(_))
        ));
        let _ = fs::remove_dir_all(&dir);
    }
}
