use super::WireCall;
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
