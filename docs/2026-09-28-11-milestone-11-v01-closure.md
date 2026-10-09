# Milestone 11 — v0.1 Closure & Hardening

> Menutup sisa lubang `v0.1` tanpa bocor ke `v0.2+`. Blokir klaim rilis, bukan fitur baru.

**Status:** Complete with documented limits — see [status.md](status.md) for the live record and evidence. The `## Goals` list is the delivered scope; `## Acceptance Criteria` items with documented limits stay unchecked.

## Product Contract

M11 adalah milestone penutup `v0.1`. Ia tidak menambah domain baru (tidak ada
file-tree/git/diff/palette, tidak ada agent spawn/status, tidak ada Ghostty/
macOS/Windows/daemon). Ia mengubah status "complete with documented limits"
pada M9/M10 menjadi bukti Wayland yang penuh, plus memberi owner pada kontrak
yang selama ini tidak punya milestone: konfigurasi umum (blueprint §61),
launch-args CLI (blueprint §60), `pane.resize` discoverability, logging
(blueprint §45), sisa keamanan terminal (blueprint §47), boundary OS
(blueprint §50), baseline perf/sumber daya (blueprint §52–§53), dan packaging/
inventaris lisensi (blueprint §59–§60, §70).

M10 tetap post-v0.1 dan kontrak layout/CWD-only M6 tidak berubah. `terminal
wait` (blueprint §42) tetap ditunda ke `v0.4`; M11 hanya mencatat desain API
agar tidak terjadi scope creep. Pilihan lisensi proyek adalah keputusan produk
terpisah; M11 hanya mewajibkan inventaris transitif yang lengkap. Perilaku
SIGTERM kill-tanpa-cleanup adalah kondisi pre-existing; M11 hanya
mendokumentasikannya dan mem-proof path `window.close` yang graceful.

## Goals

- [x] Bukti live untuk semua fix M10 yang masih berstatus unit-only
- [x] Bukti live untuk project jump + folder picker + shutdown/corrupt/CWD
- [x] Kontrak `config.toml` umum (terminal/appearance/automation) dengan
  preservasi format dan error eksplisit
- [x] CLI launch-args (`omaterm .`, path, path + command) dengan kontrak
  launcher-vs-IPC yang jelas
- [x] `split_id` dapat di-discovery dari `pane list` + proof resize live
- [x] Logging terstruktur berkategori dengan audit redaksi secret
- [x] Policy paste multiline + drop path yang aman (OSC-52 tetap refuse)
- [x] Trait boundary OS + baseline perf/sumber daya formal
- [x] `PKGBUILD` Arch + tarball + inventaris lisensi transitif lengkap
- [x] Matriks validasi manual ditutup atau dicatat eksplisit sebagai limit

## Prerequisites

- M5–M9 complete dan terverifikasi (lihat status M5–M9).
- Kode fix M10 (inactive-project replay, prompt-strip, padding-blank,
  journal-merge) sudah di working tree; yang kurang hanya proof live.
- Snapshot/recovery M6 tidak berubah jaminannya sepanjang M11.

## Deliverables

### 11A — M10 live closeout (P0)

Menutup tiga catatan `in_progress` pada status M10:

1. Inactive-project replay: `session_id_for_pane` mencari di semua project/tab
   (pane ID unik workspace-wide), staged events di re-stage (bukan di-drop)
   bila sesi belum bisa di-resolve.
2. Prompt-strip + padding-blank: sekali strip sebelum `replay_into` dan
   seeding recorder; tepat satu blank separator milik prompt yang survive;
   idempoten antar restart.
3. Journal-prefix merge: buffer journal dihangatkan dari arsip saat startup
   dan opt-in agar flush pertama tidak membuang prefix.

Proof live (release, isolated `XDG_STATE_HOME`/`XDG_CONFIG_HOME`, Secret
Service asli): dua project folder sama, enable history, output + journal,
Super+W close, reopen 1x lalu 3x dengan `ll` gagal. Assert: scrollback
per-pane pulih, tidak ada prompt/blank menumpuk, journal menggabungkan
prefix arsip + entri baru, session/PID semuanya baru, shell live.

### 11B — Project jumps + folder picker (P0)

- `Ctrl+Shift+1..9` memilih project ke-n dalam urutan sidebar via
  `ProjectCommand::Select`. `project_jump_index` memetakan digit mentah dan
  simbol shifted (`!@#$%^&*(`). Out-of-range no-op. `Ctrl+1..9` polos tetap
  tidak di-bind agar tidak mencuri control code terminal.
- Hint `"{n} · "` pada baris sidebar hanya saat Ctrl/Shift ditahan.
- `project.set-directory`: future-only (tab/split/launch berikutnya;
  sesi hidup dan CWD per-pane tidak tersentuh), validasi `is_dir`, efek
  `WorkspaceChanged → PersistenceDirty`, otorisasi owner-scope seperti
  `Rename`. Trigger desktop: tombol `change` membuka native folder picker
  directories-only (portal XDG di Wayland); cancel no-op, kegagalan portal
  menjadi warning banner.

Proof live: tahan Ctrl+Shift (hint muncul), tekan digit (seleksi pindah);
`set-directory` via IPC lalu `tab new` di base baru sementara tab lama tetap;
tutup graceful dan pastikan `pinned_directory` + CWD per-pane terpersist.

### 11C — Shutdown, corrupt, CWD (P0)

- Flush history 10s yang bounded (`shutdown_flush_targets`) sebelum
  snapshot/reap; ordering stop-ingress → save → revoke/reap → unlink
  socket/credential milik sendiri → release UI (blueprint §67).
- Restart live dari arsip korup: karantina retained, pane + shell fresh tetap
  jalan dengan warning history.
- CWD per-pane post-restore di-verifikasi ulang via plumbing M6 (bukan hanya
  PID/session baru).
- SIGTERM: didokumentasikan sebagai kill tanpa cleanup (pre-existing, socket
  stale direklamasi saat startup per M8). Handler graceful adalah proposal
  terpisah, bukan bagian M11.

### 11D — Kontrak konfigurasi umum (P1, blueprint §61)

`~/.config/omaterm/config.toml` menjadi canonical config:

```toml
[terminal]
font-family = "JetBrains Mono"
font-size = 13
scrollback-lines = 10000

[appearance]
theme = "system"

[automation]
enabled = true
```

`[history]` tetap milik M10. Gunakan `toml_edit` untuk preservasi
section/comment/format, atomic `0600` writes, migrasi satu-kali dari legacy
bila ada, dan error `Parse` eksplisit untuk TOML malformed (bukan silent
default). Unknown/invalid values gagal aman dengan log/error jelas.

### 11E — CLI launch-args (P1, blueprint §60)

- Tanpa argumen: launch desktop bila absen (bounded readiness) atau
  acknowledge bila sudah jalan (perilaku M9, tidak berubah).
- `omaterm .` / `omaterm ~/path`: buka direktori sebagai project (implisit
  `project.create` setelah desktop siap).
- `omaterm <path> -- <cmd>`: buat project + terminal lalu submit argv
  (semantik `terminal.run`, bukan janji exit-success).
- Subcommand query/mutasi tidak pernah auto-launch. Fokus kompositor tetap
  deferred.

Parser + mapping + e2e untuk kedua cabang (absent/running) dan kedua bentuk
path; dokumentasikan invocasi dev vs install.

### 11F — `pane.resize` discoverability (P1)

Terkecil yang menutup limit M9: `pane list` (human + JSON) mengekspos
`split_id`, fraction, dan geometri per pane, dengan flag `truncated` yang
akurat. CLI meneruskan `--split/--fraction` apa adanya; bound fraction
tetap milik core/router. Proof live: resize 0.5 → 0.45/0.55 lalu equalize
kembali 0.5/0.5, bukan hanya error-path `split_not_found`.

### 11G — Logging hardening (P2, blueprint §45)

Pasang subscriber `tracing` dengan kategori `workspace`, `pane`,
`terminal`, `pty`, `render`, `ipc`, `cli`, `persistence`. Audit: token
capability, password, clipboard, payload input, dan teks terminal tidak
boleh muncul di log default; format `Debug` untuk request/token hanya
ringkasan aman. Tidak ada analytics/telemetri.

### 11H — Sisa keamanan terminal (P2, blueprint §47)

- Paste multiline/command-like: policy konfirmasi eksplisit tanpa
  memblokir copy/paste dasar; bracketed paste tetap dihormati.
- Drop file ke terminal menjadi absolute path yang di-escape aman.
- OSC-52 clipboard read tetap ditolak by design (M3, tidak berubah).

Spesifikasikan policy dulu, lalu implementasi + test encoder/quoting.

### 11I — Boundary OS + baseline perf (P2, blueprint §50, §52–§53)

- Trait kecil: `PtyProvider`, `ClipboardProvider`, `NotificationProvider`,
  `ProcessInspector` (descendants, listening ports). Pindahkan `procfs` dan
  clipboard ke adapter; jangan sebar `cfg(target_os)` di modul workspace.
- Baseline formal dengan workload/durasi/hardware/build-mode/jumlah
  sesi visible-hidden yang tercatat: startup, idle CPU release, flood 3k
  burst, 100-cycle threads/FD/RSS, load IPC konkuren. Nilai growth antar
  siklus, bukan satu high-water RSS. Observasi GPU per-proses best-effort;
  bila tooling hanya device-wide (mis. RadeonTop), catat sebagai konteks
  sistem, bukan klaim nol.

### 11J — Packaging + inventaris lisensi (P2, blueprint §59, §70)

- `PKGBUILD` Arch + tarball + daftar native deps; `AppImage/.deb/.rpm`
  = Later.
- Inventaris transitif lengkap via `cargo metadata` di `docs/dependencies.md`
  (source, versi/revisi, tujuan, ekspresi lisensi + file lisensi, tanggal
  verifikasi, perintah/hasil). `Cargo.lock` tetap di-track. Pilihan lisensi
  proyek = keputusan terpisah.

### 11K — Matriks validasi penutup (P2, blueprint §48–§49, §54)

X11 runtime bila sesi tersedia, kompositor Wayland kedua bila ada,
scaling/monitor lain, `zsh`/`fish` bila terinstal, `tmux/vim/nvim/btop/less`.
Yang unavailable dicatat eksplisit sebagai limit (preseden M3/M5/M6),
bukan pass. IME composition tetap unsupported eksplisit pada GPUI 0.2.2.

## Execution Order

```
P0: 11A → 11B → 11C
P1: 11F → 11D → 11E
P2: 11G → 11H → 11I → 11J → 11K
```

11J boleh paralel sejak awal (docs/packaging only). Jangan mulai P1 sebelum
P0 hijau; jangan klaim M11 complete bila 11A–11C masih unit-only.

## Test Plan

- Unit: config round-trip/migrasi/malformed, jump-index digit+shifted,
  set-directory stale/invalid/scoped-foreign, strip/padding idempotency,
  logging redaction, drop-escaping, boundary adapter, archive/quota guards
  yang tersentuh.
- Integration: PTY lifecycle + hidden-output yang tersentuh 11A/11C,
  IPC scope/filter untuk field baru 11F, launcher absent/running 11E.
- Desktop live (release, isolated state, screenshot di `/tmp` bukan repo):
  11A (2-project reopen + 3x blank check), 11B (hint+jump+picker+future-only),
  11C (graceful close + corrupt restart + CWD), 11F (resize+equalize),
  11E (path launch + path+cmd).
- Quality gate tiap fase: `cargo fmt --all --check`,
  `cargo test --workspace -- --test-threads=1` (serial; PTY flake dicatat,
  bukan di-pass), `cargo clippy --workspace --all-targets -- -D warnings`,
  `cargo build --release`, `python3 scripts/check-docs.py`,
  `git diff --check`, update `docs/dependencies.md` bila tambah dep.

## Acceptance Criteria

- [ ] 11A: scrollback per-pane pulih di project/tab tidak aktif; tidak ada
  prompt/blank menumpuk setelah 3x reopen; journal merge terbukti live
- [ ] 11B: jump + hint + picker + future-only semantics terbukti live;
  cancel/portal-missing terobservasi atau diinjeksi
- [ ] 11C: graceful `window.close` mem-flush history + snapshot terbaru,
  socket/credential milik sendiri terhapus, tanpa orphan; corrupt live
  terisolasi; CWD per-pane terverifikasi
- [ ] 11D: config umum load/preserve/error eksplisit dengan test
- [ ] 11E: path launch + path+cmd di kedua cabang dengan test + proof
- [ ] 11F: `split_id` terdiscovery di `pane list`; resize+equalize live hijau
- [ ] 11G: kategori logging + redaction audit dengan test
- [ ] 11H: policy paste/drop dispesifikasikan + diimplementasi + dites
- [ ] 11I: adapter OS + baseline perf tercatat (workload/durasi/HW/mode)
- [ ] 11J: `PKGBUILD` + tarball + inventaris lisensi lengkap
- [ ] 11K: matriks manual ditutup atau limit eksplisit
- [ ] Flow blueprint §78 tetap hijau di instance yang sama (split mengembalikan
  pane ID baru, run acknowledgement, read bounded)

## Non-Goals

- Tidak ada file-tree, git panel, diff viewer, editor, palette, project
  search (rencana `v0.2`, blueprint §23, §32–§35).
- Tidak ada agent recognition/status/notifikasi, spawn/prompt/wait/delegasi,
  approval boundary `Safe vs RequiresApproval` (rencana `v0.3/v0.4`,
  blueprint §21, §38–§40).
- Tidak ada implementasi `terminal wait`; hanya catatan desain API di spec
  ini (blueprint §42).
- Tidak ada Ghostty backend, macOS/Windows, browser panes, plugin/remote,
  daemon terpisah, klaim secure-deletion fisik (Later, blueprint §23).
- Tidak ada handler SIGTERM graceful; hanya dokumentasi perilaku + proposal
  terpisah.
- Tidak ada pilihan lisensi proyek; hanya inventaris (blueprint §70).

## References

- Blueprint §2 — Nama, paths, prefix env `OMATERM_`
- Blueprint §20 — Capability dan security model
- Blueprint §21 — Human approval boundary (ditunda ke `v0.4`)
- Blueprint §22 — First-Run Product Scope
- Blueprint §23 — Suggested Version Progression
- Blueprint §29 — Terminal Scrollback
- Blueprint §30 — Persistence
- Blueprint §42 — Wait Primitives (desain saja di M11)
- Blueprint §43 — Output Format for CLI
- Blueprint §44 — Error Design
- Blueprint §45 — Logging
- Blueprint §47 — Terminal Security
- Blueprint §48 — Wayland
- Blueprint §49 — X11
- Blueprint §50 — Cross-Platform Boundary
- Blueprint §52 — Performance Requirements
- Blueprint §53 — Resource Lifecycle Tests
- Blueprint §54 — Testing Strategy
- Blueprint §55 — MVP Acceptance Criteria
- Blueprint §59 — Packaging
- Blueprint §60 — CLI Launch Behavior
- Blueprint §61 — Configuration
- Blueprint §62 — No Duplicate Business Logic
- Blueprint §63 — IDs Over Screen Coordinates
- Blueprint §64 — Bounded External Inputs
- Blueprint §65 — Protocol Versioning
- Blueprint §66 — Cancellation
- Blueprint §67 — Shutdown
- Blueprint §68 — Implementation Sequence
- Blueprint §70 — Licensing
- Blueprint §78 — First Implementation Objective
- [M9 — CLI](2026-09-26-09-milestone-9-cli.md) — kontrak launcher dan coverage table
- [M10 — Encrypted History Recovery](2026-09-27-10-milestone-10-history-recovery.md) — prasyarat live 11A–11C
