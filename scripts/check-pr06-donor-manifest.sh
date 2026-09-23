#!/usr/bin/env bash

# Verify the PR06 Group Ops frontend archive without compiling or rewriting it.
#
# The donor checkout is intentionally an input, not a dependency of v3.  The
# default path is the audit worktree used when this manifest was frozen; CI or
# a later audit must pass AICRM_PR06_DONOR_DIR when that path is unavailable.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"
PR06_ROOT="${AICRM_PR06_ROOT:-$REPO_ROOT}"
DONOR_DIR="${AICRM_PR06_DONOR_DIR:-/tmp/aicrm-v2-audit.yN3jmr}"
DONOR_SHA="${AICRM_PR06_DONOR_SHA:-6bfbe5816bb89913c70adaca87d6a486260e016e}"
SHA_FILE="$PR06_ROOT/docs/migration/groupops/pr06-donor-sha256.txt"
TARGET_ROOT="$PR06_ROOT/web/donors/groupops-v2/src"

fail() {
  printf 'PR06 donor manifest check: FAIL: %s\n' "$*" >&2
  exit 1
}

sha256_file() {
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

sha256_stdin() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | awk '{print $1}'
    return
  fi
  fail "neither shasum nor sha256sum is available"
}

command -v git >/dev/null 2>&1 || fail "git is required"
command -v cmp >/dev/null 2>&1 || fail "cmp is required"
command -v grep >/dev/null 2>&1 || fail "grep is required"
test -f "$SHA_FILE" || fail "missing SHA evidence: $SHA_FILE"
test -d "$TARGET_ROOT" || fail "missing donor archive root: $TARGET_ROOT"
test -d "$DONOR_DIR" || fail "missing donor checkout: $DONOR_DIR (set AICRM_PR06_DONOR_DIR)"

resolved_sha="$(git -C "$DONOR_DIR" rev-parse "$DONOR_SHA^{commit}")" \
  || fail "donor SHA is not available: $DONOR_SHA"
[[ "$resolved_sha" == "$DONOR_SHA" ]] \
  || fail "donor resolved to $resolved_sha, expected $DONOR_SHA"

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/pr06-donor-check.XXXXXX")"
trap 'rm -rf "$tmp_dir"' EXIT
expected_targets="$tmp_dir/expected-targets"
actual_targets="$tmp_dir/actual-targets"
: > "$expected_targets"

source_count=0
target_evidence_count=0
active_source_count=0
v3_owned_active_count=0

while IFS=' ' read -r expected_hash reference; do
  [[ -n "${reference:-}" ]] || continue
  case "$reference" in
    source:web/*)
      source_path="${reference#source:}"
      target_path="$PR06_ROOT/web/donors/groupops-v2/${source_path#web/}"
      source_count=$((source_count + 1))
      printf '%s\n' "$target_path" >> "$expected_targets"

      test -f "$target_path" \
        || fail "missing target for $source_path: $target_path"
      source_hash="$(git -C "$DONOR_DIR" show "$DONOR_SHA:$source_path" | sha256_stdin)" \
        || fail "unable to read donor object: $DONOR_SHA:$source_path"
      [[ "$source_hash" == "$expected_hash" ]] \
        || fail "source SHA drift for $source_path: $source_hash != $expected_hash"
      target_hash="$(sha256_file "$target_path")"
      [[ "$target_hash" == "$expected_hash" ]] \
        || fail "target SHA drift for $target_path: $target_hash != $expected_hash"
      git -C "$DONOR_DIR" show "$DONOR_SHA:$source_path" \
        | cmp -s - "$target_path" \
        || fail "byte mismatch: $source_path -> $target_path"
      if [[ "$source_path" == "web/src/api/admin.test.ts" ]]; then
        # This shared executable characterization suite is now v3-owned so it
        # can cover the HXC dashboard. The Group Ops archive above remains
        # byte-exact and the suite is still executed by npm test.
        v3_owned_active_count=$((v3_owned_active_count + 1))
        continue
      fi
      active_path="$PR06_ROOT/$source_path"
      test -f "$active_path" \
        || fail "missing active build source for $source_path: $active_path"
      active_hash="$(sha256_file "$active_path")"
      [[ "$active_hash" == "$expected_hash" ]] \
        || fail "active source SHA drift for $active_path: $active_hash != $expected_hash"
      git -C "$DONOR_DIR" show "$DONOR_SHA:$source_path" \
        | cmp -s - "$active_path" \
        || fail "byte mismatch: $source_path -> $active_path"
      active_source_count=$((active_source_count + 1))
      ;;
    target:web/donors/groupops-v2/src/*)
      target_evidence_count=$((target_evidence_count + 1))
      ;;
  esac
done < "$SHA_FILE"

[[ "$source_count" -eq 35 ]] \
  || fail "expected 35 frontend source entries, found $source_count"
[[ "$target_evidence_count" -eq "$source_count" ]] \
  || fail "target evidence count $target_evidence_count != source count $source_count"
[[ "$active_source_count" -eq $((source_count - v3_owned_active_count)) ]] \
  || fail "active source count $active_source_count does not cover donor sources minus $v3_owned_active_count v3-owned test files"

find "$TARGET_ROOT" -type f -print | LC_ALL=C sort > "$actual_targets"
LC_ALL=C sort "$expected_targets" -o "$expected_targets"
cmp -s "$expected_targets" "$actual_targets" \
  || fail "archive contains a missing or unexpected frontend file"

# The v3 PR10 shell is the only sidebar.  These business templates are
# verified here so a donor snapshot cannot silently grow a second shell.
if grep -En '<aside|class="side"|\.side([^[:alnum:]_]|$)' \
    "$TARGET_ROOT/admin/templates/groupops.html" \
    "$TARGET_ROOT/admin/templates/groupopsDetail.html" >/dev/null; then
  fail "Group Ops business templates contain a donor sidebar"
fi

printf 'PR06 donor manifest check: PASS (donor %s; %d archive files; %d active build files; %d v3-owned test files; SHA-256 + cmp)\n' \
  "$DONOR_SHA" "$source_count" "$active_source_count" "$v3_owned_active_count"
printf 'PR06 archive remains byte-exact and archive-only; mount through the v3 PR10 shell.\n'
