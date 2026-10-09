use std::path::PathBuf;

use crate::{CoreError, ProjectId, RepoEntry, Result, Tab, TabId};

/// A project groups tabs and carries an optional launch directory.
///
/// `active_repo` (M20) names the selected child repository when the
/// project root contains several repos (depth-1 scan). It is a plain
/// directory name, resolved against the live project root at use time
/// so project directory moves don't break it. `None` means "no explicit
/// choice" — callers fall back to the first-sorted candidate.
#[derive(Debug, Clone, PartialEq)]
pub struct Project {
    pub id: ProjectId,
    pub custom_name: Option<String>,
    pub pinned_directory: Option<PathBuf>,
    pub active_repo: Option<String>,
    pub tabs: Vec<Tab>,
    pub selected_tab: Option<TabId>,
}

impl Project {
    pub fn new(custom_name: Option<String>, pinned_directory: Option<PathBuf>) -> Self {
        Self {
            id: ProjectId::new(),
            custom_name,
            pinned_directory,
            active_repo: None,
            tabs: Vec::new(),
            selected_tab: None,
        }
    }

    pub fn display_name(&self, ordinal: usize) -> String {
        if let Some(name) = self
            .custom_name
            .as_deref()
            .filter(|name| !name.trim().is_empty())
        {
            return name.to_string();
        }
        self.pinned_directory
            .as_ref()
            .and_then(|path| path.file_name())
            .map(|name| name.to_string_lossy().into_owned())
            .filter(|name| !name.is_empty())
            .unwrap_or_else(|| format!("Project {ordinal}"))
    }

    pub fn tab(&self, id: TabId) -> Option<&Tab> {
        self.tabs.iter().find(|tab| tab.id == id)
    }

    pub fn tab_mut(&mut self, id: TabId) -> Option<&mut Tab> {
        self.tabs.iter_mut().find(|tab| tab.id == id)
    }

    pub fn selected_tab(&self) -> Option<&Tab> {
        self.selected_tab.and_then(|id| self.tab(id))
    }

    pub fn selected_tab_mut(&mut self) -> Option<&mut Tab> {
        let id = self.selected_tab?;
        self.tab_mut(id)
    }

    pub fn add_tab(&mut self, tab: Tab) -> Result<()> {
        if self.tabs.iter().any(|existing| existing.id == tab.id) {
            return Err(CoreError::DuplicateTabId(tab.id));
        }
        tab.validate()?;
        let id = tab.id;
        self.tabs.push(tab);
        self.selected_tab = Some(id);
        Ok(())
    }

    pub fn remove_tab(&mut self, id: TabId) -> Result<Tab> {
        let index = self
            .tabs
            .iter()
            .position(|tab| tab.id == id)
            .ok_or(CoreError::TabNotFound(id))?;
        let removed = self.tabs.remove(index);
        if self.selected_tab == Some(id) {
            self.selected_tab = self
                .tabs
                .get(index)
                .or_else(|| index.checked_sub(1).and_then(|i| self.tabs.get(i)))
                .map(|tab| tab.id);
        }
        Ok(removed)
    }

    pub fn select_tab(&mut self, id: TabId) -> Result<()> {
        if self.tab(id).is_none() {
            return Err(CoreError::TabNotFound(id));
        }
        self.selected_tab = Some(id);
        Ok(())
    }

    /// Select the active child repository by directory name (M20). The
    /// name must match one of the live scan `candidates` exactly;
    /// anything else (including `..`, separators, or empty names) is
    /// rejected so callers can never escape the project root.
    pub fn set_active_repo(&mut self, name: &str, candidates: &[RepoEntry]) -> Result<()> {
        if !is_valid_repo_name(name) || !candidates.iter().any(|entry| entry.name == name) {
            return Err(CoreError::UnknownRepo(name.to_string()));
        }
        self.active_repo = Some(name.to_string());
        Ok(())
    }

    /// Clear an explicit selection, returning to first-sorted default.
    pub fn clear_active_repo(&mut self) {
        self.active_repo = None;
    }

    /// Resolve the effective repo: the saved selection when it is still
    /// present in `candidates`, else the first-sorted default (M20
    /// agreed rule: last-saved, else first-sorted).
    pub fn resolve_active_repo<'a>(&self, candidates: &'a [RepoEntry]) -> Option<&'a RepoEntry> {
        match self.active_repo.as_deref() {
            Some(saved) => candidates
                .iter()
                .find(|entry| entry.name == saved)
                .or_else(|| candidates.first()),
            None => candidates.first(),
        }
    }

    pub fn validate(&self) -> Result<()> {
        if self
            .tabs
            .iter()
            .map(|tab| tab.id)
            .collect::<std::collections::HashSet<_>>()
            .len()
            != self.tabs.len()
        {
            return Err(CoreError::DuplicateTabId(self.tabs[0].id));
        }
        for tab in &self.tabs {
            tab.validate()?;
        }
        match (self.tabs.is_empty(), self.selected_tab) {
            (true, None) => Ok(()),
            (false, Some(id)) if self.tab(id).is_some() => Ok(()),
            (false, Some(id)) => Err(CoreError::TabNotFound(id)),
            _ => Err(CoreError::NoSelectedTab),
        }?;
        if let Some(name) = self.active_repo.as_deref()
            && !is_valid_repo_name(name)
        {
            return Err(CoreError::UnknownRepo(name.to_string()));
        }
        Ok(())
    }
}

/// A valid repo selection is a single child directory name: non-empty,
/// no separators, no parent/current markers. Membership against the
/// live scan is checked separately by `set_active_repo`.
fn is_valid_repo_name(name: &str) -> bool {
    !name.is_empty() && name != "." && name != ".." && !name.contains('/') && !name.contains('\\')
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::PathBuf;

    fn candidates() -> Vec<RepoEntry> {
        ["api", "gateway", "web"]
            .into_iter()
            .map(|name| RepoEntry::new(name, PathBuf::from(format!("/root/{name}"))))
            .collect()
    }

    #[test]
    fn unset_selection_resolves_to_first_sorted_candidate() {
        let project = Project::new(None, Some(PathBuf::from("/root")));
        let repos = candidates();
        assert_eq!(
            project.resolve_active_repo(&repos).map(|entry| &entry.name),
            Some(&"api".to_string())
        );
        assert!(project.resolve_active_repo(&[]).is_none());
    }

    #[test]
    fn saved_selection_wins_and_stale_names_fall_back() {
        let mut project = Project::new(None, Some(PathBuf::from("/root")));
        let repos = candidates();
        project.set_active_repo("web", &repos).unwrap();
        assert_eq!(
            project.resolve_active_repo(&repos).map(|entry| &entry.name),
            Some(&"web".to_string())
        );
        // Saved name gone from a fresh scan → first-sorted fallback.
        let reduced = repos[..2].to_vec();
        assert_eq!(
            project
                .resolve_active_repo(&reduced)
                .map(|entry| &entry.name),
            Some(&"api".to_string())
        );
    }

    #[test]
    fn set_active_repo_rejects_unknown_and_traversal_names() {
        let mut project = Project::new(None, Some(PathBuf::from("/root")));
        let repos = candidates();
        for bad in ["missing", "", ".", "..", "api/web", "api\\web", "../api"] {
            assert_eq!(
                project.set_active_repo(bad, &repos),
                Err(CoreError::UnknownRepo(bad.to_string())),
                "name {bad:?} must be rejected"
            );
        }
        assert_eq!(project.active_repo, None);
    }

    #[test]
    fn validate_rejects_malformed_saved_names() {
        let mut project = Project::new(None, Some(PathBuf::from("/root")));
        project.active_repo = Some("../escape".into());
        assert_eq!(
            project.validate(),
            Err(CoreError::UnknownRepo("../escape".into()))
        );
    }
}
