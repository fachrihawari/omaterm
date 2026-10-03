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

/// On-disk revision captured on read and re-checked on save. Carries size
/// plus wall-clock mtime and Unix device/inode identity. Atomic replacement
/// conflicts even for same-size files sharing a filesystem timestamp tick.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct FileRevision {
    pub size: u64,
    pub mtime_secs: u64,
    pub mtime_nanos: u32,
    pub device: u64,
    pub inode: u64,
}

impl FileRevision {
    fn of(metadata: &std::fs::Metadata) -> Self {
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
            Self::Io(_) => "io_error",
        }
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

/// Read and validate one existing text file under `root`. Rejects
/// directories, escapes, oversize, binary, and non-UTF-8 inputs.
pub fn read_text_file(root: &Path, user_path: &Path) -> Result<EditorFile, EditorError> {
    let absolute = canonical_document_path(root, user_path)?;
    read_canonical_file(&absolute, user_path)
}

fn read_canonical_file(absolute: &Path, user_path: &Path) -> Result<EditorFile, EditorError> {
    let mut options = std::fs::OpenOptions::new();
    options.read(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.custom_flags(libc::O_NOFOLLOW | libc::O_NONBLOCK);
    }
    let file = options.open(absolute)?;
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
    let revision = FileRevision::of(&file.metadata()?);
    if revision != FileRevision::of(&metadata) {
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

/// Atomically replace one existing text file under `root`. `expected` is the
/// revision captured on open or last save; a mismatch refuses the write with
/// `Conflict` instead of clobbering external changes. Preserves the existing
/// permission bits across the rename.
pub fn write_text_file(
    root: &Path,
    user_path: &Path,
    text: &str,
    expected: Option<&FileRevision>,
) -> Result<FileRevision, EditorError> {
    if text.len() > MAX_EDITOR_BYTES || count_lines(text) > MAX_EDITOR_LINES {
        return Err(EditorError::TooLarge);
    }
    let absolute = canonical_document_path(root, user_path)?;
    let metadata = std::fs::symlink_metadata(&absolute)?;
    if !metadata.file_type().is_file() {
        return Err(EditorError::NotRegularFile);
    }
    if let Some(expected) = expected
        && FileRevision::of(&metadata) != *expected
    {
        return Err(EditorError::Conflict);
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
        let current = std::fs::symlink_metadata(&absolute)?;
        if !current.file_type().is_file()
            || FileRevision::of(&current) != FileRevision::of(&metadata)
        {
            return Err(EditorError::Conflict);
        }
        let revision = FileRevision::of(&file.metadata()?);
        std::fs::rename(&temp, &absolute)?;
        std::fs::File::open(parent)?.sync_all()?;
        Ok(revision)
    })();
    if result.is_err() {
        let _ = std::fs::remove_file(&temp);
    }
    result
}

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

    #[test]
    fn stable_codes_match_the_core_wire_strings() {
        assert_eq!(EditorError::PathOutsideRoot.code(), "path_outside_root");
        assert_eq!(EditorError::NotFound.code(), "file_not_found");
        assert_eq!(EditorError::NotRegularFile.code(), "invalid_request");
        assert_eq!(EditorError::TooLarge.code(), "document_too_large");
        assert_eq!(EditorError::NotTextFile.code(), "not_text_file");
        assert_eq!(EditorError::Conflict.code(), "document_conflict");
        assert_eq!(MAX_EDITOR_BYTES, omaterm_core::validation::MAX_EDITOR_BYTES);
        assert_eq!(MAX_EDITOR_LINES, omaterm_core::validation::MAX_EDITOR_LINES);
    }
}
