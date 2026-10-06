#!/usr/bin/env bash
# OmaTerm uninstaller — removes exactly the files installed by install.sh.
# Run without arguments to clean every known location (system + user):
#   curl -sL https://github.com/fachrihawari/omaterm/releases/latest/download/uninstall.sh | bash
#
# Options:
#   --system          only /usr/local
#   --user            only ~/.local
#   --prefix PATH     only the given prefix
#   --help            show this help
#
# Never removes directories wholesale — only the listed files, then any
# parent directories left empty.
set -euo pipefail

SYSTEM_PREFIXES=("/usr/local")
USER_PREFIX="$HOME/.local"
TARGETS=()
MODE="all"
PREFIX_ARG=""

usage() {
  printf '%s\n' 'Usage: uninstall.sh [--user | --system | --prefix PATH]' \
    'Default: /usr/local and ~/.local. Pacman packages: use pacman -R.'
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --system) MODE="system"; shift ;;
    --user) MODE="user"; shift ;;
    --prefix)
      [[ $# -ge 2 && -n "$2" && "$2" != --* ]] || { echo 'error: --prefix requires a value' >&2; exit 1; }
      MODE="prefix"; PREFIX_ARG="$(realpath -m -- "$2")"; shift 2 ;;
    --help | -h) usage; exit 0 ;;
    *) echo "error: unknown argument '$1' (see --help)" >&2; exit 1 ;;
  esac
done

case "$MODE" in
  system) TARGETS=("${SYSTEM_PREFIXES[@]}") ;;
  user) TARGETS=("$USER_PREFIX") ;;
  prefix) TARGETS=("$PREFIX_ARG") ;;
  all) TARGETS=("${SYSTEM_PREFIXES[@]}" "$USER_PREFIX") ;;
esac

FILES=(
  "bin/omaterm"
  "bin/omaterm-desktop"
  "share/applications/omaterm.desktop"
  "share/icons/hicolor/scalable/apps/omaterm.svg"
  "share/licenses/omaterm/LICENSE-MIT"
  "share/licenses/omaterm/LICENSE-APACHE"
)
# Parent dirs to rmdir (deepest first) if left empty.
DIRS=(
  "share/licenses/omaterm"
  "share/icons/hicolor/scalable/apps"
  "share/applications"
)

REMOVED=0
for prefix in "${TARGETS[@]}"; do
  [[ "$prefix" != / && "$prefix" != /usr ]] || { echo 'error: uninstall pacman packages using pacman -R' >&2; exit 1; }
  found=false
  for f in "${FILES[@]}"; do
    if [[ -f "$prefix/$f" || -L "$prefix/$f" ]]; then found=true; break; fi
  done
  "$found" || continue
  SUDO=""
  if [[ -e "$prefix" && ! -w "$prefix" && "$(id -u)" -ne 0 ]]; then
    if command -v sudo >/dev/null 2>&1; then
      SUDO="sudo"
    else
      echo "skip: $prefix is not writable and sudo is unavailable."
      continue
    fi
  fi
  for f in "${FILES[@]}"; do
    if [[ -f "$prefix/$f" || -L "$prefix/$f" ]]; then
      # shellcheck disable=SC2086
      $SUDO rm -f "$prefix/$f"
      echo "removed: $prefix/$f"
      REMOVED=$((REMOVED + 1))
    fi
  done
  for d in "${DIRS[@]}"; do
    # shellcheck disable=SC2086
    $SUDO rmdir --ignore-fail-on-non-empty "$prefix/$d" 2>/dev/null || true
  done
  command -v update-desktop-database >/dev/null 2>&1 &&
      $SUDO update-desktop-database "$prefix/share/applications" 2>/dev/null || true
  command -v gtk-update-icon-cache >/dev/null 2>&1 &&
      $SUDO gtk-update-icon-cache -f -t "$prefix/share/icons/hicolor" 2>/dev/null || true
done

if [[ "$REMOVED" -eq 0 ]]; then
  echo "Nothing to remove: no OmaTerm files found."
else
  echo "Done: removed $REMOVED file(s). User data (~/.config/omaterm, state) was left untouched."
fi
