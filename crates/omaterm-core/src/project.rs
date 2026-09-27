use std::path::PathBuf;

use crate::{CoreError, ProjectId, Result, Tab, TabId};

/// A project groups tabs and carries an optional launch directory.
#[derive(Debug, Clone, PartialEq)]
pub struct Project {
    pub id: ProjectId,
    pub custom_name: Option<String>,
    pub pinned_directory: Option<PathBuf>,
    pub tabs: Vec<Tab>,
    pub selected_tab: Option<TabId>,
}

impl Project {
    pub fn new(custom_name: Option<String>, pinned_directory: Option<PathBuf>) -> Self {
        Self {
            id: ProjectId::new(),
            custom_name,
            pinned_directory,
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
        }
    }
}
