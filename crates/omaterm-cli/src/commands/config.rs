//! Thin general-configuration CLI mapping (M21 S2).

use super::WireCall;
use clap::Subcommand;
use serde_json::json;

#[derive(Debug, Clone, Subcommand)]
pub enum ConfigCmd {
    /// Show the effective configuration (resolved values, not raw file).
    Get,
    /// Set one dotted key (e.g. `terminal.font-size 13`). Validated
    /// server-side before touching the disk; invalid values fail with
    /// `invalid_request` and leave the file untouched.
    Set {
        /// Dotted key (`section.key`, see `config get` for the whitelist).
        key: String,
        /// Raw value (`true`, `13`, `dark` — parsed per key).
        value: String,
    },
    /// Reset one dotted key (or every known key) to compiled defaults.
    Reset {
        /// Dotted key to clear; absent means all known keys.
        key: Option<String>,
    },
}

pub fn build(command: &ConfigCmd) -> Result<WireCall, String> {
    match command {
        ConfigCmd::Get => Ok(WireCall {
            method: "config.get".into(),
            params: json!({}),
        }),
        ConfigCmd::Set { key, value } => {
            if key.trim().is_empty() {
                return Err("missing config key (e.g. terminal.font-size)".into());
            }
            if value.len() > 256 {
                return Err("config value exceeds the 256-byte limit".into());
            }
            Ok(WireCall {
                method: "config.set".into(),
                params: json!({"key": key.trim(), "value": value}),
            })
        }
        ConfigCmd::Reset { key } => Ok(WireCall {
            method: "config.reset".into(),
            params: match key {
                Some(key) => json!({"key": key.trim()}),
                None => json!({}),
            },
        }),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn get_set_and_reset_map_to_wire() {
        let call = build(&ConfigCmd::Get).unwrap();
        assert_eq!(call.method, "config.get");
        let call = build(&ConfigCmd::Set {
            key: "terminal.font-size".into(),
            value: "13".into(),
        })
        .unwrap();
        assert_eq!(call.method, "config.set");
        assert_eq!(call.params["key"], "terminal.font-size");
        assert_eq!(call.params["value"], "13");
        let call = build(&ConfigCmd::Reset { key: None }).unwrap();
        assert_eq!(call.method, "config.reset");
        assert!(call.params.get("key").is_none());
        let call = build(&ConfigCmd::Reset {
            key: Some("appearance.theme".into()),
        })
        .unwrap();
        assert_eq!(call.params["key"], "appearance.theme");
    }

    #[test]
    fn empty_key_and_oversized_value_are_usage_errors() {
        assert!(
            build(&ConfigCmd::Set {
                key: "  ".into(),
                value: "1".into(),
            })
            .is_err()
        );
        assert!(
            build(&ConfigCmd::Set {
                key: "terminal.font-size".into(),
                value: "x".repeat(257),
            })
            .is_err()
        );
    }
}
