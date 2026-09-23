#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$script_dir/.." && pwd)"
donor_dir="${AICRM_CHANNEL_DONOR_DIR:-${PR07_DONOR_DIR:-}}"
donor_sha="6bfbe5816bb89913c70adaca87d6a486260e016e"
hash_file="$repo_root/docs/migration/channel/donor-sha256.txt"

fail() { printf 'channel donor manifest check: FAIL: %s\n' "$*" >&2; exit 1; }

sha256_stdin() {
  if command -v shasum >/dev/null 2>&1; then shasum -a 256 | awk '{print $1}'; return; fi
  if command -v sha256sum >/dev/null 2>&1; then sha256sum | awk '{print $1}'; return; fi
  fail "SHA-256 utility unavailable"
}

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'; return; fi
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'; return; fi
  fail "SHA-256 utility unavailable"
}

test -f "$hash_file" || fail "missing hash evidence"
test -n "$donor_dir" || fail "set AICRM_CHANNEL_DONOR_DIR or PR07_DONOR_DIR"
test -d "$donor_dir/.git" || fail "missing donor checkout: $donor_dir"
resolved_sha="$(git -C "$donor_dir" rev-parse HEAD)"
[[ "$resolved_sha" == "$donor_sha" ]] || fail "donor SHA drift: $resolved_sha"

source_count=0
active_count=0
while IFS=' ' read -r expected reference; do
  [[ "$expected" =~ ^[0-9a-f]{64}$ ]] || continue
  [[ "$reference" == source:* ]] || fail "invalid manifest reference: $reference"
  source_path="${reference#source:}"
  actual="$(git -C "$donor_dir" show "$donor_sha:$source_path" | sha256_stdin)"
  [[ "$actual" == "$expected" ]] || fail "source SHA drift: $source_path"
  source_count=$((source_count + 1))
  case "$source_path" in
    web/src/admin/templates/channels.html|web/src/admin/templates/channelForm.html)
      target="$repo_root/$source_path"
      test -f "$target" || fail "missing active template: $source_path"
      [[ "$(sha256_file "$target")" == "$expected" ]] || fail "active template SHA drift: $source_path"
      git -C "$donor_dir" show "$donor_sha:$source_path" | cmp -s - "$target" || fail "active template byte drift: $source_path"
      grep -q 'style=' "$target" || fail "channel inline style contract missing: $source_path"
      active_count=$((active_count + 1))
      ;;
  esac
done < "$hash_file"

[[ "$source_count" -eq 12 ]] || fail "expected 12 donor files, found $source_count"
[[ "$active_count" -eq 2 ]] || fail "expected 2 active templates, found $active_count"

if grep -ERn --include='*.go' --include='go.mod' --include='go.sum' \
  'github\.com/qianlan33333-png/AI-CRM-v2|replace[[:space:]]+github\.com/qianlan33333-png/AI-CRM-v2' \
  "$repo_root/go.mod" "$repo_root/go.sum" "$repo_root/internal" "$repo_root/cmd" >/dev/null; then
  fail "forbidden v2 runtime import detected"
fi

printf 'channel donor manifest check: PASS (donor %s; %d sources; %d byte-exact active templates; no v2 runtime import)\n' \
  "$resolved_sha" "$source_count" "$active_count"
