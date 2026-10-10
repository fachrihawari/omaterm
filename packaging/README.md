# OmaTerm Packaging (M11 task 11J)

Two AUR packages reserve the OmaTerm brand; both install the same two
binaries (`omaterm` CLI + `omaterm-desktop` GPUI window) plus the desktop
entry, icon, and licenses.

> Full publish runbook (one-off AUR setup, dry-run, first release,
> routine, troubleshooting): [AUR-PUBLISHING.md](AUR-PUBLISHING.md).

## Quick install (Linux x86_64, no AUR needed)

Prebuilt x86_64 tarball from GitHub Releases, checksum-verified:

```bash
curl -sL https://github.com/fachrihawari/omaterm/releases/latest/download/install.sh | bash
```

Variants: `--user` (into `~/.local`, no sudo), `--prefix PATH`,
`--version X.Y.Z` (default: latest). Uninstall anytime:

```bash
curl -sL https://github.com/fachrihawari/omaterm/releases/latest/download/uninstall.sh | bash
```

(`--user` / `--system` / `--prefix PATH` supported; user data under
`~/.config/omaterm` is left untouched.) Both scripts are attached to
every GitHub Release by the `release` workflow alongside the tarball.

For a user-only installation:

```bash
curl -fsSL https://github.com/fachrihawari/omaterm/releases/latest/download/install.sh | bash -s -- --user
curl -fsSL https://github.com/fachrihawari/omaterm/releases/latest/download/uninstall.sh | bash -s -- --user
```

Use the same `--prefix PATH` for custom installation and removal. The
uninstaller never targets `/usr`; remove pacman/AUR packages with pacman.
The installer checks missing dynamic libraries before installing. On Arch:

```bash
sudo pacman -S --needed gcc-libs fontconfig freetype2 libxkbcommon libx11 libxcb wayland vulkan-icd-loader
```

Run the GUI in a Wayland/X11 desktop with a working GPU driver. Library
checks do not replace desktop validation. To update, run the installer again.

| AUR package | In-repo source | Installs from |
|---|---|---|
| `omaterm` | `packaging/arch/` | source build (`cargo build --release --locked`) |
| `omaterm-bin` | `packaging/arch-bin/` | prebuilt `omaterm-<ver>-x86_64.tar.gz` from GitHub Releases |

The app icon at `packaging/icons/omaterm.svg` is a **placeholder**; final
branding follows. Both packages conflict with each other (`conflicts`),
`omaterm-bin` additionally `provides=('omaterm')`.

## Install (Arch / Omarchy)

```bash
yay -S omaterm-bin        # fast: prebuilt binary
# or
yay -S omaterm            # source build; runs the serial workspace suite
                          # (--test-threads=1; PTY tests are timing-sensitive)
```

Local build from this repo:

```bash
cd packaging/arch && makepkg -si
```

Requires the same native packages as development (see `docs/dependencies.md`
M1 record): `wayland`, `libxkbcommon`, `libx11`, `libxcb`, `fontconfig`,
`freetype2`, `mesa`, `vulkan-icd-loader`, `pkgconf`.

## Release flow (maintainer)

1. Bump versions in-repo (also regenerates both `.SRCINFO` files):
   ```bash
   ./scripts/bump-aur.sh 0.4.0
   git add packaging/ && git commit -m "chore(packaging): bump to 0.4.0"
   git tag v0.4.0 && git push origin main v0.4.0
   ```
2. The `release` workflow builds both binaries with `mbx` on `ubuntu-22.04`
   (`backend: local`, no shared cache for published artifacts), attaches
   `omaterm-<ver>-x86_64.tar.gz` (+ `.sha256`) to the GitHub Release, then
   pins the tag-tarball checksum into `omaterm` and the release-tarball
   checksum into `omaterm-bin` and pushes each to its AUR repo. It aborts if
   either in-repo `pkgver` does not match the tag — run the bump script first.
3. Optionally pin the checksums in-repo to match what was pushed to AUR:
   ```bash
   ./scripts/bump-aur.sh <ver> <tag-tarball-sha256> <release-tarball-sha256>
   ```
   Both checksums are printed in the workflow log and the `.sha256` release
   asset covers the `-bin` tarball.

`python3 scripts/check-aur-sync.py` (also a CI job) verifies PKGBUILD ↔
`.SRCINFO` consistency and `pkgver` parity with `apps/omaterm` and
`crates/omaterm-cli`.

## First-time AUR setup (one-off)

1. Create an [AUR account](https://aur.archlinux.org/register) when registration
   is available, and add an SSH public key under My Account. New packages
   are submitted by pushing a valid PKGBUILD and `.SRCINFO` to their AUR git
   repositories; no web-form registration or empty placeholder is needed.
2. Add the matching SSH **private** key as the `AUR_SSH_PRIVATE_KEY` repo
   secret (Settings → Secrets → Actions). The workflow pushes via
   `ssh://aur@aur.archlinux.org/<pkg>.git`.
3. Set repository Actions variable `AUR_ENABLED` to `true` when ready.
   Leave it unset while distributing through GitHub Releases only.

To test the tarball build without tagging or touching AUR, run the `release`
workflow manually (Actions → Release → Run workflow) with a version number.

## Deferred

`AppImage`, `.deb`, `.run`, `.rpm` are Later (blueprint §59). The release
tarball layout (`omaterm`, `omaterm-desktop`, `omaterm.desktop`,
`omaterm.svg`, licenses at top level) is the contract the `-bin` PKGBUILD
and all future bundle formats build on — do not rename its entries without
updating `packaging/arch-bin/PKGBUILD`.
