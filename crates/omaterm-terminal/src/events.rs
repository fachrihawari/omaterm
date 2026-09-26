/// Events produced by feeding PTY output through the terminal engine.
///
/// These are OmaTerm's domain events, translated from
/// `alacritty_terminal::event::Event`. The GPUI layer consumes these;
/// `omaterm-terminal` itself never depends on `gpui`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum TerminalEvent {
    /// OSC title changed (`Event::Title`).
    TitleChanged(String),
    /// Title reset to default (`Event::ResetTitle`).
    TitleReset,
    /// Terminal bell (`Event::Bell`).
    Bell,
    /// New content available, repaint requested (`Event::Wakeup`).
    Wakeup,
    /// Cursor blinking mode changed.
    CursorBlinkingChanged,
    /// Child process exited with the given status.
    ChildExited(std::process::ExitStatus),
    /// Engine requested shutdown (`Event::Exit`).
    ExitRequested,
}
