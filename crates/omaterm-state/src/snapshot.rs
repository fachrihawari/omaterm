use std::collections::{HashMap, HashSet};
use std::path::PathBuf;

use omaterm_core::{
    DocumentId, Pane, PaneContent, PaneId, PaneNode, PaneTree, Project, ProjectId, SplitAxis,
    SplitId, Tab, TabId, WindowId, WorkspaceWindow,
};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum CwdProvenance {
    Launch,
    Osc7,
    Procfs,
    FallbackHome,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct PersistedCwd {
    pub path: PathBuf,
    pub provenance: CwdProvenance,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum PaneNodeSnapshot {
    Pane {
        id: String,
        working_directory: PathBuf,
        cwd_provenance: CwdProvenance,
        /// Shell program this pane last launched. Missing on older files.
        #[serde(default, skip_serializing_if = "Option::is_none")]
        shell: Option<String>,
    },
    Split {
        id: String,
        axis: SplitAxisSnapshot,
        fraction: f32,
        first: Box<PaneNodeSnapshot>,
        second: Box<PaneNodeSnapshot>,
    },
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum SplitAxisSnapshot {
    Horizontal,
    Vertical,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct WorkspaceSnapshot {
    pub schema_version: u32,
    pub windows: Vec<WindowSnapshot>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct WindowSnapshot {
    pub id: String,
    pub selected_project: Option<String>,
    pub projects: Vec<ProjectSnapshot>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ProjectSnapshot {
    pub id: String,
    pub custom_name: Option<String>,
    pub pinned_directory: Option<PathBuf>,
    pub selected_tab: Option<String>,
    pub tabs: Vec<TabSnapshot>,
    /// Expanded file-tree directories, relative to the project root
    /// (M13, blueprint §30 sidebar state). Bounded to 128 per project;
    /// absent in schema v1 (defaults to empty on migration).
    #[serde(default)]
    pub expanded_dirs: Vec<PathBuf>,
    /// Selected child repository for multi-repo projects (M20): a single
    /// directory name resolved against the live project root, so project
    /// directory moves don't break it. `None` means first-sorted default.
    /// Absent in schema v1–v3 (defaults to `None` on migration).
    #[serde(default)]
    pub active_repo: Option<String>,
    /// Open editor documents. This deliberately stores metadata only: no text,
    /// cursor, history, clipboard, or runtime handles.
    #[serde(default)]
    pub documents: Vec<DocumentSnapshot>,
    /// The selected editor document, independent from the selected terminal tab.
    #[serde(default)]
    pub active_document: Option<String>,
}

/// Lossless, root-scoped identity for one open editor document.
///
/// `path_bytes` is a root-relative Unix path encoded as JSON numeric bytes so
/// non-UTF-8 filenames never pass through a lossy display string.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct DocumentSnapshot {
    pub id: String,
    pub path_bytes: Vec<u8>,
    pub root_device: u64,
    pub root_inode: u64,
}

/// A validated document descriptor safe to hand to restart restoration.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DocumentDescriptor {
    pub id: DocumentId,
    pub path_bytes: Vec<u8>,
    pub root_device: u64,
    pub root_inode: u64,
}

/// The validated, metadata-only editor registry belonging to one project.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct DocumentRegistry {
    pub documents: Vec<DocumentDescriptor>,
    pub active_document: Option<DocumentId>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct TabSnapshot {
    pub id: String,
    pub custom_name: Option<String>,
    pub focused_pane: String,
    pub root: PaneNodeSnapshot,
}

impl WorkspaceSnapshot {
    pub const SCHEMA_VERSION: u32 = 4;
    /// Previous schema version (M13 migration source). v1 snapshots carry
    /// no `expanded_dirs` and decode with an empty expansion set.
    pub const V1_SCHEMA_VERSION: u32 = 1;
    /// Previous schema version. v2 snapshots carry no document registry.
    pub const V2_SCHEMA_VERSION: u32 = 2;
    /// Previous schema version. v3 snapshots carry no multi-repo selection.
    pub const V3_SCHEMA_VERSION: u32 = 3;

    pub fn capture(window: &WorkspaceWindow, pane_cwds: &HashMap<PaneId, PersistedCwd>) -> Self {
        Self::capture_with_expanded(window, pane_cwds, &HashMap::new())
    }

    /// Capture with per-project expanded file-tree directories (relative
    /// to each project root, truncated to the limit). Keys for unknown
    /// projects are ignored.
    pub fn capture_with_expanded(
        window: &WorkspaceWindow,
        pane_cwds: &HashMap<PaneId, PersistedCwd>,
        expanded: &HashMap<ProjectId, Vec<PathBuf>>,
    ) -> Self {
        Self::capture_with_expanded_and_documents(
            window,
            pane_cwds,
            expanded,
            &HashMap::new(),
            &HashMap::new(),
        )
    }

    /// Capture terminal workspace state together with bounded, metadata-only
    /// editor registries. Registries for unknown projects are ignored.
    pub fn capture_with_expanded_and_documents(
        window: &WorkspaceWindow,
        pane_cwds: &HashMap<PaneId, PersistedCwd>,
        expanded: &HashMap<ProjectId, Vec<PathBuf>>,
        document_registries: &HashMap<ProjectId, DocumentRegistry>,
        pane_shells: &HashMap<PaneId, String>,
    ) -> Self {
        let limits = SnapshotLimits::default();
        let mut remaining_documents = limits.max_documents;
        let projects = window
            .projects
            .iter()
            .map(|project| {
                let registry = document_registries.get(&project.id);
                let documents = registry
                    .map(|registry| {
                        registry
                            .documents
                            .iter()
                            .take(remaining_documents.min(limits.max_documents_per_project))
                            .map(|document| DocumentSnapshot {
                                id: document.id.0.to_string(),
                                path_bytes: document.path_bytes.clone(),
                                root_device: document.root_device,
                                root_inode: document.root_inode,
                            })
                            .collect::<Vec<_>>()
                    })
                    .unwrap_or_default();
                remaining_documents -= documents.len();
                let active_document = registry
                    .and_then(|registry| registry.active_document)
                    .filter(|active| {
                        documents
                            .iter()
                            .any(|document| document.id == active.0.to_string())
                    })
                    .map(|id| id.0.to_string());
                ProjectSnapshot {
                    id: project.id.0.to_string(),
                    custom_name: project.custom_name.clone(),
                    pinned_directory: project.pinned_directory.clone(),
                    active_repo: project.active_repo.clone(),
                    selected_tab: project.selected_tab.map(|id| id.0.to_string()),
                    expanded_dirs: expanded
                        .get(&project.id)
                        .map(|dirs| {
                            dirs.iter()
                                .filter(|dir| !dir.as_os_str().is_empty())
                                .take(SnapshotLimits::default().max_expanded_dirs)
                                .cloned()
                                .collect()
                        })
                        .unwrap_or_default(),
                    documents,
                    active_document,
                    tabs: project
                        .tabs
                        .iter()
                        .map(|tab| TabSnapshot {
                            id: tab.id.0.to_string(),
                            custom_name: tab.custom_name.clone(),
                            focused_pane: tab.focused_pane.0.to_string(),
                            root: capture_node(
                                tab.tree
                                    .root()
                                    .expect("validated tabs always contain a pane"),
                                pane_cwds,
                                pane_shells,
                            ),
                        })
                        .collect(),
                }
            })
            .collect();
        Self {
            schema_version: Self::SCHEMA_VERSION,
            windows: vec![WindowSnapshot {
                id: window.id.0.to_string(),
                selected_project: window.selected_project.map(|id| id.0.to_string()),
                projects,
            }],
        }
    }

    pub fn validate(&self, limits: SnapshotLimits) -> Result<ValidatedSnapshot, SnapshotError> {
        if self.schema_version != Self::SCHEMA_VERSION {
            return Err(SnapshotError::UnsupportedVersion(self.schema_version));
        }
        if self.windows.len() != 1 {
            return Err(SnapshotError::Invalid(
                "M6 supports exactly one window".into(),
            ));
        }
        let mut ids = HashSet::new();
        let window = &self.windows[0];
        let window_id = WindowId(parse_uuid(&window.id, &mut ids)?);
        if window.projects.len() > limits.max_projects {
            return Err(SnapshotError::Invalid("too many projects".into()));
        }
        let mut projects = Vec::with_capacity(window.projects.len());
        let mut remaining_documents = limits.max_documents;
        let mut remaining_registry_bytes = limits.max_document_registry_bytes;
        for project in &window.projects {
            remaining_documents = remaining_documents
                .checked_sub(project.documents.len())
                .ok_or_else(|| SnapshotError::Invalid("too many documents in snapshot".into()))?;
            remaining_registry_bytes = remaining_registry_bytes
                .checked_sub(document_registry_bytes(project)?)
                .ok_or_else(|| {
                    SnapshotError::Invalid(
                        "document registries exceed serialized-byte limit".into(),
                    )
                })?;
            check_name(project.custom_name.as_deref(), limits)?;
            check_path(project.pinned_directory.as_deref(), limits)?;
            if let Some(repo) = project.active_repo.as_deref()
                && (repo.is_empty()
                    || repo == "."
                    || repo == ".."
                    || repo.contains('/')
                    || repo.contains('\\')
                    || repo.len() > limits.max_name_bytes)
            {
                return Err(SnapshotError::Invalid(
                    "active repo must be a single directory name".into(),
                ));
            }
            let project_id = ProjectId(parse_uuid(&project.id, &mut ids)?);
            let documents = validate_document_registry(project, limits, &mut ids)?;
            if project.expanded_dirs.len() > limits.max_expanded_dirs {
                return Err(SnapshotError::Invalid(
                    "too many expanded directories".into(),
                ));
            }
            let mut expanded_dirs = Vec::with_capacity(project.expanded_dirs.len());
            for dir in &project.expanded_dirs {
                if dir.is_absolute()
                    || dir.as_os_str().is_empty()
                    || dir.components().any(|c| {
                        matches!(
                            c,
                            std::path::Component::ParentDir | std::path::Component::Prefix(_)
                        )
                    })
                {
                    return Err(SnapshotError::Invalid(
                        "expanded directory must be a relative path without traversals".into(),
                    ));
                }
                check_path(Some(dir), limits)?;
                expanded_dirs.push(dir.clone());
            }
            if project.tabs.len() > limits.max_tabs_per_project {
                return Err(SnapshotError::Invalid("too many tabs in project".into()));
            }
            let mut tabs = Vec::with_capacity(project.tabs.len());
            for tab in &project.tabs {
                check_name(tab.custom_name.as_deref(), limits)?;
                let tab_id = TabId(parse_uuid(&tab.id, &mut ids)?);
                let focused = reference(&tab.focused_pane).map(PaneId)?;
                let mut cwd = Vec::new();
                let mut shells = Vec::new();
                let mut count = 0;
                let root = convert_node(
                    &tab.root,
                    1,
                    &mut count,
                    limits,
                    &mut ids,
                    &mut cwd,
                    &mut shells,
                )?;
                let tree = PaneTree::from_root(root)
                    .map_err(|error| SnapshotError::Invalid(error.to_string()))?;
                let tab = Tab {
                    id: tab_id,
                    custom_name: tab.custom_name.clone(),
                    tree,
                    focused_pane: focused,
                };
                if tab.tree.find(focused).is_none() {
                    return Err(SnapshotError::Invalid("focused pane does not exist".into()));
                }
                tabs.push((tab, cwd, shells));
            }
            let selected_tab = project
                .selected_tab
                .as_deref()
                .map(reference)
                .transpose()?
                .map(TabId);
            let core_project = Project {
                id: project_id,
                custom_name: project.custom_name.clone(),
                pinned_directory: project.pinned_directory.clone(),
                active_repo: project.active_repo.clone(),
                tabs: tabs.iter().map(|(tab, _, _)| tab.clone()).collect(),
                selected_tab,
            };
            core_project
                .validate()
                .map_err(|error| SnapshotError::Invalid(error.to_string()))?;
            projects.push((
                core_project,
                tabs.iter()
                    .flat_map(|(_, cwd, _)| cwd.clone())
                    .collect::<Vec<_>>(),
                tabs.into_iter()
                    .flat_map(|(_, _, shells)| shells)
                    .collect::<Vec<_>>(),
                expanded_dirs,
                documents,
            ));
        }
        let selected_project = window
            .selected_project
            .as_deref()
            .map(reference)
            .transpose()?
            .map(ProjectId);
        let core_window = WorkspaceWindow {
            id: window_id,
            projects: projects
                .iter()
                .map(|(project, _, _, _, _)| project.clone())
                .collect(),
            selected_project,
        };
        core_window
            .validate()
            .map_err(|error| SnapshotError::Invalid(error.to_string()))?;
        // Direct validation must enforce the same document-wide byte boundary
        // as decoding. The store separately caps its pretty-printed file bytes.
        if serde_json::to_vec(self)
            .map_err(|error| SnapshotError::Invalid(error.to_string()))?
            .len()
            > limits.max_file_bytes
        {
            return Err(SnapshotError::TooLarge);
        }
        let mut expanded_out = Vec::with_capacity(projects.len());
        let mut document_registries = Vec::with_capacity(projects.len());
        let mut pane_shells = Vec::new();
        let pane_cwds = projects
            .into_iter()
            .flat_map(|(project, cwds, shells, expanded, documents)| {
                expanded_out.push((project.id, expanded));
                document_registries.push((project.id, documents));
                pane_shells.extend(shells);
                cwds
            })
            .collect();
        Ok(ValidatedSnapshot {
            window: core_window,
            pane_cwds,
            pane_shells,
            expanded_dirs: expanded_out,
            document_registries,
        })
    }
}

fn validate_document_registry(
    project: &ProjectSnapshot,
    limits: SnapshotLimits,
    ids: &mut HashSet<Uuid>,
) -> Result<DocumentRegistry, SnapshotError> {
    if project.documents.len() > limits.max_documents_per_project {
        return Err(SnapshotError::Invalid(
            "too many documents in project".into(),
        ));
    }
    let mut keys = HashSet::with_capacity(project.documents.len());
    let mut documents = Vec::with_capacity(project.documents.len());
    for document in &project.documents {
        let id = DocumentId(parse_uuid(&document.id, ids)?);
        check_document_path(&document.path_bytes, limits)?;
        // Device zero is a valid st_dev value, not a missing identity sentinel.
        if document.root_inode == 0 {
            return Err(SnapshotError::Invalid(
                "document root inode must be nonzero".into(),
            ));
        }
        let key = (
            document.root_device,
            document.root_inode,
            document.path_bytes.clone(),
        );
        if !keys.insert(key) {
            return Err(SnapshotError::Invalid(
                "duplicate document descriptor".into(),
            ));
        }
        documents.push(DocumentDescriptor {
            id,
            path_bytes: document.path_bytes.clone(),
            root_device: document.root_device,
            root_inode: document.root_inode,
        });
    }

    let active_document = project
        .active_document
        .as_deref()
        .map(reference)
        .transpose()?
        .map(DocumentId);
    if active_document.is_some_and(|active| !documents.iter().any(|document| document.id == active))
    {
        return Err(SnapshotError::Invalid(
            "active document does not exist in project registry".into(),
        ));
    }
    Ok(DocumentRegistry {
        documents,
        active_document,
    })
}

fn document_registry_bytes(project: &ProjectSnapshot) -> Result<usize, SnapshotError> {
    serde_json::to_vec(&(&project.documents, &project.active_document))
        .map(|bytes| bytes.len())
        .map_err(|error| SnapshotError::Invalid(error.to_string()))
}

fn check_document_path(path: &[u8], limits: SnapshotLimits) -> Result<(), SnapshotError> {
    if path.is_empty() || path.len() > limits.max_path_bytes {
        return Err(SnapshotError::Invalid(
            "document path exceeds limit or is empty".into(),
        ));
    }
    if path.contains(&0) || path.starts_with(b"/") {
        return Err(SnapshotError::Invalid(
            "document path must be relative and contain no NUL".into(),
        ));
    }
    if path
        .split(|byte| *byte == b'/')
        .any(|component| component.is_empty() || matches!(component, b"." | b".."))
    {
        return Err(SnapshotError::Invalid(
            "document path must not contain empty or traversal components".into(),
        ));
    }
    Ok(())
}

fn capture_node(
    node: &PaneNode,
    cwds: &HashMap<PaneId, PersistedCwd>,
    shells: &HashMap<PaneId, String>,
) -> PaneNodeSnapshot {
    match node {
        PaneNode::Pane(pane) => {
            let cwd = cwds.get(&pane.id).cloned().unwrap_or_else(|| PersistedCwd {
                path: PathBuf::new(),
                provenance: CwdProvenance::Launch,
            });
            let shell = shells
                .get(&pane.id)
                .filter(|program| !program.is_empty())
                .cloned();
            PaneNodeSnapshot::Pane {
                id: pane.id.0.to_string(),
                working_directory: cwd.path,
                cwd_provenance: cwd.provenance,
                shell,
            }
        }
        PaneNode::Split {
            id,
            axis,
            fraction,
            first,
            second,
        } => PaneNodeSnapshot::Split {
            id: id.0.to_string(),
            axis: match axis {
                SplitAxis::Horizontal => SplitAxisSnapshot::Horizontal,
                SplitAxis::Vertical => SplitAxisSnapshot::Vertical,
            },
            fraction: *fraction,
            first: Box::new(capture_node(first, cwds, shells)),
            second: Box::new(capture_node(second, cwds, shells)),
        },
    }
}

fn convert_node(
    node: &PaneNodeSnapshot,
    depth: usize,
    count: &mut usize,
    limits: SnapshotLimits,
    ids: &mut HashSet<Uuid>,
    cwds: &mut Vec<(PaneId, PersistedCwd)>,
    shells: &mut Vec<(PaneId, String)>,
) -> Result<PaneNode, SnapshotError> {
    *count += 1;
    if depth > limits.max_tree_depth || *count > limits.max_nodes_per_tree {
        return Err(SnapshotError::Invalid("pane tree exceeds limits".into()));
    }
    match node {
        PaneNodeSnapshot::Pane {
            id,
            working_directory,
            cwd_provenance,
            shell,
        } => {
            check_path(Some(working_directory), limits)?;
            let id = PaneId(parse_uuid(id, ids)?);
            if let Some(program) = shell.as_deref().filter(|program| !program.is_empty()) {
                check_shell(program, limits)?;
                shells.push((id, program.to_string()));
            }
            if !working_directory.is_absolute() {
                return Err(SnapshotError::Invalid(
                    "working directory must be absolute".into(),
                ));
            }
            cwds.push((
                id,
                PersistedCwd {
                    path: working_directory.clone(),
                    provenance: *cwd_provenance,
                },
            ));
            Ok(PaneNode::Pane(Pane {
                id,
                content: PaneContent::Empty,
            }))
        }
        PaneNodeSnapshot::Split {
            id,
            axis,
            fraction,
            first,
            second,
        } => {
            let id = SplitId(parse_uuid(id, ids)?);
            if !fraction.is_finite() || !(0.1..=0.9).contains(fraction) {
                return Err(SnapshotError::Invalid(
                    "split fraction outside [0.1, 0.9]".into(),
                ));
            }
            Ok(PaneNode::Split {
                id,
                axis: match axis {
                    SplitAxisSnapshot::Horizontal => SplitAxis::Horizontal,
                    SplitAxisSnapshot::Vertical => SplitAxis::Vertical,
                },
                fraction: *fraction,
                first: Box::new(convert_node(
                    first,
                    depth + 1,
                    count,
                    limits,
                    ids,
                    cwds,
                    shells,
                )?),
                second: Box::new(convert_node(
                    second,
                    depth + 1,
                    count,
                    limits,
                    ids,
                    cwds,
                    shells,
                )?),
            })
        }
    }
}

fn reference(text: &str) -> Result<Uuid, SnapshotError> {
    Uuid::parse_str(text).map_err(|_| SnapshotError::Invalid("invalid UUID reference".into()))
}

fn parse_uuid(text: &str, ids: &mut HashSet<Uuid>) -> Result<Uuid, SnapshotError> {
    let uuid = Uuid::parse_str(text).map_err(|_| SnapshotError::Invalid("invalid UUID".into()))?;
    if uuid.is_nil() {
        return Err(SnapshotError::Invalid(
            "nil UUID is not a valid entity ID".into(),
        ));
    }
    if !ids.insert(uuid) {
        return Err(SnapshotError::Invalid("duplicate ID".into()));
    }
    Ok(uuid)
}

fn check_name(name: Option<&str>, limits: SnapshotLimits) -> Result<(), SnapshotError> {
    if name.is_some_and(|value| value.len() > limits.max_name_bytes) {
        return Err(SnapshotError::Invalid("name exceeds limit".into()));
    }
    Ok(())
}
fn check_shell(program: &str, limits: SnapshotLimits) -> Result<(), SnapshotError> {
    if program.len() > limits.max_path_bytes || program.chars().any(char::is_control) {
        return Err(SnapshotError::Invalid("shell program exceeds limit".into()));
    }
    Ok(())
}

fn check_path(path: Option<&std::path::Path>, limits: SnapshotLimits) -> Result<(), SnapshotError> {
    if path.is_some_and(|value| value.as_os_str().as_encoded_bytes().len() > limits.max_path_bytes)
    {
        return Err(SnapshotError::Invalid("path exceeds limit".into()));
    }
    Ok(())
}

#[derive(Debug, Clone, Copy)]
pub struct SnapshotLimits {
    pub max_file_bytes: usize,
    pub max_projects: usize,
    pub max_tabs_per_project: usize,
    pub max_tree_depth: usize,
    pub max_nodes_per_tree: usize,
    pub max_name_bytes: usize,
    pub max_path_bytes: usize,
    /// Bounded expanded file-tree directories per project (M13).
    pub max_expanded_dirs: usize,
    /// Bounded metadata-only open-document descriptors per project (M19).
    pub max_documents_per_project: usize,
    /// Maximum descriptors across all projects, matching the editor store cap.
    pub max_documents: usize,
    /// Maximum aggregate serialized JSON bytes for project document registries,
    /// including their active-document references.
    pub max_document_registry_bytes: usize,
}
impl Default for SnapshotLimits {
    fn default() -> Self {
        Self {
            max_file_bytes: 1_048_576,
            max_projects: 64,
            max_tabs_per_project: 128,
            max_tree_depth: 32,
            max_nodes_per_tree: 4096,
            max_name_bytes: 256,
            max_path_bytes: 4096,
            max_expanded_dirs: 128,
            max_documents_per_project: 32,
            max_documents: 32,
            // JSON encodes each path byte as up to three digits plus a comma.
            max_document_registry_bytes: 32 * (4 * 4096 + 256),
        }
    }
}

#[derive(Debug)]
pub struct ValidatedSnapshot {
    pub window: WorkspaceWindow,
    pub pane_cwds: Vec<(PaneId, PersistedCwd)>,
    /// Shell program last launched in each pane. Empty when the snapshot
    /// predates per-pane shells; restore then uses the current default.
    pub pane_shells: Vec<(PaneId, String)>,
    /// Per-project expanded file-tree directories (relative to root).
    pub expanded_dirs: Vec<(ProjectId, Vec<PathBuf>)>,
    /// Per-project validated open-document metadata. Desktop restore installs
    /// this logical state before separately scheduling bounded disk reads.
    pub document_registries: Vec<(ProjectId, DocumentRegistry)>,
}

#[derive(Debug, thiserror::Error)]
pub enum SnapshotError {
    #[error("snapshot exceeds maximum file size")]
    TooLarge,
    #[error("unsupported snapshot schema version {0}")]
    UnsupportedVersion(u32),
    #[error("corrupt snapshot: {0}")]
    Corrupt(String),
    #[error("invalid snapshot: {0}")]
    Invalid(String),
}

#[cfg(test)]
mod tests {
    use super::*;

    fn sample() -> (WorkspaceWindow, HashMap<PaneId, PersistedCwd>) {
        let pane = Pane::empty();
        let pane_id = pane.id;
        let tab = Tab::new(PaneTree::new(pane), pane_id).unwrap();
        let tab_id = tab.id;
        let mut project = Project::new(Some("demo".into()), Some(PathBuf::from("/tmp")));
        project.add_tab(tab).unwrap();
        let project_id = project.id;
        let mut window = WorkspaceWindow::new();
        window.add_project(project).unwrap();
        let mut cwd = HashMap::new();
        cwd.insert(
            pane_id,
            PersistedCwd {
                path: PathBuf::from("/tmp"),
                provenance: CwdProvenance::Osc7,
            },
        );
        assert_eq!(window.selected_project, Some(project_id));
        assert_eq!(
            window.project(project_id).unwrap().selected_tab,
            Some(tab_id)
        );
        (window, cwd)
    }

    fn document_snapshot(path_bytes: Vec<u8>) -> DocumentSnapshot {
        DocumentSnapshot {
            id: Uuid::new_v4().to_string(),
            path_bytes,
            root_device: 7,
            root_inode: 11,
        }
    }

    fn multi_project_documents(counts: &[usize]) -> WorkspaceSnapshot {
        let mut window = WorkspaceWindow::new();
        for _ in counts {
            window.add_project(Project::new(None, None)).unwrap();
        }
        let mut snapshot = WorkspaceSnapshot::capture(&window, &HashMap::new());
        for (project, count) in snapshot.windows[0].projects.iter_mut().zip(counts) {
            project.documents = (0..*count)
                .map(|index| document_snapshot(format!("src/{index}.rs").into_bytes()))
                .collect();
            project.active_document = project.documents.last().map(|document| document.id.clone());
        }
        snapshot
    }

    #[test]
    fn global_document_cap_accepts_32_across_projects_and_rejects_33() {
        let limits = SnapshotLimits::default();
        let snapshot = multi_project_documents(&[16, 16, 0]);
        let bytes = serde_json::to_vec(&snapshot).unwrap();
        let decoded = crate::migration::decode(&bytes, limits).unwrap();
        let restored = decoded.validate(limits).unwrap();
        assert_eq!(restored.document_registries.len(), 3);
        for (project, (_, registry)) in snapshot.windows[0]
            .projects
            .iter()
            .zip(&restored.document_registries)
        {
            assert_eq!(registry.documents.len(), project.documents.len());
            assert_eq!(
                registry.active_document.map(|id| id.0.to_string()),
                project.active_document
            );
        }

        let over = multi_project_documents(&[16, 17]);
        for result in [
            over.validate(limits).map(|_| ()),
            crate::migration::decode(&serde_json::to_vec(&over).unwrap(), limits).map(|_| ()),
        ] {
            assert!(matches!(result, Err(SnapshotError::Invalid(message))
                if message == "too many documents in snapshot"));
        }

        let mut foreign_active = snapshot.clone();
        foreign_active.windows[0].projects[1].active_document = foreign_active.windows[0].projects
            [0]
        .active_document
        .clone();
        assert!(
            matches!(foreign_active.validate(limits), Err(SnapshotError::Invalid(message))
            if message == "active document does not exist in project registry")
        );

        let mut duplicate_id = snapshot;
        duplicate_id.windows[0].projects[1].documents[0].id =
            duplicate_id.windows[0].projects[0].documents[0].id.clone();
        assert!(
            matches!(duplicate_id.validate(limits), Err(SnapshotError::Invalid(message))
            if message == "duplicate ID")
        );
    }

    #[test]
    fn capture_shares_document_budget_and_keeps_only_retained_active_references() {
        for counts in [&[16, 16, 0][..], &[16, 17, 1][..]] {
            let source = multi_project_documents(counts);
            let restored = source
                .validate(SnapshotLimits {
                    max_documents: 34,
                    ..SnapshotLimits::default()
                })
                .unwrap();
            let registries = restored.document_registries.into_iter().collect();
            let captured = WorkspaceSnapshot::capture_with_expanded_and_documents(
                &restored.window,
                &HashMap::new(),
                &HashMap::new(),
                &registries,
                &HashMap::new(),
            );
            let projects = &captured.windows[0].projects;
            assert_eq!(
                projects.iter().map(|p| p.documents.len()).sum::<usize>(),
                32
            );
            assert_eq!(
                projects[0].documents,
                source.windows[0].projects[0].documents
            );
            assert_eq!(
                projects[1].documents,
                source.windows[0].projects[1].documents[..16]
            );
            assert!(projects[2].documents.is_empty());
            assert_eq!(
                projects[0].active_document,
                source.windows[0].projects[0].active_document
            );
            assert_eq!(
                projects[1].active_document,
                if counts[1] == 16 {
                    source.windows[0].projects[1].active_document.clone()
                } else {
                    None
                }
            );
            assert_eq!(projects[2].active_document, None);
            captured.validate(SnapshotLimits::default()).unwrap();
        }
    }

    #[test]
    fn registry_byte_budget_is_global_and_includes_active_references() {
        let mut snapshot = multi_project_documents(&[16, 16]);
        for project in &mut snapshot.windows[0].projects {
            for (index, document) in project.documents.iter_mut().enumerate() {
                // Maximum-length lossless paths with worst-case JSON byte expansion.
                document.path_bytes = vec![255; SnapshotLimits::default().max_path_bytes];
                document.path_bytes[0] = 128 + index as u8;
            }
        }
        let registry_bytes = snapshot.windows[0]
            .projects
            .iter()
            .map(|project| document_registry_bytes(project).unwrap())
            .sum::<usize>();
        snapshot.validate(SnapshotLimits::default()).unwrap();
        let limits = SnapshotLimits {
            max_document_registry_bytes: registry_bytes,
            ..SnapshotLimits::default()
        };
        snapshot.validate(limits).unwrap();
        let bytes = serde_json::to_vec(&snapshot).unwrap();
        crate::migration::decode(&bytes, limits).unwrap();
        let over = SnapshotLimits {
            max_document_registry_bytes: registry_bytes - 1,
            ..limits
        };
        assert!(
            matches!(snapshot.validate(over), Err(SnapshotError::Invalid(message))
            if message == "document registries exceed serialized-byte limit")
        );
        assert!(matches!(
            crate::migration::decode(&bytes, over),
            Err(SnapshotError::Invalid(_))
        ));

        let mut no_active = snapshot.clone();
        for project in &mut no_active.windows[0].projects {
            project.active_document = None;
        }
        let no_active_bytes = no_active.windows[0]
            .projects
            .iter()
            .map(|project| document_registry_bytes(project).unwrap())
            .sum();
        assert!(no_active_bytes < registry_bytes);
        no_active
            .validate(SnapshotLimits {
                max_document_registry_bytes: no_active_bytes,
                ..limits
            })
            .unwrap();
        assert!(matches!(
            snapshot.validate(SnapshotLimits {
                max_document_registry_bytes: no_active_bytes,
                ..limits
            }),
            Err(SnapshotError::Invalid(_))
        ));
    }

    #[test]
    fn whole_snapshot_byte_cap_applies_to_direct_validation_and_raw_decode() {
        let snapshot = multi_project_documents(&[16, 16]);
        let bytes = serde_json::to_vec(&snapshot).unwrap();
        let limits = SnapshotLimits {
            max_file_bytes: bytes.len(),
            ..SnapshotLimits::default()
        };
        snapshot.validate(limits).unwrap();
        crate::migration::decode(&bytes, limits).unwrap();
        let over = SnapshotLimits {
            max_file_bytes: bytes.len() - 1,
            ..limits
        };
        assert!(matches!(
            snapshot.validate(over),
            Err(SnapshotError::TooLarge)
        ));
        assert!(matches!(
            crate::migration::decode(&bytes, over),
            Err(SnapshotError::TooLarge)
        ));
        let mut padded = bytes;
        padded.push(b' ');
        assert!(matches!(
            crate::migration::decode(&padded, limits),
            Err(SnapshotError::TooLarge)
        ));

        // Non-registry metadata must also consume the whole-snapshot budget.
        let mut bulky = multi_project_documents(&[0, 0, 0]);
        for project in &mut bulky.windows[0].projects {
            project.expanded_dirs = vec![PathBuf::from("x".repeat(4096)); 128];
        }
        assert!(matches!(
            bulky.validate(SnapshotLimits::default()),
            Err(SnapshotError::TooLarge)
        ));
    }

    #[test]
    fn accepts_device_zero_with_a_valid_inode_and_lossless_path() {
        let mut snapshot = multi_project_documents(&[1]);
        let document = &mut snapshot.windows[0].projects[0].documents[0];
        document.root_device = 0;
        document.path_bytes = b"src/non-utf8-\xff.rs".to_vec();
        let decoded = crate::migration::decode(
            &serde_json::to_vec(&snapshot).unwrap(),
            SnapshotLimits::default(),
        )
        .unwrap();
        let restored = decoded.validate(SnapshotLimits::default()).unwrap();
        let descriptor = &restored.document_registries[0].1.documents[0];
        assert_eq!(descriptor.root_device, 0);
        assert_eq!(descriptor.root_inode, 11);
        assert_eq!(descriptor.path_bytes, b"src/non-utf8-\xff.rs");
    }

    #[test]
    fn capture_encode_validate_round_trip_preserves_logical_ids_and_cwd() {
        let (window, cwd) = sample();
        let snapshot = WorkspaceSnapshot::capture(&window, &cwd);
        let bytes = serde_json::to_vec_pretty(&snapshot).unwrap();
        assert!(String::from_utf8_lossy(&bytes).contains("schema_version"));
        let decoded = crate::migration::decode(&bytes, SnapshotLimits::default()).unwrap();
        let restored = decoded.validate(SnapshotLimits::default()).unwrap();
        assert_eq!(restored.window, window);
        assert_eq!(restored.pane_cwds, cwd.into_iter().collect::<Vec<_>>());
        assert!(restored.pane_shells.is_empty());
        assert!(!String::from_utf8_lossy(&bytes).contains("session_id"));
    }

    #[test]
    fn pane_shell_round_trips_and_old_files_omit_it() {
        let (window, mut cwd) = sample();
        let pane_id = window.projects[0].tabs[0].focused_pane;
        cwd.get_mut(&pane_id).unwrap().path = std::env::temp_dir();
        let shells = HashMap::from([(pane_id, "powershell.exe".to_string())]);
        let snapshot = WorkspaceSnapshot::capture_with_expanded_and_documents(
            &window,
            &cwd,
            &HashMap::new(),
            &HashMap::new(),
            &shells,
        );
        let restored = snapshot.validate(SnapshotLimits::default()).unwrap();
        assert_eq!(
            restored.pane_shells,
            vec![(pane_id, "powershell.exe".to_string())]
        );
        let bare = WorkspaceSnapshot::capture(&window, &cwd);
        let text = serde_json::to_string(&bare).unwrap();
        assert!(!text.contains("\"shell\""));
        let decoded: WorkspaceSnapshot = serde_json::from_str(&text).unwrap();
        assert!(
            decoded
                .validate(SnapshotLimits::default())
                .unwrap()
                .pane_shells
                .is_empty()
        );
    }

    #[test]
    fn rejects_non_finite_and_out_of_range_split_fractions() {
        let (window, cwd) = sample();
        let mut snapshot = WorkspaceSnapshot::capture(&window, &cwd);
        let root = &mut snapshot.windows[0].projects[0].tabs[0].root;
        *root = PaneNodeSnapshot::Split {
            id: Uuid::new_v4().to_string(),
            axis: SplitAxisSnapshot::Horizontal,
            fraction: f32::NAN,
            first: Box::new(PaneNodeSnapshot::Pane {
                id: Uuid::new_v4().to_string(),
                working_directory: PathBuf::from("/tmp"),
                cwd_provenance: CwdProvenance::Launch,
                shell: None,
            }),
            second: Box::new(PaneNodeSnapshot::Pane {
                id: Uuid::new_v4().to_string(),
                working_directory: PathBuf::from("/tmp"),
                cwd_provenance: CwdProvenance::Launch,
                shell: None,
            }),
        };
        assert!(matches!(
            snapshot.validate(SnapshotLimits::default()),
            Err(SnapshotError::Invalid(_))
        ));
    }

    #[test]
    fn rejects_duplicate_ids_and_deep_trees() {
        let (window, cwd) = sample();
        let mut snapshot = WorkspaceSnapshot::capture(&window, &cwd);
        let duplicate = snapshot.windows[0].projects[0].clone();
        snapshot.windows[0].projects.push(duplicate);
        assert!(matches!(
            snapshot.validate(SnapshotLimits::default()),
            Err(SnapshotError::Invalid(_))
        ));

        let snapshot = WorkspaceSnapshot::capture(&window, &cwd);
        let limits = SnapshotLimits {
            max_tree_depth: 0,
            ..SnapshotLimits::default()
        };
        assert!(matches!(
            snapshot.validate(limits),
            Err(SnapshotError::Invalid(_))
        ));
    }

    #[test]
    fn enforces_project_tab_node_name_and_path_limits() {
        let (window, cwd) = sample();
        let base = WorkspaceSnapshot::capture(&window, &cwd);
        for limits in [
            SnapshotLimits {
                max_projects: 0,
                ..SnapshotLimits::default()
            },
            SnapshotLimits {
                max_tabs_per_project: 0,
                ..SnapshotLimits::default()
            },
            SnapshotLimits {
                max_nodes_per_tree: 0,
                ..SnapshotLimits::default()
            },
            SnapshotLimits {
                max_name_bytes: 2,
                ..SnapshotLimits::default()
            },
            SnapshotLimits {
                max_path_bytes: 2,
                ..SnapshotLimits::default()
            },
        ] {
            assert!(matches!(
                base.validate(limits),
                Err(SnapshotError::Invalid(_))
            ));
        }
    }

    #[test]
    fn rejects_invalid_focus_selection_and_relative_cwd() {
        let (window, cwd) = sample();
        let mut snapshot = WorkspaceSnapshot::capture(&window, &cwd);
        snapshot.windows[0].projects[0].tabs[0].focused_pane = Uuid::new_v4().to_string();
        assert!(matches!(
            snapshot.validate(SnapshotLimits::default()),
            Err(SnapshotError::Invalid(_))
        ));

        let mut snapshot = WorkspaceSnapshot::capture(&window, &cwd);
        snapshot.windows[0].selected_project = Some(Uuid::new_v4().to_string());
        assert!(matches!(
            snapshot.validate(SnapshotLimits::default()),
            Err(SnapshotError::Invalid(_))
        ));

        let mut snapshot = WorkspaceSnapshot::capture(&window, &cwd);
        if let PaneNodeSnapshot::Pane {
            working_directory, ..
        } = &mut snapshot.windows[0].projects[0].tabs[0].root
        {
            *working_directory = PathBuf::from("relative");
        }
        assert!(matches!(
            snapshot.validate(SnapshotLimits::default()),
            Err(SnapshotError::Invalid(_))
        ));
    }

    #[test]
    fn round_trips_multi_project_nested_layout_and_all_selections() {
        let first = Pane::empty();
        let first_id = first.id;
        let second = Pane::empty();
        let second_id = second.id;
        let mut tree = PaneTree::new(first);
        tree.split(first_id, omaterm_core::SplitDirection::Right, second)
            .unwrap();
        let split_id = match tree.root().unwrap() {
            PaneNode::Split { id, .. } => *id,
            PaneNode::Pane(_) => unreachable!(),
        };
        tree.resize(split_id, 0.63).unwrap();
        let mut tab = Tab::new(tree, second_id).unwrap();
        tab.custom_name = Some("nested".into());
        let first_tab_id = tab.id;
        let mut project = Project::new(Some("alpha".into()), Some(PathBuf::from("/tmp")));
        project.add_tab(tab).unwrap();
        let extra_pane = Pane::empty();
        let extra_tab = Tab::new(PaneTree::new(extra_pane.clone()), extra_pane.id).unwrap();
        let extra_tab_id = extra_tab.id;
        project.add_tab(extra_tab).unwrap();
        project.select_tab(first_tab_id).unwrap();
        let first_project_id = project.id;

        let mut other_project = Project::new(Some("beta".into()), None);
        let other_pane = Pane::empty();
        other_project
            .add_tab(Tab::new(PaneTree::new(other_pane.clone()), other_pane.id).unwrap())
            .unwrap();
        let mut window = WorkspaceWindow::new();
        window.add_project(project).unwrap();
        window.add_project(other_project).unwrap();
        window.select_project(first_project_id).unwrap();

        let cwd = [first_id, second_id, extra_pane.id, other_pane.id]
            .into_iter()
            .map(|id| {
                (
                    id,
                    PersistedCwd {
                        path: PathBuf::from("/tmp"),
                        provenance: CwdProvenance::Launch,
                    },
                )
            })
            .collect();
        let snapshot = WorkspaceSnapshot::capture(&window, &cwd);
        let restored = snapshot.validate(SnapshotLimits::default()).unwrap();
        assert_eq!(restored.window, window);
        assert_eq!(restored.pane_cwds.len(), 4);
        assert_eq!(restored.window.selected_project, Some(first_project_id));
        assert_eq!(
            restored
                .window
                .project(first_project_id)
                .unwrap()
                .selected_tab,
            Some(first_tab_id)
        );
        assert_eq!(
            extra_tab_id,
            window.project(first_project_id).unwrap().tabs[1].id
        );
    }

    #[test]
    fn active_repo_round_trips_and_rejects_traversal_names() {
        let (window, cwd) = sample();
        let project_id = window.projects[0].id;
        let mut window = window;
        window.project_mut(project_id).unwrap().active_repo = Some("api".into());
        let snapshot = WorkspaceSnapshot::capture(&window, &cwd);
        assert_eq!(
            snapshot.windows[0].projects[0].active_repo.as_deref(),
            Some("api")
        );
        let restored = snapshot.validate(SnapshotLimits::default()).unwrap();
        assert_eq!(
            restored
                .window
                .project(project_id)
                .unwrap()
                .active_repo
                .as_deref(),
            Some("api")
        );

        for bad in ["", ".", "..", "api/web", "api\\web"] {
            let mut tainted = snapshot.clone();
            tainted.windows[0].projects[0].active_repo = Some(bad.into());
            assert!(
                matches!(
                    tainted.validate(SnapshotLimits::default()),
                    Err(SnapshotError::Invalid(_))
                ),
                "active repo {bad:?} must be rejected"
            );
        }
    }

    #[test]
    fn expanded_dirs_round_trip_and_cap_at_capture() {
        let (window, cwd) = sample();
        let project_id = window.projects[0].id;
        let expanded = HashMap::from([(
            project_id,
            vec![PathBuf::from("src"), PathBuf::from("docs")],
        )]);
        let snapshot = WorkspaceSnapshot::capture_with_expanded(&window, &cwd, &expanded);
        assert_eq!(
            snapshot.windows[0].projects[0].expanded_dirs,
            vec![PathBuf::from("src"), PathBuf::from("docs")]
        );
        let restored = snapshot.validate(SnapshotLimits::default()).unwrap();
        assert_eq!(
            restored.expanded_dirs,
            vec![(
                project_id,
                vec![PathBuf::from("src"), PathBuf::from("docs")]
            )]
        );
    }

    #[test]
    fn document_registry_round_trips_lossless_path_bytes_and_active_selection() {
        let (window, cwd) = sample();
        let project_id = window.projects[0].id;
        let document = DocumentDescriptor {
            id: DocumentId::new(),
            path_bytes: b"src/non-utf8-\xff.rs".to_vec(),
            root_device: 7,
            root_inode: 11,
        };
        let registries = HashMap::from([(
            project_id,
            DocumentRegistry {
                active_document: Some(document.id),
                documents: vec![document.clone()],
            },
        )]);
        let snapshot = WorkspaceSnapshot::capture_with_expanded_and_documents(
            &window,
            &cwd,
            &HashMap::new(),
            &registries,
            &HashMap::new(),
        );
        let bytes = serde_json::to_vec(&snapshot).unwrap();
        let value: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
        let project = &value["windows"][0]["projects"][0];
        assert!(project.get("dirty_text").is_none());
        assert!(project.get("clipboard").is_none());
        assert!(project.get("undo_history").is_none());
        assert!(project.get("operation_id").is_none());
        let document_value = project["documents"][0].as_object().unwrap();
        assert_eq!(document_value.len(), 4);
        for key in ["id", "path_bytes", "root_device", "root_inode"] {
            assert!(document_value.contains_key(key));
        }

        let restored = snapshot.validate(SnapshotLimits::default()).unwrap();
        assert_eq!(
            restored.document_registries,
            vec![(
                project_id,
                DocumentRegistry {
                    documents: vec![document],
                    active_document: registries[&project_id].active_document,
                },
            )]
        );
    }

    #[test]
    fn document_capture_caps_entries_and_drops_truncated_active_selection() {
        let (window, cwd) = sample();
        let project_id = window.projects[0].id;
        let documents = (0..=SnapshotLimits::default().max_documents_per_project)
            .map(|index| DocumentDescriptor {
                id: DocumentId::new(),
                path_bytes: format!("src/{index}.rs").into_bytes(),
                root_device: 7,
                root_inode: 11,
            })
            .collect::<Vec<_>>();
        let active_document = documents.last().map(|document| document.id);
        let snapshot = WorkspaceSnapshot::capture_with_expanded_and_documents(
            &window,
            &cwd,
            &HashMap::new(),
            &HashMap::from([(
                project_id,
                DocumentRegistry {
                    documents,
                    active_document,
                },
            )]),
            &HashMap::new(),
        );
        let project = &snapshot.windows[0].projects[0];
        assert_eq!(
            project.documents.len(),
            SnapshotLimits::default().max_documents_per_project
        );
        assert_eq!(project.active_document, None);
        snapshot.validate(SnapshotLimits::default()).unwrap();
    }

    #[test]
    fn v1_snapshot_without_expanded_dirs_migrates_cleanly() {
        let (window, cwd) = sample();
        let mut snapshot = WorkspaceSnapshot::capture(&window, &cwd);
        snapshot.schema_version = WorkspaceSnapshot::V1_SCHEMA_VERSION;
        let mut value = serde_json::to_value(&snapshot).unwrap();
        // Simulate a real v1 file: no `expanded_dirs` key at all.
        if let Some(projects) = value
            .get_mut("windows")
            .and_then(|windows| windows.as_array_mut())
            .and_then(|windows| windows.first_mut())
            .and_then(|window| window.get_mut("projects"))
            .and_then(|projects| projects.as_array_mut())
        {
            for project in projects.iter_mut() {
                project.as_object_mut().unwrap().remove("expanded_dirs");
            }
        }
        value["schema_version"] = serde_json::json!(1);
        let bytes = serde_json::to_vec(&value).unwrap();
        let decoded = crate::migration::decode(&bytes, SnapshotLimits::default()).unwrap();
        assert_eq!(decoded.schema_version, WorkspaceSnapshot::SCHEMA_VERSION);
        let restored = decoded.validate(SnapshotLimits::default()).unwrap();
        assert_eq!(restored.window, window);
        assert!(
            restored
                .expanded_dirs
                .iter()
                .all(|(_, dirs)| dirs.is_empty())
        );
        assert!(restored.document_registries.iter().all(|(_, registry)| {
            registry.documents.is_empty() && registry.active_document.is_none()
        }));
    }

    #[test]
    fn rejects_over_bound_and_absolute_expanded_dirs() {
        let (window, cwd) = sample();
        let mut snapshot = WorkspaceSnapshot::capture(&window, &cwd);
        snapshot.windows[0].projects[0].expanded_dirs =
            vec![PathBuf::from("ok"); SnapshotLimits::default().max_expanded_dirs + 1];
        assert!(matches!(
            snapshot.validate(SnapshotLimits::default()),
            Err(SnapshotError::Invalid(_))
        ));

        let mut snapshot = WorkspaceSnapshot::capture(&window, &cwd);
        snapshot.windows[0].projects[0].expanded_dirs = vec![PathBuf::from("/absolute")];
        assert!(matches!(
            snapshot.validate(SnapshotLimits::default()),
            Err(SnapshotError::Invalid(_))
        ));

        let mut snapshot = WorkspaceSnapshot::capture(&window, &cwd);
        snapshot.windows[0].projects[0].expanded_dirs = vec![PathBuf::from("../escape")];
        assert!(matches!(
            snapshot.validate(SnapshotLimits::default()),
            Err(SnapshotError::Invalid(_))
        ));
    }

    #[test]
    fn rejects_invalid_document_paths_root_identities_duplicates_and_active_selection() {
        for path_bytes in [
            vec![],
            b"/absolute.rs".to_vec(),
            b"src/../escape.rs".to_vec(),
            b"src/child/../../escape.rs".to_vec(),
            b"src/..".to_vec(),
            b"../escape.rs".to_vec(),
            b"src/trailing/".to_vec(),
            b"src//empty.rs".to_vec(),
            b"src/./current.rs".to_vec(),
            b"src/nul\0.rs".to_vec(),
        ] {
            let (window, cwd) = sample();
            let mut snapshot = WorkspaceSnapshot::capture(&window, &cwd);
            snapshot.windows[0].projects[0].documents = vec![document_snapshot(path_bytes)];
            assert!(matches!(
                snapshot.validate(SnapshotLimits::default()),
                Err(SnapshotError::Invalid(_))
            ));
        }

        for root_identity in [(0, 0), (7, 0)] {
            let (window, cwd) = sample();
            let mut snapshot = WorkspaceSnapshot::capture(&window, &cwd);
            let mut document = document_snapshot(b"src/main.rs".to_vec());
            document.root_device = root_identity.0;
            document.root_inode = root_identity.1;
            snapshot.windows[0].projects[0].documents = vec![document];
            assert!(matches!(
                snapshot.validate(SnapshotLimits::default()),
                Err(SnapshotError::Invalid(_))
            ));
        }

        let (window, cwd) = sample();
        let mut snapshot = WorkspaceSnapshot::capture(&window, &cwd);
        let first = document_snapshot(b"src/main.rs".to_vec());
        let mut duplicate = document_snapshot(b"src/main.rs".to_vec());
        duplicate.root_device = first.root_device;
        duplicate.root_inode = first.root_inode;
        snapshot.windows[0].projects[0].documents = vec![first, duplicate];
        assert!(matches!(
            snapshot.validate(SnapshotLimits::default()),
            Err(SnapshotError::Invalid(_))
        ));

        let (window, cwd) = sample();
        let mut snapshot = WorkspaceSnapshot::capture(&window, &cwd);
        let first = document_snapshot(b"src/first.rs".to_vec());
        let mut duplicate_id = document_snapshot(b"src/second.rs".to_vec());
        duplicate_id.id = first.id.clone();
        snapshot.windows[0].projects[0].documents = vec![first, duplicate_id];
        assert!(matches!(
            snapshot.validate(SnapshotLimits::default()),
            Err(SnapshotError::Invalid(_))
        ));

        let (window, cwd) = sample();
        let mut snapshot = WorkspaceSnapshot::capture(&window, &cwd);
        snapshot.windows[0].projects[0].documents =
            vec![document_snapshot(b"src/main.rs".to_vec())];
        snapshot.windows[0].projects[0].active_document = Some(Uuid::new_v4().to_string());
        assert!(matches!(
            snapshot.validate(SnapshotLimits::default()),
            Err(SnapshotError::Invalid(_))
        ));
    }

    #[test]
    fn enforces_document_count_path_and_registry_byte_caps() {
        let (window, cwd) = sample();
        let mut snapshot = WorkspaceSnapshot::capture(&window, &cwd);
        let document = document_snapshot(b"src/main.rs".to_vec());
        snapshot.windows[0].projects[0].active_document = Some(document.id.clone());
        snapshot.windows[0].projects[0].documents = vec![document];
        let registry_bytes = document_registry_bytes(&snapshot.windows[0].projects[0]).unwrap();
        snapshot
            .validate(SnapshotLimits {
                max_document_registry_bytes: registry_bytes,
                ..SnapshotLimits::default()
            })
            .unwrap();
        for limits in [
            SnapshotLimits {
                max_documents_per_project: 0,
                ..SnapshotLimits::default()
            },
            SnapshotLimits {
                max_path_bytes: 3,
                ..SnapshotLimits::default()
            },
            SnapshotLimits {
                max_document_registry_bytes: registry_bytes - 1,
                ..SnapshotLimits::default()
            },
        ] {
            assert!(matches!(
                snapshot.validate(limits),
                Err(SnapshotError::Invalid(_))
            ));
        }
    }

    #[test]
    fn direct_validation_requires_the_current_schema() {
        let (window, cwd) = sample();
        let mut snapshot = WorkspaceSnapshot::capture(&window, &cwd);
        snapshot.schema_version = WorkspaceSnapshot::V2_SCHEMA_VERSION;
        assert!(matches!(
            snapshot.validate(SnapshotLimits::default()),
            Err(SnapshotError::UnsupportedVersion(
                WorkspaceSnapshot::V2_SCHEMA_VERSION
            ))
        ));
    }

    #[test]
    fn accepts_empty_workspace_and_empty_project() {
        let empty = WorkspaceWindow::new();
        let snapshot = WorkspaceSnapshot::capture(&empty, &HashMap::new());
        assert_eq!(
            snapshot.validate(SnapshotLimits::default()).unwrap().window,
            empty
        );

        let mut window = WorkspaceWindow::new();
        window
            .add_project(Project::new(Some("empty".into()), None))
            .unwrap();
        let snapshot = WorkspaceSnapshot::capture(&window, &HashMap::new());
        assert_eq!(
            snapshot.validate(SnapshotLimits::default()).unwrap().window,
            window
        );
    }
}
