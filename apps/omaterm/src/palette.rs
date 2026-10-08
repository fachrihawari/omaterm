//! GPUI-independent command-palette candidate ranking and command targets.

use std::collections::HashSet;
use std::path::{Path, PathBuf};

use omaterm_core::{OmaCommand, PaneId, ProjectId, SessionId};

pub const MAX_PALETTE_RESULTS: usize = omaterm_core::MAX_PALETTE_RESULTS;
pub const MAX_PALETTE_QUERY_BYTES: usize = 256;
pub const MAX_PALETTE_SOURCE_CANDIDATES: usize = 10_000;

pub fn path_identity(path: &Path) -> String {
    use std::fmt::Write;
    let mut encoded = String::with_capacity(path.as_os_str().as_encoded_bytes().len() * 2);
    for byte in path.as_os_str().as_encoded_bytes() {
        let _ = write!(encoded, "{byte:02x}");
    }
    encoded
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
pub enum PaletteKind {
    Command,
    Project,
    Tab,
    Pane,
    Session,
    File,
    GitPath,
}

#[derive(Debug, Clone, PartialEq)]
pub enum PaletteTarget {
    Semantic(OmaCommand),
    /// View-local UI action (no core command): the confirm handler matches
    /// the candidate key and performs the overlay transition itself.
    ViewAction,
    PaneFocus {
        pane: PaneId,
        expected_session: Option<SessionId>,
    },
    GitDiff {
        project: ProjectId,
        path: PathBuf,
        staged: bool,
    },
    /// Windows shell choice. The id is `powershell`, `cmd`, or `git-bash`.
    Shell(String),
}

#[derive(Debug, Clone, PartialEq)]
pub struct PaletteCandidate {
    /// Stable, non-sensitive identity used for result retention and MRU.
    pub key: String,
    pub label: String,
    pub detail: String,
    pub aliases: Vec<String>,
    pub kind: PaletteKind,
    pub target: PaletteTarget,
    /// Canonical source root for file results, preventing MRU identities from
    /// crossing a project-root replacement.
    pub file_root: Option<PathBuf>,
    /// Successful-use rank. Lower is more recent; absent entries sort last.
    pub mru_rank: Option<usize>,
}

impl PaletteCandidate {
    pub fn file_target(&self) -> Option<(ProjectId, PathBuf)> {
        let PaletteTarget::Semantic(OmaCommand::File(omaterm_core::FileCommand::Open {
            project,
            path,
        })) = &self.target
        else {
            return None;
        };
        Some((*project, path.clone()))
    }
}

pub fn semantic_candidate(
    key: impl Into<String>,
    label: impl Into<String>,
    detail: impl Into<String>,
    aliases: impl IntoIterator<Item = impl Into<String>>,
    kind: PaletteKind,
    command: OmaCommand,
) -> PaletteCandidate {
    PaletteCandidate {
        key: key.into(),
        label: label.into(),
        detail: detail.into(),
        aliases: aliases.into_iter().map(Into::into).collect(),
        kind,
        target: PaletteTarget::Semantic(command),
        file_root: None,
        mru_rank: None,
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
struct MatchClass(u8);

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
struct Score {
    class: MatchClass,
    fuzzy: i64,
}

fn score(candidate: &PaletteCandidate, query: &str) -> Option<Score> {
    if query.is_empty() {
        return Some(Score {
            class: MatchClass(0),
            fuzzy: 0,
        });
    }
    let query_lower = query.to_lowercase();
    let mut best = None;
    for text in std::iter::once(candidate.label.as_str())
        .chain(std::iter::once(candidate.detail.as_str()))
        .chain(candidate.aliases.iter().map(String::as_str))
    {
        let lower = text.to_lowercase();
        let class = if lower == query_lower {
            0
        } else if lower.starts_with(&query_lower) {
            1
        } else {
            2
        };
        let fuzzy = omaterm_context::fuzzy_match_indices(text, query).map(|(score, _)| score);
        let fuzzy = match fuzzy {
            Some(score) => score,
            None if class < 2 => 0,
            None => continue,
        };
        let next = Score {
            class: MatchClass(class),
            fuzzy,
        };
        if best.is_none_or(|current: Score| {
            (next.class, std::cmp::Reverse(next.fuzzy))
                < (current.class, std::cmp::Reverse(current.fuzzy))
        }) {
            best = Some(next);
        }
    }
    best
}

/// Rank matching entries deterministically, applying the single merged result
/// bound after filtering and sorting.
#[cfg(test)]
pub fn rank(
    candidates: impl IntoIterator<Item = PaletteCandidate>,
    query: &str,
    limit: usize,
) -> Vec<PaletteCandidate> {
    rank_with_truncation(candidates, query, limit).0
}

pub fn rank_with_truncation(
    candidates: impl IntoIterator<Item = PaletteCandidate>,
    query: &str,
    limit: usize,
) -> (Vec<PaletteCandidate>, bool) {
    let mut keys = HashSet::new();
    let matched: Vec<_> = candidates
        .into_iter()
        .filter(|candidate| keys.insert(candidate.key.clone()))
        .filter_map(|candidate| score(&candidate, query).map(|score| (candidate, score)))
        .collect();
    let truncated = matched.len() > limit.min(MAX_PALETTE_RESULTS);
    let ranks = matched
        .iter()
        .map(|(candidate, score)| omaterm_core::PaletteRank {
            class: score.class.0,
            fuzzy_score: score.fuzzy,
            mru_rank: candidate.mru_rank,
            kind: candidate.kind as u8,
            label: &candidate.label,
            key: &candidate.key,
        })
        .collect::<Vec<_>>();
    let results = omaterm_core::rank_palette_indices(&ranks, limit)
        .into_iter()
        .map(|index| matched[index].0.clone())
        .collect();
    (results, truncated)
}

pub fn file_candidate(
    project: ProjectId,
    root: &Path,
    path: PathBuf,
    display: String,
) -> PaletteCandidate {
    use omaterm_core::FileCommand;
    PaletteCandidate {
        key: format!(
            "file:{}:{}:{}",
            project.0,
            path_identity(root),
            path_identity(&path)
        ),
        label: display,
        detail: path.to_string_lossy().into_owned(),
        aliases: Vec::new(),
        kind: PaletteKind::File,
        target: PaletteTarget::Semantic(OmaCommand::File(FileCommand::Open { project, path })),
        file_root: Some(root.to_path_buf()),
        mru_rank: None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use omaterm_core::{PaneCommand, PaneId};
    use std::path::Path;

    fn candidate(key: &str, label: &str, kind: PaletteKind) -> PaletteCandidate {
        PaletteCandidate {
            key: key.into(),
            label: label.into(),
            detail: String::new(),
            aliases: Vec::new(),
            kind,
            target: PaletteTarget::Semantic(OmaCommand::Pane(PaneCommand::Focus {
                pane: PaneId::new(),
            })),
            file_root: None,
            mru_rank: None,
        }
    }

    #[test]
    fn exact_then_prefix_then_fuzzy_with_stable_ties() {
        let results = rank(
            [
                candidate("z", "split right", PaletteKind::Command),
                candidate("a", "split right", PaletteKind::Command),
                candidate("e", "split", PaletteKind::Command),
                candidate("f", "right split", PaletteKind::Command),
            ],
            "split",
            100,
        );
        assert_eq!(
            results
                .iter()
                .map(|item| item.key.as_str())
                .collect::<Vec<_>>(),
            ["e", "a", "z", "f"]
        );
    }

    #[test]
    fn successful_use_mru_breaks_equal_match_ties_only() {
        let mut recent = candidate("recent", "project alpha", PaletteKind::Project);
        recent.mru_rank = Some(0);
        let ordinary = candidate("ordinary", "project alpha", PaletteKind::Project);
        let results = rank([ordinary, recent], "project", 10);
        assert_eq!(results[0].key, "recent");
        assert_eq!(rank(results.clone(), "nomatch", 10), Vec::new());
    }

    #[test]
    fn result_limit_is_enforced_after_global_ranking() {
        let entries = (0..150).map(|index| {
            candidate(
                &format!("{index:03}"),
                &format!("open project {index}"),
                PaletteKind::Command,
            )
        });
        let results = rank(entries, "open", usize::MAX);
        assert_eq!(results.len(), MAX_PALETTE_RESULTS);
        assert_eq!(results.first().unwrap().key, "000");
    }

    #[test]
    fn merged_limit_reports_truncation_and_deduplicates_stable_keys() {
        let mut entries = (0..120)
            .map(|index| {
                candidate(
                    &format!("{index:03}"),
                    &format!("searchable {index}"),
                    PaletteKind::Command,
                )
            })
            .collect::<Vec<_>>();
        entries.push(entries[0].clone());
        let (results, truncated) = rank_with_truncation(entries, "searchable", 100);
        assert_eq!(results.len(), 100);
        assert!(truncated);
        assert_eq!(results.iter().filter(|entry| entry.key == "000").count(), 1);
    }

    #[test]
    fn file_identity_includes_project_root_and_original_path_bytes() {
        let project = ProjectId::new();
        let path = PathBuf::from("src/main.rs");
        let left = file_candidate(
            project,
            Path::new("/repo/a"),
            path.clone(),
            "main.rs".into(),
        );
        let right = file_candidate(project, Path::new("/repo/b"), path, "main.rs".into());
        assert_ne!(left.key, right.key);
        assert_eq!(left.file_root.as_deref(), Some(Path::new("/repo/a")));
        assert!(left.file_target().is_some());
    }
}
