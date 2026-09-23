#!/usr/bin/env bash
set -euo pipefail

repository_root="${AICRM_SURVEY_ROOT:-$(git rev-parse --show-toplevel)}"
donor_root="${AICRM_SURVEY_DONOR_DIR:-}"
frozen_sha="6bfbe5816bb89913c70adaca87d6a486260e016e"
hash_manifest="$repository_root/docs/migration/survey/donor-sha256.txt"

fail() {
  printf 'survey donor manifest: FAIL: %s\n' "$*" >&2
  exit 1
}

hash_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    fail 'sha256sum or shasum is required'
  fi
}

test -f "$hash_manifest" || fail "missing $hash_manifest"
if [[ -n "$donor_root" ]]; then
  test -d "$donor_root/.git" || fail "invalid donor checkout: $donor_root"
  actual_sha="$(git -C "$donor_root" rev-parse HEAD)"
  [[ "$actual_sha" == "$frozen_sha" ]] || fail "donor SHA $actual_sha != $frozen_sha"
fi

count=0
while read -r expected file_name; do
  [[ "$expected" =~ ^[0-9a-f]{64}$ ]] || continue
  [[ "$file_name" == web/src/* ]] || fail "unexpected target path: $file_name"
  target="$repository_root/$file_name"
  test -f "$target" || fail "missing target: $file_name"
  [[ "$(hash_file "$target")" == "$expected" ]] || fail "target drift: $file_name"
  if [[ -n "$donor_root" ]]; then
    donor_file="$donor_root/$file_name"
    test -f "$donor_file" || fail "missing donor source: $file_name"
    [[ "$(hash_file "$donor_file")" == "$expected" ]] || fail "donor drift: $file_name"
    cmp -s "$donor_file" "$target" || fail "byte mismatch: $file_name"
  fi
  count=$((count + 1))
done < "$hash_manifest"

[[ "$count" -eq 16 ]] || fail "expected 16 unchanged donor files, got $count"
editor="$repository_root/web/src/admin/sections/questionnaireEditor.ts"
grep -q "from '../../api/questionnaireEditorV3'" "$editor" || fail 'missing v3 editor adapter boundary'
if grep -q '多维测评和分数规则未开放写入' "$editor"; then fail 'legacy assessment block remains'; fi
printf 'survey donor manifest: PASS (%d files; donor=%s; source-check=%s)\n' "$count" "$frozen_sha" "$([[ -n "$donor_root" ]] && printf enabled || printf skipped)"
