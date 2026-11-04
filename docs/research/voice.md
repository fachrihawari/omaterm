# Voice input in web access: why whisper.cpp, 2026-11-03

## Context
The user wanted to speak prompts on the phone instead of typing them, in several languages, Indonesian first. Decided with the user: the speech is written down on the desktop by a local engine; the text goes into the compose box to be checked before Send; voice is **off by default** and nothing downloads or runs until it is turned on in Settings › Voice.

## Choices

**whisper.cpp, run as `whisper-server`.**
- One multilingual model covers about 99 languages, Indonesian, Javanese and Sundanese among them. "Download a language" is therefore a model size, and the language is a setting (and the phone's chip).
- Local: the recording reaches only the user's own computer, through the tunnel web access already uses. No account, no key, no cloud service.
- It runs as a process of its own on the loopback, started with the first recording and stopped after 10 minutes idle, when voice is turned off, or when its downloads are removed. A crash in whisper can't take down the daemon, which holds every terminal. No cgo, so `internal/voice` cross-builds as the rest does.
- `whisper-server` on the PATH (a package manager's) is used first. Otherwise Settings downloads omaterm's own static build, made by `.github/workflows/whisper.yml` and pinned by SHA-256 in `internal/voice/catalog.go`. Until that workflow has run, the checksums are empty and the download is refused: only the PATH's engine works.

**Models** come from `huggingface.co/ggerganov/whisper.cpp` at a fixed revision, each pinned by its LFS SHA-256 (tiny's was checked by downloading it). `small` (466 MiB) is the one recommended for Indonesian; `tiny` and `base` are rough outside English; `large-v3-turbo-q5_0` is best and slowest.

**WAV made on the phone.** The page records with `getUserMedia`, resamples to 16 kHz mono with an `OfflineAudioContext` and encodes 16-bit PCM itself. `MediaRecorder` gives WebM (Chrome) or MP4 (iOS), which would need ffmpeg on the computer. A recording is capped at 2 minutes (3.84 MB), and the server takes at most 4 MB, 20 recordings a minute per device.

**Needs HTTPS.** A phone's browser gives the microphone to secure pages only: Tailscale and Cloudflare tunnels are HTTPS; "On your network" is plain HTTP, and there the mic says so instead of failing.

**The desktop, 2026-11-03.** The user's decisions: one command, `voice.dictate`, on Ctrl+Shift+Space (⇧⌘Space on macOS: ⌘Space is Spotlight, ⌃⌘Space the emoji picker), and a mic in a terminal pane's header, a toggle, Esc dropping it; the text is pasted (bracketed when the program asks) into the pane that had the keyboard, on one line, never with Enter (changed 2026-11-04, below). It goes through the session daemon (`voice.transcribe`, the WAV base64 in the JSON: 4 MB is well under the socket's 64 MB line), so one whisper-server serves the phone and the desktop.
- Linux records with a program, the first on the PATH of `pw-record`, `parecord`, `arecord`, as 16 kHz mono 16-bit WAV into a file of the state folder's, stopped with SIGINT, which has each finish its file; sizes a killed one left out of the header are written in, and the data cut at 2 minutes.
- macOS has no such program: a web view loads a page from the app's loopback file server (`filesrv.go`), which records with `getUserMedia` and the phone's own `voice.js`, and posts the WAV back. Only that web view, and only for that origin, gets the microphone (`Page.SetPermissionHandler`); `NSMicrophoneUsageDescription` is in `mygo.json`, without which macOS ends the app. The web view sits in the header at one transparent pixel while it records, as WebKit may mute capture in a page it doesn't show. Unverified: built with GOOS=darwin, not run on a Mac.
- Windows: none, as web access isn't there either.

**How a dictation ends, 2026-11-04.** The user found key, key, wait, Enter too many steps. While a dictation goes on, Enter is the dictation's, as Esc is: it ends the recording and, once the text is written down and pasted, presses Enter, 150 ms after the paste, as a program reading both at once takes the Enter as a newline. When the agent in the pane asks for permission, the text is pasted and not sent, as Enter would answer the question. The key again pastes it to read first, with a space after it for the next one. Held, the key is push-to-talk: the app sees its modifiers let go of (Linux sends no key repeat flag, so a repeat while they are held is ignored), and letting go after 400 ms ends it, pasted to read first. No key was added. The status bar shows a dictation going on, wherever the pane is; the macOS recorder page sits there, as a pane's header can go out of sight. On the phone, Send while recording ends it and sends what was said once written down.

## Not done
- Streaming (text as you speak): whisper works on a whole recording, so the text comes once you stop.
- Windows: web access is off there, so voice is too, on the phone and the desktop.
- Checked live: the downloads, a real `whisper-server` and a real phone are left to the user's live check.
