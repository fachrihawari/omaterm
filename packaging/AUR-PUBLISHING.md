# Publishing OmaTerm to AUR

Runbook: from zero to `yay -S omaterm-bin`. Covers the one-off setup (AUR
account, SSH, empty submissions, GitHub secret), the dry-run, the first
release, the routine for later releases, and troubleshooting.

Package overview (what gets published) lives in [README.md](README.md).
Release tarball layout contract is defined there too — do not rename
tarball entries without updating `packaging/arch-bin/PKGBUILD`.

## Prerequisites

- Commit with the packaging work pushed to `main` (Fase 1: dual PKGBUILDs,
  `release.yml`, `scripts/bump-aur.sh`, `scripts/check-aur-sync.py`).
- No tag pushed yet for a first release; both `sha256sums` are `SKIP`.

## Step 0 — Push Fase 1 work to GitHub

```bash
git push origin main
```

## Step 1 — Create an AUR account

1. Register at <https://aur.archlinux.org/register> and log in.
2. Under **My Account**, verify the email address.

## Step 2 — SSH key for AUR

AUR only accepts pushes over SSH (`ssh://aur@aur.archlinux.org`).
Prefer a dedicated key over reusing the GitHub one:

```bash
ssh-keygen -t ed25519 -f ~/.ssh/aur -C "aur-omaterm"
cat ~/.ssh/aur.pub
```

Paste the public key into AUR **My Account → SSH Public Key → Update**,
then test:

```bash
ssh -i ~/.ssh/aur aur@aur.archlinux.org help
```

A command list means success. `Permission denied` means the key is not
installed yet or was pasted incorrectly.

## Step 3 — Submit both empty packages (one-off)

New packages **cannot** be pushed via git before they are registered
through the web form. Do this for **both** names (reserves the brand):

1. Open <https://aur.archlinux.org/packages/new>.
2. Name: `omaterm`, Category: `system`, License: `MIT, Apache-2.0`.
   Upload any minimal PKGBUILD + `.SRCINFO` — the files in
   `packaging/arch/` work even with `sha256sums` still `SKIP`.
3. Repeat for `omaterm-bin` with the files in `packaging/arch-bin/`.

Regenerate `.SRCINFO` if ever in doubt (needs `makepkg` on Arch):

```bash
cd packaging/arch && makepkg --printsrcinfo > .SRCINFO
```

After submission these pages must exist (content comes later via CI):
<https://aur.archlinux.org/packages/omaterm> and
<https://aur.archlinux.org/packages/omaterm-bin>.

## Step 4 — GitHub secret for AUR pushes

The `release` workflow pushes to AUR over SSH and needs the private key:

1. Repo → **Settings → Secrets and variables → Actions → New repository secret**.
2. Name: `AUR_SSH_PRIVATE_KEY`, value: contents of `~/.ssh/aur`
   (the **private** file, `-----BEGIN...` through `-----END...`).

No code change needed — the workflow already reads this secret name.

## Step 5 — Dry-run: build the tarball without touching AUR

1. **Actions → Release → Run workflow**, version e.g. `0.1.0`, Run.
2. Wait for the `dist` job to go green (first GPUI build: ~10–20 min).
3. Download the `omaterm-dist` artifact and inspect it:

   ```bash
   tar -tzf omaterm-0.1.0-x86_64.tar.gz
   # expected: omaterm  omaterm-desktop  omaterm.desktop
   #           omaterm.svg  LICENSE-MIT  LICENSE-APACHE
   ```

4. On failure, read the log — it is almost always native deps or the
   toolchain, not packaging.

## Step 6 — First real release

```bash
bash scripts/bump-aur.sh 0.1.0
git add packaging/
git commit -m "chore(packaging): bump to 0.1.0"
git tag v0.1.0
git push origin main v0.1.0
```

The workflow then runs automatically: **build tarball → create GitHub
Release → pin both checksums into copies of each PKGBUILD + `.SRCINFO` →
push to the `omaterm` / `omaterm-bin` AUR repos**. It aborts if either
in-repo `pkgver` differs from the tag version — bump-before-tag is
mandatory, not a suggestion.

## Step 7 — Verify

1. GitHub Release `v0.1.0` contains `omaterm-0.1.0-x86_64.tar.gz` + `.sha256`.
2. Both AUR pages show version `0.1.0-1` with real hashes (no `SKIP`).
3. On an Omarchy machine (or VM):
   ```bash
   yay -S omaterm-bin
   omaterm --version
   omaterm-desktop        # the window must open
   ```
   Also confirm the `OmaTerm` icon appears in the application launcher.
4. Optional source-package test (5–15 min compile):
   ```bash
   yay -S omaterm
   ```

## Routine for later releases (3 lines)

```bash
bash scripts/bump-aur.sh 0.2.0
git add packaging/ && git commit -m "chore(packaging): bump to 0.2.0"
git tag v0.2.0 && git push origin main v0.2.0
```

CI does everything else. Optionally pin the published checksums back
into the repo so in-repo files match AUR exactly (hashes are in the
workflow log; the `-bin` hash is also the `.sha256` release asset):

```bash
bash scripts/bump-aur.sh <ver> <tag-tarball-sha256> <release-tarball-sha256>
```

## Troubleshooting

| Symptom | Cause & fix |
|---|---|
| `aur` job fails to clone AUR | Secret wrong / key missing in AUR account. Redo Step 2 + 4 |
| `pkgver != tag version`, workflow aborts | Forgot `bump-aur.sh` before tagging. Move the tag: `git tag -d vX && git push origin :refs/tags/vX`, bump, re-tag |
| `omaterm-bin` checksum mismatch (manual flow) | Take the hash from the `.sha256` asset on the GitHub Release |
| No icon after install | `gtk-update-icon-cache` or logout/login once; or the placeholder SVG has not been replaced by final branding yet |
| Withdrawing a package from AUR | Prefer keeping it so the name stays reserved; full removal needs a deletion request |
