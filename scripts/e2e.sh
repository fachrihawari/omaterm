#!/bin/sh
# Runs the end-to-end tests: the real omaterm in a headless sway of its
# own, never on your desktop. Needs sway, wtype, grim and dbus-daemon.
# Screenshots go to e2e/out.
set -e
cd "$(dirname "$0")/.."
mkdir -p e2e/out
OMATERM_E2E=1 OMATERM_E2E_SHOTS="$PWD/e2e/out" go test -tags e2e -count=1 -v "$@" ./e2e/
