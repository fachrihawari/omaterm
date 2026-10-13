//! Settings overlay model (M21 S1): section identity plus static content.
//!
//! GPUI-free by construction: render code in `main.rs` owns widgets, this
//! module owns the section enum, the palette key, and the license/about text
//! so the decisions are unit-testable without a desktop window.

/// Palette key for the Settings entry. A `ViewAction` like the theme rows:
/// the confirm handler closes the palette first, then opens the overlay.
pub const PALETTE_KEY: &str = "command.settings.open";

/// Settings overlay sections, in nav order.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum SettingsSection {
    #[default]
    About,
    Update,
    Configuration,
    License,
}

impl SettingsSection {
    /// All sections in nav order. The render nav and the `1..4` keys share
    /// this so what is clickable is what the keyboard reaches.
    pub const ALL: [SettingsSection; 4] = [
        SettingsSection::About,
        SettingsSection::Update,
        SettingsSection::Configuration,
        SettingsSection::License,
    ];

    /// Uppercase nav label, matching the inspector group-header grammar.
    pub fn label(self) -> &'static str {
        match self {
            SettingsSection::About => "ABOUT",
            SettingsSection::Update => "UPDATE",
            SettingsSection::Configuration => "CONFIGURATION",
            SettingsSection::License => "LICENSE",
        }
    }

    /// Lucide asset slot for the nav rail. Kept as a `&'static str` (the
    /// vendored asset name) so this module stays GPUI-free.
    pub fn icon(self) -> &'static str {
        match self {
            SettingsSection::About => "info",
            SettingsSection::Update => "refresh-cw",
            SettingsSection::Configuration => "settings-2",
            SettingsSection::License => "book",
        }
    }

    /// Position in [`SettingsSection::ALL`]. The nav rail shows icons, not
    /// digits, so this is the test-visible order contract that
    /// [`SettingsSection::from_index`] (the `1..4` keyboard nav) mirrors.
    #[cfg(test)]
    pub fn index(self) -> usize {
        match self {
            SettingsSection::About => 0,
            SettingsSection::Update => 1,
            SettingsSection::Configuration => 2,
            SettingsSection::License => 3,
        }
    }

    /// Inverse of [`SettingsSection::index`]: out-of-range keys select
    /// nothing instead of wrapping.
    pub fn from_index(index: usize) -> Option<SettingsSection> {
        SettingsSection::ALL.get(index).copied()
    }
}

/// Full MIT license text, embedded so the License tab works offline and
/// always matches the shipped grant (never a disk copy that may be absent
/// in a relocatable install).
pub const LICENSE_MIT_TEXT: &str = include_str!("../../../LICENSE-MIT");

/// Full Apache-2.0 license text, same embedding contract as
/// [`LICENSE_MIT_TEXT`].
pub const LICENSE_APACHE_TEXT: &str = include_str!("../../../LICENSE-APACHE");

/// Human-facing label and one-line description for a config row. The raw
/// dotted key stays visible (muted, under the label) so a power user can
/// map the control to `config.toml`, but the primary text is prose —
/// `terminal.scrollback-lines` reads as "Scrollback", not as a key.
pub struct SettingMeta {
    /// Row title (`Font size`).
    pub label: String,
    /// One-line explanation; empty when the label is self-evident.
    pub help: String,
}

/// Look up the human label for a dotted config key. Unknown keys fall
/// back to the raw key with no help text so a future key is never
/// silently mislabeled.
pub fn setting_meta(key: &str) -> SettingMeta {
    let (label, help) = match key {
        "appearance.theme" => ("Theme", "Light, dark, or follow the system"),
        "appearance.translucent-sidebar" => (
            "Transparent sidebar",
            "Blur the desktop behind the project list",
        ),
        "terminal.font-family" => ("Font", "Monospace family for terminal text"),
        "terminal.font-size" => ("Font size", "Point size of terminal text"),
        "terminal.shell" => ("Shell", "Default shell on Windows"),
        "terminal.scrollback-lines" => ("Scrollback", "Lines kept per pane above the viewport"),
        "files.show-hidden" => ("Show hidden files", "Include dotfiles in the file tree"),
        "files.max-results" => ("Max results", "Upper bound for file listings and search"),
        "git.refresh-secs" => ("Refresh interval", "Seconds between Git status polls"),
        "automation.enabled" => (
            "Automation",
            "Recorded in config.toml (socket stays on in v0.1)",
        ),
        "updates.check-on-startup" => (
            "Check on launch",
            "Resolve the stable channel once per start",
        ),
        _ => {
            return SettingMeta {
                label: key.to_owned(),
                help: String::new(),
            };
        }
    };
    SettingMeta {
        label: label.to_owned(),
        help: help.to_owned(),
    }
}

/// Theme choices in cycle/pop-up order, with their display labels. Shared
/// by the picker so the visible order and the stored value agree.
pub const THEME_CHOICES: [(&str, &str); 3] =
    [("system", "System"), ("dark", "Dark"), ("light", "Light")];

/// Next stepped value for a scalar config row (M21 S2): `direction` is
/// `-1`/`+1`. Font steps 0.5 within 6–72; counts step by one within
/// their ceilings. Returns `None` when the current text does not parse
/// (the row shows a non-numeric effective value like `engine default`)
/// or the step would leave the valid range — the caller then renders a
/// dead stepper instead of clamping into a surprising value.
pub fn step_config_value(key: &str, current: &str, direction: i32) -> Option<String> {
    let step = direction as f32;
    match key {
        "terminal.font-size" => {
            let size: f32 = current.trim().parse().ok()?;
            let next = (size + 0.5 * step).clamp(6.0, 72.0);
            if (next - size).abs() < f32::EPSILON {
                return None;
            }
            Some(format!("{next:.1}"))
        }
        "terminal.scrollback-lines" => {
            let lines: i64 = current.trim().parse().ok()?;
            let next = lines + i64::from(direction);
            if !(0..=100_000).contains(&next) {
                return None;
            }
            Some(next.to_string())
        }
        "files.max-results" => {
            let limit: i64 = current.trim().parse().ok()?;
            let next = limit + i64::from(direction);
            if !(1..=5_000).contains(&next) {
                return None;
            }
            Some(next.to_string())
        }
        "git.refresh-secs" => {
            let secs: i64 = current.trim().parse().ok()?;
            let next = secs + i64::from(direction);
            if !(1..=300).contains(&next) {
                return None;
            }
            Some(next.to_string())
        }
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashSet;

    #[test]
    fn every_section_has_a_unique_non_empty_label() {
        let mut seen = HashSet::new();
        for section in SettingsSection::ALL {
            assert!(!section.label().is_empty());
            assert!(seen.insert(section.label()), "duplicate label");
        }
        assert_eq!(seen.len(), SettingsSection::ALL.len());
    }

    #[test]
    fn every_section_has_an_icon_slot() {
        for section in SettingsSection::ALL {
            assert!(!section.icon().is_empty());
        }
    }

    #[test]
    fn section_index_round_trips_without_wrapping() {
        for (index, section) in SettingsSection::ALL.iter().enumerate() {
            assert_eq!(section.index(), index);
            assert_eq!(SettingsSection::from_index(index), Some(*section));
        }
        assert_eq!(
            SettingsSection::from_index(SettingsSection::ALL.len()),
            None
        );
        assert_eq!(SettingsSection::from_index(usize::MAX), None);
    }

    #[test]
    fn default_section_is_about() {
        assert_eq!(SettingsSection::default(), SettingsSection::About);
    }

    #[test]
    fn license_texts_are_embedded_and_identifiable() {
        assert!(LICENSE_MIT_TEXT.contains("MIT"));
        assert!(LICENSE_APACHE_TEXT.contains("Apache License"));
        assert!(LICENSE_APACHE_TEXT.contains("Version 2.0"));
    }

    #[test]
    fn stepper_steps_within_bounds_and_gives_up_outside() {
        assert_eq!(
            step_config_value("terminal.font-size", "14.0", 1).as_deref(),
            Some("14.5")
        );
        assert_eq!(step_config_value("terminal.font-size", "6.0", -1), None);
        assert_eq!(step_config_value("terminal.font-size", "72.0", 1), None);
        assert_eq!(
            step_config_value("files.max-results", "100", 1).as_deref(),
            Some("101")
        );
        assert_eq!(step_config_value("files.max-results", "1", -1), None);
        assert_eq!(
            step_config_value("git.refresh-secs", "5", -1).as_deref(),
            Some("4")
        );
        // Non-numeric effective values (engine default) yield no step.
        assert_eq!(
            step_config_value("terminal.scrollback-lines", "engine default", 1),
            None
        );
        // Unknown keys never step.
        assert_eq!(step_config_value("appearance.theme", "dark", 1), None);
    }

    #[test]
    fn known_keys_have_human_labels_and_unknown_keys_fall_back() {
        let meta = setting_meta("terminal.scrollback-lines");
        assert_eq!(meta.label, "Scrollback");
        assert!(!meta.help.is_empty());
        // Every whitelisted key gets a real label (not the raw key).
        for key in omaterm_state::CONFIG_KEYS {
            let meta = setting_meta(key);
            assert_ne!(meta.label, *key, "{key} needs a human label");
        }
        // An unknown key is shown verbatim rather than mislabeled.
        let unknown = setting_meta("future.key");
        assert_eq!(unknown.label, "future.key");
        assert!(unknown.help.is_empty());
    }

    #[test]
    fn theme_choices_cover_the_known_themes() {
        let values: Vec<&str> = THEME_CHOICES.iter().map(|(value, _)| *value).collect();
        assert_eq!(values, vec!["system", "dark", "light"]);
        for (_, label) in THEME_CHOICES {
            assert!(!label.is_empty());
        }
    }
}
