#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$repo_root/scripts/check-radar-donor-manifest.sh"
manifest="$repo_root/docs/donor-manifests/radar-v2-6bfbe581.sha256"

test -x "$checker" || { echo "missing executable Radar donor checker" >&2; exit 1; }
test -s "$manifest" || { echo "missing Radar donor manifest" >&2; exit 1; }

"$checker"

scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT

while read -r _checksum label; do
  [[ "$label" == target:* ]] || continue
  path="${label#target:}"
  mkdir -p "$scratch/$(dirname "$path")"
  cp "$repo_root/$path" "$scratch/$path"
done < "$manifest"

printf '\n// intentional mutation\n' >> "$scratch/web/src/admin/sections/radar.ts"
if RADAR_DONOR_TARGET_ROOT="$scratch" "$checker" >/dev/null 2>&1; then
  echo "Radar donor checker accepted a mutated frozen file" >&2
  exit 1
fi

extra_manifest="$scratch/extra.sha256"
cp "$manifest" "$extra_manifest"
printf '%064d  target:web/src/admin/sections/not-radar.ts\n' 0 >> "$extra_manifest"
if RADAR_DONOR_MANIFEST="$extra_manifest" "$checker" >/dev/null 2>&1; then
  echo "Radar donor checker accepted unexpected target coverage" >&2
  exit 1
fi

echo "Radar donor manifest self-test passed"
