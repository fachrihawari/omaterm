use crate::{SnapshotError, WorkspaceSnapshot};

pub(crate) fn decode(
    bytes: &[u8],
    limits: crate::SnapshotLimits,
) -> Result<WorkspaceSnapshot, SnapshotError> {
    if bytes.len() > limits.max_file_bytes {
        return Err(SnapshotError::TooLarge);
    }
    let value: serde_json::Value =
        serde_json::from_slice(bytes).map_err(|error| SnapshotError::Corrupt(error.to_string()))?;
    let version = value
        .get("schema_version")
        .and_then(serde_json::Value::as_u64)
        .and_then(|value| u32::try_from(value).ok())
        .ok_or_else(|| SnapshotError::Corrupt("missing or invalid schema_version".into()))?;
    if version != WorkspaceSnapshot::SCHEMA_VERSION
        && version != WorkspaceSnapshot::V2_SCHEMA_VERSION
        && version != WorkspaceSnapshot::V1_SCHEMA_VERSION
    {
        return Err(SnapshotError::UnsupportedVersion(version));
    }
    let mut snapshot: WorkspaceSnapshot =
        serde_json::from_value(value).map_err(|error| SnapshotError::Corrupt(error.to_string()))?;
    if version < WorkspaceSnapshot::SCHEMA_VERSION {
        // Schema 1/2 never persisted editor state. Do not accept opportunistic
        // registry fields in an old-version file as though they were validated
        // schema-3 metadata.
        for window in &mut snapshot.windows {
            for project in &mut window.projects {
                project.documents.clear();
                project.active_document = None;
            }
        }
        snapshot.schema_version = WorkspaceSnapshot::SCHEMA_VERSION;
    }
    snapshot.validate(limits)?;
    Ok(snapshot)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{CwdProvenance, DocumentSnapshot, PersistedCwd};
    use omaterm_core::{Pane, PaneTree, Project, Tab, WorkspaceWindow};
    use std::{collections::HashMap, path::PathBuf};

    #[test]
    fn classifies_future_versions_before_decoding_their_body() {
        let error = decode(
            br#"{"schema_version":77,"future_shape":true}"#,
            crate::SnapshotLimits::default(),
        )
        .unwrap_err();
        assert!(matches!(error, SnapshotError::UnsupportedVersion(77)));
    }

    #[test]
    fn migrates_v1_and_v2_to_schema_three_with_empty_document_registries() {
        let pane = Pane::empty();
        let pane_id = pane.id;
        let tab = Tab::new(PaneTree::new(pane), pane_id).unwrap();
        let mut project = Project::new(Some("preserved".into()), Some(PathBuf::from("/tmp")));
        project.add_tab(tab).unwrap();
        let project_id = project.id;
        let mut window = WorkspaceWindow::new();
        window.add_project(project).unwrap();
        let cwd = HashMap::from([(
            pane_id,
            PersistedCwd {
                path: PathBuf::from("/tmp"),
                provenance: CwdProvenance::Osc7,
            },
        )]);
        let expanded = HashMap::from([(project_id, vec![PathBuf::from("src")])]);
        let mut snapshot = WorkspaceSnapshot::capture_with_expanded(&window, &cwd, &expanded);
        snapshot.windows[0].projects[0].documents = vec![DocumentSnapshot {
            id: uuid::Uuid::new_v4().to_string(),
            path_bytes: b"not-used-by-old-schemas".to_vec(),
            root_device: 0,
            root_inode: 0,
        }];
        snapshot.windows[0].projects[0].active_document = Some("not-a-valid-document-id".into());
        for version in [
            WorkspaceSnapshot::V1_SCHEMA_VERSION,
            WorkspaceSnapshot::V2_SCHEMA_VERSION,
        ] {
            let mut value = serde_json::to_value(&snapshot).unwrap();
            value["schema_version"] = serde_json::json!(version);
            if version == WorkspaceSnapshot::V1_SCHEMA_VERSION {
                value["windows"][0]["projects"][0]
                    .as_object_mut()
                    .unwrap()
                    .remove("expanded_dirs");
            }
            let decoded = decode(
                &serde_json::to_vec(&value).unwrap(),
                crate::SnapshotLimits::default(),
            )
            .unwrap();
            assert_eq!(decoded.schema_version, WorkspaceSnapshot::SCHEMA_VERSION);
            let project = &decoded.windows[0].projects[0];
            assert_eq!(project.custom_name.as_deref(), Some("preserved"));
            assert!(project.documents.is_empty());
            assert_eq!(project.active_document, None);
            let restored = decoded.validate(crate::SnapshotLimits::default()).unwrap();
            assert_eq!(restored.window, window);
            assert_eq!(
                restored.pane_cwds,
                cwd.clone().into_iter().collect::<Vec<_>>()
            );
            assert_eq!(
                restored.expanded_dirs,
                vec![(
                    project_id,
                    if version == WorkspaceSnapshot::V1_SCHEMA_VERSION {
                        vec![]
                    } else {
                        vec![PathBuf::from("src")]
                    }
                )]
            );
            assert!(restored.document_registries[0].1.documents.is_empty());
        }
    }

    #[test]
    fn rejects_non_metadata_document_fields() {
        let project_id = uuid::Uuid::new_v4().to_string();
        let document_id = uuid::Uuid::new_v4().to_string();
        let value = serde_json::json!({
            "schema_version": WorkspaceSnapshot::SCHEMA_VERSION,
            "windows": [{
                "id": uuid::Uuid::new_v4().to_string(),
                "selected_project": project_id,
                "projects": [{
                    "id": project_id,
                    "custom_name": null,
                    "pinned_directory": null,
                    "selected_tab": null,
                    "tabs": [],
                    "expanded_dirs": [],
                    "documents": [{
                        "id": document_id,
                        "path_bytes": [115, 114, 99, 47, 109, 97, 105, 110, 46, 114, 115],
                        "root_device": 7,
                        "root_inode": 11
                    }],
                    "active_document": document_id
                }]
            }]
        });
        for injected in [
            {
                let mut injected = value.clone();
                injected["windows"][0]["projects"][0]["dirty_text"] =
                    serde_json::json!("must never be persisted");
                injected
            },
            {
                let mut injected = value.clone();
                injected["windows"][0]["projects"][0]["documents"][0]["text"] =
                    serde_json::json!("must never be persisted");
                injected
            },
        ] {
            let error = decode(
                &serde_json::to_vec(&injected).unwrap(),
                crate::SnapshotLimits::default(),
            )
            .unwrap_err();
            assert!(matches!(error, SnapshotError::Corrupt(_)));
        }
    }
}
