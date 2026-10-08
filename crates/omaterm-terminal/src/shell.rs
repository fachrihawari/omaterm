//! Safe, deliberately small shell integration used for prompt readiness and
//! argv submission. Bash is supported for `terminal.run`; other shells remain
//! explicit unsupported operations until their lifecycle hooks are tested.

use std::path::{Path, PathBuf};

pub(crate) fn is_bash(program: &str) -> bool {
    matches!(
        shell_file_name(program),
        Some(name) if name.eq_ignore_ascii_case("bash") || name.eq_ignore_ascii_case("bash.exe")
    )
}

/// Git for Windows' launcher is `bash.exe`. A Unix `bash` is not.
pub(crate) fn is_windows_bash(program: &str) -> bool {
    shell_file_name(program).is_some_and(|name| name.eq_ignore_ascii_case("bash.exe"))
}

/// How to start Bash so the lifecycle rcfile actually loads.
///
/// MSYS `bash.exe` exits at once with `--: invalid option` when `--login` is
/// combined with `--rcfile`, and also when the `--rcfile` value contains a
/// slash, backslash, or colon. The Windows launch therefore passes only the
/// file name and starts in that file's directory. The rcfile itself moves to
/// the requested working directory. A Unix `bash` keeps the absolute path and
/// the requested directory.
pub(crate) struct BashShellLaunch {
    pub args: Vec<String>,
    pub directory: PathBuf,
}

pub(crate) fn bash_shell_launch(
    program: &str,
    rc_file: &Path,
    working_directory: &Path,
) -> BashShellLaunch {
    if is_windows_bash(program) {
        let directory = rc_file
            .parent()
            .filter(|path| !path.as_os_str().is_empty())
            .unwrap_or(working_directory)
            .to_path_buf();
        let name = rc_file
            .file_name()
            .map(|name| name.to_string_lossy().into_owned())
            .unwrap_or_else(|| rc_file.to_string_lossy().into_owned());
        BashShellLaunch {
            // `-i` before `--rcfile` makes MSYS bash exit with
            // `--: invalid option`. The file name has to come first.
            args: vec!["--rcfile".to_string(), name, "-i".to_string()],
            directory,
        }
    } else {
        BashShellLaunch {
            args: vec![
                "--rcfile".to_string(),
                rc_file.to_string_lossy().into_owned(),
            ],
            directory: working_directory.to_path_buf(),
        }
    }
}

/// Directory string Git Bash can `cd` to. Drive paths become `/c/...`.
#[cfg(windows)]
pub(crate) fn bash_directory(path: &Path) -> String {
    let mut raw = path.to_string_lossy().replace('/', "\\");
    if let Some(rest) = raw.strip_prefix(r"\\?\UNC\") {
        raw = format!(r"\\{rest}");
    } else if let Some(rest) = raw.strip_prefix(r"\\?\") {
        raw = rest.to_string();
    }
    if raw.len() >= 3 && raw.as_bytes()[1] == b':' && raw.as_bytes()[2] == b'\\' {
        let drive = (raw.as_bytes()[0] as char).to_ascii_lowercase();
        return format!("/{drive}{}", raw[2..].replace('\\', "/"));
    }
    if let Some(rest) = raw.strip_prefix(r"\\") {
        return format!("//{}", rest.replace('\\', "/"));
    }
    raw.replace('\\', "/")
}

fn shell_file_name(program: &str) -> Option<&str> {
    Path::new(program)
        .file_name()
        .and_then(|name| name.to_str())
}

/// `$SHELL` is the preferred program only when this process can start it.
/// An MSYS path such as `/usr/bin/bash` is not a Win32 program.
pub(crate) fn accept_shell_env(value: &str) -> bool {
    if value.is_empty() {
        return false;
    }
    #[cfg(windows)]
    {
        if value.starts_with('/') {
            return false;
        }
        let path = Path::new(value);
        if path.is_absolute() && !path.is_file() {
            return false;
        }
    }
    true
}

/// Shells a Windows user can pick from inside the app. The id is what
/// `config.toml` stores; the program is resolved when a terminal starts.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum WindowsShell {
    Powershell,
    Cmd,
    GitBash,
}

impl WindowsShell {
    pub const ALL: [Self; 3] = [Self::Powershell, Self::Cmd, Self::GitBash];

    pub const fn id(self) -> &'static str {
        match self {
            Self::Powershell => "powershell",
            Self::Cmd => "cmd",
            Self::GitBash => "git-bash",
        }
    }

    pub const fn label(self) -> &'static str {
        match self {
            Self::Powershell => "PowerShell",
            Self::Cmd => "Command Prompt",
            Self::GitBash => "Git Bash",
        }
    }

    pub fn parse(value: &str) -> Option<Self> {
        Self::ALL.into_iter().find(|shell| shell.id() == value)
    }

    pub fn resolve(self) -> Result<ResolvedShell, ShellResolveError> {
        let program = match self {
            Self::Powershell => "powershell.exe".to_string(),
            Self::Cmd => "cmd.exe".to_string(),
            Self::GitBash => installed_git_bash()
                .map(|path| path.to_string_lossy().into_owned())
                .ok_or(ShellResolveError::GitBashNotFound)?,
        };
        Ok(ResolvedShell {
            id: self.id(),
            program,
        })
    }
}

/// Program that `PtyProcess` can `CreateProcess`, plus the config id.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ResolvedShell {
    pub id: &'static str,
    pub program: String,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ShellResolveError {
    /// `git-bash` was chosen, but Git for Windows' `bin\bash.exe` is absent.
    GitBashNotFound,
}

impl std::fmt::Display for ShellResolveError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::GitBashNotFound => write!(
                f,
                "Git Bash was not found. Install Git for Windows, or pick PowerShell or Command Prompt"
            ),
        }
    }
}

/// `choice` is a saved id. `None` is Windows PowerShell, including when
/// `$SHELL` is an MSYS path that cannot be started.
pub fn resolve_windows_shell(choice: Option<&str>) -> Result<ResolvedShell, ShellResolveError> {
    let shell = choice
        .map(WindowsShell::parse)
        .unwrap_or(Some(WindowsShell::Powershell))
        .unwrap_or(WindowsShell::Powershell);
    shell.resolve()
}

/// True when both strings name the same executable. A full path matches the
/// bare file name (`bash.exe` and `C:\Program Files\Git\bin\bash.exe`).
#[must_use]
pub fn same_shell_program(left: &str, right: &str) -> bool {
    if left.eq_ignore_ascii_case(right) {
        return true;
    }
    let left_name = Path::new(left).file_name().and_then(|name| name.to_str());
    let right_name = Path::new(right).file_name().and_then(|name| name.to_str());
    matches!(
        (left_name, right_name),
        (Some(left), Some(right)) if left.eq_ignore_ascii_case(right)
    )
}

/// A palette shell needs a new tab when no live session already uses it.
#[must_use]
pub fn shell_tab_needed<'a>(
    live_programs: impl IntoIterator<Item = &'a str>,
    requested: &str,
) -> bool {
    !live_programs
        .into_iter()
        .any(|program| same_shell_program(program, requested))
}

/// Absolute programs must exist. A bare name (`powershell.exe`) is left for
/// `PATH` lookup. Control characters and empty strings are unusable.
#[must_use]
pub fn shell_program_usable(program: &str) -> bool {
    if program.is_empty() || program.chars().any(char::is_control) {
        return false;
    }
    let path = Path::new(program);
    if path.is_absolute() {
        path.is_file()
    } else {
        !program.contains(['/', '\\'])
    }
}

fn installed_git_bash() -> Option<std::path::PathBuf> {
    let mut roots = Vec::new();
    for key in ["ProgramFiles", "ProgramFiles(x86)"] {
        if let Some(root) = std::env::var_os(key) {
            roots.push(Path::new(&root).join("Git"));
        }
    }
    if let Some(local) = std::env::var_os("LOCALAPPDATA") {
        roots.push(Path::new(&local).join("Programs").join("Git"));
    }
    let path_dirs: Vec<std::path::PathBuf> = std::env::var_os("PATH")
        .map(|value| std::env::split_paths(&value).collect())
        .unwrap_or_default();
    git_bash_from(roots, &path_dirs)
}

pub(crate) fn git_bash_from(
    roots: impl IntoIterator<Item = std::path::PathBuf>,
    path_dirs: &[std::path::PathBuf],
) -> Option<std::path::PathBuf> {
    for root in roots {
        let bash = root.join("bin").join("bash.exe");
        if bash.is_file() {
            return Some(bash);
        }
    }
    for dir in path_dirs {
        let git = dir.join("git.exe");
        if !git.is_file() {
            continue;
        }
        let mut current = git.parent();
        for _ in 0..4 {
            let Some(dir) = current else {
                break;
            };
            let bash = dir.join("bin").join("bash.exe");
            if bash.is_file() && dir.join("cmd").join("git.exe").is_file() {
                return Some(bash);
            }
            current = dir.parent();
        }
    }
    None
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
        assert!(is_bash(r"C:\Program Files\Git\bin\bash.exe"));
        assert!(is_bash("bash.exe"));
        assert!(!is_bash("/bin/zsh"));
        assert!(!is_bash("/bin/sh"));
        assert!(!is_bash("powershell.exe"));
        assert!(!is_bash("cmd.exe"));
    }

    #[test]
    fn msys_shell_env_is_not_a_windows_program() {
        assert!(!accept_shell_env(""));
        assert!(accept_shell_env("powershell.exe"));
        #[cfg(windows)]
        {
            assert!(!accept_shell_env("/usr/bin/bash"));
            assert!(!accept_shell_env(r"C:\missing\bash.exe"));
        }
    }

    #[test]
    fn git_bash_is_the_install_launcher_not_a_path_neighbor() {
        let root = std::env::temp_dir().join(format!("omaterm-gitbash-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        let install = root.join("Git");
        std::fs::create_dir_all(install.join("bin")).unwrap();
        std::fs::create_dir_all(install.join("cmd")).unwrap();
        std::fs::create_dir_all(install.join("mingw64").join("bin")).unwrap();
        std::fs::write(install.join("bin").join("bash.exe"), b"").unwrap();
        std::fs::write(install.join("cmd").join("git.exe"), b"").unwrap();
        std::fs::write(install.join("mingw64").join("bin").join("bash.exe"), b"").unwrap();
        let found = git_bash_from([], &[install.join("cmd")]).unwrap();
        assert_eq!(found, install.join("bin").join("bash.exe"));
        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn windows_shell_ids_round_trip() {
        assert_eq!(WindowsShell::parse("git-bash"), Some(WindowsShell::GitBash));
        assert_eq!(WindowsShell::parse("zsh"), None);
    }

    #[cfg(windows)]
    #[test]
    fn windows_bash_rcfile_is_only_the_file_name() {
        let launch = bash_shell_launch(
            r"C:\Program Files\Git\bin\bash.exe",
            Path::new(r"C:\Users\USER\AppData\Local\Temp\omaterm-bashrc-1-abc"),
            Path::new(r"C:\Users\USER\Documents\Projects\omaterm"),
        );
        assert_eq!(
            launch.args,
            vec![
                "--rcfile".to_string(),
                "omaterm-bashrc-1-abc".to_string(),
                "-i".to_string()
            ]
        );
        assert!(launch.directory.ends_with("Temp"));
        assert!(
            !launch
                .args
                .iter()
                .any(|arg| arg == "--login" || arg.contains(':'))
        );
    }

    #[test]
    fn unix_bash_keeps_its_rcfile_path_and_directory() {
        let launch = bash_shell_launch(
            "/bin/bash",
            Path::new("/tmp/omaterm-bashrc-1-abc"),
            Path::new("/home/user/proj"),
        );
        assert_eq!(
            launch.args,
            vec![
                "--rcfile".to_string(),
                "/tmp/omaterm-bashrc-1-abc".to_string()
            ]
        );
        assert_eq!(launch.directory, PathBuf::from("/home/user/proj"));
    }

    #[cfg(windows)]
    #[test]
    fn git_bash_directory_uses_a_posix_drive_path() {
        assert_eq!(
            bash_directory(Path::new(r"C:\Users\USER\Documents\My Project")),
            "/c/Users/USER/Documents/My Project"
        );
        assert_eq!(
            bash_directory(Path::new(r"\\server\share\repo")),
            "//server/share/repo"
        );
        assert_eq!(
            bash_directory(Path::new(r"\\?\C:\Users\USER")),
            "/c/Users/USER"
        );
    }

    #[test]
    fn saved_shell_none_is_powershell() {
        assert_eq!(
            resolve_windows_shell(None).unwrap().program,
            "powershell.exe"
        );
        assert_eq!(
            resolve_windows_shell(Some("cmd")).unwrap().program,
            "cmd.exe"
        );
    }

    #[test]
    fn shell_tab_opens_only_when_that_program_is_not_live() {
        let git = r"C:\Program Files\Git\bin\bash.exe";
        assert!(shell_tab_needed(std::iter::empty(), git));
        assert!(!shell_tab_needed([git], git));
        assert!(!shell_tab_needed(["bash.exe"], git));
        assert!(shell_tab_needed(["powershell.exe"], git));
        assert!(shell_program_usable("powershell.exe"));
        assert!(!shell_program_usable(""));
        assert!(!shell_program_usable("bad\nname"));
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
