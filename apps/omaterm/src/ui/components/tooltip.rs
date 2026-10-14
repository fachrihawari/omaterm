//! One reusable hover tooltip for icon-only chrome controls.
//!
//! GPUI's `tooltip()` requires an `AnyView`, i.e. an entity with a `Render`
//! impl, so the alternative would be a view type per control. Instead this
//! module owns a single `Tooltip` view plus [`attach`], which builds the
//! `AnyView` on demand from the label text. Callers write
//! `attach(div, "Fetch", cx)`.
//!
//! The label is a static string: tooltips here exist to name icon-only
//! controls for pointer users, not to display live data.

use gpui::{
    AnyView, App, Context, IntoElement, Render, SharedString, Window, div, prelude::*, px, rgb,
};

use crate::ui::metrics::{self};
use crate::ui::theme;

/// A small text tooltip. One instance per hover is created by [`attach`]; the
/// view holds no state beyond its label.
pub struct Tooltip {
    label: SharedString,
}

impl Render for Tooltip {
    fn render(&mut self, _: &mut Window, _: &mut Context<Self>) -> impl IntoElement {
        let colors = theme::colors();
        metrics::text_role(div(), metrics::META_10)
            .px(px(8.0))
            .py(px(4.0))
            .rounded(px(4.0))
            .border_1()
            .border_color(rgb(colors.border2))
            .bg(rgb(colors.panel2))
            .text_color(rgb(colors.text))
            .child(self.label.clone())
    }
}

/// Attach `label` as a tooltip to an interactive element.
pub fn attach<E>(element: E, label: &'static str, cx: &mut App) -> E
where
    E: gpui::InteractiveElement + gpui::StatefulInteractiveElement,
{
    let view = cx.new(|_| Tooltip {
        label: SharedString::from(label),
    });
    element.tooltip(move |_, _| -> AnyView { view.clone().into() })
}
