#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
donor_root="${AICRM_SERVICE_PERIOD_MEMBER_GRID_DONOR_DIR:-$repo_root/.ci-donor-sidebar}"
staged_root="$repo_root/internal/product/http/member_grid_donor"
manifest="$staged_root/SHA256SUMS"
frozen_sha="dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f"

[[ -f "$manifest" ]] || { echo "missing member-grid donor manifest" >&2; exit 1; }
[[ -d "$staged_root" ]] || { echo "missing embedded member-grid donor" >&2; exit 1; }
[[ -d "$donor_root/.git" ]] || { echo "missing dd8 donor checkout: $donor_root" >&2; exit 1; }
actual_sha="$(git -C "$donor_root" rev-parse HEAD)"
[[ "$actual_sha" == "$frozen_sha" ]] || { echo "member-grid donor SHA mismatch: expected $frozen_sha got $actual_sha" >&2; exit 1; }

hash_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

count=0
while read -r expected relative; do
  [[ "$expected" =~ ^[0-9a-f]{64}$ ]] || continue
  case "$relative" in
    templates/*) donor_file="$donor_root/aicrm_next/extensions/commerce/service_period/templates/${relative#templates/}" ;;
    static/admin_console/*) donor_file="$donor_root/aicrm_next/extensions/commerce/service_period/static/admin_console/${relative#static/admin_console/}" ;;
    static/icons/*) donor_file="$donor_root/aicrm_next/extensions/commerce/service_period/static/icons/${relative#static/icons/}" ;;
    *) echo "invalid member-grid donor manifest path: $relative" >&2; exit 1 ;;
  esac
  staged_file="$staged_root/$relative"
  [[ -f "$donor_file" && -f "$staged_file" ]] || { echo "missing donor or embedded file: $relative" >&2; exit 1; }
  donor_hash="$(hash_file "$donor_file")"
  staged_hash="$(hash_file "$staged_file")"
  [[ "$donor_hash" == "$expected" && "$staged_hash" == "$expected" ]] || { echo "member-grid donor hash mismatch: $relative" >&2; exit 1; }
  cmp -s "$donor_file" "$staged_file" || { echo "member-grid donor byte mismatch: $relative" >&2; exit 1; }
  count=$((count + 1))
done < "$manifest"
[[ "$count" == 24 ]] || { echo "member-grid donor manifest count mismatch: $count" >&2; exit 1; }
echo "service-period member-grid dd8 freeze PASS: donor=$actual_sha files=$count"
