//! Deterministic project-root resolution (M12, blueprint §31).
//!
//! ```text
//! pinned_directory when set and present → Pinned
//! pinned set but missing               → Absent (explicit empty state)
//! no pin, CWD inside a repository      → Git (nearest enclosing toplevel)
//! no pin, CWD outside any repository   → Absent (never an error)
//! ```
//!
//! A set-but-missing pin resolves to `Absent` rather than falling back to
//! git: silently re-rooting an explicitly pinned project into an unrelated
//! repository would confuse file/git/diff targeting, so the missing root
//! surfaces as the downstream empty state instead. Agent worktree
//! re-rooting is deferred (non-goal).

use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::time::Duration;

use omaterm_core::{ProjectRootInfo, RootSource};

/// Bounded `git rev-parse --show-toplevel` wait. The synchronous router
/// query runs on the desktop owner thread, so a wedged git can cost at
/// most this long; M13 moves bulk filesystem work to background workers.
pub const GIT_TOPLEVEL_TIMEOUT: Duration = Duration::from_secs(2);

/// Resolve with the real git binary (`PATH` lookup) and the default timeout.
pub fn resolve_root(pinned: Option<&Path>, active_cwd: Option<&Path>) -> ProjectRootInfo {
    resolve_root_with(pinned, active_cwd, &git_toplevel_of)
}

/// Resolve with an injected toplevel lookup (tests stub git behavior
/// without touching `PATH` or spawning processes).
pub fn resolve_root_with(
    pinned: Option<&Path>,
    active_cwd: Option<&Path>,
    toplevel: &dyn Fn(&Path) -> Option<PathBuf>,
) -> ProjectRootInfo {
    if let Some(pin) = pinned {
        if pin.is_dir() {
            return ProjectRootInfo {
                root: Some(pin.to_path_buf()),
                source: RootSource::Pinned,
            };
        }
        return ProjectRootInfo {
            root: None,
            source: RootSource::Absent,
        };
    }
    match active_cwd.and_then(toplevel) {
        Some(dir) => ProjectRootInfo {
            root: Some(dir),
            source: RootSource::Git,
        },
        None => ProjectRootInfo {
            root: None,
            source: RootSource::Absent,
        },
    }
}

/// Nearest enclosing git toplevel for `cwd` via the `PATH` git binary.
/// Returns `None` when git is missing, times out, exits non-zero (non-repo),
/// or reports a directory that no longer exists. Never reads shell
/// configuration beyond what git itself needs; prompts are disabled.
pub fn git_toplevel_of(cwd: &Path) -> Option<PathBuf> {
    git_toplevel_of_with(cwd, Path::new("git"), GIT_TOPLEVEL_TIMEOUT)
}

/// Same lookup with an explicit git binary path and timeout (the timeout
/// test points this at a fake `sleep`-ing executable; production passes
/// `Path::new("git")`).
pub fn git_toplevel_of_with(cwd: &Path, git_bin: &Path, timeout: Duration) -> Option<PathBuf> {
    let mut command = Command::new(git_bin);
    command
        .arg("--no-optional-locks")
        .arg("rev-parse")
        .arg("--show-toplevel")
        .current_dir(cwd)
        .env("GIT_TERMINAL_PROMPT", "0")
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::null());
    crate::git::hide_console_window(&mut command);
    let mut child = command.spawn().ok()?;
    // The reader thread owns only the stdout pipe; the caller keeps the
    // child handle so every path below reaps it (no orphans, no leaked
    // waiter threads). Output is capped: a toplevel is one short line.
    let stdout = child.stdout.take();
    let (done_tx, done_rx) = std::sync::mpsc::channel();
    std::thread::spawn(move || {
        let mut bytes = Vec::new();
        if let Some(pipe) = stdout {
            use std::io::Read;
            let _ = pipe.take(64 * 1024).read_to_end(&mut bytes);
        }
        let _ = done_tx.send(bytes);
    });
    let bytes = match done_rx.recv_timeout(timeout) {
        Ok(bytes) => bytes,
        Err(_) => {
            // Wedged git: terminate and reap before reporting no repository,
            // so the timeout bounds the whole lookup including cleanup.
            let _ = child.kill();
            let _ = child.wait();
            return None;
        }
    };
    // Output arrived: reap promptly. `kill` is a no-op when git already
    // exited; `wait` then collects the zombie without blocking.
    let _ = child.kill();
    let status = child.wait().ok()?;
    if !status.success() {
        return None;
    }
    let line = String::from_utf8_lossy(&bytes)
        .lines()
        .next()
        .unwrap_or_default()
        .trim()
        .to_owned();
    if line.is_empty() {
        return None;
    }
    let dir = PathBuf::from(line);
    dir.is_dir().then_some(dir)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn absent() -> ProjectRootInfo {
        ProjectRootInfo {
            root: None,
            source: RootSource::Absent,
        }
    }

    #[test]
    fn pin_wins_over_a_repository_cwd() {
        let pin = std::env::temp_dir();
        let info = resolve_root_with(Some(&pin), Some(Path::new("/tmp")), &|_| {
            Some(PathBuf::from("/repo"))
        });
        assert_eq!(info.source, RootSource::Pinned);
        assert_eq!(info.root, Some(pin));
    }

    #[test]
    fn deleted_pin_is_absent_and_never_falls_back_to_git() {
        let missing = std::env::temp_dir().join("omaterm-m12-no-such-pin");
        let _ = std::fs::remove_dir_all(&missing);
        let info = resolve_root_with(Some(&missing), Some(Path::new("/tmp")), &|_| {
            Some(PathBuf::from("/repo"))
        });
        assert_eq!(info, absent());
    }

    #[test]
    fn git_fallback_and_empty_states() {
        let cwd = Path::new("/work/repo/sub");
        let info = resolve_root_with(None, Some(cwd), &|_| Some(PathBuf::from("/work/repo")));
        assert_eq!(info.source, RootSource::Git);
        assert_eq!(info.root, Some(PathBuf::from("/work/repo")));

        assert_eq!(resolve_root_with(None, Some(cwd), &|_| None), absent());
        assert_eq!(resolve_root_with(None, None, &|_| None), absent());
    }

    #[test]
    fn missing_git_binary_reports_no_repository() {
        let missing_bin = std::env::temp_dir().join("omaterm-m12-no-such-git");
        let _ = std::fs::remove_file(&missing_bin);
        assert_eq!(
            git_toplevel_of_with(&std::env::temp_dir(), &missing_bin, GIT_TOPLEVEL_TIMEOUT),
            None
        );
    }

    #[test]
    fn non_repository_directory_reports_none() {
        let dir = std::env::temp_dir().join(format!("omaterm-m12-nonrepo-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).unwrap();
        // A fresh /tmp dir is not a git repository (`.git` would have to be
        // an ancestor; the test temp dir has none).
        assert_eq!(git_toplevel_of(&dir), None);
        let _ = std::fs::remove_dir_all(&dir);
    }

    /// A wedged git costs at most the timeout — the bounded-blocking proof
    /// behind the "no UI-thread block" acceptance row.
    #[cfg(unix)]
    #[test]
    fn wedged_git_is_bounded_by_the_timeout() {
        use std::io::Write;
        use std::os::unix::fs::PermissionsExt;

        let dir = std::env::temp_dir().join(format!("omaterm-m12-hung-git-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).unwrap();
        let fake = dir.join("git");
        let mut file = std::fs::File::create(&fake).unwrap();
        file.write_all(b"#!/bin/sh\nsleep 30\n").unwrap();
        drop(file);
        std::fs::set_permissions(&fake, std::fs::Permissions::from_mode(0o755)).unwrap();

        let start = std::time::Instant::now();
        let result = git_toplevel_of_with(&std::env::temp_dir(), &fake, Duration::from_millis(400));
        let elapsed = start.elapsed();
        assert_eq!(result, None);
        assert!(
            elapsed < Duration::from_secs(10),
            "wedged git must not block: took {elapsed:?}"
        );
        let _ = std::fs::remove_dir_all(&dir);
    }
}
