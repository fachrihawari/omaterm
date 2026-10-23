#!/usr/bin/env bash
# OmaTerm installer — fetches a prebuilt tarball from GitHub Releases and
# installs it. No AUR account, no toolchain, no root required.
#
# Quick start (latest release):
#   curl -fsSL https://github.com/fachrihawari/omaterm/releases/latest/download/install.sh | sh
#
# This script is kept for the links that pointed at it. The release carries
# its own install.sh, which is the one to prefer: it knows the layout of the
# version it ships with. This one handles both layouts, so it installs the
# latest version and the older ones alike.
#
# Options:
#   --user            install into ~/.local (the default)
#   --prefix PATH     install into PATH/bin instead, as 0.6.x did
#   --version X.Y.Z   install a specific version instead of the latest
#   --help            show this help
#
# Env overrides (mostly for testing):
#   OMATERM_TARBALL_URL   full URL of the tarball to install (skips lookup)
#   OMATERM_SHA_URL       full URL of the .sha256 file (default: <tarball>.sha256)
set -euo pipefail

REPO="fachrihawari/omaterm"
NAME="omaterm"
PREFIX=""
VERSION="latest"
TARBALL_URL="${OMATERM_TARBALL_URL:-}"
SHA_URL="${OMATERM_SHA_URL:-}"

usage() {
  printf '%s\n' 'Usage: install.sh [--user] [--prefix PATH] [--version X.Y.Z]' \
    'Installs the app in ~/.local/<name>.app by default, where it updates' \
    'itself. --prefix PATH installs into PATH/bin instead, as 0.6.x did;' \
    'such an install updates with this script, not itself.'
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --user) PREFIX=""; shift ;;
    --prefix | --version)
      [[ $# -ge 2 && -n "$2" && "$2" != --* ]] || { echo "error: $1 requires a value" >&2; exit 1; }
      if [[ "$1" == --prefix ]]; then PREFIX="$2"; else VERSION="$2"; fi
      shift 2 ;;
    --help | -h) usage; exit 0 ;;
    *) echo "error: unknown argument '$1' (see --help)" >&2; exit 1 ;;
  esac
done

[[ "$VERSION" == latest || "$VERSION" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'error: invalid version' >&2; exit 1; }
if [[ -n "$PREFIX" ]]; then
  PREFIX="$(realpath -m -- "$PREFIX")"
  [[ "$PREFIX" != *$'\n'* && "$PREFIX" != *$'\r'* ]] || { echo 'error: prefix contains a newline' >&2; exit 1; }
  [[ "$PREFIX" != / && "$PREFIX" != /usr ]] || { echo 'error: use ~/.local, --user, or a custom prefix' >&2; exit 1; }
fi

[[ "$(uname -s)" == Linux ]] || { echo 'error: OmaTerm requires Linux' >&2; exit 1; }
case "$(uname -m)" in
  x86_64 | amd64) TARGET=linux-amd64 ;;
  aarch64 | arm64) TARGET=linux-arm64 ;;
  *) echo "error: OmaTerm is not made for $(uname -m)." >&2; exit 1 ;;
esac

for cmd in curl tar install; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "error: required tool '$cmd' not found in PATH." >&2; exit 1; }
done

TAG=""
if [[ -z "$TARBALL_URL" ]]; then
  if [[ "$VERSION" == "latest" ]]; then
    echo "Resolving latest release..."
    EFFECTIVE_URL="$(curl -fsSIL -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest")"
    TAG="${EFFECTIVE_URL##*/tag/}"
  else
    TAG="v${VERSION#v}"
  fi
  VER="${TAG#v}"
  BASE="https://github.com/$REPO/releases/download/$TAG"

  # 0.7 and later publish update-<target>.json, whose url names the archive
  # of that version. Older releases name the tarball after the architecture
  # instead, and only for x86_64.
  if curl -fsSL -o /dev/null "$BASE/update-$TARGET.json" 2>/dev/null; then
    MANIFEST="$(curl -fsSL "$BASE/update-$TARGET.json")"
    # The manifest lists the archive's url first, then those of the deltas,
    # which are indented: take the top-level one.
    TARBALL_URL="$(printf '%s\n' "$MANIFEST" | sed -n 's/^  "url": "\([^"]*\)".*/\1/p' | head -n 1)"
    [[ -n "$TARBALL_URL" ]] || { echo "error: $BASE/update-$TARGET.json names no archive" >&2; exit 1; }
  elif [[ "$TARGET" == "linux-amd64" ]]; then
    TARBALL_URL="$BASE/$NAME-$VER-x86_64.tar.gz"
  else
    echo "error: releases before 0.7 are x86_64-only (detected: $(uname -m))." >&2
    echo "Build from source instead: https://github.com/$REPO" >&2
    exit 1
  fi
fi
if [[ -z "$SHA_URL" && "$TARBALL_URL" != file://* ]]; then
  SHA_URL="$TARBALL_URL.sha256"
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

echo "Downloading $TARBALL_URL ..."
if [[ "$TARBALL_URL" == file://* ]]; then
  cp "${TARBALL_URL#file://}" ./app.tar.gz
else
  curl -fSL -o ./app.tar.gz "$TARBALL_URL"
fi

# Only the older releases publish a checksum; a 404 is not an error.
if [[ -n "$SHA_URL" ]]; then
  if [[ "$SHA_URL" == file://* ]]; then
    cp "${SHA_URL#file://}" ./app.tar.gz.sha256 2>/dev/null || SHA_URL=""
  elif curl -fsSL -o ./app.tar.gz.sha256 "$SHA_URL" 2>/dev/null; then
    :
  else
    SHA_URL=""
  fi
fi
if [[ -n "$SHA_URL" ]]; then
  echo "Verifying checksum..."
  EXPECTED="$(cut -d' ' -f1 ./app.tar.gz.sha256)"
  [[ "$EXPECTED" =~ ^[0-9a-f]{64}$ ]] || { echo 'error: invalid SHA-256 file' >&2; exit 1; }
  ACTUAL="$(sha256sum ./app.tar.gz | cut -d' ' -f1)"
  if [[ "$EXPECTED" != "$ACTUAL" ]]; then
    echo "error: checksum mismatch (expected $EXPECTED, got $ACTUAL)." >&2
    exit 1
  fi
  echo "Checksum OK."
fi

tar -tzf ./app.tar.gz > ./names 2>/dev/null || { echo 'error: not a .tar.gz archive' >&2; exit 1; }
if grep -Eq '^/|(^|/)\.\.(/|$)' ./names; then
  echo 'error: archive holds files outside the app' >&2
  exit 1
fi
mkdir -p ./payload
tar -xzf ./app.tar.gz -C ./payload
# 0.6.x released the app under its old name; 0.7 renamed it.
if [[ ! -f ./payload/$NAME && -f ./payload/oterm ]]; then
  NAME="oterm"
fi
[[ -f ./payload/$NAME && ! -L ./payload/$NAME ]] || { echo "error: the archive holds no $NAME" >&2; exit 1; }
chmod +x ./payload/$NAME

# 0.7 and later: one binary plus libghostty-vt.so, installed as a directory
# the app can replace when it updates itself.
# 0.6 and earlier: a CLI and a desktop binary, installed into a prefix.
install_app_dir() {
  local app_dir="$HOME/.local/$NAME.app" staging="$HOME/.local/.$NAME.app.install"
  local data_home="${XDG_DATA_HOME:-$HOME/.local/share}"
  rm -rf "$staging"
  mkdir -p "$staging"
  cp -a ./payload/. "$staging/"
  rm -rf "$app_dir"
  mv "$staging" "$app_dir"

  mkdir -p "$data_home/applications"
  if [[ -f "$app_dir/$NAME.desktop" ]]; then
    local line
    while IFS= read -r line || [[ -n "$line" ]]; do
      case "$line" in
        "Exec=$NAME" | "Exec=$NAME "*) line="Exec=\"$app_dir/$NAME\"${line#"Exec=$NAME"}" ;;
        "Icon=$NAME") [[ -f "$app_dir/$NAME.png" ]] && line="Icon=$app_dir/$NAME.png" ;;
      esac
      printf '%s\n' "$line"
    done <"$app_dir/$NAME.desktop" >"$data_home/applications/$NAME.desktop"
  fi

  echo ""
  echo "OmaTerm installed to $app_dir."
  echo "Open it from the applications menu, or run $app_dir/$NAME"
  echo "It updates itself from the project's releases."
}

install_prefix() {
  local bin="$PREFIX/bin" sudo=""
  mkdir -p "$PREFIX" 2>/dev/null || true
  if [[ ! -w "$PREFIX" && "$(id -u)" -ne 0 ]]; then
    if command -v sudo >/dev/null 2>&1; then
      echo "Requesting sudo to write to $PREFIX (or re-run with --user)..."
      sudo="sudo"
    else
      echo "error: $PREFIX is not writable and sudo is unavailable." >&2
      echo "Re-run with --user to install into ~/.local instead." >&2
      exit 1
    fi
  fi

  install_bin() {
    local name="$1" destination="$bin/$1" temporary
    $sudo mkdir -p "$bin"
    temporary="$($sudo mktemp "$bin/.omaterm-install.XXXXXX")"
    if ! $sudo install -m755 "./payload/$name" "$temporary" || ! $sudo mv -f -- "$temporary" "$destination"; then
      $sudo rm -f -- "$temporary"
      return 1
    fi
  }
  # shellcheck disable=SC2086
  install_bin "$NAME"
  if [[ -f ./payload/$NAME-desktop ]]; then
    # shellcheck disable=SC2086
    install_bin "$NAME-desktop"
  fi
  # The terminal binds this at run time and looks for it next to the binary.
  # shellcheck disable=SC2086
  [[ -f ./payload/libghostty-vt.so ]] && $sudo install -Dm755 ./payload/libghostty-vt.so "$bin/libghostty-vt.so"

  if [[ -f ./payload/$NAME.desktop ]]; then
    local exec_path="$bin/$NAME"
    exec_path="${exec_path//\\/\\\\}"
    exec_path="${exec_path//\"/\\\"}"
    exec_path="${exec_path//\$/\\\$}"
    exec_path="${exec_path//\`/\\\`}"
    exec_path="${exec_path//%/%%}"
    local line
    while IFS= read -r line; do
      if [[ "$line" == Exec=* ]]; then printf 'Exec="%s"\n' "$exec_path"; else printf '%s\n' "$line"; fi
    done <./payload/$NAME.desktop >./installed.desktop
    # shellcheck disable=SC2086
    $sudo install -Dm644 ./installed.desktop "$PREFIX/share/applications/$NAME.desktop"
  fi
  # 0.6.x shipped an SVG; 0.7 ships a PNG. Install whichever the archive has,
  # in the directory that layout used.
  if [[ -f ./payload/$NAME.svg ]]; then
    # shellcheck disable=SC2086
    $sudo install -Dm644 ./payload/$NAME.svg "$PREFIX/share/icons/hicolor/scalable/apps/$NAME.svg"
  elif [[ -f ./payload/$NAME.png ]]; then
    # shellcheck disable=SC2086
    $sudo install -Dm644 ./payload/$NAME.png "$PREFIX/share/icons/hicolor/256x256/apps/$NAME.png"
  fi
  for license in ./payload/LICENSE-*; do
    [[ -f "$license" ]] || continue
    # shellcheck disable=SC2086
    $sudo install -Dm644 "$license" "$PREFIX/share/licenses/$NAME/$(basename "$license")"
  done

  if command -v update-desktop-database >/dev/null 2>&1; then
    $sudo update-desktop-database "$PREFIX/share/applications" 2>/dev/null || true
  fi
  if command -v gtk-update-icon-cache >/dev/null 2>&1; then
    $sudo gtk-update-icon-cache -f -t "$PREFIX/share/icons/hicolor" 2>/dev/null || true
  fi

  echo ""
  echo "OmaTerm installed to $PREFIX."
  echo "Run: $bin/$NAME"
  echo "Note: an install in a prefix updates with this script, not itself."
}

# 0.7 and later: one binary plus libghostty-vt.so, installed as a directory
# the app can replace when it updates itself.
# 0.6 and earlier: a CLI and a desktop binary, installed into a prefix.
if [[ -n "$PREFIX" ]]; then
  install_prefix
else
  install_app_dir
fi
