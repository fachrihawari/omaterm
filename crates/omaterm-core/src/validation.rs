use crate::{
    CommandError, ErrorCode, HistoryCommand, OmaCommand, PaneCommand, ProjectCommand, TabCommand,
    TerminalCommand,
};

pub const MAX_READ_LINES: usize = 1_000;
pub const MAX_READ_COLUMNS: usize = 1_000;
pub const MAX_SEND_BYTES: usize = 8 * 1024;
pub const MAX_ARG_COUNT: usize = 256;
pub const MAX_ARG_BYTES: usize = 4 * 1024;
/// Bounded journal listing: same 1000-entry ceiling as viewport reads.
pub const MAX_JOURNAL_ENTRIES: usize = 1_000;

/// Pure field validation. Target existence and authorization are checked by
/// the application owner immediately before dispatch effects are applied.
pub fn validate(command: &OmaCommand) -> Result<(), CommandError> {
    let invalid = |message: &str| Err(CommandError::new(ErrorCode::InvalidRequest, message));
    let valid_name = |name: &str| !name.trim().is_empty() && !name.chars().any(char::is_control);
    match command {
        OmaCommand::Project(ProjectCommand::Create { name, directory }) => {
            if name.as_deref().is_some_and(|n| !valid_name(n)) {
                return invalid("project name must be non-empty and contain no control characters");
            }
            if directory.as_ref().is_some_and(|path| !path.is_dir()) {
                return invalid("project directory must exist and be a directory");
            }
        }
        OmaCommand::Project(ProjectCommand::Rename { name, .. })
        | OmaCommand::Tab(TabCommand::Rename { name, .. }) => {
            if !valid_name(name) {
                return invalid("name must be non-empty and contain no control characters");
            }
        }
        OmaCommand::Tab(TabCommand::Create { name, .. }) => {
            if name.as_deref().is_some_and(|n| !valid_name(n)) {
                return invalid("tab name must be non-empty and contain no control characters");
            }
        }
        OmaCommand::Pane(PaneCommand::Resize { fraction, .. }) => {
            if !fraction.is_finite() || !(0.1..=0.9).contains(fraction) {
                return invalid("split fraction must be finite and between 0.1 and 0.9");
            }
        }
        OmaCommand::Pane(PaneCommand::ResizeFocused { amount }) => {
            if !amount.is_finite() || amount.abs() > 0.8 {
                return invalid("resize amount must be finite and bounded");
            }
        }
        OmaCommand::Terminal(TerminalCommand::Create { directory, .. }) => {
            if directory.as_ref().is_some_and(|path| !path.is_dir()) {
                return invalid("terminal directory must exist and be a directory");
            }
        }
        OmaCommand::Terminal(TerminalCommand::RestorePane { directory, .. }) => {
            if directory.as_os_str().is_empty() || (!directory.is_dir() && directory.exists()) {
                return invalid("restore directory must be a valid directory path");
            }
        }
        OmaCommand::Terminal(TerminalCommand::SendBytes { data, .. }) => {
            if data.len() > MAX_SEND_BYTES {
                return invalid("terminal input exceeds the 8 KiB command limit");
            }
        }
        OmaCommand::Terminal(TerminalCommand::RunCommand { argv, .. }) => {
            if argv.is_empty() || argv.len() > MAX_ARG_COUNT {
                return invalid("argv must contain 1 to 256 arguments");
            }
            if argv
                .iter()
                .any(|arg| arg.len() > MAX_ARG_BYTES || arg.chars().any(char::is_control))
            {
                return invalid(
                    "each argument must be at most 4096 bytes and contain no control characters",
                );
            }
        }
        OmaCommand::Terminal(TerminalCommand::ReadVisible {
            max_lines,
            max_columns,
            ..
        }) if *max_lines == 0
            || *max_lines > MAX_READ_LINES
            || *max_columns == 0
            || *max_columns > MAX_READ_COLUMNS =>
        {
            return invalid("read bounds must be between 1 and 1000 lines/columns");
        }
        OmaCommand::History(HistoryCommand::ListJournal { limit, .. })
            if *limit == 0 || *limit > MAX_JOURNAL_ENTRIES =>
        {
            return invalid("journal list limit must be between 1 and 1000 entries");
        }
        _ => {}
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{PaneId, ProjectId, SessionId, SplitId, TabId};

    #[test]
    fn validates_fraction_names_and_bounded_terminal_commands() {
        assert!(
            validate(&OmaCommand::Pane(PaneCommand::Resize {
                split: SplitId::new(),
                fraction: f32::NAN
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::Project(ProjectCommand::Create {
                name: Some("  ".into()),
                directory: None
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::Terminal(TerminalCommand::ReadVisible {
                session: SessionId::new(),
                max_lines: 1001,
                max_columns: 10
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::Terminal(TerminalCommand::SendBytes {
                session: SessionId::new(),
                data: vec![0; MAX_SEND_BYTES + 1]
            }))
            .is_err()
        );
        assert!(
            validate(&OmaCommand::Terminal(TerminalCommand::RunCommand {
                session: SessionId::new(),
                argv: vec![]
            }))
            .is_err()
        );
    }

    #[test]
    fn accepts_boundary_values_and_empty_optional_names() {
        assert!(
            validate(&OmaCommand::Pane(PaneCommand::Resize {
                split: SplitId::new(),
                fraction: 0.1
            }))
            .is_ok()
        );
        assert!(
            validate(&OmaCommand::Project(ProjectCommand::Create {
                name: None,
                directory: Some(std::env::temp_dir())
            }))
            .is_ok()
        );
        assert!(
            validate(&OmaCommand::Tab(TabCommand::Create {
                project: ProjectId::new(),
                name: None
            }))
            .is_ok()
        );
        assert!(
            validate(&OmaCommand::Pane(PaneCommand::Focus {
                pane: PaneId::new()
            }))
            .is_ok()
        );
        assert!(
            validate(&OmaCommand::Tab(TabCommand::Rename {
                tab: TabId::new(),
                name: "Tests".into()
            }))
            .is_ok()
        );
    }
}
