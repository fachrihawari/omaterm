//! Thin app identity / update CLI mapping (M21 S3).

use super::WireCall;
use clap::Subcommand;
use serde_json::json;

#[derive(Debug, Clone, Subcommand)]
pub enum UpdateCmd {
    /// Check the stable channel for a newer release. Async server-side:
    /// the desktop answers once the bounded check finishes.
    Check,
    /// Download, verify and install the newest stable release. Refuses
    /// downgrades unless `--confirm-downgrade` is given.
    Apply {
        /// Confirm a deliberate roll-back to an older release.
        #[arg(long, default_value_t = false)]
        confirm_downgrade: bool,
    },
}

pub fn build_update(command: &UpdateCmd) -> WireCall {
    match command {
        UpdateCmd::Check => WireCall {
            method: "app.update-check".into(),
            params: json!({}),
        },
        UpdateCmd::Apply { confirm_downgrade } => WireCall {
            method: "app.update-apply".into(),
            params: json!({"confirm_downgrade": confirm_downgrade}),
        },
    }
}

pub fn build_about() -> WireCall {
    WireCall {
        method: "app.about".into(),
        params: json!({}),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn about_and_update_check_map_to_wire() {
        let call = build_about();
        assert_eq!(call.method, "app.about");
        let call = build_update(&UpdateCmd::Check);
        assert_eq!(call.method, "app.update-check");
    }

    #[test]
    fn update_apply_maps_the_downgrade_flag() {
        let call = build_update(&UpdateCmd::Apply {
            confirm_downgrade: false,
        });
        assert_eq!(call.method, "app.update-apply");
        assert_eq!(call.params["confirm_downgrade"], false);
        let call = build_update(&UpdateCmd::Apply {
            confirm_downgrade: true,
        });
        assert_eq!(call.params["confirm_downgrade"], true);
    }
}
