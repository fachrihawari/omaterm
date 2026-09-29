//! Filesystem boundary seam (M12, blueprint §50).
//!
//! Every v0.2 file/git/diff read resolves its user-supplied path through
//! [`canonicalize_under_root`] before touching the filesystem. Both sides
//! are canonicalized (symlinks resolved), so `..` traversal, absolute
//! escapes, and symlink escapes all fail with [`ContextError::PathOutsideRoot`]
//! (wire code `path_outside_root`, shared with M13/M14).
//!
//! OS-specific work stays behind this small interface; no `cfg(target_os)`
//! is scattered through workspace modules.

/// Boundary failures. The escape case carries a stable machine code; I/O
/// failures (including missing paths, which canonicalization cannot verify)
/// stay diagnostic so callers can distinguish "missing file" from "escape".
#[derive(Debug, thiserror::Error)]
pub enum ContextError {
    #[error("path escapes the project root")]
    PathOutsideRoot,
    #[error(transparent)]
    Io(#[from] std::io::Error),
}

impl ContextError {
    /// Stable machine code for the escape case (`path_outside_root`).
    /// I/O errors have no wire code; the caller maps them (e.g. NotFound
    /// becomes a normal missing-file path in M13, never an escape claim).
    pub const fn code(&self) -> &'static str {
        match self {
            Self::PathOutsideRoot => "path_outside_root",
            Self::Io(_) => "io_error",
        }
    }
}

/// Resolve `user_path` under `root`, rejecting escapes.
///
/// Relative paths join onto the canonical root; absolute paths must already
/// lie inside it. Both sides are canonicalized, so a symlink inside the
/// root that points outside is rejected exactly like `..` traversal.
pub fn canonicalize_under_root(
    root: &std::path::Path,
    user_path: &std::path::Path,
) -> Result<std::path::PathBuf, ContextError> {
    let canonical_root = std::fs::canonicalize(root)?;
    let joined = if user_path.is_absolute() {
        user_path.to_path_buf()
    } else {
        canonical_root.join(user_path)
    };
    let canonical = std::fs::canonicalize(&joined)?;
    if canonical.starts_with(&canonical_root) {
        Ok(canonical)
    } else {
        Err(ContextError::PathOutsideRoot)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn fixture_root(name: &str) -> std::path::PathBuf {
        let root = std::env::temp_dir().join(format!(
            "omaterm-m12-boundary-{}-{name}",
            std::process::id()
        ));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir_all(root.join("sub")).unwrap();
        std::fs::write(root.join("sub").join("file.txt"), b"hi").unwrap();
        root
    }

    #[test]
    fn nested_and_relative_paths_resolve() {
        let root = fixture_root("ok");
        let direct = canonicalize_under_root(&root, &root.join("sub").join("file.txt")).unwrap();
        assert!(direct.ends_with("sub/file.txt"));
        let relative =
            canonicalize_under_root(&root, std::path::Path::new("sub/file.txt")).unwrap();
        assert_eq!(direct, relative);
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn traversal_and_absolute_escapes_are_rejected() {
        let root = fixture_root("escape");
        for evil in [
            std::path::PathBuf::from("../outside.txt"),
            std::path::PathBuf::from("sub/../../outside.txt"),
            std::env::temp_dir().join("omaterm-m12-boundary-elsewhere.txt"),
        ] {
            let error = canonicalize_under_root(&root, &evil);
            // The elsewhere target may not exist (Io) or exist outside (escape);
            // either way it must never resolve inside the root.
            match error {
                Err(ContextError::PathOutsideRoot) => {}
                Err(ContextError::Io(_)) => {}
                Ok(path) => panic!("escape resolved inside root: {}", path.display()),
            }
        }
        // A traversal that still lands inside the root is legitimate.
        let legit =
            canonicalize_under_root(&root, std::path::Path::new("sub/../sub/file.txt")).unwrap();
        assert!(legit.ends_with("sub/file.txt"));
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn stable_code_marks_the_escape_case() {
        assert_eq!(ContextError::PathOutsideRoot.code(), "path_outside_root");
        assert_eq!(
            ContextError::PathOutsideRoot.to_string(),
            "path escapes the project root"
        );
    }

    /// A symlink inside the root that points outside must not smuggle
    /// reads past the boundary.
    #[cfg(unix)]
    #[test]
    fn symlink_escape_is_rejected() {
        let root = fixture_root("symlink");
        let outside = std::env::temp_dir().join(format!(
            "omaterm-m12-boundary-outside-{}",
            std::process::id()
        ));
        let _ = std::fs::remove_dir_all(&outside);
        std::fs::create_dir_all(&outside).unwrap();
        std::fs::write(outside.join("secret.txt"), b"secret").unwrap();
        std::os::unix::fs::symlink(&outside, root.join("link")).unwrap();

        let error = canonicalize_under_root(&root, std::path::Path::new("link/secret.txt"))
            .expect_err("symlink escape must be rejected");
        assert_eq!(error.code(), "path_outside_root");

        let _ = std::fs::remove_dir_all(&root);
        let _ = std::fs::remove_dir_all(&outside);
    }
}
