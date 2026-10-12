#!/usr/bin/env bash
# Tests release-size.sh: within budget passes, over either budget fails.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/dist"
head -c 1048576 /dev/urandom > "$tmp/dist/app"
(cd "$tmp/dist" && zip -qj "$tmp/r.zip" app)
RELEASE_MAX_UNPACKED_MB=2 RELEASE_MAX_ZIP_MB=2 "$here/release-size.sh" "$tmp/r.zip" "$tmp/dist" >/dev/null || { echo "FAIL: a release within budget was refused"; exit 1; }
if RELEASE_MAX_UNPACKED_MB=0 RELEASE_MAX_ZIP_MB=2 "$here/release-size.sh" "$tmp/r.zip" "$tmp/dist" >/dev/null 2>&1; then echo "FAIL: an unpacked size over budget passed"; exit 1; fi
if RELEASE_MAX_UNPACKED_MB=2 RELEASE_MAX_ZIP_MB=0 "$here/release-size.sh" "$tmp/r.zip" "$tmp/dist" >/dev/null 2>&1; then echo "FAIL: a zip over budget passed"; exit 1; fi
echo "release-size.sh: ok"
