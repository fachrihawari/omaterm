#!/bin/sh
# Runs the end-to-end tests: the real omaterm in a headless sway of its
# own, never on your desktop. Needs sway, wtype, grim and dbus-daemon.
# Screenshots go to e2e/out.
#
# Every scenario in e2e/ is a test, run together. A single one of them:
#
#   scripts/e2e.sh -run TestProjectProcessesOverTheDaemon
#
# TestProjectProcessesOverTheDaemon drives a project's processes over the
# daemon with the built binary: the file that defines them, the trust a
# command needs, the scrollback a restart carries over, a failure that
# restarts, and a changed definition that must be trusted again.
set -e
cd "$(dirname "$0")/.."
mkdir -p e2e/out
OMATERM_E2E=1 OMATERM_E2E_SHOTS="$PWD/e2e/out" go test -tags e2e -count=1 -v "$@" ./e2e/
