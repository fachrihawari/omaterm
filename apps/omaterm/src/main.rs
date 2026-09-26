use gpui::{
    App, Application, Bounds, Context, Window, WindowBounds, WindowOptions, div, prelude::*, px,
    rgb, size,
};

struct OmaTerm;

impl Render for OmaTerm {
    fn render(&mut self, _window: &mut Window, _cx: &mut Context<Self>) -> impl IntoElement {
        div().size_full().relative().bg(rgb(0x18181B)).child(
            div()
                .absolute()
                .inset_0()
                .flex()
                .items_center()
                .justify_center()
                .text_xl()
                .text_color(rgb(0xFAFAFA))
                .child("OmaTerm"),
        )
    }
}

fn main() {
    Application::new().run(|cx: &mut App| {
        cx.on_window_closed(|cx| {
            if cx.windows().is_empty() {
                cx.quit();
            }
        })
        .detach();

        let bounds = Bounds::centered(None, size(px(960.0), px(640.0)), cx);
        cx.open_window(
            WindowOptions {
                window_bounds: Some(WindowBounds::Windowed(bounds)),
                ..Default::default()
            },
            |window, cx| {
                window.set_window_title("OmaTerm");
                cx.new(|_| OmaTerm)
            },
        )
        .expect("failed to open OmaTerm window");

        cx.activate(true);
    });
}
