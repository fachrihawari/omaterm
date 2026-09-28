use super::{WireCall, optional_selector};
use clap::Subcommand;
use serde_json::json;

#[derive(Debug, Clone, Subcommand)]
pub enum PaneCmd {
    /// List panes in the resolved tab.
    List {
        /// Tab ID; defaults to OMATERM_TAB_ID, then server selection.
        #[arg(long)]
        tab: Option<String>,
    },
    /// Split a pane; exactly one direction flag is required.
    Split {
        #[arg(long)]
        right: bool,
        #[arg(long)]
        down: bool,
        #[arg(long)]
        left: bool,
        #[arg(long)]
        up: bool,
        /// Split target; defaults to OMATERM_PANE_ID, then focused pane.
        #[arg(long)]
        target: Option<String>,
    },
    /// Focus a pane.
    Focus {
        /// Pane ID to focus.
        pane_id: String,
    },
    /// Close a pane.
    Close {
        /// Pane ID to close.
        pane_id: String,
    },
    /// Resize a split boundary to an absolute fraction.
    Resize {
        /// Split ID.
        #[arg(long)]
        split: String,
        /// Absolute fraction between 0.1 and 0.9.
        #[arg(long)]
        fraction: f32,
    },
    /// Reset every split in the tab to 0.5.
    Equalize {
        /// Tab ID; defaults to OMATERM_TAB_ID, then server selection.
        #[arg(long)]
        tab: Option<String>,
    },
}

pub fn build(cmd: &PaneCmd) -> Result<WireCall, String> {
    match cmd {
        PaneCmd::List { tab } => Ok(WireCall {
            method: "pane.list".into(),
            params: match optional_selector(tab.clone(), "OMATERM_TAB_ID") {
                Some(id) => json!({ "tab_id": id }),
                None => json!({}),
            },
        }),
        PaneCmd::Split {
            right,
            down,
            left,
            up,
            target,
        } => {
            let direction = match (*right, *down, *left, *up) {
                (true, false, false, false) => "right",
                (false, true, false, false) => "down",
                (false, false, true, false) => "left",
                (false, false, false, true) => "up",
                _ => {
                    return Err(
                        "pane split requires exactly one of --right/--down/--left/--up".into(),
                    );
                }
            };
            Ok(WireCall {
                method: "pane.split".into(),
                params: json!({
                    "pane_id": optional_selector(target.clone(), "OMATERM_PANE_ID"),
                    "direction": direction,
                }),
            })
        }
        PaneCmd::Focus { pane_id } => {
            if pane_id.trim().is_empty() {
                return Err("pane focus requires a pane ID".into());
            }
            Ok(WireCall {
                method: "pane.focus".into(),
                params: json!({ "pane_id": pane_id }),
            })
        }
        PaneCmd::Close { pane_id } => {
            if pane_id.trim().is_empty() {
                return Err("pane close requires a pane ID".into());
            }
            Ok(WireCall {
                method: "pane.close".into(),
                params: json!({ "pane_id": pane_id }),
            })
        }
        PaneCmd::Resize { split, fraction } => {
            if split.trim().is_empty() {
                return Err("pane resize requires --split <split-id>".into());
            }
            if !fraction.is_finite() {
                return Err("pane resize --fraction must be a finite number".into());
            }
            Ok(WireCall {
                method: "pane.resize".into(),
                params: json!({ "split_id": split, "fraction": fraction }),
            })
        }
        PaneCmd::Equalize { tab } => Ok(WireCall {
            method: "pane.equalize".into(),
            params: match optional_selector(tab.clone(), "OMATERM_TAB_ID") {
                Some(id) => json!({ "tab_id": id }),
                None => json!({}),
            },
        }),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn all_four_split_directions_map() {
        for (flags, direction) in [
            ((true, false, false, false), "right"),
            ((false, true, false, false), "down"),
            ((false, false, true, false), "left"),
            ((false, false, false, true), "up"),
        ] {
            let (right, down, left, up) = flags;
            let call = build(&PaneCmd::Split {
                right,
                down,
                left,
                up,
                target: None,
            })
            .unwrap();
            assert_eq!(call.method, "pane.split");
            assert_eq!(call.params["direction"], direction);
        }
    }

    #[test]
    fn split_rejects_zero_or_two_directions() {
        let result = build(&PaneCmd::Split {
            right: false,
            down: false,
            left: false,
            up: false,
            target: None,
        });
        assert!(result.is_err());
        let result = build(&PaneCmd::Split {
            right: true,
            down: true,
            left: false,
            up: false,
            target: None,
        });
        assert!(result.is_err());
    }

    #[test]
    fn list_maps_with_and_without_tab_selector() {
        let call = build(&PaneCmd::List { tab: None }).unwrap();
        assert_eq!(call.method, "pane.list");
        assert_eq!(call.params, serde_json::json!({}));
        let call = build(&PaneCmd::List {
            tab: Some("t1".into()),
        })
        .unwrap();
        assert_eq!(call.method, "pane.list");
        assert_eq!(call.params["tab_id"], "t1");
    }

    #[test]
    fn resize_equalize_focus_close_map() {
        let call = build(&PaneCmd::Resize {
            split: "s".into(),
            fraction: 0.4,
        })
        .unwrap();
        assert_eq!(call.method, "pane.resize");
        let call = build(&PaneCmd::Equalize { tab: None }).unwrap();
        assert_eq!(call.method, "pane.equalize");
        assert_eq!(
            build(&PaneCmd::Focus {
                pane_id: "p".into()
            })
            .unwrap()
            .method,
            "pane.focus"
        );
        assert_eq!(
            build(&PaneCmd::Close {
                pane_id: "p".into()
            })
            .unwrap()
            .method,
            "pane.close"
        );
    }
}
