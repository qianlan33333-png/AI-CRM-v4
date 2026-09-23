#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
manifest="${RADAR_DONOR_MANIFEST:-$repo_root/docs/donor-manifests/radar-v2-6bfbe581.sha256}"
target_root="${RADAR_DONOR_TARGET_ROOT:-$repo_root}"

test -s "$manifest" || { echo "missing Radar donor manifest: $manifest" >&2; exit 1; }
grep -Fq 'Source commit: 6bfbe5816bb89913c70adaca87d6a486260e016e' "$manifest" || {
  echo "Radar donor manifest has the wrong source commit" >&2
  exit 1
}

hash_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  shasum -a 256 "$1" | awk '{print $1}'
}

scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT

cat > "$scratch/expected-targets" <<'EOF'
web/src/admin/controller.ts
web/src/admin/nav.json
web/src/admin/registry.json
web/src/admin/sections/qr.ts
web/src/admin/sections/radar.ts
web/src/api/admin.ts
web/src/api/generated/p4-radar/p4-radar.ts
web/src/api/transport.ts
web/src/shared/api/client.ts
web/src/shared/api/types.ts
EOF

awk 'NF == 2 && $1 ~ /^[0-9a-f]{64}$/ && $2 ~ /^target:/ { sub(/^target:/, "", $2); print $2 }' "$manifest" \
  | sort > "$scratch/actual-targets"

if ! diff -u "$scratch/expected-targets" "$scratch/actual-targets"; then
  echo "Radar donor target set is missing a file or contains unexpected coverage" >&2
  exit 1
fi

test "$(sort -u "$scratch/actual-targets" | wc -l | tr -d ' ')" = "$(wc -l < "$scratch/actual-targets" | tr -d ' ')" || {
  echo "duplicate target path in Radar donor manifest" >&2
  exit 1
}

source_count="$(awk 'NF == 2 && $1 ~ /^[0-9a-f]{64}$/ && $2 ~ /^source:/ { count++ } END { print count + 0 }' "$manifest")"
test "$source_count" -ge 60 || {
  echo "Radar donor source evidence is incomplete: expected at least 60 entries, got $source_count" >&2
  exit 1
}

while read -r checksum label; do
  [[ "$checksum" =~ ^[0-9a-f]{64}$ ]] || continue
  [[ "$label" == target:* ]] || continue
  path="${label#target:}"
  file="$target_root/$path"
  test -f "$file" || { echo "missing frozen Radar donor file: $path" >&2; exit 1; }
  actual="$(hash_file "$file")"
  test "$actual" = "$checksum" || { echo "Radar donor checksum mismatch: $path" >&2; exit 1; }
done < "$manifest"

echo "Radar donor manifest verified"
