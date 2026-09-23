#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$repo_root/scripts/check-ai-assistant-donor-manifest.sh"
donor_prefix="web/donors"
donor_name="ai-assistant-production"

test -x "$checker" || { echo "missing executable AI Assistant donor checker" >&2; exit 1; }
git -C "$repo_root" check-ignore -q web/donor-sources/source-index.json && {
  echo "source index is ignored; donor boundary test would not exercise its precise exemption" >&2
  exit 1
}
source_index_binding="$donor_prefix/$donor_name/static/send_content_readonly_detail.css"
grep -Fq "$source_index_binding" \
  "$repo_root/web/donor-sources/source-index.json" || {
  echo "source index fixture no longer contains the governed donor binding" >&2
  exit 1
}
"$checker"

# These fragments deliberately create fixture contents at runtime: the fixture
# verifies the scanner boundary without becoming a real repository reference.
audit_fixture="$repo_root/scripts/audit/ai_assistant_donor_gate_fixture_$$"
script_fixture="$repo_root/scripts/ai_assistant_donor_gate_fixture_$$.mjs"
runtime_fixture="$repo_root/internal/ai_assistant_donor_gate_fixture_$$"
trap 'rm -rf "$audit_fixture" "$script_fixture" "$runtime_fixture"' EXIT
mkdir -p "$audit_fixture" "$runtime_fixture"
printf 'audit source record: %s/%s\n' "$donor_prefix" "$donor_name" > "$audit_fixture/source-record.txt"
git -C "$repo_root" check-ignore -q "$audit_fixture/source-record.txt" && {
  echo "audit fixture is ignored; boundary test would not exercise rg" >&2
  exit 1
}
"$checker"

printf 'const frozenDonor = "%s/%s"\n' "$donor_prefix" "$donor_name" > "$script_fixture"
git -C "$repo_root" check-ignore -q "$script_fixture" && {
  echo "ordinary script fixture is ignored; boundary test would not exercise rg" >&2
  exit 1
}
if "$checker" >/dev/null 2>&1; then
  echo "AI Assistant donor checker accepted an ordinary script reference" >&2
  exit 1
fi
rm -f "$script_fixture"
"$checker"

printf 'const frozenDonor = "%s/%s"\n' "$donor_prefix" "$donor_name" > "$runtime_fixture/runtime_reference.go"
git -C "$repo_root" check-ignore -q "$runtime_fixture/runtime_reference.go" && {
  echo "runtime fixture is ignored; boundary test would not exercise rg" >&2
  exit 1
}
if "$checker" >/dev/null 2>&1; then
  echo "AI Assistant donor checker accepted a runtime reference" >&2
  exit 1
fi

echo "AI Assistant donor manifest boundary self-test passed"
