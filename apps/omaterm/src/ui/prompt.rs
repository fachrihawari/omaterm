//! Theme-aligned modal prompts with explicit, keyboard-accessible choices.
use gpui::{
    App, AppContext, Context, EventEmitter, FocusHandle, Focusable, IntoElement, KeyDownEvent,
    PromptButton, PromptHandle, PromptResponse, Render, RenderablePromptHandle, Window, div,
    prelude::*, px, rgb, rgba,
};

use super::{assets, metrics, theme};

pub fn install(cx: &mut App) {
    cx.set_prompt_builder(|level, message, detail, actions, handle, window, cx| {
        build(level, message, detail, actions, handle, window, cx)
    });
}

fn build(
    level: gpui::PromptLevel,
    message: &str,
    detail: Option<&str>,
    actions: &[PromptButton],
    handle: PromptHandle,
    window: &mut Window,
    cx: &mut App,
) -> RenderablePromptHandle {
    let view = cx.new(|cx| ThemedPrompt {
        level,
        message: message.into(),
        detail: detail.map(str::to_owned),
        actions: actions.to_vec(),
        selected: 0,
        focus: cx.focus_handle(),
    });
    handle.with_view(view, window, cx)
}

struct ThemedPrompt {
    level: gpui::PromptLevel,
    message: String,
    detail: Option<String>,
    actions: Vec<PromptButton>,
    selected: usize,
    focus: FocusHandle,
}

impl ThemedPrompt {
    /// Severity icon: callers pass `PromptLevel`, so the icon reflects it
    /// instead of always rendering undo/orange.
    fn severity_icon(&self) -> (&'static str, u32) {
        match self.level {
            gpui::PromptLevel::Info => (assets::INFO, theme::colors().blue),
            gpui::PromptLevel::Warning => (assets::UNDO, theme::colors().orange),
            gpui::PromptLevel::Critical => (assets::UNDO, theme::colors().red),
        }
    }
}

impl EventEmitter<PromptResponse> for ThemedPrompt {}

impl Focusable for ThemedPrompt {
    fn focus_handle(&self, _: &App) -> FocusHandle {
        self.focus.clone()
    }
}

impl Render for ThemedPrompt {
    fn render(&mut self, _: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let mut buttons = div().flex().justify_end().items_center().gap(px(8.0));
        for (index, action) in self.actions.iter().enumerate() {
            let destructive = index != 0;
            buttons = buttons.child(
                metrics::text_role(div(), metrics::BODY_11_MEDIUM)
                    .id(index)
                    .cursor_pointer()
                    .px(px(14.0))
                    .py(px(7.0))
                    .rounded(px(7.0))
                    .border_1()
                    .border_color(rgb(if self.selected == index {
                        theme::colors().blue
                    } else if destructive {
                        theme::colors().red
                    } else {
                        theme::colors().border2
                    }))
                    .bg(if destructive {
                        rgba(theme::with_alpha(theme::colors().red, 0.12))
                    } else {
                        rgb(theme::colors().panel3)
                    })
                    .text_color(rgb(if destructive {
                        theme::colors().red
                    } else {
                        theme::colors().text
                    }))
                    .hover(|style| {
                        if destructive {
                            style.bg(rgba(theme::with_alpha(theme::colors().red, 0.2)))
                        } else {
                            style
                                .bg(rgb(theme::colors().cmd_hover_bg))
                                .border_color(rgb(theme::colors().cmd_hover_border))
                        }
                    })
                    .on_click(cx.listener(move |_, _, _, cx| {
                        cx.emit(PromptResponse(index));
                    }))
                    .child(action.label().clone()),
            );
        }
        div()
            .size_full()
            .absolute()
            .inset_0()
            .flex()
            .items_center()
            .justify_center()
            .p(px(24.0))
            .bg(rgba(0x00000099))
            .track_focus(&self.focus)
            .on_key_down(cx.listener(|view, event: &KeyDownEvent, _, cx| {
                cx.stop_propagation();
                match event.keystroke.key.as_str() {
                    "escape" => cx.emit(PromptResponse(0)),
                    "enter" if !view.actions.is_empty() => {
                        cx.emit(PromptResponse(view.selected));
                    }
                    "tab" | "left" | "right" if !view.actions.is_empty() => {
                        let backwards =
                            event.keystroke.modifiers.shift || event.keystroke.key == "left";
                        let count = view.actions.len();
                        view.selected =
                            (view.selected + if backwards { count - 1 } else { 1 }) % count;
                        cx.notify();
                    }
                    _ => {}
                }
            }))
            .child(
                div()
                    .w(px(440.0))
                    .max_w_full()
                    .rounded(px(8.0))
                    .border_1()
                    .border_color(rgb(theme::colors().border2))
                    .bg(rgb(theme::colors().panel))
                    .shadow_lg()
                    .overflow_hidden()
                    .child(
                        div()
                            .p(px(20.0))
                            .flex()
                            .gap(px(14.0))
                            .child({
                                let (icon, tint) = self.severity_icon();
                                div()
                                    .flex_shrink_0()
                                    .pt(px(2.0))
                                    .child(assets::icon(icon, 20.0, tint))
                            })
                            .child(
                                div()
                                    .flex_1()
                                    .min_w(px(0.0))
                                    .flex()
                                    .flex_col()
                                    .gap(px(8.0))
                                    .child(
                                        metrics::text_role(div(), metrics::NAME_12_MEDIUM)
                                            .text_color(rgb(theme::colors().text))
                                            .child(self.message.clone()),
                                    )
                                    .children(self.detail.clone().map(|detail| {
                                        metrics::text_role(div(), metrics::BODY_11)
                                            .text_color(rgb(theme::colors().text2))
                                            .child(detail)
                                    })),
                            ),
                    )
                    .child(
                        div()
                            .border_t_1()
                            .border_color(rgb(theme::colors().border))
                            .bg(rgb(theme::colors().bg2))
                            .px(px(20.0))
                            .py(px(14.0))
                            .child(buttons),
                    ),
            )
    }
}
