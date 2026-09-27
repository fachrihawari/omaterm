use crate::{CoreError, PaneId, PaneTree, Result, TabId};

/// One independently focused pane layout.
#[derive(Debug, Clone, PartialEq)]
pub struct Tab {
    pub id: TabId,
    pub custom_name: Option<String>,
    pub tree: PaneTree,
    pub focused_pane: PaneId,
}

impl Tab {
    pub fn new(tree: PaneTree, focused_pane: PaneId) -> Result<Self> {
        let tab = Self {
            id: TabId::new(),
            custom_name: None,
            tree,
            focused_pane,
        };
        tab.validate()?;
        Ok(tab)
    }

    pub fn validate(&self) -> Result<()> {
        if self.tree.find(self.focused_pane).is_none() {
            return Err(CoreError::InvalidFocusedPane);
        }
        Ok(())
    }

    pub fn display_name(&self, ordinal: usize) -> String {
        self.custom_name
            .as_deref()
            .filter(|name| !name.trim().is_empty())
            .map(str::to_string)
            .unwrap_or_else(|| format!("Tab {ordinal}"))
    }
}
