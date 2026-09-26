use gpui::{
    App, Application, Bounds, Context, FocusHandle, KeyBinding, Window, WindowBounds,
    WindowOptions, actions, div, prelude::*, px, relative, rgb, size,
};
use omaterm_core::{Pane, PaneId, PaneNode, PaneTree, SplitAxis, SplitDirection};

actions!(
    omaterm,
    [
        SplitRight, SplitDown, ClosePane, FocusLeft, FocusRight, FocusUp, FocusDown, ResizeLess,
        ResizeMore, Equalize
    ]
);

struct OmaTerm {
    focus_handle: FocusHandle,
    panes: PaneTree,
    focused_pane: Option<PaneId>,
}

impl OmaTerm {
    fn new(window: &mut Window, cx: &mut Context<Self>) -> Self {
        let focus_handle = cx.focus_handle();
        window.focus(&focus_handle);
        let pane = Pane::empty();
        Self {
            focus_handle,
            panes: PaneTree::new(pane.clone()),
            focused_pane: Some(pane.id),
        }
    }

    fn split(&mut self, direction: SplitDirection, _: &(), _: &mut Window, cx: &mut Context<Self>) {
        let Some(focused) = self.focused_pane else {
            return;
        };
        let pane = Pane::empty();
        if self.panes.split(focused, direction, pane.clone()).is_ok() {
            self.focused_pane = Some(pane.id);
            cx.notify();
        }
    }

    fn close(&mut self, _: &ClosePane, _: &mut Window, cx: &mut Context<Self>) {
        let Some(focused) = self.focused_pane else {
            return;
        };
        if self.panes.remove(focused).is_ok() {
            self.focused_pane = self.panes.panes().first().map(|pane| pane.id);
            cx.notify();
        }
    }

    fn focus(&mut self, direction: SplitDirection, cx: &mut Context<Self>) {
        if let Some(next) = self
            .focused_pane
            .and_then(|focused| self.panes.neighbor(focused, direction))
        {
            self.focused_pane = Some(next);
            cx.notify();
        }
    }

    fn equalize(&mut self, _: &Equalize, _: &mut Window, cx: &mut Context<Self>) {
        self.panes.equalize();
        cx.notify();
    }

    fn resize(&mut self, amount: f32, cx: &mut Context<Self>) {
        let Some(split) = self
            .focused_pane
            .and_then(|pane| self.panes.ancestors(pane))
            .and_then(|ancestors| ancestors.last().copied())
        else {
            return;
        };
        let Some(fraction) = self.panes.split_fraction(split) else {
            return;
        };
        if self.panes.resize(split, fraction + amount).is_ok() {
            cx.notify();
        }
    }

    fn split_right(&mut self, _: &SplitRight, window: &mut Window, cx: &mut Context<Self>) {
        self.split(SplitDirection::Right, &(), window, cx);
    }

    fn split_down(&mut self, _: &SplitDown, window: &mut Window, cx: &mut Context<Self>) {
        self.split(SplitDirection::Down, &(), window, cx);
    }

    fn focus_left(&mut self, _: &FocusLeft, _: &mut Window, cx: &mut Context<Self>) {
        self.focus(SplitDirection::Left, cx);
    }

    fn focus_right(&mut self, _: &FocusRight, _: &mut Window, cx: &mut Context<Self>) {
        self.focus(SplitDirection::Right, cx);
    }

    fn focus_up(&mut self, _: &FocusUp, _: &mut Window, cx: &mut Context<Self>) {
        self.focus(SplitDirection::Up, cx);
    }

    fn focus_down(&mut self, _: &FocusDown, _: &mut Window, cx: &mut Context<Self>) {
        self.focus(SplitDirection::Down, cx);
    }

    fn resize_less(&mut self, _: &ResizeLess, _: &mut Window, cx: &mut Context<Self>) {
        self.resize(-0.05, cx);
    }

    fn resize_more(&mut self, _: &ResizeMore, _: &mut Window, cx: &mut Context<Self>) {
        self.resize(0.05, cx);
    }

    fn render_node(&self, node: &PaneNode) -> gpui::AnyElement {
        match node {
            PaneNode::Pane(pane) => {
                let colors = [0x1D4ED8, 0x7C3AED, 0x0F766E, 0xB45309];
                let color = colors[pane.id.0.as_bytes()[0] as usize % colors.len()];
                let focused = self.focused_pane == Some(pane.id);
                div()
                    .flex()
                    .flex_1()
                    .m_1()
                    .p_3()
                    .bg(rgb(color))
                    .border_2()
                    .border_color(rgb(if focused { 0xFDE047 } else { 0x27272A }))
                    .text_color(rgb(0xFAFAFA))
                    .child(if focused { "Focused pane" } else { "Pane" })
                    .into_any_element()
            }
            PaneNode::Split {
                axis,
                fraction,
                first,
                second,
                ..
            } => {
                let first = self.render_node(first);
                let second = self.render_node(second);
                let first = match axis {
                    SplitAxis::Horizontal => {
                        div().w(relative(*fraction)).h_full().flex().child(first)
                    }
                    SplitAxis::Vertical => {
                        div().h(relative(*fraction)).w_full().flex().child(first)
                    }
                };
                let second = match axis {
                    SplitAxis::Horizontal => div()
                        .w(relative(1.0 - *fraction))
                        .h_full()
                        .flex()
                        .child(second),
                    SplitAxis::Vertical => div()
                        .h(relative(1.0 - *fraction))
                        .w_full()
                        .flex()
                        .child(second),
                };
                let container = div().flex().flex_1().size_full();
                match axis {
                    SplitAxis::Horizontal => container.flex_row(),
                    SplitAxis::Vertical => container.flex_col(),
                }
                .child(first)
                .child(second)
                .into_any_element()
            }
        }
    }
}

impl Render for OmaTerm {
    fn render(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let content = self
            .panes
            .root()
            .map(|root| self.render_node(root))
            .unwrap_or_else(|| {
                div()
                    .size_full()
                    .flex()
                    .items_center()
                    .justify_center()
                    .text_color(rgb(0xA1A1AA))
                    .child("No panes. Use Ctrl+Shift+R/D to split, HJKL to focus, {/} to resize.")
                    .into_any_element()
            });
        div()
            .track_focus(&self.focus_handle)
            .on_action(cx.listener(Self::split_right))
            .on_action(cx.listener(Self::split_down))
            .on_action(cx.listener(Self::close))
            .on_action(cx.listener(Self::focus_left))
            .on_action(cx.listener(Self::focus_right))
            .on_action(cx.listener(Self::focus_up))
            .on_action(cx.listener(Self::focus_down))
            .on_action(cx.listener(Self::resize_less))
            .on_action(cx.listener(Self::resize_more))
            .on_action(cx.listener(Self::equalize))
            .size_full()
            .flex()
            .bg(rgb(0x18181B))
            .child(content)
    }
}

fn main() {
    Application::new().run(|cx: &mut App| {
        cx.bind_keys([
            KeyBinding::new("ctrl-shift-r", SplitRight, None),
            KeyBinding::new("ctrl-shift-d", SplitDown, None),
            KeyBinding::new("ctrl-shift-w", ClosePane, None),
            KeyBinding::new("ctrl-shift-h", FocusLeft, None),
            KeyBinding::new("ctrl-shift-l", FocusRight, None),
            KeyBinding::new("ctrl-shift-k", FocusUp, None),
            KeyBinding::new("ctrl-shift-j", FocusDown, None),
            KeyBinding::new("ctrl-{", ResizeLess, None),
            KeyBinding::new("ctrl-}", ResizeMore, None),
            KeyBinding::new("ctrl-shift-e", Equalize, None),
        ]);
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
                cx.new(|cx| OmaTerm::new(window, cx))
            },
        )
        .expect("failed to open OmaTerm window");

        cx.activate(true);
    });
}
