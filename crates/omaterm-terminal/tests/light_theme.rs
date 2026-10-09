use omaterm_terminal::{
    AlacrittyEngine, TermColor, TerminalEngine, ThemeMode, initialize_theme, terminal_palette,
};

#[test]
fn light_session_reports_its_painted_palette_and_preserves_explicit_colors() {
    initialize_theme(ThemeMode::Light);
    let palette = terminal_palette();
    let mut engine = AlacrittyEngine::new(80, 24);
    for (slot, (r, g, b)) in [
        (10, palette.foreground),
        (11, palette.background),
        (12, palette.cursor),
        (4, palette.ansi[1]),
    ] {
        let query = if slot == 4 {
            "\x1b]4;1;?\x07".to_owned()
        } else {
            format!("\x1b]{slot};?\x07")
        };
        let reply = engine.advance_output(query.as_bytes());
        let expected = format!("rgb:{r:02x}{r:02x}/{g:02x}{g:02x}/{b:02x}{b:02x}");
        assert!(String::from_utf8_lossy(&reply.reply_bytes).contains(&expected));
    }
    engine.advance_output(b"\x1b[31mR\x1b[38;2;1;2;3mX");
    let viewport = engine.viewport();
    let (r, g, b) = palette.ansi[1];
    assert_eq!(viewport.rows[0].cells[0].fg, TermColor::Rgb(r, g, b));
    assert_eq!(viewport.rows[0].cells[1].fg, TermColor::Rgb(1, 2, 3));
    engine.advance_output(b"\x1b]11;#123456\x07");
    let reply = engine.advance_output(b"\x1b]11;?\x07");
    assert!(String::from_utf8_lossy(&reply.reply_bytes).contains("rgb:1212/3434/5656"));
    engine.advance_output(b"\x1b]111\x07");
    let reply = engine.advance_output(b"\x1b]11;?\x07");
    assert!(String::from_utf8_lossy(&reply.reply_bytes).contains("rgb:fafa/fbfb/fdfd"));
    engine.advance_output(b"\x1b]12;#123456\x07");
    assert_eq!(engine.viewport().cursor_color, (0x12, 0x34, 0x56));
    engine.advance_output(b"\x1b]112\x07");
    assert_eq!(engine.viewport().cursor_color, palette.cursor);
}
