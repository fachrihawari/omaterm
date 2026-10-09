use super::{WireCall, optional_selector};
use clap::Subcommand;
use serde_json::json;
use std::path::PathBuf;

#[derive(Debug, Clone, Subcommand)]
pub enum ProjectCmd {
    /// List authorized projects.
    List,
    /// Open a directory as a new project (creates even on duplicates).
    Open {
        /// Directory to open as a project.
        path: PathBuf,
        /// Optional display name.
        #[arg(long)]
        name: Option<String>,
    },
    /// Select a project within scope.
    Select {
        /// Project ID to select.
        project_id: String,
    },
    /// Change a project's base directory (future tabs use it).
    SetDirectory {
        /// Project ID to update.
        project_id: String,
        /// New base directory.
        directory: PathBuf,
    },
    /// Resolve the project's filesystem root (empty state for non-repos).
    Root {
        /// Project ID; defaults to OMATERM_PROJECT_ID, then server selection.
        #[arg(long)]
        project: Option<String>,
    },
    /// List repositories under the project root (depth-1 scan).
    Repos {
        /// Project ID; defaults to OMATERM_PROJECT_ID, then server selection.
        #[arg(long)]
        project: Option<String>,
    },
    /// Select the active repository for Git features in this project.
    SetRepo {
        /// Repository directory name under the project root (e.g. `api`).
        repo: String,
        /// Project ID; defaults to OMATERM_PROJECT_ID, then server selection.
        #[arg(long)]
        project: Option<String>,
    },
}

pub fn build(cmd: &ProjectCmd) -> Result<WireCall, String> {
    match cmd {
        ProjectCmd::List => Ok(WireCall {
            method: "project.list".into(),
            params: json!({}),
        }),
        ProjectCmd::Open { path, name } => {
            if let Some(name) = name
                && (name.trim().is_empty() || name.chars().any(char::is_control))
            {
                return Err("project --name must be non-empty without control characters".into());
            }
            Ok(WireCall {
                method: "project.create".into(),
                params: json!({
                    "directory": path.to_string_lossy(),
                    "name": name,
                }),
            })
        }
        ProjectCmd::Select { project_id } => {
            if project_id.trim().is_empty() {
                return Err("project select requires a project ID".into());
            }
            Ok(WireCall {
                method: "project.select".into(),
                params: json!({ "project_id": project_id }),
            })
        }
        ProjectCmd::SetDirectory {
            project_id,
            directory,
        } => {
            if project_id.trim().is_empty() {
                return Err("project set-directory requires a project ID".into());
            }
            Ok(WireCall {
                method: "project.set-directory".into(),
                params: json!({
                    "project_id": project_id,
                    "directory": directory.to_string_lossy(),
                }),
            })
        }
        ProjectCmd::Root { project } => Ok(WireCall {
            method: "project.root".into(),
            params: match optional_selector(project.clone(), "OMATERM_PROJECT_ID") {
                Some(id) => json!({ "project_id": id }),
                None => json!({}),
            },
        }),
        ProjectCmd::Repos { project } => Ok(WireCall {
            method: "project.repos".into(),
            params: match optional_selector(project.clone(), "OMATERM_PROJECT_ID") {
                Some(id) => json!({ "project_id": id }),
                None => json!({}),
            },
        }),
        ProjectCmd::SetRepo { repo, project } => {
            // Same shape rule as the live scan: a bare directory name, no
            // separators or traversal. Membership is validated server-side.
            if repo.is_empty()
                || repo.contains('/')
                || repo.contains('\\')
                || repo == "."
                || repo == ".."
            {
                return Err("project set-repo requires a single directory name".into());
            }
            let selector = match optional_selector(project.clone(), "OMATERM_PROJECT_ID") {
                Some(id) => id,
                None => return Err("project set-repo requires a project ID".into()),
            };
            Ok(WireCall {
                method: "project.set-active-repo".into(),
                params: json!({ "project_id": selector, "repo": repo }),
            })
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn list_open_and_select_map_to_wire() {
        assert_eq!(
            build(&ProjectCmd::List).unwrap(),
            WireCall {
                method: "project.list".into(),
                params: json!({}),
            }
        );
        let call = build(&ProjectCmd::Open {
            path: PathBuf::from("/tmp/app"),
            name: Some("My App".into()),
        })
        .unwrap();
        assert_eq!(call.method, "project.create");
        assert_eq!(call.params["directory"], "/tmp/app");
        assert_eq!(call.params["name"], "My App");
        let call = build(&ProjectCmd::Select {
            project_id: "p1".into(),
        })
        .unwrap();
        assert_eq!(call.method, "project.select");
        let call = build(&ProjectCmd::SetDirectory {
            project_id: "p1".into(),
            directory: PathBuf::from("/tmp/app"),
        })
        .unwrap();
        assert_eq!(call.method, "project.set-directory");
        assert_eq!(call.params["project_id"], "p1");
        assert_eq!(call.params["directory"], "/tmp/app");
        assert!(
            build(&ProjectCmd::SetDirectory {
                project_id: "  ".into(),
                directory: PathBuf::from("/tmp"),
            })
            .is_err()
        );
        let call = build(&ProjectCmd::Root { project: None }).unwrap();
        assert_eq!(call.method, "project.root");
        assert_eq!(call.params, json!({}));
        let call = build(&ProjectCmd::Root {
            project: Some("p1".into()),
        })
        .unwrap();
        assert_eq!(call.params["project_id"], "p1");
    }

    #[test]
    fn repos_and_set_repo_map_to_wire_with_guards() {
        let call = build(&ProjectCmd::Repos { project: None }).unwrap();
        assert_eq!(call.method, "project.repos");
        assert_eq!(call.params, json!({}));
        let call = build(&ProjectCmd::Repos {
            project: Some("p1".into()),
        })
        .unwrap();
        assert_eq!(call.params["project_id"], "p1");

        let call = build(&ProjectCmd::SetRepo {
            repo: "api".into(),
            project: Some("p1".into()),
        })
        .unwrap();
        assert_eq!(call.method, "project.set-active-repo");
        assert_eq!(call.params, json!({"project_id":"p1","repo":"api"}));

        // No project anywhere → refusal, never a silent server default.
        assert!(
            build(&ProjectCmd::SetRepo {
                repo: "api".into(),
                project: None,
            })
            .is_err()
        );
        // Traversal and separators are rejected before the wire.
        for bad in ["", ".", "..", "a/b", "a\\b"] {
            assert!(
                build(&ProjectCmd::SetRepo {
                    repo: bad.into(),
                    project: Some("p1".into()),
                })
                .is_err(),
                "repo {bad:?} must be rejected"
            );
        }
    }

    #[test]
    fn open_rejects_control_characters_in_name() {
        let result = build(&ProjectCmd::Open {
            path: PathBuf::from("/tmp"),
            name: Some("bad\nname".into()),
        });
        assert!(result.is_err());
    }
}
