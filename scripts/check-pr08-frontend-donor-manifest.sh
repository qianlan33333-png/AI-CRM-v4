#!/usr/bin/env bash
set -euo pipefail

# PR08 is intentionally a two-file, byte-exact frontend archive.  This gate
# checks the frozen donor object, the hash manifest, and the fragment-only
# target.  It does not build or run the v2 web shell.

repo_root="$(git rev-parse --show-toplevel)"
donor_root="${PR08_DONOR_DIR:-/tmp/aicrm-v2-audit.yN3jmr}"
frozen_sha="${PR08_DONOR_SHA:-6bfbe5816bb89913c70adaca87d6a486260e016e}"
target_root="$repo_root/web/donors/operation-cycles-v2/src"
hash_manifest="$repo_root/docs/donor-manifests/pr08-operation-cycles.sha256"
yaml_manifest="$repo_root/docs/donor-manifests/pr08-operation-cycles.yaml"

die() {
  echo "PR08 frontend donor check failed: $*" >&2
  exit 1
}

hash_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    die "no SHA-256 utility found (sha256sum or shasum required)"
  fi
}

hash_stdin() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 | awk '{print $1}'
  else
    die "no SHA-256 utility found (sha256sum or shasum required)"
  fi
}

[[ -f "$hash_manifest" ]] || die "missing hash manifest: $hash_manifest"
[[ -f "$yaml_manifest" ]] || die "missing YAML manifest: $yaml_manifest"
[[ -d "$target_root" ]] || die "missing staged donor root: $target_root"
[[ -d "$donor_root" ]] || die "missing donor git checkout: $donor_root"
git -C "$donor_root" rev-parse --git-dir >/dev/null 2>&1 || die "donor path is not a git checkout: $donor_root"

# The YAML is deliberately checked without introducing a parser dependency;
# the shape checks below guard the fields consumed by this script.
grep -qE "^source_commit: ${frozen_sha}$" "$yaml_manifest" || die "YAML source_commit does not match $frozen_sha"
grep -qE '^  exact_file_count: 2$' "$yaml_manifest" || die "YAML exact_file_count is not 2"
grep -qE '^  byte_exact_staged_under: web/donors/operation-cycles-v2/src$' "$yaml_manifest" || die "YAML staged root is not the allowlisted root"

resolved_sha="$(git -C "$donor_root" rev-parse "${frozen_sha}^{commit}")"
[[ "$resolved_sha" == "$frozen_sha" ]] || die "donor SHA mismatch: expected $frozen_sha, got $resolved_sha"

expected_files=(
  "admin/templates/cycles.html"
  "admin/templates/cyclesDetail.html"
)

manifest_hash() {
  local reference="$1"
  local count value
  count="$(awk -v ref="$reference" '$2 == ref { count += 1 } END { print count + 0 }' "$hash_manifest")"
  [[ "$count" == "1" ]] || die "expected exactly one hash entry for $reference, got $count"
  value="$(awk -v ref="$reference" '$2 == ref { print $1 }' "$hash_manifest")"
  [[ "$value" =~ ^[0-9a-f]{64}$ ]] || die "invalid SHA-256 entry for $reference"
  printf '%s\n' "$value"
}

checked=0
for relative in "${expected_files[@]}"; do
  source_path="web/src/$relative"
  target_path="web/donors/operation-cycles-v2/src/$relative"
  source_ref="source:$source_path"
  target_ref="target:$target_path"
  target_file="$repo_root/$target_path"

  [[ -f "$target_file" ]] || die "missing target donor file: $target_path"
  expected_source="$(manifest_hash "$source_ref")"
  expected_target="$(manifest_hash "$target_ref")"
  [[ "$expected_source" == "$expected_target" ]] || die "source/target manifest hashes differ for $relative"

  actual_source="$(git -C "$donor_root" show "${frozen_sha}:$source_path" | hash_stdin)"
  actual_target="$(hash_file "$target_file")"
  [[ "$actual_source" == "$expected_source" ]] || die "donor hash mismatch for $source_path: expected $expected_source, got $actual_source"
  [[ "$actual_target" == "$expected_target" ]] || die "target hash mismatch for $target_path: expected $expected_target, got $actual_target"

  if ! git -C "$donor_root" show "${frozen_sha}:$source_path" | cmp -s - "$target_file"; then
    die "byte mismatch for $relative"
  fi
  echo "PASS $relative sha256=$actual_source cmp=PASS"
  checked=$((checked + 1))
done

[[ "$checked" == "2" ]] || die "checked frontend file count is $checked, expected 2"

# No shared runtime, CSS, script, icon, font, or other asset is allowed in
# the frozen target archive.  Such files would silently pull in excluded v2
# domains or a second shell.
actual_target_files="$(find "$target_root" -type f -print | sed "s#^$target_root/##" | LC_ALL=C sort)"
expected_target_files=$'admin/templates/cycles.html\nadmin/templates/cyclesDetail.html'
[[ "$actual_target_files" == "$expected_target_files" ]] || {
  echo "expected target files:" >&2
  printf '%s\n' "$expected_target_files" >&2
  echo "actual target files:" >&2
  printf '%s\n' "$actual_target_files" >&2
  die "staged donor file set differs from the two-file allowlist"
}

# Both files are HTML fragments.  A full v2 page or navigation shell would
# create a second sidebar when mounted by the v3 webshell.
if grep -R -nE '<!DOCTYPE|<html|<body|<aside|class="shell"|class="side"' "$target_root"; then
  die "staged donor contains a complete page or v2 sidebar shell"
fi

echo "PR08 frontend donor freeze PASS: donor=$resolved_sha files=$checked target=$target_root"
