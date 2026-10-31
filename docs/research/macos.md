# omaterm on macOS: quality and function audit, 2026-10-31

## Context
On 2026-10-31 the user found on a Mac that the Info panel showed no processes and no ports. The cause: every process lookup read Linux's `/proc`. That is fixed on `release/0.12.1` (a4e80cd), along with dev servers that listen on `::1` only (0cc1eaa). The user then asked for a wider look at macOS quality and function.

How the audit was done:
- Four read-only passes over the code at 0cc1eaa: stubs and OS gates; launch, daemon and environment; UI and platform conventions; tests, CI and agents.
- `GOOS=darwin go vet ./...`.
- The most important claims were re-checked by reading the code.

Nothing here has run on a Mac. CI runs only on Linux; release.yml only builds the macOS bundle. Each finding is marked:
- **verified**: the code path was read end to end.
- **suspected**: depends on how macOS behaves at runtime; to confirm on a Mac.

This file only collects findings. No code changes.

---

## 0. At a glance

| Area | State on macOS |
| --- | --- |
| PTY, sockets, file watching (FSEvents through mygo), ⌘C/⌘V/⌘A, IME, dead keys, retina, fullscreen | Fine |
| Process lookups: Info panel, a pane's program, agent hooks finding their pane | Fixed on `release/0.12.1`, not yet run on a Mac |
| Tools the app and daemon run themselves (tailscale, cloudflared, certutil, gh, rg, agents) and project processes | Broken when launched from Finder/Dock: launchd's PATH, no login environment |
| Notifications while the window is closed | Broken: the daemon only knows `notify-send` |
| Dev URLs | Probably broken: binding `127.0.0.1:443` likely needs root on macOS, and the checklist says OK regardless |
| Terminal keys | Degraded: Option isn't Meta (⌥B/⌥F type ∫ ƒ); ⌘←/⌘→/⌘⌫ do nothing |
| First run and updates | Gatekeeper blocks the first launch (not notarized); an ad-hoc signature changes every build |
| Running the tests on a Mac | Unsafe: tests can't isolate the config dir and would touch the developer's real state.json and trust.json |

---

## 1. Broken

**1.1 No login environment for the app and the daemon.** Verified.
- Opened from Finder or the Dock, the app gets launchd's `PATH=/usr/bin:/bin:/usr/sbin:/sbin`. Nothing imports the user's shell environment (no `$SHELL -l -i -c env` anywhere, mygo included). The daemon inherits that environment (`internal/daemon/remote.go:293`). Panes are fine, because they start login shells.
- Lookups that miss Homebrew, `~/.local/bin`, nvm and the like:
  - `tailscale` (`internal/tunnel/tailscale.go:16,159`, `serves.go:38`, `internal/web/pair.go:59`) and `cloudflared` (`internal/tunnel/cloudflare.go:36,53`): the tunnels say "not installed".
  - `certutil` (`internal/devurl/setup.go:109`, `checks.go:62`).
  - `gh` (`internal/worktree/worktree.go:523`): Create PR opens a web page instead.
  - `rg` (`service/search.go:30`): search uses the slow fallback.
  - The agents (`worktreedialogs.go:94`): New Agent always suggests "claude".
- Project processes run `zsh -l -c` (`internal/shellcmd/shellcmd.go:72-77` through `internal/procs/supervisor.go:489`). A non-interactive login shell skips `~/.zshrc`, where nvm, mise and asdf usually load, so `npm run dev` gives "command not found" when the app was opened from the Dock. It works when the app was started from a terminal.
- Fix: at startup on darwin (app and daemon), run the login shell once with a timeout (`$SHELL -l -i -c 'env -0'`) and merge its PATH, or its whole environment, into the process environment before anything looks a tool up. Ghostty, VS Code and Zed do the same. Separately, try `/Applications/Tailscale.app/Contents/MacOS/Tailscale` when `tailscale` isn't on PATH (suspected need).

**1.2 No notifications while the window is closed.** Verified.
- `internal/daemon/notify.go:170-176` only runs `notify-send`. With the app quit, waiting, done and error, failed processes and OSC 9/777 show nothing. This matters more on macOS, because ⌘Q quits silently while sessions keep running (see 3.3).
- Fix: on darwin, `osascript -e 'display notification …'` (clicking it can't open the session), or start the app hidden with `--session` to notify through its own notifier.

**1.3 Dev URLs may never start.** Suspected, high confidence.
- The proxy binds `127.0.0.1:443` (`internal/daemon/devurl.go:26`). Since 10.14, macOS lets programs bind ports below 1024 without root only on the wildcard address, not on a specific one.
- `portCheck` reports OK on darwin unconditionally (`internal/devurl/checks.go:111`), so the checklist goes green while the proxy fails.
- Fix: confirm on a Mac first. Then either bind `:443` and refuse peers that aren't loopback (this may bring up the firewall's "accept incoming connections" dialog), or bind through a small root step. Either way, test-bind in `portCheck`.
- Related, suspected: `security add-trusted-cert -d` run as root through `osascript … with administrator privileges` (`internal/devurl/system.go:139`) can fail on macOS 11+ with "no user interaction was possible".

**1.4 Gatekeeper on first launch.** Verified.
- `mygo.json` has no signing identity or notarization, and release.yml imports no certificate. The app opened from the DMG is blocked ("Open Anyway" in Privacy & Security). RELEASING.md:80 says so.
- The ad-hoc signature gets a new hash every build, so privacy grants (Documents, Desktop, Downloads, which panes trigger) are likely asked for again after each update. Suspected.
- Fix: a Developer ID certificate plus notarization in release.yml. This needs an Apple Developer account: the user's decision.

**1.5 Tests on a Mac touch the developer's real files.** Verified.
- On darwin, `os.UserConfigDir` is `~/Library/Application Support` and ignores `XDG_CONFIG_HOME`. `paths.ConfigDir()` (`internal/paths/paths.go:33`) uses it.
- So the ~30 `t.Setenv("XDG_CONFIG_HOME", …)` calls, `main_test.go:18`, `internal/daemon/main_test.go:38`, and the env blocks in `scripts/web-check.mjs:28` and `scripts/site-web-shots.mjs:25` isolate nothing there.
- Daemons started without a StateFile (`internal/daemon/contract_test.go:176`, `share_test.go:325`) and `procs.TrustPath()` (`internal/daemon/daemon.go:218`) would read and write the real `state.json` and `trust.json`.
- Safe today: `internal/pty`, `service`, `internal/procs`, `internal/devurl`.
- Fix: an `OMATERM_CONFIG_DIR` override in `paths` that tests and scripts set, or a temporary `HOME` in each TestMain.

## 2. Degraded

**Terminal input**
- **Option as Meta** (verified). omaterm never sets mygo's `OptionAsAlt` (`terminal.go:234-268`, `internal/daemon/remote.go:734`). ⌥B, ⌥F, ⌥D and ⌥. type ∫ ƒ ∂ ≥ instead of moving or deleting by word, and agents' Meta keys don't work. Only ⌥←/⌥→ are mapped (`internal/session/wordkeys.go:15`). Fix: an "Option as Meta" setting (left, right, both, off), as Ghostty, iTerm and Terminal.app have.
- **⌘←, ⌘→, ⌘⌫ do nothing** (verified). mygo hands ⌘ keys to the app (`plugins/terminal/view.go:632`), and the app binds none of these. Fix: in `wordKey` on darwin, send `\x01`, `\x05` and `\x15`, as Terminal.app and iTerm do. This is terminal input, not a new app shortcut.

**Sockets and the CLI**
- **No peer check on darwin** (verified). `internal/peer/peer_other.go:8` returns -1, so the uid checks (`internal/ctl/server.go:110`, `internal/client/client.go:67`) never run. Only the socket's 0600 mode guards it. Fix: `peer_darwin.go` with `getsockopt(SOL_LOCAL, LOCAL_PEERCRED)` or `getpeereid` through purego. On every OS, `attach()` (`internal/daemon/remote.go:868`) checks no peer either.
- **The daemon socket depends on the caller's environment** (verified path; whether it bites depends on the user's setup). `DaemonSocketPath()` follows `XDG_RUNTIME_DIR` (`internal/paths/paths.go:56-90`). The Finder-launched app lacks it; a shell that sets it in `.zshrc` computes another path. Then `omaterm daemon status`, hooks and the OpenCode service see "not running" while the window is closed. Panes only get `OMATERM_SOCKET`, the app's socket (`terminal.go:217`). Fix: ignore XDG on darwin, or give panes `OMATERM_DAEMON_SOCKET`.
- **The command-line tool goes to `~/.local/bin`** (verified). `internal/cli/cli.go:1077` and `internal/cli/settings.go:18` link it there, which isn't on macOS's PATH, so "Installed" shows and Terminal.app says command not found. Fix: offer `/usr/local/bin` (elevated) or say what to add to `~/.zprofile`.
- **Links into a temporary location** (suspected). Started from the DMG or a translocated copy, `main.go:46` and `internal/clilink/clilink.go:13` link to `/Volumes/…` or `…/AppTranslocation/…`, and the daemon runs from there too. Both disappear. Fix: detect it and ask to move the app to /Applications.
- **`omaterm <dir>` starts the app as a child of the terminal** (suspected). `internal/cli/settings.go:70` doesn't detach it, so closing that terminal ends the app. Fix: `open -a` on darwin.

**Daemon and app lifecycle**
- **No start at login** (verified). Only a systemd unit exists (`internal/daemon/service.go:21-66`). The row is rightly hidden on macOS (`settings_pages.go:239,1157`), but `omaterm daemon install-service` says "systemctl not found", and a settings-search row may surface with no control behind it (`settings.go:328`). Fix: a LaunchAgent plist with `launchctl bootstrap gui/$UID`, or mygo's `SetOpenAtLogin` (SMAppService).
- **No Dock badge** (verified). The "needs you" count only goes into the window title (`status.go:117`, `notify.go:124`). `mygo.App.SetBadgeCount` exists. Fix: set it next to the title.
- **Updating in place needs write access next to the bundle** (verified in mygo's `updater.go:157-298`). Non-admin users in /Applications, and copies run from the DMG, can't update.

**Elevation and Dev URLs messages**
- **osascript errors are dropped** (verified). `internal/devurl/elevate.go:25-28` discards stdout and stderr, so cancelling shows "The system step failed: exit status 1" instead of Cancelled (error -128), and real failures give no detail (`settings_devurls.go:82`). The dialog also says "osascript wants to make changes". Fix: `CombinedOutput`, map -128 to `ErrCancelled`, add `with prompt "omaterm …"`.

**Smaller degraded items**
- **Shell fallback** (verified, rare). `internal/shellcmd/shellcmd.go:31` reads `/etc/passwd`, which has no real users on macOS. With `SHELL` unset (a daemon started over ssh or by launchd), panes run `/bin/sh` without Homebrew's PATH. Fix: `dscl . -read /Users/$USER UserShell`, else `/bin/zsh`.
- **git without the Command Line Tools** (suspected). `service/git.go:68,212,414`, `service/gitsummary.go:26` and `internal/worktree/git.go:15` call `git` directly, skipping the stub check in `internal/gitsnap/git.go:43`. The install dialog may keep popping up. Fix: go through `gitsnap.command()`.
- **Paths that open nothing** (verified). `terminal.go:311` builds `"file://" + path` unescaped; NSURL returns nil for spaces, `%` or `#`. Fix: `mygo.Shell.OpenPath`.
- **The CPU and Memory toggle does nothing** (verified). It is on by default (`settings.go:352`), but the stats only come from `/proc` (`service/system.go:25`). Fix: sample through sysctl and `host_statistics64`, or hide the row on darwin.
- **Watching a project rooted at `~`** (suspected). `service/discover.go:171` `IgnoreFunc` doesn't apply `Protected()`, so a snapshot can scan `~/Library`, Desktop and Documents, which trigger privacy prompts.

## 3. Cosmetic

**3.1 Bundle identifier `com.example.omaterm`** (`mygo.json:3`, verified). macOS keys notification permission, preferences and the Keychain on it, so changing it later loses them. Choose the real ID before the next macOS-facing release. The user's decision.

**3.2 Menu bar** (`main.go:36-40`, verified). It is mygo's default app menu. There is no "Settings…" item for ⌘, (HIG), no "Check for Updates…" and no Help menu. The Window menu has only Minimize, Zoom and Bring All to Front.

**3.3 Quitting with the daemon on** (`tabs.go:350-388`, verified). ⌘Q and the close button quit with no word that sessions keep running, and on macOS nothing notifies afterwards (1.2). Fix: one line in the quit path the first time.

**3.4 Linux wording shown on macOS** (verified):
- Dev URLs:
  - "through pkexec" (`internal/devurl/system.go:84`)
  - "libnss3-tools on Debian" instead of `brew install nss` (`setup.go:113-116`)
  - "polkit" (`elevate.go:19`)
  - "Trusted by Chrome and Firefox" (only Firefox's databases are checked on a Mac, `checks.go:60`)
  - "the setting that lets programs use port 443" (`settings_devurls.go:391`)
- "Install webkit2gtk-4.1…" as the browser pane's fallback error (`browser.go:255`, suspected).
- "Theme: Follow Omarchy" in the palette (`commands.go:78`) and the theme caption (`settings_pages.go:349`), though the Omarchy tile is hidden.
- "Follow Omarchy (JetBrainsMono Nerd Font)" in the font popup (`settings_pages.go:441`), which renders SF Mono on a Mac.

**3.5 Window.** The default title bar follows the system's appearance, not omaterm's theme (`main.go:164-173`), so a dark theme on a light Mac gets a light bar. Settings is a child window (`settingswin.go:125`) and moves with the main window (suspected). FocusFollowsMouse is on by default (`settings.go:127`), a Hyprland habit.

**3.6 Naming.**
- The Info panel shows native Claude Code as its version ("2.1.3"): its executable is named by version under `~/.local/share/claude/versions/`. `service/process.go` should fall back to `filepath.Base(args[0])`, which is "claude". Pane detection is unaffected: `agent.Detect` reads argv.
- Homebrew's Python runs as `Python` (capital P), which `internal/session/pane.go:797` doesn't treat as a script runtime.

**3.7 Other cosmetic items.**
- **Paths.** Config is in `~/Library/Application Support/omaterm`, but state is in `~/.local/state/omaterm` (`internal/paths/paths.go:31-50`). README.md:413 gives `~/.config/omaterm/settings.json`, which is wrong on a Mac.
- **Ghostty font.** `fonts.go:46` reads only `~/.config/ghostty/config`, not `~/Library/Application Support/com.mitchellh.ghostty/config`.
- **Dev URLs checks.** The `*.localhost` check (`internal/devurl/checks.go:89`) may go red falsely on macOS's resolver, though browsers resolve these names themselves (suspected). Uninstalling (`system.go:211`) removes the trust setting but leaves the certificate in System.keychain.
- **Agent hooks.** `internal/session/host.go:174` compares folders literally, but `/tmp` and `/var` are symlinks and APFS is case-insensitive, so the folder-based guess for a hook can miss.

## 4. Tests and CI

- **One package doesn't compile for darwin: `internal/session`'s tests.** `startExec` and `waitFor` live in `session_test.go` (tagged linux) but are used by `keys_test.go`, `paste_test.go` and `summary_test.go`. Nothing else in `session_test.go` is Linux-only. Fix: tag it `linux || darwin`.
- **CI can't catch darwin compile errors in tests.** Its cross-compile step runs `go build .`, which skips test files. Adding `GOOS=darwin go vet ./...` to the ubuntu job catches them for free.
- **Socket paths too long on a Mac** (macOS allows 103 bytes; `t.TempDir()` there is ~49 characters plus the test name):
  - `TestDaemonProcessAutostartSyncAndLocal` (`internal/daemon/process_test.go:125`)
  - `fakeSocket` in `internal/cli/process_test.go:38` for the Share and ps tests
  - Fix: `os.MkdirTemp("", "omx")`, as `startDaemon` already does.
- **The leak test proves nothing on a Mac.** `internal/daemon/processleak_test.go:25` counts a failed `/proc/<pid>/stat` read as "ended".
- **Tests that could now run on darwin:**
  - `internal/session/agentpane_test.go:63` skips with "process lookups are Linux's".
  - `internal/pty/pty_test.go` is tagged linux, though only `TestNoFDLeak` and the `gone` helper read `/proc`.
- **Scripts:**
  - `scripts/site-video.sh:23` uses GNU `realpath -m`.
  - e2e needs sway by design.
  - `packaging/install.sh:57` installs on Linux only.
- **No picture shows the Mac UI.** TestShots and the site shots render with `onMac=false`, so ⌘ labels and the Mac settings pages are never pictured.
- **A `macos-latest` job could run today:**
  - `./internal/{agent,detect,osc,proto,model,keys,notice,crash,shellcmd,theme,paths,client,ctl,pty,procs,gitsnap,worktree,tunnel,devurl,web} ./service`
  - `internal/session` once its tag is fixed.
  - The rest (`internal/daemon`, `internal/cli`, the root package) after 1.5 and the socket paths.

## 5. To confirm on a Mac
1. The new process lookups: `go test ./internal/pty ./service ./internal/procs ./internal/devurl -count=1` (safe, see 1.5), then the Info panel with a dev server running.
2. Binding `127.0.0.1:443` as a user (1.3): `nc -l 127.0.0.1 443` vs `nc -l 0.0.0.0 443`.
3. `security add-trusted-cert` through osascript on the current macOS.
4. Opening the app from the Dock: tunnels and New Agent, and a project process that needs nvm (1.1).
5. Whether privacy prompts come back after an update (1.4).
6. The git stub prompt on a Mac without the Command Line Tools.
7. Whether `*.localhost` subdomains resolve for Go's resolver on macOS.

## 6. Suggested order
1. **Safety and coverage first**, so macOS work can be tested:
   - config isolation (1.5)
   - `internal/session` test tags
   - short socket paths in tests
   - `GOOS=darwin go vet ./...` in CI
   - a `macos-latest` job for the packages in §4
2. **Function:**
   - login environment (1.1)
   - daemon notifications (1.2)
   - Dev URLs binding, after confirming 1.3
   - Option as Meta and the ⌘ terminal keys
   - peer check on darwin
   - Dock badge
   - osascript error handling
   - the Linux wording (3.4)
3. **Distribution, needing the user's decisions:**
   - bundle identifier (3.1)
   - Developer ID and notarization (1.4)
   - where the CLI goes (§2)
   - start at login (LaunchAgent or SMAppService)

Decisions for the user:
- The real bundle identifier.
- Whether to get an Apple Developer account for notarization.
- Option as Meta's default.
- `/usr/local/bin` (elevated) or a PATH hint for the CLI.
- How Dev URLs bind on a Mac if 1.3 is confirmed.
