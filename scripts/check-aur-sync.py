#!/usr/bin/env python3
"""Verify AUR packaging files are internally consistent.

Checks, for packaging/arch (omaterm) and packaging/arch-bin (omaterm-bin):
  1. PKGBUILD pkgver matches its .SRCINFO pkgver.
  2. PKGBUILD source entry matches its .SRCINFO source entry.
  3. PKGBUILD sha256sums matches its .SRCINFO sha256sums.
  4. Both PKGBUILDs share the same pkgver.
  5. That pkgver matches the workspace binaries (apps/omaterm,
     crates/omaterm-cli Cargo.toml versions).

This is a pure-text check so it runs on CI runners without makepkg.
Maintainers on Arch should additionally run
`makepkg --printsrcinfo | diff - .SRCINFO` (or scripts/bump-aur.sh, which
regenerates .SRCINFO) inside each packaging directory.
"""

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
PKGS = {
    "omaterm": ROOT / "packaging/arch",
    "omaterm-bin": ROOT / "packaging/arch-bin",
}

failures: list[str] = []


def fail(msg: str) -> None:
    failures.append(msg)
    print(f"FAIL: {msg}")


def parse_pkgbuild(path: Path) -> dict[str, str]:
    text = path.read_text()
    out: dict[str, str] = {}
    for var in ("pkgname", "_pkgname", "pkgver", "url"):
        m = re.search(rf"^{var}=(\S+)\s*$", text, re.M)
        if m:
            out[var] = m.group(1).strip("'\"")
    m = re.search(r"^source=\((.*)\)\s*$", text, re.M)
    if m:
        # source=("file::url") with $pkgname/$_pkgname/$pkgver/$url refs:
        # expand them the way makepkg would before comparing to .SRCINFO.
        src = m.group(1).strip().strip('"')
        for var in ("_pkgname", "pkgname", "pkgver", "url"):
            if var in out:
                src = src.replace(f"${var}", out[var]).replace(
                    f"${{{var}}}", out[var]
                )
        out["source"] = src
    m = re.search(r"^sha256sums=\((.*)\)\s*$", text, re.M)
    if m:
        out["sha256sums"] = m.group(1).strip().strip("'\"")
    return out


def parse_srcinfo(path: Path) -> dict[str, str]:
    out: dict[str, str] = {}
    for line in path.read_text().splitlines():
        line = line.strip()
        for key in ("pkgver", "source", "sha256sums"):
            if line.startswith(f"{key} = "):
                out[key] = line.split(" = ", 1)[1]
    return out


def cargo_version(manifest: Path) -> str:
    m = re.search(r'^version = "([^"]+)"', manifest.read_text(), re.M)
    if not m:
        raise SystemExit(f"cannot find version in {manifest}")
    return m.group(1)


def main() -> int:
    pkgvers: dict[str, str] = {}
    for pkg, d in PKGS.items():
        pb = d / "PKGBUILD"
        si = d / ".SRCINFO"
        if not pb.exists():
            fail(f"{pkg}: {pb} missing")
            continue
        if not si.exists():
            fail(f"{pkg}: {si} missing")
            continue
        p, s = parse_pkgbuild(pb), parse_srcinfo(si)
        for key in ("pkgver", "source", "sha256sums"):
            if key not in p:
                fail(f"{pkg}: PKGBUILD has no {key}")
            elif key not in s:
                fail(f"{pkg}: .SRCINFO has no {key}")
            elif p[key] != s[key]:
                fail(f"{pkg}: {key} mismatch PKGBUILD={p[key]!r} .SRCINFO={s[key]!r}")
        if "pkgver" in p:
            pkgvers[pkg] = p["pkgver"]

    if len(set(pkgvers.values())) > 1:
        fail(f"pkgver differs between packages: {pkgvers}")

    if pkgvers:
        repo_ver = next(iter(pkgvers.values()))
        for manifest in (
            ROOT / "apps/omaterm/Cargo.toml",
            ROOT / "crates/omaterm-cli/Cargo.toml",
        ):
            cv = cargo_version(manifest)
            if cv != repo_ver:
                fail(f"{manifest.relative_to(ROOT)} version {cv} != pkgver {repo_ver}")

    if failures:
        print(f"{len(failures)} packaging check(s) failed", file=sys.stderr)
        return 1
    print("packaging sync OK")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
