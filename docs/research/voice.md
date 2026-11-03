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

## Not done
- Streaming (text as you speak): whisper works on a whole recording, so the text comes once you stop.
- Windows: web access is off there, so voice is too.
- Checked live: the downloads, a real `whisper-server` and a real phone are left to the user's live check.
