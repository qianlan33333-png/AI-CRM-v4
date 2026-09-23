#!/usr/bin/env bash
set -euo pipefail

# PR05 closure gate. This is intentionally read-only: it compares the frozen
# donor evidence, checks the active build/shell boundary, and never builds or
# serves either repository.

repo_root="$(git rev-parse --show-toplevel)"
donor_root="${PR05_DONOR_ROOT:-/private/tmp/aicrm-v2-pr04-donor}"
expected_donor_sha="6bfbe5816bb89913c70adaca87d6a486260e016e"
staged_root="$repo_root/web/donors/coupons-v2/src"
hash_file="$repo_root/docs/migration/coupon/pr05-donor-sha256.txt"
shell_file="$repo_root/internal/webshell/templates/admin_base.html"
contract_file="$repo_root/internal/webshell/contract.go"
navigation_file="$repo_root/internal/webshell/static/admin_console/admin-navigation.v3.json"
build_file="$donor_root/web/scripts/build.mjs"
main_file="$donor_root/web/src/admin/main.ts"
legacy_file="$donor_root/web/src/admin/legacy.ts"

if ! git -C "$donor_root" rev-parse --verify HEAD >/dev/null 2>&1; then
  printf 'FAIL donor git tree not found: %s\n' "$donor_root" >&2
  exit 1
fi
actual_donor_sha="$(git -C "$donor_root" rev-parse HEAD)"
if [[ "$actual_donor_sha" != "$expected_donor_sha" ]]; then
  printf 'FAIL donor SHA expected=%s actual=%s\n' "$expected_donor_sha" "$actual_donor_sha" >&2
  exit 1
fi
if ! git -C "$donor_root" diff --quiet HEAD -- || ! git -C "$donor_root" diff --cached --quiet; then
  printf 'FAIL donor worktree/index is not clean: %s\n' "$donor_root" >&2
  exit 1
fi
if [[ ! -d "$staged_root" || ! -f "$hash_file" ]]; then
  printf 'FAIL PR05 frontend evidence is absent; apply prep 4727238 and audit e8263f first\n' >&2
  exit 1
fi

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

frontend_files=(
  "admin/controller.ts"
  "admin/main.ts"
  "admin/nav.json"
  "admin/registry.json"
  "admin/templates/couponData.html"
  "admin/templates/couponForm.html"
  "admin/templates/coupons.html"
  "api/admin.ts"
  "api/generated/health.schemas.ts"
  "api/generated/p4-coupon-compat/p4-coupon-compat.ts"
  "api/transport.ts"
  "shared/api/client.ts"
  "shared/api/mockData.ts"
  "shared/api/types.ts"
  "shared/ui/download.ts"
  "shared/ui/feedback.ts"
  "shared/ui/picker.ts"
  "shared/ui/runtime.ts"
  "shared/ui/tokens.css"
)

failed=0
checked=0
for relative_path in "${frontend_files[@]}"; do
  donor_file="$donor_root/web/src/$relative_path"
  staged_file="$staged_root/$relative_path"
  if [[ ! -f "$donor_file" || ! -f "$staged_file" ]]; then
    printf 'FAIL missing evidence path=%s\n' "$relative_path" >&2
    failed=1
    continue
  fi
  donor_hash="$(sha256 "$donor_file")"
  staged_hash="$(sha256 "$staged_file")"
  ledger_hash="$(awk -v path="web/src/$relative_path" '$2 == path { print $1; exit }' "$hash_file")"
  if [[ -z "$ledger_hash" || "$donor_hash" != "$ledger_hash" || "$staged_hash" != "$donor_hash" ]] || ! cmp -s "$donor_file" "$staged_file"; then
    printf 'FAIL byte/hash gate path=%s donor=%s ledger=%s staged=%s\n' "$relative_path" "$donor_hash" "$ledger_hash" "$staged_hash" >&2
    failed=1
  else
    printf 'PASS byte/hash %s\n' "$relative_path"
  fi
  checked=$((checked + 1))
done

actual_file_count="$(find "$staged_root" -type f -print | wc -l | tr -d ' ')"
if [[ "$actual_file_count" != "19" ]]; then
  printf 'FAIL evidence inventory is not exactly 19 files\n' >&2
  failed=1
else
  printf 'PASS evidence inventory files=19\n'
fi

active_templates=(
  "$staged_root/admin/templates/coupons.html"
  "$staged_root/admin/templates/couponForm.html"
)
if rg -n 'class="(shell|side|side-nav)"|<aside[^>]*class="side' "${active_templates[@]}" >/dev/null 2>&1; then
  printf 'FAIL active donor fragments contain a second shell/sidebar\n' >&2
  failed=1
else
  printf 'PASS active donor fragments contain no donor shell/sidebar\n'
fi

if ! rg -q 'admin: path\.join\(SRC, .admin/main\.ts.' "$build_file" || ! rg -q 'path\.join\(DIST, .admin.' "$build_file"; then
  printf 'FAIL donor build entry/output contract is not visible: %s\n' "$build_file" >&2
  failed=1
else
  printf 'PASS donor actual admin build entry/output\n'
fi
if ! rg -q 'import\("\./legacy"\)' "$main_file" || ! rg -q 'new AdminController' "$legacy_file"; then
  printf 'FAIL donor frozen browser chain main.ts -> legacy.ts -> AdminController is not visible\n' >&2
  failed=1
else
  printf 'PASS donor frozen browser chain main.ts -> legacy.ts -> AdminController\n'
fi

if [[ ! -f "$shell_file" || ! -f "$contract_file" ]]; then
  printf 'FAIL v3 PR10 shell/contract files are absent\n' >&2
  failed=1
else
  sidebar_count="$(rg -o 'class="admin-sidebar"' "$shell_file" | wc -l | tr -d ' ')"
  if [[ "$sidebar_count" != "1" ]]; then
    printf 'FAIL v3 admin_base sidebar count=%s expected=1\n' "$sidebar_count" >&2
    failed=1
  else
    printf 'PASS v3 admin_base has one admin-sidebar\n'
  fi
  if rg -n '<aside[^>]*class="side"|class="side-nav"' "$shell_file" >/dev/null 2>&1; then
    printf 'FAIL v3 admin_base contains donor sidebar markup\n' >&2
    failed=1
  else
    printf 'PASS v3 admin_base contains no donor sidebar markup\n'
  fi
  if ! rg -q '"api\.admin_coupons_page":.*"/admin/coupons"' "$contract_file"; then
    printf 'FAIL PR10 Coupons canonical endpoint/route is not visible\n' >&2
    failed=1
  elif [[ -f "$navigation_file" ]] && rg -Uq '"key": "coupons",\s+"label": "优惠券",\s+"endpoint": "api\.admin_coupons_page",\s+"href": "/admin/coupons",' "$navigation_file"; then
    printf 'PASS PR10 Coupons route/nav ownership is V3 JSON-owned\n'
  elif rg -q 'Key: "coupons".*Endpoint: "api\.admin_coupons_page"' "$contract_file"; then
    printf 'PASS PR10 Coupons route/nav ownership is v3-owned\n'
  else
    printf 'FAIL PR10 Coupons route/nav ownership is not visible\n' >&2
    failed=1
  fi
fi

printf 'SUMMARY donor_sha=%s files=%d active_rule_templates=2 excluded_evidence=1\n' "$actual_donor_sha" "$checked"
if [[ "$failed" -ne 0 ]]; then
  exit 1
fi
printf 'PASS PR05 closure gate\n'
