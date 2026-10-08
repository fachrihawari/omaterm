//! Config and state directories.
//!
//! Linux keeps the XDG contract: `$XDG_*` when it is absolute, otherwise a
//! directory under `$HOME`. Windows has neither variable, so the same
//! helpers fall back to `%APPDATA%` (config) and `%LOCALAPPDATA%` (state).

use std::path::PathBuf;

/// `$XDG_CONFIG_HOME` or `$HOME/.config`, else the Windows roaming profile.
pub fn config_base_dir() -> Option<PathBuf> {
    resolve_base(
        absolute_env("XDG_CONFIG_HOME"),
        env_path("HOME"),
        ".config",
        platform_dir("APPDATA", &["AppData", "Roaming"]),
    )
}

/// `$XDG_STATE_HOME` or `$HOME/.local/state`, else the Windows local profile.
pub fn state_base_dir() -> Option<PathBuf> {
    resolve_base(
        absolute_env("XDG_STATE_HOME"),
        env_path("HOME"),
        ".local/state",
        platform_dir("LOCALAPPDATA", &["AppData", "Local"]),
    )
}

fn absolute_env(name: &str) -> Option<PathBuf> {
    env_path(name).filter(|path| path.is_absolute())
}

fn env_path(name: &str) -> Option<PathBuf> {
    std::env::var_os(name).map(PathBuf::from)
}

fn platform_dir(env_name: &str, profile_parts: &[&str]) -> Option<PathBuf> {
    #[cfg(windows)]
    {
        if let Some(path) = absolute_env(env_name) {
            return Some(path);
        }
        let mut path = env_path("USERPROFILE")?;
        for part in profile_parts {
            path.push(part);
        }
        Some(path)
    }
    #[cfg(not(windows))]
    {
        let _ = (env_name, profile_parts);
        None
    }
}

fn resolve_base(
    xdg: Option<PathBuf>,
    home: Option<PathBuf>,
    home_suffix: &str,
    platform: Option<PathBuf>,
) -> Option<PathBuf> {
    xdg.or_else(|| home.map(|path| path.join(home_suffix)))
        .or(platform)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn absolute_xdg_wins_over_home_and_platform() {
        let resolved = resolve_base(
            Some(PathBuf::from("/xdg")),
            Some(PathBuf::from("/home/user")),
            ".config",
            Some(PathBuf::from("/platform")),
        );
        assert_eq!(resolved, Some(PathBuf::from("/xdg")));
    }

    #[test]
    fn home_suffix_is_used_when_xdg_is_missing() {
        let resolved = resolve_base(
            None,
            Some(PathBuf::from("/home/user")),
            ".local/state",
            Some(PathBuf::from("/platform")),
        );
        assert_eq!(resolved, Some(PathBuf::from("/home/user/.local/state")));
    }

    #[test]
    fn platform_dir_is_used_when_xdg_and_home_are_missing() {
        let resolved = resolve_base(None, None, ".config", Some(PathBuf::from("/platform")));
        assert_eq!(resolved, Some(PathBuf::from("/platform")));
    }
}
