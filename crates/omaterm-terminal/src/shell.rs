//! Safe, deliberately small shell integration used for prompt readiness and
//! argv submission. Bash is supported for `terminal.run`; other shells remain
//! explicit unsupported operations until their lifecycle hooks are tested.

use std::path::Path;

pub(crate) fn is_bash(program: &str) -> bool {
    Path::new(program)
        .file_name()
        .and_then(|name| name.to_str())
        == Some("bash")
}

pub(crate) fn quote_bash_argument(argument: &str) -> String {
    if argument.is_empty() {
        return "''".into();
    }
    format!("'{}'", argument.replace('\'', "'\\''"))
}

pub(crate) fn encode_bash_argv(argv: &[String]) -> String {
    argv.iter()
        .map(|argument| quote_bash_argument(argument))
        .collect::<Vec<_>>()
        .join(" ")
}

/// Bash `--rcfile` contents. The user's interactive rc file is sourced first,
/// then the private per-session OSC marker is appended to PROMPT_COMMAND.
pub(crate) fn bash_rcfile(token: &str) -> String {
    format!(
        r#"if [[ -r "$HOME/.bashrc" ]]; then source "$HOME/.bashrc"; fi
__omaterm_emit_prompt_ready() {{ printf '\033]133;A;{token}\007'; }}
if [[ $(declare -p PROMPT_COMMAND 2>/dev/null) == "declare -a"* ]]; then
  PROMPT_COMMAND+=(__omaterm_emit_prompt_ready)
else
  PROMPT_COMMAND="${{PROMPT_COMMAND:+${{PROMPT_COMMAND}}; }}__omaterm_emit_prompt_ready"
fi
"#
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn only_bash_is_claimed_as_supported() {
        assert!(is_bash("/bin/bash"));
        assert!(is_bash("bash"));
        assert!(!is_bash("/bin/zsh"));
        assert!(!is_bash("/bin/sh"));
    }

    #[test]
    fn argv_encoding_handles_shell_metacharacters_without_interpolation() {
        let argv = vec![
            "printf".into(),
            "%s|%s|%s|%s\\n".into(),
            "two words".into(),
            "quote'and\\slash".into(),
            "雪☃".into(),
            String::new(),
            "$(touch /tmp/should-not-exist); *".into(),
        ];
        assert_eq!(
            encode_bash_argv(&argv),
            "'printf' '%s|%s|%s|%s\\n' 'two words' 'quote'\\''and\\slash' '雪☃' '' '$(touch /tmp/should-not-exist); *'"
        );
    }

    #[test]
    fn bash_hook_sources_user_config_and_emits_session_marker() {
        let source = bash_rcfile("session-token");
        assert!(source.contains("source \"$HOME/.bashrc\""));
        assert!(source.contains("133;A;session-token"));
        assert!(source.contains("PROMPT_COMMAND+=(__omaterm_emit_prompt_ready)"));
    }
}
