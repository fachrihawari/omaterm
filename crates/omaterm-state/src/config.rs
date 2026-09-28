//! General application configuration: the `config.toml` sections outside
//! `[history]` (M11 task 11D).
//!
//! ```toml
//! [terminal]
//! font-family = "JetBrains Mono"
//! font-size = 13
//! scrollback-lines = 10000
//!
//! [appearance]
//! theme = "system"
//!
//! [automation]
//! enabled = true
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
/// Accepted `appearance.theme` values. Only `system` changes nothing today;
/// `dark`/`light` parse and validate so the key is reserved for the future
/// theme engine (blueprint §58) without silently accepting typos.
pub const KNOWN_THEMES: &[&str] = &["system", "dark", "light"];

/// `[terminal]` section. All keys optional.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct TerminalSettings {
    pub font_family: Option<String>,
    pub font_size: Option<f32>,
    pub scrollback_lines: Option<u32>,
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

/// Validated general configuration.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct AppConfig {
    pub terminal: TerminalSettings,
    pub appearance: AppearanceSettings,
    pub automation: AutomationSettings,
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

    if let Some(table) = doc.get("terminal").and_then(toml_edit::Item::as_table) {
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
    }

    if let Some(table) = doc.get("appearance").and_then(toml_edit::Item::as_table)
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

    if let Some(table) = doc.get("automation").and_then(toml_edit::Item::as_table)
        && let Some(item) = table.get("enabled")
    {
        let enabled = item
            .as_bool()
            .ok_or_else(|| invalid("automation", "enabled", "expected true or false"))?;
        config.automation.enabled = Some(enabled);
    }

    Ok(config)
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
        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn full_sections_parse_and_resolve() {
        let dir = temp_dir("full");
        let path = write_config(
            &dir,
            "[terminal]\nfont-family = \"JetBrains Mono\"\nfont-size = 13\nscrollback-lines = 5000\n[appearance]\ntheme = \"dark\"\n[automation]\nenabled = false\n[history]\nenabled = true\n",
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
    fn malformed_toml_is_an_error() {
        let dir = temp_dir("malformed");
        let path = write_config(&dir, "[terminal\nfont-size = ");
        let error = load_app_config_toml(&path).unwrap_err();
        assert!(matches!(error, ConfigError::Parse(_)));
        let _ = fs::remove_dir_all(&dir);
    }
}
