#!/usr/bin/env bash
set -euo pipefail

# PR07 donor freeze gate. This script is intentionally read-only with respect
# to both the donor checkout and the v3 worktree. PR07_DONOR_DIR must point
# at a clean checkout of the frozen donor commit.

readonly DONOR_SHA="6bfbe5816bb89913c70adaca87d6a486260e016e"
readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly REPO_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
readonly DONOR_ROOT="${PR07_DONOR_DIR:-}"
readonly MANIFEST="${REPO_ROOT}/docs/migration/automation/pr07-donor-manifest.yaml"
readonly LEDGER="${REPO_ROOT}/docs/migration/automation/pr07-donor-sha256.txt"
readonly TARGET_ROOT="${REPO_ROOT}/web/donors/automation-v2/src"

fail() {
  echo "PR07 frontend freeze FAIL: $*" >&2
  exit 1
}

[[ -n "${DONOR_ROOT}" ]] || fail "PR07_DONOR_DIR is required"
[[ -e "${DONOR_ROOT}/.git" ]] || fail "donor checkout not found: ${DONOR_ROOT}"
[[ -f "${MANIFEST}" ]] || fail "manifest not found: ${MANIFEST}"
[[ -f "${LEDGER}" ]] || fail "SHA ledger not found: ${LEDGER}"
[[ -d "${TARGET_ROOT}" ]] || fail "snapshot root not found: ${TARGET_ROOT}"

actual_donor_sha="$(git -C "${DONOR_ROOT}" rev-parse HEAD)"
[[ "${actual_donor_sha}" == "${DONOR_SHA}" ]] || fail "donor HEAD ${actual_donor_sha} != frozen ${DONOR_SHA}"

if [[ -n "$(git -C "${DONOR_ROOT}" status --porcelain --untracked-files=all)" ]]; then
  fail "donor checkout is dirty"
fi
git -C "${DONOR_ROOT}" diff --quiet "${DONOR_SHA}" -- || fail "donor worktree differs from frozen commit"
git -C "${DONOR_ROOT}" diff --cached --quiet "${DONOR_SHA}" -- || fail "donor index differs from frozen commit"

grep -Fqx "  commit: ${DONOR_SHA}" "${MANIFEST}" || fail "manifest donor commit does not match frozen SHA"

sha256() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  fail "neither shasum nor sha256sum is available"
}

expected_file="$(mktemp "${TMPDIR:-/tmp}/pr07-frontend-expected.XXXXXX")"
actual_file="$(mktemp "${TMPDIR:-/tmp}/pr07-frontend-actual.XXXXXX")"
trap 'rm -f "${expected_file}" "${actual_file}"' EXIT

expected_count=0
while IFS= read -r source_path; do
  [[ -n "${source_path}" ]] || continue
  printf '%s\n' "${source_path#web/src/}" >>"${expected_file}"
  expected_count=$((expected_count + 1))
done < <(sed -n '/^  exact_files:/,/^  immutable_excluded_domain_references:/p' "${MANIFEST}" | sed -n 's/^    - //p')

[[ "${expected_count}" -eq 20 ]] || fail "manifest exact_file count ${expected_count} != 20"

find "${TARGET_ROOT}" -type f -print | sed "s#^${TARGET_ROOT}/##" | LC_ALL=C sort >"${actual_file}"
LC_ALL=C sort -o "${expected_file}" "${expected_file}"
if ! diff -u "${expected_file}" "${actual_file}"; then
  fail "snapshot file set differs from manifest"
fi

checked=0
while IFS= read -r source_path; do
  [[ -n "${source_path}" ]] || continue
  source_file="${DONOR_ROOT}/${source_path}"
  target_file="${TARGET_ROOT}/${source_path#web/src/}"
  [[ -f "${source_file}" ]] || fail "donor source missing: ${source_path}"
  [[ -f "${target_file}" ]] || fail "snapshot target missing: ${source_path#web/src/}"
  git -C "${DONOR_ROOT}" cat-file -e "${DONOR_SHA}:${source_path}" || fail "source is not in frozen donor commit: ${source_path}"
  cmp -s "${source_file}" "${target_file}" || fail "cmp mismatch: ${source_path}"

  source_hash="$(sha256 "${source_file}")"
  target_hash="$(sha256 "${target_file}")"
  [[ "${source_hash}" == "${target_hash}" ]] || fail "SHA mismatch: ${source_path}"

  recorded_source_hash="$(awk -v key="source:${source_path}" '$2 == key { print $1; exit }' "${LEDGER}")"
  target_key="target:web/donors/automation-v2/src/${source_path#web/src/}"
  recorded_target_hash="$(awk -v key="${target_key}" '$2 == key { print $1; exit }' "${LEDGER}")"
  [[ "${recorded_source_hash}" == "${source_hash}" ]] || fail "ledger source hash mismatch: ${source_path}"
  [[ "${recorded_target_hash}" == "${target_hash}" ]] || fail "ledger target hash mismatch: ${source_path#web/src/}"

  printf 'OK %s (%s)\n' "${source_path}" "${source_hash}"
  checked=$((checked + 1))
done < <(sed -n '/^  exact_files:/,/^  immutable_excluded_domain_references:/p' "${MANIFEST}" | sed -n 's/^    - //p')

[[ "${checked}" -eq 20 ]] || fail "checked ${checked} files, expected 20"

if grep -REn '<aside|class="side"|class="side-nav"' "${TARGET_ROOT}" >/dev/null 2>&1; then
  fail "snapshot contains donor shell/sidebar markup"
fi

asset_candidates="$(git -C "${DONOR_ROOT}" ls-tree -r --name-only "${DONOR_SHA}" -- web/src | grep -Ei '(automation|agent|fixed|script|prompt|话术).*(css|scss|sass|less|svg|png|jpg|jpeg|webp|gif|ico|woff2?|ttf|otf)$' || true)"
[[ -z "${asset_candidates}" ]] || fail "unexpected automation-specific external assets:\n${asset_candidates}"

echo "PR07 frontend freeze PASS: donor ${DONOR_SHA}; ${checked}/20 files cmp+SHA verified; no shell or automation-specific external assets"
