//! Thin `diff` CLI-to-wire mapping (M15). No workspace logic lives here:
//! each command builds exactly one `(method, params)` pair; the desktop
//! owner resolves the root, runs the system binary, and enforces bounds.

use super::{WireCall, optional_selector};
use clap::Subcommand;
use serde_json::json;
use std::path::{Path, PathBuf};

#[derive(Debug, Clone, Subcommand)]
pub enum DiffCmd {
    /// Show unified hunks for unstaged changes, staged changes
    /// (`--staged`), or one filtered path (`--path`).
    Show {
        /// Project ID; defaults to OMATERM_PROJECT_ID, then server selection.
        #[arg(long)]
        project: Option<String>,
        /// Single root-relative path (or absolute inside the root).
        #[arg(long)]
        path: Option<PathBuf>,
        /// Read `git diff --cached` instead of the worktree diff.
        #[arg(long)]
        staged: bool,
        /// Unified context lines, 0–10 (default 3).
        #[arg(long)]
        context: Option<u8>,
    },
    /// List changed files with hunk counts but no bodies (fast surface).
    ListFiles {
        /// Project ID; defaults to OMATERM_PROJECT_ID, then server selection.
        #[arg(long)]
        project: Option<String>,
        /// Read `git diff --cached` instead of the worktree diff.
        #[arg(long)]
        staged: bool,
    },
    /// Show one committed file's patch against a chosen parent/base. Strictly
    /// read-only: no stage/unstage channel is exposed.
    ShowCommit {
        /// Full commit object ID (40 or 64 hexadecimal characters).
        commit: String,
        /// Project ID; defaults to OMATERM_PROJECT_ID, then server selection.
        #[arg(long)]
        project: Option<String>,
        /// Single commit-relative path (old path via `--old-path` for renames).
        #[arg(long)]
        path: PathBuf,
        /// Pre-rename path for `R`/`C` entries.
        #[arg(long)]
        old_path: Option<PathBuf>,
        /// Full parent object ID, or `empty_tree` for a root commit.
        #[arg(long)]
        parent: Option<String>,
        /// Unified context lines, 0–10 (default 3).
        #[arg(long)]
        context: Option<u8>,
    },
}

fn check_path(path: &Path) -> Result<(), String> {
    if path.as_os_str().is_empty()
        || path.as_os_str().len() > 4096
        || path.to_string_lossy().chars().any(char::is_control)
    {
        return Err(
            "diff path must be non-empty, at most 4096 bytes, with no control characters".into(),
        );
    }
    Ok(())
}

fn check_object_id(value: &str, field: &str) -> Result<(), String> {
    let valid = matches!(value.len(), 40 | 64)
        && value.bytes().all(|byte| byte.is_ascii_hexdigit())
        && !value.chars().any(char::is_control);
    if valid {
        Ok(())
    } else {
        Err(format!(
            "diff {field} must be a full 40- or 64-character hexadecimal object ID"
        ))
    }
}

pub fn build(cmd: &DiffCmd) -> Result<WireCall, String> {
    match cmd {
        DiffCmd::Show {
            project,
            path,
            staged,
            context,
        } => {
            if let Some(path) = path {
                check_path(path)?;
            }
            let context = context.unwrap_or(3);
            if context > 10 {
                return Err("diff context lines must be between 0 and 10".into());
            }
            Ok(WireCall {
                method: "diff.show".into(),
                params: json!({
                    "project_id": optional_selector(project.clone(), "OMATERM_PROJECT_ID"),
                    "path": path.as_ref().map(|path| path.to_string_lossy().into_owned()),
                    "staged": staged,
                    "context_lines": context,
                }),
            })
        }
        DiffCmd::ListFiles { project, staged } => Ok(WireCall {
            method: "diff.list-files".into(),
            params: json!({
                "project_id": optional_selector(project.clone(), "OMATERM_PROJECT_ID"),
                "staged": staged,
            }),
        }),
        DiffCmd::ShowCommit {
            commit,
            project,
            path,
            old_path,
            parent,
            context,
        } => {
            check_object_id(commit, "commit")?;
            if let Some(parent) = parent
                && parent != "empty_tree"
            {
                check_object_id(parent, "parent")?;
            }
            check_path(path)?;
            if let Some(old) = old_path {
                check_path(old)?;
            }
            let context = context.unwrap_or(3);
            if context > 10 {
                return Err("diff context lines must be between 0 and 10".into());
            }
            Ok(WireCall {
                method: "diff.show-commit".into(),
                params: json!({
                    "project_id": optional_selector(project.clone(), "OMATERM_PROJECT_ID"),
                    "commit": commit,
                    "parent": parent,
                    "old_path": old_path.as_ref().map(|path| path.to_string_lossy().into_owned()),
                    "path": path.to_string_lossy(),
                    "context_lines": context,
                }),
            })
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn show_and_list_files_map_to_wire() {
        let call = build(&DiffCmd::Show {
            project: None,
            path: None,
            staged: false,
            context: None,
        })
        .unwrap();
        assert_eq!(call.method, "diff.show");
        assert_eq!(call.params["context_lines"], 3);
        assert_eq!(call.params["staged"], false);

        let call = build(&DiffCmd::Show {
            project: Some("p".into()),
            path: Some(PathBuf::from("src/main.rs")),
            staged: true,
            context: Some(5),
        })
        .unwrap();
        assert_eq!(call.params["path"], "src/main.rs");
        assert_eq!(call.params["context_lines"], 5);

        let call = build(&DiffCmd::ListFiles {
            project: None,
            staged: true,
        })
        .unwrap();
        assert_eq!(call.method, "diff.list-files");
        assert_eq!(call.params["staged"], true);

        assert!(
            build(&DiffCmd::Show {
                project: None,
                path: None,
                staged: false,
                context: Some(11),
            })
            .is_err()
        );
        assert!(
            build(&DiffCmd::Show {
                project: None,
                path: Some(PathBuf::from("bad\npath")),
                staged: false,
                context: None,
            })
            .is_err()
        );
    }
}
