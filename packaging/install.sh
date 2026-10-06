#!/usr/bin/env bash
# OmaTerm installer — fetches the prebuilt tarball from GitHub Releases and
# installs it. No AUR account, no Rust toolchain, no root required.
#
# Quick start (latest release, system-wide):
#   curl -sL https://github.com/fachrihawari/omaterm/releases/latest/download/install.sh | bash
#
# Options:
#   --user            install into ~/.local instead of /usr/local (no sudo)
#   --prefix PATH     install prefix (default: /usr/local; implies layout below)
#   --version X.Y.Z   install a specific version instead of the latest
#   --help            show this help
#
# Env overrides (mostly for testing):
#   OMATERM_TARBALL_URL   full URL of the tarball to install (skips lookup)
#   OMATERM_SHA_URL       full URL of the .sha256 file (default: <tarball>.sha256)
#
# Layout under $PREFIX:
#   bin/omaterm, bin/omaterm-desktop
#   share/applications/omaterm.desktop
#   share/icons/hicolor/scalable/apps/omaterm.svg
#   share/licenses/omaterm/{LICENSE-MIT,LICENSE-APACHE}
set -euo pipefail

REPO="fachrihawari/omaterm"
PREFIX="${PREFIX:-/usr/local}"
VERSION="latest"
TARBALL_URL="${OMATERM_TARBALL_URL:-}"
SHA_URL="${OMATERM_SHA_URL:-}"

usage() {
  printf '%s\n' 'Usage: install.sh [--user] [--prefix PATH] [--version X.Y.Z]' \
    'Default prefix: /usr/local; --user installs into ~/.local.'
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --user) PREFIX="$HOME/.local"; shift ;;
    --prefix | --version)
      [[ $# -ge 2 && -n "$2" && "$2" != --* ]] || { echo "error: $1 requires a value" >&2; exit 1; }
      if [[ "$1" == --prefix ]]; then PREFIX="$2"; else VERSION="$2"; fi
      shift 2 ;;
    --help | -h) usage; exit 0 ;;
    *) echo "error: unknown argument '$1' (see --help)" >&2; exit 1 ;;
  esac
done

[[ "$VERSION" == latest || "$VERSION" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'error: invalid version' >&2; exit 1; }
PREFIX="$(realpath -m -- "$PREFIX")"
[[ "$PREFIX" != *$'\n'* && "$PREFIX" != *$'\r'* ]] || { echo 'error: prefix contains a newline' >&2; exit 1; }
[[ "$PREFIX" != / && "$PREFIX" != /usr ]] || { echo 'error: use /usr/local, --user, or a custom prefix' >&2; exit 1; }

ARCH="$(uname -m)"
[[ "$(uname -s)" == Linux ]] || { echo 'error: OmaTerm requires Linux' >&2; exit 1; }
if [[ "$ARCH" != "x86_64" ]]; then
  echo "error: prebuilt tarballs are currently x86_64-only (detected: $ARCH)." >&2
  echo "Build from source instead: https://github.com/$REPO" >&2
  exit 1
fi

for cmd in curl tar sha256sum install; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "error: required tool '$cmd' not found in PATH." >&2
    exit 1
  fi
done

if [[ -z "$TARBALL_URL" ]]; then
  if [[ "$VERSION" == "latest" ]]; then
    echo "Resolving latest release..."
    EFFECTIVE_URL="$(curl -fsSIL -o /dev/null -w '%{url_effective}' \
      "https://github.com/$REPO/releases/latest")"
    TAG="${EFFECTIVE_URL##*/tag/}"
  else
    TAG="v${VERSION#v}"
  fi
  VER="${TAG#v}"
  TARBALL_URL="https://github.com/$REPO/releases/download/$TAG/omaterm-$VER-x86_64.tar.gz"
fi
if [[ -z "$SHA_URL" && "$TARBALL_URL" != file://* ]]; then
  SHA_URL="$TARBALL_URL.sha256"
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

echo "Downloading $TARBALL_URL ..."
if [[ "$TARBALL_URL" == file://* ]]; then
  cp "${TARBALL_URL#file://}" ./omaterm.tar.gz
else
  curl -fSL -o ./omaterm.tar.gz "$TARBALL_URL"
fi

if [[ -n "$SHA_URL" ]]; then
  echo "Verifying checksum..."
  if [[ "$SHA_URL" == file://* ]]; then
    cp "${SHA_URL#file://}" ./omaterm.tar.gz.sha256
  else
    curl -fSL -o ./omaterm.tar.gz.sha256 "$SHA_URL"
  fi
  # Compare hashes explicitly instead of `sha256sum -c`: the .sha256 asset
  # names the versioned tarball while we download to a fixed local name.
  EXPECTED="$(cut -d' ' -f1 ./omaterm.tar.gz.sha256)"
  [[ "$EXPECTED" =~ ^[0-9a-f]{64}$ ]] || { echo 'error: invalid SHA-256 file' >&2; exit 1; }
  ACTUAL="$(sha256sum ./omaterm.tar.gz | cut -d' ' -f1)"
  if [[ "$EXPECTED" != "$ACTUAL" ]]; then
    echo "error: checksum mismatch (expected $EXPECTED, got $ACTUAL)." >&2
    exit 1
  fi
  echo "Checksum OK."
fi

tar -xzf ./omaterm.tar.gz

# Validate the entire payload before touching an existing installation.
for file in omaterm omaterm-desktop omaterm.desktop omaterm.svg LICENSE-MIT LICENSE-APACHE; do
  [[ -f "$file" && ! -L "$file" ]] || { echo "error: tarball missing regular file $file" >&2; exit 1; }
done
for binary in omaterm omaterm-desktop; do
  if command -v ldd >/dev/null 2>&1; then
    libraries="$(ldd "./$binary" 2>&1 || true)"
    if [[ "$libraries" == *'not found'* ]]; then
      printf 'error: missing runtime libraries for %s:\n%s\n' "$binary" "$libraries" >&2
      echo 'See packaging/README.md for Arch runtime dependencies.' >&2
      exit 1
    fi
  fi
done
# Quote/escape the absolute executable path according to Desktop Entry rules.
EXEC_PATH="$PREFIX/bin/omaterm-desktop"
EXEC_PATH="${EXEC_PATH//\\/\\\\}"
EXEC_PATH="${EXEC_PATH//\"/\\\"}"
EXEC_PATH="${EXEC_PATH//\$/\\\$}"
EXEC_PATH="${EXEC_PATH//\`/\\\`}"
EXEC_PATH="${EXEC_PATH//%/%%}"
while IFS= read -r line; do
  if [[ "$line" == Exec=* ]]; then printf 'Exec="%s"\n' "$EXEC_PATH"; else printf '%s\n' "$line"; fi
done < omaterm.desktop > installed.desktop

SUDO=""
# mkdir first: -w on a nonexistent path is always false, which would
# wrongly trigger the sudo branch for fresh --prefix targets.
mkdir -p "$PREFIX" 2>/dev/null || true
if [[ ! -w "$PREFIX" && "$(id -u)" -ne 0 ]]; then
  if command -v sudo >/dev/null 2>&1; then
    echo "Requesting sudo to write to $PREFIX (or re-run with --user)..."
    SUDO="sudo"
  else
    echo "error: $PREFIX is not writable and sudo is unavailable." >&2
    echo "Re-run with --user to install into ~/.local instead." >&2
    exit 1
  fi
fi

install_binary() {
  local source="$1" destination="$PREFIX/bin/$1" temporary
  $SUDO mkdir -p "$PREFIX/bin"
  temporary="$($SUDO mktemp "$PREFIX/bin/.omaterm-install.XXXXXX")"
  if ! $SUDO install -m755 "$source" "$temporary" || ! $SUDO mv -f -- "$temporary" "$destination"; then
    $SUDO rm -f -- "$temporary"
    return 1
  fi
}
install_binary omaterm
install_binary omaterm-desktop
# shellcheck disable=SC2086
$SUDO install -Dm644 installed.desktop "$PREFIX/share/applications/omaterm.desktop"
# shellcheck disable=SC2086
$SUDO install -Dm644 omaterm.svg "$PREFIX/share/icons/hicolor/scalable/apps/omaterm.svg"
# shellcheck disable=SC2086
$SUDO install -Dm644 LICENSE-MIT "$PREFIX/share/licenses/omaterm/LICENSE-MIT"
# shellcheck disable=SC2086
$SUDO install -Dm644 LICENSE-APACHE "$PREFIX/share/licenses/omaterm/LICENSE-APACHE"

if [[ -n "$SUDO" ]]; then
  command -v update-desktop-database >/dev/null 2>&1 &&
    $SUDO update-desktop-database "$PREFIX/share/applications" 2>/dev/null || true
  command -v gtk-update-icon-cache >/dev/null 2>&1 &&
    $SUDO gtk-update-icon-cache -f -t "$PREFIX/share/icons/hicolor" 2>/dev/null || true
else
  command -v update-desktop-database >/dev/null 2>&1 &&
    update-desktop-database "$PREFIX/share/applications" 2>/dev/null || true
  command -v gtk-update-icon-cache >/dev/null 2>&1 &&
    gtk-update-icon-cache -f -t "$PREFIX/share/icons/hicolor" 2>/dev/null || true
fi

echo ""
echo "OmaTerm installed to $PREFIX."
echo "Run: omaterm-desktop   (GUI)   |   omaterm --help   (CLI)"
echo "Uninstall: curl -sL https://github.com/$REPO/releases/latest/download/uninstall.sh | bash"
if [[ "$PREFIX" == "$HOME/.local" ]]; then
  case ":$PATH:" in
    *":$HOME/.local/bin:"*) ;;
    *) echo "Note: ~/.local/bin is not in PATH yet — add it to your shell rc." ;;
  esac
fi
