#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
manifest="$repo_root/docs/migration/product/pr04-donor-sha256.txt"
staged_root="$repo_root/web/donors/products-v2/src"
donor_root="${PR04_DONOR_ROOT:-/tmp/aicrm-v2-audit.yN3jmr}"
frozen_sha="6bfbe5816bb89913c70adaca87d6a486260e016e"

[[ -f "$manifest" ]] || { echo "missing PR04 donor hash manifest: $manifest" >&2; exit 1; }
[[ -d "$staged_root" ]] || { echo "missing staged donor root: $staged_root" >&2; exit 1; }
[[ -d "$donor_root/.git" ]] || { echo "missing donor git checkout: $donor_root" >&2; exit 1; }

actual_sha="$(git -C "$donor_root" rev-parse HEAD)"
if [[ "$actual_sha" != "$frozen_sha" ]]; then
  echo "PR04 donor SHA mismatch: expected $frozen_sha, got $actual_sha" >&2
  exit 1
fi

hash_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    echo "no SHA-256 utility found (sha256sum or shasum required)" >&2
    exit 1
  fi
}

frontend_count=0
while read -r expected source; do
  [[ "$expected" =~ ^[0-9a-f]{64}$ ]] || continue
  [[ "$source" == web/src/* ]] || continue

  relative="${source#web/src/}"
  donor_file="$donor_root/$source"
  staged_file="$staged_root/$relative"
  [[ -f "$donor_file" ]] || { echo "missing donor file: $source" >&2; exit 1; }
  [[ -f "$staged_file" ]] || { echo "missing staged file: $relative" >&2; exit 1; }

  donor_hash="$(hash_file "$donor_file")"
  staged_hash="$(hash_file "$staged_file")"
  if [[ "$donor_hash" != "$expected" || "$staged_hash" != "$expected" ]]; then
    echo "PR04 donor hash mismatch: $source expected=$expected donor=$donor_hash staged=$staged_hash" >&2
    exit 1
  fi
  if ! cmp -s "$donor_file" "$staged_file"; then
    echo "PR04 donor byte mismatch: $source" >&2
    exit 1
  fi
  frontend_count=$((frontend_count + 1))
done < "$manifest"

[[ "$frontend_count" -eq 24 ]] || {
  echo "PR04 frontend manifest count mismatch: expected 24, got $frontend_count" >&2
  exit 1
}

expected_paths="$(mktemp)"
actual_paths="$(mktemp)"
trap 'rm -f "$expected_paths" "$actual_paths"' EXIT
sed -nE 's/^[0-9a-f]{64}[[:space:]]+web\/src\/(.*)$/\1/p' "$manifest" | sort > "$expected_paths"
(cd "$staged_root" && find . -type f -print | sed 's#^\./##' | sort) > "$actual_paths"
if ! diff -u "$expected_paths" "$actual_paths"; then
  echo "PR04 staged donor file set differs from manifest" >&2
  exit 1
fi

echo "PR04 donor frontend freeze PASS: donor=$actual_sha files=$frontend_count"
