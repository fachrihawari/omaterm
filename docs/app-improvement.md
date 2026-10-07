# OmaTerm — App Improvement Backlog

> Kompilasi semua issue dari sesi audit UI/UX + fungsionalitas + laporan teman.
> Status: Wave 1+2 IMPLEMENTED (item 1 dan 4b, belum commit, validasi Wayland
> manual pending). Sisanya triage. Setiap item mencantumkan lokasi kode,
> akar masalah, workaround saat ini, dan arah fix.

## Legenda severity

* **P0** — user-facing broken, memicu abandon.
* **P1** — UX salah / inkonsisten, membingungkan tapi ada workaround.
* **P2** — gap fungsionalitas vs ekspektasi Alacritty/Zed/VSCode.
* **P3** — polish / tech-debt visual.

---

## P0 — Broken

### 1. `Alt+Shift+1..9` (project jump) tidak pernah bekerja di Linux — CONFIRMED

* Lokasi app: `apps/omaterm/src/main.rs:10052-10066` (guard `alt && shift && !control`),
  registry `apps/omaterm/src/shortcuts.rs:262-276` (`alt_shift_project_index`),
  hint `apps/omaterm/src/main.rs:16658-16676` + `13950`.
* Lokasi toolkit: `gpui-0.2.2/src/platform/linux/platform.rs` (`Keystroke::from_xkb`):

  ```rust
  if modifiers.shift {
      // only include shift for upper-case letters by convention
      if key.chars().count() == 1 && key.to_lowercase() == key.to_uppercase() {
          modifiers.shift = false;
      }
  }
  ```

* Akar masalah: GPUI membuang flag `shift` untuk digit/simbol. `Alt+Shift+1`
  tiba sebagai `key="!"`, `shift=false`. Guard project-jump gagal
  (butuh `shift=true`); guard strip-jump di atasnya (`10041-10048`,
  `alt && !control && !shift`) lolos tapi `alt_strip_slot("!")` → `None`
  (`shortcuts.rs:242-255`, digit only). Fallthrough ke no-op / ESC-prefix ke PTY.
* Kejamnya: `on_modifiers_changed` membaca modifier mentah (`shift=true`),
  sehingga sidebar tetap menampilkan label `Alt+Shift+n` (`13950`).
  Hint berjanji, keypress tidak pernah delivery.
* Bukti bukan compositor: mesin pelapor Wayland+Hyprland, layout tunggal `us`,
  tidak ada `grp:alts_toggle`, GNOME `switch-input-source = Super+Space`.
  Mapping `exclam → "!"` di GPUI benar (`platform.rs:852`).
* Silent-failure tambahan di arm yang sama: index ≥ jumlah project
  (`if index < projects.len()` gagal → `return` diam tanpa toast);
  1 project + `Alt+Shift+1` = Select ke project yang sama (tanpa perubahan visual).
* Kontras: `Alt+Shift+K` works karena huruf tidak di-strip shift-nya.
* Workaround: `Alt+PageUp/PageDown` (cycle, `10290-10330`), palette project
  select, `omaterm project select/list`, klik sidebar.
* Arah fix:
  1. Guard project-jump jangan syaratkan `shift` untuk simbol —
     `alt && !control` + `alt_shift_project_index(key)` match
     (`!@#$%^&*( ` maupun digit). Aman karena strip-jump menolak simbol.
  2. Toast past-the-end (`"No project N"`), bukan silent return.
  3. Unit test dengan `shift=false + key="!"` meniru GPUI.
  4. Jangka panjang: chord cycle baru non-`Alt+Shift` + pager `‹ ›` di header sidebar.
* **IMPLEMENTED (uncommitted, 2026-10-07):** guard diganti ke predikat murni
  `is_project_jump_key` (`main.rs`, name-based, tanpa syarat `shift`) +
  `input_notice` past-the-end (`"No project N (M open)."`) + clear notice saat
  sukses + test `project_jump_matches_names_not_the_stripped_shift_flag`.
  Gate: `fmt --check` PASS, `test --workspace` PASS (semua suite ok),
  `clippy --all-targets -D warnings` PASS (notice transitif `proc-macro-error2`
  saja), `check-docs.py` PASS, `git diff --check` PASS.
  Manual Wayland pending: tahan `Alt+Shift` (hint muncul) → tekan digit →
  project pindah; tekan digit past-the-end → notice jujur.

### 2. Copy-paste terminal terasa mati — CONFIRMED

* Copy: `main.rs:10347-10362` → `copy_selection():10744-10762`.
  Syarat seleksi aktif + `input_owner == Terminal`; tanpa seleksi = silent no-op.
* Paste: `main.rs:10332-10344` → `paste():10523-10559`.
* Drag-select (`on_mouse_up:10707-10741`) hanya tulis ke PRIMARY (`10734`),
  sedangkan paste baca CLIPBOARD (`10525`). Select → `Ctrl+Shift+V` tidak nempel.
* Tidak ada right-click menu di terminal. Satu-satunya `MouseButton::Right`
  adalah project card (`14002`). `Ctrl+C/V` milik PTY (benar, jangan direbut).
* Paste multiline/control-char butuh 2x tekan dalam 8 detik
  (`PASTE_ARM_WINDOW:1189`, `10541-10548`). Satu tekan hanya banner → dikira mati.
* Workaround: `Ctrl+Shift+C` setelah drag-select, `Ctrl+Shift+V`
  (2x untuk multiline), middle-click untuk PRIMARY.
* Arah fix:
  1. Select tulis ke CLIPBOARD juga (atau keduanya).
  2. Right-click `Copy/Paste` di pane terminal.
  3. Toast saat copy tanpa seleksi; banner two-step paste diperjelas.

### 3. Context menu project atas tertutup project bawah — CONFIRMED

* Menu adalah child dari card (`main.rs:14055-14112`,
  `absolute top(60) right(0)` di dalam `card.relative:13963-13965`).
* Cards paint berurutan tanpa z-index → menu card #1 di bawah card #2 dst.
* Parent `overflow_y_scroll:14174` + `overflow_hidden:14125` = menu ke-clip,
  bukan overlay window.
* Dismiss hanya via klik card/aksi (`13994,14084,14106`); tidak ada click-outside.
* Arah fix: render sekali di level window (portal/overlay) + click-outside
  dismiss + Esc menutup.

### 4b. Huruf `f` tidak bisa diketik di terminal — CONFIRMED (bug global, bukan keyboard Teddy)

* Lokasi: `apps/omaterm/src/main.rs:17019` (`translate_key`):

  ```rust
  name if name.starts_with('f') => Key::F(name[1..].parse().ok()?),
  ```

* Akar masalah: guard function-key terlalu luas. Huruf polos `f`
  cocok `starts_with('f')`, lalu `"".parse::<u8>()` gagal → `?`
  membuat `translate_key` return `None` → caller (`10507-10509`)
  silent `return`, nol byte ke PTY.
* Bukti bukan keyboard user: `F1`-`F12` (`f1`..`f12`) parse OK;
  hanya `f` tunggal yang mati. Editor / palette / search tidak kena
  (jalur input sendiri, tanpa `translate_key`); hanya forwarding terminal.
* Arah fix (satu baris): syaratkan minimal 2 char + range valid, misal
  `strip_prefix('f')` + `1 <= n <= 12` (atau 35), else fallthrough ke `Key::Char`.
  Tambah regression test: `translate_key("f")` → `Key::Char('f')`,
  `translate_key("f1"/"f12")` → `Key::F`, `translate_key("f13"+)` sesuai kebijakan.
* **IMPLEMENTED (uncommitted, 2026-10-07):** arm diganti ke
  `name.len() > 1 && starts_with('f') => Key::F(function_key_number(name)?)` +
  helper murni `function_key_number` (hanya F1..F12; `"f"`/F13+/non-numerik → `None`)
  + test `bare_f_is_a_plain_letter_not_a_function_key`. Perilaku lain identik
  (F1–F12, F13+ swallow). Gate sama seperti item 1 (semua PASS).
  Manual Wayland pending: ketik `f` di terminal → muncul; `F1`–`F12` tetap
  kirim escape sequence.

### 4. Dotfiles (`.`) tidak muncul — by-design tapi undiscoverable

* `files_show_hidden` hanya dari config (`main.rs:2073` ←
  `crates/omaterm-state/src/config.rs:141-143`, default `false`).
* 5 hits semua read-path (`4322,4385,9363`); nol write-path / toggle UI / palette.
* Satu-satunya cara: `~/.config/omaterm/config.toml` → `[files] show-hidden=true` + restart.
* **Keputusan produk (2026-10-07): file tree tidak menyembunyikan apa pun —
  tampilkan semua secara default, itu keindahan file tree dan programming.**
* Arah fix (revisi): default `show_hidden = true` (`unwrap_or(true)` +
  update test `missing_file_yields_defaults` + doc comment); kunci config
  `show-hidden` tetap dihormati bila user eksplisit set `false`.
  **Toggle UI tidak dibuat — file tree selalu tampilkan semua.**

---

## P1 — UX salah / inkonsisten

### 5. Kebingungan `Alt+Shift+N` vs `Alt+Shift+1..9` vs `Alt+N`

* Registry (`shortcuts.rs:27-238`, `docs/shortcuts.md`):
  `project.jump=Alt+Shift+1..9`, `keys.cheatsheet=Alt+Shift+K`,
  `project.new=Ctrl+Alt+N`, `project.cycle=Alt+PageUp/PageDown`,
  `diff.hunk-nav=Alt+N/P`.
* Tidak ada binding `Alt+Shift+N`. `docs/shortcuts.md:18` menulis `Alt+Shift+n`
  (n = digit, bukan huruf N) — mudah salah baca.
* `project.cycle` nol affordance: tidak ada hint sidebar/strip/status,
  hanya baris cheatsheet.
* Arah fix: dokumentasi + label sidebar diperjelas (`Alt+Shift+1..9`),
  tambah affordance cycle (pager/header/pill).

### 6. Dual-palette: zinc vs `theme.rs` (verifikasi `rg`: 77 zinc hits vs 293 `theme::`)

* `render_ctrlp + render_keybindings:main.rs:15666-15891` full zinc
  (`0x18181B/0x52525B/0xE4E4E7/0xA1A1AA/0x27272A/0x2E2E33/0xFBBF24`,
  match-highlight `hsla(0.594,...)` vs `BLUE`).
* Empty/closed/starting panes (`10995-11053,16159-16217`) zinc
  (`0x18181B/0xFCA5A5/0xA1A1AA/0x52525B`).
* `0x71717A` ~40x untuk hint finder/files/git/history/diff vs `MUTED 0x7F8A99`.
* `kbd()` (`ui/primitives.rs:34-35`) hardcode `0x313B48/0x161B22`;
  scrim `rgba(0x00000099)` (`ui/prompt.rs:91`); `0xFFFFFF` tersebar
  (`primitives.rs:45`, `main.rs:677,687,14412,...`).
* Token ada tapi nganggur: `TERMINAL_CURSOR 0x95D7FF` (cursor pakai `0xE4E4E7:11066`),
  `COMMIT_HOVER_BG`, `SCROLLBAR_THUMB`, `PANE_FOCUS_STROKE_ALPHA`,
  `TAB_ACTIVE_TOP_ACCENT_ALPHA`, `CMD_HOVER_BG/BORDER`.
* `files.rs:51-164` ~90 warna ikon Nerd + `MATCH_ACCENT 0x4C9AFF` vs `BLUE2 0x2F81F7`.
* `workbench.rs:10-13` `WARN_BG/ERROR_BG` di luar theme.
* Arah fix: rute semua overlay + empty-pane lewat `theme.rs`; tambah token
  `WHITE/KBD_*/COMMENT/ACTIVE_LINE_NO`; pakai token nganggur; satukan bahasa ikon.

### 7. Tipografi bypass roles (52x raw `text_size`)

* Header pane terminal (`11113-11148`), label `OMATERM` (`14162`),
  baris cheatsheet, toast (`16527`) warisi default GPUI (~14px) vs spec 10/11px.
* Caret finder kopling ke font terminal (`15644`) — dilarang plan §5.
* Diff `split_cell h(21px)` (`16816,16829`) vs line 22px.
* Letter-spacing hilang framework-wide (diakui `ui/metrics.rs:8-12`).
* Arah fix: ganti semua raw size dengan `metrics::text_role`; cabut kopling
  caret→font terminal.

### 8. Radius/spacing tanpa single source (38 hits campur)

* Spec: cmd 7 / card 8 / kbd 4 / toast 8. Aktual: `rounded_sm/md/full/px(4/6/8/10)`
  campur. `sidebar_toggle:664` pakai `rounded_sm`; prompt `:115` 10px;
  toast `:16522` `rounded_md`.
* Header pane `h(31)` vs 32, footer `h(27)` vs 28 (border di luar).
* Toolbar `top(39):11198` vs spec 7; tanpa shadow; focus-gated bukan hover-only.
* Toast `bottom(14)` pane-relative vs spec 38 window-centered.
* Finder `top(72)`, `box_w clamp(200,600)` vs cheatsheet 640 — arbitrary,
  pane-relative bukan window-centered, tanpa backdrop.
* Resizer rest `BG` (strip 4px kelihatan) vs transparan; tanpa hover-blue/cursor.
* Arah fix: primitif `cmd_button/row/shadow/tab`; betulkan geometri pane/toast/toolbar.

### 9. State matrix bolong (hover/selected/active/focus)

* Tab (`14406-14435`): inactive `PANEL` vs transparan; tanpa hover; aksen 2px
  layout-border (geser konten) vs inset 1px.
* Pane (`11160-11164`): focus 1px `BLUE` solid vs inset `rgba(90,169,255,.34)`.
* Project card (`13980-13985`): hover benar untuk inactive — implementasi terbaik —
  tapi tanpa pressed/keyboard-focus-ring; aksi hanya right-click.
* Files/Git rows: hover ada, selected tambah `rounded_sm` vs radius 0;
  finder selected tambah `border_l_2` (layout shift).
* Aksi baris Git selalu mounted (nama reflow; seharusnya `opacity 0→1` + lebar cadangan).
* Nol focus-ring kecuali border input; `track_focus` hanya root + prompt.
* Empat ukuran dot: pane 6px vs project/tab/status 8px.
* Arah fix: selesaikan matrix (hover tab, inset aksen/focus, toolbar hover-only,
  seleksi tanpa layout shift, focus-ring + keyboard nav).

### 10. Ikon campur + aksesibilitas

* Chevron teks `▸/▾:554`, separator `›:7928` vs Lucide; `●` status tanpa metrik;
  files Nerd vs dir Lucide; alias `EXTERNAL` untuk `open-in-new`.
* Kontras berisiko: `MUTED2` di `PANEL` (~2.8:1), zinc di `0x18181B` (~3.6:1)
  untuk teks 9-11px (< 4.5:1).
* Keyboard: prompt + palette lengkap; cards/rows/tabs mouse-first
  (tanpa focusable role / arrow-nav / indikator).
* Motion: easing 120/100/180ms, toolbar slide/fade, toast transition, cursor
  step-blink absen; tanpa `prefers-reduced-motion`.
* Arah fix: Lucide chevron, drop Nerd untuk files, focus-ring + keyboard nav,
  contrast pass, catat gap letter-spacing di acceptance.

### 11. Layout/clipping/virtualisasi

* 3 sistem transient (toast + `input_notice` + banner + modal); banner reflow canvas.
* Files pakai tinggi window penuh bukan body-minus-search (kelebihan ~5 baris).
* `LEAF_CHROME_H` global (`11374`) abaikan header/footer/border per-leaf.
* Tab strip `overflow_hidden` tanpa scroll/minima → clip di window sempit.
* Arah fix: virtualisasi per-leaf body-minus-chrome; notice sebagai overlay;
  tab-strip scroll horizontal + minima; `input_notice` overlay.

---

## P2 — Gap fungsionalitas (ekspektasi Alacritty/Zed/VSCode)

| # | Gap | Dampak |
|---|-----|--------|
| 12 | Tanpa terminal-search (scrollback 10k hanya via `terminal read --lines`) | Alacritty `Ctrl+Shift+F` / Zed / VSCode users quit hari-1 |
| 13 | Restart = shell fresh (layout+CWD saja; scrollback hanya via opt-in encrypted history) | `npm dev`/`ssh`/`tail -f` hilang saat reboot |
| 14 | Tanpa pane-zoom, tanpa split `left/up`, tanpa font-zoom `Ctrl+/-`; equalize tanpa chord (cuma palette/IPC/CLI) | Daily driver split/zoom mati |
| 15 | Tanpa tab reorder/rename, cap `Alt+1..9` (past-end = notice) | Monorepo 20-tab tidak layak |
| 16 | Git tanpa branch/checkout/fetch/pull/push/stash/blame; graph spine-linear, tanpa ref badge / full message / parent selector; cap 100 | Tiap workflow nyata fallback ke terminal |
| 17 | Editor tanpa LSP/debugger/extensions (non-goal eksplisit), tanpa find/replace, tanpa Save-As, highlight per-baris, symlink-final ditolak, dirty hilang saat crash (metadata-only registry) | User jalankan `vim` di terminal — investasi editor sia-sia |
| 18 | IME unsupported by declaration; `Ctrl+P`/`Ctrl+Shift+P` bukan scrollback search; tanpa URL-click, bell/notif, vi/copy-mode, broadcast, profil, kustomisasi key | CJK + enterprise a11y gagal instan |
| 19 | Nol `editor.*` IPC; tanpa `palette.*`, `tab.move/rename`, `pane.zoom`, `terminal.wait`, `config get/set`, `project.remove` | Cerita agent-automation (v0.4) belum bisa drive editor |
| 20 | Linux-Arch/AUR + tarball x86_64 + `curl\|bash` saja; ikon placeholder; validasi Wayland-only | Terlihat hobi di laptop kerja (HiDPI, multi-monitor, screen-share) |

Status milestone (per `docs/status.md`, `acceptance-matrix.md`, `00-overview.md:79-84`):
M15 diff, M16 palette, M18 proses, M19 editor semua in-progress/parcial;
M17 pending; M19 melompat antrean. Sinyal perpetual-beta bagi pendatang baru.

---

## P3 — Dead/renamed (membingungkan bila ditemukan)

* Equalize chord mati → palette/IPC/CLI saja (`shortcuts.md:65-66`).
* Empty-workspace terminal chord mati → `Ctrl+Shift+T` = new-tab (`:68`).
* `Ctrl+P` finder lama → migrasi ke unified overlay (koeksistensi sementara).
* `terminal.clear` stub `not implemented` (`router.rs:3355`).
* CLI 41 baris vs protokol 44 method (`process.*`, `git.history`,
  `git.commit-files`, `diff.show-commit` ada kode tapi coverage/docs lag).

---

## Rencana eksekusi disarankan

1. **P0 dulu** (urutan): overlay menu project (3) → clipboard+right-click (2) → toggle hidden (4) → guard project-jump tanpa-shift + toast (1).
2. **P1 batch**: kill zinc (6) → tipografi/radius (7,8) → state matrix (9) → ikon/a11y (10) → layout/virtualisasi (11) → label `Alt+Shift+1..9` + affordance cycle (5).
3. **P2 keputusan**: terminal-search / pane-zoom / shell-resurrect = build atau
   WONTFIX eksplisit dengan rasional. Jangan tambah surface baru sebelum ini.
4. Tutup M15+M16+M18 atau potong scope; jangan biarkan 4 milestone terbuka bareng.

## Verifikasi yang sudah dilakukan untuk audit ini

* `rg` zinc vs `theme::` (77 vs 293), raw `text_size` (52), radius campur (38).
* Trace `on_key_down` untuk `n` + `Alt+Shift`, `!` + `Alt+Shift`,
  dan `is_cheatsheet_toggle` / `alt_strip_slot` / `alt_shift_project_index`.
* Baca `Keystroke::from_xkb` GPUI 0.2.2 (strip-shift) + mapping `exclam → "!"`.
* Cek compositor mesin pelapor: Wayland+Hyprland, layout `us` tunggal,
  tanpa `grp:alts_toggle`, `switch-input-source = Super+Space`.
* Grep `MouseButton::Right` (hanya project card), `files_show_hidden`
  (5 hits, semua read), `project_context_menu` (9 hits).
* Baca `config.rs:101-143` (default `show-hidden=false`) dan render sidebar
  `13930-14195` (menu sebagai child card dalam scroll container).
