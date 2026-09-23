#!/usr/bin/env bash
set -euo pipefail

# PR02 freezes twenty frontend business files twice: the donor snapshot is a
# traceable evidence copy and the live build inputs must remain byte-identical.
manifest="docs/migration/media/pr02-donor-sha256.txt"
test -s "$manifest" || { echo "missing PR02 donor manifest" >&2; exit 1; }

hash_file() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi; }

count=0
while read -r checksum path; do
  [[ "$checksum" =~ ^[0-9a-f]{64}$ ]] || continue
  case "$path" in
    web/donors/media-v2/src/*)
      live="web/src/${path#web/donors/media-v2/src/}"
      test -f "$path" && test -f "$live" || { echo "missing frozen PR02 file: $path or $live" >&2; exit 1; }
      test "$(hash_file "$path")" = "$checksum" || { echo "staged donor mismatch: $path" >&2; exit 1; }
      test "$(hash_file "$live")" = "$checksum" || { echo "live donor mismatch: $live" >&2; exit 1; }
      count=$((count+1));;
  esac
done < "$manifest"
test "$count" = 20 || { echo "expected 20 frozen PR02 frontend files, got $count" >&2; exit 1; }
echo "PR02 donor manifest: $count frozen frontend files verified"
