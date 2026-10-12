#!/usr/bin/env bash
# Fails when the API release outgrows what a 1 GB Alpine root can deploy.
#
#   scripts/release-size.sh <release.zip> <unpacked dir>
#
# The instance unpacks the next release beside the current one before it
# switches, with the downloaded zip on the same disk. On prod on 2026-10-12
# (1 GB root, 256 MB swap, one release unpacked, a stale 39 MB zip) 137 MB were
# free; with 128 MB swap and no stale zip (ctech-cdk's deploy script deletes
# it) that is derived as 137 + 128 + 39 = about 304 MB free.
# Two releases side by side plus the zip must fit with margin, so the budgets
# are 140 MB unpacked and 48 MB zipped (the release was 118 MB / 39 MB). A
# release that grows past them fails CI instead of failing the deploy.
set -euo pipefail
ZIP="${1:?usage: release-size.sh <release.zip> <unpacked dir>}"
DIR="${2:?usage: release-size.sh <release.zip> <unpacked dir>}"
MAX_UNPACKED_MB="${RELEASE_MAX_UNPACKED_MB:-140}"
MAX_ZIP_MB="${RELEASE_MAX_ZIP_MB:-48}"
unpacked=$(du -sb "$DIR" | cut -f1)
zipped=$(stat -c%s "$ZIP")
mb() { echo $(( ($1 + 1048575) / 1048576 )); }
echo "release: unpacked $(mb "$unpacked") MB (budget $MAX_UNPACKED_MB), zip $(mb "$zipped") MB (budget $MAX_ZIP_MB)"
fail=0
if [ "$unpacked" -gt $(( MAX_UNPACKED_MB * 1048576 )) ]; then echo "::error::the unpacked release exceeds ${MAX_UNPACKED_MB} MB" >&2; fail=1; fi
if [ "$zipped" -gt $(( MAX_ZIP_MB * 1048576 )) ]; then echo "::error::the release zip exceeds ${MAX_ZIP_MB} MB" >&2; fail=1; fi
exit "$fail"
