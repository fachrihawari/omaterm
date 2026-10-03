//! Bounded native text-file I/O for the built-in editor (M19,
//! blueprint §34).
//!
//! Every path resolves through the M12 boundary seam before touching disk,
//! so traversal, absolute, and symlink escapes fail with `PathOutsideRoot`
//! exactly like file/git/diff reads. Size and line caps are enforced
//! using a metadata precheck and a capped reader, including files growing
//! concurrently. Line counts are checked on the bounded UTF-8 text.
//! Binary content (NUL byte) and invalid UTF-8 are rejected with a reason
//! rather than lossy silent conversion. Saves are atomic (same-directory
//! temp file + rename, preserving the existing permission bits) and carry
//! an expected on-disk revision so external changes block overwrite instead
//! of being silently clobbered.
//!
//! Phase B opens and saves existing files only; file creation is follow-up
//! scope. No GPUI dependency; no UI-thread blocking beyond one bounded
//! read/write per call (callers run this off-thread with cancellation).

use std::io::{Read, Write};
use std::path::{Path, PathBuf};
use std::time::SystemTime;

use sha2::{Digest, Sha256};

/// Largest accepted editor document in bytes. Mirrors
/// `omaterm_core::validation::MAX_EDITOR_BYTES`; enforced before allocation.
pub const MAX_EDITOR_BYTES: usize = 1024 * 1024;
/// Largest accepted editor document in lines. Mirrors
/// `omaterm_core::validation::MAX_EDITOR_LINES`; enforced while splitting.
pub const MAX_EDITOR_LINES: usize = 20_000;

/// First-delivery language set (M19 plan §12). Everything else is `Plain`
/// until the highlight pipeline proves more.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EditorLanguage {
    Rust,
    Markdown,
    Toml,
    Json,
    Bash,
    Plain,
}

impl EditorLanguage {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::Rust => "rust",
            Self::Markdown => "markdown",
            Self::Toml => "toml",
            Self::Json => "json",
            Self::Bash => "bash",
            Self::Plain => "plaintext",
        }
    }
}

/// Language detection by filename/extension, case-insensitive. Lossy text is
/// display-only here; identity always uses original path bytes elsewhere.
pub fn detect_language(path: &Path) -> EditorLanguage {
    let name = path
        .file_name()
        .map(|name| name.to_string_lossy().to_lowercase())
        .unwrap_or_default();
    if name == "dockerfile" || name == "makefile" {
        return EditorLanguage::Plain;
    }
    match path
        .extension()
        .and_then(|ext| ext.to_str())
        .map(|ext| ext.to_lowercase())
        .as_deref()
    {
        Some("rs") => EditorLanguage::Rust,
        Some("md" | "markdown") => EditorLanguage::Markdown,
        Some("toml") => EditorLanguage::Toml,
        Some("json") => EditorLanguage::Json,
        Some("sh" | "bash") => EditorLanguage::Bash,
        _ => EditorLanguage::Plain,
    }
}

/// On-disk revision captured on read and re-checked on save. Carries metadata
/// plus a SHA-256 digest of the validated bytes, so same-size in-place edits
/// with restored timestamps are detectable as observable conflicts.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct FileRevision {
    pub size: u64,
    pub mtime_secs: u64,
    pub mtime_nanos: u32,
    pub device: u64,
    pub inode: u64,
    pub content_digest: [u8; 32],
}

/// Stable identity of the root directory captured for an editor operation.
/// A pathname is presentation/debug information only: device and inode are
/// what let later slices distinguish a replacement at the same pathname.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub struct RootIdentity {
    pub device: u64,
    pub inode: u64,
}

/// Owned Linux project-root descriptor for secure editor I/O. All descendant
/// opening goes through this descriptor, never a re-resolved root pathname.
/// The caller retains the handle for the operation's lifetime so a renamed or
/// replaced root cannot retarget an already prepared operation.
#[cfg(target_os = "linux")]
#[derive(Debug)]
pub struct EditorRoot {
    canonical_path: PathBuf,
    identity: RootIdentity,
    directory: std::fs::File,
}

#[cfg(target_os = "linux")]
impl EditorRoot {
    /// Capture a root directory and its descriptor. The initial canonicalize
    /// only resolves the configured root once; descendant access uses the FD.
    pub fn open(root: &Path) -> Result<Self, EditorError> {
        use std::os::unix::fs::OpenOptionsExt;

        let canonical_path = std::fs::canonicalize(root).map_err(map_not_found)?;
        let mut options = std::fs::OpenOptions::new();
        options
            .read(true)
            .custom_flags(libc::O_DIRECTORY | libc::O_CLOEXEC | libc::O_NOFOLLOW);
        let directory = options.open(&canonical_path).map_err(map_not_found)?;
        let metadata = directory.metadata()?;
        if !metadata.is_dir() {
            return Err(EditorError::NotRegularFile);
        }
        use std::os::unix::fs::MetadataExt;
        Ok(Self {
            canonical_path,
            identity: RootIdentity {
                device: metadata.dev(),
                inode: metadata.ino(),
            },
            directory,
        })
    }

    pub fn canonical_path(&self) -> &Path {
        &self.canonical_path
    }

    pub const fn identity(&self) -> RootIdentity {
        self.identity
    }

    /// Open a relative descendant beneath the captured descriptor. Linux's
    /// `openat2` resolves all components under this root, allows contained
    /// symlink aliases, and rejects escapes/magic links atomically. There is
    /// intentionally no canonicalize/open fallback on unsupported kernels.
    pub fn open_descendant(&self, path: &Path) -> Result<std::fs::File, EditorError> {
        self.open_descendant_with_flags(path, libc::O_RDONLY | libc::O_NONBLOCK)
    }

    fn open_descendant_with_flags(
        &self,
        path: &Path,
        flags: libc::c_int,
    ) -> Result<std::fs::File, EditorError> {
        use std::ffi::CString;
        use std::os::fd::{AsRawFd, FromRawFd};
        use std::os::unix::ffi::OsStrExt;

        if path.as_os_str().is_empty() || path.is_absolute() {
            return Err(EditorError::PathOutsideRoot);
        }
        let path = CString::new(path.as_os_str().as_bytes()).map_err(|_| {
            EditorError::Io(std::io::Error::new(
                std::io::ErrorKind::InvalidInput,
                "editor path contains a NUL byte",
            ))
        })?;
        // SAFETY: `open_how` must be zero-initialized so future kernel fields
        // are zero; we immediately set the supported fields below.
        let mut how: libc::open_how = unsafe { std::mem::zeroed() };
        how.flags = (flags | libc::O_CLOEXEC) as u64;
        how.resolve = libc::RESOLVE_BENEATH | libc::RESOLVE_NO_MAGICLINKS;
        let raw = unsafe {
            libc::syscall(
                libc::SYS_openat2,
                self.directory.as_raw_fd(),
                path.as_ptr(),
                &how,
                std::mem::size_of::<libc::open_how>(),
            )
        };
        if raw < 0 {
            let error = std::io::Error::last_os_error();
            return match error.raw_os_error() {
                Some(libc::EXDEV) => Err(EditorError::PathOutsideRoot),
                // An unavailable or incompatible syscall must fail closed.
                Some(libc::ENOSYS | libc::E2BIG) => Err(EditorError::SecureResolutionUnavailable),
                Some(libc::ENOENT) => Err(EditorError::NotFound),
                _ => Err(EditorError::Io(error)),
            };
        }
        // The successful raw descriptor is owned exclusively by this File.
        Ok(unsafe { std::fs::File::from_raw_fd(raw as std::os::fd::RawFd) })
    }

    fn open_parent(&self, path: &Path) -> Result<(std::fs::File, std::ffi::CString), EditorError> {
        use std::ffi::CString;
        use std::os::unix::ffi::OsStrExt;

        if path.as_os_str().is_empty() || path.is_absolute() {
            return Err(EditorError::PathOutsideRoot);
        }
        let leaf = path.file_name().ok_or(EditorError::PathOutsideRoot)?;
        let leaf = CString::new(leaf.as_bytes()).map_err(|_| {
            EditorError::Io(std::io::Error::new(
                std::io::ErrorKind::InvalidInput,
                "editor path contains a NUL byte",
            ))
        })?;
        let parent = path.parent().unwrap_or_else(|| Path::new(""));
        let directory = if parent.as_os_str().is_empty() {
            self.directory.try_clone()?
        } else {
            self.open_descendant_with_flags(parent, libc::O_RDONLY | libc::O_DIRECTORY)?
        };
        if !directory.metadata()?.is_dir() {
            return Err(EditorError::NotRegularFile);
        }
        Ok((directory, leaf))
    }
}

impl FileRevision {
    fn of(metadata: &std::fs::Metadata, bytes: &[u8]) -> Self {
        #[cfg(unix)]
        let (device, inode) = {
            use std::os::unix::fs::MetadataExt;
            (metadata.dev(), metadata.ino())
        };
        #[cfg(not(unix))]
        let (device, inode) = (0, 0);
        let (secs, nanos) = metadata
            .modified()
            .ok()
            .and_then(|time| time.duration_since(SystemTime::UNIX_EPOCH).ok())
            .map(|span| (span.as_secs(), span.subsec_nanos()))
            .unwrap_or((0, 0));
        Self {
            size: metadata.len(),
            mtime_secs: secs,
            mtime_nanos: nanos,
            device,
            inode,
            content_digest: Sha256::digest(bytes).into(),
        }
    }
}

/// Bounded read result: validated text plus its on-disk revision and
/// detected language. Byte/line counts are exact, not estimates.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct EditorFile {
    pub text: String,
    pub bytes: usize,
    pub lines: usize,
    pub revision: FileRevision,
    pub language: EditorLanguage,
}

/// Exact result of an atomic descriptor-relative save. A directory-sync
/// failure occurs after rename: the new bytes committed, but their directory
/// metadata durability could not be confirmed.
#[derive(Debug)]
pub enum WriteTextOutcome {
    CommittedDurable {
        revision: FileRevision,
    },
    CommittedDurabilityWarning {
        revision: FileRevision,
        error: std::io::Error,
    },
}

impl WriteTextOutcome {
    pub const fn revision(&self) -> FileRevision {
        match self {
            Self::CommittedDurable { revision }
            | Self::CommittedDurabilityWarning { revision, .. } => *revision,
        }
    }

    pub const fn is_durable(&self) -> bool {
        matches!(self, Self::CommittedDurable { .. })
    }
}

/// Bounded editor I/O failures. Every variant carries a stable machine code
/// matching the `omaterm-core` wire strings so the dispatcher maps without
/// inspecting message text.
#[derive(Debug, thiserror::Error)]
pub enum EditorError {
    #[error("path escapes the project root")]
    PathOutsideRoot,
    #[error("file does not exist")]
    NotFound,
    #[error("path is not a regular file")]
    NotRegularFile,
    #[error("file exceeds the editor size cap")]
    TooLarge,
    #[error("file is not openable as text")]
    NotTextFile,
    #[error("file changed on disk since it was opened")]
    Conflict,
    #[error("secure descriptor-relative path resolution is unavailable")]
    SecureResolutionUnavailable,
    #[error(transparent)]
    Io(#[from] std::io::Error),
}

impl EditorError {
    pub const fn code(&self) -> &'static str {
        match self {
            Self::PathOutsideRoot => "path_outside_root",
            Self::NotFound => "file_not_found",
            Self::NotRegularFile => "invalid_request",
            Self::TooLarge => "document_too_large",
            Self::NotTextFile => "not_text_file",
            Self::Conflict => "document_conflict",
            Self::SecureResolutionUnavailable => "runtime_failure",
            Self::Io(_) => "io_error",
        }
    }
}

fn map_not_found(error: std::io::Error) -> EditorError {
    if error.kind() == std::io::ErrorKind::NotFound {
        EditorError::NotFound
    } else {
        EditorError::Io(error)
    }
}

fn count_lines(text: &str) -> usize {
    if text.is_empty() {
        0
    } else {
        text.matches('\n').count() + 1
    }
}

/// Resolve a user-supplied document path to its canonical absolute form.
/// Shared by reads, writes, and owner-side dedup identity so all three
/// agree on one resolution.
pub fn canonical_document_path(root: &Path, user_path: &Path) -> Result<PathBuf, EditorError> {
    match super::boundary::canonicalize_under_root(root, user_path) {
        Ok(path) => Ok(path),
        Err(super::boundary::ContextError::PathOutsideRoot) => Err(EditorError::PathOutsideRoot),
        Err(super::boundary::ContextError::Io(error))
            if error.kind() == std::io::ErrorKind::NotFound =>
        {
            Err(EditorError::NotFound)
        }
        Err(super::boundary::ContextError::Io(error)) => Err(EditorError::Io(error)),
    }
}

/// Read and validate one existing text file under `root`. Linux captures the
/// root descriptor once and resolves the descendant beneath it; other targets
/// retain the existing boundary implementation until their secure equivalent
/// is introduced.
#[cfg(target_os = "linux")]
pub fn read_text_file(root: &Path, user_path: &Path) -> Result<EditorFile, EditorError> {
    let root = EditorRoot::open(root)?;
    read_text_file_from_root(&root, user_path)
}

/// Read through an already captured Linux root descriptor. This is the S1
/// worker-facing API: a root replacement after capture cannot retarget it.
#[cfg(target_os = "linux")]
pub fn read_text_file_from_root(
    root: &EditorRoot,
    user_path: &Path,
) -> Result<EditorFile, EditorError> {
    read_open_file(root.open_descendant(user_path)?, user_path)
}

#[cfg(not(target_os = "linux"))]
pub fn read_text_file(root: &Path, user_path: &Path) -> Result<EditorFile, EditorError> {
    let absolute = canonical_document_path(root, user_path)?;
    read_canonical_file(&absolute, user_path)
}

#[cfg(not(target_os = "linux"))]
fn read_canonical_file(absolute: &Path, user_path: &Path) -> Result<EditorFile, EditorError> {
    let mut options = std::fs::OpenOptions::new();
    options.read(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.custom_flags(libc::O_NOFOLLOW | libc::O_NONBLOCK);
    }
    read_open_file(options.open(absolute)?, user_path)
}

fn read_open_file(file: std::fs::File, user_path: &Path) -> Result<EditorFile, EditorError> {
    let metadata = file.metadata()?;
    if !metadata.file_type().is_file() {
        return Err(EditorError::NotRegularFile);
    }
    if metadata.len() > MAX_EDITOR_BYTES as u64 {
        return Err(EditorError::TooLarge);
    }
    let bytes = read_bounded(&file)?;
    if bytes.len() > MAX_EDITOR_BYTES {
        return Err(EditorError::TooLarge);
    }
    if bytes.contains(&0) {
        return Err(EditorError::NotTextFile);
    }
    let text = String::from_utf8(bytes).map_err(|_| EditorError::NotTextFile)?;
    let lines = count_lines(&text);
    if lines > MAX_EDITOR_LINES {
        return Err(EditorError::TooLarge);
    }
    let revision = FileRevision::of(&file.metadata()?, text.as_bytes());
    if revision != FileRevision::of(&metadata, text.as_bytes()) {
        return Err(EditorError::Conflict);
    }
    Ok(EditorFile {
        bytes: text.len(),
        lines,
        language: detect_language(user_path),
        text,
        revision,
    })
}

fn read_bounded(reader: impl Read) -> Result<Vec<u8>, EditorError> {
    let mut bytes = Vec::new();
    reader
        .take(MAX_EDITOR_BYTES as u64 + 1)
        .read_to_end(&mut bytes)?;
    if bytes.len() > MAX_EDITOR_BYTES {
        return Err(EditorError::TooLarge);
    }
    Ok(bytes)
}

/// Atomically replace an existing text file using a freshly captured Linux
/// root descriptor. The final component must be a regular file, not a
/// symlink: renaming over a symlink would replace the alias rather than its
/// referent. Contained ancestor aliases remain supported.
#[cfg(target_os = "linux")]
pub fn write_text_file_from_root(
    root: &EditorRoot,
    user_path: &Path,
    text: &str,
    expected: Option<&FileRevision>,
) -> Result<WriteTextOutcome, EditorError> {
    use std::os::fd::AsRawFd;
    use std::os::unix::fs::{MetadataExt, PermissionsExt};

    if text.len() > MAX_EDITOR_BYTES || count_lines(text) > MAX_EDITOR_LINES {
        return Err(EditorError::TooLarge);
    }
    // This follows a contained alias but rejects an escaping one before the
    // no-follow target open below decides whether replacement is safe.
    let opened = read_text_file_from_root(root, user_path)?;
    let (parent, leaf) = root.open_parent(user_path)?;
    if let Some(expected) = expected
        && opened.revision != *expected
    {
        return Err(EditorError::Conflict);
    }
    let target = open_parent_file(&parent, &leaf, libc::O_RDONLY | libc::O_NONBLOCK)?;
    let metadata = target.metadata()?;
    if !metadata.file_type().is_file() {
        return Err(EditorError::NotRegularFile);
    }

    let temp_name = unique_sidecar_name();
    let mut temp = create_sidecar(&parent, &temp_name)?;
    let result = (|| {
        temp.write_all(text.as_bytes())?;
        temp.set_permissions(std::fs::Permissions::from_mode(metadata.mode()))?;
        temp.sync_all()?;

        // This detects observable changes before commit. It is intentionally
        // not described as a compare-and-swap against arbitrary writers.
        if read_parent_file(&parent, &leaf, user_path)?.revision != opened.revision {
            return Err(EditorError::Conflict);
        }
        let revision = FileRevision::of(&temp.metadata()?, text.as_bytes());
        let renamed = unsafe {
            libc::renameat(
                parent.as_raw_fd(),
                temp_name.as_ptr(),
                parent.as_raw_fd(),
                leaf.as_ptr(),
            )
        };
        if renamed != 0 {
            return Err(EditorError::Io(std::io::Error::last_os_error()));
        }
        match parent.sync_all() {
            Ok(()) => Ok(WriteTextOutcome::CommittedDurable { revision }),
            Err(error) => Ok(WriteTextOutcome::CommittedDurabilityWarning { revision, error }),
        }
    })();
    if result.is_err() {
        // The sidecar name is private and was created with O_EXCL. Cleanup is
        // descriptor-relative; never re-resolve a potentially replaced path.
        let _ = unsafe { libc::unlinkat(parent.as_raw_fd(), temp_name.as_ptr(), 0) };
    }
    result
}

#[cfg(target_os = "linux")]
fn open_parent_file(
    parent: &std::fs::File,
    name: &std::ffi::CStr,
    flags: libc::c_int,
) -> Result<std::fs::File, EditorError> {
    use std::os::fd::{AsRawFd, FromRawFd};

    let raw = unsafe {
        libc::openat(
            parent.as_raw_fd(),
            name.as_ptr(),
            flags | libc::O_CLOEXEC | libc::O_NOFOLLOW,
        )
    };
    if raw < 0 {
        let error = std::io::Error::last_os_error();
        return match error.raw_os_error() {
            Some(libc::ENOENT) => Err(EditorError::NotFound),
            // A final symlink cannot be atomically replaced without changing
            // alias semantics, so fail explicitly rather than replace it.
            Some(libc::ELOOP) => Err(EditorError::Conflict),
            _ => Err(EditorError::Io(error)),
        };
    }
    Ok(unsafe { std::fs::File::from_raw_fd(raw) })
}

#[cfg(target_os = "linux")]
fn read_parent_file(
    parent: &std::fs::File,
    name: &std::ffi::CStr,
    user_path: &Path,
) -> Result<EditorFile, EditorError> {
    read_open_file(
        open_parent_file(parent, name, libc::O_RDONLY | libc::O_NONBLOCK)?,
        user_path,
    )
}

#[cfg(target_os = "linux")]
fn create_sidecar(
    parent: &std::fs::File,
    name: &std::ffi::CStr,
) -> Result<std::fs::File, EditorError> {
    use std::os::fd::{AsRawFd, FromRawFd};

    let raw = unsafe {
        libc::openat(
            parent.as_raw_fd(),
            name.as_ptr(),
            libc::O_WRONLY | libc::O_CREAT | libc::O_EXCL | libc::O_CLOEXEC | libc::O_NOFOLLOW,
            0o600,
        )
    };
    if raw < 0 {
        return Err(EditorError::Io(std::io::Error::last_os_error()));
    }
    Ok(unsafe { std::fs::File::from_raw_fd(raw) })
}

#[cfg(target_os = "linux")]
fn unique_sidecar_name() -> std::ffi::CString {
    use std::sync::atomic::{AtomicU64, Ordering};

    static COUNTER: AtomicU64 = AtomicU64::new(0);
    let nonce = SystemTime::now()
        .duration_since(SystemTime::UNIX_EPOCH)
        .map(|span| span.subsec_nanos())
        .unwrap_or(0);
    let slot = COUNTER.fetch_add(1, Ordering::Relaxed);
    std::ffi::CString::new(format!(
        ".omaterm-editor-{}.{}.tmp",
        std::process::id(),
        (nonce as u64)
            .wrapping_mul(0x9E37_79B9_7F4A_7C15)
            .wrapping_add(slot),
    ))
    .expect("generated sidecar name contains no NUL")
}

/// Atomically replace one existing text file under `root`. On Linux this
/// captures one descriptor and delegates to the secure rooted implementation.
#[cfg(target_os = "linux")]
pub fn write_text_file(
    root: &Path,
    user_path: &Path,
    text: &str,
    expected: Option<&FileRevision>,
) -> Result<FileRevision, EditorError> {
    let root = EditorRoot::open(root)?;
    Ok(write_text_file_from_root(&root, user_path, text, expected)?.revision())
}

/// Non-Linux fallback retained until a descriptor-relative implementation is
/// available for that target.
#[cfg(not(target_os = "linux"))]
pub fn write_text_file(
    root: &Path,
    user_path: &Path,
    text: &str,
    expected: Option<&FileRevision>,
) -> Result<FileRevision, EditorError> {
    if text.len() > MAX_EDITOR_BYTES || count_lines(text) > MAX_EDITOR_LINES {
        return Err(EditorError::TooLarge);
    }
    let opened = read_text_file(root, user_path)?;
    if let Some(expected) = expected
        && opened.revision != *expected
    {
        return Err(EditorError::Conflict);
    }
    let absolute = canonical_document_path(root, user_path)?;
    let metadata = std::fs::symlink_metadata(&absolute)?;
    if !metadata.file_type().is_file() {
        return Err(EditorError::NotRegularFile);
    }
    let parent = absolute.parent().ok_or(EditorError::NotFound)?;
    let temp = unique_sibling(parent);
    let mut options = std::fs::OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    // Only unlink a sidecar after we successfully create it exclusively.
    let mut file = options.open(&temp)?;
    let result = (|| {
        file.write_all(text.as_bytes())?;
        file.set_permissions(metadata.permissions())?;
        file.sync_all()?;
        if canonical_document_path(root, user_path)? != absolute {
            return Err(EditorError::Conflict);
        }
        let current = read_text_file(root, user_path)?;
        if current.revision != opened.revision {
            return Err(EditorError::Conflict);
        }
        let revision = FileRevision::of(&file.metadata()?, text.as_bytes());
        std::fs::rename(&temp, &absolute)?;
        std::fs::File::open(parent)?.sync_all()?;
        Ok(revision)
    })();
    if result.is_err() {
        let _ = std::fs::remove_file(&temp);
    }
    result
}

#[cfg(not(target_os = "linux"))]
fn unique_sibling(dir: &Path) -> PathBuf {
    use std::sync::atomic::{AtomicU64, Ordering};
    static COUNTER: AtomicU64 = AtomicU64::new(0);
    let nonce = SystemTime::now()
        .duration_since(SystemTime::UNIX_EPOCH)
        .map(|span| span.subsec_nanos())
        .unwrap_or(0);
    let slot = COUNTER.fetch_add(1, Ordering::Relaxed);
    dir.join(format!(
        ".omaterm-editor-{}.{}.tmp",
        std::process::id(),
        (nonce as u64)
            .wrapping_mul(0x9E37_79B9_7F4A_7C15)
            .wrapping_add(slot),
    ))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn fixture_root(name: &str) -> PathBuf {
        let root =
            std::env::temp_dir().join(format!("omaterm-m19-editor-{}-{name}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir_all(&root).unwrap();
        root
    }

    fn write_fixture(root: &Path, name: &str, bytes: &[u8]) -> PathBuf {
        let path = root.join(name);
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent).unwrap();
        }
        std::fs::write(&path, bytes).unwrap();
        path
    }

    #[test]
    fn read_cap_is_enforced_even_if_a_reader_grows_after_metadata() {
        let mut source = std::io::repeat(b'x');
        assert!(matches!(
            read_bounded(&mut source),
            Err(EditorError::TooLarge)
        ));
        assert_eq!(
            read_bounded(std::io::repeat(b'x').take(MAX_EDITOR_BYTES as u64))
                .unwrap()
                .len(),
            MAX_EDITOR_BYTES
        );
    }

    #[test]
    #[cfg(unix)]
    fn saves_preserve_permissions_and_outside_symlinks_are_rejected() {
        use std::os::unix::fs::{PermissionsExt, symlink};
        let root = fixture_root("permissions");
        let path = write_fixture(&root, "doc.txt", b"before");
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o640)).unwrap();
        let opened = read_text_file(&root, Path::new("doc.txt")).unwrap();
        write_text_file(&root, Path::new("doc.txt"), "after", Some(&opened.revision)).unwrap();
        assert_eq!(
            std::fs::metadata(&path).unwrap().permissions().mode() & 0o777,
            0o640
        );
        symlink("/etc/passwd", root.join("outside")).unwrap();
        assert!(matches!(
            read_text_file(&root, Path::new("outside")),
            Err(EditorError::PathOutsideRoot)
        ));
        assert!(matches!(
            write_text_file(&root, Path::new("outside"), "no", None),
            Err(EditorError::PathOutsideRoot)
        ));
        std::fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn language_detection_covers_first_delivery_set() {
        for (name, language) in [
            ("main.rs", EditorLanguage::Rust),
            ("MAIN.RS", EditorLanguage::Rust),
            ("notes.md", EditorLanguage::Markdown),
            ("notes.markdown", EditorLanguage::Markdown),
            ("config.toml", EditorLanguage::Toml),
            ("data.json", EditorLanguage::Json),
            ("run.sh", EditorLanguage::Bash),
            ("Makefile", EditorLanguage::Plain),
            ("notes.txt", EditorLanguage::Plain),
            ("no-extension", EditorLanguage::Plain),
        ] {
            assert_eq!(detect_language(Path::new(name)), language, "{name}");
            assert_eq!(language.as_str(), language.as_str());
        }
    }

    #[test]
    fn small_and_unicode_files_read_with_metadata() {
        let root = fixture_root("read");
        write_fixture(&root, "a.md", "# Demo\n\nHello **world**.\n".as_bytes());
        write_fixture(&root, "u.txt", "Wide: 界 combined: é\n".as_bytes());
        let file = read_text_file(&root, Path::new("a.md")).unwrap();
        assert_eq!(file.language, EditorLanguage::Markdown);
        assert_eq!(file.bytes, "# Demo\n\nHello **world**.\n".len());
        assert_eq!(file.lines, 4);
        assert!(file.text.contains("world"));
        let unicode = read_text_file(&root, Path::new("u.txt")).unwrap();
        assert!(unicode.text.contains('界'));
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn binary_invalid_encoding_oversize_and_shapes_are_rejected() {
        let root = fixture_root("reject");
        write_fixture(&root, "ok.txt", b"hello\n");
        write_fixture(&root, "bin.dat", b"PK\x03\x04\x00\x00");
        write_fixture(&root, "bad.txt", b"ok\xff\xfenot-utf8\n");
        let big = vec![b'x'; MAX_EDITOR_BYTES + 1];
        write_fixture(&root, "big.txt", &big);
        let mut many = String::new();
        for index in 0..=MAX_EDITOR_LINES {
            many.push_str(&format!("line {index}\n"));
        }
        write_fixture(&root, "many.txt", many.as_bytes());
        std::fs::create_dir_all(root.join("dir")).unwrap();

        assert!(matches!(
            read_text_file(&root, Path::new("bin.dat")),
            Err(EditorError::NotTextFile)
        ));
        assert!(matches!(
            read_text_file(&root, Path::new("bad.txt")),
            Err(EditorError::NotTextFile)
        ));
        assert!(matches!(
            read_text_file(&root, Path::new("big.txt")),
            Err(EditorError::TooLarge)
        ));
        assert!(matches!(
            read_text_file(&root, Path::new("many.txt")),
            Err(EditorError::TooLarge)
        ));
        assert!(matches!(
            read_text_file(&root, Path::new("dir")),
            Err(EditorError::NotRegularFile)
        ));
        assert!(matches!(
            read_text_file(&root, Path::new("missing.txt")),
            Err(EditorError::NotFound)
        ));
        assert!(matches!(
            read_text_file(&root, Path::new("../outside.txt")),
            Err(EditorError::PathOutsideRoot) | Err(EditorError::NotFound)
        ));
        // Boundary oversize save is rejected before touching disk.
        let too_big = "y".repeat(MAX_EDITOR_BYTES + 1);
        assert!(matches!(
            write_text_file(&root, Path::new("ok.txt"), &too_big, None),
            Err(EditorError::TooLarge)
        ));
        assert_eq!(std::fs::read(root.join("ok.txt")).unwrap(), b"hello\n");
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn save_round_trip_is_atomic_and_conflict_aware() {
        let root = fixture_root("save");
        write_fixture(&root, "doc.md", b"# v1\n");
        let opened = read_text_file(&root, Path::new("doc.md")).unwrap();
        let saved =
            write_text_file(&root, Path::new("doc.md"), "# v2\n", Some(&opened.revision)).unwrap();
        assert_eq!(
            std::fs::read_to_string(root.join("doc.md")).unwrap(),
            "# v2\n"
        );
        // Stale revision refuses instead of clobbering.
        assert!(matches!(
            write_text_file(
                &root,
                Path::new("doc.md"),
                "# stale\n",
                Some(&opened.revision)
            ),
            Err(EditorError::Conflict)
        ));
        assert_eq!(
            std::fs::read_to_string(root.join("doc.md")).unwrap(),
            "# v2\n"
        );
        // Fresh revision succeeds.
        let current = read_text_file(&root, Path::new("doc.md")).unwrap();
        assert_eq!(current.revision, saved);
        write_text_file(
            &root,
            Path::new("doc.md"),
            "# v3\n",
            Some(&current.revision),
        )
        .unwrap();
        assert_eq!(
            std::fs::read_to_string(root.join("doc.md")).unwrap(),
            "# v3\n"
        );
        // No temp sidecars leak beside the document.
        let leftovers: Vec<_> = std::fs::read_dir(&root)
            .unwrap()
            .filter_map(|entry| entry.ok().map(|entry| entry.file_name()))
            .filter(|name| name.to_string_lossy().starts_with(".omaterm-editor-"))
            .collect();
        assert!(leftovers.is_empty(), "temp files leaked: {leftovers:?}");
        let _ = std::fs::remove_dir_all(&root);
    }

    #[cfg(unix)]
    #[test]
    fn same_size_preserved_timestamp_change_conflicts_by_digest() {
        let root = fixture_root("same-size-digest");
        let path = write_fixture(&root, "doc.txt", b"before!\n");
        let opened = read_text_file(&root, Path::new("doc.txt")).unwrap();
        let modified = std::fs::metadata(&path).unwrap().modified().unwrap();

        // Preserve the observed timestamp and byte count to prove metadata
        // alone would miss this external mutation.
        std::fs::write(&path, b"after!!\n").unwrap();
        let file = std::fs::OpenOptions::new().write(true).open(&path).unwrap();
        file.set_times(std::fs::FileTimes::new().set_modified(modified))
            .unwrap();
        let current = read_text_file(&root, Path::new("doc.txt")).unwrap();
        assert_eq!(current.revision.size, opened.revision.size);
        assert_eq!(current.revision.mtime_secs, opened.revision.mtime_secs);
        assert_eq!(current.revision.mtime_nanos, opened.revision.mtime_nanos);
        assert_ne!(
            current.revision.content_digest,
            opened.revision.content_digest
        );
        assert!(matches!(
            write_text_file(
                &root,
                Path::new("doc.txt"),
                "local!!!\n",
                Some(&opened.revision)
            ),
            Err(EditorError::Conflict)
        ));
        assert_eq!(std::fs::read(&path).unwrap(), b"after!!\n");
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn stable_codes_match_the_core_wire_strings() {
        assert_eq!(EditorError::PathOutsideRoot.code(), "path_outside_root");
        assert_eq!(EditorError::NotFound.code(), "file_not_found");
        assert_eq!(EditorError::NotRegularFile.code(), "invalid_request");
        assert_eq!(EditorError::TooLarge.code(), "document_too_large");
        assert_eq!(EditorError::NotTextFile.code(), "not_text_file");
        assert_eq!(EditorError::Conflict.code(), "document_conflict");
        assert_eq!(
            EditorError::SecureResolutionUnavailable.code(),
            "runtime_failure"
        );
        assert_eq!(MAX_EDITOR_BYTES, omaterm_core::validation::MAX_EDITOR_BYTES);
        assert_eq!(MAX_EDITOR_LINES, omaterm_core::validation::MAX_EDITOR_LINES);
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn descriptor_root_keeps_original_tree_and_rejects_escaping_symlinks() {
        use std::os::unix::fs::symlink;

        let root = fixture_root("descriptor-root");
        write_fixture(&root, "inside/doc.txt", b"original root\n");
        let outside =
            root.with_file_name(format!("omaterm-m19-editor-outside-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&outside);
        std::fs::create_dir_all(&outside).unwrap();
        std::fs::write(outside.join("secret.txt"), b"outside\n").unwrap();
        symlink("inside", root.join("contained")).unwrap();
        symlink(
            format!("../{}", outside.file_name().unwrap().to_string_lossy()),
            root.join("escape"),
        )
        .unwrap();

        let captured = EditorRoot::open(&root).unwrap();
        assert_eq!(
            read_text_file_from_root(&captured, Path::new("contained/doc.txt"))
                .unwrap()
                .text,
            "original root\n"
        );
        assert!(matches!(
            captured.open_descendant(Path::new("escape/secret.txt")),
            Err(EditorError::PathOutsideRoot)
        ));
        assert!(matches!(
            captured.open_descendant(Path::new("../outside.txt")),
            Err(EditorError::PathOutsideRoot)
        ));

        let moved = root.with_file_name(format!(
            "omaterm-m19-editor-captured-root-{}",
            std::process::id()
        ));
        let _ = std::fs::remove_dir_all(&moved);
        std::fs::rename(&root, &moved).unwrap();
        std::fs::create_dir_all(root.join("inside")).unwrap();
        std::fs::write(root.join("inside/doc.txt"), b"replacement root\n").unwrap();
        assert_ne!(
            EditorRoot::open(&root).unwrap().identity(),
            captured.identity(),
            "replacement root must have a distinct identity"
        );
        assert_eq!(
            read_text_file_from_root(&captured, Path::new("inside/doc.txt"))
                .unwrap()
                .text,
            "original root\n"
        );

        std::fs::remove_dir_all(&root).unwrap();
        std::fs::remove_dir_all(&moved).unwrap();
        std::fs::remove_dir_all(&outside).unwrap();
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn descriptor_root_saves_contained_ancestors_without_retargeting_a_replaced_root() {
        use std::os::unix::fs::symlink;

        let root = fixture_root("descriptor-save");
        write_fixture(&root, "nested/doc.txt", b"before\n");
        symlink("nested", root.join("contained")).unwrap();
        symlink("nested/doc.txt", root.join("final-link.txt")).unwrap();
        let captured = EditorRoot::open(&root).unwrap();

        let opened = read_text_file_from_root(&captured, Path::new("contained/doc.txt")).unwrap();
        let outcome = write_text_file_from_root(
            &captured,
            Path::new("contained/doc.txt"),
            "ancestor save\n",
            Some(&opened.revision),
        )
        .unwrap();
        assert!(outcome.is_durable());
        assert_eq!(
            std::fs::read(root.join("nested/doc.txt")).unwrap(),
            b"ancestor save\n"
        );

        let linked = read_text_file_from_root(&captured, Path::new("final-link.txt")).unwrap();
        assert!(matches!(
            write_text_file_from_root(
                &captured,
                Path::new("final-link.txt"),
                "must not replace alias\n",
                Some(&linked.revision),
            ),
            Err(EditorError::Conflict)
        ));
        assert!(root.join("final-link.txt").is_symlink());

        let moved = root.with_file_name(format!(
            "omaterm-m19-editor-captured-save-root-{}",
            std::process::id()
        ));
        let _ = std::fs::remove_dir_all(&moved);
        std::fs::rename(&root, &moved).unwrap();
        write_fixture(&root, "nested/doc.txt", b"replacement root\n");
        let opened = read_text_file_from_root(&captured, Path::new("nested/doc.txt")).unwrap();
        write_text_file_from_root(
            &captured,
            Path::new("nested/doc.txt"),
            "captured root\n",
            Some(&opened.revision),
        )
        .unwrap();
        assert_eq!(
            std::fs::read(moved.join("nested/doc.txt")).unwrap(),
            b"captured root\n"
        );
        assert_eq!(
            std::fs::read(root.join("nested/doc.txt")).unwrap(),
            b"replacement root\n"
        );

        std::fs::remove_dir_all(&root).unwrap();
        std::fs::remove_dir_all(&moved).unwrap();
    }
}
