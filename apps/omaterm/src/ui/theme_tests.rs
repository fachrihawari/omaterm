use super::*;

#[test]
fn theme_preference_round_trips_config_and_resolves() {
    assert_eq!(ThemePreference::from_setting(None), ThemePreference::System);
    assert_eq!(
        ThemePreference::from_setting(Some("system")),
        ThemePreference::System
    );
    assert_eq!(
        ThemePreference::from_setting(Some("dark")),
        ThemePreference::Dark
    );
    assert_eq!(
        ThemePreference::from_setting(Some("light")),
        ThemePreference::Light
    );
    // Unknown values follow the system; the config loader warns separately.
    assert_eq!(
        ThemePreference::from_setting(Some("dracula")),
        ThemePreference::System
    );
    for preference in [
        ThemePreference::System,
        ThemePreference::Dark,
        ThemePreference::Light,
    ] {
        assert_eq!(
            ThemePreference::from_setting(Some(preference.as_setting())),
            preference
        );
    }
    assert_eq!(
        ThemePreference::System.resolve(ThemeMode::Light),
        ThemeMode::Light
    );
    assert_eq!(
        ThemePreference::Dark.resolve(ThemeMode::Light),
        ThemeMode::Dark
    );
    assert_eq!(
        ThemePreference::Light.resolve(ThemeMode::Dark),
        ThemeMode::Light
    );
    assert_eq!(
        appearance_mode(gpui::WindowAppearance::VibrantLight),
        ThemeMode::Light
    );
    assert_eq!(
        appearance_mode(gpui::WindowAppearance::VibrantDark),
        ThemeMode::Dark
    );
}

#[test]
fn configured_theme_overrides_platform_appearance() {
    assert_eq!(
        resolve_mode(Some("light"), ThemeMode::Dark),
        ThemeMode::Light
    );
    assert_eq!(
        resolve_mode(Some("dark"), ThemeMode::Light),
        ThemeMode::Dark
    );
    for mode in [ThemeMode::Dark, ThemeMode::Light] {
        assert_eq!(resolve_mode(Some("system"), mode), mode);
        assert_eq!(resolve_mode(None, mode), mode);
    }
}

fn luminance(color: u32) -> f64 {
    let channel = |shift| {
        let value = f64::from((color >> shift) & 255u32) / 255.0;
        if value <= 0.04045 {
            value / 12.92
        } else {
            ((value + 0.055) / 1.055).powf(2.4)
        }
    };
    0.2126 * channel(16) + 0.7152 * channel(8) + 0.0722 * channel(0)
}

#[test]
fn light_text_and_semantic_inks_have_readable_contrast() {
    for foreground in [
        LIGHT.text,
        LIGHT.text2,
        LIGHT.muted,
        LIGHT.blue,
        LIGHT.cyan,
        LIGHT.green,
        LIGHT.yellow,
        LIGHT.orange,
        LIGHT.red,
        LIGHT.purple,
        LIGHT.lime,
        // Editor/diff syntax tokens reuse shared roles: the string, number,
        // and keyword hues are covered above via orange/cyan/purple, but the
        // comment approximation has its own token and needs its own row.
        LIGHT.comment_token,
        LIGHT.active_line_no,
    ] {
        for background in [
            LIGHT.bg,
            LIGHT.bg2,
            LIGHT.panel,
            LIGHT.panel2,
            LIGHT.panel3,
            LIGHT.selected_project_bg,
            LIGHT.tree_selected_bg,
        ] {
            let contrast = (luminance(background) + 0.05) / (luminance(foreground) + 0.05);
            assert!(
                contrast >= 4.5,
                "{foreground:06x} on {background:06x}: {contrast}"
            );
        }
    }
}

#[test]
fn terminal_query_defaults_match_ui_surfaces_in_both_modes() {
    let packed =
        |(r, g, b): (u8, u8, u8)| (u32::from(r) << 16) | (u32::from(g) << 8) | u32::from(b);
    for (ui, terminal) in [
        (&DARK, &omaterm_terminal::DARK_PALETTE),
        (&LIGHT, &omaterm_terminal::LIGHT_PALETTE),
    ] {
        assert_eq!(ui.bg2, packed(terminal.background));
        assert_eq!(ui.terminal_cursor, packed(terminal.cursor));
    }
    assert_eq!(
        LIGHT.text,
        packed(omaterm_terminal::LIGHT_PALETTE.foreground)
    );
}

#[test]
fn alpha_packing_preserves_palette_channels() {
    assert_eq!(with_alpha(DARK.bg2, PANE_HEADER_BG_OPACITY), 0x0F1318E6);
    assert_eq!(with_alpha(LIGHT.bg2, PANE_HEADER_BG_OPACITY), 0xFAFBFDE6);
    assert_eq!(with_alpha(0x123456, 2.0), 0x123456FF);
    assert_eq!(with_alpha(0x123456, -1.0), 0x12345600);
}

#[test]
fn persistent_notice_pairs_are_readable_in_both_themes() {
    for palette in [&LIGHT, &DARK] {
        for (fg, bg) in [
            (palette.warning_text, palette.warning_bg),
            (palette.error_text, palette.error_bg),
        ] {
            let a = luminance(fg);
            let b = luminance(bg);
            assert!((a.max(b) + 0.05) / (a.min(b) + 0.05) >= 4.5);
        }
    }
}
