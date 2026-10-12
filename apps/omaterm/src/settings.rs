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

    /// Position in [`SettingsSection::ALL`], for the `1..4` keyboard nav.
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
}
