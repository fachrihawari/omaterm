use super::{WireCall, optional_selector};
use clap::Subcommand;
use serde_json::json;
use std::path::PathBuf;

#[derive(Debug, Clone, Subcommand)]
pub enum FileCmd {
    /// List a directory under the resolved project root.
    List {
        /// Project ID; defaults to OMATERM_PROJECT_ID, then server selection.
        #[arg(long)]
        project: Option<String>,
        /// Directory relative to the root; defaults to the root itself.
        #[arg(long)]
        dir: Option<PathBuf>,
        /// Maximum entries (server default 100, cap 5000).
        #[arg(long)]
        limit: Option<usize>,
    },
    /// Fuzzy filename search over the resolved project root (`Ctrl+P` backend).
    Search {
        /// Project ID; defaults to OMATERM_PROJECT_ID, then server selection.
        #[arg(long)]
        project: Option<String>,
        /// Filename query (1-256 bytes, no control characters).
        query: String,
        /// Maximum entries (server default 100, cap 5000).
        #[arg(long)]
        limit: Option<usize>,
    },
    /// Open a path in `$EDITOR` inside the focused terminal (`terminal.run`).
    Open {
        /// Project ID; defaults to OMATERM_PROJECT_ID, then server selection.
        #[arg(long)]
        project: Option<String>,
        /// Path relative to the root (or absolute inside it).
        path: PathBuf,
    },
}

pub fn build(cmd: &FileCmd) -> Result<WireCall, String> {
    match cmd {
        FileCmd::List {
            project,
            dir,
            limit,
        } => {
            if limit.is_some_and(|n| n == 0 || n > 5_000) {
                return Err("file list --limit must be between 1 and 5000".into());
            }
            Ok(WireCall {
                method: "file.list".into(),
                params: json!({
                    "project_id": optional_selector(project.clone(), "OMATERM_PROJECT_ID"),
                    "dir": dir.as_ref().map(|dir| dir.to_string_lossy()),
                    "limit": limit,
                }),
            })
        }
        FileCmd::Search {
            project,
            query,
            limit,
        } => {
            if query.is_empty() || query.len() > 256 || query.chars().any(char::is_control) {
                return Err(
                    "file search query must be 1 to 256 bytes with no control characters".into(),
                );
            }
            if limit.is_some_and(|n| n == 0 || n > 5_000) {
                return Err("file search --limit must be between 1 and 5000".into());
            }
            Ok(WireCall {
                method: "file.search".into(),
                params: json!({
                    "project_id": optional_selector(project.clone(), "OMATERM_PROJECT_ID"),
                    "query": query,
                    "limit": limit,
                }),
            })
        }
        FileCmd::Open { project, path } => {
            if path.as_os_str().is_empty() {
                return Err("file open requires a path".into());
            }
            Ok(WireCall {
                method: "file.open".into(),
                params: json!({
                    "project_id": optional_selector(project.clone(), "OMATERM_PROJECT_ID"),
                    "path": path.to_string_lossy(),
                }),
            })
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn list_search_and_open_map_to_wire() {
        let call = build(&FileCmd::List {
            project: None,
            dir: None,
            limit: None,
        })
        .unwrap();
        assert_eq!(call.method, "file.list");
        assert_eq!(
            call.params,
            serde_json::json!({"project_id": null, "dir": null, "limit": null})
        );
        let call = build(&FileCmd::Search {
            project: Some("p".into()),
            query: "main".into(),
            limit: Some(20),
        })
        .unwrap();
        assert_eq!(call.method, "file.search");
        assert_eq!(call.params["query"], "main");
        assert_eq!(call.params["limit"], 20);
        let call = build(&FileCmd::Open {
            project: None,
            path: PathBuf::from("src/main.rs"),
        })
        .unwrap();
        assert_eq!(call.method, "file.open");
        assert_eq!(call.params["path"], "src/main.rs");
        assert!(
            build(&FileCmd::List {
                project: None,
                dir: None,
                limit: Some(0),
            })
            .is_err()
        );
        assert!(
            build(&FileCmd::Search {
                project: None,
                query: String::new(),
                limit: None,
            })
            .is_err()
        );
        assert!(
            build(&FileCmd::Search {
                project: None,
                query: "x".into(),
                limit: Some(9_999),
            })
            .is_err()
        );
    }
}
