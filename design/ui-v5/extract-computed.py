#!/usr/bin/env python3
"""P0 helper: extract computed styles + bounds from the frozen reference.

Usage:
  1. Drop the exact user-supplied HTML at design/ui-v5/reference.html.
  2. Serve it offline with vendored CDN responses, open in the frozen browser.
  3. Paste the SELECTORS list into DevTools and save output as
     design/ui-v5/computed-styles.json (one object per selector with
     rect + computed style + pseudo-element styles).

This script only validates the manifest skeleton; it does not claim any
browser measurement until a real capture is recorded.
"""
import json
import sys
from pathlib import Path

SELECTORS = [
    "#projectsSidebar",
    "#projectsResizer",
    "#inspector",
    "#inspectorResizer",
    ".top-tab.active",
    ".terminal-pane.active",
    ".pane-toolbar",
    ".right-tab.active",
    "#toast",
]

ROOT = Path(__file__).resolve().parents[2]


def main():
    ref = ROOT / "design" / "ui-v5" / "reference.html"
    if not ref.is_file():
        print(f"MISSING: {ref} — drop exact supplied HTML here first")
        return 1
    manifest_path = ROOT / "design" / "ui-v5" / "manifest.json"
    manifest = json.loads(manifest_path.read_text())
    if not manifest["reference"]["sha256"]:
        print("NEXT: record sha256 of reference.html into manifest.json")
        return 2
    print(f"OK: reference present ({ref.stat().st_size} bytes), sha pinned")
    print(f"NEXT: capture selectors: {', '.join(SELECTORS)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
