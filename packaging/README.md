# OmaTerm Packaging (M11 task 11J)

## Arch Linux (first-class, Omarchy)

```bash
cd packaging/arch
makepkg -si
```

This builds `omaterm` (CLI) and `omaterm-desktop` (GPUI window) with
`cargo build --release --locked` and installs both to `/usr/bin`.
`check()` runs the serial workspace suite (`--test-threads=1`; PTY tests
are timing-sensitive and must not run in parallel).

`sha256sums` is `SKIP` until the first version tag exists; the maintainer
publishing a release MUST pin the tag tarball checksum. `license` is
`custom:UNLICENSED` until the project license is chosen (blueprint §70);
dependency licenses are inventoried in `docs/dependencies.md`.

## Standalone tarball

```bash
cargo build --release --locked --bin omaterm --bin omaterm-desktop
tar -czf omaterm-0.1.0-x86_64.tar.gz -C target/release omaterm omaterm-desktop
tar -tzf omaterm-0.1.0-x86_64.tar.gz
```

Requires the same native packages as development (see `docs/dependencies.md`
M1 record): `wayland`, `libxkbcommon`, `libx11`, `libxcb`, `fontconfig`,
`freetype2`, `mesa`, `vulkan-icd-loader`, `pkgconf`.

## Deferred

`AppImage`, `.deb`, `.rpm` are Later (blueprint §59). Do not let packaging
complexity delay the development binary.
