#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"
DONOR_DIR="${AICRM_AUTOMATIONOPS_DONOR_DIR:-/tmp/aicrm-v2-read.QQKBOz/repo}"
DONOR_SHA="${AICRM_AUTOMATIONOPS_DONOR_SHA:-6bfbe5816bb89913c70adaca87d6a486260e016e}"
LEDGER="$REPO_ROOT/docs/migration/automationops/donor-sha256.txt"
ARCHIVE_ROOT="$REPO_ROOT/web/donors/automation-operations-v2/src"

fail() {
  printf 'automationops donor check: FAIL: %s\n' "$*" >&2
  exit 1
}

sha_file() {
  shasum -a 256 "$1" | awk '{print $1}'
}

sha_stdin() {
  shasum -a 256 | awk '{print $1}'
}

test -d "$DONOR_DIR/.git" || fail "missing donor checkout: $DONOR_DIR"
test -f "$LEDGER" || fail "missing digest ledger"
resolved="$(git -C "$DONOR_DIR" rev-parse "$DONOR_SHA^{commit}")"
[[ "$resolved" == "$DONOR_SHA" ]] || fail "donor resolved to $resolved"

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/automationops-donor.XXXXXX")"
trap 'rm -rf "$tmp_dir"' EXIT
expected="$tmp_dir/expected"
actual="$tmp_dir/actual"
: > "$expected"
count=0

while IFS=' ' read -r expected_hash reference; do
  [[ -n "${reference:-}" ]] || continue
  [[ "$reference" == source:web/* ]] || fail "invalid ledger reference: $reference"
  source_path="${reference#source:}"
  relative="${source_path#web/src/}"
  target="$ARCHIVE_ROOT/$relative"
  active="$REPO_ROOT/$source_path"
  printf '%s\n' "$target" >> "$expected"
  test -f "$target" || fail "missing archive file: $relative"
  test -f "$active" || fail "missing active source: $source_path"
  donor_hash="$(git -C "$DONOR_DIR" show "$DONOR_SHA:$source_path" | sha_stdin)"
  [[ "$donor_hash" == "$expected_hash" ]] || fail "donor drift: $source_path"
  [[ "$(sha_file "$target")" == "$expected_hash" ]] || fail "archive drift: $relative"
  [[ "$(sha_file "$active")" == "$expected_hash" ]] || fail "active source drift: $source_path"
  git -C "$DONOR_DIR" show "$DONOR_SHA:$source_path" | cmp -s - "$target" || fail "archive byte mismatch: $relative"
  git -C "$DONOR_DIR" show "$DONOR_SHA:$source_path" | cmp -s - "$active" || fail "active byte mismatch: $source_path"
  count=$((count + 1))
done < "$LEDGER"

[[ "$count" -eq 19 ]] || fail "expected 19 files, found $count"
find "$ARCHIVE_ROOT" -type f -print | LC_ALL=C sort > "$actual"
LC_ALL=C sort "$expected" -o "$expected"
cmp -s "$expected" "$actual" || fail "archive contains missing or unexpected files"

if grep -En '<aside|class="side"|\.side([^[:alnum:]_]|$)' \
  "$ARCHIVE_ROOT/admin/templates/automation.html" \
  "$ARCHIVE_ROOT/admin/templates/audienceEdit.html" >/dev/null; then
  fail "business templates contain a donor sidebar"
fi

if ! grep -F "AI 人群包 API 不等于群发任务创建契约" \
  "$ARCHIVE_ROOT/admin/controller.ts" >/dev/null; then
  fail "blocked broadcast boundary is missing"
fi

printf 'automationops donor check: PASS (%s; %d byte-exact archive and active files)\n' "$DONOR_SHA" "$count"
