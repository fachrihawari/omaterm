#!/usr/bin/env bash
# Bump AUR packaging files to a release version.
#
# Usage:
#   scripts/bump-aur.sh <pkgver> [<src-sha256> [<bin-sha256>]]
#
# With only <pkgver>: sets pkgver in both PKGBUILDs and regenerates .SRCINFO,
# leaving sha256sums as-is (SKIP pre-release). Run this BEFORE tagging.
# With checksums: also pins sha256sums. Run this AFTER the release tarballs
# exist to keep the in-repo files identical to what was pushed to AUR.
#
# The `release` GitHub workflow performs the equivalent pinning on the AUR
# clones automatically; this script is the local/maintainer counterpart.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <pkgver> [<src-sha256> [<bin-sha256>]]" >&2
  exit 1
fi

PKGVER="$1"
SRC_SHA="${2:-}"
BIN_SHA="${3:-}"

if [[ ! "$PKGVER" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "error: pkgver must look like X.Y.Z (got '$PKGVER')" >&2
  exit 1
fi

bump_one() {
  local dir="$1" sha="$2"
  sed -i "s/^pkgver=.*/pkgver=$PKGVER/" "$dir/PKGBUILD"
  if [[ -n "$sha" ]]; then
    if [[ ! "$sha" =~ ^[0-9a-f]{64}$ ]]; then
      echo "error: sha256 must be 64 hex chars (got '$sha')" >&2
      exit 1
    fi
    sed -i "s/^sha256sums=.*/sha256sums=('$sha')/" "$dir/PKGBUILD"
  fi
  (cd "$dir" && makepkg --printsrcinfo > .SRCINFO)
}

bump_one "$ROOT/packaging/arch" "$SRC_SHA"
bump_one "$ROOT/packaging/arch-bin" "$BIN_SHA"

echo "bumped packaging/arch + packaging/arch-bin to $PKGVER"
