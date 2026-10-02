//! Thin `git` CLI-to-wire mapping (M14). No workspace logic lives here:
//! each command builds exactly one `(method, params)` pair; the desktop
//! owner resolves the root, runs the system binary, and enforces bounds.

use super::{WireCall, optional_selector};
use clap::Subcommand;
use serde_json::json;
use std::path::PathBuf;

#[derive(Debug, Clone, Subcommand)]
pub enum GitCmd {
    /// Stage one current unstaged hunk using its diff.show ID.
    StageHunk {
        #[arg(long)]
        project: Option<String>,
        path: PathBuf,
        #[arg(long)]
        hunk: u64,
    },
    /// Show branch + staged/unstaged/untracked groups for the project root.
    Status {
        /// Project ID; defaults to OMATERM_PROJECT_ID, then server selection.
        #[arg(long)]
        project: Option<String>,
    },
    /// Stage explicit root-relative paths (`git add -- <paths>`).
    Stage {
        /// Project ID; defaults to OMATERM_PROJECT_ID, then server selection.
        #[arg(long)]
        project: Option<String>,
        /// Paths relative to the root (or absolute inside it), 1–100.
        paths: Vec<PathBuf>,
    },
    /// Unstage explicit root-relative paths (index restored, worktree kept).
    Unstage {
        /// Project ID; defaults to OMATERM_PROJECT_ID, then server selection.
        #[arg(long)]
        project: Option<String>,
        /// Paths relative to the root (or absolute inside it), 1–100.
        paths: Vec<PathBuf>,
    },
    /// Discard explicit root-relative paths (tracked restored from HEAD,
    /// untracked deleted). Desktop arms this two-step; the CLI dispatches
    /// directly, so double-check paths before running.
    Discard {
        /// Project ID; defaults to OMATERM_PROJECT_ID, then server selection.
        #[arg(long)]
        project: Option<String>,
        /// Paths relative to the root (or absolute inside it), 1–100.
        paths: Vec<PathBuf>,
    },
    /// Commit staged changes with an explicit message (author from repo
    /// config; nothing staged fails server-side with `git_failed`).
    Commit {
        /// Project ID; defaults to OMATERM_PROJECT_ID, then server selection.
        #[arg(long)]
        project: Option<String>,
        /// Commit message (1–4096 bytes; newline/tab allowed for the body).
        #[arg(short, long)]
        message: String,
    },
}

fn check_paths(paths: &[PathBuf], verb: &str) -> Result<(), String> {
    if paths.is_empty() || paths.len() > 100 {
        return Err(format!("git {verb} requires 1 to 100 paths"));
    }
    if paths.iter().any(|path| {
        path.as_os_str().is_empty()
            || path.as_os_str().len() > 4096
            || path.to_string_lossy().chars().any(char::is_control)
    }) {
        return Err(format!(
            "each git {verb} path must be non-empty, at most 4096 bytes, with no control characters"
        ));
    }
    Ok(())
}

pub fn build(cmd: &GitCmd) -> Result<WireCall, String> {
    // Render once: iterators are not `Serialize`.
    let path_strings = |paths: &[PathBuf]| {
        paths
            .iter()
            .map(|path| path.to_string_lossy().into_owned())
            .collect::<Vec<_>>()
    };
    match cmd {
        GitCmd::StageHunk {
            project,
            path,
            hunk,
        } => {
            check_paths(std::slice::from_ref(path), "stage-hunk")?;
            if *hunk == 0 {
                return Err("git hunk id must be non-zero".into());
            }
            Ok(WireCall {
                method: "git.stage-hunk".into(),
                params: json!({
                    "project_id": optional_selector(project.clone(), "OMATERM_PROJECT_ID"),
                    "path": path.to_string_lossy(),
                    "hunk_id": hunk,
                }),
            })
        }
        GitCmd::Status { project } => Ok(WireCall {
            method: "git.status".into(),
            params: json!({
                "project_id": optional_selector(project.clone(), "OMATERM_PROJECT_ID"),
            }),
        }),
        GitCmd::Stage { project, paths } => {
            check_paths(paths, "stage")?;
            Ok(WireCall {
                method: "git.stage".into(),
                params: json!({
                    "project_id": optional_selector(project.clone(), "OMATERM_PROJECT_ID"),
                    "paths": path_strings(paths),
                }),
            })
        }
        GitCmd::Unstage { project, paths } => {
            check_paths(paths, "unstage")?;
            Ok(WireCall {
                method: "git.unstage".into(),
                params: json!({
                    "project_id": optional_selector(project.clone(), "OMATERM_PROJECT_ID"),
                    "paths": path_strings(paths),
                }),
            })
        }
        GitCmd::Discard { project, paths } => {
            check_paths(paths, "discard")?;
            Ok(WireCall {
                method: "git.discard".into(),
                params: json!({
                    "project_id": optional_selector(project.clone(), "OMATERM_PROJECT_ID"),
                    "paths": path_strings(paths),
                }),
            })
        }
        GitCmd::Commit { project, message } => {
            if message.is_empty()
                || message.len() > 4096
                || message
                    .chars()
                    .any(|char| char.is_control() && char != '\n' && char != '\t')
            {
                return Err(
                    "git commit message must be 1 to 4096 bytes with no control characters besides newline/tab"
                        .into(),
                );
            }
            Ok(WireCall {
                method: "git.commit".into(),
                params: json!({
                    "project_id": optional_selector(project.clone(), "OMATERM_PROJECT_ID"),
                    "message": message,
                }),
            })
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn hunk_stage_maps_selector_and_rejects_zero() {
        let command = GitCmd::StageHunk {
            project: Some("project".into()),
            path: PathBuf::from("name with spaces.txt"),
            hunk: 42,
        };
        let call = build(&command).unwrap();
        assert_eq!(call.method, "git.stage-hunk");
        assert_eq!(call.params["hunk_id"], 42);
        assert_eq!(call.params["path"], "name with spaces.txt");
        assert!(
            build(&GitCmd::StageHunk {
                project: None,
                path: PathBuf::from("a"),
                hunk: 0
            })
            .is_err()
        );
    }

    #[test]
    fn status_stage_unstage_and_discard_map_to_wire() {
        let call = build(&GitCmd::Status { project: None }).unwrap();
        assert_eq!(call.method, "git.status");
        assert_eq!(call.params, serde_json::json!({"project_id": null}));
        for (cmd, method, first) in [
            (
                GitCmd::Stage {
                    project: Some("p".into()),
                    paths: vec![PathBuf::from("a.txt")],
                },
                "git.stage",
                "a.txt",
            ),
            (
                GitCmd::Unstage {
                    project: None,
                    paths: vec![PathBuf::from("b.txt")],
                },
                "git.unstage",
                "b.txt",
            ),
            (
                GitCmd::Discard {
                    project: None,
                    paths: vec![PathBuf::from("c.txt")],
                },
                "git.discard",
                "c.txt",
            ),
            (
                GitCmd::Commit {
                    project: None,
                    message: "hello".into(),
                },
                "git.commit",
                "hello",
            ),
        ] {
            let call = build(&cmd).unwrap();
            assert_eq!(call.method, method);
            if method == "git.commit" {
                assert_eq!(call.params["message"], first);
            } else {
                assert_eq!(call.params["paths"][0], first);
            }
        }
        assert!(
            build(&GitCmd::Stage {
                project: None,
                paths: vec![],
            })
            .is_err()
        );
        assert!(
            build(&GitCmd::Discard {
                project: None,
                paths: vec![PathBuf::from("bad\npath")],
            })
            .is_err()
        );
        assert!(
            build(&GitCmd::Commit {
                project: None,
                message: String::new(),
            })
            .is_err()
        );
        assert!(
            build(&GitCmd::Commit {
                project: None,
                message: "bad\0message".into(),
            })
            .is_err()
        );
    }
}
