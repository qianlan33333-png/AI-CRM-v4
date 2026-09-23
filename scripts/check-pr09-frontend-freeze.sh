#!/usr/bin/env bash
set -euo pipefail

# Read-only proof that the PR09 frontend archive is an exact snapshot of the
# selected v2 donor commit.  The donor is read through git objects; this check
# never copies or modifies donor files.

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"
DONOR_ROOT="${PR09_DONOR_ROOT:-}"
DONOR_SHA="6bfbe5816bb89913c70adaca87d6a486260e016e"
MANIFEST="$REPO_ROOT/docs/migration/adminops/pr09-donor-manifest.yaml"
LEDGER="$REPO_ROOT/docs/migration/adminops/pr09-donor-sha256.txt"
TARGET_ROOT="$REPO_ROOT/web/donors/adminops-v2/src"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

command -v git >/dev/null || fail "git is required"
command -v cmp >/dev/null || fail "cmp is required"
[[ -n "$DONOR_ROOT" ]] || fail "PR09_DONOR_ROOT must point to the checked-out frozen v2 donor"
if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
  fail "sha256sum or shasum is required"
fi

[[ -d "$DONOR_ROOT/.git" || -f "$DONOR_ROOT/HEAD" ]] || fail "donor worktree not found: $DONOR_ROOT"
[[ -f "$MANIFEST" ]] || fail "manifest not found: $MANIFEST"
[[ -f "$LEDGER" ]] || fail "SHA ledger not found: $LEDGER"
[[ -d "$TARGET_ROOT" ]] || fail "frontend archive not found: $TARGET_ROOT"

[[ "$(git -C "$DONOR_ROOT" rev-parse HEAD)" == "$DONOR_SHA" ]] || \
  fail "donor HEAD is not $DONOR_SHA"
[[ -z "$(git -C "$DONOR_ROOT" status --porcelain=v1)" ]] || \
  fail "donor worktree is dirty; refusing to certify a moving source"

expected_count="$(sed -n '/^  exact_file_count:/ { s/^[^:]*: *//; p; q; }' "$MANIFEST")"
[[ "$expected_count" == "16" ]] || fail "manifest exact_file_count is $expected_count, expected 16"

mapfile -t source_files < <(
  sed -n '/^  exact_files:/,/^  generated_contract_notes:/ {
    /^    - / { s/^    - //; p; }
  }' "$MANIFEST"
)
[[ "${#source_files[@]}" == "$expected_count" ]] || \
  fail "manifest lists ${#source_files[@]} frontend files, expected $expected_count"

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/pr09-front-freeze.XXXXXX")"
trap 'rm -rf "$tmp_dir"' EXIT

printf '%s\n' "${source_files[@]}" | sort >"$tmp_dir/expected-source"
find "$TARGET_ROOT" -type f -print \
  | sed "s#^$REPO_ROOT/##; s#^web/donors/adminops-v2/src/#web/src/#" \
  | sort >"$tmp_dir/actual-source"
cmp -s "$tmp_dir/expected-source" "$tmp_dir/actual-source" || \
  fail "archive file set differs from manifest"

checked=0
sha256_stream() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | awk '{ print $1 }'
  else
    shasum -a 256 | awk '{ print $1 }'
  fi
}

sha256_file() {
  sha256_stream < "$1"
}

for source in "${source_files[@]}"; do
  suffix="${source#web/src/}"
  [[ "$suffix" != "$source" ]] || fail "manifest source is outside web/src: $source"
  target="web/donors/adminops-v2/src/$suffix"
  target_file="$REPO_ROOT/$target"
  [[ -f "$target_file" ]] || fail "missing archive file: $target"

  source_expected="$(awk -v key="source:$source" '$2 == key { print $1; exit }' "$LEDGER")"
  target_expected="$(awk -v key="target:$target" '$2 == key { print $1; exit }' "$LEDGER")"
  [[ "$source_expected" =~ ^[0-9a-f]{64}$ ]] || fail "missing source SHA ledger row: $source"
  [[ "$target_expected" =~ ^[0-9a-f]{64}$ ]] || fail "missing target SHA ledger row: $target"

  source_actual="$(git -C "$DONOR_ROOT" show "$DONOR_SHA:$source" | sha256_stream)"
  target_actual="$(sha256_file "$target_file")"
  [[ "$source_actual" == "$source_expected" ]] || \
    fail "donor SHA mismatch for $source: $source_actual != $source_expected"
  [[ "$target_actual" == "$target_expected" ]] || \
    fail "archive SHA mismatch for $target: $target_actual != $target_expected"
  [[ "$source_actual" == "$target_actual" ]] || \
    fail "source/archive SHA mismatch: $source -> $target"
  git -C "$DONOR_ROOT" show "$DONOR_SHA:$source" | cmp -s - "$target_file" || \
    fail "cmp mismatch: $source -> $target"
  checked=$((checked + 1))
done

# PR09 templates are fragments. A second v2 shell/sidebar must never be
# smuggled into the exact archive or mounted by a future adapter.
if find "$TARGET_ROOT" -type f -exec grep -En '<aside|class="shell"|class="side"|class="side-nav"' {} + >/dev/null 2>&1; then
  fail "archive contains v2 shell/sidebar markup"
fi

# There are intentionally no PR09 CSS/image/font assets in the exact set.
if find "$TARGET_ROOT" -type f -print | grep -En '\.(css|scss|less|png|jpe?g|gif|svg|webp|woff2?|ttf|otf|ico)$' >/dev/null 2>&1; then
  fail "archive unexpectedly contains a PR09 external CSS/media/font asset"
fi

printf 'PASS: PR09 frontend freeze verified (%d files; donor %s; SHA-256 + cmp; no shell/sidebar or external assets)\n' "$checked" "$DONOR_SHA"
