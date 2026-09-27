use crate::{CoreError, Project, ProjectId, Result, WindowId};

#[derive(Debug, Clone, PartialEq, Default)]
pub struct Workspace {
    pub windows: Vec<WorkspaceWindow>,
}

#[derive(Debug, Clone, PartialEq)]
pub struct WorkspaceWindow {
    pub id: WindowId,
    pub projects: Vec<Project>,
    pub selected_project: Option<ProjectId>,
}

impl Default for WorkspaceWindow {
    fn default() -> Self {
        Self::new()
    }
}

impl WorkspaceWindow {
    pub fn new() -> Self {
        Self {
            id: WindowId::new(),
            projects: Vec::new(),
            selected_project: None,
        }
    }

    pub fn project(&self, id: ProjectId) -> Option<&Project> {
        self.projects.iter().find(|p| p.id == id)
    }
    pub fn project_mut(&mut self, id: ProjectId) -> Option<&mut Project> {
        self.projects.iter_mut().find(|p| p.id == id)
    }
    pub fn selected_project(&self) -> Option<&Project> {
        self.selected_project.and_then(|id| self.project(id))
    }
    pub fn selected_project_mut(&mut self) -> Option<&mut Project> {
        let id = self.selected_project?;
        self.project_mut(id)
    }

    pub fn add_project(&mut self, project: Project) -> Result<()> {
        if self
            .projects
            .iter()
            .any(|existing| existing.id == project.id)
        {
            return Err(CoreError::DuplicateProjectId(project.id));
        }
        project.validate()?;
        let id = project.id;
        self.projects.push(project);
        self.selected_project = Some(id);
        Ok(())
    }

    pub fn remove_project(&mut self, id: ProjectId) -> Result<Project> {
        let index = self
            .projects
            .iter()
            .position(|project| project.id == id)
            .ok_or(CoreError::ProjectNotFound(id))?;
        let removed = self.projects.remove(index);
        if self.selected_project == Some(id) {
            self.selected_project = self
                .projects
                .get(index)
                .or_else(|| index.checked_sub(1).and_then(|i| self.projects.get(i)))
                .map(|project| project.id);
        }
        Ok(removed)
    }

    pub fn select_project(&mut self, id: ProjectId) -> Result<()> {
        if self.project(id).is_none() {
            return Err(CoreError::ProjectNotFound(id));
        }
        self.selected_project = Some(id);
        Ok(())
    }

    pub fn validate(&self) -> Result<()> {
        let mut ids = std::collections::HashSet::new();
        for project in &self.projects {
            if !ids.insert(project.id) {
                return Err(CoreError::DuplicateProjectId(project.id));
            }
            project.validate()?;
        }
        match (self.projects.is_empty(), self.selected_project) {
            (true, None) => Ok(()),
            (false, Some(id)) if self.project(id).is_some() => Ok(()),
            (false, Some(id)) => Err(CoreError::ProjectNotFound(id)),
            _ => Err(CoreError::NoSelectedProject),
        }
    }
}

impl Workspace {
    pub fn single_window() -> Self {
        Self {
            windows: vec![WorkspaceWindow::new()],
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{Pane, PaneContent, PaneId, PaneTree, Tab};

    fn project(name: &str) -> Project {
        let pane = Pane {
            id: PaneId::new(),
            content: PaneContent::Empty,
        };
        let tab = Tab::new(PaneTree::new(pane.clone()), pane.id).unwrap();
        let mut project = Project::new(Some(name.into()), None);
        project.add_tab(tab).unwrap();
        project
    }

    #[test]
    fn project_and_tab_selection_fall_back_to_next_then_previous() {
        let mut window = WorkspaceWindow::new();
        let first = project("one");
        let second = project("two");
        let third = project("three");
        let first_id = first.id;
        let second_id = second.id;
        let third_id = third.id;
        window.add_project(first).unwrap();
        window.add_project(second).unwrap();
        window.add_project(third).unwrap();
        window.select_project(second_id).unwrap();
        window.remove_project(second_id).unwrap();
        assert_eq!(window.selected_project, Some(third_id));
        window.remove_project(third_id).unwrap();
        assert_eq!(window.selected_project, Some(first_id));
        window.remove_project(first_id).unwrap();
        assert_eq!(window.selected_project, None);
        window.validate().unwrap();
    }

    #[test]
    fn removing_last_tab_leaves_valid_empty_project() {
        let mut project = project("project");
        let tab = project.selected_tab.unwrap();
        project.remove_tab(tab).unwrap();
        assert_eq!(project.selected_tab, None);
        assert!(project.tabs.is_empty());
        project.validate().unwrap();
    }
}
