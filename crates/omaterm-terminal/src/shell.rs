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
/// then private per-session lifecycle hooks are installed.
///
/// Two hooks report through token-authenticated OSC 133 sequences on the PTY
/// stream (consumed by the session parser, never rendered):
///
/// - a `DEBUG` trap (`__omaterm_preexec`) reporting each command about to
///   execute as `ESC ] 133 ; B ; <token> ; <base64-command> BEL`;
/// - `PROMPT_COMMAND` guards (`__omaterm_prompt_first` /
///   `__omaterm_prompt_last`) reporting prompt readiness and the last
///   command's exit status as `ESC ] 133 ; A ; <token> ; <exit> BEL`.
///
/// Exit-status capture must happen in `__omaterm_prompt_first`, the first
/// `PROMPT_COMMAND` element: at that instant `$?` is still the last user
/// command's status by documented bash semantics. Reading it later (in
/// `__omaterm_prompt_last`, or after user `PROMPT_COMMAND` entries) is
/// wrong on bash builds without the 5.3 DEBUG-trap `$?` save/restore
/// quirk — `first()` itself is an assignment (exit 0) and every later
/// element may clobber `$?` (proven by CI on bash 5.2: every status read
/// `Some(0)`).
///
/// Skip semantics (proven against real Bash, see lifecycle tests): our own
/// functions, assignments, and prompt internals never become records — every
/// internal simple command either starts with the `__omaterm_` prefix or
/// runs while `__omaterm_in_prompt` is set. The guards wrap (not replace)
/// the user's `PROMPT_COMMAND` entries, which keep their relative order.
///
/// Explicit semantics: `cmd1; cmd2` yields two starts and one completion
/// (attributed to `cmd2`); loop bodies report per iteration; nested shells
/// without our rcfile are invisible (only the outer invocation is
/// recorded); TUI invocations record the launching command only. Without
/// `base64(1)`, lifecycle emission disables itself and the shell runs
/// normally without journal capture.
pub(crate) fn bash_rcfile(token: &str) -> String {
    format!(
        r#"if [[ -r "$HOME/.bashrc" ]]; then source "$HOME/.bashrc"; fi
__omaterm_token='{token}'
__omaterm_in_prompt=0
__omaterm_exit=0
__omaterm_b64_ok=0
if printf '' | base64 -w0 >/dev/null 2>&1; then __omaterm_b64_ok=1; fi
__omaterm_prompt_first() {{ __omaterm_exit=$?; __omaterm_in_prompt=1; }}
__omaterm_preexec() {{
  case $1 in __omaterm_*) return 0;; esac
  if [[ $__omaterm_in_prompt == 1 ]]; then return 0; fi
  __omaterm_cmd=$1;
  if [[ $__omaterm_b64_ok == 1 ]]; then
    __omaterm_cmd=${{__omaterm_cmd:0:8192}};
    __omaterm_b64=$(printf %s "$__omaterm_cmd" | base64 -w0);
    __omaterm_in_prompt=1;
    printf '\033]133;B;%s;%s\007' "$__omaterm_token" "$__omaterm_b64";
    __omaterm_in_prompt=0;
  fi
}}
__omaterm_prompt_last() {{
  printf '\033]133;A;%s;%s\007' "$__omaterm_token" "$__omaterm_exit";
  __omaterm_in_prompt=0;
}}
trap '__omaterm_preexec "$BASH_COMMAND"' DEBUG
if [[ $(declare -p PROMPT_COMMAND 2>/dev/null) == "declare -a"* ]]; then
  PROMPT_COMMAND=(__omaterm_prompt_first "${{PROMPT_COMMAND[@]}}" __omaterm_prompt_last)
else
  PROMPT_COMMAND="__omaterm_prompt_first; ${{PROMPT_COMMAND:+${{PROMPT_COMMAND}}; }}__omaterm_prompt_last"
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
    fn bash_hook_sources_user_config_and_emits_session_markers() {
        let source = bash_rcfile("session-token");
        assert!(source.contains("source \"$HOME/.bashrc\""));
        // Prompt hook carries the session token and the last-command exit status.
        assert!(source.contains("__omaterm_token='session-token'"));
        assert!(source.contains("133;A;"));
        // Preexec hook reports each command start with its own kind.
        assert!(source.contains("133;B;"));
        assert!(source.contains("trap '__omaterm_preexec \"$BASH_COMMAND\"' DEBUG"));
        // Guards wrap the user's PROMPT_COMMAND in both string and array form.
        assert!(source.contains("__omaterm_prompt_first;"));
        assert!(source.contains("__omaterm_prompt_last"));
        assert!(source.contains("\"${PROMPT_COMMAND[@]}\""));
        // Exit capture is the FIRST statement of the FIRST guard: at that
        // instant $? is still the last user command's status. Reading it any
        // later (last guard, user entries) misreports on bash <= 5.2.
        assert!(source.contains("__omaterm_prompt_first() { __omaterm_exit=$?;"));
        assert!(
            !source.contains("__omaterm_prompt_last() {\n  __omaterm_exit=$?;"),
            "last guard must report the saved status, never re-read $?"
        );
    }

    #[test]
    fn bash_hook_internals_cannot_become_records() {
        // Every internal simple command either carries the skip prefix or
        // runs under the prompt guard: no `local` (which would not match the
        // prefix) and no bare commands outside guarded regions.
        let source = bash_rcfile("session-token");
        assert!(
            !source.contains("local "),
            "locals would not match the __omaterm_ skip prefix"
        );
        assert!(source.contains("case $1 in __omaterm_*)"));
        assert!(source.contains("__omaterm_in_prompt=1;"));
    }
}
