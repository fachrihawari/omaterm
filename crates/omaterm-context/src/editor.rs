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
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::SystemTime;

use sha2::{Digest, Sha256};

/// Largest accepted editor document in bytes. Mirrors
/// `omaterm_core::validation::MAX_EDITOR_BYTES`; enforced before allocation.
pub const MAX_EDITOR_BYTES: usize = 1024 * 1024;
/// Largest accepted editor document in lines. Mirrors
/// `omaterm_core::validation::MAX_EDITOR_LINES`; enforced while splitting.
pub const MAX_EDITOR_LINES: usize = 20_000;

/// Highlight-supported language set: the 20-language first batch plus the
/// follow-up six (Kotlin, Zig, Lua, Dockerfile; zsh/fish/PKGBUILD reuse the
/// Bash profile). Everything else is `Plain` until the pipeline proves more.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EditorLanguage {
    Rust,
    Markdown,
    Toml,
    Json,
    Bash,
    Python,
    JavaScript,
    TypeScript,
    Html,
    Css,
    Yaml,
    Xml,
    Sql,
    Go,
    Java,
    C,
    Cpp,
    CSharp,
    Ruby,
    Php,
    Kotlin,
    Zig,
    Lua,
    Dockerfile,
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
            Self::Python => "python",
            Self::JavaScript => "javascript",
            Self::TypeScript => "typescript",
            Self::Html => "html",
            Self::Css => "css",
            Self::Yaml => "yaml",
            Self::Xml => "xml",
            Self::Sql => "sql",
            Self::Go => "go",
            Self::Java => "java",
            Self::C => "c",
            Self::Cpp => "cpp",
            Self::CSharp => "csharp",
            Self::Ruby => "ruby",
            Self::Php => "php",
            Self::Kotlin => "kotlin",
            Self::Zig => "zig",
            Self::Lua => "lua",
            Self::Dockerfile => "dockerfile",
            Self::Plain => "plaintext",
        }
    }
}

/// Language detection by filename/extension, case-insensitive. Lossy text is
/// display-only here; identity always uses original path bytes elsewhere.
/// `Dockerfile`/`Makefile` stay `Plain`, and a bare `.h` maps to `C`
/// (documented subset — C/C++ share the header suffix). zsh/fish/PKGBUILD
/// and common shell dotfiles reuse the Bash profile; `Dockerfile.*` and
/// `Containerfile` reuse the Dockerfile profile.
pub fn detect_language(path: &Path) -> EditorLanguage {
    let name = path
        .file_name()
        .map(|name| name.to_string_lossy().to_lowercase())
        .unwrap_or_default();
    if name == "dockerfile" || name.starts_with("dockerfile.") {
        return EditorLanguage::Dockerfile;
    }
    if name == "containerfile" || name.starts_with("containerfile.") {
        return EditorLanguage::Dockerfile;
    }
    if name == "makefile" {
        return EditorLanguage::Plain;
    }
    if name == "go.mod" || name == "go.sum" {
        return EditorLanguage::Go;
    }
    if name == "gemfile" || name == "rakefile" {
        return EditorLanguage::Ruby;
    }
    if name == "pkgbuild" {
        return EditorLanguage::Bash;
    }
    if matches!(
        name.as_str(),
        ".bashrc" | ".bash_profile" | ".zshrc" | ".zprofile" | ".profile"
    ) {
        return EditorLanguage::Bash;
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
        Some("sh" | "bash" | "zsh" | "fish") => EditorLanguage::Bash,
        Some("py" | "pyw" | "pyi") => EditorLanguage::Python,
        Some("js" | "mjs" | "cjs" | "jsx") => EditorLanguage::JavaScript,
        Some("ts" | "mts" | "cts" | "tsx") => EditorLanguage::TypeScript,
        Some("html" | "htm" | "xhtml") => EditorLanguage::Html,
        Some("css") => EditorLanguage::Css,
        Some("yaml" | "yml") => EditorLanguage::Yaml,
        Some("xml" | "xsd" | "svg") => EditorLanguage::Xml,
        Some("sql") => EditorLanguage::Sql,
        Some("go") => EditorLanguage::Go,
        Some("java") => EditorLanguage::Java,
        Some("c" | "h") => EditorLanguage::C,
        Some("cpp" | "cxx" | "cc" | "hpp" | "hh" | "hxx") => EditorLanguage::Cpp,
        Some("cs") => EditorLanguage::CSharp,
        Some("rb") => EditorLanguage::Ruby,
        Some("php" | "phtml") => EditorLanguage::Php,
        Some("kt" | "kts") => EditorLanguage::Kotlin,
        Some("zig" | "zon") => EditorLanguage::Zig,
        Some("lua") => EditorLanguage::Lua,
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

    /// Refuse a removed, moved or replaced canonical root, including a symlink
    /// substituted at that pathname. Descendant I/O still uses the captured FD.
    pub fn validate_live_identity(&self) -> Result<(), EditorError> {
        let live =
            std::fs::symlink_metadata(&self.canonical_path).map_err(|_| EditorError::Conflict)?;
        if !live.is_dir() || !same_object(&live, &self.directory.metadata()?) {
            return Err(EditorError::Conflict);
        }
        Ok(())
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
    #[error("editor operation was cancelled before publication or commit")]
    Cancelled,
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
            Self::Cancelled => "cancelled",
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

/// Read through an already captured Linux root descriptor. A root replacement
/// after capture refuses publication rather than retargeting or publishing
/// from the moved tree.
#[cfg(target_os = "linux")]
pub fn read_text_file_from_root(
    root: &EditorRoot,
    user_path: &Path,
) -> Result<EditorFile, EditorError> {
    read_text_file_from_root_cancellable(root, user_path, &AtomicBool::new(false))
}

/// Chunked read with cooperative cancellation. Blocking filesystem syscalls
/// cannot be interrupted. Revalidate the root and current descendant before
/// publication; changes after the last check remain a residual race interval.
#[cfg(target_os = "linux")]
pub fn read_text_file_from_root_cancellable(
    root: &EditorRoot,
    user_path: &Path,
    cancelled: &AtomicBool,
) -> Result<EditorFile, EditorError> {
    check_cancelled(cancelled)?;
    root.validate_live_identity()?;
    let result =
        read_open_file_cancellable(root.open_descendant(user_path)?, user_path, cancelled)?;
    let current = root.open_descendant(user_path)?.metadata()?;
    if FileRevision::of(&current, result.text.as_bytes()) != result.revision {
        return Err(EditorError::Conflict);
    }
    root.validate_live_identity()?;
    check_cancelled(cancelled)?;
    Ok(result)
}

#[cfg(target_os = "linux")]
pub fn read_text_file_cancellable(
    root: &Path,
    user_path: &Path,
    cancelled: &AtomicBool,
) -> Result<EditorFile, EditorError> {
    check_cancelled(cancelled)?;
    read_text_file_from_root_cancellable(&EditorRoot::open(root)?, user_path, cancelled)
}

/// Path-based root used where descriptor-relative `openat2` is unavailable.
/// Reads and saves still go through [`canonical_document_path`].
#[cfg(not(target_os = "linux"))]
#[derive(Debug)]
pub struct EditorRoot {
    canonical_path: PathBuf,
    identity: RootIdentity,
}

#[cfg(not(target_os = "linux"))]
impl EditorRoot {
    pub fn open(root: &Path) -> Result<Self, EditorError> {
        let canonical_path = std::fs::canonicalize(root).map_err(map_not_found)?;
        let metadata = std::fs::metadata(&canonical_path).map_err(map_not_found)?;
        if !metadata.is_dir() {
            return Err(EditorError::NotRegularFile);
        }
        Ok(Self {
            canonical_path: canonical_path.clone(),
            identity: root_identity_of(&canonical_path, &metadata)?,
        })
    }

    pub fn canonical_path(&self) -> &Path {
        &self.canonical_path
    }

    pub const fn identity(&self) -> RootIdentity {
        self.identity
    }
}

#[cfg(not(target_os = "linux"))]
fn root_identity_of(
    path: &Path,
    metadata: &std::fs::Metadata,
) -> Result<RootIdentity, EditorError> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        let _ = path;
        Ok(RootIdentity {
            device: metadata.dev(),
            inode: metadata.ino(),
        })
    }
    #[cfg(windows)]
    {
        let _ = metadata;
        let (device, inode) = windows_file_id(path)?;
        Ok(RootIdentity { device, inode })
    }
}

/// Volume serial plus file index from `GetFileInformationByHandle`.
/// Opening with `FILE_FLAG_OPEN_REPARSE_POINT` names this directory, not a
/// target a reparse point was swapped to. A failed call is an I/O error:
/// there is no mtime fallback.
#[cfg(windows)]
fn windows_file_id(path: &Path) -> Result<(u64, u64), EditorError> {
    use std::os::windows::ffi::OsStrExt;
    use windows_sys::Win32::Foundation::{CloseHandle, INVALID_HANDLE_VALUE};
    use windows_sys::Win32::Storage::FileSystem::{
        BY_HANDLE_FILE_INFORMATION, CreateFileW, FILE_FLAG_BACKUP_SEMANTICS,
        FILE_FLAG_OPEN_REPARSE_POINT, FILE_READ_ATTRIBUTES, FILE_SHARE_DELETE, FILE_SHARE_READ,
        FILE_SHARE_WRITE, GetFileInformationByHandle, OPEN_EXISTING,
    };

    let wide: Vec<u16> = path
        .as_os_str()
        .encode_wide()
        .chain(std::iter::once(0))
        .collect();
    // SAFETY: `wide` is NUL-terminated. BACKUP_SEMANTICS is required to open
    // a directory. The handle is closed before this function returns.
    let handle = unsafe {
        CreateFileW(
            wide.as_ptr(),
            FILE_READ_ATTRIBUTES,
            FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
            std::ptr::null(),
            OPEN_EXISTING,
            FILE_FLAG_BACKUP_SEMANTICS | FILE_FLAG_OPEN_REPARSE_POINT,
            std::ptr::null_mut(),
        )
    };
    if handle == INVALID_HANDLE_VALUE {
        return Err(EditorError::Io(std::io::Error::last_os_error()));
    }
    let mut info = unsafe { std::mem::zeroed::<BY_HANDLE_FILE_INFORMATION>() };
    // SAFETY: `handle` is open and `info` is a live out-param.
    let ok = unsafe { GetFileInformationByHandle(handle, &mut info) };
    unsafe { CloseHandle(handle) };
    if ok == 0 {
        return Err(EditorError::Io(std::io::Error::last_os_error()));
    }
    let index = (u64::from(info.nFileIndexHigh) << 32) | u64::from(info.nFileIndexLow);
    Ok((u64::from(info.dwVolumeSerialNumber), index))
}

#[cfg(not(target_os = "linux"))]
pub fn read_text_file_from_root(
    root: &EditorRoot,
    user_path: &Path,
) -> Result<EditorFile, EditorError> {
    read_text_file(&root.canonical_path, user_path)
}

#[cfg(not(target_os = "linux"))]
pub fn read_text_file_from_root_cancellable(
    root: &EditorRoot,
    user_path: &Path,
    cancelled: &AtomicBool,
) -> Result<EditorFile, EditorError> {
    check_cancelled(cancelled)?;
    read_text_file(&root.canonical_path, user_path)
}

#[cfg(not(target_os = "linux"))]
pub fn write_text_file_from_root(
    root: &EditorRoot,
    user_path: &Path,
    text: &str,
    expected: Option<&FileRevision>,
) -> Result<WriteTextOutcome, EditorError> {
    write_text_file_from_root_cancellable(root, user_path, text, expected, &AtomicBool::new(false))
}

#[cfg(not(target_os = "linux"))]
pub fn write_text_file_from_root_cancellable(
    root: &EditorRoot,
    user_path: &Path,
    text: &str,
    expected: Option<&FileRevision>,
    cancelled: &AtomicBool,
) -> Result<WriteTextOutcome, EditorError> {
    check_cancelled(cancelled)?;
    let revision = write_text_file(&root.canonical_path, user_path, text, expected)?;
    Ok(WriteTextOutcome::CommittedDurable { revision })
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

#[cfg(not(target_os = "linux"))]
fn read_open_file(file: std::fs::File, user_path: &Path) -> Result<EditorFile, EditorError> {
    read_open_file_cancellable(file, user_path, &AtomicBool::new(false))
}

fn read_open_file_cancellable(
    file: std::fs::File,
    user_path: &Path,
    cancelled: &AtomicBool,
) -> Result<EditorFile, EditorError> {
    check_cancelled(cancelled)?;
    let metadata = file.metadata()?;
    if !metadata.file_type().is_file() {
        return Err(EditorError::NotRegularFile);
    }
    if metadata.len() > MAX_EDITOR_BYTES as u64 {
        return Err(EditorError::TooLarge);
    }
    let bytes = read_bounded_cancellable(&file, cancelled)?;
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
    check_cancelled(cancelled)?;
    Ok(EditorFile {
        bytes: text.len(),
        lines,
        language: detect_language(user_path),
        text,
        revision,
    })
}

#[cfg(test)]
fn read_bounded(reader: impl Read) -> Result<Vec<u8>, EditorError> {
    read_bounded_cancellable(reader, &AtomicBool::new(false))
}

const IO_CHUNK_BYTES: usize = 16 * 1024;

fn check_cancelled(cancelled: &AtomicBool) -> Result<(), EditorError> {
    if cancelled.load(Ordering::Acquire) {
        Err(EditorError::Cancelled)
    } else {
        Ok(())
    }
}

fn read_bounded_cancellable(
    mut reader: impl Read,
    cancelled: &AtomicBool,
) -> Result<Vec<u8>, EditorError> {
    let mut bytes = Vec::new();
    let mut chunk = [0; IO_CHUNK_BYTES];
    loop {
        check_cancelled(cancelled)?;
        let limit = chunk.len().min(MAX_EDITOR_BYTES + 1 - bytes.len());
        let count = match reader.read(&mut chunk[..limit]) {
            Err(error) if error.kind() == std::io::ErrorKind::Interrupted => continue,
            result => result?,
        };
        check_cancelled(cancelled)?;
        if count == 0 {
            break;
        }
        bytes.extend_from_slice(&chunk[..count]);
        #[cfg(all(test, target_os = "linux"))]
        write_hook(WriteHookPoint::ReadChunk, c"read")?;
        if bytes.len() > MAX_EDITOR_BYTES {
            return Err(EditorError::TooLarge);
        }
    }
    Ok(bytes)
}

/// Atomically replace an existing text file using a freshly captured Linux
/// root descriptor. The final component must be a regular file, not a
/// symlink: renaming over a symlink would replace the alias rather than its
/// referent, so saves via contained final aliases explicitly fail with
/// `Conflict` (reads still follow them). Contained ancestor aliases remain
/// supported. A replaced root refuses a save to either tree.
#[cfg(target_os = "linux")]
pub fn write_text_file_from_root(
    root: &EditorRoot,
    user_path: &Path,
    text: &str,
    expected: Option<&FileRevision>,
) -> Result<WriteTextOutcome, EditorError> {
    write_text_file_from_root_cancellable(root, user_path, text, expected, &AtomicBool::new(false))
}

/// Cooperative save: cancellation before rename means no commit; after rename
/// the committed outcome is preserved even if cancellation arrives. Root,
/// parent, destination and sidecar checks precede rename. The interval between
/// these checks and rename is not an atomic compare-and-swap: arbitrary writers
/// can still race it. Cleanup likewise has a stat-to-unlink residual interval.
#[cfg(target_os = "linux")]
pub fn write_text_file_from_root_cancellable(
    root: &EditorRoot,
    user_path: &Path,
    text: &str,
    expected: Option<&FileRevision>,
    cancelled: &AtomicBool,
) -> Result<WriteTextOutcome, EditorError> {
    use std::os::fd::AsRawFd;
    use std::os::unix::fs::{MetadataExt, PermissionsExt};

    check_cancelled(cancelled)?;
    root.validate_live_identity()?;
    if text.len() > MAX_EDITOR_BYTES || count_lines(text) > MAX_EDITOR_LINES {
        return Err(EditorError::TooLarge);
    }
    // This follows a contained alias but rejects an escaping one before the
    // no-follow target open below decides whether replacement is safe.
    let opened = read_text_file_from_root_cancellable(root, user_path, cancelled)?;
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
    check_cancelled(cancelled)?;
    #[cfg(test)]
    write_hook(WriteHookPoint::TempCreate, &temp_name)?;
    check_cancelled(cancelled)?;
    let mut temp = create_sidecar(&parent, &temp_name)?;
    let result = (|| {
        #[cfg(test)]
        write_hook(WriteHookPoint::AfterTempCreate, &temp_name)?;
        #[cfg(test)]
        write_hook(WriteHookPoint::Write, &temp_name)?;
        for chunk in text.as_bytes().chunks(IO_CHUNK_BYTES) {
            check_cancelled(cancelled)?;
            temp.write_all(chunk)?;
            #[cfg(test)]
            write_hook(WriteHookPoint::Chunk, &temp_name)?;
        }
        check_cancelled(cancelled)?;
        temp.set_permissions(std::fs::Permissions::from_mode(metadata.mode()))?;
        #[cfg(test)]
        write_hook(WriteHookPoint::FileSync, &temp_name)?;
        temp.sync_all()?;

        // This detects observable changes before commit. It is intentionally
        // not described as a compare-and-swap against arbitrary writers.
        if read_open_file_cancellable(
            open_parent_file(&parent, &leaf, libc::O_RDONLY | libc::O_NONBLOCK)?,
            user_path,
            cancelled,
        )?
        .revision
            != opened.revision
        {
            return Err(EditorError::Conflict);
        }
        let revision = FileRevision::of(&temp.metadata()?, text.as_bytes());
        #[cfg(test)]
        write_hook(WriteHookPoint::Rename, &temp_name)?;
        let (live_parent, _) = root.open_parent(user_path)?;
        if !same_object(&parent.metadata()?, &live_parent.metadata()?)
            || read_open_file_cancellable(
                open_parent_file(&live_parent, &leaf, libc::O_RDONLY | libc::O_NONBLOCK)?,
                user_path,
                cancelled,
            )?
            .revision
                != opened.revision
            || !sidecar_is_owned(&parent, &temp_name, &temp)
        {
            return Err(EditorError::Conflict);
        }
        root.validate_live_identity()?;
        check_cancelled(cancelled)?;
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
        #[cfg(test)]
        let directory_sync =
            write_hook(WriteHookPoint::DirectorySync, &temp_name).and_then(|()| parent.sync_all());
        #[cfg(not(test))]
        let directory_sync = parent.sync_all();
        match directory_sync {
            Ok(()) => Ok(WriteTextOutcome::CommittedDurable { revision }),
            Err(error) => Ok(WriteTextOutcome::CommittedDurabilityWarning { revision, error }),
        }
    })();
    if result.is_err() {
        // A private O_EXCL name can still be swapped by another writer.
        if sidecar_is_owned(&parent, &temp_name, &temp) {
            let _ = unsafe { libc::unlinkat(parent.as_raw_fd(), temp_name.as_ptr(), 0) };
        }
    }
    result
}

#[cfg(all(test, target_os = "linux"))]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum WriteHookPoint {
    TempCreate,
    AfterTempCreate,
    Write,
    Chunk,
    ReadChunk,
    FileSync,
    Rename,
    DirectorySync,
}

#[cfg(all(test, target_os = "linux"))]
type WriteHook = std::sync::Arc<
    dyn Fn(WriteHookPoint, &std::ffi::CStr) -> std::io::Result<()> + Send + Sync + 'static,
>;

#[cfg(all(test, target_os = "linux"))]
thread_local! {
    static WRITE_HOOK: std::cell::RefCell<Option<WriteHook>> = const { std::cell::RefCell::new(None) };
}

#[cfg(all(test, target_os = "linux"))]
fn write_hook(point: WriteHookPoint, name: &std::ffi::CStr) -> std::io::Result<()> {
    WRITE_HOOK.with(|hook| match hook.borrow().as_ref() {
        Some(hook) => hook(point, name),
        None => Ok(()),
    })
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
fn same_object(first: &std::fs::Metadata, second: &std::fs::Metadata) -> bool {
    use std::os::unix::fs::MetadataExt;
    first.dev() == second.dev() && first.ino() == second.ino()
}

#[cfg(target_os = "linux")]
fn sidecar_is_owned(parent: &std::fs::File, name: &std::ffi::CStr, owned: &std::fs::File) -> bool {
    // O_PATH avoids reading or blocking on a substituted special file.
    open_parent_file(parent, name, libc::O_PATH)
        .and_then(|file| Ok(same_object(&file.metadata()?, &owned.metadata()?)))
        .unwrap_or(false)
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

    #[cfg(target_os = "linux")]
    struct WriteHookReset;

    #[cfg(target_os = "linux")]
    impl Drop for WriteHookReset {
        fn drop(&mut self) {
            WRITE_HOOK.with(|hook| {
                hook.borrow_mut().take();
            });
        }
    }

    #[cfg(target_os = "linux")]
    fn install_write_hook(hook: WriteHook) -> WriteHookReset {
        WRITE_HOOK.with(|installed| {
            assert!(
                installed.borrow().is_none(),
                "a test write hook is already installed"
            );
            *installed.borrow_mut() = Some(hook);
        });
        WriteHookReset
    }

    #[cfg(target_os = "linux")]
    fn sidecars(directory: &Path) -> Vec<PathBuf> {
        std::fs::read_dir(directory)
            .unwrap()
            .filter_map(|entry| entry.ok().map(|entry| entry.path()))
            .filter(|path| {
                path.file_name()
                    .is_some_and(|name| name.to_string_lossy().starts_with(".omaterm-editor-"))
            })
            .collect()
    }

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
            ("app.py", EditorLanguage::Python),
            ("APP.PY", EditorLanguage::Python),
            ("app.js", EditorLanguage::JavaScript),
            ("app.jsx", EditorLanguage::JavaScript),
            ("app.ts", EditorLanguage::TypeScript),
            ("app.tsx", EditorLanguage::TypeScript),
            ("index.html", EditorLanguage::Html),
            ("style.css", EditorLanguage::Css),
            ("config.yaml", EditorLanguage::Yaml),
            ("config.yml", EditorLanguage::Yaml),
            ("data.xml", EditorLanguage::Xml),
            ("query.sql", EditorLanguage::Sql),
            ("main.go", EditorLanguage::Go),
            ("go.mod", EditorLanguage::Go),
            ("Main.java", EditorLanguage::Java),
            ("main.c", EditorLanguage::C),
            ("header.h", EditorLanguage::C),
            ("main.cpp", EditorLanguage::Cpp),
            ("header.hpp", EditorLanguage::Cpp),
            ("App.cs", EditorLanguage::CSharp),
            ("app.rb", EditorLanguage::Ruby),
            ("Gemfile", EditorLanguage::Ruby),
            ("index.php", EditorLanguage::Php),
            ("main.kt", EditorLanguage::Kotlin),
            ("build.kts", EditorLanguage::Kotlin),
            ("main.zig", EditorLanguage::Zig),
            ("build.zon", EditorLanguage::Zig),
            ("init.lua", EditorLanguage::Lua),
            ("Dockerfile", EditorLanguage::Dockerfile),
            ("dockerfile.dev", EditorLanguage::Dockerfile),
            ("Containerfile", EditorLanguage::Dockerfile),
            ("deploy.zsh", EditorLanguage::Bash),
            ("config.fish", EditorLanguage::Bash),
            ("PKGBUILD", EditorLanguage::Bash),
            (".bashrc", EditorLanguage::Bash),
            (".zshrc", EditorLanguage::Bash),
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
        assert_eq!(EditorError::Cancelled.code(), "cancelled");
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
    fn descriptor_root_refuses_replacement_and_rejects_escaping_symlinks() {
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
        assert!(matches!(
            read_text_file_from_root(&captured, Path::new("inside/doc.txt")),
            Err(EditorError::Conflict)
        ));

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
        assert!(matches!(
            write_text_file_from_root(
                &captured,
                Path::new("nested/doc.txt"),
                "captured root\n",
                None,
            ),
            Err(EditorError::Conflict)
        ));
        assert_eq!(
            std::fs::read(moved.join("nested/doc.txt")).unwrap(),
            b"ancestor save\n"
        );
        assert_eq!(
            std::fs::read(root.join("nested/doc.txt")).unwrap(),
            b"replacement root\n"
        );

        std::fs::remove_dir_all(&root).unwrap();
        std::fs::remove_dir_all(&moved).unwrap();
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn injected_pre_rename_failures_keep_old_bytes_and_clean_owned_sidecars() {
        let root = fixture_root("save-failures");
        let target = write_fixture(&root, "doc.txt", b"old bytes\n");

        for point in [
            WriteHookPoint::TempCreate,
            WriteHookPoint::Write,
            WriteHookPoint::FileSync,
            WriteHookPoint::Rename,
        ] {
            let hook = install_write_hook(std::sync::Arc::new(move |seen, _| {
                if seen == point {
                    Err(std::io::Error::other("injected write failure"))
                } else {
                    Ok(())
                }
            }));
            let captured = EditorRoot::open(&root).unwrap();
            assert!(matches!(
                write_text_file_from_root(&captured, Path::new("doc.txt"), "new bytes\n", None),
                Err(EditorError::Io(_))
            ));
            drop(hook);

            assert_eq!(std::fs::read(&target).unwrap(), b"old bytes\n", "{point:?}");
            assert!(sidecars(&root).is_empty(), "{point:?} leaked a sidecar");
        }

        std::fs::remove_dir_all(root).unwrap();
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn injected_directory_sync_failure_reports_warning_after_commit() {
        let root = fixture_root("save-directory-sync");
        let target = write_fixture(&root, "doc.txt", b"old bytes\n");
        let captured = EditorRoot::open(&root).unwrap();
        let hook = install_write_hook(std::sync::Arc::new(|point, _| {
            if point == WriteHookPoint::DirectorySync {
                Err(std::io::Error::other("injected directory sync failure"))
            } else {
                Ok(())
            }
        }));

        let outcome =
            write_text_file_from_root(&captured, Path::new("doc.txt"), "new bytes\n", None)
                .unwrap();
        drop(hook);

        assert!(!outcome.is_durable());
        let revision = match outcome {
            WriteTextOutcome::CommittedDurabilityWarning { revision, error } => {
                assert_eq!(error.kind(), std::io::ErrorKind::Other);
                revision
            }
            WriteTextOutcome::CommittedDurable { .. } => panic!("directory sync should warn"),
        };
        let saved = read_text_file_from_root(&captured, Path::new("doc.txt")).unwrap();
        assert_eq!(std::fs::read(&target).unwrap(), b"new bytes\n");
        assert_eq!(saved.revision, revision);
        assert!(sidecars(&root).is_empty());

        std::fs::remove_dir_all(root).unwrap();
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn save_barrier_refuses_writes_to_a_moved_root() {
        let root = fixture_root("save-barrier");
        write_fixture(&root, "doc.txt", b"old bytes\n");
        let captured = EditorRoot::open(&root).unwrap();
        let entered = std::sync::Arc::new(std::sync::Barrier::new(2));
        let writer_entered = std::sync::Arc::clone(&entered);
        let writer = std::thread::spawn(move || {
            let hook = install_write_hook(std::sync::Arc::new(move |point, _| {
                if point == WriteHookPoint::AfterTempCreate {
                    writer_entered.wait();
                    writer_entered.wait();
                }
                Ok(())
            }));
            let result = write_text_file_from_root(
                &captured,
                Path::new("doc.txt"),
                "captured bytes\n",
                None,
            );
            drop(hook);
            result
        });

        entered.wait();
        let moved = root.with_file_name(format!(
            "omaterm-m19-editor-save-barrier-moved-{}",
            std::process::id()
        ));
        let _ = std::fs::remove_dir_all(&moved);
        std::fs::rename(&root, &moved).unwrap();
        write_fixture(&root, "doc.txt", b"replacement bytes\n");
        entered.wait();

        assert!(matches!(writer.join().unwrap(), Err(EditorError::Conflict)));
        assert_eq!(
            std::fs::read(moved.join("doc.txt")).unwrap(),
            b"old bytes\n"
        );
        assert_eq!(
            std::fs::read(root.join("doc.txt")).unwrap(),
            b"replacement bytes\n"
        );
        assert!(sidecars(&moved).is_empty());

        std::fs::remove_dir_all(root).unwrap();
        std::fs::remove_dir_all(moved).unwrap();
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn read_publication_refuses_root_ancestor_and_final_swaps() {
        use std::os::unix::fs::symlink;
        for kind in ["root", "ancestor", "final"] {
            let root = fixture_root(&format!("read-swap-{kind}"));
            write_fixture(&root, "nested/doc.txt", b"original");
            let captured = EditorRoot::open(&root).unwrap();
            let moved = root.with_extension("moved");
            let _ = std::fs::remove_dir_all(&moved);
            let hook_root = root.clone();
            let hook_moved = moved.clone();
            let once = AtomicBool::new(false);
            let hook = install_write_hook(std::sync::Arc::new(move |point, _| {
                if point == WriteHookPoint::ReadChunk && !once.swap(true, Ordering::AcqRel) {
                    match kind {
                        "root" => {
                            std::fs::rename(&hook_root, &hook_moved)?;
                            write_fixture(&hook_root, "nested/doc.txt", b"replacement");
                        }
                        "ancestor" => {
                            std::fs::rename(hook_root.join("nested"), hook_root.join("old"))?;
                            symlink("/etc", hook_root.join("nested"))?;
                        }
                        _ => {
                            std::fs::rename(
                                hook_root.join("nested/doc.txt"),
                                hook_root.join("old.txt"),
                            )?;
                            symlink("/etc/passwd", hook_root.join("nested/doc.txt"))?;
                        }
                    }
                }
                Ok(())
            }));
            assert!(read_text_file_from_root(&captured, Path::new("nested/doc.txt")).is_err());
            drop(hook);
            std::fs::remove_dir_all(root).unwrap();
            if moved.exists() {
                std::fs::remove_dir_all(moved).unwrap();
            }
        }
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn save_revalidates_ancestor_final_and_sidecar_before_rename() {
        use std::os::unix::ffi::OsStrExt;
        use std::os::unix::fs::symlink;
        for kind in ["ancestor", "final", "sidecar"] {
            let root = fixture_root(&format!("write-swap-{kind}"));
            write_fixture(&root, "nested/doc.txt", b"original");
            let captured = EditorRoot::open(&root).unwrap();
            let hook_root = root.clone();
            let hook = install_write_hook(std::sync::Arc::new(move |point, name| {
                if point == WriteHookPoint::Rename {
                    match kind {
                        "ancestor" => {
                            std::fs::rename(hook_root.join("nested"), hook_root.join("old"))?;
                            symlink("old", hook_root.join("nested"))?;
                            // Replace the alias with a different contained parent.
                            std::fs::remove_file(hook_root.join("nested"))?;
                            write_fixture(&hook_root, "nested/doc.txt", b"replacement");
                        }
                        "final" => {
                            std::fs::rename(
                                hook_root.join("nested/doc.txt"),
                                hook_root.join("old.txt"),
                            )?;
                            symlink("/etc/passwd", hook_root.join("nested/doc.txt"))?;
                        }
                        _ => {
                            let sidecar = hook_root
                                .join("nested")
                                .join(std::ffi::OsStr::from_bytes(name.to_bytes()));
                            std::fs::rename(&sidecar, hook_root.join("owned.tmp"))?;
                            std::fs::write(sidecar, b"unrelated")?;
                        }
                    }
                }
                Ok(())
            }));
            assert!(matches!(
                write_text_file_from_root(&captured, Path::new("nested/doc.txt"), "new", None),
                Err(EditorError::Conflict)
            ));
            drop(hook);
            if kind == "sidecar" {
                let leftovers = sidecars(&root.join("nested"));
                assert_eq!(leftovers.len(), 1);
                assert_eq!(std::fs::read(&leftovers[0]).unwrap(), b"unrelated");
                assert_eq!(
                    std::fs::read(root.join("nested/doc.txt")).unwrap(),
                    b"original"
                );
            } else if kind == "ancestor" {
                assert_eq!(
                    std::fs::read(root.join("old/doc.txt")).unwrap(),
                    b"original"
                );
                assert!(sidecars(&root.join("old")).is_empty());
                assert_eq!(
                    std::fs::read(root.join("nested/doc.txt")).unwrap(),
                    b"replacement"
                );
            } else {
                assert_eq!(std::fs::read(root.join("old.txt")).unwrap(), b"original");
                assert!(root.join("nested/doc.txt").is_symlink());
            }
            std::fs::remove_dir_all(root).unwrap();
        }
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn cancellation_boundaries_preserve_precommit_and_postcommit_outcomes() {
        for point in [
            WriteHookPoint::ReadChunk,
            WriteHookPoint::TempCreate,
            WriteHookPoint::Chunk,
            WriteHookPoint::Rename,
            WriteHookPoint::DirectorySync,
        ] {
            let root = fixture_root(&format!("cancel-{point:?}"));
            write_fixture(&root, "doc.txt", b"original");
            let captured = EditorRoot::open(&root).unwrap();
            let cancelled = std::sync::Arc::new(AtomicBool::new(false));
            let flag = cancelled.clone();
            let hook = install_write_hook(std::sync::Arc::new(move |seen, _| {
                if seen == point {
                    flag.store(true, Ordering::Release);
                }
                Ok(())
            }));
            let text = "x".repeat(IO_CHUNK_BYTES * 2);
            let result = write_text_file_from_root_cancellable(
                &captured,
                Path::new("doc.txt"),
                &text,
                None,
                &cancelled,
            );
            drop(hook);
            if point == WriteHookPoint::DirectorySync {
                assert!(result.unwrap().is_durable());
                assert_eq!(std::fs::read_to_string(root.join("doc.txt")).unwrap(), text);
            } else {
                assert!(matches!(result, Err(EditorError::Cancelled)));
                assert_eq!(std::fs::read(root.join("doc.txt")).unwrap(), b"original");
            }
            assert!(sidecars(&root).is_empty());
            assert!(matches!(
                read_text_file_from_root_cancellable(&captured, Path::new("doc.txt"), &cancelled),
                Err(EditorError::Cancelled)
            ));
            std::fs::remove_dir_all(root).unwrap();
        }
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn cancelled_chunked_read_never_publishes_partial_text() {
        let root = fixture_root("read-cancel-chunk");
        write_fixture(&root, "doc.txt", &vec![b'x'; IO_CHUNK_BYTES * 2]);
        let captured = EditorRoot::open(&root).unwrap();
        let cancelled = std::sync::Arc::new(AtomicBool::new(false));
        let flag = cancelled.clone();
        let hook = install_write_hook(std::sync::Arc::new(move |point, _| {
            if point == WriteHookPoint::ReadChunk {
                flag.store(true, Ordering::Release);
            }
            Ok(())
        }));
        assert!(matches!(
            read_text_file_from_root_cancellable(&captured, Path::new("doc.txt"), &cancelled),
            Err(EditorError::Cancelled)
        ));
        drop(hook);
        assert!(matches!(
            write_text_file_from_root_cancellable(
                &captured,
                Path::new("doc.txt"),
                "new",
                None,
                &cancelled
            ),
            Err(EditorError::Cancelled)
        ));
        assert_eq!(
            std::fs::read(root.join("doc.txt")).unwrap().len(),
            IO_CHUNK_BYTES * 2
        );
        assert!(sidecars(&root).is_empty());
        std::fs::remove_dir_all(root).unwrap();
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn postcommit_cancel_preserves_a_durability_warning() {
        let root = fixture_root("cancel-directory-warning");
        write_fixture(&root, "doc.txt", b"original");
        let captured = EditorRoot::open(&root).unwrap();
        let cancelled = std::sync::Arc::new(AtomicBool::new(false));
        let flag = cancelled.clone();
        let hook = install_write_hook(std::sync::Arc::new(move |point, _| {
            if point == WriteHookPoint::DirectorySync {
                flag.store(true, Ordering::Release);
                return Err(std::io::Error::other("directory sync failed"));
            }
            Ok(())
        }));
        let outcome = write_text_file_from_root_cancellable(
            &captured,
            Path::new("doc.txt"),
            "committed",
            None,
            &cancelled,
        )
        .unwrap();
        drop(hook);
        assert!(matches!(
            outcome,
            WriteTextOutcome::CommittedDurabilityWarning { .. }
        ));
        assert_eq!(
            read_text_file_from_root(&captured, Path::new("doc.txt"))
                .unwrap()
                .revision,
            outcome.revision()
        );
        assert_eq!(std::fs::read(root.join("doc.txt")).unwrap(), b"committed");
        assert!(sidecars(&root).is_empty());
        std::fs::remove_dir_all(root).unwrap();
    }
}
