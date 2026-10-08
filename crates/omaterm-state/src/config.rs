//! General application configuration: the `config.toml` sections outside
//! `[history]` (M11 task 11D).
//!
//! ```toml
//! [terminal]
//! font-family = "JetBrains Mono"
//! font-size = 13
//! scrollback-lines = 10000
//! shell = "powershell"
//!
//! [appearance]
//! theme = "system"
//!
//! [automation]
//! enabled = true
//!
//! [files]
//! max-results = 100
//! show-hidden = false
//!
//! [git]
//! refresh-secs = 5
//! ```
//!
//! Every field is optional; an absent file or section means compiled defaults.
//! Malformed TOML is an explicit error (the caller warns and keeps defaults)
//! and out-of-range values are [`ConfigError::Invalid`], never silent
//! clamping — the user must see that their value did not apply. Loading never
//! writes, so unknown sections, comments, and formatting are preserved by
//! construction. The `[history]` section stays owned by `history.rs`.

use std::fs;
use std::path::Path;

use super::history::{ConfigError, default_config_toml_path};

/// Fallback terminal font size (matches the pre-config desktop default).
pub const DEFAULT_FONT_SIZE: f32 = 14.0;
/// Smallest/largest accepted `terminal.font-size`.
pub const MIN_FONT_SIZE: f32 = 6.0;
pub const MAX_FONT_SIZE: f32 = 72.0;
/// Largest accepted `terminal.scrollback-lines`. The engine default
/// (10 000) applies when the key is absent.
pub const MAX_SCROLLBACK_LINES: u32 = 100_000;
/// Default/largest accepted `files.max-results`. 100 matches the M13 file
/// listing default; the ceiling keeps a convenient local socket from
/// becoming an unbounded memory interface (blueprint §64).
pub const DEFAULT_MAX_RESULTS: u32 = 100;
pub const MAX_FILE_RESULTS: u32 = 5_000;
/// Default/bounds for `git.refresh-secs` (M14 debounced status refresh).
pub const DEFAULT_GIT_REFRESH_SECS: u64 = 5;
pub const MIN_GIT_REFRESH_SECS: u64 = 1;
pub const MAX_GIT_REFRESH_SECS: u64 = 300;
/// Accepted `appearance.theme` values. Only `system` changes nothing today;
/// `dark`/`light` parse and validate so the key is reserved for the future
/// theme engine (blueprint §58) without silently accepting typos.
pub const KNOWN_THEMES: &[&str] = &["system", "dark", "light"];
/// Windows shell ids the desktop palette can persist. Absent means PowerShell.
pub const KNOWN_SHELLS: &[&str] = &["powershell", "cmd", "git-bash"];

/// `[terminal]` section. All keys optional.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct TerminalSettings {
    pub font_family: Option<String>,
    pub font_size: Option<f32>,
    pub scrollback_lines: Option<u32>,
    /// `powershell`, `cmd`, or `git-bash`. Storage for the in-app picker.
    pub shell: Option<String>,
}

/// `[appearance]` section. All keys optional.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct AppearanceSettings {
    pub theme: Option<String>,
}

/// `[automation]` section. All keys optional.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct AutomationSettings {
    pub enabled: Option<bool>,
}

/// `[files]` section (M12, consumed by the M13 file tree). All keys optional.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct FilesSettings {
    pub max_results: Option<u32>,
    pub show_hidden: Option<bool>,
}

/// `[git]` section (M12, consumed by the M14 status panel). All keys optional.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct GitSettings {
    pub refresh_secs: Option<u64>,
}

/// Validated general configuration.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct AppConfig {
    pub terminal: TerminalSettings,
    pub appearance: AppearanceSettings,
    pub automation: AutomationSettings,
    pub files: FilesSettings,
    pub git: GitSettings,
}

impl AppConfig {
    /// Load from the canonical `config.toml`, or defaults when no config
    /// directory exists. A missing file is defaults; malformed TOML or an
    /// invalid value is an error.
    pub fn load() -> Result<Self, ConfigError> {
        match default_config_toml_path() {
            Some(path) => load_app_config_toml(&path),
            None => Ok(Self::default()),
        }
    }

    /// Effective font size: configured value or the desktop default.
    pub fn resolved_font_size(&self) -> f32 {
        self.terminal.font_size.unwrap_or(DEFAULT_FONT_SIZE)
    }

    /// Effective engine scrollback: `None` means the engine default.
    pub fn resolved_scrollback_lines(&self) -> Option<usize> {
        self.terminal.scrollback_lines.map(|lines| lines as usize)
    }

    /// Effective theme name: configured value or `"system"`.
    pub fn theme(&self) -> &str {
        self.appearance.theme.as_deref().unwrap_or("system")
    }

    /// Whether local automation stays enabled. Defaults to true; when false
    /// the desktop records the choice but IPC remains enabled in v0.1 —
    /// disabling the socket is future work, so this is validated and
    /// exposed rather than silently acted on.
    pub fn automation_enabled(&self) -> bool {
        self.automation.enabled.unwrap_or(true)
    }

    /// Effective file listing limit: configured value or 100.
    pub fn resolved_max_results(&self) -> u32 {
        self.files.max_results.unwrap_or(DEFAULT_MAX_RESULTS)
    }

    /// Whether dotfiles are included in file listings. Defaults to true:
    /// the file tree shows everything, including dotfiles.
    pub fn show_hidden(&self) -> bool {
        self.files.show_hidden.unwrap_or(true)
    }

    /// Effective git status refresh interval in seconds.
    pub fn resolved_git_refresh_secs(&self) -> u64 {
        self.git.refresh_secs.unwrap_or(DEFAULT_GIT_REFRESH_SECS)
    }
}

fn invalid(section: &str, key: &str, detail: impl Into<String>) -> ConfigError {
    ConfigError::Invalid(format!("{section}.{key} is not valid: {}", detail.into()))
}

/// Load and validate the general sections from `config.toml`. Unknown
/// sections (including `[history]`) are ignored, never modified.
pub fn load_app_config_toml(path: &Path) -> Result<AppConfig, ConfigError> {
    let bytes = match fs::read(path) {
        Ok(bytes) => bytes,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            return Ok(AppConfig::default());
        }
        Err(error) => return Err(ConfigError::Io(error)),
    };
    let text = String::from_utf8(bytes)
        .map_err(|_| ConfigError::Parse("config.toml is not UTF-8".into()))?;
    let doc: toml_edit::DocumentMut = text
        .parse()
        .map_err(|error| ConfigError::Parse(format!("config.toml parse failed: {error}")))?;
    let mut config = AppConfig::default();

    if let Some(table) = doc.get("terminal").and_then(toml_edit::Item::as_table_like) {
        if let Some(item) = table.get("font-family") {
            let family = item
                .as_str()
                .ok_or_else(|| invalid("terminal", "font-family", "expected a string"))?;
            if family.trim().is_empty() {
                return Err(invalid("terminal", "font-family", "must not be empty"));
            }
            config.terminal.font_family = Some(family.to_owned());
        }
        if let Some(item) = table.get("font-size") {
            let size = item
                .as_float()
                .or_else(|| item.as_integer().map(|size| size as f64))
                .ok_or_else(|| invalid("terminal", "font-size", "expected a number"))?;
            #[allow(clippy::cast_possible_truncation)]
            let size = size as f32;
            if !size.is_finite() || size < MIN_FONT_SIZE || size > MAX_FONT_SIZE {
                return Err(invalid(
                    "terminal",
                    "font-size",
                    format!("expected a size in [{MIN_FONT_SIZE}, {MAX_FONT_SIZE}]"),
                ));
            }
            config.terminal.font_size = Some(size);
        }
        if let Some(item) = table.get("scrollback-lines") {
            let lines = item
                .as_integer()
                .ok_or_else(|| invalid("terminal", "scrollback-lines", "expected an integer"))?;
            if lines < 0 || lines > i64::from(MAX_SCROLLBACK_LINES) {
                return Err(invalid(
                    "terminal",
                    "scrollback-lines",
                    format!("expected lines in [0, {MAX_SCROLLBACK_LINES}]"),
                ));
            }
            config.terminal.scrollback_lines = Some(lines as u32);
        }
        if let Some(item) = table.get("shell") {
            let shell = item
                .as_str()
                .ok_or_else(|| invalid("terminal", "shell", "expected a string"))?;
            if !KNOWN_SHELLS.contains(&shell) {
                return Err(invalid(
                    "terminal",
                    "shell",
                    format!("expected one of {}", KNOWN_SHELLS.join(", ")),
                ));
            }
            config.terminal.shell = Some(shell.to_owned());
        }
    }

    if let Some(table) = doc
        .get("appearance")
        .and_then(toml_edit::Item::as_table_like)
        && let Some(item) = table.get("theme")
    {
        let theme = item
            .as_str()
            .ok_or_else(|| invalid("appearance", "theme", "expected a string"))?;
        if !KNOWN_THEMES.contains(&theme) {
            return Err(invalid(
                "appearance",
                "theme",
                format!("expected one of {}", KNOWN_THEMES.join(", ")),
            ));
        }
        config.appearance.theme = Some(theme.to_owned());
    }

    if let Some(table) = doc
        .get("automation")
        .and_then(toml_edit::Item::as_table_like)
        && let Some(item) = table.get("enabled")
    {
        let enabled = item
            .as_bool()
            .ok_or_else(|| invalid("automation", "enabled", "expected true or false"))?;
        config.automation.enabled = Some(enabled);
    }

    if let Some(table) = doc.get("files").and_then(toml_edit::Item::as_table_like) {
        if let Some(item) = table.get("max-results") {
            let limit = item
                .as_integer()
                .ok_or_else(|| invalid("files", "max-results", "expected an integer"))?;
            if limit < 1 || limit > i64::from(MAX_FILE_RESULTS) {
                return Err(invalid(
                    "files",
                    "max-results",
                    format!("expected results in [1, {MAX_FILE_RESULTS}]"),
                ));
            }
            config.files.max_results = Some(limit as u32);
        }
        if let Some(item) = table.get("show-hidden") {
            let show = item
                .as_bool()
                .ok_or_else(|| invalid("files", "show-hidden", "expected true or false"))?;
            config.files.show_hidden = Some(show);
        }
    }

    if let Some(table) = doc.get("git").and_then(toml_edit::Item::as_table_like)
        && let Some(item) = table.get("refresh-secs")
    {
        let secs = item
            .as_integer()
            .ok_or_else(|| invalid("git", "refresh-secs", "expected an integer"))?;
        if secs < MIN_GIT_REFRESH_SECS as i64 || secs > MAX_GIT_REFRESH_SECS as i64 {
            return Err(invalid(
                "git",
                "refresh-secs",
                format!("expected seconds in [{MIN_GIT_REFRESH_SECS}, {MAX_GIT_REFRESH_SECS}]"),
            ));
        }
        config.git.refresh_secs = Some(secs as u64);
    }

    Ok(config)
}

/// Write `terminal.shell` and leave every other key, comment, and section
/// in place. The desktop calls this after a palette choice; it is not a
/// file the user is expected to edit by hand.
pub fn save_terminal_shell(path: &Path, shell: &str) -> Result<(), ConfigError> {
    if !KNOWN_SHELLS.contains(&shell) {
        return Err(invalid(
            "terminal",
            "shell",
            format!("expected one of {}", KNOWN_SHELLS.join(", ")),
        ));
    }
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(ConfigError::Io)?;
    }
    let mut doc: toml_edit::DocumentMut = match fs::read(path) {
        Ok(bytes) => {
            let text = String::from_utf8(bytes)
                .map_err(|_| ConfigError::Parse("config.toml is not UTF-8".into()))?;
            text.parse()
                .map_err(|error| ConfigError::Parse(format!("config.toml parse failed: {error}")))?
        }
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => toml_edit::DocumentMut::new(),
        Err(error) => return Err(ConfigError::Io(error)),
    };
    if !doc
        .get("terminal")
        .is_some_and(toml_edit::Item::is_table_like)
    {
        doc["terminal"] = toml_edit::Item::Table(toml_edit::Table::new());
    }
    doc["terminal"]["shell"] = toml_edit::value(shell);
    super::history::write_atomic_0600(path, doc.to_string().as_bytes())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    fn write_config(dir: &std::path::Path, text: &str) -> std::path::PathBuf {
        let path = dir.join("config.toml");
        let mut file = fs::File::create(&path).unwrap();
        file.write_all(text.as_bytes()).unwrap();
        path
    }

    fn temp_dir(name: &str) -> std::path::PathBuf {
        let dir = std::env::temp_dir().join(format!(
            "omaterm-appconfig-test-{}-{}",
            std::process::id(),
            name
        ));
        let _ = fs::remove_dir_all(&dir);
        fs::create_dir_all(&dir).unwrap();
        dir
    }

    #[test]
    fn missing_file_yields_defaults() {
        let dir = temp_dir("missing");
        let config = load_app_config_toml(&dir.join("config.toml")).unwrap();
        assert_eq!(config, AppConfig::default());
        assert_eq!(config.resolved_font_size(), DEFAULT_FONT_SIZE);
        assert_eq!(config.resolved_scrollback_lines(), None);
        assert_eq!(config.theme(), "system");
        assert!(config.automation_enabled());
        assert_eq!(config.resolved_max_results(), DEFAULT_MAX_RESULTS);
        // The file tree shows everything by default, including dotfiles.
        assert!(config.show_hidden());
        assert_eq!(config.resolved_git_refresh_secs(), DEFAULT_GIT_REFRESH_SECS);
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn full_sections_parse_and_resolve() {
        let dir = temp_dir("full");
        let path = write_config(
            &dir,
            "[terminal]\nfont-family = \"JetBrains Mono\"\nfont-size = 13\nscrollback-lines = 5000\n[appearance]\ntheme = \"dark\"\n[automation]\nenabled = false\n[files]\nmax-results = 250\nshow-hidden = true\n[git]\nrefresh-secs = 10\n[history]\nenabled = true\n",
        );
        let config = load_app_config_toml(&path).unwrap();
        assert_eq!(
            config.terminal.font_family.as_deref(),
            Some("JetBrains Mono")
        );
        assert_eq!(config.resolved_font_size(), 13.0);
        assert_eq!(config.resolved_scrollback_lines(), Some(5000));
        assert_eq!(config.theme(), "dark");
        assert!(!config.automation_enabled());
        assert_eq!(config.resolved_max_results(), 250);
        assert!(config.show_hidden());
        assert_eq!(config.resolved_git_refresh_secs(), 10);
        // Loading never writes: foreign sections and bytes are untouched.
        let after = fs::read_to_string(&path).unwrap();
        assert!(after.contains("[history]"));
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn invalid_values_are_errors_not_silent_defaults() {
        let dir = temp_dir("invalid");
        for (name, text) in [
            ("size", "[terminal]\nfont-size = 200\n"),
            ("tiny", "[terminal]\nfont-size = 1\n"),
            ("nan", "[terminal]\nfont-size = nan\n"),
            ("scroll", "[terminal]\nscrollback-lines = 99999999\n"),
            ("negative", "[terminal]\nscrollback-lines = -5\n"),
            ("family", "[terminal]\nfont-family = \"  \"\n"),
            ("theme", "[appearance]\ntheme = \"dracula\"\n"),
            ("auto", "[automation]\nenabled = \"yes\"\n"),
            ("max-results-huge", "[files]\nmax-results = 999999\n"),
            ("max-results-zero", "[files]\nmax-results = 0\n"),
            ("max-results-type", "[files]\nmax-results = \"many\"\n"),
            ("show-hidden", "[files]\nshow-hidden = \"yes\"\n"),
            ("refresh-zero", "[git]\nrefresh-secs = 0\n"),
            ("refresh-huge", "[git]\nrefresh-secs = 9999\n"),
            ("refresh-type", "[git]\nrefresh-secs = 1.5\n"),
            ("shell", "[terminal]\nshell = \"zsh\"\n"),
            ("shell-type", "[terminal]\nshell = 1\n"),
        ] {
            let path = write_config(&dir, text);
            let error = load_app_config_toml(&path).expect_err(name);
            assert!(
                matches!(error, ConfigError::Invalid(_)),
                "{name}: {error:?}"
            );
        }
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn terminal_shell_round_trip_preserves_other_keys() {
        let dir = temp_dir("shell");
        let path = write_config(
            &dir,
            "[terminal]\nfont-size = 16\n\n[git]\nrefresh-secs = 9\n",
        );
        save_terminal_shell(&path, "git-bash").unwrap();
        let loaded = load_app_config_toml(&path).unwrap();
        assert_eq!(loaded.terminal.shell.as_deref(), Some("git-bash"));
        assert_eq!(loaded.resolved_font_size(), 16.0);
        assert_eq!(loaded.resolved_git_refresh_secs(), 9);
        assert!(save_terminal_shell(&path, "zsh").is_err());
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn inline_terminal_table_loads_and_updates_shell() {
        let dir = temp_dir("inline-shell");
        let path = write_config(
            &dir,
            "terminal = { shell = \"git-bash\", font-size = 16 }\n",
        );
        let loaded = load_app_config_toml(&path).unwrap();
        assert_eq!(loaded.terminal.shell.as_deref(), Some("git-bash"));
        assert_eq!(loaded.resolved_font_size(), 16.0);
        save_terminal_shell(&path, "cmd").unwrap();
        let updated = load_app_config_toml(&path).unwrap();
        assert_eq!(updated.terminal.shell.as_deref(), Some("cmd"));
        assert_eq!(updated.resolved_font_size(), 16.0);
        let fresh = dir.join("fresh.toml");
        save_terminal_shell(&fresh, "git-bash").unwrap();
        assert_eq!(
            load_app_config_toml(&fresh)
                .unwrap()
                .terminal
                .shell
                .as_deref(),
            Some("git-bash")
        );
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn malformed_toml_is_an_error() {
        let dir = temp_dir("malformed");
        let path = write_config(&dir, "[terminal\nfont-size = ");
        let error = load_app_config_toml(&path).unwrap_err();
        assert!(matches!(error, ConfigError::Parse(_)));
        let _ = fs::remove_dir_all(&dir);
    }
}
