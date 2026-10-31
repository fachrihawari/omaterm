# omaterm on Windows: regressions and bugs audit, 2026-10-31

## Context
After v0.12.1 (the macOS release), the user asked for the same look at Windows: regressions and bugs. The last Windows fix was v0.10.1 (console windows flashing open).

How the audit was done:
- Four read-only passes over `dev` at 8732df0 (v0.12.1):
  - subprocesses and console windows;
  - sessions, agents and the CLI without the daemon;
  - paths, files and configuration;
  - UI, settings and keys, and what changed since v0.10.1.
- Each pass read mygo v0.6.3 too, where the Windows terminal lives.
- The most important claims were re-checked by reading the code: 1.1, 1.2, 1.3, 1.5, 1.6 and 2.1 below.

Nothing here has run on Windows. CI only runs `go vet` and a cross-build for it, and this machine has no Wine. Each finding is marked:
- **verified**: the code path was read end to end.
- **suspected**: depends on how Windows behaves at runtime; to confirm on Windows.

This file only collects findings. No code changes.

## 0. At a glance

**One root cause explains most of it.** On Windows, `internal/pty` is a stub (`pty_other.go`): `Start` returns `ErrUnsupported`, and `internal/session/registry.go:112-116` lets mygo's terminal run the program itself through its own ConPTY. omaterm then has no handle on the program at all:
- `p.proc` is nil;
- omaterm never sees the output, so OSC 7501/133/9;4, agent detection and the screen scan never run;
- `tick()` returns at once (`pane.go:797`);
- `write()` and `Deliver()` fail;
- `environment()` is never called, and the Shell setting is ignored;
- nothing records the PID, the exit or the exit code.

The comment there says the status "comes from its title alone", but `tick` returns before it reads the title.

So on Windows the features omaterm is built around never worked: agent status, `omaterm send`/`key`/`ask`/`reply`, `wait`, resuming agents, and the hooks. They date from v0.8.0, when panes moved into omaterm's own pty, and Windows was never moved with them.

**Regressions since v0.10.1:**
- **Project processes (v0.11.0) never start:** see 1.1.
- **0.12.0's Web Access tunnel picker, Share and Dev URLs pages show on Windows, and every action fails:** see 2.4.
- **0.12.1's CLI hint** prints a bash line on Windows: see 3.1.
- **0.12.1's cached git path:** Git for Windows installed while omaterm is open isn't seen until a restart (3.6).

**Checked and clean:**
- **No console windows flash.** Every subprocess the app can start on Windows hides its window. The one unhidden `taskkill` (`internal/tunnel/group_windows.go:18`) runs only in a daemon (2.5).
- **macOS-only code stays on macOS:** `wordkeys.go`, Option as Meta, the Dock badge, the app menu, loginenv, dscl, the Command Line Tools check, Tailscale.app.
- **AltGr types characters:** mygo treats Ctrl+Alt as text on Windows, and omaterm binds no Ctrl+Alt shortcut.
- **The control socket works:** AF_UNIX at `%APPDATA%\omaterm\omaterm.sock`, on Windows 10 1803 and later.
- **Updating keeps its folder:** mygo installs to `%LOCALAPPDATA%\Programs\omaterm`, chosen by the app's name, not its identifier.

## 1. Broken

### 1.1 Project processes never start (verified; since v0.11.0)
- `internal/procs/supervisor.go:466-489`:
  - `s.Shell` is never set by the app, so it falls back to `shellcmd.Login()`;
  - on Windows that returns `/bin/sh`: there's no `$SHELL` and no `/etc/passwd` (`internal/shellcmd/shellcmd.go:19-33`);
  - `LoginArgs` then builds `["/bin/sh", "-l", "-c", cmd]`.
- mygo runs `exec.LookPath("/bin/sh")`, which fails.
- **What the user sees:**
  - every process is Failed with `start failed: exec: "/bin/sh": file does not exist`;
  - each autostart process sends a failure notification at every launch;
  - README:220 says processes run on Windows.
- The tests set `s.Shell = "/bin/sh"` themselves (`supervisor_test.go:119`), which hides the default.
- **Fix:** on Windows, `Login()` returns `%ComSpec%` (else `cmd.exe`), and `LoginArgs` returns `{shell, "/d", "/s", "/c", cmd}` for cmd or `{shell, "-NoLogo", "-Command", cmd}` for pwsh. Add a test that passes the OS in.
  - Caveat: mygo quotes each argument with `syscall.EscapeArg`, so a `"` inside a cmd command becomes `\"`, which cmd doesn't understand. pwsh parses it correctly. Making Windows panes ours (§6 step 1) removes the caveat, since omaterm would build the command line.
- Behind it are 1.6 (an exit is never seen) and 2.2 (stopping doesn't end the tree).

### 1.2 Panes get none of omaterm's variables (verified; since v0.8.0)
- `registry.go:115` passes `opts.Env = o.Env`: only `PATH` and `OMATERM_SOCKET` (`terminal.go:215-218`, `processes.go:181`).
- `internal/session/env.go:21` `environment()` never runs. So the panes have:
  - no `OMATERM_PANE`, `OMATERM_SESSION`, `OMATERM_PROJECT(_DIR)` or `OMATERM_KIND`;
  - no `TERM_PROGRAM=ghostty`;
  - the app's own `CLAUDECODE`/`CLAUDE_CODE_*`, left unstripped.
- **What the user sees:**
  - `omaterm hook` returns early (`internal/cli/hook.go:43,52,104`), so no agent conversation is recorded and Resume Agents does nothing;
  - `omaterm split`, `report`, `ask` and `reply` with no target fail with "run this in an omaterm pane";
  - Settings → Agents still says the hook is installed.
- **PATH (suspected):** omaterm sets `PATH=`, but Windows spells it `Path`. mygo's `environment()` matches names case-sensitively (`plugins/terminal/terminal.go:740-748`), so the block gets both entries, and Windows reads the first, the original. omaterm's bin folder would then never reach PATH.
- **Fix:** `opts.Env = environment(o, "")` in that branch, and match variable names without case on Windows. Both come free with §6 step 1.

### 1.3 The `omaterm` command and the agent hooks point at nothing (verified; since v0.8.0)
- `internal/clilink/clilink.go:16-28` makes `BinDir\omaterm` with `os.Symlink`:
  - it has no `.exe`, which cmd and pwsh need to run it;
  - it needs Developer Mode or admin; without either, it fails, and `main.go:62` only logs it.
- `internal/cli/hook.go:306-312` writes hooks that run `BinDir\omaterm.exe`, a file nothing ever creates. That covers:
  - the Claude and Codex hooks;
  - the Antigravity `hooks.json`;
  - the pi and OpenCode plugins.
- **What the user sees:**
  - `omaterm` is "not recognized" in panes, so the skill's commands fail for every agent;
  - each agent start runs a missing executable, a hook error;
  - Settings → Agents says "current", because it compares the command text and never checks the file.
- **Fix:** on Windows, make `BinDir\omaterm.exe` with `os.Link` (a hard link needs no privilege), else copy the exe, and redo it when `!os.SameFile`. Or point the hooks and PATH at `os.Executable()` directly, since the install folder doesn't move.

### 1.4 The CLI prints nothing in pwsh or cmd (verified build flag, suspected effect; since the first Windows release)
- mygo builds the exe with `-H=windowsgui` (`cmd/mygo/build.go:218`). The CLI runs from the same exe (`main.go:129-132`), and nothing attaches to a console.
- **What the user sees:** in an interactive pwsh or cmd, `omaterm ls` prints nothing, the prompt returns at once, and the exit code is lost. Callers that read through pipes still work: Git Bash and agents' shell tools.
- **Fix:** ship a console `omaterm.com` beside `omaterm.exe`, as devenv.com does; PATHEXT finds `.com` first. `AttachConsole(ATTACH_PARENT_PROCESS)` alone fixes the output but not the waiting.
- Needs a mygo build option, or a second `go build` in release.yml.

### 1.5 `omaterm send`, `key` and messages between agents fail (verified; since v0.8.0)
- `pane.go:173-200`: `write()` and `Deliver()` return `io.ErrClosedPipe` when `p.proc == nil`.
- **What the user sees:** "io: read/write on closed pipe".
- **Smallest fix:** fall back to `p.term.Send(b)`. But `omaterm ask` is refused anyway ("no agent runs in X"), because agents are never detected (1.6).

### 1.6 Agent status, `wait`, `ask`, resume and exits (verified; since v0.8.0)
- `tick()` returns at once (`pane.go:797`), and the output is never scanned (`outputLocked` is reached only through `conn`). So:
  - there are no working/waiting/idle marks and no agent names;
  - `omaterm wait` returns after its 1.5 s grace, and `wait --for waiting` hangs;
  - `omaterm ask` fails with "no agent runs in X" (`internal/mail/mail.go:351`), so Ask for Review fails too;
  - Waiting, Done and Error notifications never fire.
- **Exits:** `registry.go:116`'s `OnExit` only calls `paneExited`. `Status = Exited` and `ExitCode` are set only in `Pane.exited()`, which only the pty path calls. So:
  - a project process never turns Exited or Failed, and `restart: always`/`on-failure` never fire (`supervisor.go:531`);
  - `ready()` reports a command that crashes at once as Running.
- **Resume:** `FindAgentPane` (`host.go:139-180`) needs pane PIDs and the process tree, so it never finds the agent's pane, and nothing is resumed after a restart (`restore.go:60`).
- **Ask before closing:** never asks. `runningProgram` (`tabs.go:600-611`) needs the agent or program that 1.6 never fills. That setting was added in v0.9.9 for Windows' Alt+F4.
- **Fix:** §6 step 1. There is no small fix for detection.

## 2. Degraded

### 2.1 Every Windows folder counts as "too broad" (verified; since v0.6.0)
- `service/process.go:572` counts `/` in the path, and Windows paths have none.
- **What the user sees:**
  - a session started in a folder outside every project (`omaterm new -d D:\app`) goes into the project on screen (`host_local.go:495`);
  - the Info panel always says "Not listed for this folder".
- **Fix:** strip `filepath.VolumeName`, count `filepath.Separator`, and list `C:\`, `C:\Users`, `C:\Windows` and `Program Files` as too broad.

### 2.2 Stopping a process may leave its children (suspected; since v0.11.0)
- On Windows, Stop closes mygo's terminal: `ClosePseudoConsole`, then `TerminateProcess` on the direct child after 2 s. There's no Job Object and no `taskkill /T`.
- Children started detached or windowless survive and keep the port, for example Node children started with `windowsHide`. The next start then fails.
- **Fix:** with §6 step 1, omaterm owns the process and puts it in a Job Object with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`.

### 2.3 Paths and links
- **Containment uses `/`** (verified; since v0.8.0). `within()` (`host_local.go:505`) and `worktreeBusy` (`changes.go:228`) use it, so `C:\proj\src` isn't inside `C:\proj`. Fix: the `filepath.Rel` check already in `internal/cli/process.go:222`.
- **Folder comparisons keep case** (suspected; `host_local.go:515`, `sidebar.go:997`, `main.go:109`). `omaterm .` from `c:\proj` adds a second project beside `C:\Proj`.
- **Quick open and search give backslash paths** (verified; since v0.6.0; `service/discover.go:150`, `service/search.go:102-105,172`). The rest of the app uses `/`, so:
  - one file can open in two tabs with two buffers;
  - typing `/` in quick open matches nothing;
  - recent files never show (`overlays.go:151`).
  - Fix: `filepath.ToSlash`, and `rg --path-separator /`.
- **Ctrl+click on Windows paths in a terminal** (verified). mygo's `pathPattern` (`plugins/terminal/links.go:15`) knows neither `\` nor drive letters:
  - `C:\Users\me\src\main.go:12` becomes `main.go:12`, opened relative to the pane's starting folder;
  - `file:///C:/a.go` becomes `/C:/a.go`, so nothing opens.
  - Fix: in mygo, plus `parseFileLink` dropping the `/` before `X:`.
  - Separately, a path outside the project goes to ShellExecute, which runs `.exe`, `.bat` and `.cmd` files rather than showing them (suspected).
- **The cwd isn't followed:** no OSC 7, and `ProcCwd` is a stub. A restored pane reopens in its first folder.

### 2.4 Features that need the daemon show on Windows and fail (verified; regressions from v0.12.0)
- **Web Access:** every control fails with "Web access needs the session daemon, which is off" (`webaccess.go:47-72`, `settings_pages.go:803-922`). That includes the switch, the new tunnel picker, On Your Network and Pair a Device.
- **Dev URLs:** says to turn on "Keep sessions running…" in General (`settings_devurls.go:161-166`), a switch Windows disables. The process dialog's URL field still offers `.localhost`.
- **Share:** the submenu is enabled (`processes.go:776-803`), and picking a tunnel fails with "sharing needs the session daemon, which is off".
- **Keep sessions running:** the switch shows **on**, greyed out, with no reason (`settings_pages.go:246`; `Settings.Daemon` defaults to true). The palette still toggles it, and search finds the systemd row (`settingswin.go:400` filters only `MacOnly`).
- **Fix:**
  - one message, "Not available on Windows yet";
  - disabled controls, or the pages left out;
  - widen `MacOnly` into an OS predicate.

### 2.5 The CLI starts a daemon on Windows that the app never uses (suspected; since v0.8.4)
- `omaterm web on` (`internal/cli/web.go:66`) and `omaterm daemon restart` (`internal/cli/daemon.go:57`) reach `StartProcess` (`internal/daemon/remote.go:266`), which has no Windows guard.
- That daemon's panes have no pty (§0), so its web terminals are blank. Its `taskkill` (`tunnel/group_windows.go:18`) is unhidden, and `detach_windows.go` uses `DETACHED_PROCESS`, so it would flash a console.
- **Fix:** `StartProcess`, `ReloadWeb` and `CallProxy` refuse on Windows.

### 2.6 Shell and commands
- **The Shell setting is ignored:** mygo always picks pwsh, then powershell, `%ComSpec%`, cmd (`registry.go:115`).
- **Sessions are labelled "sh":** `shellcmd.Name(Login())` (`terminal.go:612`, `sidebar.go:778`, `host_local.go:551`).
- **One-line commands fail at LookPath,** e.g. `omaterm new -- "npm run dev"` (`shellcmd.OneLine`).
- **An agent that quits closes its session:** it has no shell around it.
- `.cmd` shims (`claude.cmd`, `npm.cmd`) work when the arguments are split: Go's LookPath uses PATHEXT, and CreateProcess runs `.cmd` through cmd. Arguments with `& | < > ^ %` would be re-read by cmd (BatBadBut). The app passes none today.

### 2.7 Files
- **CRLF becomes LF when the editor saves** (verified; since v0.6.0; also Linux, but Windows hits it constantly).
  - The editor (`editor.go:244`) and Revert Hunk (`changes.go:244`) write `Buffer.String()`, which is `\n`-only. mygo restores the file's line ending only in `Buffer.WriteTo`.
  - With Git for Windows' default autocrlf, every save rewrites every line ending.
  - Fix: write through `WriteTo`, and hash those bytes for `doc.Disk`.
- **Atomic saves can fail** (suspected).
  - Rename over an open file fails, or while Defender scans the temp file. The rename helpers are in `editor.go`, `model.go`, `settings.go`, `uiprefs.go`, `procs/file.go`, `hook.go` and `gitsnap/diff.go`.
  - Settings and ui.json changes are then dropped silently.
  - Fix: one rename helper that retries `ERROR_ACCESS_DENIED` and `ERROR_SHARING_VIOLATION` for about a second.
- **Removing a worktree right after closing its session can fail** (suspected; `worktrees.go:125,286`). The shell still holds the folder; retry for a few seconds.
- **Scrollback is saved only at a clean close** (verified). `p.dirty` is set only by the output scan, so the 10 s save never writes. A crash or shutdown loses it.
- **No process list, ports, CPU or memory:**
  - the Info panel says "Nothing running in this project's terminals" and "No listening ports" (`service/process.go:46,251`);
  - the status bar's CPU and Memory read `/proc` (`service/system_proc.go`);
  - `omaterm wait --port` waits until its timeout.
  - Fix: say "not on Windows yet", or implement them with Toolhelp32, `GetExtendedTcpTable` and `GetSystemTimes`/`GlobalMemoryStatusEx`.

## 3. Cosmetic
1. **`omaterm cli install` and its Settings row** (regression from 0.12.1, 8393435):
   - it makes the useless link from 1.3 in `~\.local\bin`;
   - it then prints "Add this line to ~/.profile: export PATH=…".
   - Fix: on Windows, point at the install folder, or hide the row.
2. **Home isn't shortened to `~`** (`paneheader.go:179`, `cli/cli.go:853`, which check `home+"/"`). Windows shell titles like `C:\…\pwsh.exe` aren't recognised as a shell (`paneheader.go:167`), so they become the header.
3. **Wording that's wrong on Windows:**
   - "Per pane, kept by the session daemon" (`settings_pages.go:559`);
   - `omaterm daemon install-service` says "systemctl not found";
   - `omaterm proxy status` says "open omaterm".
4. **Locations:**
   - state is in `%USERPROFILE%\.local\state\omaterm` rather than `%LOCALAPPDATA%`;
   - the socket and `state.json` are in Roaming `%APPDATA%`;
   - the temp folder is named `omaterm--1` (`Getuid` is -1).
   - Moving any of them needs a migration.
5. **Fonts:**
   - the terminal falls back to JetBrains Mono Nerd Font, so prompt glyphs show as boxes without it;
   - the UI's Inter is usually missing;
   - "Default (the system's monospace)" could name Cascadia Mono.
6. **Git and search:**
   - `gitsnap` caches git's path for the whole run (0.12.1, c147326), so a Git installed meanwhile isn't seen;
   - search results keep a trailing `\r` on CRLF files (`search.go:104`);
   - `omaterm.yml` is rewritten with LF.
7. **Settings and project names:**
   - a `settings.json` with a UTF-8 BOM, as PowerShell 5 writes it, is treated as broken and set aside;
   - a drive root opened as a project is named `\`.
8. **The bundle identifier change (0.12.1):**
   - a fresh install over an old one leaves two "omaterm" entries in Installed apps, and uninstalling either orphans the other;
   - the single-instance key changed too, so a still-open 0.10/0.12.0 window and 0.12.1 could run at once (suspected).
   - Optional fix: on start, delete `HKCU\…\Uninstall\com.example.omaterm` when its `InstallLocation` is this folder.

## 4. Tests and CI
- CI runs only `GOOS=windows go vet ./...` and a cross-build. Nothing would have caught 1.1 or 1.2.
- **A `windows-latest` job** would start like the macOS one: the packages that pass today, then more. The auditors' guess for packages likely to pass as they are:
  - `internal/agent`, `detect`, `osc`, `proto`, `model`, `keys`, `notice`, `crash`, `theme`, `paths`;
  - `procs` (fake runner), `devurl`, `web`, `tunnel`, `client`, `gitsnap`, `worktree`.
- **To add first:** tests that pass the OS into `shellcmd.Login`/`LoginArgs`, and one for the Windows pane environment.
- **Untagged tests that assume Unix**, to tag or skip:
  - `internal/shellcmd` (`/bin/sh`);
  - `internal/session/registry_test.go`;
  - `service/process_test.go` (sleep, python3);
  - `internal/ctl` (socket mode 0600);
  - `internal/cli/hook_test.go` (POSIX quoting);
  - the root package's `host_test.go` and `collab_test.go`.
- **Before the tests:** `git config --global core.autocrlf false`, or the runner's CRLF checkout may break git comparisons.

## 5. To confirm on Windows
- whether `Path` and `PATH` both end up in a pane's environment, and which wins (1.2);
- the CLI in an interactive pwsh: no output, no wait (1.4);
- whether a stopped `npm run dev` leaves `node.exe` behind (2.2);
- atomic saves with Defender on (2.7);
- removing a worktree right after closing its session (2.7);
- two instances across the identifier change (3.8).

## 6. Suggested order
1. **Make Windows panes ours: a ConPTY `internal/pty`.**
   - mygo's terminal takes any connection (`terminal.Options.Conn`). So an `internal/pty/pty_windows.go` puts Windows on the Linux and macOS path, which fixes 1.2, 1.5, 1.6, 2.2, 2.6, the scrollback save and part of 1.1.
   - Base it on mygo's `plugins/terminal/internal/pty/pty_windows.go`: about 260 lines, MIT, credited in THIRD_PARTY_NOTICES.md.
   - Add a Job Object, `Foreground()` from the process tree, and process lookups through Toolhelp32 (name, parent, list). cwd and environment need the PEB, so they can wait.
   - The biggest step, and the one that makes omaterm's core work on Windows.
2. **Processes start:** `shellcmd` on Windows (1.1). Small, and useful even before step 1.
3. **The `omaterm` command:** `omaterm.exe` as a hard link or copy, hooks pointing at it, the CLI install row, and a console `omaterm.com` (1.3, 1.4, 3.1).
4. **Daemon features say "not on Windows yet"** and refuse in the CLI (2.4, 2.5).
5. **Paths:** too broad, containment, slashes in quick open and search, case (2.1, 2.3).
6. **Files:** CRLF on save, rename retries (2.7).
7. **CI:** a `windows-latest` job, as for macOS (§4).
8. **The rest of §3.**
